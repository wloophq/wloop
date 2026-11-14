package cli

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"

	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/intent/builtin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

type appliedOn struct {
	mu  sync.Mutex
	got []string
}

func (a *appliedOn) lookup(_ unit.ProviderID, name string) (outbox.Effect, bool) {
	return func(_ context.Context, d draft.Draft) error {
		a.mu.Lock()
		defer a.mu.Unlock()
		a.got = append(a.got, name+" "+d.Unit.String())
		return nil
	}, true
}

// effectsRig is a draft rig whose Outbox applies effects without a draft, recording them.
func effectsRig(t *testing.T, effects ...string) (*draftRig, *appliedOn) {
	t.Helper()
	st := workflow.Step{Use: builtin.StepEffectsApply, Args: map[string]any{"effects": effects}}
	r := newDraftRig(t, workflow.Workflow{Name: "closes", Steps: []workflow.Step{st}}, "")
	a := &appliedOn{}
	r.svc.Silent = builtin.SilentEffect
	r.svc.Applier = outbox.EffectApplier{Effects: a.lookup}
	return r, a
}

func TestEffectsApplyClosesTheUnitWithNoDraft(t *testing.T) {
	t.Parallel()
	r, a := effectsRig(t, builtin.EffectUnitClosed)
	if refusal := r.check(draftThread); refusal != "" {
		t.Fatalf("check refused: %s", refusal)
	}
	r.run(draftThread)
	if want := []string{builtin.EffectUnitClosed + " " + draftThread.String()}; !slices.Equal(a.got, want) {
		t.Fatalf("applied %v, want %v", a.got, want)
	}
	if ds := r.allDrafts(); len(ds) != 0 {
		t.Fatalf("drafts %+v, want none", ds)
	}
}

func TestEffectsApplyRefusesAnEffectThatNeedsAPost(t *testing.T) {
	t.Parallel()
	r, a := effectsRig(t, builtin.EffectTaskNudged)
	_, err := r.s.Run(context.Background(), r.wf.Steps[0], draftThread, workflow.Why{Workflow: r.wf.Name}, automation.RunOpts{})
	var refused automation.Refused
	if !errors.As(err, &refused) || len(a.got) != 0 {
		t.Fatalf("run %v, applied %v; want it refused, nothing applied", err, a.got)
	}
}

func TestEffectsApplyCheckRefusesAnEffectThatNeedsAPost(t *testing.T) {
	t.Parallel()
	r, _ := effectsRig(t, builtin.EffectTaskNudged)
	if refusal := r.check(draftThread); refusal == "" {
		t.Fatal("check passed a step naming task.nudged")
	}
}

func TestEffectsApplyRetriesWhileThePluginIsDown(t *testing.T) {
	t.Parallel()
	r, _ := effectsRig(t, builtin.EffectUnitClosed)
	r.svc.Applier = outbox.EffectApplier{Effects: func(unit.ProviderID, string) (outbox.Effect, bool) {
		return func(context.Context, draft.Draft) error { return fmt.Errorf("closing: %w", src.ErrUnavailable) }, true
	}}
	_, err := r.s.Run(context.Background(), r.wf.Steps[0], draftThread, workflow.Why{Workflow: r.wf.Name}, automation.RunOpts{})
	var transient automation.Transient
	if !errors.As(err, &transient) {
		t.Fatalf("run %v, want a transient failure the next pass retries", err)
	}
}
