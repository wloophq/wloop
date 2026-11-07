package tasks

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

// ViewSpec is a view of the profile as the service lists it. Over is what it lists: task.OverTasks, the stored
// tasks ViewEval places in it, or another, which the ViewSource registered for it serves. Paged says its
// predicate reads only what a write of the task itself moves, and not the time, so a page since a position lists
// only the tasks written after it; any other view is served Full. HideDisabled leaves out the tasks whose units
// are all of a disabled plugin.
type ViewSpec struct {
	ID           task.View
	Over         string
	Paged        bool
	HideDisabled bool
	// Uncounted leaves the view out of the counters, unless the badge or a review step names it: a view off
	// the rail, which the home and the queues read with Sets when they need it.
	Uncounted bool
}

// Views are the profile's views that list something, in its order, and the views its badge and its review name.
type Views struct {
	List   []ViewSpec
	Badge  []task.View
	Review []task.View
}

// ViewSort orders a view's tasks in place as the profile sorts the view; they come in their base order: the
// oldest first, a parent's children in their sibling order.
type ViewSort func(ctx context.Context, view string, ts []task.Task) error

// Pass is one evaluation of the views over the tasks: every task it ranges over, so a fact over a task's
// parent or children reads them here rather than in the store, and what an evaluator read of each task, kept
// for the pass.
type Pass struct {
	Tasks []task.Task

	mu    sync.Mutex
	facts map[task.ID]workflow.Facts
	once  map[string]*loaded
	units []unit.Unit
	held  map[unit.Unit]bool
	byID  map[task.ID]task.Task
}

type loaded struct {
	once sync.Once
	v    any
	err  error
}

type passKey struct{}

// WithPass is ctx carrying p, which ViewEval and ViewSort find with PassOf.
func WithPass(ctx context.Context, p *Pass) context.Context {
	return context.WithValue(ctx, passKey{}, p)
}

// PassOf is the pass ctx carries; false outside one.
func PassOf(ctx context.Context) (*Pass, bool) {
	p, ok := ctx.Value(passKey{}).(*Pass)
	return p, ok
}

// Facts are task id's facts as read reads them, once per pass.
func (p *Pass) Facts(id task.ID, read func() (workflow.Facts, error)) (workflow.Facts, error) {
	p.mu.Lock()
	f, ok := p.facts[id]
	p.mu.Unlock()
	if ok {
		return f, nil
	}
	f, err := read()
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.facts == nil {
		p.facts = map[task.ID]workflow.Facts{}
	}
	p.facts[id] = f
	return f, nil
}

// Once is what load returns under key, loaded once per pass and shared by every task of it: what a fact reads
// of the store as a whole, such as the open sessions, rather than once per task.
func (p *Pass) Once(key string, load func() (any, error)) (any, error) {
	p.mu.Lock()
	if p.once == nil {
		p.once = map[string]*loaded{}
	}
	l := p.once[key]
	if l == nil {
		l = &loaded{}
		p.once[key] = l
	}
	p.mu.Unlock()
	l.once.Do(func() { l.v, l.err = load() })
	if l.err != nil && cancelled(l.err) {
		p.mu.Lock()
		if p.once[key] == l {
			delete(p.once, key)
		}
		p.mu.Unlock()
	}
	return l.v, l.err
}

