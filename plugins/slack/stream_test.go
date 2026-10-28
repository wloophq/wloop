package slack

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	dslack "github.com/wloophq/wloop/plugins/slack/domain"
	"github.com/wloophq/wloop/plugins/slack/socket"
	"github.com/wloophq/wloop/plugins/slack/socket/sockettest"
)

func envelope(id string, retry int, payload json.RawMessage) map[string]any {
	reason := ""
	if retry > 0 {
		reason = "timeout"
	}
	return map[string]any{"envelope_id": id, "type": "events_api", "retry_attempt": retry, "retry_reason": reason,
		"payload": payload, "accepts_response_payload": false}
}

func socketEnvelope(id string, retry int, payload json.RawMessage) socket.Envelope {
	var p struct {
		EventID string `json:"event_id"`
	}
	_ = json.Unmarshal(payload, &p)
	reason := ""
	if retry > 0 {
		reason = "timeout"
	}
	return socket.Envelope{ID: id, Type: "events_api", EventID: p.EventID, RetryAttempt: retry, RetryReason: reason, Payload: payload}
}

// running starts the module's Run against a fake socket server and returns
// a stop that waits for Run to return.
func (f *fixture) running(t *testing.T, srv *sockettest.Server) (stop func() error) {
	t.Helper()
	f.slack.Open = srv.Open
	f.build(t)
	f.login(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- f.p.run(ctx) }()
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

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestStreamAcksBeforeParse(t *testing.T) {
	f := newFixture(t)
	srv := sockettest.New(t)
	f.running(t, srv)
	srv.Hello(1)
	srv.Send(envelope("env-bad", 0, json.RawMessage(`{"event_id":"Ev1","type":"event_callback","event":"not an event"}`)))
	srv.Send(envelope("env-good", 0, event("Ev2", evMsg(dm1, "im", other, ts(1), "hello"))))
	waitFor(t, "both acks", func() bool { return len(srv.Acks()) == 2 })
	if acks := srv.Acks(); !slices.Equal(acks, []string{"env-bad", "env-good"}) {
		t.Fatalf("acks %v", acks)
	}
	waitFor(t, "the good message stored", func() bool { return len(f.stored(t, dm1)) == 1 })
	if n := f.p.events.parseErrors.Load(); n != 1 {
		t.Fatalf("parse errors %d", n)
	}
	if ms := f.stored(t, dm1); ms[0].TS != ts(1) {
		t.Fatalf("stored %+v", ms)
	}
}

func TestStreamDropsRepeatedEventID(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	payload := event("Ev1", evMsg(dm1, "im", other, ts(1), "hello"))
	ctx := context.Background()
	f.p.events.handle(ctx, socketEnvelope("env-1", 0, payload))
	f.p.events.handle(ctx, socketEnvelope("env-2", 1, payload))
	if es := f.p.src.inbox.peek([]string{dm1})[dm1]; len(es) != 1 {
		t.Fatalf("%d deltas reached the inbox", len(es))
	}
	if s := f.p.slack.Metrics.snapshot(); s.EventsPerHour.Received != 1 || s.EventsPerHour.Admitted != 1 {
		t.Fatalf("metrics %+v", s.EventsPerHour)
	}
}

func TestEventIDWindowIsTenMinutes(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	payload := event("Ev1", evMsg(dm1, "im", other, ts(1), "hello"))
	ctx := context.Background()
	f.p.events.handle(ctx, socketEnvelope("env-1", 0, payload))
	f.now = f.now.Add(EventWindow - time.Second)
	f.p.events.handle(ctx, socketEnvelope("env-2", 1, payload))
	if es := f.p.src.inbox.peek([]string{dm1})[dm1]; len(es) != 1 {
		t.Fatalf("inside the window: %d deltas", len(es))
	}
	f.flush(t)
	f.now = f.now.Add(time.Second)
	f.p.events.handle(ctx, socketEnvelope("env-3", 2, payload))
	if es := f.p.src.inbox.peek([]string{dm1})[dm1]; len(es) != 1 {
		t.Fatalf("after the window: %d deltas", len(es))
	}
	f.flush(t)
	if cs := changesOf(t, f); len(cs) != 1 {
		t.Fatalf("the store kept %d changes of one message", len(cs))
	}
}

func changesOf(t *testing.T, f *fixture) []dslack.Change {
	t.Helper()
	cs, err := f.p.src.store.Changes(context.Background(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	return cs
}

func TestDebouncePerObject(t *testing.T) {
	if Debounce != 2*time.Second {
		t.Fatalf("Debounce %v", Debounce)
	}
	synctest.Test(t, func(t *testing.T) {
		const debounce = 150 * time.Millisecond
		var touches [][]string
		var touchedAt []time.Duration
		start := time.Now()
		b := newInbox(debounce, func(_ context.Context, objects []string) error {
			touches = append(touches, objects)
			touchedAt = append(touchedAt, time.Since(start))
			return nil
		}, nil)
		stop := runInbox(b)
		defer stop()
		obj := dslack.ThreadObject(chan1, ts(1))
		for i := range 5 {
			b.add(obj, dslack.KindChannel, dslack.Delta{Conversation: chan1, TS: ts(1 + i), Kind: dslack.KindMessage})
			time.Sleep(10 * time.Millisecond)
		}
		time.Sleep(10 * debounce)
		synctest.Wait()
		if len(touches) != 1 || !slices.Equal(touches[0], []string{obj}) || touchedAt[0] != debounce {
			t.Fatalf("touches %v at %v", touches, touchedAt)
		}
		if len(b.peek([]string{obj})) != 0 {
			t.Fatal("the touched deltas are still waiting")
		}
	})
}

func TestInboxRetriesAFailedTouch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const debounce = 20 * time.Millisecond
		var touchedAt []time.Duration
		start := time.Now()
		b := newInbox(debounce, func(context.Context, []string) error {
			touchedAt = append(touchedAt, time.Since(start))
			if len(touchedAt) < 2 {
				return errors.New("store busy")
			}
			return nil
		}, nil)
		stop := runInbox(b)
		defer stop()
		b.add(dm1, dslack.KindIM, dslack.Delta{Conversation: dm1, TS: ts(1), Kind: dslack.KindMessage})
		time.Sleep(10 * debounce)
		synctest.Wait()
		if !slices.Equal(touchedAt, []time.Duration{debounce, 2 * debounce}) {
			t.Fatalf("touched at %v", touchedAt)
		}
		if len(b.peek([]string{dm1})) != 0 {
			t.Fatal("the inbox kept the retried deltas")
		}
	})
}

