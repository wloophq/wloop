package tasks_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/core/tasks/taskstest"
	"github.com/wloophq/wloop/internal/domain/task"
)

type fakeDossiers struct {
	opened   []task.ID
	resolved map[task.ID]time.Time
	fail     error
}

func (f *fakeDossiers) Open(ctx context.Context, id task.ID) error {
	if f.fail != nil {
		return f.fail
	}
	f.opened = append(f.opened, id)
	return nil
}

func (f *fakeDossiers) Resolved(ctx context.Context, id task.ID, at time.Time) error {
	if f.resolved == nil {
		f.resolved = map[task.ID]time.Time{}
	}
	f.resolved[id] = at
	return nil
}

type projectRig struct {
	*rig
	projects *taskstest.Projects
	dossiers *fakeDossiers
}

func newProjectRig() *projectRig {
	r := &projectRig{rig: newRig(), dossiers: &fakeDossiers{}}
	r.projects = &taskstest.Projects{}
	r.svc.ServeProjects(coretasks.ProjectDeps{Store: r.projects, Dossiers: r.dossiers})
	return r
}

func (r *projectRig) do(t *testing.T, id task.ID, verb task.Verb, args map[string]string) coretasks.Result {
	t.Helper()
	res, err := r.svc.Do(context.Background(), id, coretasks.Request{Actor: task.ActorPerson, Verb: verb, Args: args})
	if err != nil {
		t.Fatalf("%s on %s: %v", verb, id, err)
	}
	return res
}

func (r *projectRig) project(t *testing.T, id task.ID, mod func(*task.Task)) task.Task {
	t.Helper()
	p := task.Task{ID: id, Props: task.Props{task.PropKind: {Text: task.KindProject}}, State: "next", Category: task.CategoryReady, Title: "Project " + string(id), Origin: task.OriginUser, Created: t0, Updated: t0}
	if mod != nil {
		mod(&p)
	}
	return r.put(t, p)
}

func (r *projectRig) action(t *testing.T, id task.ID, project task.ID, pos int, st task.State) task.Task {
	t.Helper()
	return r.put(t, task.Task{ID: id, State: st, Title: "Action " + string(id), Origin: task.OriginUser,
		Parent: project, Position: pos, Created: t0.Add(time.Duration(pos) * time.Minute), Updated: t0})
}

func positions(t *testing.T, r *projectRig, ids ...task.ID) map[task.ID]int {
	t.Helper()
	out := map[task.ID]int{}
	for _, id := range ids {
		out[id] = r.task(t, id).Position
	}
	return out
}

func TestProjectFromTask(t *testing.T) {
	r := newProjectRig()
	e := r.put(t, task.Task{ID: "t-cap", State: "inbox", Category: task.CategoryInbox, Title: "Write the RFC", Props: task.Props{"area": {Text: "OSS"}}, Origin: task.OriginUser})
	res := r.do(t, e.ID, task.VerbProject, map[string]string{"title": "Ship the v2 API"})
	if res.Task.ID != e.ID || res.Task.Parent == "" || res.Task.Position != 1 || res.Task.Category != task.CategoryInbox {
		t.Fatalf("attached task %+v", res.Task)
	}
	p := r.task(t, res.Task.Parent)
	if !p.IsProject() || p.Category != task.CategoryReady || p.Title != "Ship the v2 API" || p.Prop("area").Text != "OSS" {
		t.Fatalf("project %+v", p)
	}
	if !slices.Equal(r.dossiers.opened, []task.ID{p.ID}) {
		t.Errorf("dossiers opened %v", r.dossiers.opened)
	}

	other := r.put(t, task.Task{ID: "t-cap2", State: "next", Category: task.CategoryReady, Title: "x", Origin: task.OriginDerived})
	res = r.do(t, other.ID, task.VerbProject, map[string]string{"title": "Ship the v2 API!"})
	if p2 := r.task(t, res.Task.Parent); p2.ID == p.ID || !slices.Equal(r.dossiers.opened, []task.ID{p.ID, p2.ID}) {
		t.Errorf("a second project of the same name: %+v", p2)
	}

	if _, err := r.svc.Do(context.Background(), p.ID, coretasks.Request{Actor: task.ActorPerson, Verb: task.VerbProject, Args: map[string]string{"title": "inner"}}); !errors.Is(err, coretasks.ErrVerb) {
		t.Errorf("a project put in a project: %v, want ErrVerb", err)
	}
	if _, err := r.svc.Do(context.Background(), e.ID, coretasks.Request{Actor: task.ActorPerson, Verb: task.VerbProject}); !errors.Is(err, coretasks.ErrArgs) {
		t.Errorf("project with neither id nor title: %v, want ErrArgs", err)
	}

	r.dossiers.fail = errors.New("disk full")
	third := r.put(t, task.Task{ID: "t-cap3", State: "inbox", Category: task.CategoryInbox, Title: "y", Origin: task.OriginUser})
	res = r.do(t, third.ID, task.VerbProject, map[string]string{"title": "Broken disk"})
	if res.Task.Parent == "" || len(res.Lines) == 0 {
		t.Errorf("a failed dossier still files the project and says so: %+v", res)
	}
}

