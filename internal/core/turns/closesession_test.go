package turns_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/core/turns/turnstest"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

const typedLine = "/review https://example.test/acme/api/pull/6"

// quietSession is a loop session the hook watches, whose last prompt is the
// loop's own, typed two hours ago; session.close may close it.
func quietSession(handle string) turn.Session {
	s := loopSession(handle)
	s.TypedAt, s.TypedLine = t0.Add(-2*time.Hour), typedLine
	s.PromptSeenAt = t0.Add(-2 * time.Hour)
	s.UserPromptAt = t0.Add(-3 * time.Hour)
	s.IdleSince = t0.Add(-90 * time.Minute)
	return s
}

func doneTurn(handle string, st turn.State) []turn.Turn {
	return []turn.Turn{{Seq: 1, Intent: turn.Intent(dintent.Review), ID: "r1", Trigger: turn.Automated, State: st,
		Session: handle, SentTo: handle, SentAt: t0.Add(-2 * time.Hour), StartedAt: t0.Add(-2 * time.Hour), EndedAt: t0.Add(-90 * time.Minute)}}
}

type closeEnv struct {
	s       *turns.Service
	store   *turnstest.Store
	reg     *turnstest.Registry
	sess    *turnstest.Sessions
	rec     *recorder
	prompts *turnstest.Prompts
}

func newCloseEnv(t *testing.T, c cleanupCase) closeEnv {
	t.Helper()
	s, store, reg, sess, rec := newService(t)
	p := turnstest.NewPrompts()
	s.Prompts = p
	s.AgentFor = func(unit.Unit) turns.Resumer { return homeResumer{} }
	if c.turns == nil {
		c.turns = doneTurn(c.sess.ID, turn.Done)
	}
	if c.stateSince.IsZero() {
		c.stateSince = t0.Add(-90 * time.Minute)
	}
	c.put(reg, store, sess)
	return closeEnv{s, store, reg, sess, rec, p}
}

func (e closeEnv) refusal(t *testing.T) string {
	t.Helper()
	why, err := e.s.CloseRefusal(context.Background(), "term_1")
	if err != nil {
		t.Fatal(err)
	}
	return why
}

// refuses checks that CloseSession refuses with want and closes nothing.
func (e closeEnv) refuses(t *testing.T, want string) {
	t.Helper()
	if got := e.refusal(t); got != want {
		t.Fatalf("CloseRefusal = %q, want %q", got, want)
	}
	err := e.s.CloseSession(context.Background(), "term_1", "finished and quiet")
	if !refusal(err, want) {
		t.Fatalf("CloseSession = %v, want the refusal %q", err, want)
	}
	if got, _, _ := e.reg.Session(context.Background(), "term_1"); !got.ClosedAt.IsZero() || len(e.sess.Closed()) != 0 {
		t.Fatalf("a refused session was closed: %+v, closed %v", got, e.sess.Closed())
	}
	if n := len(kinds(e.reg.Events(), turns.KindSessionClosed)); n != 0 {
		t.Fatalf("%d session_closed events", n)
	}
}

func TestCloseAllowsAQuietSession(t *testing.T) {
	e := newCloseEnv(t, cleanupCase{sess: quietSession("term_1")})
	if got := e.refusal(t); got != "" {
		t.Fatalf("CloseRefusal = %q", got)
	}
}

func TestCloseRefusesAfterTheUserTyped(t *testing.T) {
	e := newCloseEnv(t, cleanupCase{sess: quietSession("term_1")})
	if err := e.s.PromptSubmitted(context.Background(), "term_1", "let me check one more thing"); err != nil {
		t.Fatal(err)
	}
	e.refuses(t, "you typed in it after "+brand.Name+"'s last turn")
}

