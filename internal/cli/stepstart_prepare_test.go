package cli

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/core/automation"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// fakePreparer fills a turn's inputs with filled, or refuses it with refusal.
type fakePreparer struct {
	filled  map[string]any
	refusal error
	asked   []string
}

func (p *fakePreparer) PrepareTurn(_ context.Context, intent string, u unit.Unit, _ string, inputs map[string]any) (map[string]any, error) {
	p.asked = append(p.asked, intent+" "+u.String())
	if p.refusal != nil {
		return nil, p.refusal
	}
	out := maps.Clone(inputs)
	if out == nil {
		out = map[string]any{}
	}
	maps.Copy(out, p.filled)
	return out, nil
}

var preparedNudge = map[string]any{"situation": "Waiting on ana's review since Monday.", "who": []string{"ana"}}

func TestAWorkflowNudgeLaunchesWithWhatItsPrepareFilled(t *testing.T) {
	t.Parallel()
	r := nudgeStartRig(t)
	p := &fakePreparer{filled: preparedNudge}
	r.steps.d.Prepare = p
	if _, err := r.run(t, automation.RunOpts{}); err != nil {
		t.Fatal(err)
	}
	if want := []string{string(dintent.Nudge) + " " + r.u.String()}; !slices.Equal(p.asked, want) {
		t.Fatalf("prepares asked %v, want %v", p.asked, want)
	}
	b := r.sc.te.unitBook(t, r.u)
	if len(b.Turns) != 1 {
		t.Fatalf("turns %+v", b.Turns)
	}
	in := b.Turns[0].Inputs
	if in["situation"] != preparedNudge["situation"] || fmt.Sprint(in["who"]) != "[ana]" {
		t.Fatalf("the turn's inputs %v", in)
	}
}

func TestAWorkflowNudgeItsPrepareRefusesIsRefused(t *testing.T) {
	t.Parallel()
	r := nudgeStartRig(t)
	r.steps.d.Prepare = &fakePreparer{refusal: fmt.Errorf("%w: this wait was nudged already", src.ErrUnfit)}
	_, err := r.run(t, automation.RunOpts{})
	var refused automation.Refused
	if !errors.As(err, &refused) || refused.Reason != "this wait was nudged already" {
		t.Fatalf("run: %v, want refused with the prepare's reason", err)
	}
	if b := r.sc.te.unitBook(t, r.u); len(b.Turns) != 0 {
		t.Fatalf("a refused prepare launched %+v", b.Turns)
	}
}

func TestAHeadlessWorkflowTurnCarriesWhatItsPrepareFilled(t *testing.T) {
	t.Parallel()
	r := nudgeStartRig(t)
	r.st.Args["runner"] = string(dturn.RunnerHeadless)
	h := &headlessCalls{}
	r.steps.d.Headless = h
	r.steps.d.Prepare = &fakePreparer{filled: preparedNudge}
	if _, err := r.run(t, automation.RunOpts{}); err != nil {
		t.Fatal(err)
	}
	if len(h.got) != 1 || h.got[0].Inputs["situation"] != preparedNudge["situation"] {
		t.Fatalf("headless calls %+v", h.got)
	}
}

func TestAHeadlessWorkflowTurnItsPrepareRefusesStartsNothing(t *testing.T) {
	t.Parallel()
	r := nudgeStartRig(t)
	r.st.Args["runner"] = string(dturn.RunnerHeadless)
	h := &headlessCalls{}
	r.steps.d.Headless = h
	r.steps.d.Prepare = &fakePreparer{refusal: fmt.Errorf("%w: nobody to ask", src.ErrUnfit)}
	_, err := r.run(t, automation.RunOpts{})
	if !errors.As(err, new(automation.Refused)) || len(h.got) != 0 {
		t.Fatalf("run: %v; headless calls %+v", err, h.got)
	}
}

func TestATurnWhoseIntentHasAPrepareNeedsSomeoneToRunIt(t *testing.T) {
	t.Parallel()
	r := nudgeStartRig(t)
	if _, err := r.run(t, automation.RunOpts{}); err == nil || errors.As(err, new(automation.Refused)) {
		t.Fatalf("run without a preparer: %v, want an error", err)
	}
	if b := r.sc.te.unitBook(t, r.u); len(b.Turns) != 0 {
		t.Fatalf("launched unprepared %+v", b.Turns)
	}
}
