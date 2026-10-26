package proposals

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gtd"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/unit"
)

var slackMe = gtd.Me{Keys: []string{me.Key}}

// stalledThread is a Slack thread I asked in, with three items waiting on alice and one on bob, all a week old.
func stalledThread(e *env) {
	e.ledger.put(
		item(thread, "e1", "Ship the release notes", me, alice),
		item(thread, "e2", "Rotate the staging key", me, alice),
		item(thread, "e3", "Review the runbook", me, alice),
		item(thread, "e4", "Book the room", me, bob),
	)
	e.sync(thread, slackMe)
}

func TestStalledProposesOneNudgePerPerson(t *testing.T) {
	e := newEnv(t)
	stalledThread(e)
	if sum := e.run(); sum.Proposed != 2 {
		t.Fatalf("%s; logs %v", sum, e.logs)
	}
	got := e.list(RuleStalledNudge)
	if len(got) != 2 {
		t.Fatalf("%d drafts", len(got))
	}
	byKey := map[string]draft.Draft{}
	for _, d := range got {
		byKey[d.Origin.Key] = d
	}
	aliceEntry, bobEntry := e.entry(gtd.KindWaiting, thread, alice.Key), e.entry(gtd.KindWaiting, thread, bob.Key)
	a, b := byKey[string(aliceEntry.ID)+":"+alice.Key], byKey[string(bobEntry.ID)+":"+bob.Key]
	if a.ID == 0 || b.ID == 0 {
		t.Fatalf("keys %v", slices.Collect(func(yield func(string) bool) {
			for k := range byKey {
				yield(k)
			}
		}))
	}
	if a.Kind != draft.Nudge || a.Unit != thread || a.Entry != string(aliceEntry.ID) || !slices.Equal(a.Items, []string{"e1", "e2", "e3"}) ||
		!contains(a.Text, "@{slack:U000AAA}", "Ship the release notes", "Rotate the staging key", "Review the runbook") || contains(a.Text, "Book the room") {
		t.Fatalf("alice's nudge %+v", a)
	}
	if !slices.Equal(b.Items, []string{"e4"}) || !contains(b.Text, "@{slack:U000BBB}", "Book the room") {
		t.Fatalf("bob's nudge %+v", b)
	}
	for _, d := range got {
		if d.Origin.Kind != draft.OriginAuto || d.Origin.Rule != RuleStalledNudge || d.Approval.Mode != draft.Always || d.State != draft.Proposed {
			t.Fatalf("%+v", d)
		}
	}
}

func TestStalledWaitsForTheWorkingDays(t *testing.T) {
	e := newEnv(t)
	young := item(thread, "e1", "Ship it", me, alice)
	young.CreatedAt = time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	e.ledger.put(young)
	e.sync(thread, slackMe)
	if sum := e.run(); sum.Proposed != 0 {
		t.Fatalf("one working day old: %s", sum)
	}
	e.clock.set(time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC))
	if sum := e.run(); sum.Proposed != 1 {
		t.Fatalf("two working days old: %s", sum)
	}
}

func TestNoNudgeWhileOneIsOpenOrRecent(t *testing.T) {
	t.Run("open", func(t *testing.T) {
		e := newEnv(t)
		stalledThread(e)
		e.run()
		if sum := e.run(); sum.Proposed != 0 || sum.Held != 2 || len(e.list(RuleStalledNudge)) != 2 {
			t.Fatalf("second pass %s, %d drafts", sum, len(e.list(RuleStalledNudge)))
		}
	})
	t.Run("recent last nudge", func(t *testing.T) {
		e := newEnv(t)
		stalledThread(e)
		for _, p := range []items.Person{alice, bob} {
			en := e.entry(gtd.KindWaiting, thread, p.Key)
			w := *en.Waiting
			w.LastNudge = t0.Add(-23 * time.Hour)
			en.Waiting = &w
			if err := e.entries.Apply(ctx, []gtd.Change{{Op: gtd.OpUpdate, Entry: en}}); err != nil {
				t.Fatal(err)
			}
		}
		if sum := e.run(); sum.Proposed != 0 {
			t.Fatalf("nudged 23h ago: %s", sum)
		}
		e.clock.set(t0.Add(time.Hour))
		if sum := e.run(); sum.Proposed != 2 {
			t.Fatalf("nudged 24h ago: %s", sum)
		}
	})
	t.Run("resurface", func(t *testing.T) {
		e := newEnv(t)
		stalledThread(e)
		en := e.entry(gtd.KindWaiting, thread, alice.Key)
		w := *en.Waiting
		w.Resurface = t0.Add(48 * time.Hour)
		en.Waiting = &w
		if err := e.entries.Apply(ctx, []gtd.Change{{Op: gtd.OpUpdate, Entry: en}}); err != nil {
			t.Fatal(err)
		}
		if sum := e.run(); sum.Proposed != 1 {
			t.Fatalf("alice resurfaces later, bob is due: %s", sum)
		}
		e.clock.set(t0.Add(48 * time.Hour))
		if sum := e.run(); sum.Proposed != 1 {
			t.Fatalf("alice resurfaced: %s", sum)
		}
	})
}