// runInbox runs b until the returned stop, which waits for run to return.
func runInbox(b *inbox) (stop func()) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		b.run(ctx)
	}()
	return func() {
		cancel()
		<-done
	}
}

// TestRecordingReplay streams a reduced overnight recording through Run
// with a viewer the recording never names: only DM and group DM traffic of
// people is stored, and every envelope is acked.
func TestRecordingReplay(t *testing.T) {
	f := newFixture(t)
	f.api.Handle("conversations.replies", func(url.Values) any { return map[string]any{"ok": false, "error": "thread_not_found"} })
	srv := sockettest.New(t)
	f.running(t, srv)
	raw, err := os.ReadFile(filepath.Join("domain", "testdata", "listen.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var envelopes, dmPeople int
	dms := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var fr struct {
			Type       string          `json:"type"`
			EnvelopeID string          `json:"envelope_id"`
			Payload    json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal([]byte(line), &fr); err != nil {
			t.Fatal(err)
		}
		if fr.Type == "disconnect" {
			continue
		}
		srv.Send(line)
		if fr.EnvelopeID == "" {
			continue
		}
		envelopes++
		d, ok, err := dslack.FromEvent(team, fr.Payload, dslack.ViaSocket, t0)
		if err != nil || !ok || (d.ChannelType != "im" && d.ChannelType != "mpim") {
			continue
		}
		dms[d.Conversation] = true
		if d.Kind == dslack.KindMessage && d.Message.User != "" {
			dmPeople++
		}
	}
	waitFor(t, "every ack", func() bool { return len(srv.Acks()) == envelopes })
	var conversations []string
	for c := range dms {
		conversations = append(conversations, c)
	}
	count := func() (n int) {
		for _, c := range conversations {
			n += len(f.stored(t, c))
		}
		return n
	}
	waitFor(t, "the DM messages stored", func() bool { return count() >= dmPeople })
	list, _ := json.Marshal(conversations)
	var outside, stored int
	if err := f.db.R().QueryRowContext(context.Background(), `SELECT count(*) FILTER (WHERE conversation NOT IN (SELECT value FROM json_each(?))),
		count(*) FROM slack_messages`, string(list)).Scan(&outside, &stored); err != nil {
		t.Fatal(err)
	}
	if outside != 0 {
		t.Fatalf("%d messages stored outside the DMs", outside)
	}
	var unattributed int
	if err := f.db.R().QueryRowContext(context.Background(), `SELECT count(*) FROM slack_messages WHERE user = ''`).Scan(&unattributed); err != nil || unattributed != 0 {
		t.Fatalf("%d stored messages have no person behind them (%v)", unattributed, err)
	}
	t.Logf("replayed %d envelopes: %d DM messages of people, %d stored, %d users.info calls", envelopes, dmPeople, stored, len(f.api.Calls("users.info")))
}
