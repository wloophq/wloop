package tasks

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

// Deps is what the service runs on.
type Deps struct {
	Store Store
	Items Items
	Facts Facts
	Me    task.Me
	Clock port.Clock
	// States are the profile's states and verbs; the service reads a task's category from its state.
	States States
	// Disabled are the providers whose plugin is disabled now; nil: none is.
	Disabled func() []unit.ProviderID
	// Authority says who may run each verb, as the profile's by does; nil, with no profile at hand: a person
	// may run every verb and no other actor any.
	Authority Authority
	// Gates opens the gate a gated verb opens; nil: a gated verb fails.
	Gates Gates
}

type handlerKey struct {
	class   string
	handler string
}

// EveryKind registers a handler for the stored tasks no class-specific handler serves.
const EveryKind = "*"

// classOf is what e's verb handlers are registered under: the origin of a task another service serves, a
// draft or a message, else its kind property.
func classOf(e task.Task) string {
	if e.Origin == task.OriginDraft || e.Origin == task.OriginMessage {
		return e.Origin
	}
	return e.Prop(task.PropKind).Text
}

type Service struct {
	d Deps
	// mu serializes the service's read-modify-write of entries.
	mu hookedMutex
	// beforeApply, set by tests only, runs in Do between reading the task and writing it.
	beforeApply func()
	reg         sync.RWMutex
	handlers    map[handlerKey]VerbHandler
	views       map[task.View]viewSource
	authority   Authority
}

func New(d Deps) *Service {
	s := &Service{d: d, handlers: map[handlerKey]VerbHandler{}, views: map[task.View]viewSource{}, authority: d.Authority}
	if s.authority == nil {
		s.authority = personOnly
	}
	return s
}

func personOnly(_ context.Context, _ string, a task.Actor, _ task.Task) (bool, bool, error) {
	return a == task.ActorPerson, false, nil
}

func (s *Service) now() time.Time { return s.d.Clock.Now() }

// Register makes h run, before any state change, on the tasks of class the verbs whose profile handler is
// handler, and a verb with no handler named so (a kind's own dismiss or done). A class is a value of the kind
// property, the origin task.OriginDraft or task.OriginMessage of a task another service serves, or EveryKind.
func (s *Service) Register(class, handler string, h VerbHandler) {
	s.reg.Lock()
	defer s.reg.Unlock()
	s.handlers[handlerKey{class, handler}] = h
}

// handler is the registered handler of verb on e: the one its profile handler names, else for a verb with
// none the one its own name does; of e's class, else, for a stored task, of EveryKind.
func (s *Service) handler(e task.Task, verb task.Verb) VerbHandler {
	name := cmp.Or(s.d.States.Handler(string(verb)), string(verb))
	class := classOf(e)
	s.reg.RLock()
	defer s.reg.RUnlock()
	if h, ok := s.handlers[handlerKey{class, name}]; ok || class == task.OriginDraft || class == task.OriginMessage {
		return h
	}
	return s.handlers[handlerKey{EveryKind, name}]
}

