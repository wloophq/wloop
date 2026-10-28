package proposals

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// githubSource proposes one nudge per call on pull, claiming more than a rule id.
type githubSource struct {
	mu    sync.Mutex
	calls int
	out   []outbox.Proposal
	err   error
}

func (g *githubSource) Proposals(ctx context.Context, now time.Time) ([]outbox.Proposal, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls++
	return slices.Clone(g.out), g.err
}

func ghNudge(u unit.Unit) outbox.Proposal {
	return outbox.Proposal{Unit: u, Kind: draft.Nudge, Text: "@{github:bob}, any news?", Task: "e-forged",
		Origin: draft.Origin{Kind: draft.OriginTurn, TurnSeq: 9, Intent: "autopost", Workflow: "w", Rule: "github-nudge", Key: "forged"},
		Policy: draft.Fixed(draft.Approval{Mode: draft.Send})}
}

func TestGitHubUnitGetsOneNudgeDraft(t *testing.T) {
	e := newEnv(t)
	src := &githubSource{out: []outbox.Proposal{ghNudge(pull)}}
	e.job.Sources = []Source{{Provider: unit.GitHub, Proposals: src.Proposals}}
	e.run()
	all := e.drafts.all()
	if len(all) != 1 {
		t.Fatalf("%d drafts: %+v", len(all), all)
	}
	d := all[0]
	want := draft.Origin{Kind: draft.OriginAuto, Rule: "github-nudge", Key: pull.String()}
	if g := e.gateOf(d); d.Origin != want || g.Status != gate.Open || !g.Deadline.IsZero() || d.State != draft.Held || d.Task != "" {
		t.Fatalf("the plugin's self-declared origin or approval leaked: %+v", d)
	}
}

func TestPluginProposalsOutsideTheirLaneAreDropped(t *testing.T) {
	e := newEnv(t)
	foreign := ghNudge(thread)
	norule := ghNudge(pull)
	norule.Origin.Rule = ""
	src := &githubSource{out: []outbox.Proposal{foreign, norule}}
	e.job.Sources = []Source{{Provider: unit.GitHub, Proposals: src.Proposals}}
	e.run()
	if all := e.drafts.all(); len(all) != 0 {
		t.Fatalf("proposed %+v", all)
	}
}

func TestModuleProposalsAreProposed(t *testing.T) {
	e := newEnv(t)
	other := unit.Unit{Provider: unit.GitHub, Key: "o/r#2"}
	src := &githubSource{out: []outbox.Proposal{ghNudge(pull), ghNudge(other)}}
	e.job.Sources = []Source{{Provider: unit.GitHub, Proposals: src.Proposals}}
	if sum := e.run(); sum.Proposed != 2 {
		t.Fatalf("%s", sum)
	}
	if sum := e.run(); sum.Proposed != 0 || sum.Held != 2 || len(e.drafts.all()) != 2 {
		t.Fatalf("second pass %s", sum)
	}
}

func TestPluginRulesDraftsTakeTheirPluginsApproval(t *testing.T) {
	e := newEnv(t)
	e.job.Approval = func(p unit.ProviderID) draft.Approval {
		if p == unit.GitHub {
			return draft.Approval{Mode: draft.Delay, Window: time.Hour}
		}
		return draft.Approval{Mode: draft.Wait}
	}
	src := &githubSource{out: []outbox.Proposal{ghNudge(pull)}}
	e.job.Sources = []Source{{Provider: unit.GitHub, Proposals: src.Proposals}}
	e.run()
	if g := e.gateOf(e.list("github-nudge")[0]); g.Status != gate.Open || !g.Deadline.Equal(t0.Add(time.Hour)) {
		t.Fatalf("configured rule %+v", g)
	}
}

func TestProposalsSkipDisabledPlugins(t *testing.T) {
	e := newEnv(t)
	src := &githubSource{out: []outbox.Proposal{ghNudge(pull)}}
	e.job.Sources = []Source{{Provider: unit.GitHub, Proposals: src.Proposals}}
	e.run()
	open := e.list("github-nudge", draft.Held)
	e.disabled[unit.GitHub] = true
	e.facts[pull] = task.UnitFacts{Unit: pull, ClosurePosted: true}
	e.clock.set(t0.Add(72 * time.Hour))
	if sum := e.run(); sum.Proposed+sum.Refused+sum.Held+sum.Discarded != 0 {
		t.Fatalf("acted on a disabled plugin: %s", sum)
	}
	for _, d := range open {
		if got, _ := e.drafts.Get(ctx, d.ID); got.State != draft.Held {
			t.Fatalf("draft %d is %s", d.ID, got.State)
		}
		if _, err := e.svc.Prepare(ctx, d.ID, d.Rev); !errors.Is(err, outbox.ErrDisabled) {
			t.Fatalf("approve on a disabled plugin: %v", err)
		}
	}
}

