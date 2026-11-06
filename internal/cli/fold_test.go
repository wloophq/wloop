package cli

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/automation/automationtest"
	"github.com/wloophq/wloop/internal/core/gates"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/daemon"
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
	return r
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
		Fields: &items.Fields{Kind: &kind, Summary: &summary}}
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
	for _, id := range []task.ID{"t-ask", "t-pr"} {
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

// asked is the rig with the pull request's task, then a Slack thread's ask whose item links it for review.
func askedRig(t *testing.T) *foldRig {
	t.Helper()
	r := newFoldRig(t)
	r.open(t, "t-pr", "Review acme/api#7", foldPR, testNow.Add(-48*time.Hour))
	r.ops(t, askOp("i1", "Review acme/api#7 before Friday"), reviewLinkOp("i1"))
	r.open(t, "t-ask", "Review acme/api#7 before Friday", foldThread, testNow.Add(-time.Hour), "i1")
	return r
}

func TestASlackItemLinkingAPullRequestWithATaskProposesItsMerge(t *testing.T) {
	t.Parallel()
	r := askedRig(t)
	r.sweep(t)
	g, ok := r.proposal(t)
	if !ok {
		t.Fatal("no merge proposal")
	}
	p, _ := coretasks.ProposedVerb(g)
	if g.Task != "t-ask" || p.Args[task.ArgTask] != "t-pr" || g.Kind != gate.KindProposal || g.Name != "merge" {
		t.Fatalf("gate %+v proposing %+v, want a merge of t-ask with t-pr", g, p)
	}
	if want := `Merge "Review acme/api#7 before Friday" into "Review acme/api#7"? Its items, units, gates and drafts move over; Split gives it back.`; g.Text != want {
		t.Errorf("the gate's words %q, want %q", g.Text, want)
	}
}

func TestAnApprovedFoldMergesTheSlackTaskIntoThePullRequestsTask(t *testing.T) {
	t.Parallel()
	r := askedRig(t)
	r.sweep(t)
	g, ok := r.proposal(t)
	if !ok {
		t.Fatal("no merge proposal")
	}
	r.resolve(t, g, gate.Approved)
	got := r.stored(t, "t-ask")
	if got.ID != "t-pr" || !slices.Contains(got.Units, foldThread) || !slices.Contains(got.Items, task.ItemRef{Unit: foldThread, Item: "i1"}) {
		t.Fatalf("t-ask reads as %+v, want t-pr holding the thread and its ask", got)
	}
	r.sweep(t)
	if _, ok := r.proposal(t); ok {
		t.Fatal("a merged pair is proposed again")
	}
}

func TestASlackTaskCreatedBeforeTheLinkIsFoldedTheSame(t *testing.T) {
	t.Parallel()
	r := newFoldRig(t)
	r.open(t, "t-pr", "Review acme/api#7", foldPR, testNow.Add(-48*time.Hour))
	r.ops(t, askOp("i1", "Have a look at the export fix"))
	r.open(t, "t-ask", "Have a look at the export fix", foldThread, testNow.Add(-time.Hour), "i1")
	r.sweep(t)
	if g, ok := r.proposal(t); ok {
		t.Fatalf("a proposal before any link: %+v", g)
	}
	r.ops(t, reviewLinkOp("i1"))
	r.sweep(t)
	g, ok := r.proposal(t)
	if !ok {
		t.Fatal("no merge proposal once the link arrived")
	}
	r.resolve(t, g, gate.Approved)
	if got := r.stored(t, "t-ask"); got.ID != "t-pr" {
		t.Fatalf("t-ask reads as %s, want t-pr", got.ID)
	}
}

func TestAFoldKeepsTheSlackTaskWhenItAloneWasClarified(t *testing.T) {
	t.Parallel()
	r := askedRig(t)
	if _, err := r.tasks.Do(context.Background(), "t-ask", coretasks.Request{Verb: task.VerbWaiting, Actor: task.ActorPerson, Source: "cockpit"}); err != nil {
		t.Fatal(err)
	}
	r.sweep(t)
	g, ok := r.proposal(t)
	if !ok {
		t.Fatal("no merge proposal")
	}
	if want := `Merge "Review acme/api#7" into "Review acme/api#7 before Friday"? Its items, units, gates and drafts move over; Split gives it back.`; g.Text != want {
		t.Errorf("the gate's words %q, want %q", g.Text, want)
	}
	r.resolve(t, g, gate.Approved)
	if got := r.stored(t, "t-pr"); got.ID != "t-ask" || !slices.Contains(got.Units, foldPR) {
		t.Fatalf("t-pr reads as %+v, want the Slack task a person moved, holding the pull request", got)
	}
}

func TestAFoldNeverMergesWithoutTheGate(t *testing.T) {
	t.Parallel()
	r := askedRig(t)
	for range 3 {
		r.sweep(t)
	}
	if _, ok := r.proposal(t); !ok {
		t.Fatal("no merge proposal")
	}
	for _, id := range []task.ID{"t-ask", "t-pr"} {
		if got := r.stored(t, id); got.ID != id {
			t.Fatalf("%s reads as %s before the gate was approved", id, got.ID)
		}
	}
	if _, err := r.tasks.Do(context.Background(), "t-ask", coretasks.Request{Verb: task.VerbMerge, Args: map[string]string{task.ArgTask: "t-pr"},
		Actor: task.ActorWorkflow, Source: "task-fold-linked"}); err != nil {
		t.Fatal(err)
	}
	if got := r.stored(t, "t-ask"); got.ID != "t-ask" {
		t.Fatalf("a workflow's merge ran without the gate: t-ask reads as %s", got.ID)
	}
	if _, ok := r.proposal(t); !ok {
		t.Fatal("the workflow's merge opened no gate")
	}
}

func TestAFoldGateEndsWhenEitherTaskCloses(t *testing.T) {
	t.Parallel()
	for _, closed := range []task.ID{"t-ask", "t-pr"} {
		t.Run(string(closed), func(t *testing.T) {
			t.Parallel()
			r := askedRig(t)
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
	r := askedRig(t)
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

func TestARefusedFoldIsNotProposedAgainUntilEitherTaskChanges(t *testing.T) {
	t.Parallel()
	r := askedRig(t)
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
	r.now = r.now.Add(time.Hour)
	summary := "Review acme/api#7 before Monday"
	r.ops(t, items.Op{Op: items.OpUpdate, Unit: foldThread, Item: "i1", Anchor: unit.Anchor{Unit: foldThread, Ref: "m:i1b"}, Fields: &items.Fields{Summary: &summary}})
	r.sweep(t)
	if _, ok := r.proposal(t); !ok {
		t.Fatal("no proposal once the ask changed")
	}
}
