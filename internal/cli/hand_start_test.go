package cli

import (
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory/inventorytest"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/core/intake"
	"github.com/wloophq/wloop/internal/core/intake/intaketest"
	"github.com/wloophq/wloop/internal/core/lock"
	"github.com/wloophq/wloop/internal/daemon"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/workspace/fake"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// droppedPlan is a daemon plan that keeps the drops of pending items it was asked for.
type droppedPlan struct {
	daemon.Plan
	mu      sync.Mutex
	dropped []string
}

func (p *droppedPlan) Drop(_ context.Context, u unit.Unit, why string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.dropped = append(p.dropped, u.String()+" "+why)
	return nil
}

func (p *droppedPlan) drops() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.dropped)
}

// withDroppedPlan gives te's turn starts a plan whose drops it keeps.
func withDroppedPlan(te *testEnv) *droppedPlan {
	p := &droppedPlan{}
	late := &latePlan{}
	late.set(p)
	te.Turns.Starter.Effects = startEffects{e: te.Env, plan: late}
	return p
}

func TestAReviewByHandDropsTheUnitsPendingPlanItems(t *testing.T) {
	t.Parallel()
	_, te := handScenario(t, wsdomain.Workspace{ID: "ws-1", DisplayName: "REVIEW feat/x", LinkedPR: 7})
	p := withDroppedPlan(te)
	if code := te.run("instructions", "review", r7.URL()); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if got, want := p.drops(), []string{r7.Unit().String() + " " + askedByYou}; !slices.Equal(got, want) {
		t.Fatalf("drops %v, want %v", got, want)
	}
}

func TestATurnByHandItsGuardRefusesDropsNothing(t *testing.T) {
	t.Parallel()
	_, te := handScenario(t, wsdomain.Workspace{ID: "ws-1", DisplayName: "REVIEW feat/x", LinkedPR: 7})
	te.gh.basics = strings.Replace(openBasics, `"OPEN"`, `"CLOSED"`, 1)
	p := withDroppedPlan(te)
	if code := te.run("instructions", "review", r7.URL()); code == 0 {
		t.Fatalf("a review by hand of a closed pull request ran: %s", te.out)
	}
	if got := p.drops(); len(got) != 0 {
		t.Fatalf("drops %v", got)
	}
	if b := bookOf(t, te, r7); len(b.Turns) != 0 {
		t.Fatalf("turns %+v", b.Turns)
	}
}

// heldPreparer fills a turn's inputs and keeps whether each prepare ran
// under the held loop mutex mu.
type heldPreparer struct {
	mu     *lock.Mutex
	filled map[string]any
	held   []bool
}

func (p *heldPreparer) PrepareTurn(ctx context.Context, _ string, _ unit.Unit, _ string, inputs map[string]any) (map[string]any, error) {
	locked := !p.mu.TryLock()
	if !locked {
		p.mu.Unlock()
	}
	p.held = append(p.held, locked && lock.Held(ctx))
	out := maps.Clone(inputs)
	if out == nil {
		out = map[string]any{}
	}
	maps.Copy(out, p.filled)
	return out, nil
}

func nudgeByHandScenario(t *testing.T) *testEnv {
	t.Helper()
	te := newTestEnv(t, &fake.Manager{})
	te.gh.basics = openBasics
	ctx := context.Background()
	if err := inventorytest.Of(te.Store).UpsertPR(ctx, dinv.PR{Ref: r7, Title: "Batch export", Author: "ana", State: "OPEN", HeadOid: "h1", Reasons: []string{"repo"}, URL: r7.URL()}); err != nil {
		t.Fatal(err)
	}
	if _, err := te.Store.AppendEvents(ctx, []dinv.Event{{PR: r7, Kind: dinv.KindPush, Key: "h1", Actor: "ana", At: testNow.Add(-72 * time.Hour)}}); err != nil {
		t.Fatal(err)
	}
	agentDaemon(t, te)
	inSession(te, "term_1")
	return te
}

