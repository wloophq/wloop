package cli

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

var (
	twBruno  = items.Person{Key: "slack:U000BRUNO", Name: "Bruno"}
	twMe     = items.Person{Key: "slack:U000ME", Name: "Me"}
	twThread = slackThreadUnit("T000TEAM", "C000CHAN", "1700000100.000100")
)

// taskWorkflows runs the bundled workflows over a real store from a Monday morning in America/Sao_Paulo.
func taskWorkflows(t *testing.T) (*replay, *workflowProposer) {
	t.Helper()
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), "tw.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := newReplayOn(t, db, replayStart)
	p := newWorkflowProposer(r)
	r.te.tasksMe.set(task.Me{Keys: []string{twMe.Key}})
	r.te.tasks.SetMe(task.Me{Keys: []string{twMe.Key}})
	return r, p
}

func (r *replay) taskOf(id task.ID) task.Task {
	r.t.Helper()
	e, err := r.db.Tasks().Task(context.Background(), id)
	if err != nil {
		r.t.Fatal(err)
	}
	return e
}

func (r *replay) sync(units ...unit.Unit) {
	r.t.Helper()
	if err := r.te.tasks.Sync(context.Background(), units); err != nil {
		r.t.Fatal(err)
	}
}

func (r *replay) nudges(person items.Person) []draft.Draft {
	r.t.Helper()
	ds, err := r.svc.List(context.Background(), outbox.Filter{Workflow: "slack-stalled-nudge"})
	if err != nil {
		r.t.Fatal(err)
	}
	return slices.DeleteFunc(ds, func(d draft.Draft) bool { return d.Origin.Key != d.Unit.String()+":"+person.Key })
}

// mine is an open item of the thread the user asked, created at created, due by deadline, waiting on on.
func (r *replay) mine(id string, created time.Time, deadline string, on items.Person) {
	r.t.Helper()
	it := r.item(twThread, id, items.Open, created, on)
	it.Requester, it.Deadline = twMe, deadline
	r.commit(it)
}

