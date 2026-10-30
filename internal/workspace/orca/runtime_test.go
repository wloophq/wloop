package orca

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/workspace"
)

const (
	fakeToken     = "secret-token-7f3a"
	fakeRuntimeID = "runtime-1"
)

type fakeRequest struct {
	line   string
	fields map[string]any
}

func (r fakeRequest) id() string     { s, _ := r.fields["id"].(string); return s }
func (r fakeRequest) method() string { s, _ := r.fields["method"].(string); return s }

type fakeRuntime struct {
	dir string

	mu       sync.Mutex
	requests []fakeRequest
}

func (f *fakeRuntime) seen() []fakeRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeRequest(nil), f.requests...)
}

func startFakeRuntime(t *testing.T, serve func(conn net.Conn, req fakeRequest)) *fakeRuntime {
	t.Helper()
	return startFakeRuntimeAt(t, t.TempDir(), fakeRuntimeID, serve)
}

func startFakeRuntimeAt(t *testing.T, dir, runtimeID string, serve func(conn net.Conn, req fakeRequest)) *fakeRuntime {
	t.Helper()
	sockDir, err := os.MkdirTemp("", "rt")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(sockDir) })
	sock := filepath.Join(sockDir, "s")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeRuntime{dir: dir}
	var wg sync.WaitGroup
	var connsMu sync.Mutex
	var conns []net.Conn
	t.Cleanup(func() {
		ln.Close()
		connsMu.Lock()
		for _, c := range conns {
			c.Close()
		}
		connsMu.Unlock()
		wg.Wait()
	})
	writeMetadata(t, f.dir, map[string]any{
		"runtimeId":  runtimeID,
		"authToken":  fakeToken,
		"transports": []map[string]string{{"kind": "unix", "endpoint": sock}},
	})
	wg.Go(func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			connsMu.Lock()
			conns = append(conns, conn)
			connsMu.Unlock()
			wg.Go(func() {
				defer conn.Close()
				line, err := bufio.NewReader(conn).ReadString('\n')
				if err != nil {
					return
				}
				req := fakeRequest{line: line}
				if err := json.Unmarshal([]byte(line), &req.fields); err != nil {
					return
				}
				f.mu.Lock()
				f.requests = append(f.requests, req)
				f.mu.Unlock()
				serve(conn, req)
			})
		}
	})
	return f
}

func writeMetadata(t *testing.T, dir string, meta map[string]any) {
	t.Helper()
	raw, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "orca-runtime.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func okFrame(id, runtimeID, result string) string {
	return fmt.Sprintf(`{"id":%q,"ok":true,"result":%s,"_meta":{"runtimeId":%q}}`+"\n", id, result, runtimeID)
}

func answerOK(result string) func(net.Conn, fakeRequest) {
	return func(conn net.Conn, req fakeRequest) {
		fmt.Fprint(conn, okFrame(req.id(), fakeRuntimeID, result))
	}
}

func TestRuntimeSendsOneLineWithTokenAndParams(t *testing.T) {
	f := startFakeRuntime(t, answerOK(`{"n":1}`))
	r := &Runtime{UserDataPath: f.dir}
	raw, err := r.Call(context.Background(), "worktree.list", map[string]any{"limit": 1000})
	if err != nil {
		t.Fatal(err)
	}
	var out struct{ N int }
	if _, err := decode(raw, &out, []string{"rpc"}); err != nil || out.N != 1 {
		t.Fatalf("decode = %+v, %v", out, err)
	}
	reqs := f.seen()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d", len(reqs))
	}
	req := reqs[0]
	if strings.Count(req.line, "\n") != 1 || !strings.HasSuffix(req.line, "\n") {
		t.Fatalf("request is not one line: %q", req.line)
	}
	if req.fields["authToken"] != fakeToken || req.method() != "worktree.list" || req.id() == "" {
		t.Fatalf("request = %v", req.fields)
	}
	if p, _ := req.fields["params"].(map[string]any); p["limit"] != float64(1000) {
		t.Fatalf("params = %v", req.fields["params"])
	}
}

type deadlineConn struct {
	net.Conn
	mu        sync.Mutex
	deadlines []time.Time
}

