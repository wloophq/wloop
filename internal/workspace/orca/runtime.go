package orca

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/workspace"
)

// Runtime calls Orca's runtime over its local socket, as Orca's own CLI does.
type Runtime struct {
	// UserDataPath holds orca-runtime.json; "" resolves ORCA_USER_DATA_PATH, then the
	// platform default (darwin: ~/Library/Application Support/orca; else
	// $XDG_CONFIG_HOME/orca or ~/.config/orca). A test binary may resolve it only
	// with LiveVar set: Orca exports ORCA_USER_DATA_PATH into its own terminals.
	UserDataPath string
	// Timeout bounds one call; 0 is 60 s. A keepalive frame restarts it. terminal.wait
	// uses params.timeoutMs + 10 s when that is larger.
	Timeout time.Duration
	// Dial is net.Dialer.DialContext on "unix"; tests replace it.
	Dial func(ctx context.Context, network, addr string) (net.Conn, error)
	// Observe receives one call's method, duration, response bytes and error.
	Observe func(method string, d time.Duration, bytes int, err error)
}

var _ RPC = (*Runtime)(nil)

// ErrProtocol is a runtime answer this client cannot read: Orca's protocol drifted.
var ErrProtocol = errors.New("orca runtime protocol mismatch")

const (
	KindUnavailable = "unavailable"
	KindProtocol    = "protocol"
)

// RuntimeError is a failed runtime call. It matches workspace.ErrUnreachable when Kind
// is KindUnavailable and ErrProtocol when Kind is KindProtocol.
type RuntimeError struct {
	Kind   string
	Method string
	Err    error
}

func (e *RuntimeError) Error() string {
	return fmt.Sprintf("orca runtime %s: %s: %v", e.Method, e.Kind, e.Err)
}

func (e *RuntimeError) Unwrap() []error {
	switch e.Kind {
	case KindUnavailable:
		return []error{workspace.ErrUnreachable, e.Err}
	case KindProtocol:
		return []error{ErrProtocol, e.Err}
	}
	return []error{e.Err}
}

// LiveVar set to "1" lets a test binary discover the user's running Orca runtime.
const LiveVar = "LOOP_LIVE_ORCA"

var errDiscoveryInTest = errors.New("a test may not discover the user's Orca runtime: set Runtime.UserDataPath, or " + LiveVar + "=1")

const (
	defaultTimeout = 60 * time.Second
	waitMargin     = 10 * time.Second
	metadataFile   = "orca-runtime.json"
)

type runtimeTransport struct {
	Kind     string `json:"kind"`
	Endpoint string `json:"endpoint"`
}

type runtimeMetadata struct {
	RuntimeID  string             `json:"runtimeId"`
	AuthToken  string             `json:"authToken"`
	Transports []runtimeTransport `json:"transports"`
	Transport  *runtimeTransport  `json:"transport"`
}

func (m runtimeMetadata) socket() string {
	for _, t := range m.Transports {
		if t.Kind == "unix" {
			return t.Endpoint
		}
	}
	if m.Transports == nil && m.Transport != nil && m.Transport.Kind == "unix" {
		return m.Transport.Endpoint
	}
	return ""
}

type request struct {
	ID        string `json:"id"`
	AuthToken string `json:"authToken"`
	Method    string `json:"method"`
	Params    any    `json:"params,omitempty"`
}

type frame struct {
	Keepalive bool   `json:"_keepalive"`
	ID        string `json:"id"`
	OK        *bool  `json:"ok"`
	Meta      *struct {
		RuntimeID string `json:"runtimeId"`
	} `json:"_meta"`
}

// Call returns the response frame as bytes, in the envelope decode() reads. Errors:
// *RuntimeError with Kind Unavailable wraps workspace.ErrUnreachable (no or incomplete
// metadata, dial refused, closed before a frame, no answer in time, runtimeId changed);
// Kind Protocol wraps ErrProtocol (unparseable frame, id mismatch, success frame without
// _meta.runtimeId); a context error is returned as is. The auth token never appears in
// an error.
func (r *Runtime) Call(ctx context.Context, method string, params any) ([]byte, error) {
	start := time.Now()
	raw, n, err := r.call(ctx, method, params)
	if r.Observe != nil {
		r.Observe(method, time.Since(start), n, err)
	}
	return raw, err
}

