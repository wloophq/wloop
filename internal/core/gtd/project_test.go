package gtd_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	coregtd "github.com/wloophq/wloop/internal/core/gtd"
	"github.com/wloophq/wloop/internal/core/gtd/gtdtest"
	"github.com/wloophq/wloop/internal/domain/gtd"
)

type fakeDossiers struct {
	opened   []string
	resolved map[string]time.Time
	fail     error
}

func (f *fakeDossiers) Open(ctx context.Context, slug string) error {
	if f.fail != nil {
		return f.fail
	}
	f.opened = append(f.opened, slug)
	return nil
}

func (f *fakeDossiers) Resolved(ctx context.Context, slug string, at time.Time) error {
	if f.resolved == nil {
		f.resolved = map[string]time.Time{}
	}
	f.resolved[slug] = at
	return nil
}

type projectRig struct {
	*rig
	projects *gtdtest.Projects
	dossiers *fakeDossiers
}

func newProjectRig() *projectRig {
	r := &projectRig{rig: newRig(), dossiers: &fakeDossiers{}}
	r.projects = &gtdtest.Projects{Entries: r.store}
	r.svc.ServeProjects(coregtd.ProjectDeps{Store: r.projects, Dossiers: r.dossiers})
	return r
}

func (r *projectRig) do(t *testing.T, id gtd.ID, verb gtd.Verb, args map[string]string) coregtd.Result {
	t.Helper()
	res, err := r.svc.Do(context.Background(), id, coregtd.Request{Verb: verb, Args: args})
	if err != nil {
		t.Fatalf("%s on %s: %v", verb, id, err)
	}
	return res
}

func (r *projectRig) project(t *testing.T, id gtd.ID, mod func(*gtd.Entry)) gtd.Entry {
	t.Helper()
	p := gtd.Entry{ID: id, Kind: gtd.KindProject, State: gtd.Next, Title: "Project " + string(id), Origin: gtd.OriginUser, Slug: string(id), Created: t0, Updated: t0}
	if mod != nil {
		mod(&p)
	}
	return r.put(t, p)
}

func (r *projectRig) action(t *testing.T, id gtd.ID, project gtd.ID, pos int, st gtd.State) gtd.Entry {
	t.Helper()
	return r.put(t, gtd.Entry{ID: id, Kind: gtd.KindCapture, State: st, Title: "Action " + string(id), Origin: gtd.OriginUser,
		Project: project, Position: pos, Created: t0.Add(time.Duration(pos) * time.Minute), Updated: t0})
}

func positions(t *testing.T, r *projectRig, ids ...gtd.ID) map[gtd.ID]int {
	t.Helper()
	out := map[gtd.ID]int{}
	for _, id := range ids {
		out[id] = r.entry(t, id).Position
	}
	return out
}