func (c *deadlineConn) SetDeadline(t time.Time) error {
	c.mu.Lock()
	c.deadlines = append(c.deadlines, t)
	c.mu.Unlock()
	return c.Conn.SetDeadline(t)
}

func TestRuntimeSkipsKeepalivesAndRefreshesTimeout(t *testing.T) {
	const keepalives = 5
	f := startFakeRuntime(t, func(conn net.Conn, req fakeRequest) {
		for range keepalives {
			time.Sleep(100 * time.Millisecond)
			fmt.Fprint(conn, `{"_keepalive":true}`+"\n")
		}
		time.Sleep(100 * time.Millisecond)
		fmt.Fprint(conn, okFrame(req.id(), fakeRuntimeID, `{}`))
	})
	var conn *deadlineConn
	r := &Runtime{UserDataPath: f.dir, Timeout: 300 * time.Millisecond, Dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
		c, err := (&net.Dialer{}).DialContext(ctx, network, addr)
		conn = &deadlineConn{Conn: c}
		return conn, err
	}}
	if _, err := r.Call(context.Background(), "worktree.ps", nil); err != nil {
		t.Fatal(err)
	}
	conn.mu.Lock()
	defer conn.mu.Unlock()
	if len(conn.deadlines) != 1+keepalives {
		t.Fatalf("deadlines set = %d, want %d", len(conn.deadlines), 1+keepalives)
	}
	for i := 1; i < len(conn.deadlines); i++ {
		if !conn.deadlines[i].After(conn.deadlines[i-1]) {
			t.Fatalf("deadline %d does not move forward: %v", i, conn.deadlines)
		}
	}
}

func TestRuntimeCancelReturnsContextError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	const keepAliveFor = 20 * time.Second
	f := startFakeRuntime(t, func(conn net.Conn, _ fakeRequest) {
		cancel()
		for stop := time.Now().Add(keepAliveFor); time.Now().Before(stop); {
			if _, err := fmt.Fprint(conn, `{"_keepalive":true}`+"\n"); err != nil {
				return
			}
		}
	})
	start := time.Now()
	_, err := (&Runtime{UserDataPath: f.dir, Timeout: 5 * time.Second}).Call(ctx, "terminal.wait", map[string]any{"timeoutMs": 600000})
	var rerr *RuntimeError
	if !errors.Is(err, context.Canceled) || errors.As(err, &rerr) {
		t.Fatalf("err = %v", err)
	}
	if d := time.Since(start); d >= keepAliveFor {
		t.Fatalf("returned after %s, when the runtime stopped answering", d)
	}
}

func TestRuntimeRejectsMismatchedID(t *testing.T) {
	f := startFakeRuntime(t, func(conn net.Conn, _ fakeRequest) {
		fmt.Fprint(conn, okFrame("other-id", fakeRuntimeID, `{}`))
	})
	_, err := (&Runtime{UserDataPath: f.dir}).Call(context.Background(), "status.get", nil)
	var rerr *RuntimeError
	if !errors.As(err, &rerr) || rerr.Kind != KindProtocol || !errors.Is(err, ErrProtocol) {
		t.Fatalf("err = %v", err)
	}
	if errors.Is(err, workspace.ErrUnreachable) {
		t.Fatalf("protocol error matches ErrUnreachable: %v", err)
	}
}

func TestRuntimeRuntimeIDChangedIsUnavailable(t *testing.T) {
	f := startFakeRuntime(t, func(conn net.Conn, req fakeRequest) {
		fmt.Fprint(conn, okFrame(req.id(), "runtime-2", `{}`))
	})
	_, err := (&Runtime{UserDataPath: f.dir}).Call(context.Background(), "status.get", nil)
	var rerr *RuntimeError
	if !errors.As(err, &rerr) || rerr.Kind != KindUnavailable || !errors.Is(err, workspace.ErrUnreachable) {
		t.Fatalf("err = %v", err)
	}
}