// Do runs req.Verb on entry id: a registered handler first, else the state change. An entry of a unit whose
// plugin is disabled refuses every verb with ErrPluginDisabled but dismiss, which moves a derived entry, or one
// whose units are all of disabled plugins, to the Trash whole, and detaches a disabled plugin's units from any other.
func (s *Service) Do(ctx context.Context, id task.ID, req Request) (Result, error) {
	if _, _, ok := s.d.States.Move(string(req.Verb)); !ok {
		return Result{}, fmt.Errorf("%w: unknown verb %q", ErrArgs, req.Verb)
	}
	if src, ok := s.sourceOf(id); ok {
		e, found, err := src.Task(ctx, id)
		if err != nil {
			return Result{}, err
		}
		if !found {
			return Result{}, fmt.Errorf("%w: %s", ErrNoTask, id)
		}
		h := s.handler(e, req.Verb)
		if h == nil {
			return Result{}, fmt.Errorf("%w: %s on a %s task", ErrVerb, req.Verb, classOf(e))
		}
		if gated, err := s.Allowed(ctx, e, req); err != nil || gated {
			return s.gated(ctx, e, req, err)
		}
		return h(ctx, e, req)
	}
	e, err := s.d.Store.Task(ctx, id)
	if err != nil {
		return Result{}, err
	}
	enabled := map[unit.Unit]bool{}
	if err := s.writable(ctx, e, enabled); err != nil {
		if req.Verb != task.VerbDismiss || !errors.Is(err, ErrPluginDisabled) {
			return Result{}, err
		}
		if gated, err := s.Allowed(ctx, e, req); err != nil || gated {
			return s.gated(ctx, e, req, err)
		}
		return s.dismissOff(ctx, id, req, enabled)
	}
	if err := s.declared(e, req.Verb); err != nil {
		return Result{}, err
	}
	if gated, err := s.Allowed(ctx, e, req); err != nil || gated {
		return s.gated(ctx, e, req, err)
	}
	if h := s.handler(e, req.Verb); h != nil {
		return h(ctx, e, req)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, err = s.d.Store.Task(ctx, id); err != nil {
		return Result{}, err
	}
	now := s.now()
	var cs []task.Change
	if s.d.States.Handler(string(req.Verb)) == handlerProject {
		cs, err = s.project(ctx, e, req, now)
	} else {
		var next task.Task
		next, err = s.transition(e, req, now)
		cs = []task.Change{{Op: task.OpUpdate, Task: next, Why: string(req.Verb)}}
	}
	if err != nil {
		return Result{}, err
	}
	if s.beforeApply != nil {
		s.beforeApply()
	}
	if err := s.d.Store.Apply(ctx, cs, s.verbEvents(e, req, now)...); err != nil {
		return Result{}, err
	}
	return Result{Task: cs[len(cs)-1].Task}, nil
}

// project attaches e to the project args["id"] names, or to a new one titled args["title"], after its last action.
func (s *Service) project(ctx context.Context, e task.Task, req Request, now time.Time) ([]task.Change, error) {
	if err := s.accepts(e, req.Verb); err != nil {
		return nil, err
	}
	args := req.Args
	var cs []task.Change
	var p task.Task
	switch id, title := args["id"], strings.TrimSpace(args["title"]); {
	case id != "":
		got, err := s.d.Store.Task(ctx, task.ID(id))
		if err != nil {
			return nil, err
		}
		if !got.IsProject() {
			return nil, fmt.Errorf("%w: %s is not a project", ErrArgs, id)
		}
		p = got
	case title != "":
		p = task.Task{ID: task.NewID(), Title: title, Sequential: args["sequential"] == "true", Origin: task.OriginUser, Created: now, Updated: now}
		if err := p.MoveTo(task.CategoryReady, s.d.States); err != nil {
			return nil, err
		}
		p.SetProp(task.PropKind, task.Value{Text: task.KindProject}, task.WriterWorkflow, now)
		p.SetProp(task.PropArea, e.Prop(task.PropArea), task.WriterPerson, now)
		cs = append(cs, task.Change{Op: task.OpCreate, Task: p, Why: "project"})
	default:
		return nil, fmt.Errorf("%w: project needs id or title", ErrArgs)
	}
	open, err := s.d.Store.Tasks(ctx, task.Query{View: task.ViewOpen})
	if err != nil {
		return nil, err
	}
	last := 0
	for _, o := range open {
		if o.Parent == p.ID && o.ID != e.ID {
			last = max(last, o.Position)
		}
	}
	e.Parent, e.Position, e.Updated = p.ID, last+1, now
	if pos, err := strconv.Atoi(args["position"]); err == nil {
		e.Position = pos
	}
	return append(cs, task.Change{Op: task.OpUpdate, Task: e, Why: "project"}), nil
}

// dismissOff dismisses entry id, which has a unit or an item of a disabled plugin, with no handler: it detaches
// the disabled plugins' units and the items on them, and the entry keeps its state. A derived entry, whose id the
// unit it came from names, or one with no unit of an enabled plugin moves to the Trash whole. enabled caches the
// answers per unit.
func (s *Service) dismissOff(ctx context.Context, id task.ID, req Request, enabled map[unit.Unit]bool) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, err := s.d.Store.Task(ctx, id)
	if err != nil {
		return Result{}, err
	}
	if err := s.accepts(e, task.VerbDismiss); err != nil {
		return Result{}, err
	}
	units := slices.Clone(e.Units)
	for _, r := range e.Items {
		units = append(units, r.Unit)
	}
	var off []unit.ProviderID
	for _, u := range units {
		on, seen := enabled[u]
		if !seen {
			if _, on, err = s.d.Facts.UnitFacts(ctx, u); err != nil {
				return Result{}, err
			}
			enabled[u] = on
		}
		if !on && !slices.Contains(off, u.Provider) {
			off = append(off, u.Provider)
		}
	}
	isOff := func(u unit.Unit) bool { return slices.Contains(off, u.Provider) }
	now := s.now()
	next, why := e, "detached"
	if len(off) == 0 || e.Origin == task.OriginDerived || !slices.ContainsFunc(e.Units, func(u unit.Unit) bool { return !isOff(u) }) {
		if next, err = s.transition(e, req, now); err != nil {
			return Result{}, err
		}
		why = string(task.VerbDismiss)
	} else {
		next.Units = slices.DeleteFunc(slices.Clone(e.Units), isOff)
		next.Items = slices.DeleteFunc(slices.Clone(e.Items), func(r task.ItemRef) bool { return isOff(r.Unit) })
		next.Updated = now
		for _, p := range off {
			why += " " + string(p)
		}
	}
	if err := s.d.Store.Apply(ctx, []task.Change{{Op: task.OpUpdate, Task: next, Why: why}}, s.verbEvents(e, req, now)...); err != nil {
		return Result{}, err
	}
	return Result{Task: next}, nil
}

