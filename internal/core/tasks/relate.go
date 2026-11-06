package tasks

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
)

// relate adds each of e and the task req.Args[task.ArgTask] names to the other's property of role related, in
// one Apply; relating two related tasks changes nothing.
func (s *Service) relate(ctx context.Context, e task.Task, req Request) (Result, error) {
	return s.relation(ctx, e, req, true)
}

// unrelate takes each of e and the task req.Args[task.ArgTask] names off the other's property of role
// related, in one Apply.
func (s *Service) unrelate(ctx context.Context, e task.Task, req Request) (Result, error) {
	return s.relation(ctx, e, req, false)
}

// relation links e and the other task req names, or unlinks them, under the service's lock. A listed id
// names a task as it reads, aliases followed, so an entry for a task merged into the other one counts as it.
// It refuses a task with itself, a draft or a message task, and an unlink of tasks not related.
func (s *Service) relation(ctx context.Context, e task.Task, req Request, link bool) (Result, error) {
	name, _ := s.d.States.PropOf(task.RoleRelated)
	if name == "" {
		return Result{}, fmt.Errorf("%w: %s: no property takes the role %s", ErrVerb, req.Verb, task.RoleRelated)
	}
	arg := task.ID(req.Args[task.ArgTask])
	if arg == "" {
		return Result{}, fmt.Errorf("%w: %s names no task in %s", ErrArgs, req.Verb, task.ArgTask)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, err := s.d.Store.Task(ctx, e.ID)
	if err != nil {
		return Result{}, err
	}
	o, err := s.d.Store.Task(ctx, arg)
	if err != nil {
		return Result{}, err
	}
	switch {
	case o.ID == e.ID:
		return Result{}, fmt.Errorf("%w: %s of task %s with itself", ErrArgs, req.Verb, e.ID)
	case o.Origin == task.OriginDraft || o.Origin == task.OriginMessage:
		return Result{}, fmt.Errorf("%w: %s with a %s task", ErrVerb, req.Verb, o.Origin)
	}
	now := s.now()
	ne, eChanged, err := s.relink(ctx, e, name, o.ID, arg, link, now)
	if err != nil {
		return Result{}, err
	}
	no, oChanged, err := s.relink(ctx, o, name, e.ID, e.ID, link, now)
	if err != nil {
		return Result{}, err
	}
	if !link && !eChanged {
		return Result{}, fmt.Errorf("%w: task %s is not related to %s", ErrArgs, e.ID, arg)
	}
	var cs []task.Change
	if eChanged {
		cs = append(cs, task.Change{Op: task.OpUpdate, Task: ne, Why: string(req.Verb)})
	}
	if oChanged {
		cs = append(cs, task.Change{Op: task.OpUpdate, Task: no, Why: string(req.Verb)})
	}
	if len(cs) == 0 {
		return Result{Task: e}, nil
	}
	if s.beforeApply != nil {
		s.beforeApply()
	}
	evs := s.verbEvents(e, req, now)
	for i := range evs {
		evs[i].Key = task.PairKey(o.ID, now)
	}
	if err := s.d.Store.Apply(ctx, cs, evs...); err != nil {
		return Result{}, err
	}
	if !link {
		s.takeApart(ctx, e.ID, o.ID)
	}
	return Result{Task: ne}, nil
}

// relink adds to t's property name, or takes it off, as an entry names it (relisted). A link does not count
// an entry of a task merged into to that a split can give back, so to gets its own entry, which outlives that
// split.
func (s *Service) relink(ctx context.Context, t task.Task, name string, to, as task.ID, link bool, now time.Time) (task.Task, bool, error) {
	list := t.Prop(name).List
	var keep []string
	for _, id := range list {
		named, err := s.relisted(ctx, task.ID(id), to, as)
		if err != nil {
			return t, false, err
		}
		if named && link {
			splittable, err := s.splittable(ctx, task.ID(id), to, as)
			if err != nil || !splittable {
				return t, false, err
			}
			continue
		}
		if !named {
			keep = append(keep, id)
		}
	}
	if link {
		keep = append(slices.Clone(list), string(to))
	} else if len(keep) == len(list) {
		return t, false, nil
	}
	t.SetProp(name, task.Value{List: keep}, task.WriterWorkflow, now)
	t.Updated = now
	return t, true, nil
}

// relisted says whether the listed id names task to: it is to or as, or reads as to.
func (s *Service) relisted(ctx context.Context, id, to, as task.ID) (bool, error) {
	if id == to || id == as {
		return true, nil
	}
	got, err := s.d.Store.Task(ctx, id)
	if errors.Is(err, ErrNoTask) {
		return false, nil
	}
	return err == nil && got.ID == to, err
}

// splittable says whether the listed id, which reads as to, is a task a split can take back out of it.
func (s *Service) splittable(ctx context.Context, id, to, as task.ID) (bool, error) {
	m, ok := s.d.Store.(Merger)
	if !ok || id == to || id == as {
		return false, nil
	}
	_, merged, err := m.MergeOf(ctx, id)
	return merged, err
}

// takeApart supersedes the open gates acting for a or b whose proposed verb names the other, once the user took
// the two apart: a proposal of the pair, merge or relate, a rule or a window would release later runs nothing.
// A sweep that fails leaves them to Allowed, which refuses a workflow's verb on a pair taken apart.
func (s *Service) takeApart(ctx context.Context, a, b task.ID) {
	sw, ok := s.d.Gates.(GateSweeper)
	if !ok {
		return
	}
	if ids, err := s.naming(ctx, sw, a, b); err == nil {
		_ = sw.Supersede(ctx, ids...)
	}
}

// Rejected supersedes the open proposals of the pair rejected gate g proposed, a merge or a relation, the user
// having kept the two apart; a gate that proposes no pair, or was not rejected, supersedes nothing.
func (s *Service) Rejected(ctx context.Context, g gate.Gate) {
	p, ok := ProposedVerb(g)
	other := task.ID(p.Args[task.ArgTask])
	if !ok || g.Status != gate.Rejected || g.Task == "" || other == "" {
		return
	}
	if h := s.d.States.Handler(string(p.Verb)); h != HandlerMerge && h != HandlerRelate {
		return
	}
	s.takeApart(ctx, g.Task, other)
}
