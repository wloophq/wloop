// Package facttask puts the GTD entries behind the engine's Units, as the
// core-kept entry provider: every entry neither done nor in the trash, of
// providers whose plugin is enabled, with its facts in slot entry.
package facttask

import (
	"cmp"
	"context"
	"errors"
	"maps"
	"math"
	"slices"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/core/port"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

// Tasks reads the GTD entries (coretasks.Store).
type Tasks interface {
	Tasks(ctx context.Context, q task.Query) ([]task.Task, error)
	Task(ctx context.Context, id task.ID) (task.Task, error)
}

// Items reads units' items (the items store).
type Items interface {
	Load(ctx context.Context, units []unit.Unit) (map[unit.Unit][]items.Item, error)
}

// Drafts lists the Outbox drafts (outbox.Store).
type Drafts interface {
	List(ctx context.Context, f outbox.Filter) ([]draft.Draft, error)
}

// Settings are the config values the facts read.
type Settings struct {
	// StaleDays is gtd.stale_waiting_days: working days a waiting entry
	// waits before its nudge; 0 waits for its resurface date only.
	StaleDays int
	// RenudgeAfter is drafts.repeat_after: how long a nudge keeps the
	// entry quiet.
	RenudgeAfter time.Duration
}

type Deps struct {
	Tasks Tasks
	Items Items
	// Cards are the facts of the entries' units; nil: none.
	Cards coretasks.Facts
	// Drafts are read for entry.settled_at; nil: always zero.
	Drafts Drafts
	// Enabled says whether a provider's plugin is enabled; nil: every one is.
	Enabled  func(unit.ProviderID) bool
	Settings func() Settings
	// Ended says whether a unit has ended, as its plugin says; nil: none has.
	Ended func(ctx context.Context, u unit.Unit) (bool, error)
	// Turns counts the turns run on a unit; nil: none.
	Turns func(ctx context.Context, u unit.Unit) (int, error)
	// Clock's location is where working days are counted.
	Clock port.Clock
}

// Provider is the entry subject's facts provider. It names the entry's own
// unit as the subject's owner and wakes the sweep at the next nudge-due or
// resurface time.
type Provider struct {
	d Deps

	mu  sync.Mutex
	due time.Time
}

var (
	_ automation.Units  = (*Provider)(nil)
	_ automation.Owner  = (*Provider)(nil)
	_ automation.Dueer  = (*Provider)(nil)
	_ automation.Scoper = (*Provider)(nil)
)

func New(d Deps) *Provider { return &Provider{d: d} }

// never is entry.since_nudge of an entry never nudged.
const never = time.Duration(math.MaxInt64)

func (p *Provider) enabled(id unit.ProviderID) bool { return p.d.Enabled == nil || p.d.Enabled(id) }

func (p *Provider) settings() Settings {
	if p.d.Settings == nil {
		return Settings{}
	}
	return p.d.Settings()
}

// listed says whether e is a subject: a live entry of no unit, or of a unit
// whose plugin is enabled.
func (p *Provider) listed(e task.Task) bool { return e.Live() && p.enabledFor(e) }

func (p *Provider) enabledFor(e task.Task) bool {
	u, ok := e.FirstUnit()
	return !ok || p.enabled(u.Provider)
}

// List is every listed entry, by id, and keeps the earliest nudge-due or
// resurface time after now for Due.
func (p *Provider) List(ctx context.Context, _ unit.ProviderID) ([]unit.Unit, error) {
	es, err := p.d.Tasks.Tasks(ctx, task.Query{View: task.ViewOpen})
	if err != nil {
		return nil, err
	}
	now, s := p.d.Clock.Now(), p.settings()
	var out []unit.Unit
	var due time.Time
	for _, e := range es {
		if !p.listed(e) {
			continue
		}
		out = append(out, task.UnitOf(e.ID))
		for _, t := range []time.Time{nudgeDue(e, s, now.Location()), resurface(e)} {
			if t.After(now) && (due.IsZero() || t.Before(due)) {
				due = t
			}
		}
	}
	slices.SortFunc(out, func(a, b unit.Unit) int { return cmp.Compare(a.Key, b.Key) })
	p.mu.Lock()
	p.due = due
	p.mu.Unlock()
	return out, nil
}

// Due is the earliest nudge-due or resurface time the last List saw ahead.
func (p *Provider) Due() time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.due
}

