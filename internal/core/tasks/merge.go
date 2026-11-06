package tasks

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/wloophq/wloop/internal/domain/merge"
	"github.com/wloophq/wloop/internal/domain/unit"

	"github.com/wloophq/wloop/internal/domain/task"
)

// The handlers that fold a task into another, give it back, and link two tasks: each takes the other task in
// the argument task.ArgTask.
const (
	HandlerMerge    = "merge"
	HandlerSplit    = "split"
	HandlerRelate   = "relate"
	HandlerUnrelate = "unrelate"
)

func (s *Service) serveMerges() {
	s.Register(EveryKind, HandlerMerge, s.merge)
	s.Register(EveryKind, HandlerSplit, s.split)
	s.Register(EveryKind, HandlerRelate, s.relate)
	s.Register(EveryKind, HandlerUnrelate, s.unrelate)
}

// merge folds e and the open task req.Args[task.ArgTask] names into one, under the service's lock: the
// one Survives holds on alone stays, else the older, and the other is merged into it (merge.Fold) through
// the Store's Merger; then the gates acting for it, and those left acting for a task merged earlier into
// either (GateSweeper.ActingFor), are superseded. Until they are, Allowed refuses to run any of them on the
// survivor. Both ids are compared as they read, aliases followed: a task merged into e already is e. It
// refuses e itself, a closed other task, a project, a draft or message task, and a store with no Merger or
// gates with no GateSweeper, writing nothing.
func (s *Service) merge(ctx context.Context, e task.Task, req Request) (Result, error) {
	m, sw, err := s.mergers(req)
	if err != nil {
		return Result{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	a, err := s.d.Store.Task(ctx, e.ID)
	if err != nil {
		return Result{}, err
	}
	b, err := s.d.Store.Task(ctx, task.ID(req.Args[task.ArgTask]))
	if err != nil {
		return Result{}, err
	}
	switch {
	case a.ID == b.ID:
		return Result{}, fmt.Errorf("%w: %s and %s are one task already", ErrVerb, e.ID, req.Args[task.ArgTask])
	case !a.Open() || !b.Open():
		return Result{}, fmt.Errorf("%w: a merge of %s (%s) and %s (%s): both must be open", ErrVerb, a.ID, a.State, b.ID, b.State)
	}
	for _, t := range []task.Task{a, b} {
		if t.Origin == task.OriginDraft || t.Origin == task.OriginMessage || t.IsProject() {
			return Result{}, fmt.Errorf("%w: %s on a %s task", ErrVerb, req.Verb, cmp.Or(classOf(t), t.Origin))
		}
	}
	aHolds, err := s.survives(ctx, req.Verb, a)
	if err != nil {
		return Result{}, err
	}
	bHolds, err := s.survives(ctx, req.Verb, b)
	if err != nil {
		return Result{}, err
	}
	keep, fold := merge.Survivor(a, b, aHolds, bHolds)
	now := s.now()
	y, r, err := merge.Fold(fold, keep, s.d.States, now)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %v", ErrVerb, err)
	}
	acting, err := sw.ActingFor(ctx, fold.ID)
	if err != nil {
		return Result{}, err
	}
	for _, g := range acting {
		r.Superseded = append(r.Superseded, g.ID)
	}
	evs := append(s.verbEvents(keep, req, now), s.verbEvents(fold, req, now)...)
	if err := m.Merge(ctx, r, y, evs...); err != nil {
		return Result{}, err
	}
	s.sweep(ctx, sw, r.Superseded, s.mergedIDs(y)...)
	return Result{Task: y}, nil
}

// split gives back, under the service's lock, the task req.Args[task.ArgTask] names, one whose merge
// record (Merger.MergeOf) says it was merged into e, as merge.Unfold says, then supersedes the open proposals
// of one of the two that name the other, and those left acting for a task still merged into e, and syncs its
// units. It refuses a task whose record names another, which covers a task merged into one merged elsewhere
// since: that one is split first. A split that was written succeeds, whatever the sync that follows it does:
// the next sync of its units does the same.
func (s *Service) split(ctx context.Context, e task.Task, req Request) (Result, error) {
	m, sw, err := s.mergers(req)
	if err != nil {
		return Result{}, err
	}
	from, rest, err := s.unfold(ctx, m, sw, e, req)
	if err != nil {
		return Result{}, err
	}
	_ = s.Sync(ctx, from.Units)
	return Result{Task: rest}, nil
}

func (s *Service) unfold(ctx context.Context, m Merger, sw GateSweeper, e task.Task, req Request) (from, rest task.Task, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, err := s.d.Store.Task(ctx, e.ID)
	if err != nil {
		return from, rest, err
	}
	id := task.ID(req.Args[task.ArgTask])
	r, ok, err := m.MergeOf(ctx, id)
	if err != nil {
		return from, rest, err
	}
	switch {
	case !ok:
		return from, rest, fmt.Errorf("%w: %s is not merged into another task", ErrVerb, id)
	case r.Into != cur.ID:
		return from, rest, fmt.Errorf("%w: %s was merged into %s, which is merged into %s: split %s out of %s first", ErrVerb, id, r.Into, cur.ID, r.Into, cur.ID)
	}
	p, err := s.projectOf(ctx, r.Task)
	if err != nil {
		return from, rest, err
	}
	others, err := s.othersMerged(ctx, m, cur, r)
	if err != nil {
		return from, rest, err
	}
	owners, err := s.owners(ctx, cur)
	if err != nil {
		return from, rest, err
	}
	now := s.now()
	sp, err := merge.Unfold(cur, r, others, owners, p, s.d.States, now)
	if err != nil {
		return from, rest, fmt.Errorf("%w: %v", ErrVerb, err)
	}
	evs := append(s.verbEvents(cur, req, now), s.verbEvents(sp.From, req, now)...)
	if err := m.Split(ctx, r, sp, evs...); err != nil {
		return from, rest, err
	}
	if stale, err := s.naming(ctx, sw, sp.From.ID, cur.ID); err == nil {
		s.sweep(ctx, sw, stale, s.mergedIDs(sp.Rest)...)
	}
	return sp.From, sp.Rest, nil
}

// othersMerged are the records of the tasks merged into cur but r.From and those merged into it.
func (s *Service) othersMerged(ctx context.Context, m Merger, cur task.Task, r merge.Record) ([]merge.Record, error) {
	name, _ := s.d.States.PropOf(task.RoleMerged)
	mine := append([]string{string(r.From)}, r.Task.Prop(name).List...)
	var out []merge.Record
	for _, id := range cur.Prop(name).List {
		if slices.Contains(mine, id) {
			continue
		}
		o, ok, err := m.MergeOf(ctx, task.ID(id))
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, o)
		}
	}
	return out, nil
}