func (r *Runtime) call(ctx context.Context, method string, params any) ([]byte, int, error) {
	unavailable := func(err error) error { return &RuntimeError{Kind: KindUnavailable, Method: method, Err: err} }
	protocol := func(err error) error { return &RuntimeError{Kind: KindProtocol, Method: method, Err: err} }

	if r.UserDataPath == "" && testing.Testing() && os.Getenv(LiveVar) != "1" {
		return nil, 0, unavailable(errDiscoveryInTest)
	}
	meta, err := readRuntimeMetadata(r.userDataPath())
	if err != nil {
		return nil, 0, unavailable(err)
	}
	id, err := newRequestID()
	if err != nil {
		return nil, 0, err
	}
	line, err := json.Marshal(request{ID: id, AuthToken: meta.AuthToken, Method: method, Params: params}) //nolint:gosec // G117: the Orca runtime authenticates each request by this token
	if err != nil {
		return nil, 0, fmt.Errorf("orca runtime %s: encoding params: %w", method, err)
	}
	timeout := r.timeout(method, params)

	dial := r.Dial
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	conn, err := dial(ctx, "unix", meta.socket())
	if err != nil {
		if ctx.Err() != nil {
			return nil, 0, ctx.Err()
		}
		return nil, 0, unavailable(err)
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Unix(1, 0)) })
	defer stop()

	// A refresh can overwrite the past deadline the AfterFunc set, so ctx is checked after each.
	refresh := func() error {
		_ = conn.SetDeadline(time.Now().Add(timeout))
		return ctx.Err()
	}
	if err := refresh(); err != nil {
		return nil, 0, err
	}
	if _, err := conn.Write(append(line, '\n')); err != nil {
		return nil, 0, r.ioError(ctx, err, timeout, unavailable)
	}
	reader := bufio.NewReader(conn)
	read := 0
	for {
		raw, err := reader.ReadBytes('\n')
		read += len(raw)
		if err != nil {
			return nil, read, r.ioError(ctx, err, timeout, unavailable)
		}
		if len(bytes.TrimSpace(raw)) == 0 {
			continue
		}
		var f frame
		if json.Unmarshal(raw, &f) != nil {
			return nil, read, protocol(errors.New("unparseable response frame"))
		}
		if f.Keepalive {
			if err := refresh(); err != nil {
				return nil, read, err
			}
			continue
		}
		switch {
		case f.OK == nil:
			return nil, read, protocol(errors.New("response frame without ok"))
		case f.ID != id:
			return nil, read, protocol(errors.New("mismatched response id"))
		case f.Meta != nil && f.Meta.RuntimeID != "" && f.Meta.RuntimeID != meta.RuntimeID:
			return nil, read, unavailable(errors.New("runtime changed while the request was in flight"))
		case *f.OK && (f.Meta == nil || f.Meta.RuntimeID == ""):
			return nil, read, protocol(errors.New("success frame without _meta.runtimeId"))
		}
		return raw, read, nil
	}
}

func (r *Runtime) ioError(ctx context.Context, err error, timeout time.Duration, unavailable func(error) error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return unavailable(fmt.Errorf("no answer within %s: %w", timeout, os.ErrDeadlineExceeded))
	}
	return unavailable(fmt.Errorf("connection closed before a response frame: %w", err))
}

func (r *Runtime) timeout(method string, params any) time.Duration {
	t := r.Timeout
	if t == 0 {
		t = defaultTimeout
	}
	if method != "terminal.wait" {
		return t
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return t
	}
	var p struct {
		TimeoutMs float64 `json:"timeoutMs"`
	}
	if json.Unmarshal(raw, &p) != nil || p.TimeoutMs <= 0 {
		return t
	}
	return max(t, time.Duration(p.TimeoutMs*float64(time.Millisecond))+waitMargin)
}

func (r *Runtime) userDataPath() string {
	if r.UserDataPath != "" {
		return r.UserDataPath
	}
	home, _ := os.UserHomeDir()
	return defaultUserDataPath(runtime.GOOS, os.Getenv, home)
}

func defaultUserDataPath(goos string, getenv func(string) string, home string) string {
	if p := getenv("ORCA_USER_DATA_PATH"); p != "" {
		return p
	}
	if goos == "darwin" {
		return filepath.Join(home, "Library", "Application Support", "orca")
	}
	if xdg := getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "orca")
	}
	return filepath.Join(home, ".config", "orca")
}

// readRuntimeMetadata errors never quote the file, which holds the auth token.
func readRuntimeMetadata(dir string) (runtimeMetadata, error) {
	path := filepath.Join(dir, metadataFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		return runtimeMetadata{}, fmt.Errorf("no runtime metadata at %s; is Orca running?", path)
	}
	var meta runtimeMetadata
	if json.Unmarshal(raw, &meta) != nil {
		return runtimeMetadata{}, fmt.Errorf("unreadable runtime metadata at %s", path)
	}
	var missing []string
	for field, empty := range map[string]bool{"authToken": meta.AuthToken == "", "runtimeId": meta.RuntimeID == "", "unix transport": meta.socket() == ""} {
		if empty {
			missing = append(missing, field)
		}
	}
	if len(missing) > 0 {
		slices.Sort(missing)
		return runtimeMetadata{}, fmt.Errorf("incomplete runtime metadata at %s: no %s", path, strings.Join(missing, ", "))
	}
	return meta, nil
}

func newRequestID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
