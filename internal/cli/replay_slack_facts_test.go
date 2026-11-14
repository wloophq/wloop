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
	"github.com/wloophq/wloop/internal/core/port"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/plugins/source"
	"github.com/wloophq/wloop/pkg/plugin/manifest"
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
	posts   map[unit.Unit][]string
	// stale are the units whose newest message waits for the extraction.
	stale map[unit.Unit]bool
	// set are facts the scenario gives a unit over the computed ones.
	set map[unit.Unit]workflow.Facts
}

var (
	_ automation.Units  = (*slackFacts)(nil)
	_ automation.Dueer  = (*slackFacts)(nil)
	_ automation.Scoper = (*slackFacts)(nil)
)

func newSlackFacts(db *sqlite.DB, clock port.Clock) *slackFacts {
	return &slackFacts{db: db, clock: clock, started: map[unit.Unit]bool{}, closed: map[unit.Unit]bool{}, posts: map[unit.Unit][]string{}, stale: map[unit.Unit]bool{}, set: map[unit.Unit]workflow.Facts{}}
}

// List is every Slack unit a task is attached to, open or done, in unit order.
func (f *slackFacts) List(ctx context.Context, _ unit.ProviderID) ([]unit.Unit, error) {
	es, err := f.db.Tasks().Tasks(ctx, task.Query{All: true})
	if err != nil {
		return nil, err
	}
	es = slices.DeleteFunc(es, func(e task.Task) bool { return e.Category == task.CategoryDropped })
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

// Facts are read once per pass for every unit, as the plugin's snapshots are.
func (f *slackFacts) Facts(ctx context.Context, u unit.Unit) (workflow.Facts, error) {
	if _, ok := coretasks.PassOf(ctx); !ok {
		return f.read(ctx, u)
	}
	all, err := coretasks.Once(ctx, "replay.slack", func() (map[unit.Unit]workflow.Facts, error) {
		es, err := f.db.Tasks().Tasks(ctx, task.Query{All: true})
		if err != nil {
			return nil, err
		}
		byUnit := map[unit.Unit][]task.Task{}
		for _, e := range es {
			for _, u := range e.Units {
				byUnit[u] = append(byUnit[u], e)
			}
		}
		units := slices.Collect(maps.Keys(byUnit))
		got, err := f.db.Items().Load(ctx, units)
		if err != nil {
			return nil, err
		}
		out := make(map[unit.Unit]workflow.Facts, len(units))
		for _, u := range units {
			out[u] = f.of(u, byUnit[u], got[u])
		}
		return out, nil
	})
	if err != nil {
		return nil, err
	}
	if facts, ok := all[u]; ok {
		return maps.Clone(facts), nil
	}
	return f.read(ctx, u)
}

func (f *slackFacts) read(ctx context.Context, u unit.Unit) (workflow.Facts, error) {
	es, err := f.db.Tasks().TasksOf(ctx, u)
	if err != nil {
		return nil, err
	}
	got, err := f.db.Items().Load(ctx, []unit.Unit{u})
	if err != nil {
		return nil, err
	}
	return f.of(u, es, got[u]), nil
}

// of are u's facts over es, the tasks it is attached to, and its, its items.
func (f *slackFacts) of(u unit.Unit, es []task.Task, its []items.Item) workflow.Facts {
	now := f.clock.Now()
	open, resolved, orphans, age := 0, 0, 0, 0
	var updated time.Time
	for _, it := range its {
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
	closed, started, posts, stale, set := f.closed[u], f.started[u], slices.Clone(f.posts[u]), f.stale[u], maps.Clone(f.set[u])
	f.mu.Unlock()
	facts := workflow.Facts{
		"slack.channel":        "",
		"slack.conversation":   "channel",
		"slack.started_by_me":  started,
		"slack.closure_posted": closed,
		"slack.ended":          closed,
		"slack.live_task": slices.ContainsFunc(es, func(e task.Task) bool {
			return e.Open() && e.Category != task.CategoryDeferred && slices.Contains(e.Units, u)
		}),
		"slack.items_open":                open,
		"slack.items_resolved":            resolved,
		"slack.items_unowned":             orphans,
		"slack.items_unowned_age_days":    age,
		"slack.settled_at":                time.Time{},
		"slack.fingerprint":               u.String() + "||" + updated.UTC().Format(time.RFC3339Nano),
		"slack.extracted":                 !stale,
		"slack.last_by_me":                false,
		"slack.last_to_me":                false,
		"slack.reacted_to_last":           false,
		"slack.items_resolved_unanswered": 0,
		"slack.waited_posted":             append([]string{}, posts...),
		"slack.waited_reactions":          []string{},
		"slack.unmentionable":             []string{},
	}
	maps.Copy(facts, set)
	return facts
}

// Scope fills slack.settled_at as the plugin's host does.
func (f *slackFacts) Scope(ctx context.Context, wf string, u unit.Unit, facts workflow.Facts) (workflow.Facts, error) {
	settled := source.SettledFacts{Source: &manifest.Source{SourceVocabulary: manifest.SourceVocabulary{Slot: "slack", Facts: []manifest.Fact{{Name: "slack." + source.SettledFact}}}},
		Drafts: f.db.Drafts()}
	return settled.Scope(ctx, wf, u, facts)
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

// posted is who's post at at on u, after the ask of item, which waits on who, as the plugin reports it.
func (f *slackFacts) posted(u unit.Unit, item string, who items.Person, at time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.posts[u] = append(f.posts[u], item+" "+who.Key+" "+at.UTC().Format(time.RFC3339Nano))
}

// unextracted says whether u's newest message waits for the extraction.
func (f *slackFacts) unextracted(u unit.Unit, waits bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stale[u] = waits
}

// setFact gives u the fact name at v over the computed one; a nil v drops it.
func (f *slackFacts) setFact(u unit.Unit, name string, v any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.set[u] == nil {
		f.set[u] = workflow.Facts{}
	}
	if v == nil {
		delete(f.set[u], name)
		return
	}
	f.set[u][name] = v
}

// closedUnits are the units a unit.closed closed.
func (f *slackFacts) closedUnits() []unit.Unit {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []unit.Unit
	for u, ok := range f.closed {
		if ok {
			out = append(out, u)
		}
	}
	slices.SortFunc(out, func(a, b unit.Unit) int { return cmp.Compare(a.String(), b.String()) })
	return out
}

func (f *slackFacts) startedByMe(u unit.Unit) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started[u] = true
}