func TestThePromptHookTellsTheLoopsLineFromTheUsers(t *testing.T) {
	ctx := context.Background()
	e := newCloseEnv(t, cleanupCase{sess: loopSession("term_1")})
	clk := e.s.Clock.(*clock)
	if err := e.s.Typed(ctx, "term_1", typedLine); err != nil {
		t.Fatal(err)
	}
	clk.advance(5 * time.Second)
	if err := e.s.PromptSubmitted(ctx, "term_1", typedLine+"\n"); err != nil {
		t.Fatal(err)
	}
	got, _, _ := e.reg.Session(ctx, "term_1")
	if !got.TypedAt.Equal(t0) || got.TypedLine != typedLine || !got.PromptSeenAt.Equal(t0.Add(5*time.Second)) || !got.UserPromptAt.IsZero() {
		t.Fatalf("the loop's own line counted as the user's: %+v", got)
	}
	clk.advance(time.Minute)
	if err := e.s.PromptSubmitted(ctx, "term_1", typedLine); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := e.reg.Session(ctx, "term_1"); !got.UserPromptAt.Equal(t0.Add(65 * time.Second)) {
		t.Fatalf("the same line typed a minute later is the user's: %+v", got)
	}
	evs := kinds(e.reg.Events(), turns.KindSessionPrompted)
	if len(evs) != 2 || evs[0].Data != "loop" || evs[1].Data != "user" || len(kinds(e.reg.Events(), turns.KindSessionTyped)) != 1 {
		t.Fatalf("events %+v", e.reg.Events())
	}
}