func TestDiscardedProposalIsNotProposedAgain(t *testing.T) {
	setup := func(t *testing.T) (*env, *githubSource) {
		e := newEnv(t)
		src := &githubSource{out: []outbox.Proposal{ghNudge(pull)}}
		e.job.Sources = []Source{{Provider: unit.GitHub, Proposals: src.Proposals}}
		return e, src
	}
	t.Run("discarded", func(t *testing.T) {
		e, _ := setup(t)
		e.run()
		d := e.drafts.all()[0]
		if err := e.gates.Discard(ctx, d.ID); err != nil {
			t.Fatal(err)
		}
		e.clock.set(t0.Add(23 * time.Hour))
		if sum := e.run(); sum.Proposed != 0 {
			t.Fatalf("re-proposed within repeat_after: %s", sum)
		}
		e.clock.set(t0.Add(24 * time.Hour))
		if sum := e.run(); sum.Proposed != 1 {
			t.Fatalf("after repeat_after: %s", sum)
		}
	})
	t.Run("failed", func(t *testing.T) {
		e, _ := setup(t)
		e.run()
		d := e.drafts.all()[0]
		d.State, d.Error = draft.Failed, "not posted"
		if _, err := e.drafts.Update(ctx, d, draft.Held); err != nil {
			t.Fatal(err)
		}
		e.clock.set(t0.Add(30 * 24 * time.Hour))
		if sum := e.run(); sum.Proposed != 0 || len(e.drafts.all()) != 1 {
			t.Fatalf("proposed beside a failed draft: %s", sum)
		}
	})
	t.Run("observed", func(t *testing.T) {
		e, _ := setup(t)
		e.job.Approval = func(unit.ProviderID) draft.Approval { return draft.Approval{Mode: draft.DryRun} }
		e.run()
		e.clock.set(t0.Add(23 * time.Hour))
		e.run()
		if all := e.drafts.all(); len(all) != 1 || all[0].Reason != string(draft.DryRun) {
			t.Fatalf("observed twice within repeat_after: %+v", all)
		}
		e.clock.set(t0.Add(24 * time.Hour))
		e.run()
		if all := e.drafts.all(); len(all) != 2 || all[1].Reason != string(draft.DryRun) {
			t.Fatalf("an observed key is suppressed for good: %+v", all)
		}
	})
}

func TestEmptyPluginPassKeepsItsDrafts(t *testing.T) {
	e := newEnv(t)
	src := &githubSource{out: []outbox.Proposal{ghNudge(pull)}}
	e.job.Sources = []Source{{Provider: unit.GitHub, Proposals: src.Proposals}}
	e.run()
	src.out = nil
	e.run()
	if all := e.drafts.all(); len(all) != 1 || all[0].State != draft.Held {
		t.Fatalf("%+v", all)
	}
}

func TestConcurrentPassesProposeOnce(t *testing.T) {
	for round := range 10 {
		e := newEnv(t)
		other := unit.Unit{Provider: unit.GitHub, Key: "o/r#2"}
		src := &githubSource{out: []outbox.Proposal{ghNudge(pull), ghNudge(other)}}
		e.job.Sources = []Source{{Provider: unit.GitHub, Proposals: src.Proposals}}
		const passes = 8
		var calling sync.WaitGroup
		calling.Add(passes)
		allCalling := make(chan struct{})
		go func() {
			calling.Wait()
			close(allCalling)
		}()
		e.drafts.beforeList = func() { <-allCalling }
		var wg sync.WaitGroup
		for range passes {
			wg.Add(1)
			go func() {
				defer wg.Done()
				calling.Done()
				if _, err := e.job.Run(ctx); err != nil {
					t.Error(err)
				}
			}()
		}
		wg.Wait()
		if all := e.drafts.all(); len(all) != 2 {
			t.Fatalf("round %d: %d drafts, want 2", round, len(all))
		}
	}
}

func TestPluginNudgeWithdrawnWhenTheUnitCloses(t *testing.T) {
	e := newEnv(t)
	other := unit.Unit{Provider: unit.GitHub, Key: "o/r#2"}
	src := &githubSource{out: []outbox.Proposal{ghNudge(pull), ghNudge(other)}}
	e.job.Sources = []Source{{Provider: unit.GitHub, Proposals: src.Proposals}}
	e.run()
	src.out = nil
	if sum := e.run(); sum.Discarded != 0 {
		t.Fatalf("an empty plugin pass withdrew: %s", sum)
	}
	e.facts[pull] = task.UnitFacts{Unit: pull, ClosurePosted: true}
	if sum := e.run(); sum.Discarded != 1 {
		t.Fatalf("%s", sum)
	}
	for _, d := range e.drafts.all() {
		switch {
		case d.Unit == pull && (d.State != draft.Dropped || d.Reason != ReasonMoot):
			t.Fatalf("merged PR's nudge %s %q", d.State, d.Reason)
		case d.Unit == other && d.State != draft.Held:
			t.Fatalf("open PR's nudge %s", d.State)
		}
	}
}
