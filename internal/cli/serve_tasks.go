package cli

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/catalog"
	"github.com/wloophq/wloop/internal/config"
	coreitems "github.com/wloophq/wloop/internal/core/items"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/core/wakes"
	"github.com/wloophq/wloop/internal/daemon"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/wake"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

// newTasks is the daemon's one tasks service, built on first use; its unit facts come from the stored cards.
func (e *Env) newTasks(db *sqlite.DB, cfg config.Config, its coretasks.Items) *coretasks.Service {
	if e.tasks == nil {
		e.tasksMe = &identity{}
		e.pluginsOff = &atomic.Pointer[[]unit.ProviderID]{}
		off := func() []unit.ProviderID { return offProviders(cfg, e.pluginsOff.Load()) }
		enabled := func(p unit.ProviderID) bool { return !slices.Contains(off(), p) }
		facts := coretasks.CardFacts{Cards: db.Cards(), Enabled: enabled, Me: e.tasksMe.get, Kind: e.pluginTaskKind}
		e.wakes = wakes.New(wakes.Deps{Store: db.Wakes(), Tasks: db.Tasks(), Holds: catalog.Holds(e.loadedIntents, e.factsOfTask),
			Clock: clockFunc(e.Now)})
		e.tasks = coretasks.New(coretasks.Deps{Store: db.Tasks(), Items: its, Facts: facts, Clock: clockFunc(e.Now),
			Disabled: off, States: profileStates{e},
			Authority: catalog.Authority(e.loadedIntents), Gates: serviceTaskGates{e, db}, Drafts: taskDrafts{e},
			Wakes: db.Wakes(), Baseline: catalog.Baseline(e.loadedIntents, e.factsOfTask),
			Views: lensViews(e.loadedIntents), ViewEval: e.lens().Eval, ViewSort: e.lens().Sort,
			Activity: func(ctx context.Context, id task.ID) error {
				return e.wakes.Event(ctx, id, wake.OnActivity, "")
			}})
	}
	return e.tasks
}

// factsOfTask are task t's facts a wake's condition reads: the task subject's, else those its own fields hold.
func (e *Env) factsOfTask(ctx context.Context, t task.Task) (workflow.Facts, error) {
	if e.wakeFacts != nil {
		return e.wakeFacts(ctx, t)
	}
	return catalog.TaskFacts(t, e.loadedIntents().Tasks), nil
}

// taskDrafts settles the held drafts of a task a verb ended, but those of the purposes the verb's keep_drafts names.
type taskDrafts struct{ e *Env }

func (d taskDrafts) Ended(ctx context.Context, t task.Task, v task.Verb) error {
	if d.e.outbox == nil {
		return nil
	}
	_, err := d.e.outbox.Ended(ctx, t.ID, d.e.loadedIntents().Tasks.Verbs[string(v)].KeepDrafts)
	return err
}

// profileStates are the states and verbs of the profile as it loads now.
type profileStates struct{ e *Env }

func (p profileStates) CategoryOf(state string) (task.Category, bool) {
	return p.e.loadedIntents().Tasks.CategoryOf(state)
}

func (p profileStates) FirstOf(c task.Category) string { return p.e.loadedIntents().Tasks.FirstOf(c) }

func (p profileStates) Move(verb string) ([]string, string, bool) {
	return p.e.loadedIntents().Tasks.Move(verb)
}

func (p profileStates) Handler(verb string) string { return p.e.loadedIntents().Tasks.Handler(verb) }

func (p profileStates) PropOf(r task.Role) (string, []string) {
	return p.e.loadedIntents().Tasks.PropOf(r)
}

// Gate is the gate of gates.toml verb's gate names, with its kind and intents.
func (p profileStates) Gate(verb string) (gate.Gate, bool) {
	l := p.e.loadedIntents()
	name := l.Tasks.Verbs[verb].Gate
	d, ok := l.Gates.Gate(name)
	if name == "" || !ok {
		return gate.Gate{}, false
	}
	g := gate.Gate{Name: d.Name, Kind: d.Kind}
	for _, in := range d.Intents {
		g.Intents = append(g.Intents, in.Name)
	}
	return g, true
}

// offProviders are the plugins cfg disables and those the daemon serves disabled (built), once serveTasks
// knows them.
func offProviders(cfg config.Config, built *[]unit.ProviderID) []unit.ProviderID {
	var out []unit.ProviderID
	for _, id := range cfg.PluginSwitches() {
		if !cfg.PluginEnabled(id) {
			out = append(out, unit.ProviderID(id))
		}
	}
	if built != nil {
		for _, p := range *built {
			if !slices.Contains(out, p) {
				out = append(out, p)
			}
		}
	}
	return out
}

