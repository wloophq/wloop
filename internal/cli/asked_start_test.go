package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/core/lock"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/daemon"
	"github.com/wloophq/wloop/internal/domain/gate"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/round"
	"github.com/wloophq/wloop/internal/domain/task"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

const longAsk = "look at the migration first"

// cutAsk is longAsk within the guidance.max preparedIntents gives.
const cutAsk = "look at"

// preparedIntents give the named intents a prepare run by p and a
// guidance.max of 7, and te's starts the loop mutex mu.
func preparedIntents(te *testEnv, mu *lock.Mutex, p turns.Preparer, names ...dintent.Name) {
	st := te.Turns.Starter
	st.Loop, st.Preparer = serveLoop{e: te.Env, mu: mu}, startPreparer{p}
	st.Defs = editedDefs{Defs: st.Defs, edit: func(d *turns.Def) {
		if slices.Contains(names, d.Name) {
			d.Prepare, d.Guidance.Max = "action:asked.prepare", len(cutAsk)
		}
	}}
}

func refusingPreparer() *fakePreparer {
	return &fakePreparer{refusal: fmt.Errorf("%w: nothing to do", src.ErrUnfit)}
}

func roundSentEvents(evs []dinv.Event) []string {
	var out []string
	for _, ev := range evs {
		if ev.Kind == dinv.KindRoundSent {
			out = append(out, ev.Data)
		}
	}
	return out
}

func reviewAskedScenario(t *testing.T, notSubmitted bool) (*testEnv, *droppedPlan) {
	t.Helper()
	m := warmReviewWorkspace()
	m.NotSubmitted = notSubmitted
	te := newTestEnv(t, m)
	te.gh.basics = openBasics
	te.Rec = storeRecorder{te.Store}
	return te, withDroppedPlan(te)
}

func askReview(te *testEnv, guidance string) error {
	return envActions{te.Env, te.Store}.Intent(context.Background(), string(dintent.Review), r7.Unit(), daemon.Call{Guidance: guidance, Out: io.Discard})
}

func TestAnActionOnAPullRequestPreparesUnderTheLoopAndCutsItsGuidance(t *testing.T) {
	t.Parallel()
	te, p := reviewAskedScenario(t, false)
	mu := &lock.Mutex{}
	hp := &heldPreparer{mu: mu}
	preparedIntents(te, mu, hp, dintent.Review)
	if err := askReview(te, longAsk); err != nil {
		t.Fatalf("%v\n%s", err, te.err)
	}
	if !slices.Equal(hp.held, []bool{true}) {
		t.Fatalf("prepares under the held loop mutex %v", hp.held)
	}
	if b := te.bookOf(t, r7); len(b.Turns) != 1 || b.Turns[0].Guidance != cutAsk {
		t.Fatalf("turns %+v", b.Turns)
	}
	if got, want := p.drops(), []string{r7.Unit().String() + " " + askedByYou}; !slices.Equal(got, want) {
		t.Fatalf("drops %v, want %v", got, want)
	}
}

func TestAnActionOnAPullRequestItsPrepareRefusesSendsAndDropsNothing(t *testing.T) {
	t.Parallel()
	te, p := reviewAskedScenario(t, false)
	preparedIntents(te, &lock.Mutex{}, refusingPreparer(), dintent.Review)
	if err := askReview(te, longAsk); !errors.Is(err, turns.ErrRefused) {
		t.Fatalf("ask: %v", err)
	}
	if b := te.bookOf(t, r7); len(b.Turns) != 0 || len(p.drops()) != 0 {
		t.Fatalf("turns %+v, drops %v", b.Turns, p.drops())
	}
}

func TestTheActionRouteRunsTheEffectsOfItsStart(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name         string
		notSubmitted bool
		sent         bool
	}{{"submitted", false, true}, {"not submitted", true, false}} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			te, p := reviewAskedScenario(t, c.notSubmitted)
			if err := askReview(te, ""); err != nil {
				t.Fatalf("%v\n%s", err, te.err)
			}
			if len(p.drops()) != 1 {
				t.Fatalf("drops %v", p.drops())
			}
			evs, _ := te.Store.Events(context.Background(), r7)
			sent := roundSentEvents(evs)
			b := te.bookOf(t, r7)
			want := []string(nil)
			if c.sent {
				want = []string{withInstructions(askedByYou, b.Turns[0].ID)}
			}
			if !slices.Equal(sent, want) {
				t.Fatalf("round_sent %q, want %q", sent, want)
			}
		})
	}
}