func TestAttachAndReorder(t *testing.T) {
	r := newProjectRig()
	r.project(t, "p", nil)
	r.action(t, "a", "p", 1, task.State("next"))
	r.action(t, "b", "p", 2, task.State("next"))
	r.action(t, "gone", "p", 7, task.State("trash"))
	c := r.put(t, task.Task{ID: "c", State: "inbox", Category: task.CategoryInbox, Title: "c", Origin: task.OriginUser})
	d := r.put(t, task.Task{ID: "d", State: "next", Category: task.CategoryReady, Title: "d", Origin: task.OriginUser})

	if got := r.do(t, c.ID, task.VerbProject, map[string]string{"id": "p"}).Task; got.Parent != "p" || got.Position != 3 {
		t.Fatalf("attached at %d of %q, want 3 of p", got.Position, got.Parent)
	}
	r.do(t, d.ID, task.VerbProject, map[string]string{"id": "p", "position": "2"})
	want := map[task.ID]int{"a": 1, "d": 2, "b": 3, "c": 4}
	if got := positions(t, r, "a", "b", "c", "d"); !equalPositions(got, want) {
		t.Fatalf("positions %v, want %v", got, want)
	}
	r.do(t, c.ID, task.VerbProject, map[string]string{"id": "p", "position": "1"})
	want = map[task.ID]int{"c": 1, "a": 2, "d": 3, "b": 4}
	if got := positions(t, r, "a", "b", "c", "d"); !equalPositions(got, want) {
		t.Fatalf("after moving c first: %v, want %v", got, want)
	}
	for _, bad := range []map[string]string{{"id": "p", "position": "0"}, {"id": "p", "position": "x"}, {"id": "a"}, {"id": "nope"}} {
		if _, err := r.svc.Do(context.Background(), d.ID, coretasks.Request{Actor: task.ActorPerson, Verb: task.VerbProject, Args: bad}); err == nil {
			t.Errorf("project %v: no error", bad)
		}
	}
}

