package turns_test

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/core/lock"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/core/turns/turnstest"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

var startUnit = unit.Unit{Provider: unit.GitHub, Key: "acme/widgets#7"}

type startRig struct {
	trace    []string
	defs     startDefs
	prepared map[string]any
	prepErr  error
	prepSaw  []string
	kept     map[unit.Unit]string
	takeErr  error
	held     map[string]bool
	effects  startEffects
	store    *turnstest.Store
	logs     []string
	s        *turns.Starter
}

type startDefs map[turn.Intent]turns.Def

func (d startDefs) Def(name turn.Intent) (turns.Def, bool) {
	def, ok := d[name]
	return def, ok
}

type startPreparer struct{ r *startRig }

func (p startPreparer) PrepareTurn(ctx context.Context, intent string, u unit.Unit, guidance string, inputs map[string]any) (map[string]any, error) {
	p.r.note(ctx, "prepare")
	p.r.prepSaw = append(p.r.prepSaw, guidance)
	if p.r.prepErr != nil {
		return nil, p.r.prepErr
	}
	out := maps.Clone(inputs)
	if out == nil {
		out = map[string]any{}
	}
	maps.Copy(out, p.r.prepared)
	return out, nil
}

type startKept struct{ r *startRig }

func (k startKept) Take(ctx context.Context, u unit.Unit) (string, error) {
	k.r.note(ctx, "take")
	if k.r.takeErr != nil {
		return "", k.r.takeErr
	}
	g := k.r.kept[u]
	delete(k.r.kept, u)
	return g, nil
}

func (k startKept) Keep(ctx context.Context, u unit.Unit, g string) error {
	k.r.note(ctx, "keep "+g)
	if k.r.kept[u] == "" {
		k.r.kept[u] = g
	}
	return nil
}

type startLoop struct{ r *startRig }

func (l startLoop) Enter(ctx context.Context, u unit.Unit) (context.Context, func(), error) {
	l.r.note(ctx, "enter")
	return ctx, func() { l.r.trace = append(l.r.trace, "release") }, nil
}

type startEffects struct {
	r         *startRig
	checkErr  error
	beforeErr error
	recorded  []turns.TurnOut
	sent      []turns.TurnOut
	handed    []turns.TurnOut
}

func (e *startEffects) Check(ctx context.Context, d turns.Def, u unit.Unit) error {
	e.r.note(ctx, "check")
	return e.checkErr
}

func (e *startEffects) Before(ctx context.Context, d turns.Def, u unit.Unit) error {
	e.r.note(ctx, "before")
	return e.beforeErr
}

func (e *startEffects) Recorded(ctx context.Context, o turns.TurnOut) error {
	e.r.note(ctx, "recorded")
	e.recorded = append(e.recorded, o)
	return nil
}

func (e *startEffects) Sent(ctx context.Context, o turns.TurnOut) error {
	e.r.note(ctx, "sent")
	e.sent = append(e.sent, o)
	return nil
}

func (e *startEffects) Handed(ctx context.Context, o turns.TurnOut) error {
	e.r.note(ctx, "handed")
	e.handed = append(e.handed, o)
	return nil
}

type startRunner struct {
	r         *startRig
	out       turns.Outcome
	err       error
	guardErr  error
	noSession bool
	keptBack  []string
	ran       []turns.Turn
	useErr    error
}

func (x *startRunner) Run(ctx context.Context, t turns.Turn) (turns.Outcome, error) {
	x.r.note(ctx, "run")
	x.ran = append(x.ran, t)
	x.useErr = t.Use()
	return x.out, x.err
}

func (x *startRunner) Guard(ctx context.Context, t turns.Turn) error {
	x.r.note(ctx, "guard")
	return x.guardErr
}

func (x *startRunner) Sessionless() bool { return x.noSession }

func (x *startRunner) KeptBack(g string) { x.keptBack = append(x.keptBack, g) }

// plainRunner is a Runner with no guard.
type plainRunner struct{ x *startRunner }

func (p plainRunner) Run(ctx context.Context, t turns.Turn) (turns.Outcome, error) {
	return p.x.Run(ctx, t)
}

func (r *startRig) note(ctx context.Context, step string) {
	r.trace = append(r.trace, step)
	r.held[step] = lock.Held(ctx)
}

