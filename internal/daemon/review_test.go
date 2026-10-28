package daemon

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	dintent "github.com/wloophq/wloop/internal/domain/intent"

	"github.com/wloophq/wloop/internal/api"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/task"
	intentload "github.com/wloophq/wloop/internal/intent"
)

func (r *entriesRig) serveProjects() {
	r.g.ServeProjects(coretasks.ProjectDeps{Store: r.db.Projects()})
}

func TestReviewViewIsServedOnceRegistered(t *testing.T) {
	t.Parallel()
	r := newEntriesRig(t, nil)
	var cat api.GTDCatalog
	get(t, r.s, api.GTDPath, &cat)
	if slices.ContainsFunc(cat.Views, func(v api.GTDView) bool { return v.ID == "review" }) {
		t.Fatalf("review listed before it is served: %v", cat.Views)
	}
	var none api.Review
	if code := get(t, r.s, api.ReviewPath, &none); code != 404 {
		t.Errorf("GET review before it is served: %d, want 404", code)
	}

	r.put(t, task.Entry{ID: "e-in", State: task.Inbox, Title: "in"}, task.Entry{ID: "e-p", Kind: task.KindProject, State: task.Next, Title: "P", Slug: "p"})
	r.serveProjects()
	get(t, r.s, api.GTDPath, &cat)
	var ids []string
	for _, v := range cat.Views {
		ids = append(ids, v.ID)
	}
	if want := []string{"focus", "inbox", "outbox", "next", "waiting", "projects", "someday", "logbook", "review"}; !slices.Equal(ids, want) {
		t.Fatalf("views %v, want %v", ids, want)
	}
	var rv api.Review
	if code := get(t, r.s, api.ReviewPath, &rv); code != 200 {
		t.Fatalf("GET review: %d", code)
	}
	var steps []string
	for _, st := range rv.Steps {
		steps = append(steps, st.ID)
		if st.Query["view"] != st.View || st.View == "" {
			t.Errorf("step %s: view %q query %v", st.ID, st.View, st.Query)
		}
	}
	if want := []string{"inbox", "outbox", "waiting", "projects", "someday", "focus"}; !slices.Equal(steps, want) || rv.ID == "" || rv.Done {
		t.Fatalf("review %+v", rv)
	}
	if st := rv.Steps[0]; st.Count != 1 || st.Done {
		t.Errorf("inbox step %+v", st)
	}
	if st := rv.Steps[3]; st.View != "review" || st.Count != 1 {
		t.Errorf("projects step %+v", st)
	}
	page := r.page(t, "view=review")
	if got := entryIDs(page.Entries); !slices.Equal(got, []string{"e-p"}) {
		t.Errorf("the review view lists %v", got)
	}

	rec := post(t, r.s, api.ReviewStepPath("inbox"), `{}`)
	if rec.Code != 200 {
		t.Fatalf("POST step done: %d %s", rec.Code, rec.Body)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &rv); err != nil || !rv.Steps[0].Done {
		t.Fatalf("after inbox done: %+v %v", rv, err)
	}
	if rec := post(t, r.s, api.ReviewStepPath("bogus"), `{}`); rec.Code != 400 {
		t.Errorf("an unknown step: %d", rec.Code)
	}
}

