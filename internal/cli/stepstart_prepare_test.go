package cli

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/turns"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
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
	r.sc.te.Turns.Starter.Preparer = startPreparer{p}
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
	r.sc.te.Turns.Starter.Preparer = startPreparer{&fakePreparer{refusal: fmt.Errorf("%w: this wait was nudged already", src.ErrUnfit)}}
	_, err := r.run(t, automation.RunOpts{})
	var refused automation.Refused
	if !errors.As(err, &refused) || refused.Reason != "this wait was nudged already" {
		t.Fatalf("run: %v, want refused with the prepare's reason", err)
	}
	if b := r.sc.te.unitBook(t, r.u); len(b.Turns) != 0 {
		t.Fatalf("a refused prepare launched %+v", b.Turns)
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

func TestAWorkflowNudgeLeavesTheGuidanceKeptForTheNextReview(t *testing.T) {
	t.Parallel()
	r := nudgeStartRig(t)
	r.sc.te.Turns.Starter.Preparer = startPreparer{&fakePreparer{filled: preparedNudge}}
	if err := r.rec.KeepNextGuidance(context.Background(), r.sc.ref, "check the migration"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.run(t, automation.RunOpts{}); err != nil {
		t.Fatal(err)
	}
	b := r.sc.te.unitBook(t, r.u)
	if len(b.Turns) != 1 || b.Turns[0].Guidance != "" {
		t.Fatalf("turns %+v", b.Turns)
	}
	if got := r.rec.overrides[r.sc.ref].NextGuidance; got != "check the migration" {
		t.Fatalf("the nudge took the guidance kept for the next review: %q", got)
	}
}

// preparedReview gives the review role a prepare, run by p.
func preparedReview(st *turns.Starter, p turns.Preparer) {
	st.Preparer = startPreparer{p}
	st.Defs = editedDefs{Defs: st.Defs, edit: func(d *turns.Def) {
		if d.Name == dintent.Review {
			d.Prepare = "action:review.prepare"
		}
	}}
}

func TestAWorkflowRoundRunsItsRolesPrepare(t *testing.T) {
	t.Parallel()
	sc := followupScenario(t)
	review, err := readBundled("intents/review/intent.toml")
	if err != nil {
		t.Fatal(err)
	}
	userProfile(t, sc.te, map[string]string{"intents/review/intent.toml": string(review) + `
[inputs.focus]
type    = "text"
from    = "prepare"
hidden  = true
persist = true
`})
	r := newStartRig(t, sc, string(dintent.Review), roundFollowup)
	p := &fakePreparer{filled: map[string]any{"focus": "the migration"}}
	preparedReview(sc.te.Turns.Starter, p)
	if got, err := r.run(t, automation.RunOpts{}); err != nil || got != "sent the follow-up: the author answered" {
		t.Fatalf("run = %q, %v", got, err)
	}
	if want := []string{string(dintent.Review) + " " + r.u.String()}; !slices.Equal(p.asked, want) {
		t.Fatalf("prepares asked %v, want %v", p.asked, want)
	}
	b := sc.te.unitBook(t, r.u)
	if len(b.Turns) != 1 || b.Turns[0].Inputs["focus"] != "the migration" {
		t.Fatalf("turns %+v", b.Turns)
	}
}

func TestAWorkflowRoundItsPrepareRefusesSendsNothing(t *testing.T) {
	t.Parallel()
	sc := followupScenario(t)
	r := newStartRig(t, sc, string(dintent.Review), roundFollowup)
	preparedReview(sc.te.Turns.Starter, &fakePreparer{refusal: fmt.Errorf("%w: nothing changed since the last review", src.ErrUnfit)})
	_, err := r.run(t, automation.RunOpts{})
	var refused automation.Refused
	if !errors.As(err, &refused) || refused.Reason != "nothing changed since the last review" {
		t.Fatalf("run: %v, want refused with the prepare's reason", err)
	}
	if len(sc.ws.Prompts) != 0 || len(sc.te.unitBook(t, r.u).Turns) != 0 {
		t.Fatalf("a refused prepare sent %v", sc.ws.Prompts)
	}
}
