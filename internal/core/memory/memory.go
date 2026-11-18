// Package memory is what the turns of a task learned, across every unit the task holds and the tasks merged
// into it, and, once the task reopened, how it had ended.
package memory

import (
	"cmp"
	"context"
	"slices"
	"time"

	coreitems "github.com/wloophq/wloop/internal/core/items"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

type Tasks interface {
	Task(ctx context.Context, id task.ID) (task.Task, error)
	// TasksOf are the tasks that hold u.
	TasksOf(ctx context.Context, u unit.Unit) ([]task.Task, error)
}

type Merges interface {
	// MergedInto are the tasks merged into task id, transitively.
	MergedInto(ctx context.Context, id task.ID) ([]task.ID, error)
}

type Books interface {
	Books(ctx context.Context, units ...unit.Unit) (map[unit.Unit]turn.Book, error)
}

// Items are the items ledger's items and the outcomes of its ops.
type Items interface {
	Items(ctx context.Context, u unit.Unit) ([]items.Item, error)
	Log(ctx context.Context, u unit.Unit, since time.Time) ([]coreitems.LogEntry, error)
}

// Entry is what one turn record learned, on the unit it ran on.
type Entry struct {
	Unit unit.Unit
	At   time.Time
	Text string
}

// Resolved is an item the task held and how it was resolved.
type Resolved struct {
	Unit       unit.Unit
	Item       string
	Summary    string
	Resolution items.Resolution
}

// Outcome is how a reopened task had ended: the summary of its last turn and the resolutions of its items,
// as they stood when it reopened at ReopenedAt.
type Outcome struct {
	ReopenedAt time.Time
	Summary    string
	Items      []Resolved
}

type Memory struct {
	// Task is the task whose memory it is; "" for a unit no task holds.
	Task task.ID
	// Outcome is nil unless the task reopened.
	Outcome *Outcome
	// Learned are the turns' entries, newest first.
	Learned []Entry
}

// Recent is how many learned entries a turn's instructions carry; task_read lists them all.
const Recent = 3

type Service struct {
	Tasks Tasks
	// Merges nil merges no task into another.
	Merges Merges
	Books  Books
	// Items nil derives no item of an outcome.
	Items Items
}

// Of is the memory of the task that holds u, an open one first, then the oldest; u's own when no task holds it.
func (s *Service) Of(ctx context.Context, u unit.Unit) (Memory, error) {
	e, ok, err := s.holder(ctx, u)
	if err != nil {
		return Memory{}, err
	}
	if !ok {
		learned, _, err := s.learned(ctx, []unit.Unit{u})
		return Memory{Learned: learned}, err
	}
	return s.OfTask(ctx, e)
}

// OfTask is e's memory.
func (s *Service) OfTask(ctx context.Context, e task.Task) (Memory, error) {
	units, err := s.units(ctx, e)
	if err != nil {
		return Memory{}, err
	}
	learned, books, err := s.learned(ctx, units)
	if err != nil {
		return Memory{}, err
	}
	m := Memory{Task: e.ID, Learned: learned}
	if e.Open() && !e.ClosedAt.IsZero() && !e.OpenChanged.Before(e.ClosedAt) {
		m.Outcome, err = s.outcome(ctx, e, books)
	}
	return m, err
}

// LearnedOf is what e's turns learned, newest first, without the outcome OfTask derives.
func (s *Service) LearnedOf(ctx context.Context, e task.Task) ([]Entry, error) {
	units, err := s.units(ctx, e)
	if err != nil {
		return nil, err
	}
	learned, _, err := s.learned(ctx, units)
	return learned, err
}

func (s *Service) holder(ctx context.Context, u unit.Unit) (task.Task, bool, error) {
	if id, ok := task.IDOf(u); ok {
		e, err := s.Tasks.Task(ctx, id)
		return e, err == nil, err
	}
	all, err := s.Tasks.TasksOf(ctx, u)
	if err != nil || len(all) == 0 {
		return task.Task{}, false, err
	}
	if i := slices.IndexFunc(all, task.Task.Open); i >= 0 {
		return all[i], true, nil
	}
	return slices.MinFunc(all, func(a, b task.Task) int { return a.Created.Compare(b.Created) }), true, nil
}

// units are e's own unit, the units it holds or holds items on, and the own units of the tasks merged into it.
func (s *Service) units(ctx context.Context, e task.Task) ([]unit.Unit, error) {
	out := []unit.Unit{task.UnitOf(e.ID)}
	add := func(u unit.Unit) {
		if !u.IsZero() && !slices.Contains(out, u) {
			out = append(out, u)
		}
	}
	for _, u := range e.Units {
		add(u)
	}
	for _, r := range slices.Concat(e.Items, e.Resolved) {
		add(r.Unit)
	}
	if s.Merges == nil {
		return out, nil
	}
	merged, err := s.Merges.MergedInto(ctx, e.ID)
	for _, id := range merged {
		add(task.UnitOf(id))
	}
	return out, err
}

func (s *Service) learned(ctx context.Context, units []unit.Unit) ([]Entry, map[unit.Unit]turn.Book, error) {
	books, err := s.Books.Books(ctx, units...)
	if err != nil {
		return nil, nil, err
	}
	var out []Entry
	for _, u := range units {
		for _, r := range books[u].Records {
			if r.Record.Learned != "" {
				out = append(out, Entry{Unit: u, At: r.At, Text: r.Record.Learned})
			}
		}
	}
	slices.SortStableFunc(out, func(a, b Entry) int { return b.At.Compare(a.At) })
	return out, books, nil
}

func (s *Service) outcome(ctx context.Context, e task.Task, books map[unit.Unit]turn.Book) (*Outcome, error) {
	o := Outcome{ReopenedAt: e.OpenChanged}
	var last time.Time
	for _, b := range books {
		for _, r := range b.Records {
			if r.At.Before(o.ReopenedAt) && r.Record.Summary != "" && r.At.After(last) {
				o.Summary, last = r.Record.Summary, r.At
			}
		}
	}
	resolved, err := s.resolved(ctx, slices.Concat(e.Items, e.Resolved), e.Created, o.ReopenedAt)
	if err != nil {
		return nil, err
	}
	o.Items = resolved
	if o.Summary == "" && len(o.Items) == 0 {
		return nil, nil
	}
	return &o, nil
}

// resolved are refs' last applied resolutions logged from since and before at, each with its item's summary.
func (s *Service) resolved(ctx context.Context, refs []task.ItemRef, since, at time.Time) ([]Resolved, error) {
	if s.Items == nil {
		return nil, nil
	}
	var out []Resolved
	byUnit := map[unit.Unit][]string{}
	for _, r := range refs {
		byUnit[r.Unit] = append(byUnit[r.Unit], r.Item)
	}
	for u, ids := range byUnit {
		log, err := s.Items.Log(ctx, u, since)
		if err != nil {
			return nil, err
		}
		its, err := s.Items.Items(ctx, u)
		if err != nil {
			return nil, err
		}
		last := map[string]coreitems.LogEntry{}
		for _, l := range log {
			if l.Op.Op == items.OpResolve && l.Outcome == coreitems.OutcomeApplied && l.At.Before(at) && slices.Contains(ids, l.Item) &&
				!l.At.Before(last[l.Item].At) {
				last[l.Item] = l
			}
		}
		for id, l := range last {
			r := Resolved{Unit: u, Item: id, Resolution: l.Op.Resolution}
			if i := slices.IndexFunc(its, func(it items.Item) bool { return it.ID == id }); i >= 0 {
				r.Summary = its[i].Summary
			}
			out = append(out, r)
		}
	}
	slices.SortFunc(out, func(a, b Resolved) int {
		return cmp.Or(cmp.Compare(a.Unit.String(), b.Unit.String()), cmp.Compare(a.Item, b.Item))
	})
	return out, nil
}