func TestProjectEntryViews(t *testing.T) {
	t.Parallel()
	r := newEntriesRig(t, nil)
	r.serveProjects()
	old := entriesNow.Add(-10 * 24 * time.Hour)
	r.put(t,
		task.Entry{ID: "e-p", Kind: task.KindProject, State: task.Next, Title: "P", Slug: "p", Sequential: true, ReviewedAt: old, Created: old, Updated: old},
		task.Entry{ID: "e-q", Kind: task.KindProject, State: task.Next, Title: "Q", Slug: "q", ReviewEvery: 14 * 24 * time.Hour, ReviewedAt: old, Created: old, Updated: old},
		task.Entry{ID: "e-a", State: task.Next, Title: "a", Project: "e-p", Position: 1},
		task.Entry{ID: "e-b", State: task.Waiting, Title: "b", Project: "e-p", Position: 2},
	)
	page := r.page(t, "view=projects")
	byID := map[string]api.EntryView{}
	for _, v := range page.Entries {
		byID[v.ID] = v
	}
	p, q := byID["e-p"], byID["e-q"]
	if p.NextAction != "e-a" || p.NextActionTitle != "a" || p.OpenActions != 2 || !p.Sequential || !p.ReviewDue || p.ReviewEvery != "168h0m0s" || !p.ReviewedAt.Equal(old) {
		t.Errorf("project p %+v", p)
	}
	if q.NextAction != "" || q.OpenActions != 0 || q.ReviewDue || q.ReviewEvery != "336h0m0s" {
		t.Errorf("project q %+v", q)
	}
	verbs := func(v api.EntryView) map[string]bool {
		out := map[string]bool{}
		for _, st := range v.Verbs {
			out[st.Verb] = st.Enabled
		}
		return out
	}
	if vs := verbs(p); !vs["reviewed"] || !vs["sequential"] || vs["project"] {
		t.Errorf("project verbs %v", vs)
	}
	a := r.page(t, "view=next").Entries
	i := slices.IndexFunc(a, func(v api.EntryView) bool { return v.ID == "e-a" })
	if i < 0 {
		t.Fatalf("e-a not in Next: %v", entryIDs(a))
	}
	if vs := verbs(a[i]); vs["reviewed"] || vs["sequential"] || !vs["project"] || a[i].ProjectTitle != "P" {
		t.Errorf("action verbs %v, project %q", vs, a[i].ProjectTitle)
	}

	r.put(t, task.Entry{ID: "e-done", Kind: task.KindProject, State: task.Done, Title: "Done", Slug: "done"},
		task.Entry{ID: "e-trash", Kind: task.KindProject, State: task.Trash, Title: "Trash", Slug: "trash"})
	for id, want := range map[string]map[string]bool{
		"e-done":  {"done": false, "dismiss": true, "reference": true, "reviewed": false, "sequential": false, "project": false},
		"e-trash": {"done": false, "dismiss": false, "reference": false, "reviewed": false, "sequential": false, "project": false},
		"e-p":     {"done": true, "dismiss": true, "reference": true, "reviewed": true, "sequential": true, "project": false},
	} {
		var v api.EntryView
		if code := get(t, r.s, api.EntryPath(id), &v); code != 200 {
			t.Fatalf("GET %s: %d", id, code)
		}
		got := verbs(v)
		for verb, on := range want {
			if got[verb] != on {
				t.Errorf("%s: %s enabled %v, want %v", id, verb, got[verb], on)
			}
		}
		for _, st := range v.Verbs {
			if !st.Enabled && st.Reason == "" {
				t.Errorf("%s: %s refused without a reason", id, st.Verb)
			}
		}
	}

	rec := post(t, r.s, api.EntryPath("e-p", "reviewed"), `{"args":{"every":"3d"},"source":"test"}`)
	var res api.EntryResult
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &res) != nil || res.Entry.ReviewDue || res.Entry.ReviewEvery != "72h0m0s" {
		t.Errorf("reviewed: %d %s", rec.Code, rec.Body)
	}
}

func TestTheReviewStepsAreWordedByTheProfile(t *testing.T) {
	t.Parallel()
	texts := intentload.GTDTexts{Review: map[string]string{"waiting": "Chase what waits"}}
	r := newEntriesRig(t, func(d *Deps) {
		d.Intents = func() *intentload.Loaded { return &intentload.Loaded{GTD: texts, Registry: dintent.NewRegistry(nil)} }
	})
	r.serveProjects()
	var rv api.Review
	if code := get(t, r.s, api.ReviewPath, &rv); code != 200 {
		t.Fatalf("%d", code)
	}
	labels := map[string]string{}
	for _, st := range rv.Steps {
		labels[st.ID] = st.Label
	}
	if labels["waiting"] != "Chase what waits" || labels["inbox"] != "Inbox to zero" {
		t.Errorf("labels %v", labels)
	}
}
