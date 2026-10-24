// Package slacktest is a fake Slack Web API for tests.
package slacktest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// Response lets a handler set the HTTP status and headers of its answer.
// A zero Status means 200.
type Response struct {
	Status int
	Header http.Header
	Body   any
}

type Server struct {
	// URL is the base URL to use as slack.Client.BaseURL.
	URL string

	srv      *httptest.Server
	mu       sync.Mutex
	handlers map[string]func(url.Values) any
	calls    map[string][]url.Values
	auth     map[string][]string
	limits   map[string]*rateLimit
}

type rateLimit struct{ retryAfter, times int }

// New starts a fake Slack closed when t ends. A method without a handler
// answers {"ok": false, "error": "unknown_method"}, as Slack does.
func New(t testing.TB) *Server {
	t.Helper()
	s := &Server{
		handlers: map[string]func(url.Values) any{},
		calls:    map[string][]url.Values{},
		auth:     map[string][]string{},
		limits:   map[string]*rateLimit{},
	}
	s.srv = httptest.NewServer(http.HandlerFunc(s.serve))
	s.URL = s.srv.URL + "/api/"
	t.Cleanup(s.srv.Close)
	return s
}

// Close stops the server early, so calls fail at the transport.
func (s *Server) Close() { s.srv.Close() }

// Handle answers method with fn's result encoded as JSON. nil or a map without
// "ok" gets "ok": true; a string is sent raw; a Response sets status and headers too.
func (s *Server) Handle(method string, fn func(url.Values) any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers[method] = fn
}

// Calls returns the form of every request received for method, rate-limited ones included.
func (s *Server) Calls(method string) []url.Values {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]url.Values(nil), s.calls[method]...)
}

// Authorization returns the bearer token of every request received for method ("" when absent).
func (s *Server) Authorization(method string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.auth[method]...)
}

// RateLimit answers the next times requests for method with HTTP 429 and Retry-After.
func (s *Server) RateLimit(method string, retryAfter int, times int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.limits[method] = &rateLimit{retryAfter: retryAfter, times: times}
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	method := strings.TrimPrefix(r.URL.Path, "/api/")
	if r.Method != http.MethodPost || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
		writeJSON(w, http.StatusOK, nil, map[string]any{"ok": false, "error": "invalid_form_data"})
		return
	}
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusOK, nil, map[string]any{"ok": false, "error": "invalid_form_data"})
		return
	}

	s.mu.Lock()
	s.calls[method] = append(s.calls[method], r.PostForm)
	s.auth[method] = append(s.auth[method], strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	fn := s.handlers[method]
	var limited *rateLimit
	if rl := s.limits[method]; rl != nil && rl.times > 0 {
		rl.times--
		limited = rl
	}
	s.mu.Unlock()

	if limited != nil {
		h := http.Header{"Retry-After": {strconv.Itoa(limited.retryAfter)}}
		writeJSON(w, http.StatusTooManyRequests, h, map[string]any{"ok": false, "error": "ratelimited"})
		return
	}
	if fn == nil {
		writeJSON(w, http.StatusOK, nil, map[string]any{"ok": false, "error": "unknown_method"})
		return
	}
	switch v := fn(r.PostForm).(type) {
	case Response:
		status := v.Status
		if status == 0 {
			status = http.StatusOK
		}
		writeJSON(w, status, v.Header, withOK(v.Body))
	default:
		writeJSON(w, http.StatusOK, nil, withOK(v))
	}
}

func withOK(body any) any {
	if body == nil {
		return map[string]any{"ok": true}
	}
	m, ok := body.(map[string]any)
	if !ok {
		return body
	}
	if _, set := m["ok"]; set {
		return m
	}
	out := make(map[string]any, len(m)+1)
	for k, v := range m {
		out[k] = v
	}
	out["ok"] = true
	return out
}

func writeJSON(w http.ResponseWriter, status int, h http.Header, body any) {
	for k, vs := range h {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	if raw, ok := body.(string); ok {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(raw))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
