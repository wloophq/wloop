package task_test

import (
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/task"
)

func ids(es []task.Task) []task.ID {
	out := []task.ID{}
	for _, e := range es {
		out = append(out, e.ID)
	}
	return out
}

func at(min int) time.Time { return now.Add(time.Duration(min) * time.Minute) }

func TestAreaSparesTheInbox(t *testing.T) {
	all := []task.Task{
		{ID: "e-in-oss", State: "inbox", Category: task.CategoryInbox, Props: task.Props{"area": {Text: "OSS"}}, Created: at(1)},
		{ID: "e-in-hire", State: "inbox", Category: task.CategoryInbox, Props: task.Props{"area": {Text: "Hiring"}}, Created: at(2)},
		{ID: "e-next-oss", State: "next", Category: task.CategoryReady, Props: task.Props{"area": {Text: "OSS"}}, Created: at(3)},
		{ID: "e-next-hire", State: "next", Category: task.CategoryReady, Props: task.Props{"area": {Text: "Hiring"}}, Created: at(4)},
		{ID: "e-wait-oss", State: "waiting", Category: task.CategoryWaiting, Props: task.Props{"area": {Text: "OSS"}}, Created: at(5)},
		{ID: "e-wait-none", State: "waiting", Category: task.CategoryWaiting, Created: at(6)},
		{ID: "e-star-oss", State: "someday", Category: task.CategoryDeferred, Props: task.Props{"area": {Text: "OSS"}, "focus": {Text: "on"}}, Created: at(7)},
		{ID: "e-star-hire", State: "next", Category: task.CategoryReady, Props: task.Props{"area": {Text: "Hiring"}, "focus": {Text: "on"}}, Created: at(8)},
	}
	want := map[task.View][]task.ID{
		task.ViewInbox:   {"e-in-oss", "e-in-hire"},
		task.ViewNext:    {"e-next-oss"},
		task.ViewWaiting: {"e-wait-oss"},
		task.ViewFocus:   {"e-star-oss"},
	}
	for v, w := range want {
		if got := ids(task.Select(all, task.Query{View: v, Area: "OSS"})); !slices.Equal(got, w) {
			t.Errorf("%s: %v, want %v", v, got, w)
		}
	}
}

func TestContextsCombineWithAnd(t *testing.T) {
	all := []task.Task{
		{ID: "e-both", State: "next", Category: task.CategoryReady, Props: task.Props{"contexts": {List: []string{"@reply", "@decide"}}}},
		{ID: "e-decide", State: "next", Category: task.CategoryReady, Props: task.Props{"contexts": {List: []string{"@decide"}}}},
		{ID: "e-reply", State: "next", Category: task.CategoryReady, Props: task.Props{"contexts": {List: []string{"@reply", "@agent"}}}},
	}
	got := ids(task.Select(all, task.Query{View: task.ViewNext, Contexts: []string{"@decide", "@reply"}}))
	if !slices.Equal(got, []task.ID{"e-both"}) {
		t.Fatalf("got %v", got)
	}
}

func TestSequentialProjectShowsFirstAction(t *testing.T) {
	all := []task.Task{
		{ID: "e-seq", Kind: task.KindProject, State: "next", Category: task.CategoryReady, Sequential: true, Created: at(0)},
		{ID: "e-s3", State: "next", Category: task.CategoryReady, Parent: "e-seq", Position: 3, Created: at(1)},
		{ID: "e-s1", State: "next", Category: task.CategoryReady, Parent: "e-seq", Position: 1, Created: at(2)},
		{ID: "e-s2", State: "next", Category: task.CategoryReady, Parent: "e-seq", Position: 2, Created: at(3)},
		{ID: "e-par", Kind: task.KindProject, State: "someday", Category: task.CategoryDeferred, Created: at(4)},
		{ID: "e-p1", State: "next", Category: task.CategoryReady, Parent: "e-par", Position: 1, Created: at(5)},
		{ID: "e-p2", State: "next", Category: task.CategoryReady, Parent: "e-par", Position: 2, Created: at(6)},
	}
	next := func() []task.ID { return ids(task.Select(all, task.Query{View: task.ViewNext})) }
	if got, want := next(), []task.ID{"e-seq", "e-s1", "e-p1", "e-p2"}; !slices.Equal(got, want) {
		t.Fatalf("next %v, want %v", got, want)
	}
	all[2].State, all[2].Category = "done", task.CategoryDone
	if got, want := next(), []task.ID{"e-seq", "e-s2", "e-p1", "e-p2"}; !slices.Equal(got, want) {
		t.Fatalf("after done: %v, want %v", got, want)
	}
}

func TestViewPredicates(t *testing.T) {
	var all []task.Task
	for i, s := range bundledTable {
		var sn task.Snooze
		if s.name == "scheduled" {
			sn = task.Snooze{OnActivity: true}
		}
		st := task.State(s.name)
		all = append(all,
			task.Task{ID: task.ID("e-" + s.name), State: st, Category: s.c, Snooze: sn, Created: at(i), Updated: at(i)},
			task.Task{ID: task.ID("e-star-" + s.name), State: st, Category: s.c, Snooze: sn, Props: task.Props{"focus": {Text: "on"}}, Created: at(10 + i), Updated: at(10 + i)},
			task.Task{ID: task.ID("e-proj-" + s.name), Kind: task.KindProject, State: st, Category: s.c, Snooze: sn, Created: at(20 + i), Updated: at(20 + i)},
		)
	}
	cases := []struct {
		view task.View
		want []task.ID
	}{
		{task.ViewFocus, []task.ID{"e-star-inbox", "e-star-next", "e-star-waiting", "e-star-scheduled", "e-star-someday"}},
		{task.ViewInbox, []task.ID{"e-inbox", "e-star-inbox", "e-proj-inbox"}},
		{task.ViewNext, []task.ID{"e-next", "e-star-next", "e-proj-next"}},
		{task.ViewWaiting, []task.ID{"e-waiting", "e-star-waiting", "e-proj-waiting"}},
		{task.ViewProjects, []task.ID{"e-proj-inbox", "e-proj-next", "e-proj-waiting", "e-proj-scheduled", "e-proj-someday"}},
		{task.ViewSomeday, []task.ID{"e-someday", "e-star-someday", "e-proj-someday"}},
		{task.ViewLogbook, []task.ID{"e-proj-reference", "e-proj-done", "e-star-reference", "e-star-done", "e-reference", "e-done"}},
		{task.ViewOutbox, []task.ID{}},
	}
	for _, c := range cases {
		if got := ids(task.Select(all, task.Query{View: c.view})); !slices.Equal(got, c.want) {
			t.Errorf("%s: %v, want %v", c.view, got, c.want)
		}
	}
	counts := task.CountsOf(all, "")
	for _, c := range cases {
		if counts.Views[c.view] != len(c.want) {
			t.Errorf("count of %s: %d, want %d", c.view, counts.Views[c.view], len(c.want))
		}
	}
	if counts.Badge != 3 {
		t.Errorf("badge %d", counts.Badge)
	}
}
