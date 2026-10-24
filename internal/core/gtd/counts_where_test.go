package gtd_test

import (
	"context"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/domain/gtd"
)

func TestCountsWhereCountsOnlyTheKeptEntriesOfEachView(t *testing.T) {
	ctx := context.Background()
	r := newRig()
	r.put(t, gtd.Entry{ID: "in-a", Kind: gtd.KindCapture, State: gtd.Inbox, Title: "invoice Ana"})
	r.put(t, gtd.Entry{ID: "in-b", Kind: gtd.KindCapture, State: gtd.Inbox, Title: "call Bob"})
	r.put(t, gtd.Entry{ID: "nx-a", Kind: gtd.KindAction, State: gtd.Next, Title: "invoice OSS", Area: "OSS", Contexts: []string{"@reply"}})
	r.put(t, gtd.Entry{ID: "nx-b", Kind: gtd.KindAction, State: gtd.Next, Title: "review Hiring", Area: "Hiring", Contexts: []string{"@reply"}})
	r.put(t, gtd.Entry{ID: "dn-a", Kind: gtd.KindAction, State: gtd.Done, Title: "invoice sent"})
	r.svc.RegisterView(gtd.ViewOutbox, "draft:", outbox{drafts: []gtd.Entry{{ID: "draft:1", Kind: gtd.KindDraft, Title: "invoice reply"}, {ID: "draft:2", Kind: gtd.KindDraft, Title: "hello"}}})
	invoice := func(e gtd.Entry) (bool, error) { return strings.Contains(e.Title, "invoice"), nil }
	c, err := r.svc.CountsWhere(ctx, "", invoice)
	if err != nil {
		t.Fatal(err)
	}
	want := map[gtd.View]int{gtd.ViewInbox: 1, gtd.ViewNext: 1, gtd.ViewLogbook: 1, gtd.ViewOutbox: 1}
	for v, n := range want {
		if c.Views[v] != n {
			t.Errorf("%s %d, want %d (%+v)", v, c.Views[v], n, c.Views)
		}
	}
	if c.Areas["OSS"] != 1 || c.Areas["Hiring"] != 0 || c.Contexts["@reply"] != 1 || c.Badge != 2 {
		t.Errorf("areas %v contexts %v badge %d", c.Areas, c.Contexts, c.Badge)
	}
	all, err := r.svc.CountsWhere(ctx, "Hiring", func(gtd.Entry) (bool, error) { return true, nil })
	if err != nil {
		t.Fatal(err)
	}
	plain, err := r.svc.Counts(ctx, "Hiring")
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range append(gtd.StoredViews, gtd.ViewOutbox) {
		if all.Views[v] != plain.Views[v] {
			t.Errorf("keeping every entry, %s counts %d, Counts %d", v, all.Views[v], plain.Views[v])
		}
	}
	if all.Contexts["@reply"] != plain.Contexts["@reply"] || all.Areas["OSS"] != plain.Areas["OSS"] {
		t.Errorf("keeping every entry: %+v, Counts %+v", all, plain)
	}
}