const (
	guided   turn.Intent = "guided"
	prepared turn.Intent = "prepared"
	plain    turn.Intent = "plain"
)

func newStartRig(t *testing.T) *startRig {
	t.Helper()
	r := &startRig{kept: map[unit.Unit]string{}, held: map[string]bool{}, store: turnstest.NewStore()}
	r.defs = startDefs{
		guided:   {Def: dintent.Def{Name: dintent.Name(guided), Role: "guided.role", Provider: unit.GitHub}, HandsGuidance: true},
		prepared: {Def: dintent.Def{Name: dintent.Name(prepared), Prepare: "action:fill", Guidance: dintent.Guidance{Max: 5}, Provider: unit.GitHub}},
		plain:    {Def: dintent.Def{Name: dintent.Name(plain), Provider: unit.GitHub}},
	}
	r.effects.r = r
	r.s = &turns.Starter{Defs: r.defs, Preparer: startPreparer{r}, Kept: startKept{r}, Loop: startLoop{r}, Effects: &r.effects,
		Store: r.store, Log: func(format string, args ...any) { r.logs = append(r.logs, fmt.Sprintf(format, args...)) }}
	return r
}

func (r *startRig) runner(submitted bool) *startRunner {
	return &startRunner{r: r, out: turns.Outcome{ID: "turn1", Head: "abc123", Submitted: submitted}}
}

func byPerson(i turn.Intent) turns.Request {
	return turns.Request{Unit: startUnit, Intent: i, Trigger: turn.ByHand, Route: "session", ByPerson: true}
}

func automated(i turn.Intent) turns.Request {
	return turns.Request{Unit: startUnit, Intent: i, Trigger: turn.Automated, Route: "launch"}
}

func TestStartRunsItsStepsInTheDesignedOrder(t *testing.T) {
	r := newStartRig(t)
	r.defs[guided] = turns.Def{Def: dintent.Def{Name: dintent.Name(guided), Prepare: "action:fill", Provider: unit.GitHub}, HandsGuidance: true}
	r.kept[startUnit] = "look at the tests"
	if _, err := r.s.Start(context.Background(), byPerson(guided), r.runner(true)); err != nil {
		t.Fatal(err)
	}
	want := []string{"enter", "take", "prepare", "check", "guard", "before", "run", "recorded", "sent", "handed", "release"}
	if !slices.Equal(r.trace, want) {
		t.Fatalf("trace %v, want %v", r.trace, want)
	}
}

func TestEveryStepAfterTheEnterSeesTheLoopHeld(t *testing.T) {
	r := newStartRig(t)
	if _, err := r.s.Start(context.Background(), byPerson(guided), r.runner(true)); err != nil {
		t.Fatal(err)
	}
	if r.held["enter"] {
		t.Error("the enter saw the loop held before it took it")
	}
	for _, step := range []string{"take", "check", "guard", "before", "run", "recorded", "sent"} {
		if !r.held[step] {
			t.Errorf("%s ran without the loop held", step)
		}
	}
}

func TestAStartUnderAHeldLoopDoesNotEnterItAgain(t *testing.T) {
	r := newStartRig(t)
	if _, err := r.s.Start(lock.WithHeld(context.Background()), byPerson(guided), r.runner(true)); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(r.trace, "enter") || slices.Contains(r.trace, "release") {
		t.Fatalf("trace %v entered the loop its caller holds", r.trace)
	}
}

func TestAnUnknownIntentEntersNothing(t *testing.T) {
	r := newStartRig(t)
	if _, err := r.s.Start(context.Background(), byPerson("missing"), r.runner(true)); err == nil {
		t.Fatal("an unknown intent started")
	}
	if len(r.trace) != 0 {
		t.Fatalf("trace %v", r.trace)
	}
}

func TestAnIntentThatHandsGuidanceTakesTheKeptOne(t *testing.T) {
	r := newStartRig(t)
	r.kept[startUnit] = "look at the tests"
	x := r.runner(true)
	if _, err := r.s.Start(context.Background(), byPerson(guided), x); err != nil {
		t.Fatal(err)
	}
	if x.ran[0].Guidance() != "look at the tests" || r.kept[startUnit] != "" {
		t.Fatalf("turn guidance %q, kept %q", x.ran[0].Guidance(), r.kept[startUnit])
	}
}