func TestAnActionNotSubmittedKeepsItsGuidanceAndSaysSo(t *testing.T) {
	t.Parallel()
	te, _ := reviewAskedScenario(t, true)
	var out strings.Builder
	err := envActions{te.Env, te.Store}.Intent(context.Background(), string(dintent.Review), r7.Unit(), daemon.Call{Guidance: longAsk, Out: &out})
	if err != nil {
		t.Fatal(err)
	}
	if o, _ := te.Store.Override(context.Background(), r7); o.NextGuidance != longAsk {
		t.Fatalf("kept %q", o.NextGuidance)
	}
	if !strings.Contains(out.String(), "your guidance is kept for the next round") {
		t.Fatal(out.String())
	}
}

func TestAnActionOnAUnitPreparesUnderTheLoopAndCutsItsGuidance(t *testing.T) {
	te, m, _, _ := startEnv(t)
	te.gh.clone = cloneAs(t, "acme/api", new([]string))
	ctx := context.Background()
	e, err := te.Env.tasks.Capture(ctx, "add retries", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	mu := &lock.Mutex{}
	hp := &heldPreparer{mu: mu}
	preparedIntents(te, mu, hp, "start")
	c := daemon.Call{Guidance: longAsk, Inputs: map[string]any{"repo": "acme/api"}, Out: io.Discard}
	if err := (envActions{te.Env, nil}).Intent(ctx, "start", task.UnitOf(e.ID), c); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(hp.held, []bool{true}) || len(m.Prompts) != 1 {
		t.Fatalf("prepares under the held loop mutex %v, prompts %d", hp.held, len(m.Prompts))
	}
	if b := te.unitBook(t, task.UnitOf(e.ID)); len(b.Turns) != 1 || b.Turns[0].Guidance != cutAsk {
		t.Fatalf("turns %+v", b.Turns)
	}
}

func TestAnActionOnAUnitItsPrepareRefusesSendsNothing(t *testing.T) {
	te, m, _, _ := startEnv(t)
	ctx := context.Background()
	e, err := te.Env.tasks.Capture(ctx, "add retries", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	p := withDroppedPlan(te)
	preparedIntents(te, &lock.Mutex{}, refusingPreparer(), "start")
	c := daemon.Call{Inputs: map[string]any{"repo": "acme/api"}, Out: io.Discard}
	if err := (envActions{te.Env, nil}).Intent(ctx, "start", task.UnitOf(e.ID), c); !errors.Is(err, turns.ErrRefused) {
		t.Fatalf("ask: %v", err)
	}
	if b := te.unitBook(t, task.UnitOf(e.ID)); len(b.Turns) != 0 || len(m.Prompts) != 0 || len(p.drops()) != 0 {
		t.Fatalf("turns %+v, prompts %d, drops %v", b.Turns, len(m.Prompts), p.drops())
	}
}

// heldScenario is a review follow-up the daily cap held, with guidance kept
// for its next round.
func heldScenario(t *testing.T) (sweepCase, *droppedPlan) {
	t.Helper()
	sc := followupScenario(t)
	exhaustCap(t, sc)
	sc.te.gh.basics = openBasics
	sc.te.Rec = storeRecorder{sc.te.Store}
	if err := sc.te.Store.KeepNextGuidance(context.Background(), sc.ref, longAsk); err != nil {
		t.Fatal(err)
	}
	return sc, withDroppedPlan(sc.te)
}

func TestRunNowPreparesUnderTheLoopAndHandsTheKeptGuidanceCut(t *testing.T) {
	t.Parallel()
	sc, p := heldScenario(t)
	mu := &lock.Mutex{}
	hp := &heldPreparer{mu: mu}
	preparedIntents(sc.te, mu, hp, dintent.Review)
	if err := (envActions{sc.env, nil}).Run(context.Background(), sc.ref.Unit(), daemon.Call{Out: io.Discard}); err != nil {
		t.Fatalf("%v\n%s", err, sc.te.err)
	}
	if !slices.Equal(hp.held, []bool{true}) {
		t.Fatalf("prepares under the held loop mutex %v", hp.held)
	}
	b := sc.te.bookOf(t, sc.ref)
	if len(b.Turns) != 1 || b.Turns[0].Guidance != cutAsk {
		t.Fatalf("turns %+v", b.Turns)
	}
	if len(p.drops()) != 1 {
		t.Fatalf("drops %v", p.drops())
	}
	evs, _ := sc.te.Store.Events(context.Background(), sc.ref)
	if got, want := roundSentEvents(evs), []string{withInstructions(ranNow, b.Turns[0].ID)}; !slices.Equal(got, want) {
		t.Fatalf("round_sent %q, want %q", got, want)
	}
}

func TestRunNowItsPrepareRefusesSendsAndDropsNothing(t *testing.T) {
	t.Parallel()
	sc, p := heldScenario(t)
	preparedIntents(sc.te, &lock.Mutex{}, refusingPreparer(), dintent.Review)
	if err := (envActions{sc.env, nil}).Run(context.Background(), sc.ref.Unit(), daemon.Call{Out: io.Discard}); !errors.Is(err, turns.ErrRefused) {
		t.Fatalf("run now: %v", err)
	}
	if b := sc.te.bookOf(t, sc.ref); len(b.Turns) != 0 || len(sc.ws.Prompts) != 0 || len(p.drops()) != 0 {
		t.Fatalf("turns %+v, prompts %d, drops %v", b.Turns, len(sc.ws.Prompts), p.drops())
	}
	if o, _ := sc.te.Store.Override(context.Background(), sc.ref); o.NextGuidance != longAsk {
		t.Fatalf("kept %q", o.NextGuidance)
	}
}

func releaseHeld(sc sweepCase) error {
	g := gate.Gate{ID: 1, Kind: gate.KindPlan, Unit: sc.ref.Unit(), Subject: round.HeldSubject(testNow), Status: gate.Approved, By: task.ActorPerson}
	return sc.te.Env.consequences.Release(context.Background(), g)
}

func TestAReleasedHeldRoundPreparesUnderTheDaemonsLoopAndCutsItsGuidance(t *testing.T) {
	t.Parallel()
	sc, _ := heldScenario(t)
	sc.te.serve(t)
	p := withDroppedPlan(sc.te)
	hp := &heldPreparer{mu: sc.te.loop}
	preparedIntents(sc.te, sc.te.loop, hp, dintent.Review)
	if err := releaseHeld(sc); err != nil {
		t.Fatalf("%v\n%s", err, sc.te.err)
	}
	if !slices.Equal(hp.held, []bool{true}) {
		t.Fatalf("prepares under the held loop mutex %v", hp.held)
	}
	if b := sc.te.bookOf(t, sc.ref); len(b.Turns) != 1 || b.Turns[0].Guidance != cutAsk || len(p.drops()) != 1 {
		t.Fatalf("turns %+v, drops %v", b.Turns, p.drops())
	}
}

func TestAReleasedHeldRoundItsPrepareRefusesSendsAndDropsNothing(t *testing.T) {
	t.Parallel()
	sc, _ := heldScenario(t)
	sc.te.serve(t)
	p := withDroppedPlan(sc.te)
	preparedIntents(sc.te, sc.te.loop, refusingPreparer(), dintent.Review)
	if err := releaseHeld(sc); !errors.Is(err, turns.ErrRefused) {
		t.Fatalf("release: %v", err)
	}
	if b := sc.te.bookOf(t, sc.ref); len(b.Turns) != 0 || len(sc.ws.Prompts) != 0 || len(p.drops()) != 0 {
		t.Fatalf("turns %+v, prompts %d, drops %v", b.Turns, len(sc.ws.Prompts), p.drops())
	}
}

func trustAndRun(te *testEnv) error {
	return envActions{e: te.Env}.TrustAndRun(context.Background(), pr9.Unit(), daemon.Call{Out: io.Discard})
}

func TestTrustAndRunPreparesUnderTheLoopAndCutsItsGuidance(t *testing.T) {
	t.Parallel()
	m, te, rec, _ := blockedTriage(t)
	if err := rec.KeepNextGuidance(context.Background(), pr9, longAsk); err != nil {
		t.Fatal(err)
	}
	p := withDroppedPlan(te)
	mu := &lock.Mutex{}
	hp := &heldPreparer{mu: mu}
	preparedIntents(te, mu, hp, dintent.Address)
	if err := trustAndRun(te); err != nil {
		t.Fatalf("%v\n%s", err, te.err)
	}
	if !slices.Equal(hp.held, []bool{true}) || len(m.Prompts) != 1 {
		t.Fatalf("prepares under the held loop mutex %v, prompts %d", hp.held, len(m.Prompts))
	}
	b := te.bookOf(t, pr9)
	if len(b.Turns) != 1 || b.Turns[0].Guidance != cutAsk || len(p.drops()) != 1 {
		t.Fatalf("turns %+v, drops %v", b.Turns, p.drops())
	}
	if got, want := roundSentEvents(rec.events), []string{withInstructions(trustedAndRan, b.Turns[0].ID)}; !slices.Equal(got, want) {
		t.Fatalf("round_sent %q, want %q", got, want)
	}
}

func TestTrustAndRunItsPrepareRefusesSendsAndDropsNothing(t *testing.T) {
	t.Parallel()
	m, te, _, _ := blockedTriage(t)
	p := withDroppedPlan(te)
	preparedIntents(te, &lock.Mutex{}, refusingPreparer(), dintent.Address)
	if err := trustAndRun(te); !errors.Is(err, turns.ErrRefused) {
		t.Fatalf("trust and run: %v", err)
	}
	if b := te.bookOf(t, pr9); len(b.Turns) != 0 || len(m.Prompts) != 0 || len(p.drops()) != 0 {
		t.Fatalf("turns %+v, prompts %d, drops %v", b.Turns, len(m.Prompts), p.drops())
	}
}

func TestANudgeAskedThroughTheActionRecordsItsGuidanceOnce(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, warmReviewWorkspace())
	te.gh.basics = openBasics
	te.Rec = storeRecorder{te.Store}
	if err := askedIntent(te.Env, te.cfg, r7, dintent.Nudge, "be brief", preparedNudge, ""); err != nil {
		t.Fatalf("%v\n%s", err, te.err)
	}
	evs, _ := te.Store.Events(context.Background(), r7)
	var guidance []string
	for _, ev := range evs {
		if ev.Kind == dinv.KindGuidance {
			guidance = append(guidance, ev.Data)
		}
	}
	if !slices.Equal(guidance, []string{"be brief"}) {
		t.Fatalf("guidance events %q", guidance)
	}
}

const givenAsk = "check the retries"

func TestRunNowHandsTheGuidanceItIsGivenInPlaceOfTheKeptOne(t *testing.T) {
	t.Parallel()
	sc, _ := heldScenario(t)
	if err := (envActions{sc.env, nil}).Run(context.Background(), sc.ref.Unit(), daemon.Call{Guidance: givenAsk, Out: io.Discard}); err != nil {
		t.Fatalf("%v\n%s", err, sc.te.err)
	}
	if b := sc.te.bookOf(t, sc.ref); len(b.Turns) != 1 || b.Turns[0].Guidance != givenAsk {
		t.Fatalf("turns %+v, want the round to carry the guidance given", b.Turns)
	}
	if o, _ := sc.te.Store.Override(context.Background(), sc.ref); o.NextGuidance != "" {
		t.Fatalf("kept %q, want the given guidance to consume it", o.NextGuidance)
	}
}

func TestAReleasedHeldRoundHandsTheAnswerOfItsGate(t *testing.T) {
	t.Parallel()
	sc, _ := heldScenario(t)
	sc.te.serve(t)
	preparedIntents(sc.te, sc.te.loop, &fakePreparer{}, dintent.Review)
	g := gate.Gate{ID: 1, Kind: gate.KindPlan, Unit: sc.ref.Unit(), Subject: round.HeldSubject(testNow), Status: gate.Answered, Answer: "check the retries first", By: task.ActorPerson}
	if err := sc.te.Env.consequences.Release(context.Background(), g); err != nil {
		t.Fatalf("%v\n%s", err, sc.te.err)
	}
	if b := sc.te.bookOf(t, sc.ref); len(b.Turns) != 1 || b.Turns[0].Guidance != "check t" {
		t.Fatalf("turns %+v, want the gate's answer cut at guidance.max", b.Turns)
	}
}