// RecordVerb records that req's actor ran req's verb on e, once the handler
// or the service that runs it succeeded.
func (s *Service) RecordVerb(ctx context.Context, e task.Task, req Request) error {
	evs := s.verbEvents(e, req, s.now())
	if len(evs) == 0 {
		return nil
	}
	return s.d.Store.Apply(ctx, nil, evs...)
}

// verbEvents are the event of req's verb on e: its actor as "<actor>:<source>", and as data the state it left
// with req's chain. None for a verb whose handler is seen, which clients send on their own.
func (s *Service) verbEvents(e task.Task, req Request, now time.Time) []port.Event {
	if s.d.States.Handler(string(req.Verb)) == handlerSeen {
		return nil
	}
	return []port.Event{{Unit: task.UnitOf(e.ID), Kind: task.VerbEventKind(req.Verb), Actor: string(req.Actor) + ":" + req.Source,
		Data: workflow.ChainData(string(e.State), req.Chain), At: now}}
}

// gated opens the gate req's verb names on e, for a person to approve, and leaves e as it is; err, the
// authority's refusal, is returned as it is.
func (s *Service) gated(ctx context.Context, e task.Task, req Request, err error) (Result, error) {
	if err != nil {
		return Result{}, err
	}
	if s.d.Gates == nil {
		return Result{}, fmt.Errorf("%w: %s opens a gate, and no gates are served", ErrVerb, req.Verb)
	}
	g, _ := s.d.States.Gate(string(req.Verb))
	subject, err := json.Marshal(proposal{Verb: req.Verb, Args: req.Args})
	if err != nil {
		return Result{}, err
	}
	g.Task, g.Unit, g.Subject = e.ID, task.UnitOf(e.ID), string(subject)
	held, err := s.d.Gates.OfTask(ctx, e.ID)
	if err != nil {
		return Result{}, err
	}
	if slices.ContainsFunc(held, func(o gate.Gate) bool { return o.Status == gate.Open && o.Name == g.Name && o.Subject == g.Subject }) {
		return Result{Task: e}, nil
	}
	if _, err := s.d.Gates.Open(ctx, g); err != nil {
		return Result{}, err
	}
	return Result{Task: e}, nil
}