func TestAnIntentThatHandsNoGuidanceTakesNone(t *testing.T) {
	r := newStartRig(t)
	r.kept[startUnit] = "for the next review"
	x := r.runner(true)
	if _, err := r.s.Start(context.Background(), automated(plain), x); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(r.trace, "take") || x.ran[0].Guidance() != "" || r.kept[startUnit] != "for the next review" {
		t.Fatalf("trace %v, turn guidance %q, kept %q", r.trace, x.ran[0].Guidance(), r.kept[startUnit])
	}
}

func TestAGivenGuidanceConsumesTheKeptOne(t *testing.T) {
	r := newStartRig(t)
	r.kept[startUnit] = "older"
	x := r.runner(true)
	req := byPerson(guided)
	req.Guidance = "newer"
	if _, err := r.s.Start(context.Background(), req, x); err != nil {
		t.Fatal(err)
	}
	if x.ran[0].Guidance() != "newer" || r.kept[startUnit] != "" {
		t.Fatalf("turn guidance %q, kept %q", x.ran[0].Guidance(), r.kept[startUnit])
	}
}

func TestAKeptGuidanceReadErrorIsWarnedAndTheTurnGoesOn(t *testing.T) {
	r := newStartRig(t)
	r.takeErr = errors.New("store locked")
	x := r.runner(true)
	if _, err := r.s.Start(context.Background(), byPerson(guided), x); err != nil {
		t.Fatal(err)
	}
	if len(x.ran) != 1 || len(r.logs) != 1 {
		t.Fatalf("ran %d, logs %v", len(x.ran), r.logs)
	}
}

func TestTheGuidanceIsCutAtItsMaxBeforeThePrepare(t *testing.T) {
	r := newStartRig(t)
	x := r.runner(true)
	req := byPerson(prepared)
	req.Guidance = "çàççàççà"
	if _, err := r.s.Start(context.Background(), req, x); err != nil {
		t.Fatal(err)
	}
	if x.ran[0].Guidance() != "çàççà" || !slices.Equal(r.prepSaw, []string{"çàççà"}) {
		t.Fatalf("turn guidance %q, prepare saw %q", x.ran[0].Guidance(), r.prepSaw)
	}
}

func TestThePrepareFillsTheTurnsInputs(t *testing.T) {
	r := newStartRig(t)
	r.prepared = map[string]any{"topic": "the release"}
	x := r.runner(true)
	req := automated(prepared)
	req.Inputs = map[string]any{"tone": "short"}
	if _, err := r.s.Start(context.Background(), req, x); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"tone": "short", "topic": "the release"}
	if !maps.Equal(x.ran[0].Inputs(), want) {
		t.Fatalf("turn inputs %v", x.ran[0].Inputs())
	}
	if len(req.Inputs) != 1 {
		t.Fatalf("the prepare wrote the request's inputs: %v", req.Inputs)
	}
}

func TestAnIntentWithoutAPrepareIsNotPrepared(t *testing.T) {
	r := newStartRig(t)
	r.prepErr = turns.ErrTransient
	if _, err := r.s.Start(context.Background(), automated(plain), r.runner(true)); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(r.trace, "prepare") {
		t.Fatalf("trace %v", r.trace)
	}
}

func TestAPrepareRefusalRunsNothingAndKeepsTheGuidanceBack(t *testing.T) {
	r := newStartRig(t)
	r.defs[guided] = turns.Def{Def: dintent.Def{Name: dintent.Name(guided), Prepare: "action:fill", Provider: unit.GitHub}, HandsGuidance: true}
	r.kept[startUnit] = "look at the tests"
	r.prepErr = fmt.Errorf("%w: the pull request is closed", turns.ErrRefused)
	x := r.runner(true)
	_, err := r.s.Start(context.Background(), byPerson(guided), x)
	if !errors.Is(err, turns.ErrRefused) {
		t.Fatalf("err = %v, want ErrRefused", err)
	}
	if len(x.ran) != 0 || slices.Contains(r.trace, "before") || r.kept[startUnit] != "look at the tests" {
		t.Fatalf("trace %v, kept %q", r.trace, r.kept[startUnit])
	}
}

