package outbox

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// memApplied is the AppliedStore over a memStore.
type memApplied struct{ m *memStore }

func (a memApplied) Unapplied(context.Context) ([]draft.Draft, error) {
	a.m.mu.Lock()
	defer a.m.mu.Unlock()
	var out []draft.Draft
	for _, d := range a.m.drafts {
		if d.State == draft.Sent && d.EffectsAppliedAt.IsZero() && len(d.Effects) > 0 {
			out = append(out, d)
		}
	}
	return out, nil
}

func (a memApplied) MarkApplied(_ context.Context, id draft.ID, at time.Time) error {
	a.m.mu.Lock()
	defer a.m.mu.Unlock()
	if d := &a.m.drafts[id-1]; d.EffectsAppliedAt.IsZero() {
		d.EffectsAppliedAt = at
	}
	return nil
}

// effectLog records each effect it applies by draft, and fails the names in
// fail.
type effectLog struct {
	mu      sync.Mutex
	applied []string
	fail    map[string]error
}

func (l *effectLog) lookup(_ unit.ProviderID, name string) (Effect, bool) {
	if name == "unknown.effect" {
		return nil, false
	}
	return func(_ context.Context, d draft.Draft) error {
		l.mu.Lock()
		defer l.mu.Unlock()
		if err := l.fail[name]; err != nil {
			return err
		}
		l.applied = append(l.applied, name)
		return nil
	}, true
}

func (l *effectLog) names() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.applied)
}

func withApplier(e *env) *effectLog {
	l := &effectLog{fail: map[string]error{}}
	e.svc.Applier = EffectApplier{Store: memApplied{e.store}, Effects: l.lookup, Clock: e.clock}
	return l
}