// owners are the task each ref cur holds derives from its item, as the ledger keeps the item now.
func (s *Service) owners(ctx context.Context, cur task.Task) (map[task.ItemRef]task.ID, error) {
	out := map[task.ItemRef]task.ID{}
	var units []unit.Unit
	for _, ref := range slices.Concat(cur.Items, cur.Resolved) {
		if !slices.Contains(units, ref.Unit) {
			units = append(units, ref.Unit)
		}
	}
	for _, u := range units {
		its, err := s.d.Items.Items(ctx, u)
		if err != nil {
			return nil, err
		}
		for _, it := range its {
			out[task.ItemRef{Unit: cmp.Or(it.Unit, u), Item: it.ID}] = task.DerivedID(u, cmp.Or(it.TaskKey, it.ID))
		}
	}
	return out, nil
}

// mergedIDs are the tasks e lists as merged into it.
func (s *Service) mergedIDs(e task.Task) []task.ID {
	var out []task.ID
	for _, id := range e.Role(s.d.States, task.RoleMerged).List {
		out = append(out, task.ID(id))
	}
	return out
}

// sweep supersedes the gates of ids, and every open gate still acting for one of merged, which no longer reads
// as itself; a sweep that fails leaves them for the next merge or split of the task, and Allowed refuses to run
// them meanwhile.
func (s *Service) sweep(ctx context.Context, sw GateSweeper, ids []int64, merged ...task.ID) {
	for _, id := range merged {
		gs, err := sw.ActingFor(ctx, id)
		if err != nil {
			continue
		}
		for _, g := range gs {
			if !slices.Contains(ids, g.ID) {
				ids = append(ids, g.ID)
			}
		}
	}
	_ = sw.Supersede(ctx, ids...)
}

// naming are the open gates acting for a or b whose proposed verb names the other.
func (s *Service) naming(ctx context.Context, sw GateSweeper, a, b task.ID) ([]int64, error) {
	var out []int64
	for _, pair := range [][2]task.ID{{a, b}, {b, a}} {
		gs, err := sw.ActingFor(ctx, pair[0])
		if err != nil {
			return nil, err
		}
		for _, g := range gs {
			if p, ok := ProposedVerb(g); ok && (g.Task == pair[1] || p.Args[task.ArgTask] == string(pair[1])) && !slices.Contains(out, g.ID) {
				out = append(out, g.ID)
			}
		}
	}
	return out, nil
}

// projectOf is the project e's parent names as it reads now, with its children but e.
func (s *Service) projectOf(ctx context.Context, e task.Task) (merge.Project, error) {
	if e.Parent == "" {
		return merge.Project{}, nil
	}
	p, err := s.d.Store.Task(ctx, e.Parent)
	if errors.Is(err, ErrNoTask) {
		return merge.Project{}, nil
	}
	if err != nil {
		return merge.Project{}, err
	}
	open, err := s.d.Store.Tasks(ctx, task.Query{})
	if err != nil {
		return merge.Project{}, err
	}
	out := merge.Project{Task: p}
	for _, o := range open {
		if o.Parent == p.ID && o.ID != e.ID {
			out.Children = append(out.Children, o)
		}
	}
	return out, nil
}

// mergers are the store's Merger and the gates' GateSweeper a merge and a split need.
func (s *Service) mergers(req Request) (Merger, GateSweeper, error) {
	m, ok := s.d.Store.(Merger)
	if !ok {
		return nil, nil, fmt.Errorf("%w: %s: the store keeps no merges", ErrVerb, req.Verb)
	}
	sw, ok := s.d.Gates.(GateSweeper)
	if !ok {
		return nil, nil, fmt.Errorf("%w: %s: the gates sweep none", ErrVerb, req.Verb)
	}
	if req.Args[task.ArgTask] == "" {
		return nil, nil, fmt.Errorf("%w: %s needs %s", ErrArgs, req.Verb, task.ArgTask)
	}
	return m, sw, nil
}

func (s *Service) survives(ctx context.Context, verb task.Verb, t task.Task) (bool, error) {
	if s.d.Survives == nil {
		return false, nil
	}
	return s.d.Survives(ctx, string(verb), t)
}

// MergeOf is the record of the merge that took task id, as the store keeps it; false when id is stored, was
// never merged, or the store keeps no merges.
func (s *Service) MergeOf(ctx context.Context, id task.ID) (merge.Record, bool, error) {
	m, ok := s.d.Store.(Merger)
	if !ok {
		return merge.Record{}, false, nil
	}
	return m.MergeOf(ctx, id)
}
