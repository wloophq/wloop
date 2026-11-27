package catalog

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/workflow"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/intent/expr"
)

// View says whether task t, whose facts f holds, is in view id of def, as its when says now. A view over
// gates, one that lists nothing and one def does not declare hold no task, and a view that does not ask for
// closed tasks holds no closed one the loop no longer holds.
func View(def intentload.ViewsDef, id string, t task.Task, f workflow.Facts) (bool, error) {
	return ViewAt(def, id, t, f, time.Now())
}

// ViewAt is View with now the time the view's when reads as now.
func ViewAt(def intentload.ViewsDef, id string, t task.Task, f workflow.Facts, now time.Time) (bool, error) {
	if !admits(def, id, t, HeldBy(f)) {
		return false, nil
	}
	return match(def, id, task.OverTasks, f, now)
}

// admits says whether view id of def may hold t as far as t's category goes: t is open, the loop still holds
// it, or the view lists closed tasks too.
func admits(def intentload.ViewsDef, id string, t task.Task, held bool) bool {
	v, ok := def.View(id)
	return ok && (v.Closed || t.Open() || held)
}

// GateView says whether gate g, whose facts f holds, is in view id of def. A view over tasks, one that lists
// nothing and one def does not declare hold no gate.
func GateView(def intentload.ViewsDef, id string, g gate.Gate, f workflow.Facts) (bool, error) {
	return GateViewAt(def, id, g, f, time.Now())
}

// GateViewAt is GateView with now the time the view's when reads as now.
func GateViewAt(def intentload.ViewsDef, id string, _ gate.Gate, f workflow.Facts, now time.Time) (bool, error) {
	return match(def, id, task.OverGates, f, now)
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
	for _, r := range append(viewReads(v), "task.category") {
		if !ownFact(r) {
			return false
		}
	}
	return true
}

// viewReads are the facts v reads: its when's, its group and its sort keys.
func viewReads(v intentload.ViewDef) []string {
	var reads []string
	if v.When != nil {
		reads = append(reads, v.When.Reads()...)
	}
	if v.Group != "" {
		reads = append(reads, v.Group)
	}
	for _, k := range v.Sort {
		reads = append(reads, k.Fact)
	}
	return reads
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
	return SortBy(def, id, ts, facts)
}

// SortBy orders items, stably, by the facts view id of def sorts by, each item's facts as facts reads them; a
// view with no sort keeps their order.
func SortBy[T any](def intentload.ViewsDef, id string, items []T, facts func(T) (workflow.Facts, error)) error {
	v, ok := def.View(id)
	if !ok || len(v.Sort) == 0 {
		return nil
	}
	type faced struct {
		item  T
		facts workflow.Facts
	}
	fs := make([]faced, len(items))
	for i, it := range items {
		f, err := facts(it)
		if err != nil {
			return err
		}
		fs[i] = faced{it, f}
	}
	slices.SortStableFunc(fs, func(a, b faced) int {
		for _, k := range v.Sort {
			c := compareFact(a.facts[k.Fact], b.facts[k.Fact])
			if k.Desc {
				c = -c
			}
			if c != 0 {
				return c
			}
		}
		return 0
	})
	for i, f := range fs {
		items[i] = f.item
	}
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
	// Held says whether the loop still holds t: an open gate or a failed delivery waits on the user over it,
	// or an agent's turn runs on it; nil: what Facts says of them.
	Held func(ctx context.Context, t task.Task) (bool, error)
	Now  func() time.Time
}

// Eval says whether t is in view.
func (l Lens) Eval(ctx context.Context, view string, t task.Task) (bool, error) {
	def := l.Loaded().Views
	if v, ok := def.View(view); ok && !v.Closed && !t.Open() {
		held, err := l.held(ctx, t)
		if err != nil {
			return false, err
		}
		if !admits(def, view, t, held) {
			return false, nil
		}
	}
	f, err := l.facts(ctx, view, t)
	if err != nil {
		return false, err
	}
	return match(def, view, task.OverTasks, f, l.Now())
}

// Sort orders a view's tasks as the profile sorts it.
func (l Lens) Sort(ctx context.Context, view string, ts []task.Task) error {
	return Sort(l.Loaded().Views, view, ts, func(t task.Task) (workflow.Facts, error) { return l.facts(ctx, view, t) })
}

// held is l.Held, else what t's facts say of its open gates, its decisions and its running turns.
func (l Lens) held(ctx context.Context, t task.Task) (bool, error) {
	if l.Held != nil {
		return l.Held(ctx, t)
	}
	if l.Facts == nil {
		return false, nil
	}
	f, err := l.Facts(HeldReads(ctx), t)
	if err != nil {
		return false, err
	}
	return HeldBy(f), nil
}

// HeldBy says whether facts f hold an open gate, a decision the user owes or a running turn.
func HeldBy(f workflow.Facts) bool {
	gates, _ := f["task.gates_open"].(int)
	decisions, _ := f["task.decisions"].([]string)
	turns, _ := f["task.turns_running"].(int)
	return gates > 0 || len(decisions) > 0 || turns > 0
}

var heldReads = []string{"task.gates_open", "task.decisions", "task.turns_running"}

// HeldReads narrows ctx's reads to those HeldBy reads.
func HeldReads(ctx context.Context) context.Context { return workflow.WithReads(ctx, heldReads...) }

func (l Lens) facts(ctx context.Context, view string, t task.Task) (workflow.Facts, error) {
	in := l.Loaded()
	if OwnFacts(in.Views, view) || l.Facts == nil {
		return TaskFacts(t, in.Tasks), nil
	}
	return l.Facts(workflow.WithReads(ctx, allViewReads(in)...), t)
}

// lastReads are the facts the views of the last profile loaded read.
var lastReads atomic.Pointer[profileReads]

type profileReads struct {
	in    *intentload.Loaded
	reads []string
}

// allViewReads are the facts in's views read, each once, read from in's views once per profile loaded.
func allViewReads(in *intentload.Loaded) []string {
	if r := lastReads.Load(); r != nil && r.in == in {
		return r.reads
	}
	var reads []string
	for _, v := range in.Views.Views {
		reads = append(reads, viewReads(v)...)
	}
	slices.Sort(reads)
	reads = slices.Compact(reads)
	lastReads.Store(&profileReads{in: in, reads: reads})
	return reads
}
