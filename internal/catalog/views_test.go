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
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/intent/expr"
	"github.com/wloophq/wloop/internal/profile"
	"github.com/wloophq/wloop/internal/turn"
)

// lensNow is the time the views read as now.
var lensNow = time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)

// lensFacts are the facts of a task of category c with the given facts besides, its owed_by as l's profile says.
func lensFacts(t *testing.T, l *intentload.Loaded, c task.Category, more workflow.Facts) workflow.Facts {
	t.Helper()
	f := workflow.Facts{"task.category": string(c), "task.gates_open": 0, "task.link_owes": []string{}, "task.wake_until": time.Time{}, "task.wake_when": "",
		"task.check_on": time.Time{}, "task.since_movement": time.Duration(0), "task.turns_running": 0, "task.children_mine": false}
	for _, name := range builtin.LinkSignals {
		f[builtin.LinkSignalPrefix+name] = false
	}
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
		c, _ := f["task.category"].(string)
		in, err := ViewAt(l.Views, v.ID, task.Task{Category: task.Category(c)}, f, lensNow)
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
		"an inbox task":                            {lensFacts(t, l, task.CategoryInbox, nil), []string{"inbox"}},
		"my next action":                           {lensFacts(t, l, task.CategoryReady, nil), []string{"next", "home-stalled"}},
		"one Ana owes":                             {lensFacts(t, l, task.CategoryReady, workflow.Facts{"task.link_owes": []string{"slack:U0ANA"}}), []string{"waiting", "home-stalled"}},
		"a snoozed one":                            {lensFacts(t, l, task.CategoryDeferred, workflow.Facts{"task.state": "scheduled", "task.wake_until": time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)}), []string{"snoozed"}},
		"a someday one":                            {lensFacts(t, l, task.CategoryDeferred, workflow.Facts{"task.state": "someday"}), []string{"someday"}},
		"a starred project":                        {lensFacts(t, l, task.CategoryReady, workflow.Facts{"task.prop.focus": "on", "task.prop.kind": "project", "task.children_ready": true, "task.children_mine": true}), []string{"next", "focus", "projects", "home-stalled"}},
		"a project to review":                      {lensFacts(t, l, task.CategoryReady, workflow.Facts{"task.prop.kind": "project"}), []string{"next", "projects", "review-projects", "home-stalled"}},
		"a done one":                               {lensFacts(t, l, task.CategoryDone, nil), []string{"logbook"}},
		"one handed to Ana":                        {lensFacts(t, l, task.CategoryReady, workflow.Facts{"task.state": "waiting", "task.prop.owner": "slack:U0ANA", "task.prop_by.owner": "person"}), []string{"waiting", "home-stalled"}},
		"one an agent runs":                        {lensFacts(t, l, task.CategoryReady, workflow.Facts{"task.turns_running": 1}), []string{"in-loop"}},
		"a done one an agent runs":                 {lensFacts(t, l, task.CategoryDone, workflow.Facts{"task.turns_running": 1}), []string{"in-loop", "logbook"}},
		"one an agent runs that Ana owes":          {lensFacts(t, l, task.CategoryReady, workflow.Facts{"task.turns_running": 1, "task.link_owes": []string{"slack:U0ANA"}}), []string{"waiting", "in-loop"}},
		"a project whose ready child waits on Ana": {lensFacts(t, l, task.CategoryReady, workflow.Facts{"task.prop.kind": "project", "task.children_ready": true}), []string{"next", "projects", "review-projects", "home-stalled"}},
		"a done one whose pull request's workspace remains": {lensFacts(t, l, task.CategoryDone, workflow.Facts{"task.link_signal.workspace": true}),
			[]string{"done", "logbook"}},
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
	if in, err := GateViewAt(l.Views, "inbox", gate.Gate{}, low, lensNow); err != nil || in {
		t.Errorf("a view over tasks holds a gate: %v %v", in, err)
	}
}

func TestAQuestionGateOnADoneTaskShowsInNeedsYou(t *testing.T) {
	l := bundled(t)
	asked := lensFacts(t, l, task.CategoryDone, workflow.Facts{"task.gates_open": 1, "task.decisions": []string{"answer"}})
	if got := inViews(t, l, asked); !slices.Equal(got, []string{"needs-you", "logbook", "decide-answer"}) {
		t.Errorf("a done task with an open question is in %v", got)
	}
	answered := lensFacts(t, l, task.CategoryDone, nil)
	if got := inViews(t, l, answered); !slices.Equal(got, []string{"logbook"}) {
		t.Errorf("once answered it is in %v", got)
	}
}

