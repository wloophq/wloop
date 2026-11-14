package cli

import (
	"context"
	"io"
	"testing"

	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/daemon"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
)

const brokenReviewBlock = "\n+++\noverride = true\n+++\nX"

// breakGuides gives te a profile whose review block does not read.
func breakGuides(t *testing.T, te *testEnv) {
	t.Helper()
	te.cfg.Profile = userProfile(t, te, map[string]string{"intent=review.md": brokenReviewBlock})
}

// touched is what a turn's start did for it: the prepares it ran, the
// pending plan items it dropped and the prompts it typed.
type touched struct{ prepares, drops, prompts int }

func TestEveryNewTurnOfAnIntentWithGuidesStopsOnABrokenBlockBeforeAnythingIsTouched(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		run  func(t *testing.T) (error, touched)
	}{
		{"a nudge asked through the action, in the pull request's workspace else main", func(t *testing.T) (error, touched) {
			m := warmReviewWorkspace()
			te := newTestEnv(t, m)
			te.gh.basics = openBasics
			breakGuides(t, te)
			dropped := withDroppedPlan(te)
			p := &fakePreparer{filled: preparedNudge}
			te.Turns.Starter.Preparer = startPreparer{p}
			d, _ := te.loadedIntents().Registry.Get(dintent.Nudge)
			err := te.askedRound(context.Background(), d, te.cfg, r7, "", nil, "", "")
			return err, touched{len(p.asked), len(dropped.drops()), len(m.Prompts)}
		}},
		{"a sweep follow-up", func(t *testing.T) (error, touched) {
			sc := followupScenario(t)
			r := newStartRig(t, sc, string(dintent.Review), roundFollowup)
			breakGuides(t, sc.te)
			_, err := r.run(t, automation.RunOpts{})
			return err, touched{prompts: len(sc.ws.Prompts) + len(sc.te.bookOf(t, sc.ref).Turns)}
		}},
		{"run now of a held follow-up", func(t *testing.T) (error, touched) {
			sc := followupScenario(t)
			exhaustCap(t, sc)
			sc.te.gh.basics = openBasics
			breakGuides(t, sc.te)
			dropped := withDroppedPlan(sc.te)
			err := (envActions{sc.env, nil}).Run(context.Background(), sc.ref.Unit(), daemon.Call{Out: io.Discard})
			return err, touched{drops: len(dropped.drops()), prompts: len(sc.ws.Prompts)}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if err, got := c.run(t); err == nil || got != (touched{}) {
				t.Fatalf("err %v, touched %+v; want the broken block to stop the turn first", err, got)
			}
		})
	}
}
func TestAnIntentThatComposesNoGuideIsNotStoppedByABrokenBlock(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, nil)
	breakGuides(t, te)
	for _, name := range []dintent.Name{"probe", "act", "chat"} {
		d, ok := te.loadedIntents().Registry.Get(name)
		if !ok {
			t.Fatalf("no intent %s", name)
		}
		if err := te.guidesReady(d); err != nil {
			t.Errorf("%s: %v; it renders no guide", name, err)
		}
	}
	d, _ := te.loadedIntents().Registry.Get(dintent.Nudge)
	if err := te.guidesReady(d); err == nil {
		t.Error("nudge, which composes its guide, was not stopped")
	}
}
