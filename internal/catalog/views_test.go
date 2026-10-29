package catalog

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/workflow"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/profile"
	"github.com/wloophq/wloop/internal/turn"
)

// lensFacts are the facts of a task of category c with the given facts besides, its owed_by as l's profile says.
func lensFacts(t *testing.T, l *intentload.Loaded, c task.Category, more workflow.Facts) workflow.Facts {
	t.Helper()
	f := workflow.Facts{"task.category": string(c), "task.gates_open": 0, "task.link_owes": []string{}, "task.wake_until": time.Time{}, "task.wake_when": ""}
	for k, v := range more {
		f[k] = v
	}
	who, err := OwedByOf(func() *intentload.Loaded { return l })(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	f["task.owed_by"] = who
	return f
}

// inViews are the views of l over tasks that hold a task whose facts are f.
func inViews(t *testing.T, l *intentload.Loaded, f workflow.Facts) []string {
	t.Helper()
	var out []string
	for _, v := range l.Views.Views {
		in, err := View(l.Views, v.ID, task.Task{}, f)
		if err != nil {
			t.Fatal(err)
		}
		if in {
			out = append(out, v.ID)
		}
	}
	return out
}

func TestAViewIsTheProfilesPredicate(t *testing.T) {
	l := bundled(t)
	for name, c := range map[string]struct {
		f    workflow.Facts
		want []string
	}{
		"an inbox task":         {lensFacts(t, l, task.CategoryInbox, nil), []string{"inbox"}},
		"my next action":        {lensFacts(t, l, task.CategoryReady, nil), []string{"needs-me"}},
		"one Ana owes":          {lensFacts(t, l, task.CategoryWaiting, workflow.Facts{"task.link_owes": []string{"slack:U0ANA"}}), []string{"waiting"}},
		"a snoozed one":         {lensFacts(t, l, task.CategoryDeferred, workflow.Facts{"task.state": "scheduled", "task.wake_until": time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)}), []string{"snoozed"}},
		"a someday one":         {lensFacts(t, l, task.CategoryDeferred, workflow.Facts{"task.state": "someday"}), []string{"someday"}},
		"a starred project":     {lensFacts(t, l, task.CategoryReady, workflow.Facts{"task.prop.focus": "on", "task.prop.kind": "project", "task.children_ready": true}), []string{"needs-me", "focus", "projects"}},
		"a project to review":   {lensFacts(t, l, task.CategoryReady, workflow.Facts{"task.prop.kind": "project"}), []string{"needs-me", "projects", "review-projects"}},
		"a done one":            {lensFacts(t, l, task.CategoryDone, nil), []string{"logbook"}},
		"a dropped starred one": {lensFacts(t, l, task.CategoryDropped, workflow.Facts{"task.prop.focus": "on"}), nil},
	} {
		if got := inViews(t, l, c.f); !slices.Equal(got, c.want) {
			t.Errorf("%s: in %v, want %v", name, got, c.want)
		}
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, intentload.ViewsFile), []byte("[views.inbox]\nwhen = \"task.category == 'inbox' && task.prop.priority != 'low'\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	base, err := turn.Base()
	if err != nil {
		t.Fatal(err)
	}
	mine, problems := intentload.Load(append(profile.BundledChain(), profile.Layer{Name: "user", Source: dir, Dir: dir}), turn.BundledFS(), base, turn.CheckTemplate)
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	low := lensFacts(t, mine, task.CategoryInbox, workflow.Facts{"task.prop.priority": "low"})
	if got := inViews(t, mine, low); len(got) != 0 {
		t.Errorf("the user's inbox keeps a low one: %v", got)
	}
	if got := inViews(t, l, low); !slices.Equal(got, []string{"inbox"}) {
		t.Errorf("the bundled inbox: %v", got)
	}
	if in, err := GateView(l.Views, "inbox", gate.Gate{}, low); err != nil || in {
		t.Errorf("a view over tasks holds a gate: %v %v", in, err)
	}
}

func TestAQuestionGateOnADoneTaskShowsInNeedsMe(t *testing.T) {
	l := bundled(t)
	asked := lensFacts(t, l, task.CategoryDone, workflow.Facts{"task.gates_open": 1})
	if got := inViews(t, l, asked); !slices.Equal(got, []string{"needs-me", "logbook"}) {
		t.Errorf("a done task with an open question is in %v", got)
	}
	answered := lensFacts(t, l, task.CategoryDone, nil)
	if got := inViews(t, l, answered); !slices.Equal(got, []string{"logbook"}) {
		t.Errorf("once answered it is in %v", got)
	}
}

// TestEveryOpenGateShowsInABundledView checks that an open gate of every kind, on a task of every category, is
// in a bundled view the badge counts, through its task, and a draft also in a view over gates.
func TestEveryOpenGateShowsInABundledView(t *testing.T) {
	l := bundled(t)
	for _, k := range gate.Kinds {
		for _, c := range task.Categories {
			f := lensFacts(t, l, c, workflow.Facts{"task.gates_open": 1})
			if !slices.ContainsFunc(inViews(t, l, f), l.Views.Badged) {
				t.Errorf("an open %s gate on a %s task is in no view the badge counts: %v", k, c, inViews(t, l, f))
			}
		}
		g := gate.Gate{Kind: k, Status: gate.Open}
		var over []string
		for _, v := range l.Views.Views {
			if in, err := GateView(l.Views, v.ID, g, intentload.GateFacts(g, "held")); err != nil {
				t.Fatal(err)
			} else if in {
				over = append(over, v.ID)
			}
		}
		if want := k == gate.KindDraft; want != slices.Contains(over, "outbox") {
			t.Errorf("an open %s gate: in %v", k, over)
		}
	}
}

func TestOutboxKeepsAFailedApprovedDraft(t *testing.T) {
	l := bundled(t)
	for _, c := range []struct {
		status   gate.Status
		delivery string
		want     bool
	}{
		{gate.Open, "held", true},
		{gate.Approved, "queued", false},
		{gate.Approved, "sending", true},
		{gate.Approved, "failed", true},
		{gate.Approved, "sent", false},
		{gate.Rejected, "dropped", false},
	} {
		g := gate.Gate{Kind: gate.KindDraft, Status: c.status}
		if in, err := GateView(l.Views, "outbox", g, intentload.GateFacts(g, c.delivery)); err != nil || in != c.want {
			t.Errorf("a %s draft gate delivery %s: in the outbox %v, %v", c.status, c.delivery, in, err)
		}
	}
}

// TestOnlyAViewOverATasksOwnFieldsIsPaged checks which bundled views a page since a position may list in part:
// those whose when reads only what a write of the task moves; a view reading another fact or the time is served
// whole.
func TestOnlyAViewOverATasksOwnFieldsIsPaged(t *testing.T) {
	l := bundled(t)
	var paged []string
	for _, v := range l.Views.Views {
		if Lists(l.Views, v.ID) && Paged(l.Views, v.ID) {
			paged = append(paged, v.ID)
		}
	}
	if want := []string{"inbox", "focus", "projects", "someday", "logbook"}; !slices.Equal(paged, want) {
		t.Errorf("paged views %v, want %v", paged, want)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, intentload.ViewsFile), []byte("[views.overdue]\nlabel = \"Overdue\"\nwhen = \"task.prop.due > timestamp(0) && task.prop.due < now\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	base, err := turn.Base()
	if err != nil {
		t.Fatal(err)
	}
	mine, problems := intentload.Load(append(profile.BundledChain(), profile.Layer{Name: "user", Source: dir, Dir: dir}), turn.BundledFS(), base, turn.CheckTemplate)
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	if !OwnFacts(mine.Views, "overdue") || Paged(mine.Views, "overdue") {
		t.Error("a view reading now is paged")
	}
}

// TestTheBundledViewsListWhatTheGoViewsListed is the differential test against the views Go declared before the
// profile did (domain/task/query.go): over every bundled state, starred or not, project or not, asleep or
// not, the bundled inbox, focus, projects, someday and logbook hold what Go's did.
func TestTheBundledViewsListWhatTheGoViewsListed(t *testing.T) {
	l := bundled(t)
	old := map[string]func(e task.Task) bool{
		"inbox":    func(e task.Task) bool { return e.Category == task.CategoryInbox },
		"focus":    func(e task.Task) bool { return (e.Prop("focus").Text == "on") && e.Open() },
		"projects": func(e task.Task) bool { return e.IsProject() && e.Open() },
		"someday":  func(e task.Task) bool { return e.Category == task.CategoryDeferred && !e.Snoozed() },
		"logbook":  func(e task.Task) bool { return e.Category == task.CategoryDone },
	}
	for _, st := range l.Tasks.States {
		for _, star := range []bool{false, true} {
			for _, project := range []bool{false, true} {
				e := task.Task{ID: "t-1", State: task.State(st.Name), Category: st.Category, Props: task.Props{}}
				if star {
					e.Props["focus"] = task.Value{Text: "on"}
				}
				if project {
					e.Props["kind"] = task.Value{Text: task.KindProject}
				}
				e.Asleep = st.Name == "scheduled"
				f := TaskFacts(e, l.Tasks)
				for id, was := range old {
					got, err := View(l.Views, id, e, f)
					if err != nil || got != was(e) {
						t.Errorf("%s in state %s (star %v, project %v): %v, Go's view said %v (%v)", id, st.Name, star, project, got, was(e), err)
					}
				}
			}
		}
	}
}
