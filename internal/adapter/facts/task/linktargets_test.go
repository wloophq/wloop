package facttask_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/link"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// linkTargets are task.link_target_tasks of id, read alone and in a pass, which must agree.
func (c *candidateEnv) linkTargets(id task.ID) []string {
	c.t.Helper()
	alone := c.list(context.Background(), id, "task.link_target_tasks")
	var inPass []string
	if err := c.p.Prepare(context.Background(), func(ctx context.Context) error {
		inPass = c.list(ctx, id, "task.link_target_tasks")
		return nil
	}); err != nil {
		c.t.Fatal(err)
	}
	if !slices.Equal(alone, inPass) {
		c.t.Fatalf("link targets of %s: %v alone, %v in a pass", id, alone, inPass)
	}
	return alone
}

func TestLinkTargetTasksNamesTheTaskOfAReviewLinksPullRequest(t *testing.T) {
	t.Parallel()
	c := newCandidateEnv(t)
	pr7 := unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}
	pr8 := unit.Unit{Provider: unit.GitHub, Key: "acme/api#8"}
	pr9 := unit.Unit{Provider: unit.GitHub, Key: "acme/api#9"}
	pr10 := unit.Unit{Provider: unit.GitHub, Key: "acme/api#10"}
	thread := chat("T1/C1/1.1")
	a1, a2 := c.ask(thread, "a1", uma, meP), c.ask(thread, "a2", uma, meP)
	c.ask(thread, "a3", davi, meP)
	c.link(thread, "a1", pr7, link.Review)
	c.link(thread, "a2", pr8, link.Merge)
	c.link(thread, "a2", pr9, link.Mention)
	c.link(thread, "a3", pr10, link.Review)
	c.open("t-ask", thread, monday, a1, a2)
	c.open("t-pr8", pr8, monday.Add(-time.Hour))
	c.open("t-pr7", pr7, monday.Add(-2*time.Hour))
	c.open("t-pr9", pr9, monday.Add(-3*time.Hour))
	c.open("t-pr10", pr10, monday.Add(-3*time.Hour))
	if got, want := c.linkTargets("t-ask"), []string{"t-pr7", "t-pr8"}; !slices.Equal(got, want) {
		t.Errorf("link targets %v, want %v: the review and merge links' tasks, oldest first, not a mention's nor one of an item another task holds", got, want)
	}
	if got := c.linkTargets("t-pr7"); len(got) != 0 {
		t.Errorf("link targets of the pull request's task %v, want none: its items link nothing", got)
	}
	done := c.open("t-pr8", pr8, monday.Add(-time.Hour))
	done.State, done.Category = "done", task.CategoryDone
	c.task(done)
	if got, want := c.linkTargets("t-ask"), []string{"t-pr7"}; !slices.Equal(got, want) {
		t.Errorf("link targets once t-pr8 is done %v, want %v", got, want)
	}
	ask, err := c.db.Tasks().Task(context.Background(), "t-ask")
	if err != nil {
		t.Fatal(err)
	}
	related, _ := bundledTasks.PropOf(task.RoleRelated)
	ask.SetProp(related, task.Value{List: []string{"t-pr7"}}, task.WriterPerson, monday)
	c.task(ask)
	if got := c.linkTargets("t-ask"); len(got) != 0 {
		t.Errorf("link targets of a task related to t-pr7 %v, want none", got)
	}
}

func TestLinkTargetTasksLeavesARefusedPairOut(t *testing.T) {
	t.Parallel()
	c := newCandidateEnv(t)
	pr := unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}
	thread := chat("T1/C1/1.1")
	a := c.ask(thread, "a1", uma, meP)
	c.link(thread, "a1", pr, link.Review)
	c.open("t-ask", thread, monday, a)
	c.open("t-pr7", pr, monday.Add(-time.Hour))
	if got, want := c.linkTargets("t-ask"), []string{"t-pr7"}; !slices.Equal(got, want) {
		t.Fatalf("link targets %v, want %v", got, want)
	}
	c.reject("t-pr7", "t-ask", task.VerbMerge)
	if got := c.linkTargets("t-ask"); len(got) != 0 {
		t.Errorf("link targets of a refused pair %v, want none", got)
	}
}

// linkTargetUnits are task.link_target_units of id, read alone and in a pass, which must agree.
func (c *candidateEnv) linkTargetUnits(id task.ID) []string {
	c.t.Helper()
	alone := c.list(context.Background(), id, "task.link_target_units")
	var inPass []string
	if err := c.p.Prepare(context.Background(), func(ctx context.Context) error {
		inPass = c.list(ctx, id, "task.link_target_units")
		return nil
	}); err != nil {
		c.t.Fatal(err)
	}
	if !slices.Equal(alone, inPass) {
		c.t.Fatalf("link target units of %s: %v alone, %v in a pass", id, alone, inPass)
	}
	return alone
}

