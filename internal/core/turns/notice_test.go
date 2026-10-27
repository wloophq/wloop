package turns_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/turns"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

type notices struct{ got []map[string]any }

func (n *notices) Notice(name string, data map[string]any) (string, error) {
	n.got = append(n.got, data)
	return name + " " + data["Unit"].(string), nil
}

func TestAnAnswerWhileTheSessionWorksBlocksItsStopWithTheNotice(t *testing.T) {
	s, store, _, sess, _ := busyAsked(t)
	ctx := context.Background()
	got, err := s.Answer(ctx, turns.AnswerRequest{Unit: r7, Message: 12, Text: "go ahead", Source: turn.Cockpit})
	if err != nil || got.Route != turn.Queued {
		t.Fatalf("%+v %v", got, err)
	}
	stop, err := s.Stop(ctx, turns.StopRequest{Session: "term_1", AgentSession: "s1", PromptID: "p1"})
	if err != nil || !stop.Block || stop.Reason != "answered Message=12 Unit=https://github.com/acme/api/pull/7" {
		t.Fatalf("stop %+v %v", stop, err)
	}
	b := bookOf(t, store, r7)
	if a := b.Answers[0]; a.Route != turn.Hooked || a.Session != "term_1" || !a.ReadAt.IsZero() || !a.NoticedAt.Equal(t0) {
		t.Fatalf("answer %+v", a)
	}
	if last, ok, _ := turns.LastStop(ctx, s.Registry, "term_1"); !ok || last.Blocked {
		t.Fatalf("the answer's block counts as the record's: %+v", last)
	}
	sess.SetAgents()
	if err := s.DeliverStopped(ctx, "term_1"); err != nil || len(sess.Typed()) != 0 {
		t.Fatalf("typed %+v %v", sess.Typed(), err)
	}
}

func TestAStopLeavesAnAnswerADeliverIsRouting(t *testing.T) {
	s, store, _, _, _ := busyAsked(t)
	ctx := context.Background()
	if _, err := s.Answer(ctx, turns.AnswerRequest{Unit: r7, Message: 12, Text: "go ahead", Source: turn.Cockpit}); err != nil {
		t.Fatal(err)
	}
	s.Routing(bookOf(t, store, r7).Answers[0].ID)
	if got, err := s.Stop(ctx, turns.StopRequest{Session: "term_1", AgentSession: "s1", PromptID: "p1"}); err != nil || strings.HasPrefix(got.Reason, "answered") {
		t.Fatalf("stop %+v %v", got, err)
	}
	if r := bookOf(t, store, r7).Answers[0].Route; r != turn.Queued {
		t.Fatalf("route %s", r)
	}
}

func TestAStopBlocksOnlyForTheAnswersOfItsSession(t *testing.T) {
	s, store, _, _, _ := busyAsked(t)
	ctx := context.Background()
	if _, err := s.Answer(ctx, turns.AnswerRequest{Unit: r7, Message: 12, Text: "go ahead", Source: turn.Cockpit}); err != nil {
		t.Fatal(err)
	}
	b := bookOf(t, store, r7)
	b.Turns[0].Session = "term_2"
	store.Put(b)
	if stop, err := s.Stop(ctx, turns.StopRequest{Session: "term_1", AgentSession: "s1", PromptID: "p1"}); err != nil || stop.Block {
		t.Fatalf("stop %+v %v", stop, err)
	}
}

func TestANoticeNamesAUnitThatIsNotAPullRequestAsItsCommandsReadIt(t *testing.T) {
	s, store, reg, sess, _ := newService(t)
	n := &notices{}
	s.Notices = n
	thread := unit.Unit{Provider: "chat", Key: "C1/1700000000.000100"}
	store.Put(turn.Book{Unit: thread, Turns: []turn.Turn{{Seq: 1, Intent: turn.Intent(dintent.Probe), ID: "abc", SentTo: "term_1", Session: "term_1",
		State: turn.Started, SentAt: t0.Add(-2 * time.Hour), StartedAt: t0.Add(-time.Hour)}}})
	reg.Put(turn.Session{ID: "term_1", Pane: "tab-1:leaf-1", WorkspaceID: "ws", AgentSession: "s1", StartedAt: t0.Add(-2 * time.Hour), MCPAt: t0.Add(-2 * time.Hour)})
	sess.Put("ws", turns.Terminal{Session: "term_1", Key: "tab-1", LastOutput: t0.Add(-time.Minute)})
	ctx := context.Background()
	if _, err := s.Answer(ctx, turns.AnswerRequest{Unit: thread, Message: 3, Posted: true, Turn: 1, Text: "yes", Source: turn.Cockpit}); err != nil {
		t.Fatal(err)
	}
	if stop, err := s.Stop(ctx, turns.StopRequest{Session: "term_1", AgentSession: "s1", PromptID: "p1"}); err != nil || !stop.Block {
		t.Fatalf("stop %+v %v", stop, err)
	}
	want := "chat:C1/1700000000.000100"
	if len(n.got) != 2 || n.got[0]["Unit"] != want || n.got[0]["Message"] != int64(3) || n.got[1]["Unit"] != want || n.got[1]["Turn"] != "abc" ||
		n.got[1]["Command"] != s.Command {
		t.Fatalf("notices read %+v", n.got)
	}
}

