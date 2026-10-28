package socket

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/plugins/slack/slackapi"
	"github.com/wloophq/wloop/plugins/slack/slacktest"
	"github.com/wloophq/wloop/plugins/slack/socket/sockettest"
)

type recorder struct {
	mu     sync.Mutex
	states []State
	logs   []string
	events []string
}

func (r *recorder) state(s State) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.states = append(r.states, s)
}

func (r *recorder) log(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logs = append(r.logs, fmt.Sprintf(format, args...))
}

func (r *recorder) event(e string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

func (r *recorder) find(kind StateKind) []State {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []State
	for _, s := range r.states {
		if s.Kind == kind {
			out = append(out, s)
		}
	}
	return out
}

func eventually(t *testing.T, within time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("not within %s: %s", within, what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func start(t *testing.T, c *Conn, handle func(Envelope)) (stop func() error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx, handle) }()
	var once sync.Once
	var err error
	stop = func() error {
		once.Do(func() {
			cancel()
			select {
			case err = <-done:
			case <-time.After(10 * time.Second):
				err = errors.New("Run did not return")
			}
		})
		return err
	}
	t.Cleanup(func() { _ = stop() })
	return stop
}

func event(id, eventID string) map[string]any {
	return map[string]any{"envelope_id": id, "type": "events_api", "accepts_response_payload": false,
		"payload": map[string]any{"type": "event_callback", "event_id": eventID,
			"event": map[string]any{"type": "message", "channel": "C000", "user": "U000", "ts": "1.0", "text": "hi"}}}
}

type spySocket struct {
	Socket
	rec *recorder
}

func (s spySocket) Write(ctx context.Context, frame []byte) error {
	s.rec.event("write " + string(frame))
	return s.Socket.Write(ctx, frame)
}

func TestAcksBeforeHandling(t *testing.T) {
	srv := sockettest.New(t)
	rec := &recorder{}
	got := make(chan Envelope, 1)
	c := &Conn{Open: srv.Open, OnState: rec.state, Log: rec.log,
		Dial: func(ctx context.Context, u string) (Socket, error) {
			s, err := dialWebsocket(ctx, u)
			if err != nil {
				return nil, err
			}
			return spySocket{s, rec}, nil
		}}
	start(t, c, func(e Envelope) { rec.event("handle " + e.ID); got <- e })
	srv.Hello(3)
	srv.Send(event("env-1", "Ev1"))
	e := <-got
	if e.ID != "env-1" || e.Type != "events_api" || e.EventID != "Ev1" || !strings.Contains(string(e.Payload), `"event_id":"Ev1"`) {
		t.Fatalf("envelope %+v", e)
	}
	rec.mu.Lock()
	events := slices.Clone(rec.events)
	rec.mu.Unlock()
	if want := []string{`write {"envelope_id":"env-1"}`, "handle env-1"}; !slices.Equal(events, want) {
		t.Fatalf("order %q, want %q", events, want)
	}
	eventually(t, 3*time.Second, "server got the ack", func() bool { return slices.Equal(srv.Acks(), []string{"env-1"}) })
	hellos := rec.find(StateHello)
	if len(hellos) != 1 || hellos[0].NumConnections != 3 || hellos[0].ApproxConnTime != 18060*time.Second || hellos[0].At.IsZero() {
		t.Fatalf("hello states %+v", hellos)
	}
	if len(rec.find(StateDialing)) != 1 {
		t.Fatalf("states %+v", rec.states)
	}
}

func TestSlowHandlerDoesNotDelayAcks(t *testing.T) {
	srv := sockettest.New(t)
	release := make(chan struct{})
	c := &Conn{Open: srv.Open}
	stop := start(t, c, func(e Envelope) {
		if e.ID == "env-1" {
			<-release
		}
	})
	srv.Hello(1)
	srv.Send(event("env-1", "Ev1"))
	srv.Send(event("env-2", "Ev2"))
	eventually(t, 3*time.Second, "env-2 acked while env-1 is handled",
		func() bool { return slices.Equal(srv.Acks(), []string{"env-1", "env-2"}) })
	close(release)
	if err := stop(); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run returned %v", err)
	}
}