func TestLinkTargetUnitsKeepsRelatedAndRefusedTargets(t *testing.T) {
	t.Parallel()
	c := newCandidateEnv(t)
	pr7 := unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}
	pr8 := unit.Unit{Provider: unit.GitHub, Key: "acme/api#8"}
	pr9 := unit.Unit{Provider: unit.GitHub, Key: "acme/api#9"}
	thread := chat("T1/C1/1.1")
	a := c.ask(thread, "a1", uma, meP)
	c.link(thread, "a1", pr7, link.Review)
	c.link(thread, "a1", pr8, link.Review)
	c.link(thread, "a1", pr9, link.Merge)
	c.open("t-ask", thread, monday, a)
	c.open("t-pr7", pr7, monday.Add(-time.Hour))
	c.open("t-pr8", pr8, monday.Add(-time.Hour))
	c.open("t-pr9", pr9, monday.Add(-time.Hour))
	want := []string{pr7.String(), pr8.String(), pr9.String()}
	if got := c.linkTargetUnits("t-ask"); !slices.Equal(got, want) {
		t.Fatalf("link target units %v, want %v", got, want)
	}
	ask, err := c.db.Tasks().Task(context.Background(), "t-ask")
	if err != nil {
		t.Fatal(err)
	}
	related, _ := bundledTasks.PropOf(task.RoleRelated)
	ask.SetProp(related, task.Value{List: []string{"t-pr7"}}, task.WriterWorkflow, monday)
	c.task(ask)
	c.reject("t-pr8", "t-ask", task.VerbMerge)
	if got := c.linkTargetUnits("t-ask"); !slices.Equal(got, want) {
		t.Errorf("link target units once t-pr7 is related and t-pr8 refused %v, want %v: every unit the links target", got, want)
	}
	if got, wantTasks := c.linkTargets("t-ask"), []string{"t-pr9"}; !slices.Equal(got, wantTasks) {
		t.Errorf("link targets %v, want %v", got, wantTasks)
	}
}

func TestLinkTargetUnitsCountsAPullRequestLinkedTwiceOnce(t *testing.T) {
	t.Parallel()
	c := newCandidateEnv(t)
	pr := unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}
	thread := chat("T1/C1/1.1")
	a1, a2 := c.ask(thread, "a1", uma, meP), c.ask(thread, "a2", uma, meP)
	c.link(thread, "a1", pr, link.Review)
	c.link(thread, "a2", pr, link.Merge)
	c.open("t-ask", thread, monday, a1, a2)
	if got, want := c.linkTargetUnits("t-ask"), []string{pr.String()}; !slices.Equal(got, want) {
		t.Errorf("link target units %v, want %v", got, want)
	}
}

func TestLinkTargetUnitsCountsAPullRequestWithNoTask(t *testing.T) {
	t.Parallel()
	c := newCandidateEnv(t)
	pr7 := unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}
	pr8 := unit.Unit{Provider: unit.GitHub, Key: "acme/api#8"}
	thread := chat("T1/C1/1.1")
	a := c.ask(thread, "a1", uma, meP)
	c.link(thread, "a1", pr7, link.Review)
	c.link(thread, "a1", pr8, link.Review)
	c.open("t-ask", thread, monday, a)
	c.open("t-pr7", pr7, monday.Add(-time.Hour))
	if got, want := c.linkTargetUnits("t-ask"), []string{pr7.String(), pr8.String()}; !slices.Equal(got, want) {
		t.Errorf("link target units %v, want %v: a pull request with no task counts", got, want)
	}
	if got, want := c.linkTargets("t-ask"), []string{"t-pr7"}; !slices.Equal(got, want) {
		t.Errorf("link targets %v, want %v", got, want)
	}
}

func TestUnitKindsNamesTheKindOfEachHeldUnitsPlugin(t *testing.T) {
	t.Parallel()
	c := newCandidateEnv(t)
	pr7 := unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}
	thread := chat("T1/C1/1.1")
	a := c.ask(thread, "a1", uma, meP)
	ask := c.open("t-ask", thread, monday, a)
	if got, want := c.list(context.Background(), "t-ask", "task.unit_kinds"), []string{"thread"}; !slices.Equal(got, want) {
		t.Errorf("unit kinds %v, want %v", got, want)
	}
	ask.Units = append(ask.Units, pr7, chat("T1/C2/2.2"))
	c.task(ask)
	if got, want := c.list(context.Background(), "t-ask", "task.unit_kinds"), []string{"thread", "pull-request"}; !slices.Equal(got, want) {
		t.Errorf("unit kinds of an ask that took a pull request %v, want %v: each kind once", got, want)
	}
}

func TestLinkTargetTasksLeavesARelatedTaskOutUnderItsSurvivorsID(t *testing.T) {
	t.Parallel()
	c := newCandidateEnv(t)
	pr := unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}
	thread := chat("T1/C1/1.1")
	a := c.ask(thread, "a1", uma, meP)
	c.link(thread, "a1", pr, link.Review)
	ask := c.open("t-ask", thread, monday, a)
	c.open("t-pr7", pr, monday.Add(-time.Hour))
	related, _ := bundledTasks.PropOf(task.RoleRelated)
	ask.SetProp(related, task.Value{List: []string{"t-gone"}}, task.WriterWorkflow, monday)
	c.task(ask)
	c.aliases["t-gone"] = "t-pr7"
	if got := c.linkTargets("t-ask"); len(got) != 0 {
		t.Errorf("link targets %v, want none: the related task was merged into t-pr7", got)
	}
}