func TestATurnWhoseEffectsNoPluginAppliesIsNotRun(t *testing.T) {
	r := newStartRig(t)
	r.effects.checkErr = errors.New("no running plugin applies pr.reviewed")
	x := r.runner(true)
	if _, err := r.s.Start(context.Background(), byPerson(plain), x); err == nil {
		t.Fatal("a turn whose effects no plugin applies started")
	}
	if len(x.ran) != 0 || slices.Contains(r.trace, "before") {
		t.Fatalf("trace %v", r.trace)
	}
}

func TestARunnersGuardRefusesBeforeTheBeforeEffects(t *testing.T) {
	r := newStartRig(t)
	x := r.runner(true)
	x.guardErr = errors.New("the pull request is not open")
	if _, err := r.s.Start(context.Background(), byPerson(plain), x); err == nil {
		t.Fatal("a guarded turn started")
	}
	if len(x.ran) != 0 || slices.Contains(r.trace, "before") {
		t.Fatalf("trace %v", r.trace)
	}
}

func TestARunnerWithoutAGuardRunsUnguarded(t *testing.T) {
	r := newStartRig(t)
	x := r.runner(true)
	if _, err := r.s.Start(context.Background(), byPerson(plain), plainRunner{x}); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(r.trace, "guard") || len(x.ran) != 1 {
		t.Fatalf("trace %v", r.trace)
	}
}

func TestBeforeEffectsRunOnlyOnAPersonsTurn(t *testing.T) {
	r := newStartRig(t)
	if _, err := r.s.Start(context.Background(), automated(plain), r.runner(true)); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(r.trace, "before") {
		t.Fatalf("an automated turn ran the before effects: %v", r.trace)
	}
}

func TestABeforeEffectThatFailsRunsNothing(t *testing.T) {
	r := newStartRig(t)
	r.effects.beforeErr = errors.New("dropping the pending item")
	x := r.runner(true)
	if _, err := r.s.Start(context.Background(), byPerson(plain), x); err == nil {
		t.Fatal("started past a failed before effect")
	}
	if len(x.ran) != 0 {
		t.Fatalf("trace %v", r.trace)
	}
}

func TestPluginEffectsRunOnEveryRecordedTurnWithTakenFromSubmitted(t *testing.T) {
	for _, submitted := range []bool{true, false} {
		r := newStartRig(t)
		req := automated(plain)
		req.Inputs = map[string]any{"tone": "short"}
		if _, err := r.s.Start(context.Background(), req, r.runner(submitted)); err != nil {
			t.Fatal(err)
		}
		if len(r.effects.recorded) != 1 {
			t.Fatalf("submitted %v: recorded %+v", submitted, r.effects.recorded)
		}
		o := r.effects.recorded[0]
		if o.Outcome.Submitted != submitted || o.Route != "launch" || o.Unit != startUnit || o.Outcome.ID != "turn1" || o.Outcome.Head != "abc123" ||
			o.Def.Name != dintent.Name(plain) || o.Inputs["tone"] != "short" {
			t.Fatalf("submitted %v: %+v", submitted, o)
		}
	}
}

func TestAPlannedRoundIsCheckedAndGetsItsPluginEffectsAsPlanned(t *testing.T) {
	r := newStartRig(t)
	req := automated(guided)
	req.Planned = true
	if _, err := r.s.Start(context.Background(), req, r.runner(true)); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(r.trace, "check") || len(r.effects.recorded) != 1 || !r.effects.recorded[0].Planned {
		t.Fatalf("trace %v, recorded %+v", r.trace, r.effects.recorded)
	}
	if _, err := r.s.Start(context.Background(), automated(guided), r.runner(true)); err != nil {
		t.Fatal(err)
	}
	if len(r.effects.recorded) != 2 || r.effects.recorded[1].Planned {
		t.Fatalf("recorded %+v, want a turn no plan named unplanned", r.effects.recorded)
	}
}