// serveTasks serves the tasks on d: the user's identity from every built module's Identity, and the units a
// full Sync covers. The daemon's Sync worker also runs under --no-jobs, since it only reads and writes the store.
func (e *Env) serveTasks(d daemon.Deps, db *sqlite.DB, cfg config.Config, mods []pluginModule, logf func(string, ...any)) daemon.Deps {
	ledger := e.itemsLedger(db, cfg, logf)
	g := e.newTasks(db, cfg, ledger)
	var off []unit.ProviderID
	for _, p := range d.Plugins {
		if !p.Enabled {
			off = append(off, p.ID)
		}
	}
	e.pluginsOff.Store(&off)
	if e.Turns != nil {
		e.Turns.Results = e.newTaskResults(g, mods)
	}
	me := e.tasksMe
	d.Tasks = g
	d.Wakes = e.wakes
	d.TasksMe = func(ctx context.Context) (bool, error) {
		got, err := meOf(ctx, mods)
		grew := me.set(got)
		g.SetMe(me.get())
		return grew, err
	}
	d.TaskUnits = func(ctx context.Context) ([]unit.Unit, error) { return syncUnits(ctx, db.Tasks(), ledger) }
	return d
}

// meOf is the user over every module with an Identity: each one's key and aliases. A module that cannot say
// yet is left out and named in the error.
func meOf(ctx context.Context, mods []pluginModule) (task.Me, error) {
	var me task.Me
	var errs []error
	var codecs []unit.Codec
	for _, m := range mods {
		codecs = append(codecs, m.Codec)
	}
	me.Codecs = unit.NewRegistry(codecs...)
	for _, m := range mods {
		if m.Identity == nil {
			continue
		}
		p, err := m.Identity(ctx)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", m.ID, err))
			continue
		}
		for _, k := range append([]string{p.Key}, p.Aliases...) {
			if k != "" && !slices.Contains(me.Keys, k) {
				me.Keys = append(me.Keys, k)
			}
		}
	}
	return me, errors.Join(errs...)
}

// identity is the user's identity as far as the plugins said it; it only grows while the daemon runs.
type identity struct {
	mu sync.RWMutex
	me task.Me
}

func (i *identity) get() task.Me {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return task.Me{Keys: slices.Clone(i.me.Keys), Codecs: i.me.Codecs}
}

func (i *identity) set(me task.Me) (grew bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if me.Codecs != nil {
		i.me.Codecs = me.Codecs
	}
	for _, k := range me.Keys {
		if !slices.Contains(i.me.Keys, k) {
			i.me.Keys, grew = append(i.me.Keys, k), true
		}
	}
	return grew
}

// syncUnits are the units with an open item or a live task.
func syncUnits(ctx context.Context, tasks coretasks.Reader, ledger *coreitems.Ledger) ([]unit.Unit, error) {
	open, err := ledger.Query(ctx, coreitems.Query{State: items.Open})
	if err != nil {
		return nil, err
	}
	seen := map[unit.Unit]bool{}
	var out []unit.Unit
	add := func(u unit.Unit) {
		if !seen[u] {
			seen[u] = true
			out = append(out, u)
		}
	}
	for _, it := range open {
		add(it.Unit)
	}
	live, err := tasks.Tasks(ctx, task.Query{})
	if err != nil {
		return nil, err
	}
	for _, e := range live {
		for _, u := range e.Units {
			add(u)
		}
		for _, r := range e.Items {
			add(r.Unit)
		}
	}
	return out, nil
}

// envWakes are the tasks as a plugin's wakes move them, through the tasks service once serve built it.
type envWakes struct {
	e  *Env
	db *sqlite.DB
}

func (w envWakes) TasksOf(ctx context.Context, u unit.Unit) ([]task.Task, error) {
	return w.db.Tasks().TasksOf(ctx, u)
}

func (w envWakes) Sleep(ctx context.Context, id task.ID, k wake.Wake) error {
	if w.e.tasks == nil {
		return errors.New("no tasks service: the daemon keeps the wakes")
	}
	return w.e.tasks.Sleep(ctx, id, k)
}

func (w envWakes) Rouse(ctx context.Context, id task.ID) error {
	if w.e.tasks == nil {
		return errors.New("no tasks service: the daemon keeps the wakes")
	}
	return w.e.tasks.Rouse(ctx, id)
}