// Allowed says whether req's actor may run req's verb on t, and whether running it opens the verb's gate
// instead, as the profile's authority says: ErrForbidden for a request with no actor or one the verb's by
// leaves out. A request an approved gate of t proposed (ProposedVerb) is allowed without the by.
func (s *Service) Allowed(ctx context.Context, t task.Task, req Request) (gated bool, err error) {
	if req.Actor == "" {
		return false, fmt.Errorf("%w: %s names no actor", ErrForbidden, req.Verb)
	}
	if req.Approved == 0 {
		return s.allowed(ctx, t, req.Verb, req.Actor)
	}
	if s.d.Gates == nil {
		return false, fmt.Errorf("%w: gate %d: no gates are served", ErrForbidden, req.Approved)
	}
	g, err := s.d.Gates.Get(ctx, req.Approved)
	if err != nil {
		return false, err
	}
	p, ok := ProposedVerb(g)
	if g.Status != gate.Approved || g.Task != t.ID || !ok || p.Verb != req.Verb {
		return false, fmt.Errorf("%w: gate %d does not approve %s on %s", ErrForbidden, req.Approved, req.Verb, t.ID)
	}
	return false, nil
}

// proposal is the verb a gate a gated verb opened proposes, as its Subject keeps it.
type proposal struct {
	Verb task.Verb         `json:"verb"`
	Args map[string]string `json:"args,omitempty"`
}

// ProposedVerb is the request a gate a gated verb opened proposes, run as the person who approved it: Do runs
// it once the gate is approved, without the verb's by; false for a gate no verb opened.
func ProposedVerb(g gate.Gate) (Request, bool) {
	var p proposal
	if json.Unmarshal([]byte(g.Subject), &p) != nil || p.Verb == "" {
		return Request{}, false
	}
	return Request{Verb: p.Verb, Args: p.Args, Actor: task.ActorPerson, Source: "gate", Approved: g.ID}, true
}

// writable refuses an entry with a unit whose plugin is disabled; ok caches the answers per unit.
func (s *Service) writable(ctx context.Context, e task.Task, ok map[unit.Unit]bool) error {
	for _, u := range e.Units {
		enabled, seen := ok[u]
		if !seen {
			_, got, err := s.d.Facts.UnitFacts(ctx, u)
			if err != nil {
				return err
			}
			enabled = got
			if ok != nil {
				ok[u] = got
			}
		}
		if !enabled {
			return fmt.Errorf("%w: %s", ErrPluginDisabled, u.Provider)
		}
	}
	return nil
}

// Capture files a new entry in the Inbox.
func (s *Service) Capture(ctx context.Context, title, notes string, u *unit.Unit) (task.Task, error) {
	if strings.TrimSpace(title) == "" {
		return task.Task{}, fmt.Errorf("%w: a capture needs a title", ErrArgs)
	}
	return s.create(ctx, title, notes, u, task.OriginUser, nil)
}

// Open creates a task another service owns the content of, such as a gate's: Inbox, random id. Its kind is
// left to the profile.
func (s *Service) Open(ctx context.Context, r OpenRequest) (task.Task, error) {
	return s.create(ctx, r.Title, "", r.Unit, cmp.Or(r.Origin, task.OriginMessage), r.Contexts)
}

func (s *Service) create(ctx context.Context, title, notes string, u *unit.Unit, origin string, contexts []string) (task.Task, error) {
	now := s.now()
	e := task.Task{ID: task.NewID(), Title: title, Notes: notes, Origin: origin, Created: now, Updated: now}
	if err := e.MoveTo(task.CategoryInbox, s.d.States); err != nil {
		return task.Task{}, err
	}
	e.SetProp(task.PropContexts, task.Value{List: slices.Clone(contexts)}, task.WriterPlugin, now)
	if u != nil && !u.IsZero() {
		e.Units = []unit.Unit{*u}
	}
	if err := s.d.Store.Apply(ctx, []task.Change{{Op: task.OpCreate, Task: e, Why: string(origin)}}); err != nil {
		return task.Task{}, err
	}
	return e, nil
}