func TestRuntimeCloseBeforeFrameIsUnavailable(t *testing.T) {
	f := startFakeRuntime(t, func(conn net.Conn, _ fakeRequest) {
		fmt.Fprint(conn, `{"_keepalive":true}`+"\n")
	})
	_, err := (&Runtime{UserDataPath: f.dir}).Call(context.Background(), "status.get", nil)
	if !errors.Is(err, workspace.ErrUnreachable) || errors.Is(err, ErrProtocol) {
		t.Fatalf("err = %v", err)
	}
}

func TestRuntimeMissingMetadataIsUnreachable(t *testing.T) {
	dial := func(context.Context, string, string) (net.Conn, error) {
		t.Error("dialed without metadata")
		return nil, errors.New("no dial")
	}
	empty := t.TempDir()
	noToken := t.TempDir()
	writeMetadata(t, noToken, map[string]any{"runtimeId": "r", "transports": []map[string]string{{"kind": "unix", "endpoint": "/nowhere"}}})
	noRuntimeID := t.TempDir()
	writeMetadata(t, noRuntimeID, map[string]any{"authToken": "x", "transports": []map[string]string{{"kind": "unix", "endpoint": "/nowhere"}}})
	for dir, want := range map[string]string{empty: "no runtime metadata", noToken: "no authToken", noRuntimeID: "no runtimeId"} {
		_, err := (&Runtime{UserDataPath: dir, Dial: dial}).Call(context.Background(), "status.get", nil)
		if !errors.Is(err, workspace.ErrUnreachable) || err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("want %q: err = %v", want, err)
		}
	}
}

func TestRuntimeUserDataPathFromEnv(t *testing.T) {
	env := func(vars map[string]string) func(string) string {
		return func(k string) string { return vars[k] }
	}
	for _, c := range []struct {
		goos string
		vars map[string]string
		want string
	}{
		{"darwin", map[string]string{"ORCA_USER_DATA_PATH": "/custom"}, "/custom"},
		{"linux", map[string]string{"ORCA_USER_DATA_PATH": "/custom", "XDG_CONFIG_HOME": "/xdg"}, "/custom"},
		{"darwin", nil, "/home/u/Library/Application Support/orca"},
		{"linux", map[string]string{"XDG_CONFIG_HOME": "/xdg"}, "/xdg/orca"},
		{"linux", nil, "/home/u/.config/orca"},
	} {
		if got := defaultUserDataPath(c.goos, env(c.vars), "/home/u"); got != c.want {
			t.Errorf("%s %v: %s, want %s", c.goos, c.vars, got, c.want)
		}
	}

	f := startFakeRuntime(t, answerOK(`{}`))
	t.Setenv("ORCA_USER_DATA_PATH", f.dir)
	t.Setenv(LiveVar, "1")
	if _, err := (&Runtime{}).Call(context.Background(), "status.get", nil); err != nil {
		t.Fatal(err)
	}
	if len(f.seen()) != 1 {
		t.Fatalf("requests = %d", len(f.seen()))
	}
}

func TestRuntimeWaitTimeoutWidens(t *testing.T) {
	f := startFakeRuntime(t, func(conn net.Conn, req fakeRequest) {
		if req.method() != "terminal.wait" {
			_, _ = io.Copy(io.Discard, conn)
			return
		}
		time.Sleep(150 * time.Millisecond)
		fmt.Fprint(conn, okFrame(req.id(), fakeRuntimeID, `{}`))
	})
	r := &Runtime{UserDataPath: f.dir, Timeout: 100 * time.Millisecond}
	if _, err := r.Call(context.Background(), "terminal.wait", map[string]any{"terminal": "t1", "for": "tui-idle", "timeoutMs": 200}); err != nil {
		t.Fatalf("terminal.wait: %v", err)
	}
	_, err := r.Call(context.Background(), "terminal.read", map[string]any{"terminal": "t1", "timeoutMs": 200})
	if !errors.Is(err, workspace.ErrUnreachable) || !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("terminal.read past the timeout: %v", err)
	}
}

