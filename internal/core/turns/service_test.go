package turns_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/unit"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/core/turns/turnstest"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/turn"
)

var (
	r7 = ref(7)
	t0 = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
)

func ref(n int) unit.Unit {
	return unit.Unit{Provider: unit.GitHub, Key: fmt.Sprintf("acme/api#%d", n)}
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func fixed(t time.Time) *clock { return &clock{t: t} }

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type recorder struct {
	mu        sync.Mutex
	launches  []turns.Launch
	launchErr error
	attempts  int
	reviews   map[unit.Unit][]turns.SourceAnswer
	published []port.Event
	changes   []unit.Unit
}

func (r *recorder) Launch(_ context.Context, l turns.Launch) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.attempts++
	if r.launchErr != nil {
		return r.launchErr
	}
	r.launches = append(r.launches, l)
	return nil
}

func (r *recorder) Answers(_ context.Context, u unit.Unit, since time.Time) ([]turns.SourceAnswer, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []turns.SourceAnswer
	for _, rv := range r.reviews[u] {
		if !rv.At.Before(since) {
			out = append(out, rv)
		}
	}
	return out, nil
}

func (r *recorder) Publish(evs ...port.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.published = append(r.published, evs...)
}

func (r *recorder) changed(_ context.Context, refs ...unit.Unit) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.changes = append(r.changes, refs...)
}

func newService(t *testing.T) (*turns.Service, *turnstest.Store, *turnstest.Registry, *turnstest.Sessions, *recorder) {
	t.Helper()
	store, reg, sess := turnstest.NewStore(turnstest.Strict(t)), turnstest.NewRegistry(turnstest.Strict(t)), turnstest.NewSessions()
	rec := &recorder{reviews: map[unit.Unit][]turns.SourceAnswer{}}
	return &turns.Service{Intents: turnstest.Intents(), Store: store, Registry: reg, Sessions: sess, Launcher: rec, Answers: rec,
		Changed: rec.changed, Pub: rec, Clock: fixed(t0), Command: "'/bin/" + brand.Name + "'", FastJob: 2 * time.Minute}, store, reg, sess, rec
}