// Close moves an entry opened by Open or Capture into the done or the dropped category; a derived entry, or one
// whose unit's plugin is disabled, refuses it.
func (s *Service) Close(ctx context.Context, id task.ID, to task.Category) error {
	if to != task.CategoryDone && to != task.CategoryDropped {
		return fmt.Errorf("%w: close to %s", ErrArgs, to)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, err := s.d.Store.Task(ctx, id)
	if err != nil {
		return err
	}
	if e.Origin == task.OriginDerived {
		return fmt.Errorf("%w: close on a derived entry", ErrVerb)
	}
	if err := s.writable(ctx, e, nil); err != nil {
		return err
	}
	if e.Category == to {
		return nil
	}
	now := s.now()
	if err := e.MoveTo(to, s.d.States); err != nil {
		return err
	}
	e.Snooze, e.Updated = task.Snooze{}, now
	return s.d.Store.Apply(ctx, []task.Change{{Op: task.OpResolve, Task: e, Why: "closed"}})
}

// Retitle renames an entry opened by Open or Capture, as one change; a derived entry refuses it.
func (s *Service) Retitle(ctx context.Context, id task.ID, title string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, err := s.d.Store.Task(ctx, id)
	if err != nil {
		return err
	}
	if e.Origin == task.OriginDerived {
		return fmt.Errorf("%w: retitle on a derived entry", ErrVerb)
	}
	if e.Title == title {
		return nil
	}
	e.Title, e.Updated = title, s.now()
	return s.d.Store.Apply(ctx, []task.Change{{Op: task.OpUpdate, Task: e, Why: "retitled"}})
}

// pending folds the changes of one Sync by entry, in first-seen order.
type pending struct {
	order []task.ID
	byID  map[task.ID]task.Change
}

func (p *pending) put(c task.Change) {
	if p.byID == nil {
		p.byID = map[task.ID]task.Change{}
	}
	old, ok := p.byID[c.Task.ID]
	if !ok {
		p.order = append(p.order, c.Task.ID)
	} else {
		if old.Op == task.OpCreate {
			c.Op = task.OpCreate
		}
		c.Why = old.Why + "; " + c.Why
	}
	p.byID[c.Task.ID] = c
}

func (p *pending) overlay(es []task.Task) []task.Task {
	out := slices.Clone(es)
	have := map[task.ID]bool{}
	for i, e := range out {
		have[e.ID] = true
		if c, ok := p.byID[e.ID]; ok {
			out[i] = c.Task
		}
	}
	for _, id := range p.order {
		if !have[id] {
			out = append(out, p.byID[id].Task)
		}
	}
	return out
}

// of is es with this Sync's changes over them, plus the entries it created that belong to u.
func (p *pending) of(es []task.Task, u unit.Unit) []task.Task {
	return slices.DeleteFunc(p.overlay(es), func(e task.Task) bool { return !belongs(e, u) })
}

func belongs(e task.Task, u unit.Unit) bool {
	return slices.Contains(e.Units, u) || slices.ContainsFunc(e.Items, func(r task.ItemRef) bool { return r.Unit == u })
}

func (p *pending) changes() []task.Change {
	out := make([]task.Change, 0, len(p.order))
	for _, id := range p.order {
		out = append(out, p.byID[id])
	}
	return out
}

// Sync reconciles the units named by a TopicItems, TopicSourceEvents or TopicCards note, then Wake for all,
// in one Apply. A unit whose plugin is disabled keeps its entries as they are. A snooze that ends
// reconciles its entry's units after waking it, so a still-muted unit snoozes it again before anything sees it
// in the Inbox.
func (s *Service) Sync(ctx context.Context, units []unit.Unit) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	open, err := s.d.Store.Tasks(ctx, task.Query{View: task.ViewOpen})
	if err != nil {
		return err
	}
	var p pending
	enabled := map[unit.Unit]bool{}
	units = slices.Clone(units)
	for _, e := range open {
		woke, ok, err := task.Wake(e, s.d.States, now)
		if err != nil {
			return fmt.Errorf("waking %s: %w", e.ID, err)
		}
		if !ok {
			continue
		}
		switch err := s.writable(ctx, e, enabled); {
		case errors.Is(err, ErrPluginDisabled):
			continue
		case err != nil:
			return err
		}
		p.put(task.Change{Op: task.OpUpdate, Task: woke, Why: "snooze ended"})
		units = append(units, e.Units...)
	}
	seen := map[unit.Unit]bool{}
	for _, u := range units {
		if seen[u] {
			continue
		}
		seen[u] = true
		f, ok, err := s.d.Facts.UnitFacts(ctx, u)
		if err != nil {
			return fmt.Errorf("facts of %s: %w", u, err)
		}
		enabled[u] = ok
		if !ok {
			continue
		}
		f.Unit = u
		its, err := s.d.Items.Items(ctx, u)
		if err != nil {
			return fmt.Errorf("items of %s: %w", u, err)
		}
		have, err := s.d.Store.TasksOf(ctx, u)
		if err != nil {
			return err
		}
		cs, err := task.Reconcile(f, its, s.d.Me, p.of(have, u), s.d.States, now)
		if err != nil {
			return fmt.Errorf("the tasks of %s: %w", u, err)
		}
		for _, c := range cs {
			p.put(c)
		}
		if cs, err = task.Activity(f, p.of(have, u), s.d.States, now); err != nil {
			return fmt.Errorf("the activity on %s: %w", u, err)
		}
		for _, c := range cs {
			p.put(c)
		}
	}
	for _, e := range p.overlay(open) {
		if !e.Open() {
			continue
		}
		next, why := e, []string{}
		woke, ok, err := task.Wake(next, s.d.States, now)
		if err != nil {
			return fmt.Errorf("waking %s: %w", e.ID, err)
		}
		if ok {
			next, why = woke, append(why, "snooze ended")
		}
		if len(why) == 0 {
			continue
		}
		switch err := s.writable(ctx, e, enabled); {
		case errors.Is(err, ErrPluginDisabled):
		case err != nil:
			return err
		default:
			p.put(task.Change{Op: task.OpUpdate, Task: next, Why: strings.Join(why, ", ")})
		}
	}
	if len(p.order) == 0 {
		return nil
	}
	return s.d.Store.Apply(ctx, p.changes())
}