func TestSentNudgeStampsTheEntry(t *testing.T) {
	e := newEnv(t)
	stalledThread(e)
	e.run()
	en := e.entry(gtd.KindWaiting, thread, alice.Key)
	d := e.list(RuleStalledNudge, draft.Proposed)
	i := slices.IndexFunc(d, func(d draft.Draft) bool { return d.Entry == string(en.ID) })
	e.clock.set(t0.Add(time.Minute))
	if _, err := e.svc.Approve(ctx, d[i].ID, nil); err != nil {
		t.Fatal(err)
	}
	if sent, _, err := e.svc.Send(ctx); err != nil || sent != 1 {
		t.Fatalf("sent %d: %v", sent, err)
	}
	got := e.entry(gtd.KindWaiting, thread, alice.Key)
	if !got.Waiting.LastNudge.Equal(t0.Add(time.Minute)) || got.State != gtd.Waiting {
		t.Fatalf("entry %+v", got.Waiting)
	}
	if other := e.entry(gtd.KindWaiting, thread, bob.Key); !other.Waiting.LastNudge.IsZero() {
		t.Fatalf("bob's entry stamped: %+v", other.Waiting)
	}
	e.clock.set(t0.Add(2 * time.Hour))
	if sum := e.run(); sum.Proposed != 0 {
		t.Fatalf("re-proposed right after the nudge: %s", sum)
	}
	e.clock.set(t0.Add(26 * time.Hour))
	if sum := e.run(); sum.Proposed != 1 {
		t.Fatalf("after renudge_after: %s", sum)
	}
}

func TestStampCatchesUpWithoutTheHook(t *testing.T) {
	e := newEnv(t)
	e.svc.OnSent = nil
	stalledThread(e)
	e.run()
	for _, d := range e.list(RuleStalledNudge, draft.Proposed) {
		if _, err := e.svc.Approve(ctx, d.ID, nil); err != nil {
			t.Fatal(err)
		}
	}
	e.clock.set(t0.Add(time.Minute))
	e.svc.Send(ctx)
	if err := e.job.Stamp(ctx); err != nil {
		t.Fatal(err)
	}
	for _, p := range []items.Person{alice, bob} {
		if got := e.entry(gtd.KindWaiting, thread, p.Key); !got.Waiting.LastNudge.Equal(t0.Add(time.Minute)) {
			t.Fatalf("%s: %+v", p.Key, got.Waiting)
		}
	}
	applies := e.entries.Applies
	if err := e.job.Stamp(ctx); err != nil || e.entries.Applies != applies {
		t.Fatalf("a second stamp wrote again: %v", err)
	}
}

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
	return outbox.Proposal{Unit: u, Kind: draft.Nudge, Text: "@{github:bob}, any news?", Entry: "e-forged",
		Origin:   draft.Origin{Kind: draft.OriginTurn, TurnSeq: 9, Intent: "autopost", Workflow: "w", Rule: "github-nudge", Key: "forged"},
		Approval: &draft.Approval{Mode: draft.None}}
}