func TestFullQueueReportsOverflow(t *testing.T) {
	srv := sockettest.New(t)
	rec := &recorder{}
	release := make(chan struct{})
	var handled sync.Map
	c := &Conn{Open: srv.Open, OnState: rec.state, Log: rec.log, Queue: 1}
	start(t, c, func(e Envelope) { handled.Store(e.ID, true); <-release })
	srv.Hello(1)
	for i := 1; i <= 4; i++ {
		srv.Send(event(fmt.Sprintf("env-%d", i), fmt.Sprintf("Ev%d", i)))
	}
	eventually(t, 3*time.Second, "all four acked", func() bool { return len(srv.Acks()) == 4 })
	eventually(t, 3*time.Second, "overflow reported", func() bool { return len(rec.find(StateOverflow)) > 0 })
	if o := rec.find(StateOverflow)[0]; o.Reason != "events_api" {
		t.Fatalf("overflow state %+v", o)
	}
	close(release)
	eventually(t, 3*time.Second, "queued envelopes handled", func() bool { _, ok := handled.Load("env-1"); return ok })
}

func TestReconnectsOnDisconnect(t *testing.T) {
	srv := sockettest.New(t)
	rec := &recorder{}
	got := make(chan string, 8)
	c := &Conn{Open: srv.Open, OnState: rec.state, Log: rec.log, minBackoff: 5 * time.Millisecond, maxBackoff: 20 * time.Millisecond}
	start(t, c, func(e Envelope) { got <- e.ID })
	srv.Hello(1)
	for i, reason := range []string{"refresh_requested", "warning"} {
		srv.Disconnect(reason)
		srv.Hello(2)
		id := fmt.Sprintf("after-%s", reason)
		srv.Send(event(id, "Ev"+reason))
		if g := <-got; g != id {
			t.Fatalf("got %s, want %s", g, id)
		}
		if srv.Opens() != i+2 {
			t.Fatalf("opens %d after %s", srv.Opens(), reason)
		}
		d := rec.find(StateDisconnect)
		if len(d) != i+1 || d[i].Reason != reason {
			t.Fatalf("disconnect states %+v", d)
		}
	}
	srv.Drop()
	srv.Hello(1)
	srv.Send(event("after-drop", "Ev3"))
	if g := <-got; g != "after-drop" {
		t.Fatalf("got %s", g)
	}
	if srv.Opens() != 4 || len(rec.find(StateDropped)) != 1 || rec.find(StateDropped)[0].Reason == "" {
		t.Fatalf("opens %d, dropped %+v", srv.Opens(), rec.find(StateDropped))
	}
	if len(rec.find(StateHello)) != 4 {
		t.Fatalf("hellos %+v", rec.find(StateHello))
	}
}

type fakeSocket struct {
	frames chan []byte
}