func TestNewTasksGetAKind(t *testing.T) {
	t.Parallel()
	r, p := taskWorkflows(t)
	ctx := context.Background()
	c, err := r.te.tasks.Capture(ctx, "call the bank", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	r.item(twThread, "i1", items.Open, r.now, twMe)
	r.sync(twThread)
	if err := p.pass(ctx); err != nil {
		t.Fatal(err)
	}
	if e := r.taskOf(c.ID); e.Prop(task.PropKind).Text != "capture" || e.Written[task.PropKind].By != task.WriterWorkflow {
		t.Errorf("the capture's kind %+v by %s, want capture by the workflow", e.Prop(task.PropKind), e.Written[task.PropKind].By)
	}
	if e := r.taskOf(task.DerivedID(twThread, "i1")); e.Prop(task.PropKind).Text != "thread" || e.Written[task.PropKind].By != task.WriterPlugin {
		t.Errorf("the thread's task kind %+v by %s, want thread by the plugin", e.Prop(task.PropKind), e.Written[task.PropKind].By)
	}
}

func TestAutofocusStarsStaleAndPastDue(t *testing.T) {
	t.Parallel()
	r, p := taskWorkflows(t)
	ctx := context.Background()
	friday := replayStart.AddDate(0, 0, -3)
	pastDue := replayStart.Add(-time.Hour).Format(time.RFC3339)
	r.mine("late", friday, pastDue, twMe)
	r.mine("stale", friday.AddDate(0, 0, -4), "", twBruno)
	r.mine("fresh", r.now, "", twBruno)
	r.mine("cleared", friday, pastDue, twMe)
	r.sync(twThread)
	off := r.taskOf(task.DerivedID(twThread, "cleared"))
	off.SetProp("focus", task.Value{Text: "off"}, task.WriterPerson, r.now)
	if err := r.db.Tasks().Apply(ctx, []task.Change{{Op: task.OpUpdate, Task: off}}); err != nil {
		t.Fatal(err)
	}
	if err := p.pass(ctx); err != nil {
		t.Fatal(err)
	}
	for item, want := range map[string]string{"late": "on", "stale": "on", "fresh": "", "cleared": "off"} {
		e := r.taskOf(task.DerivedID(twThread, item))
		if got := e.Prop("focus").Text; got != want {
			t.Errorf("%s: focus %q, want %q", item, got, want)
		}
		if want == "on" && e.Written["focus"].By != task.WriterWorkflow {
			t.Errorf("%s: starred by %s", item, e.Written["focus"].By)
		}
	}
}

func TestStalledNudgeSendsOncePerPerson(t *testing.T) {
	t.Parallel()
	r, p := taskWorkflows(t)
	ctx := context.Background()
	long := replayStart.AddDate(0, 0, -7)
	for _, id := range []string{"i1", "i2"} {
		r.mine(id, long, "", twBruno)
	}
	r.sync(twThread)
	if err := p.pass(ctx); err != nil {
		t.Fatal(err)
	}
	ds := r.nudges(twBruno)
	if len(ds) != 1 || !slices.Equal(ds[0].Items, []string{"i1", "i2"}) {
		t.Fatalf("nudges to Bruno %+v, want one listing both his items", ds)
	}
	if err := approveDraft(ctx, r.svc, ds[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, failed, err := r.svc.Send(ctx); err != nil || failed > 0 {
		t.Fatalf("send: %d failed, %v", failed, err)
	}
	for _, id := range []string{"i1", "i2"} {
		if e := r.taskOf(task.DerivedID(twThread, id)); e.Prop(task.PropLastNudge).Time.IsZero() {
			t.Errorf("%s's task has no last nudge after the nudge went", id)
		}
	}
	r.now = r.now.Add(23 * time.Hour)
	if err := p.pass(ctx); err != nil {
		t.Fatal(err)
	}
	if ds := r.nudges(twBruno); len(ds) != 1 {
		t.Fatalf("within drafts.repeat_after: %d nudges", len(ds))
	}
	r.now = r.now.Add(2 * time.Hour)
	if err := p.pass(ctx); err != nil {
		t.Fatal(err)
	}
	if ds := r.nudges(twBruno); len(ds) != 2 || !slices.Equal(ds[0].Items, []string{"i1", "i2"}) && !slices.Equal(ds[1].Items, []string{"i1", "i2"}) {
		t.Fatalf("past drafts.repeat_after: nudges %+v, want a second one listing both", ds)
	}
}

func TestNudgedEffectWritesLastNudge(t *testing.T) {
	t.Parallel()
	r, _ := taskWorkflows(t)
	ctx := context.Background()
	for _, id := range []string{"i1", "i2", "i3"} {
		r.mine(id, r.now, "", twBruno)
	}
	r.sync(twThread)
	d, err := r.svc.Propose(ctx, outbox.Proposal{Unit: twThread, Task: string(task.DerivedID(twThread, "i1")), Items: []string{"i1", "i2"}, Purpose: "nudge",
		Delivery: draft.Message, Text: "any news?", Effects: []string{"task.nudged"}, Origin: draft.Origin{Kind: draft.OriginUser}})
	if err != nil {
		t.Fatal(err)
	}
	if err := approveDraft(ctx, r.svc, d.ID); err != nil {
		t.Fatal(err)
	}
	if _, failed, err := r.svc.Send(ctx); err != nil || failed > 0 {
		t.Fatalf("send: %d failed, %v", failed, err)
	}
	for id, nudged := range map[string]bool{"i1": true, "i2": true, "i3": false} {
		e := r.taskOf(task.DerivedID(twThread, id))
		got := e.Prop(task.PropLastNudge)
		if nudged != !got.Time.IsZero() || nudged && (e.Written[task.PropLastNudge].By != task.WriterWorkflow || !got.Time.Equal(r.now)) {
			t.Errorf("%s: last_nudge %v by %s, nudged %v", id, got.Time, e.Written[task.PropLastNudge].By, nudged)
		}
	}
}

func TestAutofocusClearsTheStarsItSetWhoseReasonIsGone(t *testing.T) {
	t.Parallel()
	r, p := taskWorkflows(t)
	ctx := context.Background()
	pastDue := replayStart.Add(-time.Hour).Format(time.RFC3339)
	r.mine("moved", r.now, pastDue, twMe)
	r.mine("late", r.now, pastDue, twMe)
	r.mine("mine", r.now, "", twMe)
	r.mine("theirs", r.now, "", twMe)
	r.mine("restarred", r.now, pastDue, twMe)
	r.sync(twThread)
	change := func(item string, f func(e *task.Task)) {
		e := r.taskOf(task.DerivedID(twThread, item))
		f(&e)
		if err := r.db.Tasks().Apply(ctx, []task.Change{{Op: task.OpUpdate, Task: e}}); err != nil {
			t.Fatal(err)
		}
	}
	change("mine", func(e *task.Task) {
		e.SetProp("focus", task.Value{Text: "on"}, task.WriterPerson, r.now)
	})
	change("theirs", func(e *task.Task) {
		e.SetProp("focus", task.Value{Text: "on"}, task.WriterWorkflow, r.now)
	})
	if err := p.pass(ctx); err != nil {
		t.Fatal(err)
	}
	change("restarred", func(e *task.Task) {
		e.SetProp("focus", task.Value{Text: "on"}, task.WriterPerson, r.now)
		e.SetProp("due", task.Value{Time: replayStart.AddDate(0, 0, 3)}, task.WriterPerson, r.now)
	})
	change("moved", func(e *task.Task) {
		e.SetProp("due", task.Value{Time: replayStart.AddDate(0, 0, 3)}, task.WriterPerson, r.now)
	})
	if _, err := r.db.Gates(func() time.Time { return r.now }).Open(ctx, gate.Gate{Task: task.DerivedID(twThread, "moved"), Unit: twThread, Kind: gate.KindQuestion, Name: "question", Subject: "q1"}); err != nil {
		t.Fatal(err)
	}
	starred := func() int {
		es, err := r.db.Tasks().Tasks(ctx, task.Query{})
		if err != nil {
			t.Fatal(err)
		}
		return len(slices.DeleteFunc(es, func(e task.Task) bool { return !(e.Prop("focus").Text == "on") }))
	}
	before := starred()
	r.now = r.now.Add(time.Hour)
	if err := p.pass(ctx); err != nil {
		t.Fatal(err)
	}
	if after := starred(); after >= before {
		t.Fatalf("%d stars after the run, %d before: the run did not clear the stale one", after, before)
	}
	for item, want := range map[string]string{"moved": "", "late": "on", "mine": "on", "theirs": "on", "restarred": "on"} {
		if got := r.taskOf(task.DerivedID(twThread, item)).Prop("focus").Text; got != want {
			t.Errorf("%s: focus %q, want %q", item, got, want)
		}
	}
}

func TestAStaleStarStaysWhileItsNudgeIsHeldAndGoesOnceItIsSent(t *testing.T) {
	t.Parallel()
	r, p := taskWorkflows(t)
	ctx := context.Background()
	r.mine("i1", replayStart.AddDate(0, 0, -7), "", twBruno)
	r.sync(twThread)
	id := task.DerivedID(twThread, "i1")
	focus := func() string { return r.taskOf(id).Prop("focus").Text }
	if err := p.pass(ctx); err != nil {
		t.Fatal(err)
	}
	ds := r.nudges(twBruno)
	if len(ds) != 1 || focus() != "on" {
		t.Fatalf("a stale wait: %d nudges, focus %q; want one held nudge and the star", len(ds), focus())
	}
	r.now = r.now.Add(time.Hour)
	if err := p.pass(ctx); err != nil {
		t.Fatal(err)
	}
	if focus() != "on" {
		t.Fatalf("the star went while the nudge is held")
	}
	if err := approveDraft(ctx, r.svc, ds[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, failed, err := r.svc.Send(ctx); err != nil || failed > 0 {
		t.Fatalf("send: %d failed, %v", failed, err)
	}
	r.now = r.now.Add(time.Hour)
	if err := p.pass(ctx); err != nil {
		t.Fatal(err)
	}
	if got := focus(); got != "" {
		t.Fatalf("focus %q after the nudge went, want the star off", got)
	}
}