func TestSentEffectsRunOnlyOnAPersonsSubmittedTurn(t *testing.T) {
	for _, c := range []struct {
		name      string
		req       turns.Request
		submitted bool
		want      int
	}{
		{"a person's submitted turn", byPerson(plain), true, 1},
		{"a person's turn its prompt did not reach", byPerson(plain), false, 0},
		{"an automated submitted turn", automated(plain), true, 0},
	} {
		r := newStartRig(t)
		if _, err := r.s.Start(context.Background(), c.req, r.runner(c.submitted)); err != nil {
			t.Fatal(err)
		}
		if len(r.effects.sent) != c.want {
			t.Errorf("%s: sent %+v", c.name, r.effects.sent)
		}
		if c.want == 1 && r.effects.sent[0].Outcome.ID != "turn1" {
			t.Errorf("%s: sent %+v without the instructions id", c.name, r.effects.sent[0])
		}
	}
}

func TestTheGuidanceEventIsRecordedForASessionTurnAndForASessionlessOneWhoseIntentHandsIt(t *testing.T) {
	for _, c := range []struct {
		name      string
		intent    turn.Intent
		submitted bool
		noSession bool
		want      int
	}{
		{"handed and submitted", guided, true, false, 1},
		{"handed and not submitted", guided, false, false, 1},
		{"a session turn of an intent that hands none", plain, true, false, 1},
		{"a sessionless turn of an intent that hands it", guided, true, true, 1},
		{"a sessionless turn of an intent that hands none", plain, true, true, 0},
	} {
		r := newStartRig(t)
		req := automated(c.intent)
		req.Guidance = "look at the tests"
		x := r.runner(c.submitted)
		x.noSession = c.noSession
		if _, err := r.s.Start(context.Background(), req, x); err != nil {
			t.Fatal(err)
		}
		if len(r.effects.handed) != c.want {
			t.Errorf("%s: handed %+v", c.name, r.effects.handed)
		}
		if c.want == 1 && r.effects.handed[0].Guidance != "look at the tests" {
			t.Errorf("%s: handed %+v", c.name, r.effects.handed[0])
		}
	}
}

func TestTheSentEffectsReadTheReasonThePersonAskedWith(t *testing.T) {
	r := newStartRig(t)
	req := byPerson(plain)
	req.Reason = "run now"
	if _, err := r.s.Start(context.Background(), req, r.runner(true)); err != nil {
		t.Fatal(err)
	}
	if len(r.effects.sent) != 1 || r.effects.sent[0].Reason != "run now" {
		t.Fatalf("sent %+v", r.effects.sent)
	}
}

func TestNoGuidanceEventWithoutGuidance(t *testing.T) {
	r := newStartRig(t)
	if _, err := r.s.Start(context.Background(), automated(guided), r.runner(true)); err != nil {
		t.Fatal(err)
	}
	if len(r.effects.handed) != 0 {
		t.Fatalf("handed %+v", r.effects.handed)
	}
}

func TestATurnItsPromptDidNotReachKeepsItsGuidanceBack(t *testing.T) {
	for _, c := range []struct {
		name  string
		given string
	}{{"taken", ""}, {"given", "newer"}} {
		r := newStartRig(t)
		r.kept[startUnit] = "older"
		req := automated(guided)
		req.Guidance = c.given
		if _, err := r.s.Start(context.Background(), req, r.runner(false)); err != nil {
			t.Fatal(err)
		}
		want := cmp.Or(c.given, "older")
		if r.kept[startUnit] != want {
			t.Errorf("%s: kept %q, want %q", c.name, r.kept[startUnit], want)
		}
	}
}

func TestAFailedRunKeepsTheGuidanceBackAndAppliesNoEffect(t *testing.T) {
	r := newStartRig(t)
	r.kept[startUnit] = "look at the tests"
	x := &startRunner{r: r, err: errors.New("the session is busy")}
	if _, err := r.s.Start(context.Background(), byPerson(guided), x); err == nil {
		t.Fatal("a failed run started")
	}
	if r.kept[startUnit] != "look at the tests" || len(r.effects.recorded)+len(r.effects.sent)+len(r.effects.handed) != 0 {
		t.Fatalf("kept %q, trace %v", r.kept[startUnit], r.trace)
	}
}

