package automationtest

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

// Noter is the write side of the change log.
type Noter interface {
	port.Changes
	Note(t port.Topic, units ...unit.Unit) port.Change
}

// Notes makes a fake note its writes on n; a Ledger notes on a log of its own without it.
func Notes(n Noter) Option { return func(o *options) { o.notes = n } }

// Ledger is an in-memory plan and run store that notes like the store does:
// TopicLoopEvents when a write stores events, TopicGate when the pending set changes.
type Ledger struct {
	o options

	mu      sync.Mutex
	items   map[string]workflow.Item
	runs    map[string]workflow.Run
	marks   map[unit.Unit]map[string]string
	events  []port.Event
	saveErr error
}

func NewLedger(opts ...Option) *Ledger {
	o := build(opts)
	if o.notes == nil {
		o.notes = change.NewLog(256, nil)
	}
	return &Ledger{o: o, items: map[string]workflow.Item{}, runs: map[string]workflow.Run{}, marks: map[unit.Unit]map[string]string{}}
}

// Changes is the log the ledger notes on.
func (l *Ledger) Changes() port.Changes { return l.o.notes }

// Events are the events the ledger's writes stored, in order.
func (l *Ledger) Events() []port.Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.events)
}

// FailSave makes Save and Record fail with err; nil clears it.
func (l *Ledger) FailSave(err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.saveErr = err
}

type pendingEntry struct {
	deadline time.Time
	def      workflow.Outcome
	u        unit.Unit
}

func (l *Ledger) pending() map[string]pendingEntry {
	out := map[string]pendingEntry{}
	for id, it := range l.items {
		if it.Decision == workflow.DecisionPending {
			out[id] = pendingEntry{it.Deadline, it.Default, it.Unit}
		}
	}
	return out
}

func (l *Ledger) write(ctx context.Context, evs []port.Event, change func()) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, ev := range evs {
		if ev.At.IsZero() {
			return fmt.Errorf("the %s event has no time", ev.Kind)
		}
	}
	l.mu.Lock()
	if l.saveErr != nil {
		l.mu.Unlock()
		return l.saveErr
	}
	before := l.pending()
	change()
	after := l.pending()
	l.events = append(l.events, evs...)
	l.mu.Unlock()

	var moved []unit.Unit
	for id, b := range before {
		if a, ok := after[id]; !ok || !a.deadline.Equal(b.deadline) || a.def != b.def {
			moved = append(moved, b.u)
		}
	}
	for id, a := range after {
		if _, ok := before[id]; !ok {
			moved = append(moved, a.u)
		}
	}
	if len(evs) > 0 {
		units := make([]unit.Unit, 0, len(evs))
		for _, ev := range evs {
			units = append(units, ev.Unit)
		}
		l.o.notes.Note(port.TopicLoopEvents, units...)
	}
	if len(moved) > 0 {
		l.o.notes.Note(port.TopicGate, moved...)
	}
	return nil
}

func (l *Ledger) Save(ctx context.Context, p automation.PlanWrite, evs ...port.Event) error {
	l.o.write("ledger.save")
	return l.write(ctx, evs, func() {
		maps.DeleteFunc(l.items, func(_ string, it workflow.Item) bool { return slices.Contains(p.Workflows, it.Workflow) })
		for _, it := range p.Items {
			if r, ok := l.runs[it.Key.ID()]; ok && (r.Status == workflow.RunDone || r.Status == workflow.RunDismissed) {
				continue
			}
			l.items[it.ID] = cloneItem(it)
		}
	})
}

func (l *Ledger) Record(ctx context.Context, r workflow.Run, it workflow.Item, evs ...port.Event) error {
	l.o.write("ledger.record")
	return l.write(ctx, evs, func() {
		l.runs[r.Key.ID()] = r
		if r.Status == workflow.RunDone || r.Status == workflow.RunDismissed {
			delete(l.items, it.ID)
			for name, v := range it.Marks {
				if l.marks[it.Unit] == nil {
					l.marks[it.Unit] = map[string]string{}
				}
				l.marks[it.Unit][name] = v
			}
			return
		}
		l.items[it.ID] = cloneItem(it)
	})
}

func (l *Ledger) Items(ctx context.Context, f automation.Filter) ([]workflow.Item, error) {
	l.o.read("ledger.items")
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []workflow.Item
	for _, it := range l.items {
		switch {
		case f.Workflow != "" && it.Workflow != f.Workflow,
			f.Provider != "" && it.Unit.Provider != f.Provider,
			f.Unit != nil && it.Unit != *f.Unit,
			len(f.Decisions) > 0 && !slices.Contains(f.Decisions, it.Decision),
			f.ID != "" && it.ID != f.ID:
			continue
		}
		out = append(out, cloneItem(it))
	}
	slices.SortFunc(out, func(a, b workflow.Item) int { return strings.Compare(a.ID, b.ID) })
	return out, nil
}

func (l *Ledger) Run(ctx context.Context, k workflow.Key) (workflow.Run, bool, error) {
	l.o.read("ledger.run")
	l.mu.Lock()
	defer l.mu.Unlock()
	r, ok := l.runs[k.ID()]
	return r, ok, nil
}

func (l *Ledger) Count(ctx context.Context, wf string, since time.Time, u *unit.Unit) (int, error) {
	l.o.read("ledger.count")
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, r := range l.runs {
		if r.Key.Workflow == wf && r.Status == workflow.RunDone && !r.At.Before(since) && (u == nil || r.Key.Unit == *u) {
			n++
		}
	}
	return n, nil
}

func cloneItem(it workflow.Item) workflow.Item {
	it.Steps = slices.Clone(it.Steps)
	it.Why.Matched = slices.Clone(it.Why.Matched)
	it.Marks = maps.Clone(it.Marks)
	if it.Why.Event != nil {
		ev := *it.Why.Event
		it.Why.Event = &ev
	}
	return it
}

// Strict makes the fake fail t on any later write.
func (l *Ledger) Strict(t testing.TB) { l.o.seal(t) }

// Marks are the marks done and dismissed runs recorded on u.
func (l *Ledger) Marks(ctx context.Context, u unit.Unit) (map[string]string, error) {
	l.o.read("ledger.marks")
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return l.MarksOf(u), nil
}

// MarksOf is Marks for a test to assert on.
func (l *Ledger) MarksOf(u unit.Unit) map[string]string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return maps.Clone(l.marks[u])
}
