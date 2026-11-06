package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/automation/automationtest"
	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/port"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/daemon"
	"github.com/wloophq/wloop/internal/domain/card"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/link"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

// mergeProposals are the open merge gates on id and the task each proposes merging with.
func (r *profileRig) mergeProposals(t *testing.T, id task.ID) []string {
	t.Helper()
	gs, err := r.db.Gates(func() time.Time { return testNow }).OfTask(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, g := range gs {
		if p, ok := coretasks.ProposedVerb(g); ok && g.Status == gate.Open && p.Verb == task.VerbMerge {
			out = append(out, p.Args[task.ArgTask])
		}
	}
	return out
}

func TestTheTaskVerbStepPassesTheOtherTaskFromAFact(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newProfileRig(t, "")
	r.put(t, "t-ask", "next", task.CategoryReady)
	r.put(t, "t-pr", "next", task.CategoryReady)
	r.te.newTasks(r.db, r.te.cfg, nil)
	units := automationtest.NewUnits()
	u := task.UnitOf("t-ask")
	units.Put(u, workflow.Facts{"task.link_target_tasks": []string{}})
	s := newStepSet(r.te.Env, stepDeps{Units: units, Rounds: r.te.Rounds})
	st, why := wfStep("task.verb", "verb", "merge", "task", "task.link_target_tasks"), workflow.Why{Workflow: "task-fold"}
	if refusal, err := s.Check(ctx, st, u, why, automation.CheckOpts{}); err != nil || !strings.Contains(refusal, "task.link_target_tasks names no task") {
		t.Fatalf("check while the fact names none: %q %v", refusal, err)
	}
	units.Set(u, "task.link_target_tasks", []string{"t-pr", "t-other"})
	if refusal, err := s.Check(ctx, st, u, why, automation.CheckOpts{}); err != nil || refusal != "" {
		t.Fatalf("check: %q %v", refusal, err)
	}
	if _, err := s.Run(ctx, st, u, why, automation.RunOpts{}); err != nil {
		t.Fatal(err)
	}
	if got := r.mergeProposals(t, "t-ask"); len(got) != 1 || got[0] != "t-pr" {
		t.Fatalf("merge proposals %v, want one with t-pr, the fact's first", got)
	}
}

func TestTheTaskVerbStepPassesTheOtherTaskFromAClassifyOutput(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newProfileRig(t, "")
	r.put(t, "t-ask", "next", task.CategoryReady)
	r.put(t, "t-dm", "next", task.CategoryReady)
	r.te.newTasks(r.db, r.te.cfg, nil)
	units := automationtest.NewUnits()
	u := task.UnitOf("t-ask")
	units.Put(u, workflow.Facts{"classify.task-correlate.same_as": "t-dm"})
	s := newStepSet(r.te.Env, stepDeps{Units: units, Rounds: r.te.Rounds})
	st, why := wfStep("task.verb", "verb", "merge", "task", "classify.task-correlate.same_as"), workflow.Why{Workflow: "task-correlate"}
	if _, err := s.Run(ctx, st, u, why, automation.RunOpts{}); err != nil {
		t.Fatal(err)
	}
	if got := r.mergeProposals(t, "t-ask"); len(got) != 1 || got[0] != "t-dm" {
		t.Fatalf("merge proposals %v, want one with t-dm", got)
	}
	units.Set(u, "classify.task-correlate.same_as", "")
	if refusal, err := s.Check(ctx, st, u, why, automation.CheckOpts{}); err != nil || refusal == "" {
		t.Fatalf("check while the output names none: %q %v", refusal, err)
	}
}

var (
	foldThread = unit.Unit{Provider: "slack", Key: "T1/C1/1700000000.000100"}
	foldPR     = unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}
)

// foldRig is the daemon's tasks, gates and workflows over a store, with the bundled profile.
type foldRig struct {
	*profileRig
	tasks *coretasks.Service
	gates *gates.Service
	eng   *automation.Engine
	now   time.Time
	// ask is the task sync derived from the Slack thread's ask.
	ask task.ID
}

func newFoldRig(t *testing.T) *foldRig {
	t.Helper()
	r := &foldRig{profileRig: newProfileRig(t, ""), now: testNow}
	r.te.Now = func() time.Time { return r.now }
	d := r.te.serveTasks(daemon.Deps{}, r.db, r.te.cfg, nil, t.Logf)
	r.tasks = r.te.tasks
	r.gates = r.te.newGates(r.db, t.Logf)
	r.te.Env.consequences = daemon.New(d)
	ed := engineDeps{DB: r.db, SessionFacts: noUnits{}, WorkspaceFacts: noUnits{}}
	r.te.wireSubjects(&serveWiring{DB: r.db, Cfg: r.te.cfg, Engine: &ed})
	r.eng = r.te.newEngine(ed)
	if _, err := r.db.Cards().Save(context.Background(), card.Card{Unit: foldThread, Title: "#backend", State: "thread", Cares: true}, testNow); err != nil {
		t.Fatal(err)
	}
	return r
}