func bookOf(t *testing.T, store *turnstest.Store, ref unit.Unit) turn.Book {
	t.Helper()
	books, err := store.Books(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	return books[ref]
}

func kinds(evs []port.Event, kind string) []port.Event {
	var out []port.Event
	for _, e := range evs {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

func refusal(err error, want string) bool {
	var r turns.Refusal
	return errors.As(err, &r) && strings.Contains(r.Reason, want)
}

func TestWithdrawUndoesTheSend(t *testing.T) {
	s, store, _, _, rec := newService(t)
	ctx := context.Background()
	asked := store.Put(turn.Book{Unit: r7, Turns: []turn.Turn{{Intent: turn.Intent(dintent.Review), ID: "q", Trigger: turn.Automated, State: turn.Asked, SentAt: t0.Add(-time.Hour)}}})
	if _, err := s.Send(ctx, turns.SendRequest{Unit: r7, Intent: turn.Intent(dintent.Review), Turn: "abc", Trigger: turn.Automated, ResumeOf: asked.Turns[0].Seq, Records: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.Withdraw(ctx, r7, "abc", "typing the prompt: orca is down"); err != nil {
		t.Fatal(err)
	}
	b := bookOf(t, store, r7)
	if b.Turns[0].State != turn.Asked || b.Turns[1].State != turn.Withdrawn || !b.Turns[1].EndedAt.Equal(t0) {
		t.Fatalf("turns %+v", b.Turns)
	}
	if q, ok := b.Question([]string{"review", "address", "probe"}); !ok || q.Seq != asked.Turns[0].Seq || !b.OpenQuestion(turn.Intent(dintent.Review)) {
		t.Fatalf("question %+v %v", q, ok)
	}
	if evs := kinds(store.Events(), turns.KindTurnWithdrawn); len(evs) != 1 || evs[0].Data != "typing the prompt: orca is down" {
		t.Fatalf("events %+v", store.Events())
	}
	if !slices.Contains(rec.changes, r7) {
		t.Fatal("the views were not told")
	}
	if err := s.Withdraw(ctx, r7, "abc", "again"); err == nil {
		t.Fatal("a second withdraw found the withdrawn turn")
	}
	if _, err := s.Start(ctx, "term_1", "abc"); !refusal(err, "no turn abc") {
		t.Fatalf("an agent started a withdrawn turn: %v", err)
	}
	done := record.Record{State: record.Done, Summary: "done"}
	if _, err := s.RecordTurnEnd(ctx, turns.RecordRequest{Session: "term_1", Turn: "abc", Via: "mcp", Record: done}); !refusal(err, "no turn ") {
		t.Fatalf("a record of a withdrawn turn: %v", err)
	}
}

func TestSendSkipsTextsThatAskForNoRecord(t *testing.T) {
	s, store, _, _, rec := newService(t)
	ctx := context.Background()
	got, err := s.Send(ctx, turns.SendRequest{Unit: r7, Intent: turn.Intent(dintent.Review), Turn: "abc", Trigger: turn.Automated, Records: false})
	if err != nil || !reflect.DeepEqual(got, turn.Turn{}) {
		t.Fatalf("a dry run: %+v %v", got, err)
	}
	if b := bookOf(t, store, r7); len(b.Turns) != 0 || len(store.Events()) != 0 || len(rec.published) != 0 {
		t.Fatalf("a dry run stored %+v, events %+v", b, store.Events())
	}

	first, err := s.Send(ctx, turns.SendRequest{Unit: r7, Intent: turn.Intent(dintent.Review), Turn: "abc", Trigger: turn.Automated, Guidance: "look at the migration", Head: "h1", Records: true})
	if err != nil || first.Seq == 0 || first.State != turn.Sent || first.Head != "h1" || first.Guidance != "look at the migration" || !first.SentAt.Equal(t0) {
		t.Fatalf("sent %+v %v", first, err)
	}
	if _, err := s.Send(ctx, turns.SendRequest{Unit: r7, Intent: turn.Intent(dintent.Review), Turn: "def", Trigger: turn.ByHand, Records: true}); err != nil {
		t.Fatal(err)
	}
	b := bookOf(t, store, r7)
	if len(b.Turns) != 2 || b.Turns[0].State != turn.Replaced || b.Turns[1].State != turn.Sent {
		t.Fatalf("turns %+v", b.Turns)
	}
	if evs := kinds(store.Events(), turns.KindTurnSent); len(evs) != 2 || evs[0].Key != "abc" || evs[1].Key != "def" {
		t.Fatalf("events %+v", store.Events())
	}
	if len(rec.published) != 2 || len(rec.changes) != 0 {
		t.Fatalf("published %+v, changed %v: the views learn a turn once it is typed", rec.published, rec.changes)
	}
}

func TestStartOnTheSessionTheTurnWasSentTo(t *testing.T) {
	s, store, _, _, _ := newService(t)
	ctx := context.Background()
	s.Send(ctx, turns.SendRequest{Unit: r7, Intent: turn.Intent(dintent.Review), Turn: "abc", Trigger: turn.Automated, Records: true})
	if err := s.SentTo(ctx, r7, "abc", "term_1"); err != nil {
		t.Fatal(err)
	}
	got, err := s.Start(ctx, "term_1", "abc")
	if err != nil || got.State != turn.Started || got.Session != "term_1" || got.SentTo != "term_1" || !got.StartedAt.Equal(t0) {
		t.Fatalf("started %+v %v", got, err)
	}
	if again, err := s.Start(ctx, "term_1", "abc"); err != nil || again.State != turn.Started {
		t.Fatalf("started again: %+v %v", again, err)
	}
	if evs := kinds(store.Events(), turns.KindTurnStarted); len(evs) != 1 || evs[0].Key != "abc" {
		t.Fatalf("events %+v", evs)
	}
	if err := s.SentTo(ctx, r7, "abc", "term_2"); !refusal(err, "not sent") {
		t.Fatalf("a started turn was sent again: %v", err)
	}
	if _, err := s.Start(ctx, "term_2", "abc"); !refusal(err, "another session") {
		t.Fatalf("another session started a started turn: %v", err)
	}
	if _, err := s.Start(ctx, "term_1", "nope"); !refusal(err, "no turn") {
		t.Fatalf("an unknown round: %v", err)
	}

	s.Send(ctx, turns.SendRequest{Unit: r7, Intent: turn.Intent(dintent.Address), Turn: "def", Trigger: turn.Automated, Records: true})
	s.SentTo(ctx, r7, "def", "term_1")
	moved, err := s.Start(ctx, "term_2", "def")
	if err != nil || moved.Session != "term_2" || moved.SentTo != "term_1" {
		t.Fatalf("a turn sent to another handle: %+v %v", moved, err)
	}
	evs := kinds(store.Events(), turns.KindTurnStarted)
	if last := evs[len(evs)-1]; last.Key != "def" || !strings.Contains(last.Data, "term_1") || !strings.Contains(last.Data, "term_2") {
		t.Fatalf("the started event names %q", last.Data)
	}

	if _, err := s.RecordTurnEnd(ctx, turns.RecordRequest{Session: "term_1", Turn: "abc", Record: record.Record{State: record.Settled, Summary: "nothing new"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Start(ctx, "term_1", "abc"); !refusal(err, "settled") {
		t.Fatalf("a closed turn started: %v", err)
	}
}

func TestRecordTurnEndChecksTheCallingSession(t *testing.T) {
	s, store, reg, _, rec := newService(t)
	ctx := context.Background()
	reg.Put(turn.Session{ID: "term_1", MCPAt: t0})
	s.Send(ctx, turns.SendRequest{Unit: r7, Intent: "review", Turn: "abc", Trigger: "automated", Head: "h1", Records: true})
	s.SentTo(ctx, r7, "abc", "term_1")
	if _, err := s.Start(ctx, "term_1", "abc"); err != nil {
		t.Fatal(err)
	}
	settled := record.Record{State: record.Settled, Summary: "nothing new"}
	for _, c := range []struct {
		name, handle, round string
		unit                unit.Unit
		rec                 record.Record
		want                string
	}{
		{"another session", "term_2", "abc", unit.Unit{}, settled, "belongs to another session"},
		{"unknown round", "term_1", "nope", unit.Unit{}, settled, "no turn"},
		{"another pull request", "term_1", "abc", ref(8), settled, "acme/api#7"},
		{"malformed", "term_1", "abc", unit.Unit{}, record.Record{State: record.Asked, Summary: "s"}, "question: required"},
	} {
		if _, err := s.RecordTurnEnd(ctx, turns.RecordRequest{Session: c.handle, Turn: c.round, Unit: c.unit, Record: c.rec}); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	got, err := s.RecordTurnEnd(ctx, turns.RecordRequest{Session: "term_1", Turn: "abc", Via: "mcp", Record: settled})
	if err != nil || !got.Added || got.State != record.Settled {
		t.Fatal(got, err)
	}
	if again, _ := s.RecordTurnEnd(ctx, turns.RecordRequest{Session: "term_1", Turn: "abc", Record: settled}); again.Added {
		t.Fatal("a repeated record was stored twice")
	}
	b := bookOf(t, store, r7)
	if len(b.Records) != 1 || b.Records[0].Head != "h1" || b.Records[0].Session != "term_1" || b.Records[0].Acked || b.Turns[0].State != turn.Settled {
		t.Fatalf("book %+v", b)
	}
	if evs := kinds(store.Events(), turns.KindTurnEnded); len(evs) != 1 || !strings.HasPrefix(evs[0].Key, "abc:") || !strings.Contains(evs[0].Data, "mcp") {
		t.Fatalf("events %+v", evs)
	}
	if !slices.Contains(rec.changes, r7) {
		t.Fatal("the views were not told")
	}
}

func TestAnIdenticalReAskAfterATypedAnswerIsStored(t *testing.T) {
	s, store, reg, _, _ := newService(t)
	ctx := context.Background()
	reg.Put(turn.Session{ID: "term_1", AgentSession: "s1", StartedAt: t0.Add(-2 * time.Hour), MCPAt: t0.Add(-2 * time.Hour)})
	putTurn(store, true, turn.Answer{TurnSeq: 1, Text: "go", Source: turn.Cockpit, Route: turn.Typed, Session: "term_1", At: t0.Add(-10 * time.Minute)})
	b := bookOf(t, store, r7)
	b.Records[0].Hash = askedRecord().Hash()
	store.Put(b)
	got, err := s.RecordTurnEnd(ctx, turns.RecordRequest{Session: "term_1", Turn: "abc", Record: askedRecord()})
	if err != nil || !got.Added {
		t.Fatalf("the re-ask: %+v %v", got, err)
	}
	if again, _ := s.RecordTurnEnd(ctx, turns.RecordRequest{Session: "term_1", Turn: "abc", Record: askedRecord()}); again.Added {
		t.Fatal("a retry of the re-ask was stored")
	}
	stop, err := s.Stop(ctx, turns.StopRequest{Session: "term_1", AgentSession: "s1", PromptID: "p2"})
	if err != nil || stop.Block {
		t.Fatalf("the stop after the re-ask: %+v %v", stop, err)
	}
	if b := bookOf(t, store, r7); len(b.Records) != 2 || b.Turns[0].State != turn.Asked {
		t.Fatalf("book %+v", b)
	}
}

func TestRecordTurnEndRefusesAnOlderTurnOfTheSession(t *testing.T) {
	s, _, _, _, _ := newService(t)
	ctx := context.Background()
	s.Send(ctx, turns.SendRequest{Unit: r7, Intent: turn.Intent(dintent.Review), Turn: "abc", Trigger: turn.Automated, Records: true})
	s.SentTo(ctx, r7, "abc", "term_1")
	s.Start(ctx, "term_1", "abc")
	s.Send(ctx, turns.SendRequest{Unit: ref(8), Intent: turn.Intent(dintent.Address), Turn: "def", Trigger: turn.Automated, Records: true})
	s.SentTo(ctx, ref(8), "def", "term_1")
	done := record.Record{State: record.Failed, Summary: "broke", Resume: "the build fails"}
	if _, err := s.RecordTurnEnd(ctx, turns.RecordRequest{Session: "term_1", Turn: "abc", Record: done}); !refusal(err, "this session's turn is now def") {
		t.Fatalf("an older turn: %v", err)
	}
	if _, err := s.RecordTurnEnd(ctx, turns.RecordRequest{Session: "term_1", Turn: "def", Record: done}); err != nil {
		t.Fatal(err)
	}
}

func TestRecordTurnEndByHandOutsideOrca(t *testing.T) {
	s, store, _, _, _ := newService(t)
	ctx := context.Background()
	started, err := s.StartByHand(ctx, turns.SendRequest{Unit: r7, Intent: turn.Intent(dintent.Review), Turn: "abc", Trigger: turn.ByHand, Head: "h1", Records: true}, "")
	if err != nil || started.State != turn.Started || started.Session != "" {
		t.Fatalf("by hand: %+v %v", started, err)
	}
	if len(kinds(store.Events(), turns.KindTurnSent)) != 1 || len(kinds(store.Events(), turns.KindTurnStarted)) != 1 {
		t.Fatalf("events %+v", store.Events())
	}
	settled := record.Record{State: record.Settled, Summary: "nothing new"}
	if _, err := s.RecordTurnEnd(ctx, turns.RecordRequest{Session: "term_9", Turn: "abc", Record: settled}); !refusal(err, "another session") {
		t.Fatalf("a session recorded a turn started outside Orca: %v", err)
	}
	r7copy := r7
	if got, err := s.RecordTurnEnd(ctx, turns.RecordRequest{Turn: "abc", Unit: r7copy, Via: "socket", Record: settled}); err != nil || !got.Added {
		t.Fatalf("no handle: %+v %v", got, err)
	}
}

func TestSessionsFromTheHooks(t *testing.T) {
	s, _, reg, _, _ := newService(t)
	ctx := context.Background()
	c := s.Clock.(*clock)
	if err := s.MCPSeen(ctx, "term_1"); err != nil {
		t.Fatal(err)
	}
	c.advance(time.Second)
	s.NewID = func() string { return "term_1" }
	if id, err := s.Resolve(ctx, "orca", "term_1"); err != nil || id != "term_1" {
		t.Fatalf("Resolve = %q, %v", id, err)
	}
	if err := s.SessionStarted(ctx, turns.SessionStart{Session: "term_1", Pane: "tab-1:leaf-1", WorkspaceID: "ws", AgentSession: "s1", Source: "startup", Cwd: "/wt/a", Home: "/h"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Launched(ctx, turn.Session{ID: "term_1", WorkspaceID: "ws2", Worktree: "/wt/b"}); err != nil {
		t.Fatal(err)
	}
	c.advance(time.Second)
	if err := s.SessionStarted(ctx, turns.SessionStart{Session: "term_1", Pane: "tab-9:leaf-9", Source: "compact"}); err != nil {
		t.Fatal(err)
	}
	got, _, _ := reg.Session(ctx, "term_1")
	want := turn.Session{ID: "term_1", Manager: "orca", Handle: "term_1", LastHandle: "term_1", Pane: "tab-1:leaf-1", AgentSession: "s1", WorkspaceID: "ws2", Worktree: "/wt/b", Home: "/h",
		Managed: true, StartedAt: t0.Add(time.Second), MCPAt: t0}
	if got != want {
		t.Fatalf("session %+v\nwant %+v", got, want)
	}
	if ok, _ := s.Capable(ctx, "term_1"); !ok {
		t.Fatal("an initialize a second before the start does not count")
	}
	var seen []string
	for _, e := range reg.Events() {
		seen = append(seen, e.Kind)
	}
	if want := []string{turns.KindSessionAdopted, turns.KindMCPSeen, turns.KindSessionStarted, turns.KindSessionLaunched, turns.KindSessionStarted}; !slices.Equal(seen, want) {
		t.Fatalf("events %v, want %v", seen, want)
	}
	if err := s.Abandoned(ctx, "term_1"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.Capable(ctx, "term_1"); ok {
		t.Fatal("an abandoned session takes turns")
	}
}

func askedRecord() record.Record {
	return record.Record{
		State: record.Asked, Summary: "drafted APPROVE", Resume: "the draft is ready",
		Question: &record.Question{Kind: "choice", Text: "Publish the approval?", Recommend: "publish", Options: []record.Option{
			{ID: "publish", Label: "Publish", Consequence: "posts the approval", Publishes: true},
			{ID: "hold", Label: "Hold", Consequence: "keeps the draft"},
		}},
	}
}

// putTurn seeds turn abc on r7 started on term_1 at t0 minus an
// hour, with an acked asked record when asked.
func putTurn(store *turnstest.Store, asked bool, answers ...turn.Answer) {
	tu := turn.Turn{Seq: 1, Intent: turn.Intent(dintent.Review), ID: "abc", Trigger: turn.Automated, Head: "h1", SentTo: "term_1", Session: "term_1",
		State: turn.Started, SentAt: t0.Add(-2 * time.Hour), StartedAt: t0.Add(-time.Hour)}
	b := turn.Book{Unit: r7}
	if asked {
		tu.State = turn.Asked
		b.Records = []turn.Record{{ID: 1, TurnSeq: 1, Session: "term_1", Hash: "h", At: t0.Add(-30 * time.Minute), Acked: true, Record: askedRecord()}}
	}
	b.Turns = []turn.Turn{tu}
	b.Answers = answers
	store.Put(b)
}

func TestStopTable(t *testing.T) {
	typed := turn.Answer{ID: 1, TurnSeq: 1, Text: "go", Source: turn.Cockpit, Route: turn.Typed, Session: "term_1", At: t0.Add(-10 * time.Minute)}
	for _, c := range []struct {
		name       string
		turn       bool
		asked      bool
		unacked    bool
		answers    []turn.Answer
		prompts    []string
		wantBlocks []bool
		wantState  turn.State
	}{
		{name: "no turn", prompts: []string{"p1"}, wantBlocks: []bool{false}},
		{name: "record written", turn: true, asked: true, unacked: true, prompts: []string{"p1"}, wantBlocks: []bool{false}, wantState: turn.Asked},
		{name: "asked, the user chats", turn: true, asked: true, prompts: []string{"p2", "p3"}, wantBlocks: []bool{false, false}, wantState: turn.Asked},
		{name: "asked, " + brand.Name + " typed an answer here, no record", turn: true, asked: true, answers: []turn.Answer{typed}, prompts: []string{"p2", "p2"}, wantBlocks: []bool{true, false}, wantState: turn.Unrecorded},
		{name: "no record, first and second stop", turn: true, prompts: []string{"p1", "p1"}, wantBlocks: []bool{true, false}, wantState: turn.Unrecorded},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, store, reg, _, rec := newService(t)
			ctx := context.Background()
			reg.Put(turn.Session{ID: "term_1", AgentSession: "s1", StartedAt: t0.Add(-2 * time.Hour), MCPAt: t0.Add(-2 * time.Hour)})
			if c.turn {
				putTurn(store, c.asked, c.answers...)
				if c.unacked {
					b := bookOf(t, store, r7)
					b.Records[0].Acked = false
					store.Put(b)
				}
			}
			for i, p := range c.prompts {
				got, err := s.Stop(ctx, turns.StopRequest{Session: "term_1", AgentSession: "s1", PromptID: p, LastMessage: "Should I drop it?\nMore."})
				if err != nil {
					t.Fatal(err)
				}
				if got.Block != c.wantBlocks[i] {
					t.Fatalf("stop %d: blocked %v", i, got.Block)
				}
				if got.Block && got.Reason != "record_owed ScratchDir=.loop Turn=abc Unit=https://github.com/acme/api/pull/7" {
					t.Fatalf("reason %q", got.Reason)
				}
			}
			last, logged, _ := reg.LastStop(ctx, "term_1")
			if logged != c.turn {
				t.Fatalf("stop logged %v", logged)
			}
			if !c.turn {
				return
			}
			b := bookOf(t, store, r7)
			if b.Turns[0].State != c.wantState {
				t.Fatalf("state %s, want %s", b.Turns[0].State, c.wantState)
			}
			for _, r := range b.Records {
				if !r.Acked {
					t.Fatalf("a record left unacked: %+v", r)
				}
			}
			if c.wantState == turn.Unrecorded {
				r, _ := b.LastRecord(1)
				if r.Record.State != record.UnrecordedState || r.Record.Summary != "Should I drop it?" || last.RecordID != r.ID || last.Blocked {
					t.Fatalf("record %+v, stop %+v", r, last)
				}
				if !slices.Contains(rec.changes, r7) {
					t.Fatal("the views were not told")
				}
			}
			if c.unacked && last.RecordID != 1 {
				t.Fatalf("the stop does not name the record it acked: %+v", last)
			}
		})
	}

	t.Run("a new agent session id is a new start", func(t *testing.T) {
		s, _, reg, _, _ := newService(t)
		ctx := context.Background()
		reg.Put(turn.Session{ID: "term_1", AgentSession: "s1", StartedAt: t0.Add(-2 * time.Hour), MCPAt: t0.Add(-2 * time.Hour)})
		if ok, _ := s.Capable(ctx, "term_1"); !ok {
			t.Fatal("not capable before")
		}
		if got, err := s.Stop(ctx, turns.StopRequest{Session: "term_1", AgentSession: "s2", PromptID: "p1"}); err != nil || got.Block {
			t.Fatal(got, err)
		}
		if ok, _ := s.Capable(ctx, "term_1"); ok {
			t.Fatal("a new process counts as capable before its initialize")
		}
		s.MCPSeen(ctx, "term_1")
		if ok, _ := s.Capable(ctx, "term_1"); !ok {
			t.Fatal("not capable after its initialize")
		}
	})
}

func TestAnswerRoutes(t *testing.T) {
	type setup struct {
		s     *turns.Service
		store *turnstest.Store
		reg   *turnstest.Registry
		sess  *turnstest.Sessions
	}
	stoppedAs := func(st turn.State) func(setup) {
		return func(x setup) {
			b := bookOf(t, x.store, r7)
			b.Turns[0].State = st
			x.store.Put(b)
		}
	}
	confirmAsked := func(x setup) {
		b := bookOf(t, x.store, r7)
		b.Records[0].Record.Question = &record.Question{Kind: "confirm", Text: "Publish?"}
		x.store.Put(b)
	}
	warm := func(t *testing.T) (*turns.Service, *turnstest.Store, *turnstest.Registry, *turnstest.Sessions, *recorder) {
		s, store, reg, sess, rec := newService(t)
		putTurn(store, true)
		reg.Put(turn.Session{ID: "term_1", Pane: "tab-1:leaf-1", WorkspaceID: "ws", AgentSession: "s1", StartedAt: t0.Add(-2 * time.Hour), MCPAt: t0.Add(-2 * time.Hour)})
		sess.Put("ws", turns.Terminal{Session: "term_1", Key: "tab-1", LastOutput: t0.Add(-5 * time.Minute)})
		return s, store, reg, sess, rec
	}
	notice := "answered Gate=4 Unit=https://github.com/acme/api/pull/7"
	for _, c := range []struct {
		name      string
		arrange   func(setup)
		req       turns.AnswerRequest
		route     turn.Route
		refused   bool
		typedLine string
	}{
		{name: "warm and capable", req: turns.AnswerRequest{Text: "go ahead", Source: turn.Cockpit}, route: turn.Typed, typedLine: notice},
		{name: "working", arrange: func(x setup) {
			x.sess.SetAgents(turns.AgentObservation{WorkspaceID: "ws", Pane: "tab-1:leaf-1", State: turn.AgentWorking})
		}, req: turns.AnswerRequest{Text: "go ahead", Source: turn.Cockpit}, route: turn.Queued},
		{name: "waiting on a permission", arrange: func(x setup) {
			x.sess.SetAgents(turns.AgentObservation{WorkspaceID: "ws", Pane: "tab-1:leaf-1", State: turn.AgentWaiting})
		}, req: turns.AnswerRequest{Text: "go ahead", Source: turn.Cockpit}, route: turn.Queued},
		{name: "terminal gone", arrange: func(x setup) { x.sess.Put("ws") }, req: turns.AnswerRequest{Text: "go ahead", Source: turn.TUI}, route: turn.Resumed},
		{name: "orca unreadable", arrange: func(x setup) { x.sess.FailReads(errors.New("orca is down")) }, req: turns.AnswerRequest{Text: "go ahead", Source: turn.TUI}, route: turn.Resumed},
		{name: "a session with no workspace", arrange: func(x setup) {
			x.reg.Put(turn.Session{ID: "term_1", Pane: "tab-1:leaf-1", AgentSession: "s1", StartedAt: t0.Add(-2 * time.Hour), MCPAt: t0.Add(-2 * time.Hour)})
			x.sess.Put("", turns.Terminal{Session: "term_1", LastOutput: t0.Add(-5 * time.Minute)})
		}, req: turns.AnswerRequest{Text: "go ahead", Source: turn.TUI}, route: turn.Resumed},
		{name: "quiet 61 minutes", arrange: func(x setup) {
			x.sess.Put("ws", turns.Terminal{Session: "term_1", LastOutput: t0.Add(-61 * time.Minute)})
		}, req: turns.AnswerRequest{Text: "go ahead", Source: turn.TUI}, route: turn.Resumed},
		{name: "not capable", arrange: func(x setup) {
			x.reg.Put(turn.Session{ID: "term_1", Pane: "tab-1:leaf-1", WorkspaceID: "ws", StartedAt: t0.Add(-time.Minute), MCPAt: t0.Add(-2 * time.Hour)})
		}, req: turns.AnswerRequest{Text: "go ahead", Source: turn.TUI}, route: turn.Resumed},
		{name: "the agent took the line without submitting it", arrange: func(x setup) { x.sess.Hold("term_1") },
			req: turns.AnswerRequest{Text: "go ahead", Source: turn.Cockpit}, route: turn.Refused, refused: true, typedLine: notice},
		{name: "a failed turn resumes", arrange: stoppedAs(turn.Failed), req: turns.AnswerRequest{Text: "go ahead", Source: turn.Cockpit}, route: turn.Resumed},
		{name: "an unrecorded turn resumes", arrange: stoppedAs(turn.Unrecorded), req: turns.AnswerRequest{Text: "go ahead", Source: turn.Cockpit}, route: turn.Resumed},
		{name: "multi-line", req: turns.AnswerRequest{Text: "keep the column\nand drop the index", Source: turn.Cockpit}, route: turn.Typed, typedLine: notice},
		{name: "an option from the notification", req: turns.AnswerRequest{OptionID: "publish", Source: turn.Notification}, route: turn.Typed, typedLine: notice},
		{name: "yes from the TUI", arrange: confirmAsked, req: turns.AnswerRequest{Yes: true, Source: turn.TUI}, route: turn.Typed, typedLine: notice},
		{name: "no to a confirm", arrange: confirmAsked, req: turns.AnswerRequest{Source: turn.TUI}, route: turn.Typed, typedLine: notice},
		{name: "an explicit no", arrange: confirmAsked, req: turns.AnswerRequest{No: true, Source: turn.Notification}, route: turn.Typed, typedLine: notice},
		{name: "yes with a note", arrange: confirmAsked, req: turns.AnswerRequest{Yes: true, Text: "but thank them", Source: turn.Cockpit}, route: turn.Typed,
			typedLine: notice},
		{name: "no with a note", arrange: confirmAsked, req: turns.AnswerRequest{No: true, Text: "the draft misreads the diff", Source: turn.Cockpit}, route: turn.Typed,
			typedLine: notice},
		{name: "free text to a confirm", arrange: confirmAsked, req: turns.AnswerRequest{Text: "wait for CI first", Source: turn.Cockpit}, route: turn.Typed,
			typedLine: notice},
		{name: "an option with a note", req: turns.AnswerRequest{OptionID: "publish", Text: "shorten the summary", Source: turn.Cockpit}, route: turn.Typed,
			typedLine: notice},
		{name: "an option the client already worded", req: turns.AnswerRequest{OptionID: "publish", Text: "Publish (option publish)", Source: turn.Notification}, route: turn.Typed,
			typedLine: notice},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, store, reg, sess, rec := warm(t)
			ctx := context.Background()
			if c.arrange != nil {
				c.arrange(setup{s, store, reg, sess})
			}
			req := c.req
			req.Unit, req.Message, req.Gate = r7, 9, 4
			got, err := s.Answer(ctx, req)
			if c.refused != refusal(err, "no turn started") || (!c.refused && err != nil) {
				t.Fatalf("err %v", err)
			}
			if got.Route != c.route {
				t.Fatalf("route %+v, want %s", got, c.route)
			}
			b := bookOf(t, store, r7)
			if len(b.Answers) != 1 {
				t.Fatalf("answers %+v", b.Answers)
			}
			a := b.Answers[0]
			if a.Route != c.route || a.TurnSeq != 1 || a.Message != 9 || (a.Route == turn.Resumed) != !a.ReadAt.IsZero() || (a.Route == turn.Typed) != !a.NoticedAt.IsZero() || a.Text != req.Text || a.OptionID != req.OptionID || a.Yes != req.Yes || a.No != req.No || a.Source != req.Source {
				t.Fatalf("answer %+v", a)
			}
			typed := sess.Typed()
			switch c.route {
			case turn.Typed:
				if got.Handle != "term_1" || a.Session != "term_1" || len(typed) != 1 || typed[0] != (turnstest.Typed{Handle: "term_1", Line: c.typedLine}) {
					t.Fatalf("typed %+v, result %+v, answer %+v", typed, got, a)
				}
			case turn.Resumed:
				want := turns.Launch{Unit: r7, Intent: turn.Intent(dintent.Review), ResumeOf: 1, Answer: req.Text}
				if len(typed) != 0 || len(rec.launches) != 1 || !reflect.DeepEqual(rec.launches[0], want) || a.Session != "" {
					t.Fatalf("typed %+v, launches %+v", typed, rec.launches)
				}
				if len(kinds(store.Events(), turns.KindTurnResumed)) != 1 {
					t.Fatalf("events %+v", store.Events())
				}
			default:
				wantTyped := 0
				if c.typedLine != "" {
					wantTyped = 1
				}
				if len(typed) != wantTyped || len(rec.launches) != 0 || a.Session != "" {
					t.Fatalf("a refused answer went somewhere: %+v %+v", typed, rec.launches)
				}
			}
			answered, routed := kinds(store.Events(), turns.KindAnswered), kinds(store.Events(), turns.KindAnswerRouted)
			if len(answered) != 1 || len(routed) != 1 || routed[0].Key != "1:"+string(c.route) || !strings.HasPrefix(routed[0].Data, string(c.route)) {
				t.Fatalf("events %+v", store.Events())
			}
			if strings.Contains(answered[0].Data, "\n") {
				t.Fatalf("answered data %q", answered[0].Data)
			}
		})
	}

	t.Run("what the question does not take", func(t *testing.T) {
		for _, req := range []turns.AnswerRequest{{OptionID: "merge"}, {Yes: true}, {No: true, Text: "not now"}, {Text: "  "}} {
			s, store, _, sess, rec := warm(t)
			req.Unit, req.Source = r7, turn.Cockpit
			var invalid turns.InvalidAnswer
			if _, err := s.Answer(context.Background(), req); !errors.As(err, &invalid) {
				t.Fatalf("%+v: err %v", req, err)
			}
			if b := bookOf(t, store, r7); len(b.Answers) != 0 || len(sess.Typed()) != 0 || len(rec.launches) != 0 || len(store.Events()) != 0 {
				t.Fatalf("%+v was stored or sent: %+v", req, b.Answers)
			}
		}
	})

	t.Run("no turn", func(t *testing.T) {
		s, _, _, sess, _ := newService(t)
		if _, err := s.Answer(context.Background(), turns.AnswerRequest{Unit: r7, Text: "go", Source: turn.Cockpit}); !errors.Is(err, turn.ErrNoQuestion) {
			t.Fatalf("err %v", err)
		}
		if len(sess.Typed()) != 0 {
			t.Fatal("typed with no question")
		}
	})
}

func TestContinueAndDismiss(t *testing.T) {
	s, store, _, _, rec := newService(t)
	ctx := context.Background()
	if err := s.Continue(ctx, r7); !errors.Is(err, turn.ErrNoQuestion) {
		t.Fatalf("continue with no turn: %v", err)
	}
	putTurn(store, true)
	rec.launchErr = errors.New("the workspace holds commits not on the pull request")
	if err := s.Continue(ctx, r7); err == nil {
		t.Fatal("a refused launch continued")
	}
	if len(kinds(store.Events(), turns.KindTurnResumed)) != 0 {
		t.Fatalf("a refused launch wrote an event: %+v", store.Events())
	}
	rec.launchErr = nil
	if err := s.Continue(ctx, r7); err != nil {
		t.Fatal(err)
	}
	s.Clock.(*clock).advance(time.Second)
	if err := s.Continue(ctx, r7); err != nil {
		t.Fatal(err)
	}
	if want := (turns.Launch{Unit: r7, Intent: turn.Intent(dintent.Review), ResumeOf: 1}); len(rec.launches) != 2 || !reflect.DeepEqual(rec.launches[0], want) {
		t.Fatalf("launches %+v", rec.launches)
	}
	if len(kinds(store.Events(), turns.KindTurnResumed)) != 2 {
		t.Fatalf("events %+v", store.Events())
	}
	if err := s.Dismiss(ctx, r7); err != nil {
		t.Fatal(err)
	}
	if b := bookOf(t, store, r7); b.Turns[0].State != turn.Dismissed || !b.Turns[0].EndedAt.Equal(s.Clock.Now()) {
		t.Fatalf("turn %+v", b.Turns[0])
	}
	if err := s.Dismiss(ctx, r7); !errors.Is(err, turn.ErrNoQuestion) {
		t.Fatalf("dismissed twice: %v", err)
	}
	if err := s.Continue(ctx, r7); !errors.Is(err, turn.ErrNoQuestion) {
		t.Fatalf("continued a dismissed turn: %v", err)
	}
	if evs := kinds(store.Events(), turns.KindQuestionDismissed); len(evs) != 1 || evs[0].Key != "abc" {
		t.Fatalf("events %+v", evs)
	}
}

// racing stores a record of round c the first time the service re-reads its
// pull request, as if the agent's record arrived between the read and the
// write.
type racing struct {
	*turnstest.Store
	ref  unit.Unit
	once sync.Once
}

func (r *racing) Books(ctx context.Context, refs ...unit.Unit) (map[unit.Unit]turn.Book, error) {
	if len(refs) == 1 && refs[0] == r.ref {
		r.once.Do(func() {
			books, _ := r.Store.Books(ctx, r.ref)
			b := books[r.ref]
			b.Record(turn.Record{TurnSeq: b.Turns[0].Seq, Session: "term_3", Hash: "agent", At: t0,
				Record: record.Record{State: record.Done, Summary: "the agent's own record"}})
			if _, err := r.Store.Save(ctx, b, port.Event{Unit: r.ref, Kind: turns.KindTurnEnded, Key: "c:agent", At: t0}); err != nil {
				panic(err)
			}
		})
	}
	return r.Store.Books(ctx, refs...)
}

func started(n int, intent turn.Intent, round, handle string) turn.Book {
	return turn.Book{Unit: ref(n), Turns: []turn.Turn{{Seq: int64(n), Intent: intent, ID: round, Trigger: turn.Automated, SentTo: handle, Session: handle,
		State: turn.Started, SentAt: t0.Add(-2 * time.Hour), StartedAt: t0.Add(-time.Hour)}}}
}

func TestReconcileAtStartup(t *testing.T) {
	s, store, reg, sess, rec := newService(t)
	ctx := context.Background()
	s.Store = &racing{Store: store, ref: ref(9)}
	long := t0.Add(-10 * time.Minute)

	store.Put(started(8, turn.Intent(dintent.Review), "a", "term_1"))
	reg.Put(turn.Session{ID: "term_1", WorkspaceID: "ws1", Pane: "tab-1:leaf-1"})

	store.Put(started(10, turn.Intent(dintent.Address), "b", "term_2"))
	reg.Put(turn.Session{ID: "term_2", WorkspaceID: "ws2", Pane: "tab-2:leaf-1"})
	sess.Put("ws2", turns.Terminal{Session: "term_2", LastOutput: long})

	store.Put(started(9, turn.Intent(dintent.Review), "c", "term_3"))
	reg.Put(turn.Session{ID: "term_3", WorkspaceID: "ws3", Pane: "tab-3:leaf-1"})

	never := turn.Book{Unit: ref(11), Turns: []turn.Turn{{Seq: 11, Intent: turn.Intent(dintent.Review), ID: "d", Trigger: turn.Automated, SentTo: "term_4", State: turn.Sent, SentAt: t0.Add(-11 * time.Minute)}}}
	store.Put(never)
	reg.Put(turn.Session{ID: "term_4", WorkspaceID: "ws4", Pane: "tab-4:leaf-1"})
	sess.Put("ws4", turns.Terminal{Session: "term_4", LastOutput: long})

	queued := turn.Book{Unit: ref(12), Turns: []turn.Turn{{Seq: 12, Intent: turn.Intent(dintent.Review), ID: "e", Trigger: turn.Automated, SentTo: "term_5", State: turn.Sent, SentAt: t0.Add(-11 * time.Minute)}}}
	store.Put(queued)
	reg.Put(turn.Session{ID: "term_5", WorkspaceID: "ws5"})
	sess.Put("ws5", turns.Terminal{Session: "term_5", LastOutput: long})

	asked := turn.Book{Unit: r7, Turns: []turn.Turn{{Seq: 7, Intent: turn.Intent(dintent.Review), ID: "f", Trigger: turn.Automated, SentTo: "term_6", Session: "term_6",
		State: turn.Asked, SentAt: t0.Add(-2 * time.Hour), StartedAt: t0.Add(-2 * time.Hour)}},
		Records: []turn.Record{{ID: 70, TurnSeq: 7, Session: "term_6", Hash: "q", At: t0.Add(-time.Hour), Acked: true, Record: askedRecord()}}}
	store.Put(asked)
	reg.Put(turn.Session{ID: "term_6", WorkspaceID: "ws6", Pane: "tab-6:leaf-1"})
	sess.Put("ws6", turns.Terminal{Session: "term_6", LastOutput: long})
	rec.reviews[r7] = []turns.SourceAnswer{{ID: "98", State: "COMMENTED", Verdict: "COMMENT", At: t0.Add(-2 * time.Hour)}, {ID: "99", State: "APPROVED", Verdict: "APPROVE", URL: "https://github.com/acme/api/pull/7#pullrequestreview-99", At: t0.Add(-30 * time.Minute)}}

	sess.SetAgents(
		turns.AgentObservation{WorkspaceID: "ws2", Pane: "tab-2:leaf-1", State: turn.AgentDone, LastMessage: "Keep the column?\nOr drop it."},
		turns.AgentObservation{WorkspaceID: "ws5", Pane: "tab-5:leaf-1", State: turn.AgentWorking},
	)

	rep, err := s.Reconcile(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	last := func(n int) (turn.Turn, record.Record) {
		b := bookOf(t, store, ref(n))
		if r, ok := b.LastRecord(b.Turns[0].Seq); ok {
			return b.Turns[0], r.Record
		}
		return b.Turns[0], record.Record{}
	}
	for _, c := range []struct {
		n       int
		state   turn.State
		summary string
	}{
		{8, turn.Unrecorded, "the session's process is gone"},
		{10, turn.Unrecorded, "Keep the column?"},
		{9, turn.Done, "the agent's own record"},
		{11, turn.Unrecorded, "the agent never started the turn"},
		{12, turn.Sent, ""},
	} {
		if tu, r := last(c.n); tu.State != c.state || r.Summary != c.summary {
			t.Errorf("acme/api#%d: %s %q, want %s %q", c.n, tu.State, r.Summary, c.state, c.summary)
		}
	}
	tu, r := last(7)
	if tu.State != turn.Done || r.State != record.Done || r.Outcome == nil || r.Outcome.Verdict != "APPROVE" || r.Outcome.URL != "https://github.com/acme/api/pull/7#pullrequestreview-99" {
		t.Fatalf("asked review: %s %+v", tu.State, r)
	}
	if want := []unit.Unit{ref(8), ref(10), ref(11)}; !sameRefs(rep.Unrecorded, want) {
		t.Errorf("unrecorded %v, want %v", rep.Unrecorded, want)
	}
	if !slices.Equal(rep.Closed, []unit.Unit{r7}) {
		t.Errorf("closed %v", rep.Closed)
	}
	if !slices.Equal(rep.GoneSessions, []string{"term_1", "term_3"}) {
		t.Errorf("gone %v", rep.GoneSessions)
	}
	if !sameRefs(rec.changes, []unit.Unit{r7, ref(8), ref(10), ref(11)}) {
		t.Errorf("changed %v", rec.changes)
	}
}

func sameRefs(got, want []unit.Unit) bool {
	cmp := func(a, b unit.Unit) int { return strings.Compare(a.Key, b.Key) }
	return slices.Equal(slices.SortedFunc(slices.Values(got), cmp), slices.SortedFunc(slices.Values(want), cmp))
}

func TestReconcileFastJob(t *testing.T) {
	for _, c := range []struct {
		name       string
		runs       int
		lastOutput time.Duration
		stop       bool
		want       turn.State
	}{
		{name: "done once", runs: 1, lastOutput: -10 * time.Minute, want: turn.Started},
		{name: "done twice, output a minute ago", runs: 2, lastOutput: -time.Minute, want: turn.Started},
		{name: "done twice, quiet, no stop", runs: 2, lastOutput: -10 * time.Minute, want: turn.Unrecorded},
		{name: "done twice, quiet, a stop since", runs: 2, lastOutput: -10 * time.Minute, stop: true, want: turn.Started},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, store, reg, sess, _ := newService(t)
			ctx := context.Background()
			store.Put(started(7, turn.Intent(dintent.Review), "abc", "term_1"))
			reg.Put(turn.Session{ID: "term_1", WorkspaceID: "ws", Pane: "tab-1:leaf-1"})
			sess.Put("ws", turns.Terminal{Session: "term_1", LastOutput: t0.Add(c.lastOutput)})
			sess.SetAgents(turns.AgentObservation{WorkspaceID: "ws", Pane: "tab-1:leaf-1", State: turn.AgentDone, LastMessage: "Keep the column?"})
			if c.stop {
				stop := turn.Stop{Session: "term_1", PromptID: "p1", At: t0.Add(-5 * time.Minute), Blocked: true}
				if err := reg.LogStop(ctx, stop, port.Event{Kind: turns.KindStopBlocked, Key: "term_1:p1", At: stop.At}); err != nil {
					t.Fatal(err)
				}
			}
			var rep turns.Report
			for range c.runs {
				var err error
				if rep, err = s.Reconcile(ctx, false); err != nil {
					t.Fatal(err)
				}
			}
			b := bookOf(t, store, r7)
			if b.Turns[0].State != c.want {
				t.Fatalf("state %s, want %s", b.Turns[0].State, c.want)
			}
			if c.want == turn.Unrecorded {
				if r, _ := b.LastRecord(7); r.Record.Summary != "Keep the column?" || !r.Acked || !slices.Equal(rep.Unrecorded, []unit.Unit{r7}) {
					t.Fatalf("record %+v, report %+v", r, rep)
				}
			}
		})
	}

	t.Run("a session Orca no longer lists waits for start-up", func(t *testing.T) {
		s, store, reg, _, _ := newService(t)
		store.Put(started(7, turn.Intent(dintent.Review), "abc", "term_1"))
		reg.Put(turn.Session{ID: "term_1", WorkspaceID: "ws", Pane: "tab-1:leaf-1"})
		if _, err := s.Reconcile(context.Background(), false); err != nil {
			t.Fatal(err)
		}
		if b := bookOf(t, store, r7); b.Turns[0].State != turn.Started {
			t.Fatalf("state %s", b.Turns[0].State)
		}
	})
}

func TestALateRecordNamesTheTurnThatReplacedIt(t *testing.T) {
	s, _, _, _, _ := newService(t)
	ctx := context.Background()
	s.Send(ctx, turns.SendRequest{Unit: r7, Intent: turn.Intent(dintent.Review), Turn: "abc", Trigger: turn.Automated, Records: true})
	s.SentTo(ctx, r7, "abc", "term_1")
	s.Start(ctx, "term_1", "abc")
	s.Send(ctx, turns.SendRequest{Unit: r7, Intent: turn.Intent(dintent.Review), Turn: "def", Trigger: turn.Automated, Records: true})
	if err := s.Withdraw(ctx, r7, "def", "orca is down"); err != nil {
		t.Fatal(err)
	}
	s.Send(ctx, turns.SendRequest{Unit: r7, Intent: turn.Intent(dintent.Review), Turn: "ghi", Trigger: turn.Automated, Records: true})
	s.SentTo(ctx, r7, "ghi", "term_2")
	got, err := s.RecordTurnEnd(ctx, turns.RecordRequest{Session: "term_1", Turn: "abc", Record: record.Record{State: record.Settled, Summary: "nothing new"}})
	if err != nil || got.Note != "this turn was replaced by turn ghi; your record changes nothing" {
		t.Fatalf("a late record: %+v %v", got, err)
	}
}

func TestRecordTurnEndOnAReplacedOrDismissedTurn(t *testing.T) {
	s, store, _, _, rec := newService(t)
	ctx := context.Background()
	s.Send(ctx, turns.SendRequest{Unit: r7, Intent: turn.Intent(dintent.Review), Turn: "abc", Trigger: turn.Automated, Records: true})
	s.SentTo(ctx, r7, "abc", "term_1")
	s.Start(ctx, "term_1", "abc")
	s.Send(ctx, turns.SendRequest{Unit: r7, Intent: turn.Intent(dintent.Review), Turn: "def", Trigger: turn.Automated, Records: true})
	s.SentTo(ctx, r7, "def", "term_1")
	changes, ended := len(rec.changes), len(kinds(store.Events(), turns.KindTurnEnded))
	settled := record.Record{State: record.Settled, Summary: "nothing new"}
	got, err := s.RecordTurnEnd(ctx, turns.RecordRequest{Session: "term_1", Turn: "abc", Record: settled})
	if err != nil || !got.Added || got.Note != "this turn was replaced by turn def; your record changes nothing" {
		t.Fatalf("a late record: %+v %v", got, err)
	}
	b := bookOf(t, store, r7)
	if b.Turns[0].State != turn.Replaced || len(b.Records) != 1 || b.Records[0].TurnSeq != b.Turns[0].Seq {
		t.Fatalf("book %+v", b)
	}
	if len(rec.changes) != changes || len(kinds(store.Events(), turns.KindTurnEnded)) != ended || len(kinds(store.Events(), turns.KindLateRecord)) != 1 {
		t.Fatalf("a late record told the views: changes %v, events %+v", rec.changes, store.Events())
	}
	if slices.ContainsFunc(rec.published, func(e port.Event) bool { return e.Kind == turns.KindLateRecord }) {
		t.Fatal("a late record was published")
	}

	if _, err := s.RecordTurnEnd(ctx, turns.RecordRequest{Session: "term_1", Turn: "def", Record: record.Record{State: record.Asked, Summary: "q", Resume: "r",
		Question: &record.Question{Kind: "free", Text: "Keep it?"}}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Dismiss(ctx, r7); err != nil {
		t.Fatal(err)
	}
	got, err = s.RecordTurnEnd(ctx, turns.RecordRequest{Session: "term_1", Turn: "def", Record: settled})
	if err != nil || !got.Added || got.Note != "this turn was dismissed; your record changes nothing" {
		t.Fatalf("a record after dismiss: %+v %v", got, err)
	}
	if b := bookOf(t, store, r7); b.Turns[1].State != turn.Dismissed {
		t.Fatalf("state %s", b.Turns[1].State)
	}
}

// brokenSessions fails to list the terminals of one workspace.
type brokenSessions struct {
	*turnstest.Sessions
	workspace string
}

func (b brokenSessions) Terminals(ctx context.Context, workspaceID string) ([]turns.Terminal, error) {
	if workspaceID == b.workspace {
		return nil, errors.New("orca: timeout")
	}
	return b.Sessions.Terminals(ctx, workspaceID)
}

func TestReconcileSurvivesAWorkspaceItCannotRead(t *testing.T) {
	s, store, reg, sess, _ := newService(t)
	ctx := context.Background()
	s.Sessions = brokenSessions{Sessions: sess, workspace: "ws-broken"}
	store.Put(started(8, turn.Intent(dintent.Review), "a", "term_1"))
	reg.Put(turn.Session{ID: "term_1", WorkspaceID: "ws-broken", Pane: "tab-1:leaf-1"})
	store.Put(started(10, turn.Intent(dintent.Address), "b", "term_2"))
	reg.Put(turn.Session{ID: "term_2", WorkspaceID: "ws2", Pane: "tab-2:leaf-1"})
	sess.Put("ws2", turns.Terminal{Session: "term_2", LastOutput: t0.Add(-10 * time.Minute)})
	sess.SetAgents(turns.AgentObservation{WorkspaceID: "ws2", Pane: "tab-2:leaf-1", State: turn.AgentDone, LastMessage: "Keep the column?"})

	rep, err := s.Reconcile(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Warnings) != 1 || !strings.Contains(rep.Warnings[0], "ws-broken") {
		t.Fatalf("warnings %v", rep.Warnings)
	}
	if b := bookOf(t, store, ref(8)); b.Turns[0].State != turn.Started {
		t.Fatalf("a fast job closed a turn of an unreadable workspace: %s", b.Turns[0].State)
	}
	s.Clock.(*clock).advance(time.Second)
	if rep, err = s.Reconcile(ctx, true); err != nil {
		t.Fatal(err)
	}
	if !sameRefs(rep.Unrecorded, []unit.Unit{ref(8), ref(10)}) || !slices.Equal(rep.GoneSessions, []string{"term_1"}) {
		t.Fatalf("report %+v", rep)
	}
}

func TestReconcileNeverStartedWaitsTheGracePlusAFastJob(t *testing.T) {
	s, store, reg, _, _ := newService(t)
	ctx := context.Background()
	store.Put(turn.Book{Unit: r7, Turns: []turn.Turn{{Seq: 1, Intent: turn.Intent(dintent.Review), ID: "abc", Trigger: turn.Automated, SentTo: "term_1",
		State: turn.Sent, SentAt: t0.Add(-turns.SentGrace - s.FastJob + time.Second)}}})
	reg.Put(turn.Session{ID: "term_1", WorkspaceID: "ws", Pane: "tab-1:leaf-1"})
	if _, err := s.Reconcile(ctx, false); err != nil {
		t.Fatal(err)
	}
	if b := bookOf(t, store, r7); b.Turns[0].State != turn.Sent {
		t.Fatalf("closed before the grace: %s", b.Turns[0].State)
	}
	s.Clock.(*clock).advance(time.Second)
	if _, err := s.Reconcile(ctx, false); err != nil {
		t.Fatal(err)
	}
	if b := bookOf(t, store, r7); b.Turns[0].State != turn.Unrecorded {
		t.Fatalf("state %s", b.Turns[0].State)
	}
}

type onlyDone struct{ turns.Intents }

func (onlyDone) Rules(string) (dintent.RecordRules, error) {
	return dintent.RecordRules{States: []string{"done"}, Artifacts: []string{"note"}, Outcome: []string{"url"}, Require: []dintent.Require{{State: "done", Fields: []string{"url"}}}}, nil
}

func startedTurn(t *testing.T, s *turns.Service, intent turn.Intent) {
	t.Helper()
	ctx := context.Background()
	if _, err := s.Send(ctx, turns.SendRequest{Unit: r7, Intent: intent, Turn: "abc", Trigger: turn.Automated, Records: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.SentTo(ctx, r7, "abc", "term_1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Start(ctx, "term_1", "abc"); err != nil {
		t.Fatal(err)
	}
}

func TestRecordTurnEndValidatesWithThePortsRules(t *testing.T) {
	s, _, _, _, _ := newService(t)
	s.Intents = onlyDone{s.Intents}
	startedTurn(t, s, "review")
	ctx := context.Background()
	asked := record.Record{State: record.Asked, Summary: "s", Resume: "r", Question: &record.Question{Kind: "free", Text: "q"}}
	if _, err := s.RecordTurnEnd(ctx, turns.RecordRequest{Session: "term_1", Turn: "abc", Record: asked}); err == nil || !strings.Contains(err.Error(), `state: done, not "asked"`) {
		t.Fatalf("a state the port does not list: %v", err)
	}
	done := record.Record{State: record.Done, Summary: "s"}
	if _, err := s.RecordTurnEnd(ctx, turns.RecordRequest{Session: "term_1", Turn: "abc", Record: done}); err == nil || !strings.Contains(err.Error(), "outcome.url") {
		t.Fatalf("a field the port requires: %v", err)
	}
	done.Outcome = &record.Outcome{URL: "https://github.com/acme/api/pull/7#c1"}
	if got, err := s.RecordTurnEnd(ctx, turns.RecordRequest{Session: "term_1", Turn: "abc", Record: done}); err != nil || !got.Added {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestAnUnknownIntentsRecordIsRefused(t *testing.T) {
	s, _, _, _, _ := newService(t)
	startedTurn(t, s, "mystery")
	_, err := s.RecordTurnEnd(context.Background(), turns.RecordRequest{Session: "term_1", Turn: "abc", Record: record.Record{State: record.Done, Summary: "s"}})
	if !refusal(err, `"mystery"`) {
		t.Fatalf("%v", err)
	}
}

type brokenIntents struct{ *dintent.Registry }

func (brokenIntents) Rules(intent string) (dintent.RecordRules, error) {
	return dintent.RecordRules{}, fmt.Errorf("the intent %q does not load: intent.toml:3:1: key: x is the key of review", intent)
}

func TestABrokenIntentsRecordIsRefusedWithItsProblem(t *testing.T) {
	s, _, _, _, _ := newService(t)
	s.Intents = brokenIntents{turnstest.Intents()}
	startedTurn(t, s, "ask-author")
	_, err := s.RecordTurnEnd(context.Background(), turns.RecordRequest{Session: "term_1", Turn: "abc", Record: record.Record{State: record.Done, Summary: "s"}})
	if !refusal(err, `intent.toml:3:1: key: x is the key of review`) {
		t.Fatalf("%v", err)
	}
}

func TestANilIntentsPortIsAWiringError(t *testing.T) {
	s, _, _, _, _ := newService(t)
	startedTurn(t, s, "review")
	s.Intents = nil
	ctx := context.Background()
	if _, err := s.RecordTurnEnd(ctx, turns.RecordRequest{Session: "term_1", Turn: "abc", Record: record.Record{State: record.Done, Summary: "s"}}); !errors.Is(err, turns.ErrNoIntents) {
		t.Fatalf("turn end: %v", err)
	}
	if err := s.Dismiss(ctx, r7); !errors.Is(err, turns.ErrNoIntents) {
		t.Fatalf("dismiss: %v", err)
	}
	if err := s.Continue(ctx, r7); !errors.Is(err, turns.ErrNoIntents) {
		t.Fatalf("continue: %v", err)
	}
	if err := s.KeepSessions(ctx); !errors.Is(err, turns.ErrNoIntents) {
		t.Fatalf("keep sessions: %v", err)
	}
}

func TestReconcileFollowsTheManifest(t *testing.T) {
	s, store, reg, sess, rec := newService(t)
	ctx := context.Background()
	var defs []dintent.Def
	for _, d := range turnstest.Intents().All() {
		switch d.Name {
		case dintent.Review:
			d.Reconcile = ""
		case dintent.Address:
			d.Reconcile = "my_review"
		}
		defs = append(defs, d)
	}
	s.Intents = dintent.NewRegistry(defs)
	long := t0.Add(-time.Hour)
	for n, intent := range map[int]turn.Intent{7: turn.Intent(dintent.Review), 8: turn.Intent(dintent.Address)} {
		handle := fmt.Sprintf("term_%d", n)
		store.Put(turn.Book{Unit: ref(n), Turns: []turn.Turn{{Seq: int64(n), Intent: intent, ID: handle, Trigger: turn.Automated, SentTo: handle, Session: handle,
			State: turn.Asked, SentAt: t0.Add(-2 * time.Hour), StartedAt: t0.Add(-2 * time.Hour)}},
			Records: []turn.Record{{ID: int64(n * 10), TurnSeq: int64(n), Session: handle, Hash: "q", At: t0.Add(-time.Hour), Acked: true, Record: askedRecord()}}})
		reg.Put(turn.Session{ID: handle, WorkspaceID: "ws" + handle, Pane: "tab:" + handle})
		sess.Put("ws"+handle, turns.Terminal{Session: handle, LastOutput: long})
		rec.reviews[ref(n)] = []turns.SourceAnswer{{ID: "99", State: "APPROVED", Verdict: "APPROVE", At: t0.Add(-30 * time.Minute)}}
	}
	if _, err := s.Reconcile(ctx, true); err != nil {
		t.Fatal(err)
	}
	if b := bookOf(t, store, ref(8)); b.Turns[0].State != turn.Done {
		t.Fatalf("the address turn whose manifest reconciles by my review: %s", b.Turns[0].State)
	}
	if b := bookOf(t, store, ref(7)); b.Turns[0].State != turn.Asked {
		t.Fatalf("the review turn whose manifest reconciles nothing: %s", b.Turns[0].State)
	}
}

func TestALaunchCarriesANonGitHubUnit(t *testing.T) {
	s, store, _, _, rec := newService(t)
	thread := unit.Unit{Provider: "thread", Key: "C1 thread 9"}
	store.Put(turn.Book{Unit: thread, Turns: []turn.Turn{{Seq: 1, Intent: turn.Intent(dintent.Review), ID: "abc", Trigger: turn.Automated, SentTo: "term_1", Session: "term_1",
		State: turn.Asked, SentAt: t0.Add(-2 * time.Hour)}}, Records: []turn.Record{{ID: 1, TurnSeq: 1, Session: "term_1", Hash: "h", At: t0.Add(-30 * time.Minute), Acked: true, Record: askedRecord()}}})

	if err := s.Continue(context.Background(), thread); err != nil {
		t.Fatal(err)
	}
	if want := (turns.Launch{Unit: thread, Intent: turn.Intent(dintent.Review), ResumeOf: 1}); len(rec.launches) != 1 || !reflect.DeepEqual(rec.launches[0], want) {
		t.Fatalf("launches %+v", rec.launches)
	}
	if got := bookOf(t, store, r7); len(got.Turns) != 0 {
		t.Fatalf("the launch touched another unit: %+v", got)
	}
	if evs := kinds(store.Events(), turns.KindTurnResumed); len(evs) != 1 || evs[0].Unit != thread {
		t.Fatalf("events %+v", store.Events())
	}
}

func TestSendKeepsTheItemsAndTaskItWasAskedAbout(t *testing.T) {
	s, store, _, _, _ := newService(t)
	ctx := context.Background()
	if _, err := s.Send(ctx, turns.SendRequest{Unit: r7, Intent: turn.Intent(dintent.Review), Turn: "abc", Trigger: turn.ByHand, Records: true,
		Inputs: map[string]any{"depth": "deep"}, Items: []string{"i1", "i2"}, Task: "e-1"}); err != nil {
		t.Fatal(err)
	}
	got := bookOf(t, store, r7).Turns[0]
	if !slices.Equal(turns.ItemsOf(got), []string{"i1", "i2"}) || turns.TaskOf(got) != "e-1" || got.Inputs["depth"] != "deep" {
		t.Fatalf("turn %+v", got)
	}
}