// task is u's entry; ErrUnitGone when it is gone, or no longer listed
// unless anyState is true, or its plugin is disabled.
func (p *Provider) task(ctx context.Context, u unit.Unit, anyState bool) (task.Task, error) {
	e, err := p.d.Tasks.Task(ctx, task.ID(u.Key))
	switch {
	case errors.Is(err, coretasks.ErrNoTask):
		return task.Task{}, automation.ErrUnitGone
	case err != nil:
		return task.Task{}, err
	case anyState && !p.enabledFor(e), !anyState && !p.listed(e):
		return task.Task{}, automation.ErrUnitGone
	}
	return e, nil
}

// Facts are the entry's facts in slot entry; entry.settled_at is zero until
// Scope reads the workflow's drafts.
func (p *Provider) Facts(ctx context.Context, u unit.Unit) (workflow.Facts, error) {
	return p.facts(ctx, u, false)
}

// EventFacts are the facts of an entry an event names, also once done or
// dismissed.
func (p *Provider) EventFacts(ctx context.Context, u unit.Unit) (workflow.Facts, error) {
	return p.facts(ctx, u, true)
}

func (p *Provider) facts(ctx context.Context, u unit.Unit, anyState bool) (workflow.Facts, error) {
	e, err := p.task(ctx, u, anyState)
	if err != nil {
		return nil, err
	}
	now, s := p.d.Clock.Now(), p.settings()
	loc := now.Location()
	f := workflow.Facts{
		"entry.id":                  string(e.ID),
		"entry.kind":                string(e.Kind),
		"entry.state":               string(e.State),
		"entry.waiting_on":          []string{},
		"entry.resurface_at":        resurface(e),
		"entry.waiting_days":        0,
		"entry.since_nudge":         never,
		"entry.nudge_due_at":        time.Time{},
		"entry.muted":               e.Snooze.Mute,
		"entry.unit":                "",
		"entry.unit_provider":       "",
		"entry.units":               []string{},
		"entry.unit_providers":      []string{},
		"entry.units_ended":         false,
		"entry.captured_by_user":    e.CapturedByUser(),
		"entry.turns":               0,
		"entry.unit_started_by_me":  false,
		"entry.unit_closure_posted": false,
		"entry.items_open":          0,
		"entry.items_resolved":      0,
		"entry.items_owed":          0,
		"entry.settled_at":          time.Time{},
		"entry.fingerprint":         string(e.ID) + "|" + string(e.State) + "|" + e.Updated.UTC().Format(time.RFC3339Nano),
	}
	if w := e.Waiting; w != nil {
		keys := make([]string, 0, len(w.On))
		for _, p := range w.On {
			keys = append(keys, p.Key)
		}
		f["entry.waiting_on"] = keys
		if e.State == task.Waiting && !w.Since.IsZero() {
			f["entry.waiting_days"] = workdaysSince(w.Since, now, loc)
		}
		if !w.LastNudge.IsZero() {
			f["entry.since_nudge"] = now.Sub(w.LastNudge)
		}
	}
	if due := nudgeDue(e, s, loc); !due.IsZero() && !due.After(now) {
		f["entry.nudge_due_at"] = due
	}
	if p.d.Turns != nil {
		n, err := p.d.Turns(ctx, task.UnitOf(e.ID))
		if err != nil {
			return nil, err
		}
		f["entry.turns"] = n
	}
	us := unitsOf(e)
	if len(us) == 0 {
		return f, nil
	}
	eu := us[0]
	f["entry.unit"], f["entry.unit_provider"] = eu.String(), string(eu.Provider)
	names, providers := make([]string, len(us)), []string{}
	for i, u := range us {
		names[i] = u.String()
		if !slices.Contains(providers, string(u.Provider)) {
			providers = append(providers, string(u.Provider))
		}
	}
	f["entry.units"], f["entry.unit_providers"] = names, providers
	ended, err := p.ended(ctx, e, us)
	if err != nil {
		return nil, err
	}
	f["entry.units_ended"] = ended
	if p.d.Cards != nil {
		uf, ok, err := p.d.Cards.UnitFacts(ctx, eu)
		if err != nil {
			return nil, err
		}
		if ok {
			f["entry.unit_started_by_me"], f["entry.unit_closure_posted"] = uf.StartedByMe, uf.ClosurePosted
		}
	}
	got, err := p.d.Items.Load(ctx, us)
	if err != nil {
		return nil, err
	}
	var open, resolved, owed int
	for _, u := range us {
		for _, it := range got[u] {
			switch it.State {
			case items.Open:
				open++
			case items.Resolved:
				resolved++
			}
		}
		owed += len(held(e, got[u]))
	}
	f["entry.items_open"], f["entry.items_resolved"], f["entry.items_owed"] = open, resolved, owed
	return f, nil
}