func TestProjectFromEntry(t *testing.T) {
	r := newProjectRig()
	e := r.put(t, gtd.Entry{ID: "e-cap", Kind: gtd.KindCapture, State: gtd.Inbox, Title: "Write the RFC", Area: "OSS", Origin: gtd.OriginUser})
	res := r.do(t, e.ID, gtd.VerbProject, map[string]string{"title": "Ship the v2 API"})
	if res.Entry.ID != e.ID || res.Entry.Project == "" || res.Entry.Position != 1 || res.Entry.State != gtd.Inbox {
		t.Fatalf("attached entry %+v", res.Entry)
	}
	p := r.entry(t, res.Entry.Project)
	if p.Kind != gtd.KindProject || p.State != gtd.Next || p.Title != "Ship the v2 API" || p.Slug != "ship-the-v2-api" || p.Area != "OSS" {
		t.Fatalf("project %+v", p)
	}
	if !slices.Equal(r.dossiers.opened, []string{"ship-the-v2-api"}) {
		t.Errorf("dossiers opened %v", r.dossiers.opened)
	}

	other := r.put(t, gtd.Entry{ID: "e-cap2", Kind: gtd.KindAction, State: gtd.Next, Title: "x", Origin: gtd.OriginDerived})
	res = r.do(t, other.ID, gtd.VerbProject, map[string]string{"title": "Ship the v2 API!"})
	if p2 := r.entry(t, res.Entry.Project); p2.Slug != "ship-the-v2-api-2" || p2.ID == p.ID {
		t.Errorf("a second project of the same name: %+v", p2)
	}

	if _, err := r.svc.Do(context.Background(), p.ID, coregtd.Request{Verb: gtd.VerbProject, Args: map[string]string{"title": "inner"}}); !errors.Is(err, coregtd.ErrVerb) {
		t.Errorf("a project put in a project: %v, want ErrVerb", err)
	}
	if _, err := r.svc.Do(context.Background(), e.ID, coregtd.Request{Verb: gtd.VerbProject}); !errors.Is(err, coregtd.ErrArgs) {
		t.Errorf("project with neither id nor title: %v, want ErrArgs", err)
	}

	r.dossiers.fail = errors.New("disk full")
	third := r.put(t, gtd.Entry{ID: "e-cap3", Kind: gtd.KindCapture, State: gtd.Inbox, Title: "y", Origin: gtd.OriginUser})
	res = r.do(t, third.ID, gtd.VerbProject, map[string]string{"title": "Broken disk"})
	if res.Entry.Project == "" || len(res.Lines) == 0 {
		t.Errorf("a failed dossier still files the project and says so: %+v", res)
	}
}

func TestAttachAndReorder(t *testing.T) {
	r := newProjectRig()
	r.project(t, "p", nil)
	r.action(t, "a", "p", 1, gtd.Next)
	r.action(t, "b", "p", 2, gtd.Next)
	r.action(t, "gone", "p", 7, gtd.Trash)
	c := r.put(t, gtd.Entry{ID: "c", Kind: gtd.KindCapture, State: gtd.Inbox, Title: "c", Origin: gtd.OriginUser})
	d := r.put(t, gtd.Entry{ID: "d", Kind: gtd.KindCapture, State: gtd.Next, Title: "d", Origin: gtd.OriginUser})

	if got := r.do(t, c.ID, gtd.VerbProject, map[string]string{"id": "p"}).Entry; got.Project != "p" || got.Position != 3 {
		t.Fatalf("attached at %d of %q, want 3 of p", got.Position, got.Project)
	}
	r.do(t, d.ID, gtd.VerbProject, map[string]string{"id": "p", "position": "2"})
	want := map[gtd.ID]int{"a": 1, "d": 2, "b": 3, "c": 4}
	if got := positions(t, r, "a", "b", "c", "d"); !equalPositions(got, want) {
		t.Fatalf("positions %v, want %v", got, want)
	}
	r.do(t, c.ID, gtd.VerbProject, map[string]string{"id": "p", "position": "1"})
	want = map[gtd.ID]int{"c": 1, "a": 2, "d": 3, "b": 4}
	if got := positions(t, r, "a", "b", "c", "d"); !equalPositions(got, want) {
		t.Fatalf("after moving c first: %v, want %v", got, want)
	}
	for _, bad := range []map[string]string{{"id": "p", "position": "0"}, {"id": "p", "position": "x"}, {"id": "a"}, {"id": "nope"}} {
		if _, err := r.svc.Do(context.Background(), d.ID, coregtd.Request{Verb: gtd.VerbProject, Args: bad}); err == nil {
			t.Errorf("project %v: no error", bad)
		}
	}
}

