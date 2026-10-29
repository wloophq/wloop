package tasks_test

import (
	"context"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/domain/task"
)

func TestCountsWhereCountsOnlyTheKeptTasksOfEachView(t *testing.T) {
	ctx := context.Background()
	r := newRig()
	r.put(t, task.Task{ID: "in-a", State: "inbox", Category: task.CategoryInbox, Title: "invoice Ana"})
	r.put(t, task.Task{ID: "in-b", State: "inbox", Category: task.CategoryInbox, Title: "call Bob"})
	r.put(t, task.Task{ID: "nx-a", State: "next", Category: task.CategoryReady, Title: "invoice OSS", Props: task.Props{"area": {Text: "OSS"}, "contexts": {List: []string{"@reply"}}}})
	r.put(t, task.Task{ID: "nx-b", State: "next", Category: task.CategoryReady, Title: "review Hiring", Props: task.Props{"area": {Text: "Hiring"}, "contexts": {List: []string{"@reply"}}}})
	r.put(t, task.Task{ID: "dn-a", State: "done", Category: task.CategoryDone, Title: "invoice sent"})
	r.svc.RegisterView(task.ViewOutbox, "draft:", outbox{drafts: []task.Task{{ID: "draft:1", Title: "invoice reply"}, {ID: "draft:2", Title: "hello"}}})
	invoice := func(e task.Task) (bool, error) { return strings.Contains(e.Title, "invoice"), nil }
	c, err := r.svc.CountsWhere(ctx, "", invoice)
	if err != nil {
		t.Fatal(err)
	}
	want := map[task.View]int{task.ViewInbox: 1, task.ViewNext: 1, task.ViewLogbook: 1, task.ViewOutbox: 1}
	for v, n := range want {
		if c.Views[v] != n {
			t.Errorf("%s %d, want %d (%+v)", v, c.Views[v], n, c.Views)
		}
	}
	if c.Areas["OSS"] != 1 || c.Areas["Hiring"] != 0 || c.Contexts["@reply"] != 1 || c.Badge != 2 {
		t.Errorf("areas %v contexts %v badge %d", c.Areas, c.Contexts, c.Badge)
	}
	all, err := r.svc.CountsWhere(ctx, "Hiring", func(task.Task) (bool, error) { return true, nil })
	if err != nil {
		t.Fatal(err)
	}
	plain, err := r.svc.Counts(ctx, "Hiring")
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range append(task.StoredViews, task.ViewOutbox) {
		if all.Views[v] != plain.Views[v] {
			t.Errorf("keeping every entry, %s counts %d, Counts %d", v, all.Views[v], plain.Views[v])
		}
	}
	if all.Contexts["@reply"] != plain.Contexts["@reply"] || all.Areas["OSS"] != plain.Areas["OSS"] {
		t.Errorf("keeping every entry: %+v, Counts %+v", all, plain)
	}
}