func TestGitHubUnitGetsOneNudgeDraft(t *testing.T) {
	e := newEnv(t)
	src := &githubSource{out: []outbox.Proposal{ghNudge(pull)}}
	e.job.Sources = []Source{{Provider: unit.GitHub, Proposals: src.Proposals}}
	e.ledger.put(item(pull, "gh:review", "Review o/r#1", ghMe, ghBob))
	e.sync(pull, gtd.Me{Keys: []string{ghMe.Key}})
	if en := e.entry(gtd.KindWaiting, pull, ghBob.Key); en.State != gtd.Waiting {
		t.Fatalf("no stale waiting entry to tempt the generic rule: %+v", en)
	}
	e.run()
	all := e.drafts.all()
	if len(all) != 1 {
		t.Fatalf("%d drafts: %+v", len(all), all)
	}
	d := all[0]
	want := draft.Origin{Kind: draft.OriginAuto, Rule: "github-nudge", Key: pull.String()}
	if d.Origin != want || d.Approval != (draft.Approval{Mode: draft.Always}) || d.State != draft.Proposed || d.Entry != "" {
		t.Fatalf("the plugin's self-declared origin or approval leaked: %+v", d)
	}
}

func TestPluginProposalsOutsideTheirLaneAreDropped(t *testing.T) {
	e := newEnv(t)
	core := ghNudge(pull)
	core.Origin.Rule = RuleClosure
	foreign := ghNudge(thread)
	norule := ghNudge(pull)
	norule.Origin.Rule = ""
	src := &githubSource{out: []outbox.Proposal{core, foreign, norule}}
	e.job.Sources = []Source{{Provider: unit.GitHub, Proposals: src.Proposals}}
	e.run()
	if all := e.drafts.all(); len(all) != 0 {
		t.Fatalf("proposed %+v", all)
	}
}

func TestClosureProposedOnce(t *testing.T) {
	e := newEnv(t)
	e.facts[thread] = gtd.UnitFacts{StartedByMe: true}
	e.ledger.put(
		resolved(item(thread, "e1", "Ship the release notes", me, alice), items.Done),
		resolved(item(thread, "e2", "Is staging up?", me, bob), items.Answered),
	)
	e.sync(thread, slackMe)
	closeEntry := e.entry(gtd.KindClose, thread, "")
	e.run()
	e.run()
	got := e.list(RuleClosure)
	if len(got) != 1 {
		t.Fatalf("%d close drafts", len(got))
	}
	d := got[0]
	if d.Kind != draft.Close || d.Entry != string(closeEntry.ID) || d.Origin != (draft.Origin{Kind: draft.OriginAuto, Rule: RuleClosure, Key: thread.String()}) ||
		!slices.Equal(d.Items, []string{"e1", "e2"}) || !contains(d.Text, "Ship the release notes: done", "Is staging up?: answered") {
		t.Fatalf("%+v", d)
	}
}

func TestSentClosureEndsTheUnit(t *testing.T) {
	e := newEnv(t)
	e.facts[thread] = gtd.UnitFacts{StartedByMe: true}
	e.ledger.put(resolved(item(thread, "e1", "Ship it", me, alice), items.Done))
	e.sync(thread, slackMe)
	e.run()
	d := e.list(RuleClosure)[0]
	if _, err := e.svc.Approve(ctx, d.ID, nil); err != nil {
		t.Fatal(err)
	}
	if sent, _, err := e.svc.Send(ctx); sent != 1 || err != nil {
		t.Fatalf("sent %d: %v", sent, err)
	}
	its, _ := e.ledger.Items(ctx, thread)
	posted := slices.ContainsFunc(its, func(it items.Item) bool {
		return slices.ContainsFunc(it.Trail, func(ev items.Evidence) bool {
			return ev.Kind == items.EvidenceSentDraft && ev.Anchor.Ref == fmt.Sprintf("draft:%d", d.ID)
		})
	})
	if !posted {
		t.Fatalf("no close evidence: %+v", its)
	}
	e.facts[thread] = gtd.UnitFacts{StartedByMe: true, ClosurePosted: true}
	e.clock.set(t0.Add(48 * time.Hour))
	e.sync(thread, slackMe)
	if got := e.entry(gtd.KindClose, thread, ""); got.State != gtd.Done {
		t.Fatalf("close entry %s", got.State)
	}
	if sum := e.run(); sum.Proposed != 0 || len(e.list(RuleClosure)) != 1 {
		t.Fatalf("%s", sum)
	}
}

