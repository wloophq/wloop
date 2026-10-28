// Package sockettest is a fake Slack Socket Mode server for tests.
package sockettest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// Server accepts one Socket Mode connection at a time. Frames sent while no
// client is connected wait for the next connection.
type Server struct {
	t     testing.TB
	srv   *httptest.Server
	mu    sync.Mutex
	conn  *websocket.Conn
	ready chan struct{} // closed while conn is set
	acks  []string
	opens int
}

func New(t testing.TB) *Server {
	t.Helper()
	s := &Server{t: t, ready: make(chan struct{})}
	s.srv = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(func() {
		s.mu.Lock()
		if s.conn != nil {
			_ = s.conn.CloseNow()
		}
		s.mu.Unlock()
		s.srv.Close()
	})
	return s
}

// URL is the WebSocket endpoint without a ticket.
func (s *Server) URL() string { return "ws" + strings.TrimPrefix(s.srv.URL, "http") + "/link/" }

// Open plays apps.connections.open: it counts the call and returns a fresh URL
// with a ticket and an app id in the query, as Slack does.
func (s *Server) Open(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.opens++
	return fmt.Sprintf("%s?ticket=fake-ticket-%d&app_id=A000", s.URL(), s.opens), nil
}

func (s *Server) Opens() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.opens
}

// Acks are the envelope ids the client acknowledged, in order.
func (s *Server) Acks() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.acks...)
}

// Send writes frame (JSON-encoded unless it is a string or []byte) to the
// current connection, waiting up to 5 s for one.
func (s *Server) Send(frame any) {
	s.t.Helper()
	var data []byte
	switch f := frame.(type) {
	case string:
		data = []byte(f)
	case []byte:
		data = f
	default:
		var err error
		if data, err = json.Marshal(f); err != nil {
			s.t.Fatalf("sockettest: %v", err)
		}
	}
	c := s.current()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Write(ctx, websocket.MessageText, data); err != nil {
		s.t.Errorf("sockettest: send: %v", err)
	}
}

func (s *Server) Hello(n int) {
	s.t.Helper()
	s.Send(map[string]any{"type": "hello", "num_connections": n,
		"debug_info":      map[string]any{"host": "fake", "approximate_connection_time": 18060},
		"connection_info": map[string]any{"app_id": "A000"}})
}

// Disconnect sends a disconnect envelope; later frames go to the next connection.
func (s *Server) Disconnect(reason string) {
	s.t.Helper()
	c := s.current()
	s.Send(map[string]any{"type": "disconnect", "reason": reason, "debug_info": map[string]any{"host": "fake"}})
	s.detach(c)
}

// Drop closes the current connection without a close handshake.
func (s *Server) Drop() {
	s.t.Helper()
	c := s.current()
	s.detach(c)
	_ = c.CloseNow()
}

func (s *Server) current() *websocket.Conn {
	s.t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		s.mu.Lock()
		c, ready := s.conn, s.ready
		s.mu.Unlock()
		if c != nil {
			return c
		}
		select {
		case <-ready:
		case <-deadline:
			s.t.Fatal("sockettest: no client connected within 5 s")
		}
	}
}

// detach forgets c so the next frame waits for a new connection.
func (s *Server) detach(c *websocket.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn == c {
		s.conn = nil
		s.ready = make(chan struct{})
	}
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("ticket") == "" {
		http.Error(w, "no ticket", http.StatusUnauthorized)
		return
	}
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	s.mu.Lock()
	if s.conn != nil {
		_ = s.conn.CloseNow()
	} else {
		close(s.ready)
	}
	s.conn = c
	s.mu.Unlock()
	defer s.detach(c)
	for {
		_, data, err := c.Read(context.Background())
		if err != nil {
			return
		}
		var ack struct {
			EnvelopeID string `json:"envelope_id"`
		}
		if json.Unmarshal(data, &ack) == nil && ack.EnvelopeID != "" {
			s.mu.Lock()
			s.acks = append(s.acks, ack.EnvelopeID)
			s.mu.Unlock()
		}
	}
}
