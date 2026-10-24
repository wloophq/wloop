package gtd

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/domain/gtd"
)

// ProjectStore keeps what the projects need beyond their entries: the slugs taken and the Review's runs.
type ProjectStore interface {
	// SlugTaken says whether a project, done or not, holds slug.
	SlugTaken(ctx context.Context, slug string) (bool, error)
	// CurrentRun is the Review run with a step still open.
	CurrentRun(ctx context.Context) (gtd.ReviewRun, bool, error)
	PutRun(ctx context.Context, r gtd.ReviewRun) error
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
var actionKinds = []gtd.Kind{gtd.KindAction, gtd.KindWaiting, gtd.KindClose, gtd.KindMessage, gtd.KindCapture, gtd.KindReference}

// endVerbs end a project, so its dossier's lifecycle starts.
var endVerbs = []gtd.Verb{gtd.VerbDone, gtd.VerbDismiss, gtd.VerbReference}

func init() {
	for _, v := range []gtd.Verb{gtd.VerbReviewed, gtd.VerbSequential} {
		moves[v] = struct {
			from []gtd.State
			to   gtd.State
		}{}
	}
}

type projects struct {
	s *Service
	d ProjectDeps
}

// ServeProjects runs the project verbs on s (project with its position, sequential, reviewed), resolves an
// ending project's dossier, and serves the review view and the Review.
func (s *Service) ServeProjects(d ProjectDeps) {
	p := &projects{s: s, d: d}
	for _, k := range actionKinds {
		s.Register(k, gtd.VerbProject, p.attach)
	}
	s.Register(gtd.KindProject, gtd.VerbProject, func(ctx context.Context, e gtd.Entry, req Request) (Result, error) {
		return Result{}, fmt.Errorf("%w: a project cannot join a project", ErrVerb)
	})
	s.Register(gtd.KindProject, gtd.VerbSequential, p.sequential)
	s.Register(gtd.KindProject, gtd.VerbReviewed, p.reviewed)
	for _, v := range endVerbs {
		s.Register(gtd.KindProject, v, p.end)
	}
	s.RegisterView(gtd.ViewReview, "", &reviewSource{s: s, store: d.Store})
}

// Serves says whether v is served: a stored view, the outbox, or a view a ViewSource serves.
func (s *Service) Serves(v gtd.View) bool {
	if _, ok := s.view(v); ok || v == gtd.ViewOutbox {
		return true
	}
	return slices.Contains(gtd.StoredViews, v)
}

// ProjectState is a project's status at the service's now.
type ProjectState struct {
	gtd.ProjectStatus
	Due bool // past its review interval
}

// ProjectsStatus is each live project's next action, open actions and review mark, over one read of the open set.
func (s *Service) ProjectsStatus(ctx context.Context) (map[gtd.ID]ProjectState, error) {
	open, err := s.d.Store.Entries(ctx, gtd.Query{View: gtd.ViewOpen})
	if err != nil {
		return nil, err
	}
	st, now := gtd.ProjectsOf(open), s.now()
	out := make(map[gtd.ID]ProjectState, len(st))
	for _, e := range open {
		if e.Kind == gtd.KindProject {
			out[e.ID] = ProjectState{ProjectStatus: st[e.ID], Due: e.Open() && e.ReviewDue(now)}
		}
	}
	return out, nil
}

// locked reads e again under the service's lock and runs f on it, applying the changes it returns.
func (p *projects) locked(ctx context.Context, id gtd.ID, f func(e gtd.Entry, now time.Time) ([]gtd.Change, error)) (gtd.Entry, error) {
	s := p.s
	s.mu.Lock()
	defer s.mu.Unlock()
	e, err := s.d.Store.Entry(ctx, id)
	if err != nil {
		return gtd.Entry{}, err
	}
	cs, err := f(e, s.now())
	if err != nil {
		return gtd.Entry{}, err
	}
	if err := s.d.Store.Apply(ctx, cs); err != nil {
		return gtd.Entry{}, err
	}
	for _, c := range cs {
		if c.Entry.ID == id {
			e = c.Entry
		}
	}
	return e, nil
}

// attach puts e in the project args["id"] names, or in a new one titled args["title"]: after its last action, or
// at args["position"], shifting the later ones.
func (p *projects) attach(ctx context.Context, e gtd.Entry, req Request) (Result, error) {
	var created gtd.Entry
	got, err := p.locked(ctx, e.ID, func(e gtd.Entry, now time.Time) ([]gtd.Change, error) {
		if err := accepts(e, gtd.VerbProject); err != nil {
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
		var cs []gtd.Change
		var proj gtd.Entry
		switch id, title := req.Args["id"], strings.TrimSpace(req.Args["title"]); {
		case id != "":
			got, err := p.s.d.Store.Entry(ctx, gtd.ID(id))
			if err != nil {
				return nil, err
			}
			if got.Kind != gtd.KindProject || !got.Live() {
				return nil, fmt.Errorf("%w: %s is not a live project", ErrArgs, id)
			}
			proj = got
		case title != "":
			slug, err := p.slug(ctx, title)
			if err != nil {
				return nil, err
			}
			proj = gtd.Entry{ID: gtd.NewID(), Kind: gtd.KindProject, State: gtd.Next, Title: title, Slug: slug,
				Sequential: req.Args["sequential"] == "true", Area: e.Area, Origin: gtd.OriginUser, Created: now, Updated: now}
			created = proj
			cs = append(cs, gtd.Change{Op: gtd.OpCreate, Entry: proj, Why: "project"})
		default:
			return nil, fmt.Errorf("%w: project needs id or title", ErrArgs)
		}
		open, err := p.s.d.Store.Entries(ctx, gtd.Query{View: gtd.ViewOpen})
		if err != nil {
			return nil, err
		}
		var others []gtd.Entry
		last := 0
		for _, o := range open {
			if o.Project == proj.ID && o.ID != e.ID {
				others = append(others, o)
				last = max(last, o.Position)
			}
		}
		e.Project, e.Updated = proj.ID, now
		if pos == 0 {
			e.Position = last + 1
			return append(cs, gtd.Change{Op: gtd.OpUpdate, Entry: e, Why: "project"}), nil
		}
		for _, a := range gtd.Place(others, e, pos) {
			if a.ID != e.ID {
				a.Updated = now
			}
			cs = append(cs, gtd.Change{Op: gtd.OpUpdate, Entry: a, Why: "project position"})
		}
		return cs, nil
	})
	if err != nil {
		return Result{}, err
	}
	res := Result{Entry: got}
	if created.ID != "" && p.d.Dossiers != nil {
		if err := p.d.Dossiers.Open(ctx, created.Slug); err != nil {
			res.Lines = append(res.Lines, fmt.Sprintf("the dossier of project %s was not created: %v", created.Slug, err))
		}
	}
	return res, nil
}

// slug is title's slug, numbered from 2 while another project holds it.
func (p *projects) slug(ctx context.Context, title string) (string, error) {
	base := gtd.Slug(title)
	for n := 1; ; n++ {
		slug := gtd.NumberedSlug(base, n)
		taken, err := p.d.Store.SlugTaken(ctx, slug)
		if err != nil || !taken {
			return slug, err
		}
	}
}

// liveProject refuses a project that is done or trashed, naming the verb.
func liveProject(e gtd.Entry, v gtd.Verb) error {
	if !e.Live() {
		return fmt.Errorf("%w: %s on a %s project", ErrVerb, v, e.State)
	}
	return nil
}

// sequential sets args["set"] ("true" or "false"), or toggles.
func (p *projects) sequential(ctx context.Context, e gtd.Entry, req Request) (Result, error) {
	got, err := p.locked(ctx, e.ID, func(e gtd.Entry, now time.Time) ([]gtd.Change, error) {
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
		return []gtd.Change{{Op: gtd.OpUpdate, Entry: e, Why: "sequential"}}, nil
	})
	return Result{Entry: got}, err
}

// reviewed marks the project reviewed now; args["every"] ("14d" or a Go duration) sets its interval.
func (p *projects) reviewed(ctx context.Context, e gtd.Entry, req Request) (Result, error) {
	got, err := p.locked(ctx, e.ID, func(e gtd.Entry, now time.Time) ([]gtd.Change, error) {
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
		return []gtd.Change{{Op: gtd.OpUpdate, Entry: e, Why: "reviewed"}}, nil
	})
	return Result{Entry: got}, err
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
func (p *projects) end(ctx context.Context, e gtd.Entry, req Request) (Result, error) {
	var was gtd.Entry
	got, err := p.locked(ctx, e.ID, func(e gtd.Entry, now time.Time) ([]gtd.Change, error) {
		was = e
		next, err := transition(e, req, now)
		if err != nil {
			return nil, err
		}
		return []gtd.Change{{Op: gtd.OpUpdate, Entry: next, Why: string(req.Verb)}}, nil
	})
	if err != nil {
		return Result{}, err
	}
	res := Result{Entry: got}
	if was.Open() && !got.Open() && got.Slug != "" && p.d.Dossiers != nil {
		if err := p.d.Dossiers.Resolved(ctx, got.Slug, got.Updated); err != nil {
			res.Lines = append(res.Lines, fmt.Sprintf("the dossier of project %s was not resolved: %v", got.Slug, err))
		}
	}
	return res, nil
}