// TestEveryOpenGateShowsInABundledView checks that an open gate of every kind, on a task of every category, is
// in a bundled view the badge names, through its task.
func TestEveryOpenGateShowsInABundledView(t *testing.T) {
	l := bundled(t)
	for _, k := range gate.Kinds {
		for _, c := range task.Categories {
			f := lensFacts(t, l, c, workflow.Facts{"task.gates_open": 1, "task.decisions": []string{string(l.Decisions.Map.Gates[k])}})
			if !slices.ContainsFunc(inViews(t, l, f), l.Views.Badged) {
				t.Errorf("an open %s gate on a %s task is in no view the badge names: %v", k, c, inViews(t, l, f))
			}
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
	if want := []string{"inbox", "focus", "projects", "someday"}; !slices.Equal(paged, want) {
		t.Errorf("paged views %v, want %v", paged, want)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, intentload.ViewsFile), []byte("[views.overdue]\nlabel = \"Overdue\"\nsummary = \"s\"\nwhen = \"task.prop.due > timestamp(0) && task.prop.due < now\"\n"), 0o600); err != nil {
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
					got, err := ViewAt(l.Views, id, e, f, lensNow)
					if err != nil || got != was(e) {
						t.Errorf("%s in state %s (star %v, project %v): %v, Go's view said %v (%v)", id, st.Name, star, project, got, was(e), err)
					}
				}
			}
		}
	}
}

func TestAViewListsOpenTasksAloneUnlessItAsksForClosedOnes(t *testing.T) {
	ctx := context.Background()
	l := new(intentload.Loaded)
	*l = *bundled(t)
	every, err := expr.CompileWorkflow(`true`, builtin.WorkflowEnv(builtin.TaskSlot))
	if err != nil {
		t.Fatal(err)
	}
	l.Views.Views = append(slices.Clone(l.Views.Views),
		intentload.ViewDef{ID: "every", Over: task.OverTasks, When: every},
		intentload.ViewDef{ID: "every-ever", Over: task.OverTasks, When: every, Closed: true})
	var read, heldAsked []task.ID
	lens := Lens{Loaded: func() *intentload.Loaded { return l }, Now: func() time.Time { return lensNow },
		Held: func(_ context.Context, e task.Task) (bool, error) {
			heldAsked = append(heldAsked, e.ID)
			return e.ID == "t-asked", nil
		},
		Facts: func(_ context.Context, e task.Task) (workflow.Facts, error) {
			read = append(read, e.ID)
			return TaskFacts(e, l.Tasks), nil
		}}
	open := task.Task{ID: "t-open", State: "next", Category: task.CategoryReady, Props: task.Props{}}
	done := task.Task{ID: "t-done", State: "done", Category: task.CategoryDone, Props: task.Props{}}
	dropped := task.Task{ID: "t-dropped", State: "trash", Category: task.CategoryDropped, Props: task.Props{}}
	asked := task.Task{ID: "t-asked", State: "done", Category: task.CategoryDone, Props: task.Props{}}
	failed := task.Task{ID: "t-failed", State: "done", Category: task.CategoryDone, Props: task.Props{}}
	for _, c := range []struct {
		view string
		e    task.Task
		want bool
	}{
		{"every", open, true}, {"every", done, false}, {"every", dropped, false},
		{"every-ever", open, true}, {"every-ever", done, true}, {"every-ever", dropped, true},
		{"every", asked, true}, {"every-ever", asked, true},
	} {
		read, heldAsked = nil, nil
		in, err := lens.Eval(ctx, c.view, c.e)
		if err != nil || in != c.want {
			t.Errorf("%s holds %s: %v (%v), want %v", c.view, c.e.ID, in, err, c.want)
		}
		if !c.e.Open() && !c.want && len(read) > 0 {
			t.Errorf("%s read the facts of %s, a closed task it cannot hold", c.view, c.e.ID)
		}
		if c.view == "every-ever" && len(heldAsked) > 0 {
			t.Errorf("%s, which lists closed tasks, asked whether the loop holds %s", c.view, c.e.ID)
		}
		f := TaskFacts(c.e, l.Tasks)
		if c.e.ID == "t-asked" {
			f["task.gates_open"] = 1
		}
		if at, err := ViewAt(l.Views, c.view, c.e, f, lensNow); err != nil || at != c.want {
			t.Errorf("ViewAt %s holds %s: %v (%v), want %v", c.view, c.e.ID, at, err, c.want)
		}
	}
	unwired := lens
	unwired.Held = nil
	unwired.Facts = func(_ context.Context, e task.Task) (workflow.Facts, error) {
		f := TaskFacts(e, l.Tasks)
		f["task.turns_running"] = map[task.ID]int{"t-asked": 1}[e.ID]
		if e.ID == "t-failed" {
			f["task.decisions"] = []string{"send-draft"}
		}
		return f, nil
	}
	for _, c := range []struct {
		e    task.Task
		want bool
	}{{asked, true}, {failed, true}, {done, false}} {
		if in, err := unwired.Eval(ctx, "every", c.e); err != nil || in != c.want {
			t.Errorf("with no Held, every holds %s: %v (%v), want %v", c.e.ID, in, err, c.want)
		}
	}
}
