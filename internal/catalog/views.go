package catalog

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/workflow"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/intent/expr"
)

// View says whether task t, whose facts f holds, is in view id of def, as its when says now. A view over
// gates, one that lists nothing and one def does not declare hold no task.
func View(def intentload.ViewsDef, id string, t task.Task, f workflow.Facts) (bool, error) {
	return ViewAt(def, id, t, f, time.Now())
}

// ViewAt is View with now the time the view's when reads as now.
func ViewAt(def intentload.ViewsDef, id string, _ task.Task, f workflow.Facts, now time.Time) (bool, error) {
	return match(def, id, task.OverTasks, f, now)
}

// GateView says whether gate g, whose facts f holds, is in view id of def. A view over tasks, one that lists
// nothing and one def does not declare hold no gate.
func GateView(def intentload.ViewsDef, id string, _ gate.Gate, f workflow.Facts) (bool, error) {
	return match(def, id, task.OverGates, f, time.Now())
}

func match(def intentload.ViewsDef, id, over string, f workflow.Facts, now time.Time) (bool, error) {
	v, ok := def.View(id)
	if !ok || v.Over != over || v.When == nil {
		return false, nil
	}
	in, err := v.When.Match(workflow.Vars{Facts: f, Now: now})
	if err != nil {
		return false, fmt.Errorf("views.%s.when: %w", id, err)
	}
	return in, nil
}

// Lists says whether view id of def lists something: it has a when.
func Lists(def intentload.ViewsDef, id string) bool {
	v, ok := def.View(id)
	return ok && v.When != nil
}

// OwnFacts says whether view id of def reads only the facts a task's own fields hold, which TaskFacts reads
// with no store: its when, its group and its sort.
func OwnFacts(def intentload.ViewsDef, id string) bool {
	v, ok := def.View(id)
	if !ok || v.Over != task.OverTasks {
		return false
	}
	reads := []string{"task.category"}
	if v.When != nil {
		reads = append(reads, v.When.Reads()...)
	}
	if v.Group != "" {
		reads = append(reads, v.Group)
	}
	for _, k := range v.Sort {
		reads = append(reads, k.Fact)
	}
	for _, r := range reads {
		if !ownFact(r) {
			return false
		}
	}
	return true
}

func ownFact(name string) bool {
	return name == "task.category" || slices.Contains(builtin.OfferFacts, name) ||
		strings.HasPrefix(name, builtin.PropPrefix) || strings.HasPrefix(name, builtin.PropByPrefix)
}

// Paged says whether view id of def can be paged since a position: it reads only a task's own facts, and not
// the time, so a write of the task is all that moves it in or out.
func Paged(def intentload.ViewsDef, id string) bool {
	v, _ := def.View(id)
	return OwnFacts(def, id) && (v.When == nil || !expr.ReadsNow(v.When))
}

// Sort orders ts, stably, by the facts view id of def sorts by, each task's facts as facts reads them; a
// view with no sort keeps their order.
func Sort(def intentload.ViewsDef, id string, ts []task.Task, facts func(task.Task) (workflow.Facts, error)) error {
	v, ok := def.View(id)
	if !ok || len(v.Sort) == 0 {
		return nil
	}
	fs := make(map[task.ID]workflow.Facts, len(ts))
	for _, t := range ts {
		f, err := facts(t)
		if err != nil {
			return err
		}
		fs[t.ID] = f
	}
	slices.SortStableFunc(ts, func(a, b task.Task) int {
		for _, k := range v.Sort {
			c := compareFact(fs[a.ID][k.Fact], fs[b.ID][k.Fact])
			if k.Desc {
				c = -c
			}
			if c != 0 {
				return c
			}
		}
		return 0
	})
	return nil
}

// compareFact orders two values of one fact: false before true, then by number, text, time or duration; a
// missing value reads as its type's zero.
func compareFact(a, b any) int {
	switch x := a.(type) {
	case bool:
		y, _ := b.(bool)
		switch {
		case x == y:
			return 0
		case !x:
			return -1
		}
		return 1
	case int:
		y, _ := b.(int)
		return cmp.Compare(x, y)
	case int64:
		y, _ := b.(int64)
		return cmp.Compare(x, y)
	case string:
		y, _ := b.(string)
		return cmp.Compare(x, y)
	case time.Time:
		y, _ := b.(time.Time)
		return x.Compare(y)
	case time.Duration:
		y, _ := b.(time.Duration)
		return cmp.Compare(x, y)
	case nil:
		if b == nil {
			return 0
		}
		return -compareFact(b, a)
	}
	return 0
}

// Lens places tasks in the profile's views and sorts them, the core's tasks.ViewEval and tasks.ViewSort. Facts
// reads a task's facts for a view that reads more than its own fields hold; Now is the time a view's when
// reads as now.
type Lens struct {
	Loaded func() *intentload.Loaded
	Facts  func(ctx context.Context, t task.Task) (workflow.Facts, error)
	Now    func() time.Time
}

// Eval says whether t is in view.
func (l Lens) Eval(ctx context.Context, view string, t task.Task) (bool, error) {
	f, err := l.facts(ctx, view, t)
	if err != nil {
		return false, err
	}
	return ViewAt(l.Loaded().Views, view, t, f, l.Now())
}

// Sort orders a view's tasks as the profile sorts it.
func (l Lens) Sort(ctx context.Context, view string, ts []task.Task) error {
	return Sort(l.Loaded().Views, view, ts, func(t task.Task) (workflow.Facts, error) { return l.facts(ctx, view, t) })
}

func (l Lens) facts(ctx context.Context, view string, t task.Task) (workflow.Facts, error) {
	in := l.Loaded()
	if OwnFacts(in.Views, view) || l.Facts == nil {
		return TaskFacts(t, in.Tasks), nil
	}
	return l.Facts(ctx, t)
}