func equalPositions(a, b map[task.ID]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func TestToggleSequential(t *testing.T) {
	r := newProjectRig()
	ctx := context.Background()
	r.project(t, "p", nil)
	r.action(t, "a", "p", 1, task.State("next"))
	r.action(t, "b", "p", 2, task.State("next"))
	if got := r.do(t, "p", task.VerbSequential, nil).Task; !got.Sequential {
		t.Fatal("sequential did not toggle on")
	}
	r.do(t, "p", task.VerbSequential, map[string]string{"set": "true"})
	if !r.task(t, "p").Sequential {
		t.Fatal("set true turned it off")
	}
	if r.do(t, "p", task.VerbSequential, nil); r.task(t, "p").Sequential {
		t.Fatal("sequential did not toggle off")
	}
	if _, err := r.svc.Do(ctx, "a", coretasks.Request{Actor: task.ActorPerson, Verb: task.VerbSequential}); !errors.Is(err, coretasks.ErrVerb) {
		t.Errorf("sequential on an action: %v, want ErrVerb", err)
	}
}

func TestMarkReviewed(t *testing.T) {
	r := newProjectRig()
	ctx := context.Background()
	r.project(t, "p", nil)
	r.clock.t = t0.Add(3 * time.Hour)
	if got := r.do(t, "p", task.VerbReviewed, nil).Task; !got.ReviewedAt.Equal(r.clock.t) || got.ReviewEvery != 0 {
		t.Fatalf("reviewed %+v", got)
	}
	r.do(t, "p", task.VerbReviewed, map[string]string{"every": "14d"})
	if got := r.task(t, "p"); got.ReviewEvery != 14*24*time.Hour {
		t.Errorf("every 14d set %v", got.ReviewEvery)
	}
	r.do(t, "p", task.VerbReviewed, map[string]string{"every": "36h"})
	if got := r.task(t, "p"); got.ReviewEvery != 36*time.Hour {
		t.Errorf("every 36h set %v", got.ReviewEvery)
	}
	for _, bad := range []string{"0d", "-1h", "soon"} {
		if _, err := r.svc.Do(ctx, "p", coretasks.Request{Actor: task.ActorPerson, Verb: task.VerbReviewed, Args: map[string]string{"every": bad}}); !errors.Is(err, coretasks.ErrArgs) {
			t.Errorf("every %q: %v, want ErrArgs", bad, err)
		}
	}
	r.action(t, "a", "p", 1, task.State("next"))
	if _, err := r.svc.Do(ctx, "a", coretasks.Request{Actor: task.ActorPerson, Verb: task.VerbReviewed}); !errors.Is(err, coretasks.ErrVerb) {
		t.Errorf("reviewed on an action: %v, want ErrVerb", err)
	}
}

func TestDoneProjectResolvesItsDossier(t *testing.T) {
	r := newProjectRig()
	r.project(t, "p", nil)
	r.project(t, "q", nil)
	r.action(t, "a", "p", 1, task.State("next"))
	r.clock.t = t0.Add(time.Hour)
	r.do(t, "a", task.VerbDone, nil)
	if len(r.dossiers.resolved) != 0 {
		t.Fatalf("an action's done resolved %v", r.dossiers.resolved)
	}
	if got := r.do(t, "p", task.VerbDone, nil).Task; got.Category != task.CategoryDone {
		t.Fatalf("project state %s", got.State)
	}
	r.do(t, "q", task.VerbDismiss, nil)
	want := map[task.ID]time.Time{"p": r.clock.t, "q": r.clock.t}
	for id, at := range want {
		if got, ok := r.dossiers.resolved[id]; !ok || !got.Equal(at) {
			t.Errorf("dossier of %s resolved %v, %v; want at %v", id, got, ok, at)
		}
	}
	if _, err := r.svc.Do(context.Background(), "p", coretasks.Request{Actor: task.ActorPerson, Verb: task.VerbDone}); !errors.Is(err, coretasks.ErrVerb) {
		t.Errorf("done twice: %v, want ErrVerb", err)
	}
}

// TestTheAreaFiltersEveryView checks that the area narrows every view, the Inbox's too, in its list and its
// count.
func TestTheAreaFiltersEveryView(t *testing.T) {
	r := newProjectRig()
	ctx := context.Background()
	area := func(e *task.Task, a string) { e.SetProp("area", task.Value{Text: a}, task.WriterPerson, t0) }
	for id, a := range map[task.ID]string{"i1": "OSS", "i2": "Hiring", "i3": ""} {
		e := task.Task{ID: id, State: "inbox", Category: task.CategoryInbox, Origin: task.OriginUser}
		area(&e, a)
		r.put(t, e)
	}
	r.project(t, "p1", func(e *task.Task) { area(e, "OSS") })
	for a, want := range map[string]map[task.View][]task.ID{
		"OSS":    {"inbox": {"i1"}, "ready": {"p1"}},
		"Hiring": {"inbox": {"i2"}, "ready": nil},
		"":       {"inbox": {"i1", "i2", "i3"}, "ready": {"p1"}},
	} {
		c, err := r.svc.Counts(ctx, a)
		if err != nil {
			t.Fatal(err)
		}
		for v, w := range want {
			es, err := r.svc.Tasks(ctx, task.Query{View: v, Area: a})
			if err != nil {
				t.Fatal(err)
			}
			var ids []task.ID
			for _, e := range es {
				ids = append(ids, e.ID)
			}
			slices.Sort(ids)
			if !slices.Equal(ids, w) || c.Views[v] != len(w) {
				t.Errorf("area %q, %s: %v counted %d, want %v", a, v, ids, c.Views[v], w)
			}
		}
	}
}

func TestProjectStatusOverTheOpenSet(t *testing.T) {
	r := newProjectRig()
	r.project(t, "p", nil)
	r.action(t, "a", "p", 1, task.State("waiting"))
	r.action(t, "b", "p", 2, task.State("next"))
	r.project(t, "old", func(e *task.Task) { e.Created = t0.Add(-8 * 24 * time.Hour) })
	st, err := r.svc.ProjectsStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st["p"] != (coretasks.ProjectState{ProjectStatus: task.ProjectStatus{Next: "b", NextTitle: "Action b", Open: 2}}) {
		t.Errorf("status %+v", st["p"])
	}
	if !st["old"].Due || len(st) != 2 {
		t.Errorf("an old project is not due: %+v", st)
	}
}
