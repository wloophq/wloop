package tasks_test

import (
	"context"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/domain/task"
)

func TestCountsWhereCountsOnlyTheKeptEntriesOfEachView(t *testing.T) {
	ctx := context.Background()
	r := newRig()
	r.put(t, task.Entry{ID: "in-a", Kind: task.KindCapture, State: task.Inbox, Title: "invoice Ana"})
	r.put(t, task.Entry{ID: "in-b", Kind: task.KindCapture, State: task.Inbox, Title: "call Bob"})
	r.put(t, task.Entry{ID: "nx-a", Kind: task.KindAction, State: task.Next, Title: "invoice OSS", Area: "OSS", Contexts: []string{"@reply"}})
	r.put(t, task.Entry{ID: "nx-b", Kind: task.KindAction, State: task.Next, Title: "review Hiring", Area: "Hiring", Contexts: []string{"@reply"}})
	r.put(t, task.Entry{ID: "dn-a", Kind: task.KindAction, State: task.Done, Title: "invoice sent"})
	r.svc.RegisterView(task.ViewOutbox, "draft:", outbox{drafts: []task.Entry{{ID: "draft:1", Kind: task.KindDraft, Title: "invoice reply"}, {ID: "draft:2", Kind: task.KindDraft, Title: "hello"}}})
	invoice := func(e task.Entry) (bool, error) { return strings.Contains(e.Title, "invoice"), nil }
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
	all, err := r.svc.CountsWhere(ctx, "Hiring", func(task.Entry) (bool, error) { return true, nil })
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