func (e *env) sendNow(t *testing.T, p Proposal) draft.Draft {
	t.Helper()
	d := e.propose(p)
	if _, err := e.svc.Approve(ctx, d.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.svc.Send(ctx); err != nil && !errors.Is(err, errBoom) && !errors.Is(err, ErrUnknownEffect) {
		t.Fatal(err)
	}
	return e.get(d.ID)
}

func TestASentDraftAppliesItsEffectsOnce(t *testing.T) {
	e := newEnv()
	l := withApplier(e)
	p := reply("closing")
	p.Effects = []string{"entry.nudged", "unit.closed"}
	d := e.sendNow(t, p)
	if d.State != draft.Sent || !d.EffectsAppliedAt.Equal(t0) {
		t.Fatalf("draft %+v", d)
	}
	if got := l.names(); !slices.Equal(got, []string{"entry.nudged", "unit.closed"}) {
		t.Fatalf("applied %v", got)
	}
	for range 2 {
		if _, _, err := e.svc.Send(ctx); err != nil {
			t.Fatal(err)
		}
		if err := e.svc.Applier.Apply(ctx, e.get(d.ID)); err != nil {
			t.Fatal(err)
		}
	}
	if got := l.names(); len(got) != 2 {
		t.Fatalf("applied again: %v", got)
	}
	stored, err := e.store.Insert(ctx, draft.Draft{Unit: thread, Purpose: "reply", Delivery: draft.Message, Text: "an effect nobody implements now",
		State: draft.Approved, Effects: []string{"unknown.effect"}, CreatedAt: t0, UpdatedAt: t0})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.svc.Send(ctx); !errors.Is(err, ErrUnknownEffect) {
		t.Fatalf("send: %v", err)
	}
	if d := e.get(stored.ID); d.State != draft.Sent || !d.EffectsAppliedAt.IsZero() {
		t.Fatalf("a draft whose effect has no implementation is marked applied: %+v", d)
	}
}

func TestADraftDeclaringAnEffectItsProviderDoesNotImplementIsRefused(t *testing.T) {
	e := newEnv()
	withApplier(e)
	p := reply("an effect nobody implements")
	p.Effects = []string{"unknown.effect"}
	if _, err := e.svc.Propose(ctx, p); !errors.Is(err, ErrInvalid) || !errors.Is(err, ErrUnknownEffect) {
		t.Fatalf("propose: %v", err)
	}
	if ds, _ := e.svc.List(ctx, Filter{}); len(ds) != 0 {
		t.Fatalf("drafts %+v", ds)
	}
}

func TestEffectsAreAppliedOnceAcrossARestart(t *testing.T) {
	e := newEnv()
	l := withApplier(e)
	l.fail["entry.nudged"] = errBoom
	p := reply("any news?")
	p.Effects = []string{"entry.nudged"}
	d := e.sendNow(t, p)
	if d.State != draft.Sent || !d.EffectsAppliedAt.IsZero() {
		t.Fatalf("a failed effect undid the send or marked it applied: %+v", d)
	}
	before := e.store.drafts[0]
	before.State, before.EffectsAppliedAt = draft.Sent, t0.Add(-time.Hour)
	before.ID = draft.ID(len(e.store.drafts) + 1)
	before.Effects = []string{"unit.closed"}
	e.store.drafts = append(e.store.drafts, before)

	delete(l.fail, "entry.nudged")
	restarted := &Service{Store: e.store, Effects: e.svc.Effects, Clock: e.clock,
		Applier: EffectApplier{Store: memApplied{e.store}, Effects: l.lookup, Clock: e.clock}}
	for range 2 {
		if err := restarted.Recover(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if got := l.names(); !slices.Equal(got, []string{"entry.nudged"}) {
		t.Fatalf("applied %v, want the failed effect once and none of the draft applied before", got)
	}
	if got := e.get(d.ID); !got.EffectsAppliedAt.Equal(t0) {
		t.Fatalf("not marked applied: %+v", got)
	}
}

func TestNoEffectIsDerivedFromKind(t *testing.T) {
	e := newEnv()
	l := withApplier(e)
	for _, k := range draft.Kinds {
		p := reply("closing")
		p.Kind = k
		if k == draft.React {
			p.Reaction = "white_check_mark"
		}
		if d := e.sendNow(t, p); d.State != draft.Sent || len(d.Effects) != 0 {
			t.Fatalf("%s: %+v", k, d)
		}
	}
	if got := l.names(); len(got) != 0 {
		t.Fatalf("a draft that declares no effect applied %v", got)
	}
}

func TestTheApprovalIsConfigThenLegacyThenManifestElseNone(t *testing.T) {
	t.Parallel()
	a := func(m draft.ApprovalMode) *draft.Policy { return draft.Fixed(draft.Approval{Mode: m}) }
	window := draft.Fixed(draft.Approval{Mode: draft.Window, Window: time.Hour})
	for _, c := range []struct {
		name                     string
		config, legacy, manifest *draft.Policy
		want                     draft.Approval
	}{
		{"config wins", a(draft.None), a(draft.Observe), window, draft.Approval{Mode: draft.None}},
		{"then legacy", nil, a(draft.Observe), window, draft.Approval{Mode: draft.Observe}},
		{"then the manifest", nil, nil, window, window.Default},
	} {
		if got := ResolveApproval(c.config, c.legacy, c.manifest); got == nil || got.Default != c.want || len(got.Rules) != 0 {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
	}
	if got := ResolveApproval(nil, nil, nil); got != nil {
		t.Errorf("none set: %+v", got)
	}
}

func TestDuplicateReadsPurposeDeliveryAndObservedFor(t *testing.T) {
	e := newEnv()
	auto := func(purpose string, delivery draft.Delivery, a draft.ApprovalMode) Proposal {
		return Proposal{Unit: thread, Purpose: purpose, Delivery: delivery, Text: "done", Reaction: "white_check_mark",
			Policy: draft.Fixed(draft.Approval{Mode: a}), ObservedFor: time.Hour,
			Origin: draft.Origin{Kind: draft.OriginAuto, Workflow: "closure", Rule: "closure", Key: thread.String()}}
	}
	first := e.propose(auto("close", draft.Message, draft.Always))
	if d, err := e.svc.Propose(ctx, auto("close", draft.Message, draft.Always)); !errors.Is(err, ErrDuplicate) || d.ID != first.ID {
		t.Fatalf("same purpose and delivery: %+v %v", d, err)
	}
	for _, p := range []Proposal{auto("close", draft.Reaction, draft.Always), auto("thanks", draft.Message, draft.Always)} {
		if _, err := e.svc.Propose(ctx, p); err != nil {
			t.Fatalf("%s %s: %v", p.Purpose, p.Delivery, err)
		}
	}
	observed := e.propose(auto("observed", draft.Message, draft.Observe))
	e.clock.now = t0.Add(59 * time.Minute)
	if _, err := e.svc.Propose(ctx, auto("observed", draft.Message, draft.Observe)); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("inside ObservedFor: %v", err)
	}
	e.clock.now = t0.Add(time.Hour)
	if d, err := e.svc.Propose(ctx, auto("observed", draft.Message, draft.Observe)); err != nil || d.ID == observed.ID {
		t.Fatalf("past ObservedFor: %+v %v", d, err)
	}
}

func TestACommentDraftWaitsForAPersonWhateverItsMode(t *testing.T) {
	e := newEnv()
	for _, mode := range []draft.Approval{{Mode: draft.None}, {Mode: draft.Window, Window: time.Minute}} {
		p := Proposal{Unit: thread, Purpose: "nudge", Delivery: draft.CommentDelivery, Text: "any news?", Policy: draft.Fixed(mode),
			Origin: draft.Origin{Kind: draft.OriginAuto, Workflow: "my-followups", Rule: "my-followups", Key: string(mode.Mode)}}
		d := e.propose(p)
		if d.State != draft.Proposed || d.Approval.Mode != draft.Always || !d.Deadline.IsZero() || d.Would.Policy != draft.WouldHold {
			t.Fatalf("%s: %+v", mode.Mode, d)
		}
	}
	e.clock.now = t0.Add(time.Hour)
	if n, err := e.svc.Tick(ctx); n != 0 || err != nil {
		t.Fatalf("tick approved %d %v", n, err)
	}
}