// holders are the tasks that hold item id of the thread, open or resolved.
func (r *foldRig) holders(t *testing.T, id string) []task.Task {
	t.Helper()
	es, err := r.db.Tasks().Tasks(context.Background(), task.Query{All: true})
	if err != nil {
		t.Fatal(err)
	}
	ref := task.ItemRef{Unit: foldThread, Item: id}
	return slices.DeleteFunc(es, func(e task.Task) bool { return !slices.Contains(e.Items, ref) && !slices.Contains(e.Resolved, ref) })
}

// derived notes in r.ask the one task sync derived holding item id.
func (r *foldRig) derived(t *testing.T, id string) {
	t.Helper()
	hs := r.holders(t, id)
	if len(hs) != 1 || hs[0].Origin != task.OriginDerived {
		t.Fatalf("the tasks holding %s: %+v, want one sync derived", id, hs)
	}
	r.ask = hs[0].ID
}

// ops applies extraction ops to the items ledger, as the Slack extraction does.
func (r *foldRig) ops(t *testing.T, ops ...items.Op) {
	t.Helper()
	res, err := r.te.itemsLedger(r.db, r.te.cfg, t.Logf).Apply(context.Background(), items.Extracted, ops)
	if err != nil || len(res.Refused) > 0 {
		t.Fatalf("ops: %+v %v", res.Refused, err)
	}
	if err := r.tasks.Sync(context.Background(), []unit.Unit{foldThread}); err != nil {
		t.Fatal(err)
	}
}

func askOp(id, summary string) items.Op {
	kind := items.Request
	return items.Op{Op: items.OpCreate, Unit: foldThread, Item: id, Anchor: unit.Anchor{Unit: foldThread, Ref: "m:" + id},
		Fields: &items.Fields{Kind: &kind, Summary: &summary, Requester: &items.Person{Key: "slack:U0001", Name: "Uma Um"},
			WaitingOn: &[]items.Person{{Key: "slack:U0002", Name: "Davi Dois"}}}}
}

func reviewLinkOp(id string) items.Op {
	pr := foldPR
	return items.Op{Op: items.OpLink, Unit: foldThread, Item: id, Anchor: unit.Anchor{Unit: foldThread, Ref: "m:" + id}, To: &pr, LinkKind: link.Review}
}

// open writes an open derived task holding u's items refs, created at.
func (r *foldRig) open(t *testing.T, id task.ID, title string, u unit.Unit, at time.Time, refs ...string) {
	t.Helper()
	e := task.Task{ID: id, State: "next", Category: task.CategoryReady, Title: title, Origin: task.OriginUser,
		Units: []unit.Unit{u}, Created: at, Updated: at}
	for _, ref := range refs {
		e.Items = append(e.Items, task.ItemRef{Unit: u, Item: ref})
	}
	if err := r.db.Tasks().Apply(context.Background(), []task.Change{{Op: task.OpCreate, Task: e}}); err != nil {
		t.Fatal(err)
	}
}

func (r *foldRig) sweep(t *testing.T) {
	t.Helper()
	for range 2 {
		if _, _, err := r.eng.Sweep(context.Background(), &sync.Mutex{}); err != nil {
			t.Fatal(err)
		}
	}
}