// Recategorize gives every stored task the category of its state as the profile declares it now, and returns
// the states stored tasks hold that the profile does not declare; those tasks keep their category. A store
// that keeps no categories recategorizes nothing.
func (s *Service) Recategorize(ctx context.Context) ([]string, error) {
	r, ok := s.d.Store.(Recategorizer)
	if !ok {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return r.Recategorize(ctx, s.d.States.CategoryOf)
}

// Attach adds u to entry id's units, after those it holds; a unit it holds already changes nothing.
func (s *Service) Attach(ctx context.Context, id task.ID, u unit.Unit) error {
	if u.IsZero() || u == task.UnitOf(id) {
		return fmt.Errorf("%w: attach %s to entry %s", ErrArgs, u, id)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, err := s.d.Store.Task(ctx, id)
	if err != nil {
		return err
	}
	if slices.Contains(e.Units, u) {
		return nil
	}
	e.Units, e.Updated = append(slices.Clone(e.Units), u), s.now()
	return s.d.Store.Apply(ctx, []task.Change{{Op: task.OpUpdate, Task: e, Why: "attached " + u.String()}})
}

// UnitClosed ends the open tasks that hold u, its closure posted: each moves into the done category.
func (s *Service) UnitClosed(ctx context.Context, u unit.Unit) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	es, err := s.d.Store.TasksOf(ctx, u)
	if err != nil {
		return err
	}
	now := s.now()
	var cs []task.Change
	for _, e := range es {
		if !e.Open() || !slices.Contains(e.Units, u) {
			continue
		}
		if err := e.MoveTo(task.CategoryDone, s.d.States); err != nil {
			return err
		}
		e.Snooze, e.Updated = task.Snooze{}, now
		cs = append(cs, task.Change{Op: task.OpResolve, Task: e, Why: "its unit " + u.String() + " closed"})
	}
	if len(cs) == 0 {
		return nil
	}
	return s.d.Store.Apply(ctx, cs)
}