func TestARunThatRecordedItsTurnAndFailedRecordsOnlyTheGuidanceEvent(t *testing.T) {
	r := newStartRig(t)
	r.kept[startUnit] = "look at the tests"
	x := r.runner(false)
	x.err = errors.New("typing the prompt: the terminal is gone")
	if _, err := r.s.Start(context.Background(), byPerson(guided), x); err == nil {
		t.Fatal("a failed run started")
	}
	if len(r.effects.handed) != 1 || r.effects.handed[0].Guidance != "look at the tests" || r.effects.handed[0].Outcome.ID != "turn1" {
		t.Fatalf("handed %+v", r.effects.handed)
	}
	if len(r.effects.recorded)+len(r.effects.sent) != 0 || r.kept[startUnit] != "look at the tests" {
		t.Fatalf("recorded %+v, sent %+v, kept %q", r.effects.recorded, r.effects.sent, r.kept[startUnit])
	}
}

func TestARunThatRecordedNoTurnRecordsNoGuidanceEvent(t *testing.T) {
	r := newStartRig(t)
	req := automated(guided)
	req.Guidance = "look at the tests"
	if _, err := r.s.Start(context.Background(), req, &startRunner{r: r}); err != nil {
		t.Fatal(err)
	}
	if len(r.effects.handed) != 0 || r.kept[startUnit] != "look at the tests" {
		t.Fatalf("handed %+v, kept %q", r.effects.handed, r.kept[startUnit])
	}
}

func TestTheKeptBackGuidanceIsTheWholeTextTaken(t *testing.T) {
	r := newStartRig(t)
	r.defs[guided] = turns.Def{Def: dintent.Def{Name: dintent.Name(guided), Guidance: dintent.Guidance{Max: 4}, Provider: unit.GitHub}, HandsGuidance: true}
	r.kept[startUnit] = "look at the tests"
	if _, err := r.s.Start(context.Background(), byPerson(guided), r.runner(false)); err != nil {
		t.Fatal(err)
	}
	if r.kept[startUnit] != "look at the tests" {
		t.Fatalf("kept %q", r.kept[startUnit])
	}
}

func TestAnIntentThatHandsNoGuidanceKeepsNoneBack(t *testing.T) {
	r := newStartRig(t)
	req := automated(plain)
	req.Guidance = "the release"
	if _, err := r.s.Start(context.Background(), req, r.runner(false)); err != nil {
		t.Fatal(err)
	}
	if r.kept[startUnit] != "" {
		t.Fatalf("kept %q", r.kept[startUnit])
	}
}

func TestTheRunnerReadsTheRequestThroughTheTurn(t *testing.T) {
	r := newStartRig(t)
	x := r.runner(true)
	req := automated(plain)
	if _, err := r.s.Start(context.Background(), req, x); err != nil {
		t.Fatal(err)
	}
	got := x.ran[0]
	if got.Unit() != startUnit || got.Intent() != plain || got.Trigger() != turn.Automated || got.ResumeOf() != 0 {
		t.Fatalf("turn %+v", got)
	}
	if len(r.effects.recorded) != 1 || r.effects.recorded[0].Route != "launch" || slices.Contains(r.trace, "before") {
		t.Fatalf("recorded %+v, trace %v", r.effects.recorded, r.trace)
	}
}

func TestATurnIsUsedOnce(t *testing.T) {
	r := newStartRig(t)
	x := r.runner(true)
	if _, err := r.s.Start(context.Background(), byPerson(plain), x); err != nil {
		t.Fatal(err)
	}
	if x.useErr != nil {
		t.Fatalf("first use: %v", x.useErr)
	}
	if err := x.ran[0].Use(); !errors.Is(err, turns.ErrUsedTurn) {
		t.Fatalf("second use = %v, want ErrUsedTurn", err)
	}
}

func TestAZeroTurnIsRefused(t *testing.T) {
	if err := (turns.Turn{}).Use(); !errors.Is(err, turns.ErrNoTurn) {
		t.Fatalf("Use of a zero turn = %v, want ErrNoTurn", err)
	}
}