func TestAskOwnerGroupsUnassigned(t *testing.T) {
	e := newEnv(t)
	e.ledger.put(item(thread, "e0", "Mine to answer", alice, me))
	e.sync(thread, slackMe)
	orphan := func(id, summary string, age time.Duration) items.Item {
		it := item(thread, id, summary, alice)
		it.Basis, it.CreatedAt = items.NoBasis, t0.Add(-age)
		return it
	}
	e.ledger.put(orphan("e1", "Who renews the cert?", 72*time.Hour), orphan("e2", "Who pays the invoice?", 72*time.Hour),
		orphan("e3", "Asked an hour ago", time.Hour))
	e.run()
	got := e.list(RuleAskOwner)
	if len(got) != 1 {
		t.Fatalf("%d ask-owner drafts", len(got))
	}
	d := got[0]
	if d.Kind != draft.AskOwner || d.Origin != (draft.Origin{Kind: draft.OriginAuto, Rule: RuleAskOwner, Key: thread.String()}) ||
		!slices.Equal(d.Items, []string{"e1", "e2"}) || !contains(d.Text, "Who renews the cert?", "Who pays the invoice?") ||
		d.Would.Policy != draft.WouldHold {
		t.Fatalf("%+v", d)
	}
	other := unit.Unit{Provider: "slack", Key: "C000BBB/1.1"}
	stray := orphan("x1", "No entry of mine here", 72*time.Hour)
	stray.Unit = other
	e.ledger.put(stray)
	if sum := e.run(); sum.Proposed != 0 {
		t.Fatalf("asked on a unit I have no entry on: %s", sum)
	}
}