func TestRuntimeErrorNeverCarriesToken(t *testing.T) {
	replies := map[string]func(net.Conn, fakeRequest){
		"echo":    func(conn net.Conn, req fakeRequest) { fmt.Fprint(conn, req.line) },
		"garbage": func(conn net.Conn, req fakeRequest) { fmt.Fprint(conn, "not json "+req.line) },
		"badid":   func(conn net.Conn, _ fakeRequest) { fmt.Fprint(conn, okFrame(fakeToken, fakeRuntimeID, `{}`)) },
		"close":   func(net.Conn, fakeRequest) {},
		"noMeta": func(conn net.Conn, req fakeRequest) {
			fmt.Fprintf(conn, `{"id":%q,"ok":true,"result":%q}`+"\n", req.id(), fakeToken)
		},
		"restart":  func(conn net.Conn, req fakeRequest) { fmt.Fprint(conn, okFrame(req.id(), fakeToken, `{}`)) },
		"timesout": func(net.Conn, fakeRequest) { time.Sleep(100 * time.Millisecond) },
	}
	for name, reply := range replies {
		f := startFakeRuntime(t, reply)
		var observed error
		r := &Runtime{UserDataPath: f.dir, Timeout: 50 * time.Millisecond, Observe: func(_ string, _ time.Duration, _ int, err error) { observed = err }}
		_, err := r.Call(context.Background(), "status.get", nil)
		if err == nil {
			t.Errorf("%s: no error", name)
			continue
		}
		if strings.Contains(err.Error(), fakeToken) || strings.Contains(fmt.Sprintf("%+v", err), fakeToken) {
			t.Errorf("%s: error carries the token: %v", name, err)
		}
		if observed != err {
			t.Errorf("%s: observed %v, returned %v", name, observed, err)
		}
	}
	bad := t.TempDir()
	if err := os.WriteFile(filepath.Join(bad, "orca-runtime.json"), []byte(`{"authToken":"`+fakeToken+`",`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (&Runtime{UserDataPath: bad}).Call(context.Background(), "status.get", nil); err == nil || strings.Contains(err.Error(), fakeToken) {
		t.Errorf("broken metadata: %v", err)
	}
}

type countingRunner struct{ calls int }

func (c *countingRunner) Run(context.Context, ...string) ([]byte, error) {
	c.calls++
	return nil, errors.New("no process expected")
}

func TestCreateFromPRUsesRuntime(t *testing.T) {
	f := startFakeRuntime(t, func(conn net.Conn, req fakeRequest) {
		switch req.method() {
		case "worktree.resolvePrBase":
			fmt.Fprint(conn, okFrame(req.id(), fakeRuntimeID, `{"baseBranch":"abc","branchNameOverride":"feat/x"}`))
		case "worktree.create":
			fmt.Fprint(conn, okFrame(req.id(), fakeRuntimeID, `{"worktree":{"id":"r1::/wt/x","path":"/wt/x","displayName":"REVIEW feat/x","branch":"refs/heads/feat/x"},"agentTerminalHandle":"term-1"}`))
		}
	})
	runner := &countingRunner{}
	m := &Manager{R: runner, RPC: &Runtime{UserDataPath: f.dir}}
	ws, handle, err := m.Create(context.Background(), workspace.CreateRequest{
		RepoID: "r1", Name: "REVIEW feat/x", Unit: prUnit(7), Agent: "claude", Prompt: "/skill url",
		Checkout: workspace.Checkout{Branch: "feat/x", Base: "main"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if ws.Branch != "feat/x" || handle != "term-1" {
		t.Fatalf("ws = %+v, handle = %q", ws, handle)
	}
	var methods []string
	for _, req := range f.seen() {
		methods = append(methods, req.method())
	}
	if strings.Join(methods, ",") != "worktree.resolvePrBase,worktree.create" {
		t.Fatalf("runtime calls = %v", methods)
	}
	if runner.calls != 0 {
		t.Fatalf("processes = %d", runner.calls)
	}
}

func TestRuntimeFraming(t *testing.T) {
	big := `{"blob":"` + strings.Repeat("x", 300<<10) + `"}`
	for name, c := range map[string]struct {
		serve  func(net.Conn, fakeRequest)
		result string
	}{
		"split": {func(conn net.Conn, req fakeRequest) {
			frame := okFrame(req.id(), fakeRuntimeID, `{"n":1}`)
			fmt.Fprint(conn, frame[:10])
			time.Sleep(20 * time.Millisecond)
			fmt.Fprint(conn, frame[10:])
		}, `{"n":1}`},
		"large": {answerOK(big), big},
		"blank lines": {func(conn net.Conn, req fakeRequest) {
			fmt.Fprint(conn, "\n  \n"+`{"_keepalive":true}`+"\n\r\n"+okFrame(req.id(), fakeRuntimeID, `{"n":2}`))
		}, `{"n":2}`},
	} {
		f := startFakeRuntime(t, c.serve)
		raw, err := (&Runtime{UserDataPath: f.dir}).Call(context.Background(), "worktree.ps", nil)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		var env struct{ Result json.RawMessage }
		if err := json.Unmarshal(raw, &env); err != nil || string(env.Result) != c.result {
			t.Errorf("%s: result %.80s, err %v", name, env.Result, err)
		}
	}

	f := startFakeRuntime(t, func(conn net.Conn, req fakeRequest) {
		fmt.Fprint(conn, okFrame(req.id(), fakeRuntimeID, fmt.Sprintf(`{"id":%q}`, req.id())))
	})
	r := &Runtime{UserDataPath: f.dir}
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			raw, err := r.Call(context.Background(), "status.get", nil)
			if err != nil {
				t.Error(err)
				return
			}
			var env struct {
				ID     string
				Result struct{ ID string }
			}
			if err := json.Unmarshal(raw, &env); err != nil || env.ID != env.Result.ID {
				t.Errorf("frame %s, err %v", raw, err)
			}
		})
	}
	wg.Wait()
	if len(f.seen()) != 20 {
		t.Fatalf("requests = %d", len(f.seen()))
	}
}

func TestRuntimeFollowsRestartedRuntime(t *testing.T) {
	dir := t.TempDir()
	first := startFakeRuntimeAt(t, dir, "runtime-1", func(conn net.Conn, req fakeRequest) {
		fmt.Fprint(conn, okFrame(req.id(), "runtime-1", `{}`))
	})
	r := &Runtime{UserDataPath: dir}
	if _, err := r.Call(context.Background(), "status.get", nil); err != nil {
		t.Fatal(err)
	}
	second := startFakeRuntimeAt(t, dir, "runtime-2", func(conn net.Conn, req fakeRequest) {
		fmt.Fprint(conn, okFrame(req.id(), "runtime-2", `{}`))
	})
	if _, err := r.Call(context.Background(), "status.get", nil); err != nil {
		t.Fatalf("after restart: %v", err)
	}
	if len(first.seen()) != 1 || len(second.seen()) != 1 {
		t.Fatalf("first saw %d, second saw %d", len(first.seen()), len(second.seen()))
	}
}

func TestNewManagerInATestNeverReachesTheRuntime(t *testing.T) {
	f := startFakeRuntime(t, answerOK(`{"worktrees":[]}`))
	home := t.TempDir()
	for _, dir := range []string{filepath.Join(home, "Library", "Application Support", "orca"), filepath.Join(home, ".config", "orca")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		f2 := startFakeRuntimeAt(t, dir, "runtime-home", answerOK(`{"worktrees":[]}`))
		t.Cleanup(func() {
			if n := len(f2.seen()); n != 0 {
				t.Errorf("the runtime under HOME got %d requests", n)
			}
		})
	}
	t.Setenv(LiveVar, "")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	for _, userData := range []string{f.dir, ""} {
		t.Setenv("ORCA_USER_DATA_PATH", userData)
		m := New()
		m.R = &countingRunner{}
		_, err := m.List(context.Background())
		if !errors.Is(err, workspace.ErrUnreachable) || !strings.Contains(err.Error(), LiveVar) {
			t.Fatalf("ORCA_USER_DATA_PATH=%q: err = %v", userData, err)
		}
		if n := m.R.(*countingRunner).calls; n != 0 {
			t.Fatalf("the CLI ran %d times", n)
		}
	}
	if n := len(f.seen()); n != 0 {
		t.Fatalf("the runtime at ORCA_USER_DATA_PATH got %d requests", n)
	}
}