func TestResumeCarriesTheStoredTurnAndTakesNothing(t *testing.T) {
	r := newStartRig(t)
	r.kept[startUnit] = "for the next review"
	r.store.Put(turn.Book{Unit: startUnit, Turns: []turn.Turn{{Seq: 1, Intent: guided, ID: "turn1", Trigger: turn.Automated, Guidance: "look at the tests",
		State: turn.Asked, Inputs: map[string]any{"tone": "short"}}}})
	got, err := r.s.Resume(context.Background(), startUnit, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got.Unit() != startUnit || got.Intent() != guided || got.ResumeOf() != 1 || got.Trigger() != turn.Automated ||
		got.Guidance() != "look at the tests" || got.Inputs()["tone"] != "short" {
		t.Fatalf("resumed %+v", got)
	}
	if len(r.trace) != 0 || r.kept[startUnit] != "for the next review" {
		t.Fatalf("trace %v, kept %q", r.trace, r.kept[startUnit])
	}
	if err := got.Use(); err != nil {
		t.Fatalf("Use of a resumed turn: %v", err)
	}
}

func TestResumeOfATurnTheBookDoesNotHoldIsGone(t *testing.T) {
	r := newStartRig(t)
	_, err := r.s.Resume(context.Background(), startUnit, 4)
	var gone turns.Gone
	if !errors.As(err, &gone) {
		t.Fatalf("err = %v, want Gone", err)
	}
}

func TestAGuardThatAnswersItselfGivesBackOnlyTheKeptGuidance(t *testing.T) {
	for name, c := range map[string]struct {
		given, kept, want string
		told              []string
	}{
		"given":      {given: "fix the nit", kept: "look at the tests", want: "look at the tests"},
		"given only": {given: "fix the nit", want: ""},
		"kept only":  {kept: "look at the tests", want: "look at the tests", told: []string{"look at the tests"}},
	} {
		r := newStartRig(t)
		if c.kept != "" {
			r.kept[startUnit] = c.kept
		}
		x := r.runner(true)
		x.guardErr = fmt.Errorf("asked whether to take it over: %w", turns.ErrAnswered)
		req := byPerson(guided)
		req.Guidance = c.given
		if _, err := r.s.Start(context.Background(), req, x); !errors.Is(err, turns.ErrAnswered) {
			t.Fatalf("%s: err %v", name, err)
		}
		if len(x.ran) != 0 || slices.Contains(r.trace, "before") || r.kept[startUnit] != c.want {
			t.Errorf("%s: trace %v, kept %q, want %q", name, r.trace, r.kept[startUnit], c.want)
		}
		if !slices.Equal(x.keptBack, c.told) {
			t.Errorf("%s: the runner was told %q kept back, want %q", name, x.keptBack, c.told)
		}
	}
}

func TestARunnerIsToldOfTheGuidanceKeptBack(t *testing.T) {
	r := newStartRig(t)
	req := byPerson(guided)
	req.Guidance = "look at the tests"
	x := r.runner(false)
	if _, err := r.s.Start(context.Background(), req, x); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(x.keptBack, []string{"look at the tests"}) {
		t.Fatalf("kept back %q", x.keptBack)
	}
	r = newStartRig(t)
	x = r.runner(true)
	if _, err := r.s.Start(context.Background(), req, x); err != nil || len(x.keptBack) != 0 {
		t.Fatalf("a submitted turn kept back %q: %v", x.keptBack, err)
	}
}

func TestAStarterWithNoPrepareRefusesAnIntentThatHasOne(t *testing.T) {
	r := newStartRig(t)
	r.s.Preparer = nil
	x := r.runner(true)
	if _, err := r.s.Start(context.Background(), automated(prepared), x); err == nil || len(x.ran) != 0 {
		t.Fatalf("start = %v, ran %d", err, len(x.ran))
	}
	if _, err := r.s.Start(context.Background(), automated(plain), x); err != nil || len(x.ran) != 1 {
		t.Fatalf("start of an intent with no prepare = %v, ran %d", err, len(x.ran))
	}
}

type refusingGuides struct{ err error }

func (g refusingGuides) Ready(turns.Def) error { return g.err }

func TestGuidesThatDoNotReadStopATurnBeforeAnythingIsTouched(t *testing.T) {
	r := newStartRig(t)
	r.s.Guides = refusingGuides{errors.New("intent=review.md: a broken block")}
	x := r.runner(true)
	if _, err := r.s.Start(context.Background(), byPerson(prepared), x); err == nil {
		t.Fatal("a turn started on guides that do not read")
	}
	for _, step := range []string{"prepare", "check", "guard", "before", "run"} {
		if slices.Contains(r.trace, step) {
			t.Fatalf("trace %v", r.trace)
		}
	}
}