func TestANudgeByHandRunsItsPrepareUnderTheLoopMutex(t *testing.T) {
	t.Parallel()
	te := nudgeByHandScenario(t)
	p := &heldPreparer{mu: te.loop, filled: preparedNudge}
	te.Turns.Starter.Preparer = startPreparer{p}
	if code := te.run("instructions", "nudge", r7.URL(), "--", "be", "brief"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if !slices.Equal(p.held, []bool{true}) {
		t.Fatalf("prepares under the held loop mutex %v", p.held)
	}
	b := bookOf(t, te, r7)
	if len(b.Turns) != 1 || b.Turns[0].Inputs["situation"] != preparedNudge["situation"] {
		t.Fatalf("turns %+v", b.Turns)
	}
}

func TestANudgeByHandRecordsItsGuidance(t *testing.T) {
	t.Parallel()
	te := nudgeByHandScenario(t)
	if code := te.run("instructions", "nudge", r7.URL(), "--", "be", "brief"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	evs, _ := te.Store.Events(context.Background(), r7)
	if !slices.ContainsFunc(evs, func(ev dinv.Event) bool { return ev.Kind == dinv.KindGuidance && ev.Data == "be brief" }) {
		t.Fatalf("events %+v", evs)
	}
}

func TestAReviewByHandGuidanceReplacesTheKeptOne(t *testing.T) {
	t.Parallel()
	_, te := handScenario(t, wsdomain.Workspace{ID: "ws-1", DisplayName: "REVIEW feat/x", LinkedPR: 7})
	if err := te.Store.KeepNextGuidance(context.Background(), r7, "an old ask"); err != nil {
		t.Fatal(err)
	}
	if code := te.run("instructions", "review", r7.URL(), "--", "only", "the", "tests"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if out := te.out.String(); !strings.Contains(out, "only the tests") || strings.Contains(out, "an old ask") {
		t.Fatal(out)
	}
	if o, _ := te.Store.Override(context.Background(), r7); o.NextGuidance != "" {
		t.Fatalf("the replaced guidance stayed for the next round: %+v", o)
	}
}

func TestAReviewByHandTakesTheKeptGuidance(t *testing.T) {
	t.Parallel()
	_, te := handScenario(t, wsdomain.Workspace{ID: "ws-1", DisplayName: "REVIEW feat/x", LinkedPR: 7})
	if err := te.Store.KeepNextGuidance(context.Background(), r7, "an old ask"); err != nil {
		t.Fatal(err)
	}
	if code := te.run("instructions", "review", r7.URL()); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if out := te.out.String(); !strings.Contains(out, "an old ask") {
		t.Fatal(out)
	}
	if o, _ := te.Store.Override(context.Background(), r7); o.NextGuidance != "" {
		t.Fatalf("the taken guidance stayed for the next round: %+v", o)
	}
}

func TestAReviewByHandItsGuardRefusesKeepsItsGuidanceAndSaysSo(t *testing.T) {
	t.Parallel()
	_, te := handScenario(t, wsdomain.Workspace{ID: "ws-1", DisplayName: "REVIEW feat/x", LinkedPR: 7})
	te.gh.basics = strings.Replace(openBasics, `"OPEN"`, `"CLOSED"`, 1)
	if code := te.run("instructions", "review", r7.URL(), "--", "only", "the", "tests"); code == 0 {
		t.Fatalf("a review by hand of a closed pull request ran: %s", te.out)
	}
	if o, _ := te.Store.Override(context.Background(), r7); o.NextGuidance != "only the tests" {
		t.Fatalf("kept guidance %q", o.NextGuidance)
	}
	if out := te.out.String() + te.err.String(); !strings.Contains(out, r7.String()+": your guidance is kept for the next round") {
		t.Fatalf("output %q", out)
	}
}

func TestAReviewByHandItsPrepareRefusesKeepsItsGuidanceAndSaysSo(t *testing.T) {
	t.Parallel()
	_, te := handScenario(t, wsdomain.Workspace{ID: "ws-1", DisplayName: "REVIEW feat/x", LinkedPR: 7})
	preparedReview(te.Turns.Starter, &fakePreparer{refusal: fmt.Errorf("%w: %w", src.ErrUnfit, daemon.BadRequest("nothing new to review"))})
	if code := te.run("instructions", "review", r7.URL(), "--", "only", "the", "tests"); code == 0 {
		t.Fatalf("a refused review by hand ran: %s", te.out)
	}
	if o, _ := te.Store.Override(context.Background(), r7); o.NextGuidance != "only the tests" {
		t.Fatalf("kept guidance %q", o.NextGuidance)
	}
	out := te.out.String() + te.err.String()
	if !strings.Contains(out, "nothing new to review") || !strings.Contains(out, r7.String()+": your guidance is kept for the next round") {
		t.Fatalf("output %q", out)
	}
}

func TestTheTakeOverQuestionKeepsTheKeptGuidanceAndDropsNothing(t *testing.T) {
	t.Parallel()
	_, te := handScenario(t, wsdomain.Workspace{ID: "ws-5", DisplayName: "feat-x"})
	te.gh.basics = addressBasics(t, obj{"__typename": "User", "login": "jo"})
	ref := pr.Ref{Owner: "acme", Repo: "api", Number: 21}
	if err := te.Store.KeepNextGuidance(context.Background(), ref, "an old ask"); err != nil {
		t.Fatal(err)
	}
	p := withDroppedPlan(te)
	if code := te.run("instructions", "address", ref.URL(), "--", "fix", "the", "nit"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if out := te.out.String(); !strings.Contains(out, "Ask the user whether to take it over") {
		t.Fatal(out)
	}
	if o, _ := te.Store.Override(context.Background(), ref); o.NextGuidance != "an old ask" {
		t.Fatalf("kept guidance %q", o.NextGuidance)
	}
	if out := te.out.String() + te.err.String(); strings.Contains(out, "your guidance is kept") {
		t.Fatalf("the dropped guidance was said to be kept: %q", out)
	}
	if got := p.drops(); len(got) != 0 {
		t.Fatalf("drops %v", got)
	}
	if b := bookOf(t, te, ref); len(b.Turns) != 0 {
		t.Fatalf("turns %+v", b.Turns)
	}
}

func TestATurnByHandThroughAnIntakePreparesAtItsStart(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	te.gh.basics = openBasics
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), "intakes.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	mu := &lock.Mutex{}
	p := &heldPreparer{mu: mu}
	preparedReview(te.Turns.Starter, p)
	srv := daemon.New(withRows(daemon.Deps{Pauses: te.Rounds, Turns: te.Turns, Views: te.Views, Actions: envActions{te.Env, te.Store}, Intents: te.loadedIntents, TurnByHand: te.byHand(), LoopMu: mu,
		Intakes: db.Intakes(), IntakeBriefs: intaketest.NewBriefs(intake.Readiness{Go: true}), ByHandRef: te.byHandRef,
		Cfg: te.cfg, Version: "test", Now: te.Now, Identity: viewerIs("me"), Log: t.Logf}, te.Store))
	te.Socket = serveSocketOf(t, srv.Handler())
	if code := te.run("instructions", "review", r7.URL()); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if !slices.Equal(p.held, []bool{true}) {
		t.Fatalf("prepares under the intake's held loop mutex %v", p.held)
	}
	if open, err := db.Intakes().Open(context.Background()); err != nil || len(open) != 0 {
		t.Fatalf("open intakes %+v %v", open, err)
	}
}
