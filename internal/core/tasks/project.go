package tasks

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/domain/task"
)

// ProjectStore keeps what the projects need beyond their entries: the slugs taken and the Review's runs.
type ProjectStore interface {
	// SlugTaken says whether a project, done or not, holds slug.
	SlugTaken(ctx context.Context, slug string) (bool, error)
	// CurrentRun is the Review run with a step still open.
	CurrentRun(ctx context.Context) (task.ReviewRun, bool, error)
	PutRun(ctx context.Context, r task.ReviewRun) error
}

// ProjectDossiers opens a new project's dossier and starts its lifecycle once the project ends (#dossier).
type ProjectDossiers interface {
	Open(ctx context.Context, slug string) error
	Resolved(ctx context.Context, slug string, at time.Time) error
}

type ProjectDeps struct {
	Store    ProjectStore
	Dossiers ProjectDossiers // nil keeps no dossiers
}

// actionKinds are the kinds of entry the project verb attaches to a project.
var actionKinds = []task.Kind{task.KindAction, task.KindWaiting, task.KindClose, task.KindMessage, task.KindCapture, task.KindReference}

// endVerbs end a project, so its dossier's lifecycle starts.
var endVerbs = []task.Verb{task.VerbDone, task.VerbDismiss, task.VerbReference}

type projects struct {
	s *Service
	d ProjectDeps
}

// ServeProjects runs the project verbs on s (project with its position, sequential, reviewed), resolves an
// ending project's dossier, and serves the review view and the Review.
func (s *Service) ServeProjects(d ProjectDeps) {
	p := &projects{s: s, d: d}
	for _, k := range actionKinds {
		s.Register(k, task.VerbProject, p.attach)
	}
	s.Register(task.KindProject, task.VerbProject, func(ctx context.Context, e task.Task, req Request) (Result, error) {
		return Result{}, fmt.Errorf("%w: a project cannot join a project", ErrVerb)
	})
	s.Register(task.KindProject, task.VerbSequential, p.sequential)
	s.Register(task.KindProject, task.VerbReviewed, p.reviewed)
	for _, v := range endVerbs {
		s.Register(task.KindProject, v, p.end)
	}
	s.RegisterView(task.ViewReview, "", &reviewSource{s: s, store: d.Store})
}

// Serves says whether v is served: a stored view, the outbox, or a view a ViewSource serves.
func (s *Service) Serves(v task.View) bool {
	if _, ok := s.view(v); ok || v == task.ViewOutbox {
		return true
	}
	return slices.Contains(task.StoredViews, v)
}

// ProjectState is a project's status at the service's now.
type ProjectState struct {
	task.ProjectStatus
	Due bool // past its review interval
}

// ProjectsStatus is each live project's next action, open actions and review mark, over one read of the open set.
func (s *Service) ProjectsStatus(ctx context.Context) (map[task.ID]ProjectState, error) {
	open, err := s.d.Store.Tasks(ctx, task.Query{View: task.ViewOpen})
	if err != nil {
		return nil, err
	}
	st, now := task.ProjectsOf(open), s.now()
	out := make(map[task.ID]ProjectState, len(st))
	for _, e := range open {
		if e.Kind == task.KindProject {
			out[e.ID] = ProjectState{ProjectStatus: st[e.ID], Due: e.Open() && e.ReviewDue(now)}
		}
	}
	return out, nil
}

// Children are the open children of every parent, in sibling order: by Position, then id.
func (s *Service) Children(ctx context.Context) (map[task.ID][]task.ID, error) {
	open, err := s.d.Store.Tasks(ctx, task.Query{View: task.ViewOpen})
	if err != nil {
		return nil, err
	}
	slices.SortStableFunc(open, func(a, b task.Task) int { return cmp.Or(cmp.Compare(a.Position, b.Position), cmp.Compare(a.ID, b.ID)) })
	out := map[task.ID][]task.ID{}
	for _, e := range open {
		if e.Parent != "" {
			out[e.Parent] = append(out[e.Parent], e.ID)
		}
	}
	return out, nil
}