// ended says whether every unit of us but the entry's own has ended, with
// at least one such unit.
func (p *Provider) ended(ctx context.Context, e task.Task, us []unit.Unit) (bool, error) {
	if p.d.Ended == nil {
		return false, nil
	}
	n := 0
	for _, u := range us {
		if u == task.UnitOf(e.ID) {
			continue
		}
		done, err := p.d.Ended(ctx, u)
		if err != nil || !done {
			return false, err
		}
		n++
	}
	return n > 0, nil
}

// Scope sets entry.settled_at to the newest settled draft workflow wf
// proposed for the entry.
func (p *Provider) Scope(ctx context.Context, wf string, u unit.Unit, f workflow.Facts) (workflow.Facts, error) {
	if p.d.Drafts == nil {
		return f, nil
	}
	ds, err := p.d.Drafts.List(ctx, outbox.Filter{Task: u.Key, Workflow: wf})
	if err != nil {
		return nil, err
	}
	out := maps.Clone(f)
	out["entry.settled_at"] = draft.SettledAt(ds, wf, u)
	return out, nil
}

// Owners are the entry's units, whose timelines show the steps run on it.
func (p *Provider) Owners(ctx context.Context, u unit.Unit) ([]unit.Unit, error) {
	e, err := p.task(ctx, u, true)
	if errors.Is(err, automation.ErrUnitGone) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return unitsOf(e), nil
}

// unitsOf are an entry's units in order: those attached to it, the unit it
// was captured or derived on first, then those only its items name.
func unitsOf(e task.Task) []unit.Unit {
	out := slices.Clone(e.Units)
	for _, r := range e.Items {
		if !slices.Contains(out, r.Unit) {
			out = append(out, r.Unit)
		}
	}
	return out
}

// held are the open items of its that entry e holds.
func held(e task.Task, its []items.Item) []items.Item {
	var out []items.Item
	for _, it := range its {
		if it.State == items.Open && slices.Contains(e.Items, task.ItemRef{Unit: it.Unit, Item: it.ID}) {
			out = append(out, it)
		}
	}
	return out
}

func resurface(e task.Task) time.Time {
	if e.Waiting == nil {
		return time.Time{}
	}
	return e.Waiting.Resurface
}

// nudgeDue is when waiting entry e is due a nudge: its resurface date when
// set, else StaleDays working days after it started waiting, and no sooner
// than RenudgeAfter after its last nudge; zero when it waits on nobody or
// has no date to wait for.
func nudgeDue(e task.Task, s Settings, loc *time.Location) time.Time {
	w := e.Waiting
	if w == nil || e.State != task.Waiting || len(w.On) == 0 {
		return time.Time{}
	}
	due := w.Resurface
	if due.IsZero() {
		if s.StaleDays <= 0 || w.Since.IsZero() {
			return time.Time{}
		}
		due = task.AddWorkdays(w.Since, s.StaleDays, loc)
	}
	if !w.LastNudge.IsZero() {
		if again := w.LastNudge.Add(s.RenudgeAfter); again.After(due) {
			due = again
		}
	}
	return due
}

// workdaysSince is the most working days n with since moved n working days
// no later than now.
func workdaysSince(since, now time.Time, loc *time.Location) int {
	n := 0
	for !task.AddWorkdays(since, n+1, loc).After(now) {
		n++
	}
	return n
}