func TestPromptSubmittedLeavesAnUnknownSessionAlone(t *testing.T) {
	s, _, reg, _, _ := newService(t)
	if err := s.PromptSubmitted(context.Background(), "term_9", "hi"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := reg.Session(context.Background(), "term_9"); ok || len(reg.Events()) != 0 {
		t.Fatal("a prompt registered a session")
	}
}

func TestAnAnswerTypedIntoASessionIsTheLoops(t *testing.T) {
	ctx := context.Background()
	s, store, reg, sess, _ := newService(t)
	c := cleanupCase{sess: loopSession("term_1"), turns: []turn.Turn{{Intent: turn.Intent(dintent.Review), ID: "r1", Trigger: turn.Automated, State: turn.Asked,
		Session: "term_1", SentTo: "term_1", SentAt: t0.Add(-time.Hour), StartedAt: t0.Add(-time.Hour)}}, lastOutput: t0.Add(-time.Minute)}
	c.sess.MCPAt, c.sess.StartedAt = t0.Add(-2*time.Hour), t0.Add(-2*time.Hour)
	c.put(reg, store, sess)
	var during string
	sess.OnType = func(ctx context.Context, _, _ string) (bool, error) {
		during, _ = s.CloseRefusal(ctx, "term_1")
		return true, nil
	}
	res, err := s.Answer(ctx, turns.AnswerRequest{Unit: ref(6), Text: "yes"})
	if err != nil || res.Route != turn.Typed {
		t.Fatalf("Answer = %+v, %v", res, err)
	}
	if during != "its turn is sent" {
		t.Fatalf("CloseRefusal while the answer was typed = %q", during)
	}
	if got, _, _ := reg.Session(ctx, "term_1"); got.TypedLine != "The user answered: yes" || !got.TypedAt.Equal(t0) {
		t.Fatalf("session %+v", got)
	}
}

func TestCloseRefusesWhenTypingCannotBeKnown(t *testing.T) {
	sess := quietSession("term_1")
	sess.PromptSeenAt, sess.UserPromptAt = time.Time{}, time.Time{}
	e := newCloseEnv(t, cleanupCase{sess: sess})
	e.refuses(t, "cannot tell whether you typed in it")

	e.prompts.Fail(errors.New("permission denied"))
	e.refuses(t, "cannot tell whether you typed in it")

	e.s.Prompts = nil
	e.refuses(t, "cannot tell whether you typed in it")
}

func TestCloseFallsBackToTheTranscript(t *testing.T) {
	sess := quietSession("term_1")
	sess.PromptSeenAt, sess.UserPromptAt = time.Time{}, time.Time{}
	e := newCloseEnv(t, cleanupCase{sess: sess})
	e.prompts.Put("term_1", t0.Add(-2*time.Hour-time.Second))
	if got := e.refusal(t); got != "" {
		t.Fatalf("the loop's own prompt in the transcript refused: %q", got)
	}
	e.prompts.Put("term_1", t0.Add(-10*time.Minute))
	e.refuses(t, "you typed in it after "+brand.Name+"'s last turn")
}

func TestATranscriptBeforeTypedAtWasKeptCountsTheTurnsStart(t *testing.T) {
	sess := quietSession("term_1")
	sess.TypedAt, sess.TypedLine, sess.PromptSeenAt, sess.UserPromptAt = time.Time{}, "", time.Time{}, time.Time{}
	e := newCloseEnv(t, cleanupCase{sess: sess})
	e.prompts.Put("term_1", t0.Add(-2*time.Hour-30*time.Second))
	if got := e.refusal(t); got != "" {
		t.Fatalf("CloseRefusal = %q", got)
	}
}

func TestAManualSessionPromptedWhileIdleRefuses(t *testing.T) {
	sess := quietSession("term_1")
	sess.Managed, sess.TypedAt, sess.TypedLine = false, time.Time{}, ""
	sess.UserPromptAt = t0.Add(-30 * time.Minute)
	e := newCloseEnv(t, cleanupCase{sess: sess, turns: []turn.Turn{}})
	e.refuses(t, "you prompted it while it was idle")
}

func TestAManualSessionQuietSinceItsLastPromptCloses(t *testing.T) {
	sess := quietSession("term_1")
	sess.Managed, sess.TypedAt, sess.TypedLine = false, time.Time{}, ""
	sess.UserPromptAt = t0.Add(-2 * time.Hour)
	e := newCloseEnv(t, cleanupCase{sess: sess, turns: []turn.Turn{}})
	if got := e.refusal(t); got != "" {
		t.Fatalf("CloseRefusal = %q", got)
	}
}

func TestAManualSessionWithoutHookOrTranscriptRefuses(t *testing.T) {
	sess := quietSession("term_1")
	sess.Managed, sess.TypedAt, sess.TypedLine, sess.PromptSeenAt, sess.UserPromptAt = false, time.Time{}, "", time.Time{}, time.Time{}
	e := newCloseEnv(t, cleanupCase{sess: sess, turns: []turn.Turn{}})
	e.refuses(t, "cannot tell whether you typed in it")
}

func TestCloseRefusesAWaitingAgent(t *testing.T) {
	e := newCloseEnv(t, cleanupCase{sess: quietSession("term_1"), state: turn.AgentWaiting})
	e.refuses(t, "its agent is waiting for permission")
}

func TestCloseRefusesTheFocusedWorkspace(t *testing.T) {
	e := newCloseEnv(t, cleanupCase{sess: quietSession("term_1"), userActive: true})
	e.refuses(t, "its workspace is the one open in the app")
}

func TestCloseRefusesAStartedTurn(t *testing.T) {
	for _, st := range []turn.State{turn.Started, turn.Sent} {
		t.Run(string(st), func(t *testing.T) {
			e := newCloseEnv(t, cleanupCase{sess: quietSession("term_1"), turns: doneTurn("term_1", st)})
			e.refuses(t, "its turn is "+string(st))
		})
	}
}

func TestCloseAllowsAnAskedTurn(t *testing.T) {
	e := newCloseEnv(t, cleanupCase{sess: quietSession("term_1"), turns: doneTurn("term_1", turn.Asked)})
	if got := e.refusal(t); got != "" {
		t.Fatalf("CloseRefusal = %q", got)
	}
}

func TestCloseRefusesNoAgent(t *testing.T) {
	e := newCloseEnv(t, cleanupCase{sess: quietSession("term_1")})
	e.sess.SetAgents()
	e.refuses(t, "no agent")

	e = newCloseEnv(t, cleanupCase{sess: quietSession("term_1")})
	e.sess.Put(cleanupWS)
	e.refuses(t, "no agent")
}

func TestCloseRefusesAnAgentInAnyStateButDone(t *testing.T) {
	for _, c := range []struct {
		state turn.AgentState
		want  string
	}{
		{turn.AgentWorking, "its agent is working"},
		{turn.AgentWaiting, "its agent is waiting for permission"},
		{turn.ParseAgentState("idle"), "its agent is not done"},
	} {
		t.Run(c.state.String(), func(t *testing.T) {
			e := newCloseEnv(t, cleanupCase{sess: quietSession("term_1"), state: c.state})
			e.refuses(t, c.want)
		})
	}
}

func TestCloseRefusalPropagatesAnUnreadableManager(t *testing.T) {
	e := newCloseEnv(t, cleanupCase{sess: quietSession("term_1")})
	e.sess.FailReads(errors.New("orca is down"))
	if _, err := e.s.CloseRefusal(context.Background(), "term_1"); err == nil {
		t.Fatal("CloseRefusal allowed a session it could not see")
	}
	if err := e.s.CloseSession(context.Background(), "term_1", "finished and quiet"); err == nil || len(e.sess.Closed()) != 0 {
		t.Fatalf("CloseSession = %v, closed %v", err, e.sess.Closed())
	}
}

func TestCloseSessionClosesRecordsAndKeepsTheResumeLine(t *testing.T) {
	ctx := context.Background()
	e := newCloseEnv(t, cleanupCase{sess: quietSession("term_1")})
	if err := e.s.CloseSession(ctx, "term_1", "finished and quiet"); err != nil {
		t.Fatal(err)
	}
	if got := e.sess.Closed(); len(got) != 1 || got[0] != "term_1" {
		t.Fatalf("closed %v", got)
	}
	got, _, _ := e.reg.Session(ctx, "term_1")
	if !got.ClosedAt.Equal(t0) {
		t.Fatalf("ClosedAt = %v", got.ClosedAt)
	}
	want := "finished and quiet; resume with: cd '/wt/acme api' && agent --home  --resume sess-term_1"
	evs := kinds(e.reg.Events(), turns.KindSessionClosed)
	if len(evs) != 1 || evs[0].Key != "term_1" || evs[0].Data != want || len(kinds(e.rec.published, turns.KindSessionClosed)) != 1 {
		t.Fatalf("events %+v, want data %q", evs, want)
	}
	if err := e.s.CloseSession(ctx, "term_1", "finished and quiet"); !refusal(err, "the session is closed") {
		t.Fatalf("a second close = %v", err)
	}
}

func TestCloseSessionKeepsTheSessionWhenTheManagerFails(t *testing.T) {
	e := newCloseEnv(t, cleanupCase{sess: quietSession("term_1")})
	e.sess.FailClose(errors.New("orca is down"))
	if err := e.s.CloseSession(context.Background(), "term_1", "finished and quiet"); err == nil {
		t.Fatal("CloseSession succeeded")
	}
	if got, _, _ := e.reg.Session(context.Background(), "term_1"); !got.ClosedAt.IsZero() {
		t.Fatal("the session was marked closed")
	}
}

func TestAnAnswerToAClosedSessionResumesAFreshOne(t *testing.T) {
	ctx := context.Background()
	e := newCloseEnv(t, cleanupCase{sess: quietSession("term_1"), turns: doneTurn("term_1", turn.Asked)})
	if err := e.s.CloseSession(ctx, "term_1", "asked and quiet"); err != nil {
		t.Fatal(err)
	}
	res, err := e.s.Answer(ctx, turns.AnswerRequest{Unit: ref(6), Text: "go ahead"})
	if err != nil || res.Route != turn.Resumed {
		t.Fatalf("Answer = %+v, %v", res, err)
	}
	if len(e.rec.launches) != 1 || e.rec.launches[0].ResumeOf != 1 || e.rec.launches[0].Answer != "go ahead" {
		t.Fatalf("launches %+v", e.rec.launches)
	}
	if typed := e.sess.Typed(); len(typed) != 0 {
		t.Fatalf("typed into the closed session: %+v", typed)
	}
}

func TestCensusMarksWithoutClosing(t *testing.T) {
	ctx := context.Background()
	s, reg, sc, rec := expireService(t, "term_1", "term_2")
	sc.complete(t0, "rt", "term_1")
	if err := s.Census(ctx); err != nil {
		t.Fatal(err)
	}
	if s.ProcessGone("term_2") {
		t.Fatal("one census made term_2 gone")
	}
	sc.complete(t0.Add(2*time.Minute), "rt", "term_1")
	if err := s.Census(ctx); err != nil {
		t.Fatal(err)
	}
	if !s.ProcessGone("term_2") || s.ProcessGone("term_1") {
		t.Fatalf("Gone term_2 %v, term_1 %v", s.ProcessGone("term_2"), s.ProcessGone("term_1"))
	}
	sc.complete(t0.Add(4*time.Minute), "rt", "term_1")
	if err := s.Census(ctx); err != nil || !s.ProcessGone("term_2") {
		t.Fatalf("a third census forgot term_2: %v", err)
	}
	if open := openHandles(t, reg); len(open) != 2 || len(rec.published) != 0 {
		t.Fatalf("Census closed something: open %v", open)
	}
	sc.complete(t0.Add(6*time.Minute), "rt", "term_1", "term_2")
	if err := s.Census(ctx); err != nil || s.ProcessGone("term_2") {
		t.Fatalf("a listed session stayed gone: %v", err)
	}
}

func TestDismissTurnRecordsTheReason(t *testing.T) {
	ctx := context.Background()
	s, store, _, _, rec := newService(t)
	b := store.Put(turn.Book{Unit: r7, Turns: doneTurn("term_1", turn.Asked),
		Answers: []turn.Answer{{TurnSeq: 1, Text: "later", Route: turn.Queued, At: t0.Add(-time.Hour)}}})
	seq := b.Turns[0].Seq
	if err := s.DismissTurn(ctx, r7, seq, "pull request merged"); err != nil {
		t.Fatal(err)
	}
	got := bookOf(t, store, r7)
	if got.Turns[0].State != turn.Dismissed || !got.Turns[0].EndedAt.Equal(t0) || got.Answers[0].Route != turn.Refused {
		t.Fatalf("book %+v", got)
	}
	evs := kinds(store.Events(), turns.KindTurnDismissedByRule)
	if len(evs) != 1 || evs[0].Key != "1:pull request merged" || evs[0].Data != "pull request merged" || evs[0].Unit != r7 {
		t.Fatalf("events %+v", store.Events())
	}
	if len(kinds(rec.published, turns.KindTurnDismissedByRule)) != 1 {
		t.Fatalf("published %+v", rec.published)
	}
	if err := s.DismissTurn(ctx, r7, seq, "again"); !refusal(err, "is dismissed") {
		t.Fatalf("dismissing a dismissed turn = %v", err)
	}
}

func TestRefuseAnswerEmitsAnEvent(t *testing.T) {
	ctx := context.Background()
	s, store, _, _, rec := newService(t)
	b := store.Put(turn.Book{Unit: r7, Turns: doneTurn("term_1", turn.Asked), Answers: []turn.Answer{
		{TurnSeq: 1, Text: "later", Route: turn.Queued, At: t0.Add(-time.Hour)},
		{TurnSeq: 1, Text: "now", Route: turn.Typed, At: t0.Add(-time.Hour)},
	}})
	queued, typed := b.Answers[0].ID, b.Answers[1].ID
	if err := s.RefuseAnswer(ctx, r7, queued, "its session is gone"); err != nil {
		t.Fatal(err)
	}
	if got := bookOf(t, store, r7); got.Answers[0].Route != turn.Refused {
		t.Fatalf("answers %+v", got.Answers)
	}
	evs := kinds(store.Events(), turns.KindAnswerRefused)
	if len(evs) != 1 || evs[0].Data != "its session is gone" || evs[0].Unit != r7 || len(kinds(rec.published, turns.KindAnswerRefused)) != 1 {
		t.Fatalf("events %+v", store.Events())
	}
	if err := s.RefuseAnswer(ctx, r7, queued, "again"); err != nil || len(kinds(store.Events(), turns.KindAnswerRefused)) != 1 {
		t.Fatalf("refusing twice = %v", err)
	}
	if err := s.RefuseAnswer(ctx, r7, typed, "too late"); !refusal(err, "is typed") {
		t.Fatalf("refusing a typed answer = %v", err)
	}
}

func TestCloseSessionReadsTheTranscriptWhenTheHookMissedAPrompt(t *testing.T) {
	e := newCloseEnv(t, cleanupCase{sess: quietSession("term_1")})
	e.prompts.Put("term_1", t0.Add(-30*time.Minute))
	if got := e.refusal(t); got != "" {
		t.Fatalf("evaluation read the transcript of a hooked session: %q", got)
	}
	want := "you typed in it after " + brand.Name + "'s last turn"
	if err := e.s.CloseSession(context.Background(), "term_1", "finished and quiet"); !refusal(err, want) {
		t.Fatalf("CloseSession = %v, want %q", err, want)
	}
	if len(e.sess.Closed()) != 0 {
		t.Fatalf("closed %v", e.sess.Closed())
	}
}

func TestCloseSessionWithTheHookSeenClosesWithoutATranscript(t *testing.T) {
	e := newCloseEnv(t, cleanupCase{sess: quietSession("term_1")})
	e.prompts.Fail(errors.New("permission denied"))
	if err := e.s.CloseSession(context.Background(), "term_1", "finished and quiet"); err != nil {
		t.Fatal(err)
	}
}

func TestCloseRefusesATurnBeingTyped(t *testing.T) {
	sending := doneTurn("term_1", turn.Done)
	sending = append(sending, turn.Turn{Seq: 2, Intent: turn.Intent(dintent.Address), ID: "r2", Trigger: turn.Automated, State: turn.Sent, SentAt: t0})
	e := newCloseEnv(t, cleanupCase{sess: quietSession("term_1"), turns: sending})
	e.refuses(t, "its turn is sent")
}

func TestCloseRefusesWhileTheLoopTypes(t *testing.T) {
	e := newCloseEnv(t, cleanupCase{sess: quietSession("term_1")})
	done := e.s.Typing("term_1")
	e.refuses(t, "its turn is sent")
	done()
	if got := e.refusal(t); got != "" {
		t.Fatalf("CloseRefusal after typing = %q", got)
	}
}

func TestDismissTurnRefusesItsPendingAnswers(t *testing.T) {
	ctx := context.Background()
	s, store, _, _, _ := newService(t)
	b := store.Put(turn.Book{Unit: r7, Turns: doneTurn("term_1", turn.Asked),
		Answers: []turn.Answer{{TurnSeq: 1, Text: "later", Route: turn.Pending, At: t0.Add(-time.Hour)}}})
	if err := s.DismissTurn(ctx, r7, b.Turns[0].Seq, "pull request merged"); err != nil {
		t.Fatal(err)
	}
	if got := bookOf(t, store, r7); got.Answers[0].Route != turn.Refused {
		t.Fatalf("answers %+v", got.Answers)
	}
	evs := kinds(store.Events(), turns.KindAnswerRefused)
	if len(evs) != 1 || evs[0].Data != "its turn was dismissed" {
		t.Fatalf("events %+v", store.Events())
	}
}