// locked reads e again under the service's lock and runs f on it, applying the changes it returns with the
// event of req's verb.
func (p *projects) locked(ctx context.Context, id task.ID, req Request, f func(e task.Task, now time.Time) ([]task.Change, error)) (task.Task, error) {
	s := p.s
	s.mu.Lock()
	defer s.mu.Unlock()
	e, err := s.d.Store.Task(ctx, id)
	if err != nil {
		return task.Task{}, err
	}
	now := s.now()
	cs, err := f(e, now)
	if err != nil {
		return task.Task{}, err
	}
	if err := s.d.Store.Apply(ctx, cs, verbEvents(e, req, now)...); err != nil {
		return task.Task{}, err
	}
	for _, c := range cs {
		if c.Task.ID == id {
			e = c.Task
		}
	}
	return e, nil
}

// attach puts e in the project args["id"] names, or in a new one titled args["title"]: after its last action, or
// at args["position"], shifting the later ones.
func (p *projects) attach(ctx context.Context, e task.Task, req Request) (Result, error) {
	var created task.Task
	got, err := p.locked(ctx, e.ID, req, func(e task.Task, now time.Time) ([]task.Change, error) {
		if err := p.s.accepts(e, task.VerbProject); err != nil {
			return nil, err
		}
		pos := 0
		if raw := req.Args["position"]; raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 1 {
				return nil, fmt.Errorf("%w: position %q is not a number from 1", ErrArgs, raw)
			}
			pos = n
		}
		var cs []task.Change
		var proj task.Task
		switch id, title := req.Args["id"], strings.TrimSpace(req.Args["title"]); {
		case id != "":
			got, err := p.s.d.Store.Task(ctx, task.ID(id))
			if err != nil {
				return nil, err
			}
			if got.Kind != task.KindProject || !got.Open() {
				return nil, fmt.Errorf("%w: %s is not a live project", ErrArgs, id)
			}
			proj = got
		case title != "":
			slug, err := p.slug(ctx, title)
			if err != nil {
				return nil, err
			}
			proj = task.Task{ID: task.NewID(), Kind: task.KindProject, Title: title, Slug: slug,
				Sequential: req.Args["sequential"] == "true", Origin: task.OriginUser, Created: now, Updated: now}
			if err := proj.MoveTo(task.CategoryReady, p.s.d.States); err != nil {
				return nil, err
			}
			proj.SetProp(task.PropArea, e.Prop(task.PropArea), task.WriterPerson, now)
			created = proj
			cs = append(cs, task.Change{Op: task.OpCreate, Task: proj, Why: "project"})
		default:
			return nil, fmt.Errorf("%w: project needs id or title", ErrArgs)
		}
		open, err := p.s.d.Store.Tasks(ctx, task.Query{View: task.ViewOpen})
		if err != nil {
			return nil, err
		}
		var others []task.Task
		last := 0
		for _, o := range open {
			if o.Parent == proj.ID && o.ID != e.ID {
				others = append(others, o)
				last = max(last, o.Position)
			}
		}
		e.Parent, e.Updated = proj.ID, now
		if pos == 0 {
			e.Position = last + 1
			return append(cs, task.Change{Op: task.OpUpdate, Task: e, Why: "project"}), nil
		}
		for _, a := range task.Place(others, e, pos) {
			if a.ID != e.ID {
				a.Updated = now
			}
			cs = append(cs, task.Change{Op: task.OpUpdate, Task: a, Why: "project position"})
		}
		return cs, nil
	})
	if err != nil {
		return Result{}, err
	}
	res := Result{Task: got}
	if created.ID != "" && p.d.Dossiers != nil {
		if err := p.d.Dossiers.Open(ctx, created.Slug); err != nil {
			res.Lines = append(res.Lines, fmt.Sprintf("the dossier of project %s was not created: %v", created.Slug, err))
		}
	}
	return res, nil
}