func equalPositions(a, b map[gtd.ID]int) bool {
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
	r.action(t, "a", "p", 1, gtd.Next)
	r.action(t, "b", "p", 2, gtd.Next)
	next := func() ([]gtd.ID, int) {
		t.Helper()
		es, err := r.svc.Entries(ctx, gtd.Query{View: gtd.ViewNext})
		if err != nil {
			t.Fatal(err)
		}
		c, err := r.svc.Counts(ctx, "")
		if err != nil {
			t.Fatal(err)
		}
		var ids []gtd.ID
		for _, e := range es {
			if e.Kind != gtd.KindProject {
				ids = append(ids, e.ID)
			}
		}
		return ids, c.Views[gtd.ViewNext]
	}
	ids, n := next()
	if !slices.Equal(ids, []gtd.ID{"a", "b"}) || n != 3 {
		t.Fatalf("parallel: next %v, count %d", ids, n)
	}
	if got := r.do(t, "p", gtd.VerbSequential, nil).Entry; !got.Sequential {
		t.Fatal("sequential did not toggle on")
	}
	if ids, n = next(); !slices.Equal(ids, []gtd.ID{"a"}) || n != 2 {
		t.Fatalf("sequential: next %v, count %d", ids, n)
	}
	r.do(t, "p", gtd.VerbSequential, map[string]string{"set": "true"})
	if !r.entry(t, "p").Sequential {
		t.Fatal("set true turned it off")
	}
	r.do(t, "p", gtd.VerbSequential, nil)
	if ids, n = next(); !slices.Equal(ids, []gtd.ID{"a", "b"}) || n != 3 {
		t.Fatalf("back to parallel: next %v, count %d", ids, n)
	}
	if _, err := r.svc.Do(ctx, "a", coregtd.Request{Verb: gtd.VerbSequential}); !errors.Is(err, coregtd.ErrVerb) {
		t.Errorf("sequential on an action: %v, want ErrVerb", err)
	}
}

func TestMarkReviewed(t *testing.T) {
	r := newProjectRig()
	ctx := context.Background()
	r.project(t, "p", nil)
	r.clock.t = t0.Add(3 * time.Hour)
	if got := r.do(t, "p", gtd.VerbReviewed, nil).Entry; !got.ReviewedAt.Equal(r.clock.t) || got.ReviewEvery != 0 {
		t.Fatalf("reviewed %+v", got)
	}
	r.do(t, "p", gtd.VerbReviewed, map[string]string{"every": "14d"})
	if got := r.entry(t, "p"); got.ReviewEvery != 14*24*time.Hour {
		t.Errorf("every 14d set %v", got.ReviewEvery)
	}
	r.do(t, "p", gtd.VerbReviewed, map[string]string{"every": "36h"})
	if got := r.entry(t, "p"); got.ReviewEvery != 36*time.Hour {
		t.Errorf("every 36h set %v", got.ReviewEvery)
	}
	for _, bad := range []string{"0d", "-1h", "soon"} {
		if _, err := r.svc.Do(ctx, "p", coregtd.Request{Verb: gtd.VerbReviewed, Args: map[string]string{"every": bad}}); !errors.Is(err, coregtd.ErrArgs) {
			t.Errorf("every %q: %v, want ErrArgs", bad, err)
		}
	}
	r.action(t, "a", "p", 1, gtd.Next)
	if _, err := r.svc.Do(ctx, "a", coregtd.Request{Verb: gtd.VerbReviewed}); !errors.Is(err, coregtd.ErrVerb) {
		t.Errorf("reviewed on an action: %v, want ErrVerb", err)
	}
}

