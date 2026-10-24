package gtd_test

import (
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/gtd"
)

func ids(es []gtd.Entry) []gtd.ID {
	out := []gtd.ID{}
	for _, e := range es {
		out = append(out, e.ID)
	}
	return out
}

func at(min int) time.Time { return now.Add(time.Duration(min) * time.Minute) }

func TestAreaSparesTheInbox(t *testing.T) {
	all := []gtd.Entry{
		{ID: "e-in-oss", State: gtd.Inbox, Area: "OSS", Created: at(1)},
		{ID: "e-in-hire", State: gtd.Inbox, Area: "Hiring", Created: at(2)},
		{ID: "e-next-oss", State: gtd.Next, Area: "OSS", Created: at(3)},
		{ID: "e-next-hire", State: gtd.Next, Area: "Hiring", Created: at(4)},
		{ID: "e-wait-oss", State: gtd.Waiting, Area: "OSS", Created: at(5)},
		{ID: "e-wait-none", State: gtd.Waiting, Created: at(6)},
		{ID: "e-star-oss", State: gtd.Someday, Area: "OSS", Focus: gtd.Focus{On: true}, Created: at(7)},
		{ID: "e-star-hire", State: gtd.Next, Area: "Hiring", Focus: gtd.Focus{On: true}, Created: at(8)},
	}
	want := map[gtd.View][]gtd.ID{
		gtd.ViewInbox:   {"e-in-oss", "e-in-hire"},
		gtd.ViewNext:    {"e-next-oss"},
		gtd.ViewWaiting: {"e-wait-oss"},
		gtd.ViewFocus:   {"e-star-oss"},
	}
	for v, w := range want {
		if got := ids(gtd.Select(all, gtd.Query{View: v, Area: "OSS"})); !slices.Equal(got, w) {
			t.Errorf("%s: %v, want %v", v, got, w)
		}
	}
}

func TestContextsCombineWithAnd(t *testing.T) {
	all := []gtd.Entry{
		{ID: "e-both", State: gtd.Next, Contexts: []string{"@reply", "@decide"}},
		{ID: "e-decide", State: gtd.Next, Contexts: []string{"@decide"}},
		{ID: "e-reply", State: gtd.Next, Contexts: []string{"@reply", "@agent"}},
	}
	got := ids(gtd.Select(all, gtd.Query{View: gtd.ViewNext, Contexts: []string{"@decide", "@reply"}}))
	if !slices.Equal(got, []gtd.ID{"e-both"}) {
		t.Fatalf("got %v", got)
	}
}

func TestSequentialProjectShowsFirstAction(t *testing.T) {
	all := []gtd.Entry{
		{ID: "e-seq", Kind: gtd.KindProject, State: gtd.Next, Sequential: true, Created: at(0)},
		{ID: "e-s3", State: gtd.Next, Project: "e-seq", Position: 3, Created: at(1)},
		{ID: "e-s1", State: gtd.Next, Project: "e-seq", Position: 1, Created: at(2)},
		{ID: "e-s2", State: gtd.Next, Project: "e-seq", Position: 2, Created: at(3)},
		{ID: "e-par", Kind: gtd.KindProject, State: gtd.Someday, Created: at(4)},
		{ID: "e-p1", State: gtd.Next, Project: "e-par", Position: 1, Created: at(5)},
		{ID: "e-p2", State: gtd.Next, Project: "e-par", Position: 2, Created: at(6)},
	}
	next := func() []gtd.ID { return ids(gtd.Select(all, gtd.Query{View: gtd.ViewNext})) }
	if got, want := next(), []gtd.ID{"e-seq", "e-s1", "e-p1", "e-p2"}; !slices.Equal(got, want) {
		t.Fatalf("next %v, want %v", got, want)
	}
	all[2].State = gtd.Done
	if got, want := next(), []gtd.ID{"e-seq", "e-s2", "e-p1", "e-p2"}; !slices.Equal(got, want) {
		t.Fatalf("after done: %v, want %v", got, want)
	}
}

func TestViewPredicates(t *testing.T) {
	var all []gtd.Entry
	for i, s := range []gtd.State{gtd.Inbox, gtd.Next, gtd.Waiting, gtd.Scheduled, gtd.Someday, gtd.Reference, gtd.Done, gtd.Trash} {
		all = append(all,
			gtd.Entry{ID: gtd.ID("e-" + string(s)), State: s, Created: at(i), Updated: at(i)},
			gtd.Entry{ID: gtd.ID("e-star-" + string(s)), State: s, Focus: gtd.Focus{On: true}, Created: at(10 + i), Updated: at(10 + i)},
			gtd.Entry{ID: gtd.ID("e-proj-" + string(s)), Kind: gtd.KindProject, State: s, Created: at(20 + i), Updated: at(20 + i)},
		)
	}
	cases := []struct {
		view gtd.View
		want []gtd.ID
	}{
		{gtd.ViewFocus, []gtd.ID{"e-star-inbox", "e-star-next", "e-star-waiting", "e-star-scheduled", "e-star-someday", "e-star-reference"}},
		{gtd.ViewInbox, []gtd.ID{"e-inbox", "e-star-inbox", "e-proj-inbox"}},
		{gtd.ViewNext, []gtd.ID{"e-next", "e-star-next", "e-proj-next"}},
		{gtd.ViewWaiting, []gtd.ID{"e-waiting", "e-star-waiting", "e-proj-waiting"}},
		{gtd.ViewProjects, []gtd.ID{"e-proj-inbox", "e-proj-next", "e-proj-waiting", "e-proj-scheduled", "e-proj-someday", "e-proj-reference"}},
		{gtd.ViewSomeday, []gtd.ID{"e-someday", "e-star-someday", "e-proj-someday"}},
		{gtd.ViewLogbook, []gtd.ID{"e-proj-reference", "e-star-reference", "e-reference", "e-proj-done", "e-star-done", "e-done"}},
		{gtd.ViewOutbox, []gtd.ID{}},
	}
	for _, c := range cases {
		if got := ids(gtd.Select(all, gtd.Query{View: c.view})); !slices.Equal(got, c.want) {
			t.Errorf("%s: %v, want %v", c.view, got, c.want)
		}
	}
	counts := gtd.CountsOf(all, "")
	for _, c := range cases {
		if counts.Views[c.view] != len(c.want) {
			t.Errorf("count of %s: %d, want %d", c.view, counts.Views[c.view], len(c.want))
		}
	}
	if counts.Badge != 3 {
		t.Errorf("badge %d", counts.Badge)
	}
}
