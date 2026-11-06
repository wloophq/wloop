package cli

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/automation/automationtest"
	"github.com/wloophq/wloop/internal/core/gates"
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
	r.te.cfg.Workflows["task-fold-linked"] = config.WorkflowConfig{Gates: &config.WorkflowGates{Approval: &config.ApprovalConfig{Rule: []config.ApprovalRuleConfig{{When: `gate.name == "merge"`, Mode: "wait"}}}}}
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