func TestMarkReadKeepsTheFirstRead(t *testing.T) {
	s, store, _, _, _ := newService(t)
	c := fixed(t0)
	s.Clock = c
	putTurn(store, true, turn.Answer{ID: 1, TurnSeq: 1, Text: "a", Route: turn.Typed, At: t0, Message: 4},
		turn.Answer{ID: 2, TurnSeq: 1, Text: "b", Route: turn.Typed, At: t0, Message: 5, ReadAt: t0.Add(-time.Minute)})
	c.advance(time.Hour)
	if err := s.MarkRead(context.Background(), r7, []int64{1, 2}); err != nil {
		t.Fatal(err)
	}
	b := bookOf(t, store, r7)
	if !b.Answers[0].ReadAt.Equal(t0.Add(time.Hour)) || !b.Answers[1].ReadAt.Equal(t0.Add(-time.Minute)) {
		t.Fatalf("answers %+v", b.Answers)
	}
}

func TestAfterTheNoticeTheTurnOwesARecordAgain(t *testing.T) {
	s, _, _, _, _ := busyAsked(t)
	ctx := context.Background()
	if _, err := s.Answer(ctx, turns.AnswerRequest{Unit: r7, Message: 12, Text: "go ahead", Source: turn.Cockpit}); err != nil {
		t.Fatal(err)
	}
	stop := func(prompt string) turns.StopAnswer {
		t.Helper()
		got, err := s.Stop(ctx, turns.StopRequest{Session: "term_1", AgentSession: "s1", PromptID: prompt})
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := stop("p1"); !got.Block || !strings.HasPrefix(got.Reason, "answered") {
		t.Fatalf("first stop %+v", got)
	}
	if got := stop("p1"); !got.Block || !strings.HasPrefix(got.Reason, "record_owed") {
		t.Fatalf("a stop after acting on the answer with no record: %+v", got)
	}
	if got := stop("p1"); got.Block {
		t.Fatalf("the record was asked for twice in one prompt: %+v", got)
	}
}

func TestTheRecordBlockOfAPromptSurvivesANoticeInBetween(t *testing.T) {
	s, store, _, sess, _ := busyAsked(t)
	ctx := context.Background()
	b := bookOf(t, store, r7)
	b.Answers = append(b.Answers, turn.Answer{ID: 1, TurnSeq: 1, Text: "go", Source: turn.Cockpit, Route: turn.Typed, Session: "term_1", At: t0})
	store.Put(b)
	stop := func() turns.StopAnswer {
		t.Helper()
		got, err := s.Stop(ctx, turns.StopRequest{Session: "term_1", AgentSession: "s1", PromptID: "p1"})
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := stop(); !got.Block || !strings.HasPrefix(got.Reason, "record_owed") {
		t.Fatalf("first stop %+v", got)
	}
	sess.SetAgents(working)
	if _, err := s.Answer(ctx, turns.AnswerRequest{Unit: r7, Message: 12, Text: "and this", Source: turn.Cockpit}); err != nil {
		t.Fatal(err)
	}
	if got := stop(); !got.Block || !strings.HasPrefix(got.Reason, "answered") {
		t.Fatalf("the notice stop %+v", got)
	}
	if got := stop(); got.Block {
		t.Fatalf("the record was asked for again in the same prompt: %+v", got)
	}
}

func TestANoticeBeforeTheRecordIsAckedStillOwesARecord(t *testing.T) {
	s, store, _, _, _ := busyAsked(t)
	ctx := context.Background()
	b := bookOf(t, store, r7)
	b.Records[0].Acked = false
	store.Put(b)
	if _, err := s.Answer(ctx, turns.AnswerRequest{Unit: r7, Message: 12, Text: "go ahead", Source: turn.Cockpit}); err != nil {
		t.Fatal(err)
	}
	stop := func(prompt string) turns.StopAnswer {
		t.Helper()
		got, err := s.Stop(ctx, turns.StopRequest{Session: "term_1", AgentSession: "s1", PromptID: prompt})
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := stop("p1"); !got.Block || !strings.HasPrefix(got.Reason, "answered") {
		t.Fatalf("first stop %+v", got)
	}
	if got := stop("p1"); !got.Block || !strings.HasPrefix(got.Reason, "record_owed") {
		t.Fatalf("a stop after acting on the answer with no record: %+v", got)
	}
}
