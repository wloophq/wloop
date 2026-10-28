// Package mcp serves the loop's MCP endpoint: streamable HTTP on localhost with
// JSON responses only. The X-Loop-Session header names the calling terminal
// session when it runs in the workspace manager; tools that record a turn
// need it, the others work without it.
package mcp

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/termfmt"
)

const (
	maxBody         = 512 << 10
	sessionIDHeader = "Mcp-Session-Id"
	latestVersion   = "2025-06-18"
)

var versions = []string{latestVersion, "2025-03-26"}

const (
	codeParse          = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternal       = -32603
)

type Turns interface {
	MCPSeen(ctx context.Context, handle string) error
	RecordTurnEnd(ctx context.Context, r turns.RecordRequest) (turns.Recorded, error)
	// SessionOfHandle is the loop's session of a process handle; ok is false
	// for one no session took.
	SessionOfHandle(ctx context.Context, handle string) (string, bool, error)
}

// Server is the MCP endpoint. Its session ids live in memory only: after a
// restart a client's old id gets 404 and the client initializes again.
type Server struct {
	Turns   Turns
	Port    int
	Version string
	// Log notes each initialize; nil logs nothing.
	Log func(format string, args ...any)
	// Plugins are the tools the plugins' manifests declare.
	Plugins []PluginTool
	Inbox   Inbox // set by internal/cli/serve_messages.go; nil hides inbox_post and inbox_read
	// Texts are the tools' descriptions, the profile's tools.toml; nil
	// describes none.
	Texts func() map[string]ToolText
	// Bundled are the bundled profile's texts, which an answer falls back
	// to when the profile's text fails; nil falls back to none.
	Bundled func() map[string]ToolText
	mu      sync.Mutex
	ids     map[string]string // Mcp-Session-Id to handle ("" when none), in memory only
	// byHandle keeps one id per handle: initialize replaces the old one.
	byHandle map[string]string
	logged   map[string]bool // what logOnce logged

	// Key is the per-install secret each request carries in
	// api.MCPKeyHeader; the daemon always sets it, nil checks nothing.
	Key func() string
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/mcp" {
		http.NotFound(w, r)
		return
	}
	if !s.localHost(r.Host) || r.Header.Get("Origin") != "" {
		http.Error(w, brand.Name+"'s MCP endpoint answers only local agents", http.StatusForbidden)
		return
	}
	if s.Key != nil && !keyMatches(s.Key(), r.Header.Get(api.MCPKeyHeader)) {
		http.Error(w, errKey, http.StatusUnauthorized)
		return
	}
	raw := r.Header.Get(api.SessionHeader)
	handle, herr := api.SessionHandle(raw)
	if herr != nil && raw != "" && s.Log != nil {
		s.Log("mcp: %s %q is no process handle", api.SessionHeader, termfmt.OneLine(raw, 160))
	}
	sid := r.Header.Get(sessionIDHeader)
	switch r.Method {
	case http.MethodPost:
	case http.MethodDelete:
		switch known, ok := s.handleOf(sid); {
		case !ok:
			http.Error(w, "unknown MCP session", http.StatusNotFound)
		case known != handle:
			http.Error(w, api.SessionHeader+" is not this MCP session's", http.StatusBadRequest)
		default:
			s.forget(sid)
			w.WriteHeader(http.StatusNoContent)
		}
		return
	default:
		w.Header().Set("Allow", "POST, DELETE")
		http.Error(w, brand.Name+"'s MCP endpoint streams nothing; POST JSON-RPC requests", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			http.Error(w, "request body over 512 KiB", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var req request
	if err := json.Unmarshal(body, &req); err != nil {
		code := codeParse
		if t := bytes.TrimSpace(body); len(t) > 0 && t[0] == '[' {
			code = codeInvalidRequest
		}
		writeJSON(w, http.StatusBadRequest, response{JSONRPC: "2.0", ID: null, Error: &rpcError{code, "one JSON-RPC 2.0 request object per POST"}})
		return
	}
	if req.JSONRPC != "2.0" {
		writeJSON(w, http.StatusBadRequest, response{JSONRPC: "2.0", ID: idOrNull(req.ID), Error: &rpcError{codeInvalidRequest, `jsonrpc must be "2.0"`}})
		return
	}
	if sid != "" {
		known, ok := s.handleOf(sid)
		if !ok {
			http.Error(w, "unknown MCP session; initialize again", http.StatusNotFound)
			return
		}
		if known != handle {
			writeJSON(w, http.StatusBadRequest, response{JSONRPC: "2.0", ID: idOrNull(req.ID), Error: &rpcError{codeInvalidRequest, api.SessionHeader + " changed within the MCP session"}})
			return
		}
	}
	if len(req.ID) == 0 || req.Method == "" {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	resp := response{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		result, newID, rerr := s.initialize(r.Context(), handle, req.Params)
		if newID != "" {
			w.Header().Set(sessionIDHeader, newID)
		}
		resp.Result, resp.Error = result, rerr
	case "ping":
		resp.Result = struct{}{}
	case "tools/list", "tools/call":
		if sid == "" {
			writeJSON(w, http.StatusBadRequest, response{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{codeInvalidRequest, sessionIDHeader + " is required; initialize first"}})
			return
		}
		if req.Method == "tools/list" {
			resp.Result = map[string]any{"tools": s.listTools()}
		} else {
			resp.Result, resp.Error = s.callTool(r.Context(), handle, req.Params)
		}
	default:
		resp.Error = &rpcError{codeMethodNotFound, "method not found: " + req.Method}
	}
	writeJSON(w, http.StatusOK, resp)
}

var null = json.RawMessage("null")

func idOrNull(id json.RawMessage) json.RawMessage {
	if len(id) == 0 {
		return null
	}
	return id
}

func (s *Server) localHost(host string) bool {
	h, port, err := net.SplitHostPort(host)
	if err != nil || port != strconv.Itoa(s.Port) {
		return false
	}
	return h == "127.0.0.1" || h == "localhost"
}

func (s *Server) initialize(ctx context.Context, handle string, params json.RawMessage) (any, string, *rpcError) {
	var p struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, "", &rpcError{codeInvalidParams, "initialize params: " + err.Error()}
		}
	}
	if handle != "" {
		if err := s.Turns.MCPSeen(ctx, handle); err != nil {
			return nil, "", &rpcError{codeInternal, brand.Name + " could not note this session: " + err.Error()}
		}
		if s.Log != nil {
			s.Log("mcp_seen %s", handle)
		}
	}
	version := latestVersion
	for _, v := range versions {
		if p.ProtocolVersion == v {
			version = v
		}
	}
	id := rand.Text()
	s.mu.Lock()
	if s.ids == nil {
		s.ids, s.byHandle = map[string]string{}, map[string]string{}
	}
	s.ids[id] = handle
	if handle != "" {
		delete(s.ids, s.byHandle[handle])
		s.byHandle[handle] = id
	}
	s.mu.Unlock()
	return map[string]any{
		"protocolVersion": version,
		"capabilities":    map[string]any{"tools": map[string]bool{"listChanged": false}},
		"serverInfo":      map[string]string{"name": brand.Name, "version": s.Version},
	}, id, nil
}

func (s *Server) handleOf(id string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h, ok := s.ids[id]
	return h, ok
}

func (s *Server) forget(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if h, ok := s.ids[id]; ok {
		delete(s.ids, id)
		delete(s.byHandle, h)
	}
}

type registered struct {
	tool        tool
	needsHandle bool
	call        func(ctx context.Context, handle string, args json.RawMessage) any
}

// tools is the registry of the tools this server lists and calls; a nil
// Server field leaves its tool out.
func (s *Server) tools() []registered {
	r := []registered{{turnEndTool(), true, s.turnEnd}}
	if s.Inbox != nil {
		r = append(r, registered{inboxPostTool(), true, s.inboxPost}, registered{inboxReadTool(), true, s.inboxRead})
	}
	return append(r, s.pluginTools()...)
}

func (s *Server) listTools() []tool {
	var texts map[string]ToolText
	if s.Texts != nil {
		texts = s.Texts()
	}
	var out []tool
	for _, r := range s.tools() {
		t := r.tool
		s.describe(&t, texts[t.Name])
		out = append(out, t)
	}
	return out
}

// session is the loop's session of the request's process handle, "" for none.
func (s *Server) session(ctx context.Context, handle string) (string, error) {
	if handle == "" || s.Turns == nil {
		return "", nil
	}
	id, ok, err := s.Turns.SessionOfHandle(ctx, handle)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", api.ErrUnknownSession
	}
	return id, nil
}

func (s *Server) callTool(ctx context.Context, handle string, params json.RawMessage) (any, *rpcError) {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &rpcError{codeInvalidParams, "tools/call params: " + err.Error()}
	}
	var names []string
	for _, r := range s.tools() {
		if r.tool.Name != p.Name {
			names = append(names, r.tool.Name)
			continue
		}
		if r.needsHandle && handle == "" {
			return s.answer(ctx, p.Name, "refused", fields{"Error": api.ErrSessionHandle.Error()}, true), nil
		}
		session, err := s.session(ctx, handle)
		if err != nil && (r.needsHandle || !errors.Is(err, api.ErrUnknownSession)) {
			return s.answer(ctx, p.Name, "refused", fields{"Error": err.Error()}, true), nil
		}
		return r.call(s.withTexts(ctx), session, p.Arguments), nil
	}
	return nil, &rpcError{codeInvalidParams, fmt.Sprintf("unknown tool %q; "+brand.Name+" has %s", p.Name, strings.Join(names, ", "))}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
