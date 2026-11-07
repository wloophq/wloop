package daemon

import (
	"fmt"
	"net/http/httptest"
	"net/url"
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
)

func search(t *testing.T, r *tasksRig, q string) api.Search {
	t.Helper()
	var out api.Search
	if code := get(t, r.s, api.SearchPath+"?q="+url.QueryEscape(q), &out); code != 200 {
		t.Fatalf("search %q: %d", q, code)
	}
	return out
}

func groupCounts(s api.Search) map[string]int {
	out := map[string]int{}
	for _, g := range s.Groups {
		out[g.ID] = g.Count
	}
	return out
}

func TestSearchGroupsByWaitsOn(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	r.put(t, task.Task{ID: "t-0000000000000001", State: "next", Category: task.CategoryReady, Title: "add retries to the sender"},
		task.Task{ID: "t-0000000000000002", State: "waiting", Category: task.CategoryWaiting, Title: "retries review from Ada",
			Props: task.Props{"owner": {Actors: []items.Person{{Key: "chat:ADA", Name: "Ada"}}}}},
		task.Task{ID: "t-0000000000000003", State: "done", Category: task.CategoryDone, Title: "retries spike"},
		task.Task{ID: "t-0000000000000004", State: "next", Category: task.CategoryReady, Title: "call Brook"})
	s := search(t, r, "retries")
	if s.Query != "retries" || s.Total != 3 {
		t.Fatalf("search %+v", s)
	}
	if got := groupCounts(s); got["you"] != 1 || got["others"] != 1 || got["done"] != 1 || len(got) != 3 {
		t.Fatalf("groups %v", got)
	}
	if s.Groups[0].ID != "you" || s.Groups[0].Label != "Needs you" || s.Groups[0].Results[0].Task.ID != "t-0000000000000001" {
		t.Fatalf("first group %+v", s.Groups[0])
	}
	for _, g := range s.Groups {
		if g.Count == 0 {
			t.Errorf("group %s is empty and listed", g.ID)
		}
	}
}

func TestSearchListsViewsOfEachResult(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	r.put(t, task.Task{ID: "t-0000000000000001", State: "next", Category: task.CategoryReady, Title: "ship the retries",
		Props: task.Props{"focus": {Text: "on"}}})
	r.withGates(t, "t-0000000000000001")
	s := search(t, r, "ship")
	views := s.Groups[0].Results[0].Views
	if !slices.Contains(views, "needs-me") || !slices.Contains(views, "focus") {
		t.Fatalf("views %v, want needs-me and focus", views)
	}
	for _, v := range views {
		if spec, _ := bundledLoaded().Views.View(v); !spec.Rail {
			t.Errorf("%s is off the rail and listed", v)
		}
	}
}

func TestSearchFoldsAccents(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	r.put(t, task.Task{ID: "t-0000000000000001", State: "next", Category: task.CategoryReady, Title: "Revisão do contrato"})
	if s := search(t, r, "revisao"); s.Total != 1 {
		t.Fatalf("search %+v", s)
	}
}

func TestSearchLimitPerGroupCountsAll(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	for i := range 7 {
		r.put(t, task.Task{ID: task.ID(fmt.Sprintf("t-%016x", i+1)), State: "next", Category: task.CategoryReady, Title: fmt.Sprintf("invoice %d", i)})
		r.withGates(t, task.ID(fmt.Sprintf("t-%016x", i+1)))
	}
	s := search(t, r, "invoice")
	if g := s.Groups[0]; g.ID != "you" || g.Count != 7 || len(g.Results) != 5 {
		t.Fatalf("group %s count %d results %d", g.ID, g.Count, len(g.Results))
	}
}

func TestSearchRanksWholeWordsFirst(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	r.put(t, task.Task{ID: "t-0000000000000001", State: "next", Category: task.CategoryReady, Title: "retrying the sender"},
		task.Task{ID: "t-0000000000000002", State: "next", Category: task.CategoryReady, Title: "the retry budget"})
	r.withGates(t, "t-0000000000000001")
	r.withGates(t, "t-0000000000000002")
	s := search(t, r, "retry")
	if got := s.Groups[0].Results; len(got) != 2 || got[0].Task.ID != "t-0000000000000002" {
		t.Fatalf("results %+v, want the whole-word match first", got)
	}
}

func TestSearchEmptyQuery(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	for _, q := range []string{"", "%20%20"} {
		rec := httptest.NewRecorder()
		r.s.Handler().ServeHTTP(rec, apiRequest("GET", api.SearchPath+"?q="+q, nil))
		if rec.Code != 400 {
			t.Errorf("q %q: %d", q, rec.Code)
		}
	}
}
