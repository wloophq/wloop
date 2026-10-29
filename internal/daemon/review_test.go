package daemon

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/task"
	intentload "github.com/wloophq/wloop/internal/intent"
)

func (r *tasksRig) serveProjects() {
	r.g.ServeProjects(coretasks.ProjectDeps{Store: r.db.Projects()})
}

func TestTheReviewIsServedOnceRegistered(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	var none api.Review
	if code := get(t, r.s, api.ReviewPath, &none); code != 404 {
		t.Errorf("GET review before it is served: %d, want 404", code)
	}
	r.put(t, task.Task{ID: "e-in", State: "inbox", Category: task.CategoryInbox, Title: "in"}, task.Task{ID: "e-p", Props: task.Props{task.PropKind: {Text: task.KindProject}}, State: "next", Category: task.CategoryReady, Title: "P"})
	r.serveProjects()
	var cat api.ViewsCatalog
	get(t, r.s, api.ViewsPath, &cat)
	if want := []string{"inbox", "outbox", "waiting", "review-projects", "someday", "focus"}; !slices.Equal(cat.Review, want) {
		t.Fatalf("the catalog's review %v, want %v", cat.Review, want)
	}
	var rv api.Review
	if code := get(t, r.s, api.ReviewPath, &rv); code != 200 {
		t.Fatalf("GET review: %d", code)
	}
	var steps []string
	for _, st := range rv.Steps {
		steps = append(steps, st.ID)
		if st.Query["view"] != st.View || st.View != st.ID {
			t.Errorf("step %s: view %q query %v", st.ID, st.View, st.Query)
		}
	}
	if !slices.Equal(steps, cat.Review) || rv.ID == "" || rv.Done {
		t.Fatalf("review %+v", rv)
	}
	if st := rv.Steps[0]; st.Count != 1 || st.Done || st.Label != "Inbox to zero" {
		t.Errorf("inbox step %+v", st)
	}
	if st := rv.Steps[3]; st.Count != 1 {
		t.Errorf("projects step %+v", st)
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

// TestReviewProjectsStepListsProjectsDueOrWithoutANextAction runs the bundled review-projects view over the task
// facts of a real store: an open project past its review, or with no ready child unless in Someday.
func TestReviewProjectsStepListsProjectsDueOrWithoutANextAction(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	r.serveProjects()
	old, recent := tasksNow.Add(-10*24*time.Hour), tasksNow.Add(-time.Hour)
	project := func(id task.ID, state string, c task.Category, at time.Time) task.Task {
		return task.Task{ID: id, Props: task.Props{task.PropKind: {Text: task.KindProject}}, State: task.State(state), Category: c, Title: string(id), Created: at, Updated: at}
	}
	r.put(t,
		project("p-due", "next", task.CategoryReady, old),
		task.Task{ID: "a-due", State: "next", Category: task.CategoryReady, Parent: "p-due", Position: 1, Created: old, Updated: old},
		project("p-empty", "next", task.CategoryReady, recent),
		task.Task{ID: "a-empty", State: "waiting", Category: task.CategoryWaiting, Parent: "p-empty", Position: 1, Created: recent, Updated: recent},
		project("p-fine", "next", task.CategoryReady, recent),
		task.Task{ID: "a-fine", State: "next", Category: task.CategoryReady, Parent: "p-fine", Position: 1, Created: recent, Updated: recent},
		project("p-someday", "someday", task.CategoryDeferred, recent),
		project("p-done", "done", task.CategoryDone, old),
	)
	page := r.page(t, "view=review-projects")
	if got := taskIDs(page.Tasks); !slices.Equal(got, []string{"p-due", "p-empty"}) || page.Counters.Views["review-projects"] != 2 {
		t.Fatalf("review-projects lists %v, counts %d", got, page.Counters.Views["review-projects"])
	}
	var rv api.Review
	get(t, r.s, api.ReviewPath, &rv)
	if i := slices.IndexFunc(rv.Steps, func(st api.ReviewStep) bool { return st.View == "review-projects" }); i < 0 || rv.Steps[i].Count != 2 {
		t.Fatalf("the review's projects step %+v", rv.Steps)
	}
}

func TestProjectTaskViews(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	r.serveProjects()
	old := tasksNow.Add(-10 * 24 * time.Hour)
	r.put(t,
		task.Task{ID: "e-p", Props: task.Props{task.PropKind: {Text: task.KindProject}}, State: "next", Category: task.CategoryReady, Title: "P", Sequential: true, ReviewedAt: old, Created: old, Updated: old},
		task.Task{ID: "e-q", Props: task.Props{task.PropKind: {Text: task.KindProject}}, State: "next", Category: task.CategoryReady, Title: "Q", ReviewEvery: 14 * 24 * time.Hour, ReviewedAt: old, Created: old, Updated: old},
		task.Task{ID: "e-a", State: "next", Category: task.CategoryReady, Title: "a", Parent: "e-p", Position: 1},
		task.Task{ID: "e-b", State: "waiting", Category: task.CategoryWaiting, Title: "b", Parent: "e-p", Position: 2},
	)
	page := r.page(t, "view=projects")
	byID := map[string]api.TaskView{}
	for _, v := range page.Tasks {
		byID[v.ID] = v
	}
	p, q := byID["e-p"], byID["e-q"]
	if !slices.Equal(p.Children, []string{"e-a", "e-b"}) || !p.Sequential || !p.ReviewDue || p.ReviewEvery != "168h0m0s" || !p.ReviewedAt.Equal(old) {
		t.Errorf("project p %+v", p)
	}
	if len(q.Children) != 0 || q.ReviewDue || q.ReviewEvery != "336h0m0s" {
		t.Errorf("project q %+v", q)
	}
	verbs := func(v api.TaskView) map[string]bool {
		out := map[string]bool{}
		for _, st := range v.Verbs {
			out[st.Verb] = st.Enabled
		}
		return out
	}
	if vs := verbs(p); !vs["reviewed"] || !vs["sequential"] || vs["project"] {
		t.Errorf("project verbs %v", vs)
	}
	a := r.page(t, "view=needs-me").Tasks
	i := slices.IndexFunc(a, func(v api.TaskView) bool { return v.ID == "e-a" })
	if i < 0 {
		t.Fatalf("e-a not in Needs me: %v", taskIDs(a))
	}
	if vs := verbs(a[i]); vs["reviewed"] || vs["sequential"] || !vs["project"] || a[i].Parent != "e-p" {
		t.Errorf("action verbs %v, parent %q", vs, a[i].Parent)
	}

	r.put(t, task.Task{ID: "e-done", Props: task.Props{task.PropKind: {Text: task.KindProject}}, State: "done", Category: task.CategoryDone, Title: "Done"},
		task.Task{ID: "e-trash", Props: task.Props{task.PropKind: {Text: task.KindProject}}, State: "trash", Category: task.CategoryDropped, Title: "Trash"})
	for id, want := range map[string]map[string]bool{
		"e-done":  {"done": false, "dismiss": true, "reference": true, "reviewed": false, "sequential": false, "project": false},
		"e-trash": {"done": false, "dismiss": false, "reference": false, "reviewed": false, "sequential": false, "project": false},
		"e-p":     {"done": true, "dismiss": true, "reference": true, "reviewed": true, "sequential": true, "project": false},
	} {
		var v api.TaskView
		if code := get(t, r.s, api.TaskPath(id), &v); code != 200 {
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

	rec := post(t, r.s, api.TaskPath("e-p", "reviewed"), `{"args":{"every":"3d"},"source":"test"}`)
	var res api.TaskResult
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &res) != nil || res.Task.ReviewDue || res.Task.ReviewEvery != "72h0m0s" {
		t.Errorf("reviewed: %d %s", rec.Code, rec.Body)
	}
}

func TestTheReviewStepsAreWordedByTheProfile(t *testing.T) {
	t.Parallel()
	l := *bundledLoaded()
	l.Views.Review = slices.Clone(l.Views.Review)
	l.Views.Review[slices.IndexFunc(l.Views.Review, func(st intentload.ReviewStepDef) bool { return st.View == "waiting" })].Label = "Chase what waits"
	r := newTasksRig(t, func(d *Deps) { d.Intents = func() *intentload.Loaded { return &l } })
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