func cancelled(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// Units are the units of every task of the pass, its own and its items', each once, in pass order.
func (p *Pass) Units() []unit.Unit {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.units == nil {
		seen := map[unit.Unit]bool{}
		p.units = []unit.Unit{}
		for _, e := range p.Tasks {
			for _, u := range e.Units {
				if !seen[u] {
					seen[u] = true
					p.units = append(p.units, u)
				}
			}
			for _, r := range e.Items {
				if !seen[r.Unit] {
					seen[r.Unit] = true
					p.units = append(p.units, r.Unit)
				}
			}
		}
	}
	return p.units
}

// Task is task id of the pass; false when the pass does not range over it.
func (p *Pass) Task(id task.ID) (task.Task, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.byID == nil {
		p.byID = make(map[task.ID]task.Task, len(p.Tasks))
		for _, e := range p.Tasks {
			p.byID[e.ID] = e
		}
	}
	e, ok := p.byID[id]
	return e, ok
}

// Holds says whether u is a unit of a task of the pass.
func (p *Pass) Holds(u unit.Unit) bool {
	units := p.Units()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.held == nil {
		p.held = make(map[unit.Unit]bool, len(units))
		for _, u := range units {
			p.held[u] = true
		}
	}
	return p.held[u]
}

// Once is what load returns under key, once per the pass ctx carries; load's result each time outside a pass.
func Once[T any](ctx context.Context, key string, load func() (T, error)) (T, error) {
	p, ok := PassOf(ctx)
	if !ok {
		return load()
	}
	v, err := p.Once(key, func() (any, error) { return load() })
	if err != nil && cancelled(err) && ctx.Err() == nil {
		// The load another reader of the pass ran ended with its reader's context.
		v, err = p.Once(key, func() (any, error) { return load() })
	}
	if err != nil {
		var zero T
		return zero, err
	}
	return v.(T), nil
}

// ItemsKey is the Once key of the items of every unit of a pass, a map[unit.Unit][]items.Item, which the facts
// and the draft step share.
const ItemsKey = "items"

// Lookup is the value of k, held or not: in a pass, when all is not nil, every value is loaded once under key
// with all; one reads k alone otherwise.
func Lookup[K comparable, V any](ctx context.Context, key string, k K, all func() (map[K]V, error), one func() (V, bool, error)) (V, bool, error) {
	if _, ok := PassOf(ctx); !ok || all == nil {
		return one()
	}
	m, err := Once(ctx, key, all)
	v, held := m[k]
	return v, held, err
}

// UnitLookup is Lookup over the units of the pass: when the pass holds u, the values of all its units are
// loaded once under key with all.
func UnitLookup[V any](ctx context.Context, key string, u unit.Unit, all func(units []unit.Unit) (map[unit.Unit]V, error), one func() (V, bool, error)) (V, bool, error) {
	pass, ok := PassOf(ctx)
	if !ok || all == nil || !pass.Holds(u) {
		return one()
	}
	return Lookup(ctx, key, u, func() (map[unit.Unit]V, error) { return all(pass.Units()) }, one)
}

type viewSource struct {
	prefix string
	src    ViewSource
}

// RegisterView serves the views over over from src; task ids starting with prefix are read from it.
func (s *Service) RegisterView(over, prefix string, src ViewSource) {
	s.reg.Lock()
	defer s.reg.Unlock()
	s.views[over] = viewSource{prefix: prefix, src: src}
}

func (s *Service) source(over string) (ViewSource, bool) {
	s.reg.RLock()
	defer s.reg.RUnlock()
	vs, ok := s.views[over]
	return vs.src, ok
}

func (s *Service) sourceOf(id task.ID) (ViewSource, bool) {
	s.reg.RLock()
	defer s.reg.RUnlock()
	for _, vs := range s.views {
		if vs.prefix != "" && strings.HasPrefix(string(id), vs.prefix) {
			return vs.src, true
		}
	}
	return nil, false
}

func (s *Service) profileViews() Views {
	if s.d.Views == nil {
		return Views{}
	}
	return s.d.Views()
}

// spec is view's spec; ErrArgs when the profile lists nothing by that name.
func (s *Service) spec(v task.View) (ViewSpec, error) {
	vs := s.profileViews()
	i := slices.IndexFunc(vs.List, func(x ViewSpec) bool { return x.ID == v })
	if i < 0 {
		return ViewSpec{}, fmt.Errorf("%w: view %q is not served", ErrArgs, v)
	}
	return vs.List[i], nil
}

// Tasks is q's view: its tasks in the view's order, narrowed to q's area and contexts.
func (s *Service) Tasks(ctx context.Context, q task.Query) ([]task.Task, error) {
	spec, err := s.spec(q.View)
	if err != nil {
		return nil, err
	}
	if spec.Over != task.OverTasks {
		return s.sourced(ctx, spec, q)
	}
	pass, err := s.pass(ctx)
	if err != nil {
		return nil, err
	}
	return s.list(WithPass(ctx, pass), spec, q, pass.Tasks)
}

// sourced are the tasks of the view over another thing than the stored tasks, from its ViewSource, narrowed to
// q's area and contexts as every view is; none while no source serves it.
func (s *Service) sourced(ctx context.Context, spec ViewSpec, q task.Query) ([]task.Task, error) {
	src, ok := s.source(spec.Over)
	if !ok {
		return nil, nil
	}
	es, err := src.Tasks(ctx, q)
	return slices.DeleteFunc(es, func(e task.Task) bool { return !s.matches(q, e) }), err
}

// list is q's view over all in the view's order.
func (s *Service) list(ctx context.Context, spec ViewSpec, q task.Query, all []task.Task) ([]task.Task, error) {
	out, err := s.listed(ctx, spec, q, all)
	if err != nil {
		return nil, err
	}
	baseOrder(out)
	if s.d.ViewSort != nil {
		if err := s.d.ViewSort(ctx, string(spec.ID), out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// listed are the tasks of all in q's view, in all's order.
func (s *Service) listed(ctx context.Context, spec ViewSpec, q task.Query, all []task.Task) ([]task.Task, error) {
	hidden := s.hiderFor(spec)
	var out []task.Task
	for _, e := range all {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		in, err := s.in(ctx, spec, q, e, hidden)
		if err != nil {
			return nil, err
		}
		if in {
			out = append(out, e)
		}
	}
	return out, nil
}

// in says whether e is in q's view: its predicate holds, and e passes q's area and contexts and is not hidden.
func (s *Service) in(ctx context.Context, spec ViewSpec, q task.Query, e task.Task, hidden func(task.Task) bool) (bool, error) {
	if !s.matches(q, e) || (hidden != nil && hidden(e)) || s.d.ViewEval == nil {
		return false, nil
	}
	return s.d.ViewEval(ctx, string(spec.ID), e)
}

// matches says whether e passes q's area and contexts, the properties of the area and contexts roles.
func (s *Service) matches(q task.Query, e task.Task) bool {
	if q.Area != "" && e.Role(s.d.States, task.RoleArea).Text != q.Area {
		return false
	}
	for _, c := range q.Contexts {
		if !slices.Contains(e.Role(s.d.States, task.RoleContexts).List, c) {
			return false
		}
	}
	return true
}

// baseOrder orders ts the oldest first, then puts each parent's children, where they stand, in their sibling
// order.
func baseOrder(ts []task.Task) {
	slices.SortStableFunc(ts, func(a, b task.Task) int { return cmp.Or(a.Created.Compare(b.Created), cmp.Compare(a.ID, b.ID)) })
	slots := map[task.ID][]int{}
	for i, e := range ts {
		if e.Parent != "" {
			slots[e.Parent] = append(slots[e.Parent], i)
		}
	}
	for _, at := range slots {
		kids := make([]task.Task, len(at))
		for j, i := range at {
			kids[j] = ts[i]
		}
		slices.SortStableFunc(kids, func(a, b task.Task) int { return cmp.Or(cmp.Compare(a.Position, b.Position), cmp.Compare(a.ID, b.ID)) })
		for j, i := range at {
			ts[i] = kids[j]
		}
	}
}

// hiderFor is hider in a view that hides a disabled plugin's tasks; nil in any other.
func (s *Service) hiderFor(spec ViewSpec) func(task.Task) bool {
	if !spec.HideDisabled {
		return nil
	}
	return s.hider()
}

// hider says whether a task is hidden: it has units, all of a disabled plugin; nil when no plugin is disabled.
func (s *Service) hider() func(task.Task) bool {
	if s.d.Disabled == nil {
		return nil
	}
	off := s.d.Disabled()
	if len(off) == 0 {
		return nil
	}
	return func(e task.Task) bool {
		return len(e.Units) > 0 && !slices.ContainsFunc(e.Units, func(u unit.Unit) bool { return !slices.Contains(off, u.Provider) })
	}
}

// Task reads id from the ViewSource its prefix names, or the store.
func (s *Service) Task(ctx context.Context, id task.ID) (task.Task, error) {
	if src, ok := s.sourceOf(id); ok {
		e, found, err := src.Task(ctx, id)
		if err != nil {
			return task.Task{}, err
		}
		if !found {
			return task.Task{}, fmt.Errorf("%w: %s", ErrNoTask, id)
		}
		return e, nil
	}
	return s.d.Store.Task(ctx, id)
}

// Counts are the counters of the profile's views over area.
func (s *Service) Counts(ctx context.Context, area string) (task.Counters, error) {
	return s.CountsWhere(ctx, area, nil)
}

// CountsWhere is Counts over the tasks keep takes, every task when keep is nil. Each view, area and context
// counts only the tasks of its list that keep takes, so a client's filter narrows the counters as it narrows
// the lists; keep reads a task inside the pass its ctx carries.
func (s *Service) CountsWhere(ctx context.Context, area string, keep func(context.Context, task.Task) (bool, error)) (task.Counters, error) {
	n, err := s.Snapshot(ctx)
	if err != nil {
		return task.Counters{}, err
	}
	return s.counts(WithPass(ctx, n.pass), area, n.pass.Tasks, keep)
}

func (s *Service) counts(ctx context.Context, area string, all []task.Task, keep Keep) (task.Counters, error) {
	keeps := keptOnce(ctx, keep)
	vs := s.profileViews()
	c := task.Counters{Area: area, Views: map[task.View]int{}, Areas: map[string]int{}, Contexts: map[string]int{}}
	counted := slices.DeleteFunc(slices.Clone(vs.List), func(spec ViewSpec) bool {
		return spec.Uncounted && !slices.Contains(vs.Badge, spec.ID) && !slices.Contains(vs.Review, spec.ID)
	})
	sets, err := s.sets(ctx, counted, area, all, keeps, false)
	if err != nil {
		return task.Counters{}, err
	}
	for v, es := range sets {
		c.Views[v] = len(es)
	}
	hidden := s.hider()
	for _, e := range all {
		if !e.Open() || (hidden != nil && hidden(e)) {
			continue
		}
		k, err := keeps(e)
		if err != nil {
			return task.Counters{}, err
		}
		if !k {
			continue
		}
		a := e.Role(s.d.States, task.RoleArea).Text
		if a != "" {
			c.Areas[a]++
		}
		if area == "" || a == area {
			for _, cx := range e.Role(s.d.States, task.RoleContexts).List {
				c.Contexts[cx]++
			}
		}
	}
	for _, v := range vs.Badge {
		c.Badge += c.Views[v]
	}
	return c, nil
}

// TasksSince is q's page after since with the counters of q's area. A paged view's page from the same epoch
// lists the tasks written after since and the tasks holding one of units that are in the view, and removes
// those that are not; any other view's page is Full, at the store's position.
func (s *Service) TasksSince(ctx context.Context, q task.Query, since Since, units []unit.Unit) (Page, error) {
	spec, err := s.spec(q.View)
	if err != nil {
		return Page{}, err
	}
	p, err := s.d.Store.TasksSince(ctx, task.Query{All: true}, since)
	if err != nil {
		return Page{}, err
	}
	pass, err := s.pass(ctx)
	if err != nil {
		return Page{}, err
	}
	if !pass.covers(p) {
		if pass, err = s.newPass(ctx); err != nil {
			return Page{}, err
		}
	}
	all := pass.Tasks
	ctx = WithPass(ctx, pass)
	switch {
	case spec.Over != task.OverTasks:
		p.Full, p.Removed = true, nil
		p.Tasks, err = s.sourced(ctx, spec, q)
	case p.Full || !spec.Paged:
		p.Full, p.Removed = true, nil
		p.Tasks, err = s.list(ctx, spec, q, all)
	default:
		p.Tasks, p.Removed, err = s.delta(ctx, spec, q, withHolding(p.Tasks, all, units))
	}
	if err != nil {
		return Page{}, err
	}
	p.Counters, err = s.counts(ctx, q.Area, all, nil)
	p.Pass = pass
	return p, err
}

// withHolding is changed and the tasks of all that hold one of units, each once.
func withHolding(changed, all []task.Task, units []unit.Unit) []task.Task {
	if len(units) == 0 {
		return changed
	}
	moved := map[unit.Unit]bool{}
	for _, u := range units {
		moved[u] = true
	}
	out := slices.Clone(changed)
	listed := map[task.ID]bool{}
	for _, e := range changed {
		listed[e.ID] = true
	}
	for _, e := range all {
		if !listed[e.ID] && holdsAny(e, moved) {
			out = append(out, e)
		}
	}
	return out
}

// holdsAny says whether e holds one of units: its own unit, one of its units or an item's.
func holdsAny(e task.Task, units map[unit.Unit]bool) bool {
	if units[task.UnitOf(e.ID)] {
		return true
	}
	for _, u := range e.Units {
		if units[u] {
			return true
		}
	}
	for _, it := range e.Items {
		if units[it.Unit] {
			return true
		}
	}
	return false
}

// Snapshot is the stored tasks read once, for the calls that must agree on them, such as the tasks feed's
// publish of the units they hold and the counters.
type Snapshot struct {
	s    *Service
	pass *Pass
}

// Snapshot reads every stored task, whatever its category.
func (s *Service) Snapshot(ctx context.Context) (Snapshot, error) {
	pass, err := s.pass(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{s: s, pass: pass}, nil
}

// Open are n's open tasks, in the store's order.
func (n Snapshot) Open() []task.Task {
	return slices.DeleteFunc(slices.Clone(n.pass.Tasks), func(e task.Task) bool { return !e.Open() })
}

// Held are those of units a task of n holds, as its own or an item's, in their order.
func (n Snapshot) Held(units []unit.Unit) []unit.Unit {
	return slices.DeleteFunc(slices.Clone(units), func(u unit.Unit) bool { return !n.pass.Holds(u) })
}

// Counts are the counters of the profile's views over area, over the tasks of n.
func (n Snapshot) Counts(ctx context.Context, area string) (task.Counters, error) {
	return n.s.counts(WithPass(ctx, n.pass), area, n.pass.Tasks, nil)
}

// delta splits the tasks written since a position into those in the paged view and the ids of those not.
func (s *Service) delta(ctx context.Context, spec ViewSpec, q task.Query, changed []task.Task) (in []task.Task, removed []task.ID, err error) {
	hidden := s.hiderFor(spec)
	for _, e := range changed {
		ok, err := s.in(ctx, spec, q, e, hidden)
		if err != nil {
			return nil, nil, err
		}
		if ok {
			in = append(in, e)
		} else {
			removed = append(removed, e.ID)
		}
	}
	baseOrder(in)
	return in, removed, nil
}