func (s *fakeSocket) Read(ctx context.Context) ([]byte, error) {
	select {
	case f, ok := <-s.frames:
		if !ok {
			return nil, errors.New("connection reset")
		}
		return f, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *fakeSocket) Write(context.Context, []byte) error { return nil }
func (s *fakeSocket) Close(string) error                  { return nil }

func TestBackoffResetsAfterHello(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var waits []time.Duration
	dials := 0
	c := &Conn{
		Open: func(context.Context) (string, error) { return "wss://fake.test/link/?ticket=t&app_id=A", nil },
		Dial: func(context.Context, string) (Socket, error) {
			dials++
			switch {
			case dials <= 7 || dials == 9:
				return nil, errors.New("connection refused")
			case dials == 8:
				s := &fakeSocket{frames: make(chan []byte, 1)}
				s.frames <- []byte(`{"type":"hello","num_connections":1}`)
				close(s.frames)
				return s, nil
			}
			cancel()
			return nil, ctx.Err()
		},
		sleep: func(_ context.Context, d time.Duration) error { waits = append(waits, d); return nil },
	}
	if err := c.Run(ctx, func(Envelope) {}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run returned %v", err)
	}
	s := time.Second
	want := []time.Duration{s, 2 * s, 4 * s, 8 * s, 16 * s, 30 * s, 30 * s, s, 2 * s}
	if !slices.Equal(waits, want) {
		t.Fatalf("waits %v, want %v", waits, want)
	}
}

func TestRetryMetadata(t *testing.T) {
	srv := sockettest.New(t)
	got := make(chan Envelope, 2)
	c := &Conn{Open: srv.Open}
	start(t, c, func(e Envelope) { got <- e })
	srv.Hello(1)
	e := event("env-r", "Ev7")
	e["retry_attempt"], e["retry_reason"] = 2, "timeout"
	srv.Send(e)
	srv.Send(map[string]any{"envelope_id": "env-s", "type": "slash_commands", "payload": map[string]any{"command": "/x"}})
	r := <-got
	if r.RetryAttempt != 2 || r.RetryReason != "timeout" || r.EventID != "Ev7" {
		t.Fatalf("envelope %+v", r)
	}
	if s := <-got; s.Type != "slash_commands" || s.EventID != "" || s.RetryAttempt != 0 {
		t.Fatalf("envelope %+v", s)
	}
}

func TestInvalidAppTokenStops(t *testing.T) {
	const token = "xapp-1-A000-1-secretvalue"
	fake := slacktest.New(t)
	fake.Handle("apps.connections.open", func(url.Values) any { return map[string]any{"ok": false, "error": "invalid_auth"} })
	client := &slack.Client{BaseURL: fake.URL, Limits: &slack.Limiter{PerMinute: map[string]int{"apps.connections.open": 100}}}
	rec := &recorder{}
	c := &Conn{Open: func(ctx context.Context) (string, error) { return client.ConnectionsOpen(ctx, token) },
		OnState: rec.state, Log: rec.log}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	began := time.Now()
	err := c.Run(ctx, func(Envelope) { t.Error("handled an envelope") })
	if !errors.Is(err, ErrInvalidAuth) || time.Since(began) > 2*time.Second {
		t.Fatalf("Run returned %v after %s", err, time.Since(began))
	}
	if n := len(fake.Calls("apps.connections.open")); n != 1 {
		t.Fatalf("%d opens", n)
	}
	if a := fake.Authorization("apps.connections.open"); len(a) != 1 || a[0] != token {
		t.Fatalf("authorization %q", a)
	}
	for _, s := range append(slices.Clone(rec.logs), err.Error()) {
		if strings.Contains(s, "secretvalue") || strings.Contains(s, "xapp") {
			t.Fatalf("token leaked: %q", s)
		}
	}
}

func TestSocketURLNeverLeaks(t *testing.T) {
	const u = "wss://127.0.0.1:1/link/?ticket=sekrit-ticket&app_id=A0SECRETAPP"
	rec := &recorder{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &Conn{Open: func(context.Context) (string, error) { return u, nil },
		OnState: func(s State) {
			rec.state(s)
			if s.Kind == StateDropped {
				cancel()
			}
		}, Log: rec.log}
	if err := c.Run(ctx, func(Envelope) {}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run returned %v", err)
	}
	dropped := rec.find(StateDropped)
	if len(dropped) != 1 || !strings.Contains(dropped[0].Reason, "wss://127.0.0.1:1/link/") {
		t.Fatalf("dropped %+v", dropped)
	}
	var texts []string
	for _, s := range rec.states {
		texts = append(texts, s.Reason)
	}
	texts = append(texts, rec.logs...)
	for _, s := range texts {
		for _, bad := range []string{"ticket=", "sekrit", "A0SECRETAPP", "app_id", "?"} {
			if strings.Contains(s, bad) {
				t.Fatalf("%q holds %q", s, bad)
			}
		}
	}

	direct := (&Conn{}).dropped(`dial: Get "https://h.test/link/?ticket=x&app_id=y": refused; retry ?ticket=z`)
	if strings.Contains(direct.Error(), "ticket") || strings.Contains(direct.Error(), "app_id") {
		t.Fatalf("scrubbed %q", direct)
	}
	for in, want := range map[string]string{
		u:                        "wss://127.0.0.1:1/link/",
		"wss://h.test/link?a=b":  "wss://h.test/link",
		"not a url ?ticket=1":    "[url]",
		"https://h.test/p#frag":  "https://h.test/p",
		"wss://u:pw@h.test/link": "wss://h.test/link",
	} {
		if got := RedactURL(in); got != want {
			t.Errorf("RedactURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestOpensThroughTheWebAPIClient(t *testing.T) {
	const token = "xapp-1-A000-1-fake"
	srv := sockettest.New(t)
	fake := slacktest.New(t)
	fake.Handle("apps.connections.open", func(url.Values) any {
		u, _ := srv.Open(context.Background())
		return map[string]any{"url": u}
	})
	client := &slack.Client{BaseURL: fake.URL, Limits: &slack.Limiter{PerMinute: map[string]int{"apps.connections.open": 100}}}
	got := make(chan Envelope, 1)
	c := &Conn{Open: func(ctx context.Context) (string, error) { return client.ConnectionsOpen(ctx, token) }}
	start(t, c, func(e Envelope) { got <- e })
	srv.Hello(1)
	srv.Send(event("env-1", "Ev1"))
	if e := <-got; e.EventID != "Ev1" {
		t.Fatalf("envelope %+v", e)
	}
	if a := fake.Authorization("apps.connections.open"); len(a) != 1 || a[0] != token {
		t.Fatalf("authorization %q", a)
	}
	eventually(t, 3*time.Second, "acked", func() bool { return slices.Equal(srv.Acks(), []string{"env-1"}) })
}