func TestProjectWithoutNextAction(t *testing.T) {
	r := newProjectRig()
	ctx := context.Background()
	r.project(t, "p", nil)
	r.action(t, "a", "p", 1, gtd.Next)
	r.action(t, "b", "p", 2, gtd.Next)
	r.project(t, "q", func(e *gtd.Entry) { e.ReviewedAt = t0 })
	r.action(t, "c", "q", 1, gtd.Next)
	r.clock.t = t0.Add(time.Hour)
	review := func() []gtd.ID {
		t.Helper()
		es, err := r.svc.Entries(ctx, gtd.Query{View: gtd.ViewReview})
		if err != nil {
			t.Fatal(err)
		}
		var ids []gtd.ID
		for _, e := range es {
			ids = append(ids, e.ID)
		}
		return ids
	}
	if got := review(); len(got) != 0 {
		t.Fatalf("projects with next actions under review: %v", got)
	}
	r.do(t, "a", gtd.VerbDone, nil)
	r.do(t, "b", gtd.VerbDone, nil)
	if got := review(); !slices.Equal(got, []gtd.ID{"p"}) {
		t.Fatalf("review lists %v, want p", got)
	}
	if p := r.entry(t, "p"); p.State != gtd.Next {
		t.Errorf("a project with every action done was moved to %s", p.State)
	}
	rv, err := r.svc.Review(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st := rv.Steps[slices.IndexFunc(rv.Steps, func(s coregtd.ReviewStep) bool { return s.ID == "projects" })]; st.Count != 1 || st.Done {
		t.Errorf("projects step %+v", st)
	}
	r.clock.t = t0.Add(8 * 24 * time.Hour)
	if got := review(); !slices.Equal(got, []gtd.ID{"p", "q"}) {
		t.Errorf("past the interval: %v, want p and q", got)
	}
}

func TestDoneProjectResolvesItsDossier(t *testing.T) {
	r := newProjectRig()
	r.project(t, "p", func(e *gtd.Entry) { e.Slug = "ship-it" })
	r.project(t, "q", func(e *gtd.Entry) { e.Slug = "drop-it" })
	r.action(t, "a", "p", 1, gtd.Next)
	r.clock.t = t0.Add(time.Hour)
	r.do(t, "a", gtd.VerbDone, nil)
	if len(r.dossiers.resolved) != 0 {
		t.Fatalf("an action's done resolved %v", r.dossiers.resolved)
	}
	if got := r.do(t, "p", gtd.VerbDone, nil).Entry; got.State != gtd.Done {
		t.Fatalf("project state %s", got.State)
	}
	r.do(t, "q", gtd.VerbDismiss, nil)
	want := map[string]time.Time{"ship-it": r.clock.t, "drop-it": r.clock.t}
	for slug, at := range want {
		if got, ok := r.dossiers.resolved[slug]; !ok || !got.Equal(at) {
			t.Errorf("dossier %s resolved %v, %v; want at %v", slug, got, ok, at)
		}
	}
	if _, err := r.svc.Do(context.Background(), "p", coregtd.Request{Verb: gtd.VerbDone}); !errors.Is(err, coregtd.ErrVerb) {
		t.Errorf("done twice: %v, want ErrVerb", err)
	}
}

func TestProjectsViewCounts(t *testing.T) {
	r := newProjectRig()
	ctx := context.Background()
	r.project(t, "p1", func(e *gtd.Entry) { e.Area = "OSS" })
	r.project(t, "p2", func(e *gtd.Entry) { e.Area = "OSS"; e.State = gtd.Someday })
	r.project(t, "p3", func(e *gtd.Entry) { e.Area = "Hiring" })
	r.project(t, "p4", func(e *gtd.Entry) { e.Area = "OSS"; e.State = gtd.Done })
	for area, want := range map[string][]gtd.ID{"OSS": {"p1", "p2"}, "Hiring": {"p3"}, "": {"p1", "p2", "p3"}} {
		es, err := r.svc.Entries(ctx, gtd.Query{View: gtd.ViewProjects, Area: area})
		if err != nil {
			t.Fatal(err)
		}
		var ids []gtd.ID
		for _, e := range es {
			ids = append(ids, e.ID)
		}
		slices.Sort(ids)
		c, err := r.svc.Counts(ctx, area)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(ids, want) || c.Views[gtd.ViewProjects] != len(want) {
			t.Errorf("area %q: %v counted %d, want %v", area, ids, c.Views[gtd.ViewProjects], want)
		}
	}
}

func TestProjectStatusOverTheOpenSet(t *testing.T) {
	r := newProjectRig()
	r.project(t, "p", nil)
	r.action(t, "a", "p", 1, gtd.Waiting)
	r.action(t, "b", "p", 2, gtd.Next)
	r.project(t, "old", func(e *gtd.Entry) { e.Created = t0.Add(-8 * 24 * time.Hour) })
	st, err := r.svc.ProjectsStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st["p"] != (coregtd.ProjectState{ProjectStatus: gtd.ProjectStatus{Next: "b", NextTitle: "Action b", Open: 2}}) {
		t.Errorf("status %+v", st["p"])
	}
	if !st["old"].Due || len(st) != 2 {
		t.Errorf("an old project is not due: %+v", st)
	}
}