func TestAskOwnerDiscardedWhenAssigned(t *testing.T) {
	e := newEnv(t)
	e.ledger.put(item(thread, "e0", "Mine to answer", alice, me))
	e.sync(thread, slackMe)
	a, b := item(thread, "e1", "Who renews the cert?", alice), item(thread, "e2", "Who pays the invoice?", alice)
	a.Basis, b.Basis = items.NoBasis, items.NoBasis
	e.ledger.put(a, b)
	e.run()
	first := e.list(RuleAskOwner)[0]
	a.Assignees, a.Basis = []items.Person{bob}, items.ExplicitMention
	e.ledger.put(a)
	e.run()
	got, _ := e.svc.List(ctx, outbox.Filter{Rule: RuleAskOwner})
	if old := got[len(got)-1]; old.ID != first.ID || old.State != draft.Discarded || old.Reason != ReasonAssigned {
		t.Fatalf("first draft %+v", old)
	}
	if len(got) != 2 || got[0].State != draft.Proposed || !slices.Equal(got[0].Items, []string{"e2"}) {
		t.Fatalf("the remaining item is not asked again: %+v", got)
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

func TestRuleApprovalFromConfig(t *testing.T) {
	e := newEnv(t)
	e.job.Approval = func(rule string) draft.Approval {
		if rule == RuleClosure {
			return draft.Approval{Mode: draft.Window, Window: time.Hour}
		}
		return draft.Approval{Mode: draft.Always}
	}
	e.facts[thread] = gtd.UnitFacts{StartedByMe: true}
	e.ledger.put(resolved(item(thread, "e1", "Ship it", me, alice), items.Done))
	e.sync(thread, slackMe)
	src := &githubSource{out: []outbox.Proposal{ghNudge(pull)}}
	e.job.Sources = []Source{{Provider: unit.GitHub, Proposals: src.Proposals}}
	e.run()
	c := e.list(RuleClosure)[0]
	if c.Approval != (draft.Approval{Mode: draft.Window, Window: time.Hour}) || !c.Deadline.Equal(t0.Add(time.Hour)) {
		t.Fatalf("closure %+v", c)
	}
	if g := e.list("github-nudge")[0]; g.Approval != (draft.Approval{Mode: draft.Always}) {
		t.Fatalf("unset rule %+v", g.Approval)
	}
}

func TestProposalsSkipDisabledPlugins(t *testing.T) {
	e := newEnv(t)
	stalledThread(e)
	e.run()
	open := e.list(RuleStalledNudge, draft.Proposed)
	e.disabled["slack"] = true
	e.facts[thread] = gtd.UnitFacts{StartedByMe: true}
	e.clock.set(t0.Add(72 * time.Hour))
	if sum := e.run(); sum.Proposed+sum.Refused+sum.Held != 0 {
		t.Fatalf("proposed on a disabled plugin: %s", sum)
	}
	for _, d := range open {
		if got, _ := e.drafts.Get(ctx, d.ID); got.State != draft.Proposed {
			t.Fatalf("draft %d is %s", d.ID, got.State)
		}
		if _, err := e.svc.Approve(ctx, d.ID, nil); !errors.Is(err, outbox.ErrDisabled) {
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
		if _, err := e.svc.Discard(ctx, d.ID, ""); err != nil {
			t.Fatal(err)
		}
		e.clock.set(t0.Add(23 * time.Hour))
		if sum := e.run(); sum.Proposed != 0 {
			t.Fatalf("re-proposed within renudge_after: %s", sum)
		}
		e.clock.set(t0.Add(24 * time.Hour))
		if sum := e.run(); sum.Proposed != 1 {
			t.Fatalf("after renudge_after: %s", sum)
		}
	})
	t.Run("failed", func(t *testing.T) {
		e, _ := setup(t)
		e.run()
		d := e.drafts.all()[0]
		d.State, d.Error = draft.Failed, "not posted"
		if _, err := e.drafts.Update(ctx, d, draft.Proposed); err != nil {
			t.Fatal(err)
		}
		e.clock.set(t0.Add(30 * 24 * time.Hour))
		if sum := e.run(); sum.Proposed != 0 || len(e.drafts.all()) != 1 {
			t.Fatalf("proposed beside a failed draft: %s", sum)
		}
	})
	t.Run("observed", func(t *testing.T) {
		e, _ := setup(t)
		e.job.Approval = func(string) draft.Approval { return draft.Approval{Mode: draft.Observe} }
		e.run()
		e.clock.set(t0.Add(23 * time.Hour))
		e.run()
		if all := e.drafts.all(); len(all) != 1 || all[0].Reason != "observe" {
			t.Fatalf("observed twice within renudge_after: %+v", all)
		}
		e.clock.set(t0.Add(24 * time.Hour))
		e.run()
		if all := e.drafts.all(); len(all) != 2 || all[1].Reason != "observe" {
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
	if all := e.drafts.all(); len(all) != 1 || all[0].State != draft.Proposed {
		t.Fatalf("%+v", all)
	}
}

func TestConcurrentPassesProposeOnce(t *testing.T) {
	for round := range 10 {
		e := newEnv(t)
		stalledThread(e)
		src := &githubSource{out: []outbox.Proposal{ghNudge(pull)}}
		e.job.Sources = []Source{{Provider: unit.GitHub, Proposals: src.Proposals}}
		e.drafts.beforeList = func() { time.Sleep(time.Millisecond) }
		var wg sync.WaitGroup
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, err := e.job.Run(ctx); err != nil {
					t.Error(err)
				}
			}()
		}
		wg.Wait()
		if all := e.drafts.all(); len(all) != 3 {
			t.Fatalf("round %d: %d drafts, want 3", round, len(all))
		}
	}
}

func TestStalledNudgeDiscardedWhenAnswered(t *testing.T) {
	e := newEnv(t)
	stalledThread(e)
	e.run()
	aliceEntry := e.entry(gtd.KindWaiting, thread, alice.Key)
	for _, id := range []string{"e1", "e2", "e3"} {
		its, _ := e.ledger.Items(ctx, thread)
		i := slices.IndexFunc(its, func(it items.Item) bool { return it.ID == id })
		e.ledger.put(resolved(its[i], items.Answered))
	}
	e.sync(thread, slackMe)
	if got := e.entry(gtd.KindWaiting, thread, alice.Key); got.State != gtd.Done {
		t.Fatalf("alice's entry %s", got.State)
	}
	sum := e.run()
	if sum.Discarded != 1 || sum.Proposed != 0 {
		t.Fatalf("%s", sum)
	}
	for _, d := range e.list(RuleStalledNudge) {
		switch {
		case d.Entry == string(aliceEntry.ID) && (d.State != draft.Discarded || d.Reason != ReasonMoot):
			t.Fatalf("alice's nudge %s %q", d.State, d.Reason)
		case d.Entry != string(aliceEntry.ID) && d.State != draft.Proposed:
			t.Fatalf("bob's nudge %s", d.State)
		}
	}
}

func TestClosureDiscardedWhenReopened(t *testing.T) {
	e := newEnv(t)
	e.facts[thread] = gtd.UnitFacts{StartedByMe: true}
	done := resolved(item(thread, "e1", "Ship it", me, alice), items.Done)
	e.ledger.put(done)
	e.sync(thread, slackMe)
	e.run()
	reopened := done
	reopened.State, reopened.Resolution = items.Open, ""
	e.ledger.put(reopened)
	e.sync(thread, slackMe)
	if got := e.entry(gtd.KindClose, thread, ""); got.Live() {
		t.Fatalf("close entry %s", got.State)
	}
	e.run()
	d := e.list(RuleClosure)[0]
	if d.State != draft.Discarded || d.Reason != ReasonMoot {
		t.Fatalf("%s %q", d.State, d.Reason)
	}
}

func TestSentClosureIsNeverProposedAgain(t *testing.T) {
	e := newEnv(t)
	e.facts[thread] = gtd.UnitFacts{StartedByMe: true}
	e.ledger.put(resolved(item(thread, "e1", "Ship it", me, alice), items.Done))
	e.sync(thread, slackMe)
	e.run()
	d := e.list(RuleClosure)[0]
	if _, err := e.svc.Approve(ctx, d.ID, nil); err != nil {
		t.Fatal(err)
	}
	e.svc.Send(ctx)
	e.clock.set(t0.Add(30 * 24 * time.Hour))
	if sum := e.run(); sum.Proposed != 0 || len(e.list(RuleClosure)) != 1 {
		t.Fatalf("a second closure: %s", sum)
	}
}

func TestGitHubUnitGetsNoClosureDraft(t *testing.T) {
	e := newEnv(t)
	e.job.Sources = []Source{{Provider: unit.GitHub, Proposals: (&githubSource{}).Proposals}}
	e.facts[pull] = gtd.UnitFacts{StartedByMe: true}
	e.ledger.put(resolved(item(pull, "gh:review", "Review o/r#1", ghMe, ghBob), items.Done))
	e.sync(pull, gtd.Me{Keys: []string{ghMe.Key}})
	if got := e.entry(gtd.KindClose, pull, ""); !got.Live() {
		t.Fatalf("no close entry: %+v", got)
	}
	if sum := e.run(); sum.Proposed+sum.Refused != 0 {
		t.Fatalf("%s", sum)
	}
}

func TestGitHubUnitGetsNoAskOwnerDraft(t *testing.T) {
	e := newEnv(t)
	e.job.Sources = []Source{{Provider: unit.GitHub, Proposals: (&githubSource{}).Proposals}}
	e.ledger.put(item(pull, "gh:mine", "Mine", ghBob, ghMe))
	e.sync(pull, gtd.Me{Keys: []string{ghMe.Key}})
	orphan := item(pull, "gh:o", "Whose is this?", ghBob)
	orphan.Basis = items.NoBasis
	e.ledger.put(orphan)
	if sum := e.run(); sum.Proposed+sum.Refused != 0 {
		t.Fatalf("%s", sum)
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
	e.facts[pull] = gtd.UnitFacts{Unit: pull, ClosurePosted: true}
	if sum := e.run(); sum.Discarded != 1 {
		t.Fatalf("%s", sum)
	}
	for _, d := range e.drafts.all() {
		switch {
		case d.Unit == pull && (d.State != draft.Discarded || d.Reason != ReasonMoot):
			t.Fatalf("merged PR's nudge %s %q", d.State, d.Reason)
		case d.Unit == other && d.State != draft.Proposed:
			t.Fatalf("open PR's nudge %s", d.State)
		}
	}
}
