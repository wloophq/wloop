// Package facttask puts the tasks behind the engine's Units, as the
// core-kept task provider: every task neither done nor in the trash, of
// providers whose plugin is enabled, with its facts in slot task.
package facttask

import (
	"cmp"
	"context"
	"errors"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/core/port"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/wake"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

// Tasks reads the tasks (coretasks.Store).
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

// Gates reads a task's gates (gates.Reader).
type Gates interface {
	OfTask(ctx context.Context, id task.ID) ([]gate.Gate, error)
}

// Settings are the config values the facts read.
type Settings struct {
	// StaleDays is tasks.stale_waiting_days: working days after task.owed_since that a task someone else owes
	// is due a check, when it has no check_on property; 0 checks on the property only.
	StaleDays int
	// RenudgeAfter is drafts.repeat_after: how long a nudge keeps the task quiet.
	RenudgeAfter time.Duration
}

type Deps struct {
	Tasks Tasks
	Items Items
	// Cards are the facts of the tasks' units; nil: none.
	Cards coretasks.Facts
	// Drafts are read for task.settled_at; nil: always zero.
	Drafts Drafts
	// Enabled says whether a provider's plugin is enabled; nil: every one is.
	Enabled  func(unit.ProviderID) bool
	Settings func() Settings
	// Ended says whether a unit has ended, as its plugin says; nil: none has.
	Ended func(ctx context.Context, u unit.Unit) (bool, error)
	// Repo is the repository of a unit, owner/name: its placement's, else its plugin's; "" none, and nil none
	// at all.
	Repo func(ctx context.Context, u unit.Unit) (string, error)
	// Gates are read for task.gates_open; nil: always zero.
	Gates Gates
	// Turns counts the turns run on a unit; nil: none.
	Turns func(ctx context.Context, u unit.Unit) (int, error)
	// Sessions are the sessions not closed, for task.runs_live; nil: none.
	Sessions func(ctx context.Context) ([]turn.Session, error)
	// Clock's location is where working days are counted.
	Clock port.Clock
	// Props are the task.prop.* facts of a task, typed as tasks.toml declares them; nil: none.
	Props func(t task.Task) workflow.Facts
	// Me is the user's identity, whom task.link_owes names me; nil: nobody.
	Me func() task.Me
	// OwedBy is who owes a task's next step over its other facts, as the profile says; nil: nobody.
	OwedBy func(ctx context.Context, f workflow.Facts) (string, error)
	// Wakes are the tasks' wakes, for task.wake_until and task.wake_when; nil: none has one.
	Wakes Wakes
	// Links are what a unit's plugin reports of it, under the host's overlays, for task.link_signal.* and
	// task.since_movement; nil: every signal off and no unit ever moved.
	Links func(ctx context.Context, u unit.Unit) (Link, error)
	// Extraction is when the oldest message of units still waiting to be extracted arrived, as their plugins'
	// extraction health says, for task.extraction_lag; zero when none waits, and nil none ever does.
	Extraction func(ctx context.Context, units []unit.Unit) (time.Time, error)
	// RoleProp is the property role r acts on, "" for none; nil: none.
	RoleProp func(r task.Role) string
}

// Link is what the facts read of one unit a task links.
type Link struct {
	// Signals are the link signals on or off, by name; one no plugin declares is ignored.
	Signals map[string]bool
	// Moved is when the unit last moved: its situation began or a run on it changed it; zero when unknown.
	Moved time.Time
}

// Wakes reads a task's wake; false when it has none.
type Wakes interface {
	Of(ctx context.Context, id task.ID) (wake.Wake, bool, error)
}

// Provider is the task subject's facts provider. It names the task's own
// unit as the subject's owner and wakes the sweep at the next check_on, or
// at the end of a nudge's quiet time.
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

func (p *Provider) enabled(id unit.ProviderID) bool { return p.d.Enabled == nil || p.d.Enabled(id) }

func (p *Provider) settings() Settings {
	if p.d.Settings == nil {
		return Settings{}
	}
	return p.d.Settings()
}

// listed says whether e is a subject: an open task of no unit, or of a unit
// whose plugin is enabled.
func (p *Provider) listed(e task.Task) bool { return e.Open() && p.enabledFor(e) }

func (p *Provider) enabledFor(e task.Task) bool {
	u, ok := e.FirstUnit()
	return !ok || p.enabled(u.Provider)
}

// List is every listed task, by id, and keeps the earliest time after now
// of a check_on property or of a nudge's quiet time ending, for Due.
func (p *Provider) List(ctx context.Context, _ unit.ProviderID) ([]unit.Unit, error) {
	es, err := p.d.Tasks.Tasks(ctx, task.Query{})
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
		for _, t := range []time.Time{e.Prop(task.PropCheckOn).Time, quietUntil(e, s)} {
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

// Due is the earliest time the last List saw ahead.
func (p *Provider) Due() time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.due
}

// task is u's task; ErrUnitGone when it is gone, or no longer listed
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

// Facts are the task's facts in slot task; task.settled_at is zero until
// Scope reads the workflow's drafts.
func (p *Provider) Facts(ctx context.Context, u unit.Unit) (workflow.Facts, error) {
	return p.facts(ctx, u, false)
}

// TaskFacts are the facts of task e, as Facts reads them of its unit.
func (p *Provider) TaskFacts(ctx context.Context, e task.Task) (workflow.Facts, error) {
	f, err := p.base(ctx, e)
	if err != nil {
		return nil, err
	}
	return p.owed(ctx, e, f)
}

// EventFacts are the facts of a task an event names, also once done or
// dismissed.
func (p *Provider) EventFacts(ctx context.Context, u unit.Unit) (workflow.Facts, error) {
	return p.facts(ctx, u, true)
}

func (p *Provider) facts(ctx context.Context, u unit.Unit, anyState bool) (workflow.Facts, error) {
	e, err := p.task(ctx, u, anyState)
	if err != nil {
		return nil, err
	}
	f, err := p.base(ctx, e)
	if err != nil {
		return nil, err
	}
	return p.owed(ctx, e, f)
}

func (p *Provider) base(ctx context.Context, e task.Task) (workflow.Facts, error) {
	f := workflow.Facts{
		"task.id":                  string(e.ID),
		"task.state":               string(e.State),
		"task.unit":                "",
		"task.unit_provider":       "",
		"task.units":               []string{},
		"task.unit_providers":      []string{},
		"task.link_ended":          false,
		"task.captured_by_user":    e.CapturedByUser(),
		"task.turns":               0,
		"task.link_started_by_me":  false,
		"task.link_closure_posted": false,
		"task.items_open":          0,
		"task.items_resolved":      0,
		"task.settled_at":          time.Time{},
		"task.fingerprint":         string(e.ID) + "|" + string(e.State) + "|" + e.Updated.UTC().Format(time.RFC3339Nano),
		"task.category":            string(e.Category),
		"task.owed_by":             "",
		"task.owed_since":          time.Time{},
		"task.since_movement":      time.Duration(0),
		"task.check_on":            e.Prop(task.PropCheckOn).Time,
		"task.gates_open":          0,
		"task.runs_live":           0,
		"task.wake_until":          time.Time{},
		"task.wake_when":           "",
		"task.repo":                "",
		"task.sequential_next":     false,
		"task.review_due":          false,
		"task.children_ready":      false,
		"task.ask_age":             time.Duration(0),
		"task.askers":              []string{},
		"task.promised":            time.Time{},
		"task.extraction_lag":      time.Duration(0),
		"task.link_owes":           []string{},
		"task.link_signals":        []string{},
	}
	for _, name := range builtin.LinkSignals {
		f[builtin.LinkSignalPrefix+name] = false
	}
	if p.d.RoleProp != nil {
		if name := p.d.RoleProp(task.RolePromise); name != "" {
			f["task.promised"] = e.Prop(name).Time
		}
	}
	if p.d.Props != nil {
		maps.Copy(f, p.d.Props(e))
	}
	if p.d.Wakes != nil {
		w, ok, err := p.d.Wakes.Of(ctx, e.ID)
		if err != nil {
			return nil, err
		}
		if ok {
			f["task.wake_until"], f["task.wake_when"] = w.Until, w.When
		}
	}
	if err := p.family(ctx, e, f); err != nil {
		return nil, err
	}
	var err error
	if p.d.Turns != nil {
		n, err := p.d.Turns(ctx, task.UnitOf(e.ID))
		if err != nil {
			return nil, err
		}
		f["task.turns"] = n
	}
	gs, err := p.gates(ctx, e.ID)
	if err != nil {
		return nil, err
	}
	f["task.gates_open"] = len(gs)
	if p.d.Sessions != nil {
		n, err := p.runsLive(ctx, task.UnitOf(e.ID))
		if err != nil {
			return nil, err
		}
		f["task.runs_live"] = n
	}
	us := unitsOf(e)
	if err := p.links(ctx, e, us, f); err != nil {
		return nil, err
	}
	if len(us) == 0 {
		return f, nil
	}
	eu := us[0]
	f["task.unit"], f["task.unit_provider"] = eu.String(), string(eu.Provider)
	names, providers := make([]string, len(us)), []string{}
	for i, u := range us {
		names[i] = u.String()
		if !slices.Contains(providers, string(u.Provider)) {
			providers = append(providers, string(u.Provider))
		}
	}
	f["task.units"], f["task.unit_providers"] = names, providers
	ended, err := p.ended(ctx, e, us)
	if err != nil {
		return nil, err
	}
	f["task.link_ended"] = ended
	if f["task.repo"], err = p.repo(ctx, us); err != nil {
		return nil, err
	}
	if p.d.Cards != nil {
		uf, ok, err := p.d.Cards.UnitFacts(ctx, eu)
		if err != nil {
			return nil, err
		}
		if ok {
			f["task.link_started_by_me"], f["task.link_closure_posted"] = uf.StartedByMe, uf.ClosurePosted
		}
	}
	got, err := p.d.Items.Load(ctx, us)
	if err != nil {
		return nil, err
	}
	var open, resolved int
	for _, u := range us {
		for _, it := range got[u] {
			switch it.State {
			case items.Open:
				open++
			case items.Resolved:
				resolved++
			}
		}
	}
	f["task.items_open"], f["task.items_resolved"] = open, resolved
	f["task.link_owes"] = p.linkOwes(e, us, got)
	f["task.ask_age"], f["task.askers"] = p.asks(e, us, got)
	if p.d.Extraction != nil {
		oldest, err := p.d.Extraction(ctx, us)
		if err != nil {
			return nil, err
		}
		if !oldest.IsZero() {
			f["task.extraction_lag"] = max(p.d.Clock.Now().Sub(oldest), 0)
		}
	}
	return f, nil
}

// asks are the age of the oldest open item e's units hold that waits on me, and the requesters of those items,
// each once; the items are those task.link_owes reads.
func (p *Provider) asks(e task.Task, us []unit.Unit, got map[unit.Unit][]items.Item) (time.Duration, []string) {
	var me task.Me
	if p.d.Me != nil {
		me = p.d.Me()
	}
	var oldest time.Time
	askers := []string{}
	for _, u := range us {
		for _, it := range owing(e, u, got[u]) {
			if !slices.ContainsFunc(it.WaitingOn, func(who items.Person) bool { return who.Is(me) }) {
				continue
			}
			if !it.CreatedAt.IsZero() && (oldest.IsZero() || it.CreatedAt.Before(oldest)) {
				oldest = it.CreatedAt
			}
			if k := it.Requester.Key; k != "" && !it.Requester.Is(me) && !slices.Contains(askers, k) {
				askers = append(askers, k)
			}
		}
	}
	if oldest.IsZero() {
		return 0, askers
	}
	return max(p.d.Clock.Now().Sub(oldest), 0), askers
}

// owing are the open items of its, u's, that e's facts read: those e holds, or every open one of a unit e holds
// no item of.
func owing(e task.Task, u unit.Unit, its []items.Item) []items.Item {
	if slices.ContainsFunc(e.Items, func(r task.ItemRef) bool { return r.Unit == u }) {
		return held(e, its)
	}
	return slices.DeleteFunc(slices.Clone(its), func(it items.Item) bool { return it.State != items.Open })
}

// links sets task.link_signal.<name>, on when the signal is on for any of us, task.link_signals, the declared
// signals any of us reports, and task.since_movement, the time
// since the newest of e's own last change and its units' last moves.
func (p *Provider) links(ctx context.Context, e task.Task, us []unit.Unit, f workflow.Facts) error {
	moved := e.Updated
	reported := []string{}
	defer func() { slices.Sort(reported); f["task.link_signals"] = reported }()
	if p.d.Links != nil {
		for _, u := range us {
			l, err := p.d.Links(ctx, u)
			if err != nil {
				return err
			}
			for name, on := range l.Signals {
				if _, declared := f[builtin.LinkSignalPrefix+name]; !declared {
					continue
				}
				f[builtin.LinkSignalPrefix+name] = f[builtin.LinkSignalPrefix+name] == true || on
				if !slices.Contains(reported, name) {
					reported = append(reported, name)
				}
			}
			if l.Moved.After(moved) {
				moved = l.Moved
			}
		}
	}
	if !moved.IsZero() {
		f["task.since_movement"] = max(p.d.Clock.Now().Sub(moved), 0)
	}
	return nil
}

// family sets the facts of e's place among its parent's children and of its own children:
// task.sequential_next, e is its sequential parent's first open child by sibling order; task.children_ready,
// an open child of e is in the ready category; task.review_due, e is an open project whose review is due. The
// tasks are the pass's when the service evaluates a view, else the store's open ones, read only for a task
// with a parent or a project.
func (p *Provider) family(ctx context.Context, e task.Task, f workflow.Facts) error {
	if e.Parent == "" && !e.IsProject() {
		return nil
	}
	f["task.review_due"] = e.IsProject() && e.Open() && e.ReviewDue(p.d.Clock.Now())
	var all []task.Task
	if pass, ok := coretasks.PassOf(ctx); ok {
		all = pass.Tasks
	} else {
		var err error
		if all, err = p.d.Tasks.Tasks(ctx, task.Query{}); err != nil {
			return err
		}
	}
	var first *task.Task
	sequential := false
	for i, o := range all {
		switch {
		case o.ID == e.Parent:
			sequential = o.Sequential && o.IsProject()
		case !o.Open():
		case o.Parent == e.ID && o.Category == task.CategoryReady:
			f["task.children_ready"] = true
		}
		if e.Parent != "" && o.Parent == e.Parent && o.Open() &&
			(first == nil || cmp.Or(cmp.Compare(o.Position, first.Position), cmp.Compare(o.ID, first.ID)) < 0) {
			first = &all[i]
		}
	}
	f["task.sequential_next"] = sequential && first != nil && first.ID == e.ID
	return nil
}

// repo is the repository of the first unit of us that has one.
func (p *Provider) repo(ctx context.Context, us []unit.Unit) (string, error) {
	if p.d.Repo == nil {
		return "", nil
	}
	for _, u := range us {
		r, err := p.d.Repo(ctx, u)
		if err != nil || r != "" {
			return r, err
		}
	}
	return "", nil
}

// ended says whether every unit of us but the task's own has ended, with
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

// Scope sets task.settled_at to the newest settled draft workflow wf
// proposed for the task.
func (p *Provider) Scope(ctx context.Context, wf string, u unit.Unit, f workflow.Facts) (workflow.Facts, error) {
	if p.d.Drafts == nil {
		return f, nil
	}
	ds, err := p.d.Drafts.List(ctx, outbox.Filter{Task: u.Key, Workflow: wf})
	if err != nil {
		return nil, err
	}
	out := maps.Clone(f)
	out["task.settled_at"] = draft.SettledAt(ds, wf, u)
	return out, nil
}

// Owners are the task's units, whose timelines show the steps run on it.
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

// unitsOf are a task's units in order: those attached to it, the unit it
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

// held are the open items of its that task e holds.
func held(e task.Task, its []items.Item) []items.Item {
	var out []items.Item
	for _, it := range its {
		if it.State == items.Open && slices.Contains(e.Items, task.ItemRef{Unit: it.Unit, Item: it.ID}) {
			out = append(out, it)
		}
	}
	return out
}

// quietUntil is when the quiet time of e's last nudge ends; zero when it was never nudged.
func quietUntil(e task.Task, s Settings) time.Time {
	at := e.Prop(task.PropLastNudge).Time
	if at.IsZero() {
		return time.Time{}
	}
	return at.Add(s.RenudgeAfter)
}

// linkOwes are who the open items e holds wait on, each once, me for the user; of a unit e holds no item of,
// every open item of the unit.
func (p *Provider) linkOwes(e task.Task, us []unit.Unit, got map[unit.Unit][]items.Item) []string {
	var me task.Me
	if p.d.Me != nil {
		me = p.d.Me()
	}
	out := []string{}
	for _, u := range us {
		for _, it := range owing(e, u, got[u]) {
			for _, who := range it.WaitingOn {
				k := who.Key
				if who.Is(me) {
					k = "me"
				}
				if k != "" && !slices.Contains(out, k) {
					out = append(out, k)
				}
			}
		}
	}
	return out
}

// owed sets the facts e's owed_by makes over f, its other facts: task.owed_by, as the profile says;
// task.owed_since, the oldest of the open items e holds waiting on whoever owes it, else for me its oldest
// open gate, else e's creation; and task.check_on, its check_on property, else, when what e holds waits on
// someone else and not on me, StaleDays working days after the oldest of those items. The check date reads
// the items alone, so a gate e's own nudge opens leaves it as it was.
func (p *Provider) owed(ctx context.Context, e task.Task, f workflow.Facts) (workflow.Facts, error) {
	if p.d.OwedBy == nil {
		return f, nil
	}
	who, err := p.d.OwedBy(ctx, f)
	if err != nil {
		return nil, err
	}
	f["task.owed_by"] = who
	if who == "" {
		return f, nil
	}
	since, err := p.owedSince(ctx, e, who)
	if err != nil {
		return nil, err
	}
	f["task.owed_since"] = since
	links, _ := f["task.link_owes"].([]string)
	s, now := p.settings(), p.d.Clock.Now()
	if !e.Prop(task.PropCheckOn).Time.IsZero() || !FallsBack(links, s.StaleDays) {
		return f, nil
	}
	if links[0] != who {
		if since, err = p.owedSince(ctx, e, links[0]); err != nil {
			return nil, err
		}
	}
	f["task.check_on"] = task.AddWorkdays(since, s.StaleDays, now.Location())
	return f, nil
}

// FallsBack says whether a task with no check_on property takes the check date StaleDays working days after
// what its links owe: they owe someone else and not the user.
func FallsBack(linkOwes []string, staleDays int) bool {
	return staleDays > 0 && len(linkOwes) > 0 && !slices.Contains(linkOwes, "me")
}

func (p *Provider) owedSince(ctx context.Context, e task.Task, who string) (time.Time, error) {
	var me task.Me
	if p.d.Me != nil {
		me = p.d.Me()
	}
	got, err := p.d.Items.Load(ctx, unitsOf(e))
	if err != nil {
		return time.Time{}, err
	}
	var since time.Time
	earlier := func(t time.Time) {
		if !t.IsZero() && (since.IsZero() || t.Before(since)) {
			since = t
		}
	}
	for _, its := range got {
		for _, it := range held(e, its) {
			if slices.ContainsFunc(it.WaitingOn, func(w items.Person) bool { return w.Key == who || who == "me" && w.Is(me) }) {
				earlier(it.CreatedAt)
			}
		}
	}
	if since.IsZero() && who == "me" {
		gs, err := p.gates(ctx, e.ID)
		if err != nil {
			return time.Time{}, err
		}
		for _, g := range gs {
			earlier(g.OpenedAt)
		}
	}
	if since.IsZero() {
		since = e.Created
	}
	return since, nil
}

// gates are the open gates of task id.
func (p *Provider) gates(ctx context.Context, id task.ID) ([]gate.Gate, error) {
	if p.d.Gates == nil {
		return nil, nil
	}
	gs, err := p.d.Gates.OfTask(ctx, id)
	return slices.DeleteFunc(gs, func(g gate.Gate) bool { return g.Status != gate.Open }), err
}

// runsLive counts the sessions of u with a live process.
func (p *Provider) runsLive(ctx context.Context, u unit.Unit) (int, error) {
	all, err := p.d.Sessions(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, s := range all {
		if s.Unit == u && s.State() == turn.SessionActive {
			n++
		}
	}
	return n, nil
}
