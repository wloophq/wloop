package github

import (
	"context"
	"errors"
	"sync"

	sdk "github.com/wloophq/wloop/pkg/plugin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/github/plan"
	"github.com/wloophq/wloop/plugins/github/pr"
)

// PlanData is the planner's part of a pull request's snapshot data: where
// its rounds rank and what the board reads of its workspaces.
type PlanData struct {
	Rank  plan.Rank  `json:"rank"`
	Board plan.Board `json:"board"`
}

// listings keeps the host's workspace listing between its changes, which
// Notify(workspaces) and each fast job read again.
type listings struct {
	mu   sync.Mutex
	read bool
	l    src.Listing
	bad  bool
}

func (l *listings) forget() {
	l.mu.Lock()
	l.read = false
	l.mu.Unlock()
}

// listing is the host's workspace listing; unlisted says it could not be
// read.
func (r *running) listing(ctx context.Context) (src.Listing, bool) {
	l := &r.lists
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.read {
		return l.l, l.bad
	}
	if r.host.Reader == nil {
		return src.Listing{}, true
	}
	got, err := r.host.Reader.Workspaces(ctx, "")
	if err != nil {
		if !errors.Is(err, src.ErrUnsupported) {
			logf("workspaces: %v", err)
		}
		return src.Listing{}, true
	}
	l.l, l.bad, l.read = got, false, true
	return got, false
}

// planInputs are what the planner reads of refs: every stored pull request,
// their details and the host's round state of each, with events and books
// read already.
func (r *running) planInputs(ctx context.Context, refs []pr.Ref, events map[pr.Ref][]pr.Event, books map[pr.Ref]src.Book) (plan.Inputs, error) {
	viewer, err := r.db.Viewer(ctx)
	if err != nil {
		return plan.Inputs{}, err
	}
	rows, err := r.db.ListPRs(ctx, false)
	if err != nil {
		return plan.Inputs{}, err
	}
	details, err := r.db.Details(ctx, refs)
	if err != nil {
		return plan.Inputs{}, err
	}
	adopted, err := r.db.Adopted(ctx)
	if err != nil {
		return plan.Inputs{}, err
	}
	r.cfgMu.Lock()
	cfg := r.cfg.Plan
	r.cfgMu.Unlock()
	in := plan.Inputs{Now: r.now(), Viewer: viewer, Rows: rows, Details: details, Adopted: adopted, Books: books, Events: events,
		Rounds: map[pr.Ref]src.RoundState{}, Git: r.git, Cfg: cfg}
	in.Listing, in.Unlisted = r.listing(ctx)
	if r.host.Reader != nil {
		for _, ref := range refs {
			st, err := r.host.Reader.RoundState(ctx, ref.Unit())
			if errors.Is(err, src.ErrUnsupported) {
				break
			}
			if err != nil {
				return plan.Inputs{}, err
			}
			in.Rounds[ref] = st
		}
	}
	return in, nil
}

// Plan is the rounds the planner plans on u over st, the host's round state
// of u: each with the marks the host writes when it sends or skips it.
func (p *Plugin) Plan(ctx context.Context, u sdk.Unit, st src.RoundState) (src.Plan, error) {
	r, err := p.running()
	if err != nil {
		return src.Plan{}, err
	}
	ref, err := refOf(u)
	if err != nil {
		return src.Plan{}, err
	}
	ri, err := r.rowInputs(ctx, []pr.Ref{ref})
	if err != nil {
		return src.Plan{}, err
	}
	in, err := r.planInputs(ctx, nil, ri.events, ri.books)
	if err != nil {
		return src.Plan{}, err
	}
	if in.Details, err = r.db.Details(ctx, []pr.Ref{ref}); err != nil {
		return src.Plan{}, err
	}
	in.Rounds = map[pr.Ref]src.RoundState{ref: st}
	pl := plan.New(ctx, in)
	res, err := pl.Derive(ref)
	if err != nil {
		return src.Plan{}, err
	}
	return pl.Plan(ref, res), nil
}

// settle writes what planning left behind: each pull request's marks, then
// its events.
func (r *running) settle(ctx context.Context, s map[pr.Ref]plan.Settlement) error {
	var errs []error
	var evs []src.Event
	for ref, x := range s {
		if len(x.Marks) > 0 && r.host.Recorder != nil {
			if err := r.host.Recorder.SetMarks(ctx, ref.Unit(), x.Marks); err != nil && !errors.Is(err, src.ErrUnsupported) {
				errs = append(errs, err)
			}
		}
		for _, e := range x.Events {
			evs = append(evs, src.Event{Unit: ref.Unit(), Kind: e.Kind, Key: e.Key, At: e.At, Data: []byte(e.Data)})
		}
	}
	if len(evs) > 0 && r.host.Recorder != nil {
		if err := r.host.Recorder.RecordEvents(ctx, evs); err != nil && !errors.Is(err, src.ErrUnsupported) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Placement is where a pull request's work runs: the plugin names none, and
// the host's workspaces place it.
func (p *Plugin) Placement(context.Context, sdk.Unit, string) (src.Placements, error) {
	return src.Placements{}, nil
}
