package cli

import (
	"cmp"
	"context"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

// slackFacts stand in for the facts the Slack plugin reports of its units,
// computed as the plugin computes them from the host's tasks and items, for
// a replay that stores no Slack messages: a unit ends only when a sent
// draft's unit.closed closes it.
type slackFacts struct {
	db    *sqlite.DB
	clock port.Clock

	mu      sync.Mutex
	due     time.Time
	started map[unit.Unit]bool
	closed  map[unit.Unit]bool
}

var (
	_ automation.Units  = (*slackFacts)(nil)
	_ automation.Dueer  = (*slackFacts)(nil)
	_ automation.Scoper = (*slackFacts)(nil)
)

func newSlackFacts(db *sqlite.DB, clock port.Clock) *slackFacts {
	return &slackFacts{db: db, clock: clock, started: map[unit.Unit]bool{}, closed: map[unit.Unit]bool{}}
}

// List is every Slack unit a task is attached to, open or done, in unit order.
func (f *slackFacts) List(ctx context.Context, _ unit.ProviderID) ([]unit.Unit, error) {
	es, err := f.db.Tasks().Tasks(ctx, task.Query{View: task.ViewOpen})
	if err != nil {
		return nil, err
	}
	done, err := f.db.Tasks().Tasks(ctx, task.Query{View: task.ViewLogbook})
	if err != nil {
		return nil, err
	}
	es = append(es, done...)
	var out []unit.Unit
	for _, e := range es {
		for _, u := range e.Units {
			if u.Provider == slackPlugin {
				out = append(out, u)
			}
		}
	}
	slices.SortFunc(out, func(a, b unit.Unit) int { return cmp.Compare(a.Key, b.Key) })
	out = slices.Compact(out)
	got, err := f.db.Items().Load(ctx, out)
	if err != nil {
		return nil, err
	}
	now := f.clock.Now()
	var due time.Time
	for _, its := range got {
		for _, it := range its {
			if !unownedItem(it, it.Unit) || it.CreatedAt.IsZero() {
				continue
			}
			if at := task.AddWorkdays(it.CreatedAt, 1, now.Location()); at.After(now) && (due.IsZero() || at.Before(due)) {
				due = at
			}
		}
	}
	f.mu.Lock()
	f.due = due
	f.mu.Unlock()
	return out, nil
}

func (f *slackFacts) Due() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.due
}

func (f *slackFacts) Facts(ctx context.Context, u unit.Unit) (workflow.Facts, error) {
	now := f.clock.Now()
	es, err := f.db.Tasks().TasksOf(ctx, u)
	if err != nil {
		return nil, err
	}
	got, err := f.db.Items().Load(ctx, []unit.Unit{u})
	if err != nil {
		return nil, err
	}
	open, resolved, orphans, age := 0, 0, 0, 0
	var updated time.Time
	for _, it := range got[u] {
		if it.UpdatedAt.After(updated) {
			updated = it.UpdatedAt
		}
		switch it.State {
		case items.Open:
			open++
		case items.Resolved:
			resolved++
		}
		if !unownedItem(it, it.Unit) {
			continue
		}
		orphans++
		if !it.CreatedAt.IsZero() {
			n := 0
			for !task.AddWorkdays(it.CreatedAt, n+1, now.Location()).After(now) {
				n++
			}
			age = max(age, n)
		}
	}
	f.mu.Lock()
	closed, started := f.closed[u], f.started[u]
	f.mu.Unlock()
	return workflow.Facts{
		"slack.channel":        "",
		"slack.conversation":   "channel",
		"slack.started_by_me":  started,
		"slack.closure_posted": closed,
		"slack.ended":          closed,
		"slack.live_task": slices.ContainsFunc(es, func(e task.Task) bool {
			return e.Open() && e.Category != task.CategoryDeferred && slices.Contains(e.Units, u)
		}),
		"slack.items_open":             open,
		"slack.items_resolved":         resolved,
		"slack.items_unowned":          orphans,
		"slack.items_unowned_age_days": age,
		"slack.settled_at":             time.Time{},
		"slack.fingerprint":            u.String() + "||" + updated.UTC().Format(time.RFC3339Nano),
	}, nil
}

func (f *slackFacts) Scope(ctx context.Context, wf string, u unit.Unit, facts workflow.Facts) (workflow.Facts, error) {
	ds, err := f.db.Drafts().List(ctx, outbox.Filter{Unit: &u, Workflow: wf})
	if err != nil {
		return nil, err
	}
	out := maps.Clone(facts)
	out["slack.settled_at"] = draft.SettledAt(ds, wf, u)
	return out, nil
}

// markClosed is the plugin's unit.closed: the draft's unit is closed.
func (f *slackFacts) markClosed(_ context.Context, d draft.Draft) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed[d.Unit] = true
	return nil
}

func (f *slackFacts) isClosed(u unit.Unit) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed[u]
}

func (f *slackFacts) startedByMe(u unit.Unit) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started[u] = true
}