// proposal is the one open merge gate of the fold, on either task.
func (r *foldRig) proposal(t *testing.T) (gate.Gate, bool) {
	t.Helper()
	var out []gate.Gate
	for _, id := range []task.ID{r.ask, "t-pr"} {
		gs, err := r.db.Gates(func() time.Time { return testNow }).OfTask(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		for _, g := range gs {
			if p, ok := coretasks.ProposedVerb(g); ok && g.Status == gate.Open && p.Verb == task.VerbMerge {
				out = append(out, g)
			}
		}
	}
	if len(out) > 1 {
		t.Fatalf("open merge gates %+v, want at most one", out)
	}
	if len(out) == 0 {
		return gate.Gate{}, false
	}
	return out[0], true
}

func (r *foldRig) resolve(t *testing.T, g gate.Gate, s gate.Status) {
	t.Helper()
	intent := "approve"
	if s == gate.Rejected {
		intent = "reject"
	}
	if err := r.gates.Resolve(context.Background(), g.ID, gates.Resolution{Status: s, Intent: intent, By: task.ActorPerson}); err != nil {
		t.Fatal(err)
	}
}

func (r *foldRig) stored(t *testing.T, id task.ID) task.Task {
	t.Helper()
	e, err := r.db.Tasks().Task(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// released is the one merge gate of the fold a rule approved, on either task.
func (r *foldRig) released(t *testing.T) (gate.Gate, bool) {
	t.Helper()
	var out []gate.Gate
	for _, id := range []task.ID{r.ask, "t-pr"} {
		gs, err := r.db.Gates(func() time.Time { return testNow }).OfTask(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		for _, g := range gs {
			if p, ok := coretasks.ProposedVerb(g); ok && g.Status == gate.Approved && g.Rule != "" && p.Verb == task.VerbMerge {
				out = append(out, g)
			}
		}
	}
	if len(out) == 0 {
		return gate.Gate{}, false
	}
	return out[len(out)-1], true
}

// asking configures the fold's merge gates to wait for you, as a user who replaced its approval's rules does.
func (r *foldRig) asking() {
	if r.te.cfg.Workflows == nil {
		r.te.cfg.Workflows = map[string]config.WorkflowConfig{}
	}
	r.te.cfg.Workflows["task-fold-linked"] = config.WorkflowConfig{Gates: &config.WorkflowGates{Approval: &config.ApprovalConfig{Rule: []config.ApprovalRuleConfig{{When: `gate.kind == "proposal" && gate.name == "merge"`, Mode: "wait"}}}}}
}

// split splits r.ask back out of t-pr, as a person does.
func (r *foldRig) split(t *testing.T) {
	t.Helper()
	if _, err := r.tasks.Do(context.Background(), "t-pr", coretasks.Request{Verb: task.VerbSplit, Args: map[string]string{task.ArgTask: string(r.ask)},
		Actor: task.ActorPerson, Source: "cockpit"}); err != nil {
		t.Fatal(err)
	}
	if got := r.stored(t, r.ask); got.ID != r.ask {
		t.Fatalf("the Slack task reads as %s after the split", got.ID)
	}
}

// touch changes the Slack ask an hour later, as a new message in the thread does.
func (r *foldRig) touch(t *testing.T, summary string) {
	t.Helper()
	r.now = r.now.Add(time.Hour)
	r.ops(t, items.Op{Op: items.OpUpdate, Unit: foldThread, Item: "i1", Anchor: unit.Anchor{Unit: foldThread, Ref: "m:i1" + summary[len(summary)-1:]}, Fields: &items.Fields{Summary: &summary}})
}

// asked is the rig with the pull request's task, then a Slack thread's ask whose item links it for review;
// asking makes the fold wait for you.
func askedRig(t *testing.T, asking bool) *foldRig {
	t.Helper()
	r := newFoldRig(t)
	if asking {
		r.asking()
	}
	r.open(t, "t-pr", "Review acme/api#7", foldPR, testNow.Add(-48*time.Hour))
	r.ops(t, askOp("i1", "Review acme/api#7 before Friday"), reviewLinkOp("i1"))
	r.derived(t, "i1")
	return r
}

func TestASlackItemLinkingAPullRequestWithATaskIsMergedIntoIt(t *testing.T) {
	t.Parallel()
	r := askedRig(t, false)
	r.sweep(t)
	if got := r.stored(t, r.ask); got.ID != "t-pr" || !slices.Contains(got.Units, foldThread) || !slices.Contains(got.Items, task.ItemRef{Unit: foldThread, Item: "i1"}) {
		t.Fatalf("the Slack task reads as %+v, want t-pr holding the thread and its ask", got)
	}
	g, ok := r.released(t)
	if !ok {
		t.Fatal("no merge gate a rule approved")
	}
	if g.By != task.ActorWorkflow || g.Rule != "workflows/task-fold-linked gates.approval rule 0" || g.Name != "merge" || g.Kind != gate.KindProposal {
		t.Fatalf("the merge gate %+v, want the fold's rule to approve it", g)
	}
	if want := `Merge "Review acme/api#7 before Friday" into "Review acme/api#7"? Its items, units, gates and drafts move over; Split gives it back.`; g.Text != want {
		t.Errorf("the gate's words %q, want %q", g.Text, want)
	}
	evs, err := r.db.EventsOfKinds(context.Background(), []string{task.VerbEventKind(task.VerbMerge)})
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) == 0 || !slices.ContainsFunc(evs, func(ev workflow.Event) bool { return ev.Actor == "workflow:task-fold-linked" }) {
		t.Fatalf("the merge's events %+v, want them by the workflow task-fold-linked", evs)
	}
	r.touch(t, "Review acme/api#7 before Monday")
	if hs := r.holders(t, "i1"); len(hs) != 1 || hs[0].ID != "t-pr" {
		t.Fatalf("the tasks holding i1 after a later sync: %+v, want only t-pr", hs)
	}
	r.sweep(t)
	if g, ok := r.proposal(t); ok {
		t.Fatalf("a merged pair is proposed again: %+v", g)
	}
}

func TestAFoldThatWaitsForYouMergesOnceApproved(t *testing.T) {
	t.Parallel()
	r := askedRig(t, true)
	for range 3 {
		r.sweep(t)
	}
	g, ok := r.proposal(t)
	if !ok {
		t.Fatal("no merge proposal")
	}
	p, _ := coretasks.ProposedVerb(g)
	if g.Task != r.ask || p.Args[task.ArgTask] != "t-pr" || coretasks.ProposalFrom(g) != "task-fold-linked" {
		t.Fatalf("gate %+v proposing %+v, want a merge of the ask with t-pr from task-fold-linked", g, p)
	}
	for _, id := range []task.ID{r.ask, "t-pr"} {
		if got := r.stored(t, id); got.ID != id {
			t.Fatalf("%s reads as %s before the gate was approved", id, got.ID)
		}
	}
	r.resolve(t, g, gate.Approved)
	if got := r.stored(t, r.ask); got.ID != "t-pr" {
		t.Fatalf("the Slack task reads as %s once approved, want t-pr", got.ID)
	}
}

func TestASlackTaskCreatedBeforeTheLinkIsFoldedTheSame(t *testing.T) {
	t.Parallel()
	r := newFoldRig(t)
	r.open(t, "t-pr", "Review acme/api#7", foldPR, testNow.Add(-48*time.Hour))
	r.ops(t, askOp("i1", "Have a look at the export fix"))
	r.derived(t, "i1")
	r.sweep(t)
	if got := r.stored(t, r.ask); got.ID != r.ask {
		t.Fatalf("a merge before any link: %s", got.ID)
	}
	r.ops(t, reviewLinkOp("i1"))
	r.sweep(t)
	if got := r.stored(t, r.ask); got.ID != "t-pr" {
		t.Fatalf("t-ask reads as %s once the link arrived, want t-pr", got.ID)
	}
}

func TestAFoldKeepsTheSlackTaskWhenItAloneWasClarified(t *testing.T) {
	t.Parallel()
	r := askedRig(t, false)
	if _, err := r.tasks.Do(context.Background(), r.ask, coretasks.Request{Verb: task.VerbWaiting, Actor: task.ActorPerson, Source: "cockpit"}); err != nil {
		t.Fatal(err)
	}
	r.sweep(t)
	if got := r.stored(t, "t-pr"); got.ID != r.ask || !slices.Contains(got.Units, foldPR) {
		t.Fatalf("t-pr reads as %+v, want the Slack task a person moved, holding the pull request", got)
	}
}

func TestAFoldGateEndsWhenEitherTaskCloses(t *testing.T) {
	t.Parallel()
	for _, ask := range []bool{true, false} {
		t.Run(fmt.Sprintf("the Slack task %v", ask), func(t *testing.T) {
			t.Parallel()
			r := askedRig(t, true)
			closed := task.ID("t-pr")
			if ask {
				closed = r.ask
			}
			r.sweep(t)
			g, ok := r.proposal(t)
			if !ok {
				t.Fatal("no merge proposal")
			}
			if _, err := r.tasks.Do(context.Background(), closed, coretasks.Request{Verb: task.VerbDismiss, Actor: task.ActorPerson, Source: "cockpit"}); err != nil {
				t.Fatal(err)
			}
			got, err := r.db.Gates(func() time.Time { return testNow }).Get(context.Background(), g.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != gate.Superseded {
				t.Fatalf("the merge gate once %s closed: %+v, want superseded", closed, got)
			}
		})
	}
}

func TestAFoldGateEndsWhenEitherTaskIsMergedElsewhere(t *testing.T) {
	t.Parallel()
	r := askedRig(t, true)
	r.open(t, "t-other", "Ship the export", unit.Unit{Provider: unit.GitHub, Key: "acme/api#9"}, testNow.Add(-72*time.Hour))
	r.sweep(t)
	g, ok := r.proposal(t)
	if !ok {
		t.Fatal("no merge proposal")
	}
	if _, err := r.tasks.Do(context.Background(), "t-other", coretasks.Request{Verb: task.VerbMerge, Args: map[string]string{task.ArgTask: "t-pr"},
		Actor: task.ActorPerson, Source: "cockpit"}); err != nil {
		t.Fatal(err)
	}
	got, err := r.db.Gates(func() time.Time { return testNow }).Get(context.Background(), g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != gate.Superseded {
		t.Fatalf("the fold's gate once t-pr merged into t-other: %+v, want superseded", got)
	}
}

func TestARefusedFoldIsProposedAgainOnceEitherTaskChangesAndWaitsForYou(t *testing.T) {
	t.Parallel()
	r := askedRig(t, true)
	r.sweep(t)
	g, ok := r.proposal(t)
	if !ok {
		t.Fatal("no merge proposal")
	}
	r.resolve(t, g, gate.Rejected)
	r.sweep(t)
	if again, ok := r.proposal(t); ok {
		t.Fatalf("a refused fold proposed again: %+v", again)
	}
	delete(r.te.cfg.Workflows, "task-fold-linked")
	r.touch(t, "Review acme/api#7 before Monday")
	r.sweep(t)
	if _, ok := r.proposal(t); !ok {
		t.Fatal("no proposal once the ask changed")
	}
	if got := r.stored(t, r.ask); got.ID != r.ask {
		t.Fatalf("a fold you refused ran on its own: the ask reads as %s", got.ID)
	}
}

func TestASplitFoldWaitsForYouEverAfter(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := askedRig(t, false)
	r.sweep(t)
	if got := r.stored(t, r.ask); got.ID != "t-pr" {
		t.Fatalf("the ask reads as %s, want it merged into t-pr", got.ID)
	}
	r.now = r.now.Add(time.Hour)
	r.split(t)
	if _, err := r.tasks.Do(ctx, r.ask, coretasks.Request{Verb: task.VerbWaiting, Actor: task.ActorPerson, Source: "cockpit"}); err != nil {
		t.Fatal(err)
	}
	r.sweep(t)
	if again, ok := r.proposal(t); ok {
		t.Fatalf("a pair the user split proposed again before it changed: %+v", again)
	}
	for _, summary := range []string{"Review acme/api#7 before Monday", "Review acme/api#7 before Tuesday"} {
		r.touch(t, summary)
		r.sweep(t)
		if got := r.stored(t, r.ask); got.ID != r.ask {
			t.Fatalf("a pair you split was merged again on its own once the ask changed: it reads as %s", got.ID)
		}
		if _, ok := r.proposal(t); !ok {
			t.Fatal("no proposal for you once the ask changed after the split")
		}
	}
	g, _ := r.proposal(t)
	r.resolve(t, g, gate.Approved)
	if got := r.stored(t, "t-pr"); got.ID != r.ask {
		t.Fatalf("t-pr reads as %s once you approved, want the ask you moved", got.ID)
	}
}

func TestASplitFoldWaitsForYouAfterThePullRequestsTaskIsMergedAway(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := askedRig(t, false)
	r.open(t, "t-x", "Ship the api", unit.Unit{Provider: unit.GitHub, Key: "acme/api#70"}, testNow.Add(-96*time.Hour))
	r.sweep(t)
	if got := r.stored(t, r.ask); got.ID != "t-pr" {
		t.Fatalf("the ask reads as %s, want it merged into t-pr", got.ID)
	}
	r.now = r.now.Add(time.Hour)
	r.split(t)
	if _, err := r.tasks.Do(ctx, "t-x", coretasks.Request{Verb: task.VerbMerge, Args: map[string]string{task.ArgTask: "t-pr"}, Actor: task.ActorPerson, Source: "cockpit"}); err != nil {
		t.Fatal(err)
	}
	if got := r.stored(t, "t-pr"); got.ID != "t-x" {
		t.Fatalf("t-pr reads as %s, want it merged into t-x", got.ID)
	}
	r.touch(t, "Review acme/api#7 before Monday")
	r.sweep(t)
	if got := r.stored(t, r.ask); got.ID != r.ask {
		t.Fatalf("the ask you split from t-pr was merged on its own into %s, which holds t-pr now", got.ID)
	}
	open := 0
	for _, id := range []task.ID{r.ask, "t-x"} {
		gs, err := r.db.Gates(func() time.Time { return testNow }).OfTask(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		for _, g := range gs {
			if p, ok := coretasks.ProposedVerb(g); ok && p.Verb == task.VerbMerge && g.Status == gate.Open {
				open++
			}
		}
	}
	if open != 1 {
		t.Fatalf("%d open merge gates for the ask and t-x, want the one that waits for you", open)
	}
}

var (
	foldPR8 = unit.Unit{Provider: unit.GitHub, Key: "acme/api#8"}
	foldPR9 = unit.Unit{Provider: unit.GitHub, Key: "acme/api#9"}
)

// linkOpTo links the thread's item id to pr for review.
func linkOpTo(id string, pr unit.Unit) items.Op {
	return items.Op{Op: items.OpLink, Unit: foldThread, Item: id, Anchor: unit.Anchor{Unit: foldThread, Ref: "m:" + id}, To: &pr, LinkKind: link.Review}
}

// settled sweeps until a sweep changes no task, at most n times.
func (r *foldRig) settled(t *testing.T, n int) {
	t.Helper()
	for range n {
		r.sweep(t)
	}
}

// relatedOf are the tasks id's related property lists.
func (r *foldRig) relatedOf(t *testing.T, id task.ID) []string {
	t.Helper()
	name, _ := r.te.loadedIntents().Tasks.PropOf(task.RoleRelated)
	return r.stored(t, id).Prop(name).List
}

// apart says each of ids still reads as itself.
func (r *foldRig) apart(t *testing.T, ids ...task.ID) {
	t.Helper()
	for _, id := range ids {
		if got := r.stored(t, id); got.ID != id {
			t.Errorf("%s reads as %s, want it apart", id, got.ID)
		}
	}
}

func TestAnAskLinkingSeveralPullRequestsIsRelatedToEachAndMergedIntoNone(t *testing.T) {
	t.Parallel()
	r := newFoldRig(t)
	r.open(t, "t-pr", "Review acme/api#7", foldPR, testNow.Add(-48*time.Hour))
	r.open(t, "t-pr8", "Review acme/api#8", foldPR8, testNow.Add(-47*time.Hour))
	r.open(t, "t-pr9", "Review acme/api#9", foldPR9, testNow.Add(-46*time.Hour))
	r.ops(t, askOp("i1", "Review three PRs: acme/api#7, #8 and #9"), linkOpTo("i1", foldPR), linkOpTo("i1", foldPR8), linkOpTo("i1", foldPR9))
	r.derived(t, "i1")
	r.settled(t, 4)
	r.apart(t, r.ask, "t-pr", "t-pr8", "t-pr9")
	got := r.relatedOf(t, r.ask)
	for _, id := range []string{"t-pr", "t-pr8", "t-pr9"} {
		if !slices.Contains(got, id) {
			t.Errorf("the ask relates %v, want %s among them", got, id)
		}
	}
}

func TestRelatingSomeOfAnAsksPullRequestsNeverMergesTheRest(t *testing.T) {
	t.Parallel()
	r := newFoldRig(t)
	r.open(t, "t-pr", "Review acme/api#7", foldPR, testNow.Add(-48*time.Hour))
	r.open(t, "t-pr8", "Review acme/api#8", foldPR8, testNow.Add(-47*time.Hour))
	r.ops(t, askOp("i1", "Review acme/api#7 and #8"), linkOpTo("i1", foldPR), linkOpTo("i1", foldPR8))
	r.derived(t, "i1")
	if _, err := r.tasks.Do(context.Background(), r.ask, coretasks.Request{Verb: task.VerbRelate, Args: map[string]string{task.ArgTask: "t-pr"},
		Actor: task.ActorPerson, Source: "cockpit"}); err != nil {
		t.Fatal(err)
	}
	r.settled(t, 3)
	r.apart(t, r.ask, "t-pr", "t-pr8")
	if got := r.relatedOf(t, r.ask); !slices.Contains(got, "t-pr8") {
		t.Errorf("the ask relates %v, want t-pr8 among them", got)
	}
}

func TestAnAskLinkingAPullRequestWithATaskAndOneWithoutIsNotMerged(t *testing.T) {
	t.Parallel()
	r := newFoldRig(t)
	r.open(t, "t-pr", "Review acme/api#7", foldPR, testNow.Add(-48*time.Hour))
	r.ops(t, askOp("i1", "Review acme/api#7 and #8"), linkOpTo("i1", foldPR), linkOpTo("i1", foldPR8))
	r.derived(t, "i1")
	r.settled(t, 3)
	r.apart(t, r.ask, "t-pr")
	if got := r.relatedOf(t, r.ask); !slices.Contains(got, "t-pr") {
		t.Errorf("the ask relates %v, want t-pr", got)
	}
}

func TestASurvivingAskHoldingAPullRequestDoesNotFoldAnotherItsItemLinks(t *testing.T) {
	t.Parallel()
	r := askedRig(t, false)
	if _, err := r.tasks.Do(context.Background(), r.ask, coretasks.Request{Verb: task.VerbWaiting, Actor: task.ActorPerson, Source: "cockpit"}); err != nil {
		t.Fatal(err)
	}
	r.sweep(t)
	if got := r.stored(t, "t-pr"); got.ID != r.ask {
		t.Fatalf("t-pr reads as %s, want the ask you moved", got.ID)
	}
	r.open(t, "t-pr8", "Review acme/api#8", foldPR8, testNow.Add(-47*time.Hour))
	r.ops(t, linkOpTo("i1", foldPR8))
	r.settled(t, 3)
	r.apart(t, r.ask, "t-pr8")
	if got := r.relatedOf(t, r.ask); !slices.Contains(got, "t-pr8") {
		t.Errorf("the surviving ask relates %v, want t-pr8", got)
	}
}

func TestASecondAskAboutTheSamePullRequestIsMergedIntoItsTask(t *testing.T) {
	t.Parallel()
	r := askedRig(t, false)
	r.sweep(t)
	if got := r.stored(t, r.ask); got.ID != "t-pr" {
		t.Fatalf("the first ask reads as %s, want it merged into t-pr", got.ID)
	}
	other := unit.Unit{Provider: "slack", Key: "T1/C2/1700000000.000200"}
	if _, err := r.db.Cards().Save(context.Background(), card.Card{Unit: other, Title: "#frontend", State: "thread", Cares: true}, testNow); err != nil {
		t.Fatal(err)
	}
	kind, summary := items.Request, "Merge acme/api#7 once green"
	pr := foldPR
	ops := []items.Op{
		{Op: items.OpCreate, Unit: other, Item: "j1", Anchor: unit.Anchor{Unit: other, Ref: "m:j1"},
			Fields: &items.Fields{Kind: &kind, Summary: &summary, Requester: &items.Person{Key: "slack:U0003", Name: "Tito Tres"},
				WaitingOn: &[]items.Person{{Key: "slack:U0002", Name: "Davi Dois"}}}},
		{Op: items.OpLink, Unit: other, Item: "j1", Anchor: unit.Anchor{Unit: other, Ref: "m:j1"}, To: &pr, LinkKind: link.Merge},
	}
	if res, err := r.te.itemsLedger(r.db, r.te.cfg, t.Logf).Apply(context.Background(), items.Extracted, ops); err != nil || len(res.Refused) > 0 {
		t.Fatalf("ops: %+v %v", res.Refused, err)
	}
	if err := r.tasks.Sync(context.Background(), []unit.Unit{other}); err != nil {
		t.Fatal(err)
	}
	r.settled(t, 2)
	if got := r.stored(t, "t-pr"); !slices.Contains(got.Units, other) || !slices.Contains(got.Items, task.ItemRef{Unit: other, Item: "j1"}) {
		t.Fatalf("t-pr reads as %+v, want it holding the second ask", got)
	}
}

// windowed makes the fold's merge gates wait an hour, then merge, as a user who chose a window does.
func (r *foldRig) windowed() {
	if r.te.cfg.Workflows == nil {
		r.te.cfg.Workflows = map[string]config.WorkflowConfig{}
	}
	r.te.cfg.Workflows["task-fold-linked"] = config.WorkflowConfig{Gates: &config.WorkflowGates{Approval: &config.ApprovalConfig{Rule: []config.ApprovalRuleConfig{{When: `gate.kind == "proposal" && gate.name == "merge"`, Mode: "delay", Window: config.Days(time.Hour)}}}}}
}

// windowedRig is askedRig whose fold's merge gate waits its window, open.
func windowedRig(t *testing.T) (*foldRig, gate.Gate) {
	t.Helper()
	r := newFoldRig(t)
	r.windowed()
	r.open(t, "t-pr", "Review acme/api#7", foldPR, testNow.Add(-48*time.Hour))
	r.ops(t, askOp("i1", "Review acme/api#7 before Friday"), reviewLinkOp("i1"))
	r.derived(t, "i1")
	r.sweep(t)
	g, ok := r.proposal(t)
	if !ok || g.Deadline.IsZero() {
		t.Fatalf("the fold's merge gate %+v, want one waiting its window", g)
	}
	return r, g
}

// passWindow ticks the gates two hours on, past the window.
func (r *foldRig) passWindow(t *testing.T) {
	t.Helper()
	r.now = r.now.Add(2 * time.Hour)
	if err := r.gates.Tick(context.Background(), r.now); err != nil {
		t.Fatal(err)
	}
}

func TestAWindowedFoldWhosePairYouUnrelatedRunsNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r, g := windowedRig(t)
	for _, v := range []task.Verb{task.VerbRelate, task.VerbUnrelate} {
		if _, err := r.tasks.Do(ctx, r.ask, coretasks.Request{Verb: v, Args: map[string]string{task.ArgTask: "t-pr"}, Actor: task.ActorPerson, Source: "cockpit"}); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := r.db.Gates(func() time.Time { return testNow }).Get(ctx, g.ID); err != nil || got.Status != gate.Superseded {
		t.Fatalf("the fold's merge gate once you unrelated the pair: %+v %v, want it superseded", got, err)
	}
	r.passWindow(t)
	r.apart(t, r.ask, "t-pr")
}

func TestRejectingARelationOfAPairSupersedesItsMerge(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r, merge := windowedRig(t)
	r.te.cfg.Workflows["task-correlate-relate"] = config.WorkflowConfig{Gates: &config.WorkflowGates{Approval: &config.ApprovalConfig{Rule: []config.ApprovalRuleConfig{{When: `gate.kind == "proposal"`, Mode: "wait"}}}}}
	res, err := r.tasks.Do(ctx, r.ask, coretasks.Request{Verb: task.VerbRelate, Args: map[string]string{task.ArgTask: "t-pr"}, Actor: task.ActorWorkflow, Source: "task-correlate-relate"})
	if err != nil {
		t.Fatal(err)
	}
	gs, err := r.db.Gates(func() time.Time { return testNow }).OfTask(ctx, res.Task.ID)
	if err != nil {
		t.Fatal(err)
	}
	var relate gate.Gate
	for _, g := range gs {
		if p, ok := coretasks.ProposedVerb(g); ok && p.Verb == task.VerbRelate && g.Status == gate.Open {
			relate = g
		}
	}
	if relate.ID == 0 {
		t.Fatalf("no relate gate waits for you: %+v", gs)
	}
	r.resolve(t, relate, gate.Rejected)
	if got, err := r.db.Gates(func() time.Time { return testNow }).Get(ctx, merge.ID); err != nil || got.Status != gate.Superseded {
		t.Fatalf("the pair's merge gate once you rejected its relation: %+v %v, want it superseded", got, err)
	}
	r.passWindow(t)
	r.apart(t, r.ask, "t-pr")
}

func TestAWindowedFoldOfAPairTakenApartUnseenRunsNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r, g := windowedRig(t)
	at := r.now.Add(time.Minute)
	if err := sqlite.New(r.db).RecordEvents(ctx, port.Event{Unit: task.UnitOf(r.ask), Kind: task.VerbEventKind(task.VerbUnrelate),
		Key: task.PairKey("t-pr", at), Actor: "person:cockpit", At: at}); err != nil {
		t.Fatal(err)
	}
	r.now = r.now.Add(2 * time.Hour)
	if err := r.gates.Tick(ctx, r.now); err == nil || !strings.Contains(err.Error(), "a pair you took apart") {
		t.Fatalf("the window's consequence: %v, want the merge refused as a pair you took apart", err)
	}
	r.apart(t, r.ask, "t-pr")
	if got, err := r.db.Gates(func() time.Time { return testNow }).Get(ctx, g.ID); err != nil || got.Status != gate.Approved || got.Rule != gates.RuleWindow {
		t.Fatalf("the merge gate %+v %v, want its window passed", got, err)
	}
}

func TestAPairTakenApartWaitsForYouWhenTheAskingWorkflowIsOff(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := askedRig(t, false)
	if err := os.WriteFile(filepath.Join(r.dir, "gates.toml"), []byte("[[approval.rule]]\nwhen = 'gate.kind == \"proposal\"'\nmode = \"run\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.write(t, "")
	off := false
	r.te.cfg.Workflows = map[string]config.WorkflowConfig{"task-fold-linked": {Enabled: &off}}
	at := r.now.Add(-time.Minute)
	if err := sqlite.New(r.db).RecordEvents(ctx, port.Event{Unit: task.UnitOf(r.ask), Kind: task.VerbEventKind(task.VerbUnrelate),
		Key: task.PairKey("t-pr", at), Actor: "person:cockpit", At: at}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.tasks.Do(ctx, r.ask, coretasks.Request{Verb: task.VerbMerge, Args: map[string]string{task.ArgTask: "t-pr"}, Actor: task.ActorWorkflow, Source: "task-fold-linked"}); err != nil {
		t.Fatal(err)
	}
	r.apart(t, r.ask, "t-pr")
	if _, ok := r.proposal(t); !ok {
		t.Fatal("no merge gate waits for you on a pair you took apart")
	}
}