// slug is title's slug, numbered from 2 while another project holds it.
func (p *projects) slug(ctx context.Context, title string) (string, error) {
	base := task.Slug(title)
	for n := 1; ; n++ {
		slug := task.NumberedSlug(base, n)
		taken, err := p.d.Store.SlugTaken(ctx, slug)
		if err != nil || !taken {
			return slug, err
		}
	}
}

// liveProject refuses a project that is done or trashed, naming the verb.
func liveProject(e task.Task, v task.Verb) error {
	if !e.Open() {
		return fmt.Errorf("%w: %s on a %s project", ErrVerb, v, e.State)
	}
	return nil
}

// sequential sets args["set"] ("true" or "false"), or toggles.
func (p *projects) sequential(ctx context.Context, e task.Task, req Request) (Result, error) {
	got, err := p.locked(ctx, e.ID, req, func(e task.Task, now time.Time) ([]task.Change, error) {
		if err := liveProject(e, req.Verb); err != nil {
			return nil, err
		}
		switch set := req.Args["set"]; set {
		case "":
			e.Sequential = !e.Sequential
		case "true", "false":
			e.Sequential = set == "true"
		default:
			return nil, fmt.Errorf("%w: sequential set %q is neither true nor false", ErrArgs, set)
		}
		e.Updated = now
		return []task.Change{{Op: task.OpUpdate, Task: e, Why: "sequential"}}, nil
	})
	return Result{Task: got}, err
}

// reviewed marks the project reviewed now; args["every"] ("14d" or a Go duration) sets its interval.
func (p *projects) reviewed(ctx context.Context, e task.Task, req Request) (Result, error) {
	got, err := p.locked(ctx, e.ID, req, func(e task.Task, now time.Time) ([]task.Change, error) {
		if err := liveProject(e, req.Verb); err != nil {
			return nil, err
		}
		if raw := req.Args["every"]; raw != "" {
			d, err := interval(raw)
			if err != nil {
				return nil, err
			}
			e.ReviewEvery = d
		}
		e.ReviewedAt, e.Updated = now, now
		return []task.Change{{Op: task.OpUpdate, Task: e, Why: "reviewed"}}, nil
	})
	return Result{Task: got}, err
}

func interval(raw string) (time.Duration, error) {
	var d time.Duration
	var err error
	if days, ok := strings.CutSuffix(raw, "d"); ok {
		var n int
		n, err = strconv.Atoi(days)
		d = time.Duration(n) * 24 * time.Hour
	} else {
		d, err = time.ParseDuration(raw)
	}
	if d = d.Truncate(time.Second); err != nil || d <= 0 {
		return 0, fmt.Errorf("%w: every %q is neither a count of days (14d) nor a positive duration", ErrArgs, raw)
	}
	return d, nil
}

// end runs a verb that ends the project as the state change, then starts its dossier's lifecycle.
func (p *projects) end(ctx context.Context, e task.Task, req Request) (Result, error) {
	var was task.Task
	got, err := p.locked(ctx, e.ID, req, func(e task.Task, now time.Time) ([]task.Change, error) {
		was = e
		next, err := p.s.transition(e, req, now)
		if err != nil {
			return nil, err
		}
		return []task.Change{{Op: task.OpUpdate, Task: next, Why: string(req.Verb)}}, nil
	})
	if err != nil {
		return Result{}, err
	}
	res := Result{Task: got}
	if was.Open() && !got.Open() && got.Slug != "" && p.d.Dossiers != nil {
		if err := p.d.Dossiers.Resolved(ctx, got.Slug, got.Updated); err != nil {
			res.Lines = append(res.Lines, fmt.Sprintf("the dossier of project %s was not resolved: %v", got.Slug, err))
		}
	}
	return res, nil
}
