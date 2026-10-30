package facttask_test

import (
	"slices"
	"testing"
	"time"

	facttask "github.com/wloophq/wloop/internal/adapter/facts/task"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/turn"
)

// closingWorkflows are the bundled workflows that offer to close a Slack task, by name, whose when holds for f.
func closingWorkflows(t *testing.T, f workflow.Facts) []string {
	t.Helper()
	l, err := turn.Bundled()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, w := range l.Workflows {
		if !slices.Contains([]string{"slack-done-reacted", "slack-done-replied", "slack-done-quiet"}, w.Name) {
			continue
		}
		if ok, err := w.When.Match(workflow.Vars{Facts: f, Now: monday}); err != nil {
			t.Fatalf("%s: %v", w.Name, err)
		} else if ok {
			out = append(out, w.Name)
		}
	}
	return out
}

func TestASlackTaskIsOfferedToCloseByTheSignalItsSourceReports(t *testing.T) {
	t.Parallel()
	ana := items.Person{Key: "chat:ANA"}
	cases := []struct {
		name  string
		link  facttask.Link
		ended bool
		state task.State
		cat   task.Category
		want  []string
	}{
		{"nothing reported", facttask.Link{}, false, "waiting", task.CategoryWaiting, nil},
		{"the asker checked your answer", facttask.Link{Signals: map[string]bool{"asked_reacted": true}}, false, "waiting", task.CategoryWaiting,
			[]string{"slack-done-reacted"}},
		{"you replied last", facttask.Link{Signals: map[string]bool{"replied_last": true}}, false, "next", task.CategoryReady,
			[]string{"slack-done-replied"}},
		{"the thread went quiet", facttask.Link{}, true, "waiting", task.CategoryWaiting, []string{"slack-done-quiet"}},
		{"a snoozed task", facttask.Link{Signals: map[string]bool{"asked_reacted": true}}, true, "scheduled", task.CategoryDeferred, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			at := monday.Add(-10 * 24 * time.Hour)
			th := task.Task{ID: "t-th", State: c.state, Category: c.cat, Title: "check the rollout", Origin: task.OriginDerived,
				Units: []unit.Unit{chatA}, Items: []task.ItemRef{{Unit: chatA, Item: "i1"}}, Created: at, Updated: at}
			th.SetProp(task.PropKind, task.Value{Text: "thread"}, task.WriterPlugin, at)
			e.task(th)
			e.item(chatA, "i1", items.Open, at, ana)
			e.links[chatA], e.ended[chatA] = c.link, c.ended
			if got := closingWorkflows(t, e.facts("t-th")); !slices.Equal(got, c.want) {
				t.Fatalf("offered by %v, want %v", got, c.want)
			}
		})
	}
}

func TestOnlyASlackThreadsTaskIsOfferedToClose(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	at := monday.Add(-10 * 24 * time.Hour)
	pr := task.Task{ID: "t-pr", State: "waiting", Category: task.CategoryWaiting, Title: "follow it", Origin: task.OriginDerived,
		Units: []unit.Unit{chatA}, Items: []task.ItemRef{{Unit: chatA, Item: "i1"}}, Created: at, Updated: at}
	pr.SetProp(task.PropKind, task.Value{Text: "pull-request"}, task.WriterPlugin, at)
	e.task(pr)
	e.item(chatA, "i1", items.Open, at, items.Person{Key: "chat:ANA"})
	e.links[chatA], e.ended[chatA] = facttask.Link{Signals: map[string]bool{"asked_reacted": true, "replied_last": true}}, true
	if got := closingWorkflows(t, e.facts("t-pr")); len(got) > 0 {
		t.Fatalf("a pull request's task is offered by %v", got)
	}
}
