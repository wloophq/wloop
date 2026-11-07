package tasks

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/wake"
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
	// Ending settles what a task that ends holds; nil settles nothing.
	Ending Ending
	// Wakes are the tasks' wakes, which schedule puts and wake reads; nil: schedule fails and no task has one.
	Wakes Wakes
	// Baseline fills a wake Sleep puts on task t: a When that asks to wake once a fact of t changes becomes the
	// condition over the value t has now; nil leaves every When as it is.
	Baseline func(ctx context.Context, t task.Task, when string) (string, error)
	// Views are the profile's views as it loads now; nil lists none.
	Views func() Views
	// ViewEval places a stored task in the profile's views; nil places none.
	ViewEval ViewEval
	// ViewSort orders a view's tasks as the profile sorts it; nil keeps their base order.
	ViewSort ViewSort
	// Activity is told each task Sync finds asleep with a person's activity on its unit since the user saw it,
	// for a wake on activity; nil: none is told.
	Activity func(ctx context.Context, id task.ID) error
	// Survives picks the task a merge keeps, as the merge verb's survivor says; nil: the older stays.
	Survives Survives
	// GateText words the gate a gated verb opens; nil writes none.
	GateText GateText
	// TakenApart holds the pairs the user took apart; nil holds none.
	TakenApart TakenApart
	// Gen is the store's change generation; with it, the reads that start while it holds share one pass for
	// up to passReuse. nil: every read has a pass of its own.
	Gen func() port.Generation
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
	// mu serializes the service's read-modify-write of tasks.
	mu hookedMutex
	// beforeApply, set by tests only, runs in Do between reading the task and writing it.
	beforeApply func()
	reg         sync.RWMutex
	handlers    map[handlerKey]VerbHandler
	views       map[string]viewSource
	authority   Authority
	// runs keeps the review's runs; nil until ServeProjects serves the review.
	runs ProjectStore
	// review serializes the review's read-modify-write of its run.
	review sync.Mutex
	shared sharedPass
}

func New(d Deps) *Service {
	s := &Service{d: d, handlers: map[handlerKey]VerbHandler{}, views: map[string]viewSource{}, authority: d.Authority}
	if s.authority == nil {
		s.authority = userOnly
	}
	s.serveMerges()
	return s
}

// userOnly lets the user run every verb: a person, or an agent, which reaches the service only through a session
// the user drives.
func userOnly(_ context.Context, _ string, a task.Actor, _ task.Task) (bool, bool, error) {
	return a == task.ActorPerson || a == task.ActorAgent, false, nil
}

func (s *Service) now() time.Time { return s.d.Clock.Now() }

// Register makes h run, before any state change, on the tasks of class the verbs whose profile handler is
// handler. A class is a value of the kind property, the origin task.OriginDraft or task.OriginMessage of a task
// another service serves, or EveryKind.
func (s *Service) Register(class, handler string, h VerbHandler) {
	s.reg.Lock()
	defer s.reg.Unlock()
	s.handlers[handlerKey{class, handler}] = h
}

// handler is the registered handler of verb on e, the one its profile handler names: of e's class, else, for
// a stored task, of EveryKind; nil for a verb that names none.
func (s *Service) handler(e task.Task, verb task.Verb) VerbHandler {
	name := s.d.States.Handler(string(verb))
	if name == "" {
		return nil
	}
	class := classOf(e)
	s.reg.RLock()
	defer s.reg.RUnlock()
	if h, ok := s.handlers[handlerKey{class, name}]; ok || class == task.OriginDraft || class == task.OriginMessage {
		return h
	}
	return s.handlers[handlerKey{EveryKind, name}]
}

// Do runs req.Verb on task id: a registered handler first, else the state change. A task of a unit whose
// plugin is disabled refuses every verb with ErrPluginDisabled but one whose handler is drop, which moves a
// derived task, or one whose units are all of disabled plugins, to the Trash whole, and detaches a disabled
// plugin's units from any other.
func (s *Service) Do(ctx context.Context, id task.ID, req Request) (Result, error) {
	prev, r, err := s.do(ctx, id, req)
	if err != nil || s.d.Ending == nil || !prev.Open() {
		return r, err
	}
	next := r.Task
	if next.ID == "" {
		if next, err = s.d.Store.Task(ctx, id); err != nil {
			return r, nil
		}
	}
	s.settle(ctx, []task.Task{next}, req.Verb, req.Actor, req.Session)
	return r, nil
}

// settle hands Ending each of es that ended, by verb v of by, or by no verb when v is empty; it runs with the
// lock released, since settling a draft or a gate can reach back into the service.
func (s *Service) settle(ctx context.Context, es []task.Task, v task.Verb, by task.Actor, session string) {
	if s.d.Ending == nil {
		return
	}
	for _, e := range es {
		if !e.Open() {
			s.d.Ending.Ended(ctx, e, v, by, session)
		}
	}
}

// do runs req.Verb on task id as Do says, returning the task as it was before the verb.
func (s *Service) do(ctx context.Context, id task.ID, req Request) (task.Task, Result, error) {
	if _, _, ok := s.d.States.Move(string(req.Verb)); !ok {
		return task.Task{}, Result{}, fmt.Errorf("%w: unknown verb %q", ErrArgs, req.Verb)
	}
	if src, ok := s.sourceOf(id); ok {
		e, found, err := src.Task(ctx, id)
		if err != nil {
			return e, Result{}, err
		}
		if !found {
			return e, Result{}, fmt.Errorf("%w: %s", ErrNoTask, id)
		}
		h := s.handler(e, req.Verb)
		if h == nil {
			return e, Result{}, fmt.Errorf("%w: %s on a %s task", ErrVerb, req.Verb, classOf(e))
		}
		if gated, err := s.Allowed(ctx, e, req); err != nil || gated {
			r, err := s.gated(ctx, e, req, err)
			return e, r, err
		}
		r, err := h(ctx, e, req)
		return e, r, err
	}
	e, err := s.d.Store.Task(ctx, id)
	if err != nil {
		return e, Result{}, err
	}
	if req, err = s.Addressed(ctx, id, e, req); err != nil {
		return e, Result{}, err
	}
	enabled := map[unit.Unit]bool{}
	if err := s.writable(ctx, e, enabled); err != nil {
		if s.d.States.Handler(string(req.Verb)) != HandlerDrop || !errors.Is(err, ErrPluginDisabled) {
			return e, Result{}, err
		}
		if gated, err := s.Allowed(ctx, e, req); err != nil || gated {
			r, err := s.gated(ctx, e, req, err)
			return e, r, err
		}
		r, err := s.dismissOff(ctx, id, req, enabled)
		return e, r, err
	}
	if err := s.declared(e, req.Verb); err != nil {
		return e, Result{}, err
	}
	if gated, err := s.Allowed(ctx, e, req); err != nil || gated {
		r, err := s.gated(ctx, e, req, err)
		return e, r, err
	}
	if err := s.heldOpen(ctx, e, req); err != nil {
		return e, Result{}, err
	}
	if h := s.handler(e, req.Verb); h != nil {
		r, err := h(ctx, e, req)
		return e, r, err
	}
	prev, next, err := s.change(ctx, id, req)
	return prev, Result{Task: next}, err
}

// Addressed is req as it may run on e, the task id reads as: a verb sent to the id of a task merged into e is
// refused with ErrMerged, but split, which splits that task out when it names no other task; a verb sent to e's own
// id, or to an id an older alias leads to, runs as sent.
func (s *Service) Addressed(ctx context.Context, id task.ID, e task.Task, req Request) (Request, error) {
	if e.ID == id {
		return req, nil
	}
	r, merged, err := s.MergeOf(ctx, id)
	if err != nil || !merged {
		return req, err
	}
	if other := req.Args[task.ArgTask]; s.d.States.Handler(string(req.Verb)) != HandlerSplit || other != "" && other != string(id) {
		return req, fmt.Errorf("%w: %s was merged into %s; %s runs on %s", ErrMerged, id, r.Into, req.Verb, e.ID)
	}
	if req.Args[task.ArgTask] == "" {
		req.Args = maps.Clone(req.Args)
		if req.Args == nil {
			req.Args = map[string]string{}
		}
		req.Args[task.ArgTask] = string(id)
	}
	return req, nil
}

// change applies req.Verb's state change to task id under the lock, returning the task before and after it.
func (s *Service) change(ctx context.Context, id task.ID, req Request) (task.Task, task.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, err := s.d.Store.Task(ctx, id)
	if err != nil {
		return task.Task{}, task.Task{}, err
	}
	now := s.now()
	var cs []task.Change
	var wc wakeChange
	if s.d.States.Handler(string(req.Verb)) == handlerProject {
		cs, err = s.project(ctx, e, req, now)
	} else {
		var cur wake.Wake
		if cur, err = s.wakeOf(ctx, e.ID); err != nil {
			return task.Task{}, task.Task{}, err
		}
		var next task.Task
		next, wc, err = s.transition(e, req, cur, now)
		cs = []task.Change{{Op: task.OpUpdate, Task: next, Why: string(req.Verb)}}
	}
	if err != nil {
		return task.Task{}, task.Task{}, err
	}
	if wc.put != nil && s.d.Wakes == nil {
		return task.Task{}, task.Task{}, fmt.Errorf("%w: %s puts a wake, and no wakes are kept", ErrVerb, req.Verb)
	}
	if s.beforeApply != nil {
		s.beforeApply()
	}
	if err := s.d.Store.Apply(ctx, cs, s.verbEvents(e, req, now)...); err != nil {
		return task.Task{}, task.Task{}, err
	}
	if err := s.applyWake(ctx, e.ID, wc); err != nil {
		return task.Task{}, task.Task{}, err
	}
	return e, cs[len(cs)-1].Task, nil
}

// wakeOf is task id's wake, zero when it has none or no wakes are kept.
func (s *Service) wakeOf(ctx context.Context, id task.ID) (wake.Wake, error) {
	if s.d.Wakes == nil {
		return wake.Wake{}, nil
	}
	w, _, err := s.d.Wakes.Of(ctx, id)
	return w, err
}

// WakeOf is task id's wake; false when it has none.
func (s *Service) WakeOf(ctx context.Context, id task.ID) (wake.Wake, bool, error) {
	if s.d.Wakes == nil {
		return wake.Wake{}, false, nil
	}
	return WakeIn(ctx, s.d.Wakes, id)
}

func (s *Service) applyWake(ctx context.Context, id task.ID, wc wakeChange) error {
	switch {
	case wc.put != nil:
		return s.d.Wakes.Put(ctx, *wc.put)
	case wc.drop && s.d.Wakes != nil:
		return s.d.Wakes.Drop(ctx, id)
	}
	return nil
}

// Sleep defers open task id under w: a task not deferred moves to the first deferred state, and w returns it
// where it was; one deferred already keeps where its wake returned it, else its state. It is how a plugin's
// wake, such as a mute, holds a task.
func (s *Service) Sleep(ctx context.Context, id task.ID, w wake.Wake) error {
	if s.d.Wakes == nil {
		return fmt.Errorf("%w: no wakes are kept", ErrVerb)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, err := s.d.Store.Task(ctx, id)
	if err != nil {
		return err
	}
	if !e.Open() {
		return fmt.Errorf("%w: a wake on a %s task", ErrVerb, e.State)
	}
	cur, err := s.wakeOf(ctx, id)
	if err != nil {
		return err
	}
	w.Task, w.Return = id, string(e.State)
	if e.Snoozed() && cur.Task == id {
		w.Return = cur.Return
	}
	if s.d.Baseline != nil && w.When != "" {
		if w.When, err = s.d.Baseline(ctx, e, w.When); err != nil {
			return err
		}
	}
	if e.Category != task.CategoryDeferred {
		if err := e.MoveTo(task.CategoryDeferred, s.d.States); err != nil {
			return err
		}
		e.Asleep, e.Updated = true, s.now()
		if err := s.d.Store.Apply(ctx, []task.Change{{Op: task.OpUpdate, Task: e, Why: "asleep"}}); err != nil {
			return err
		}
	}
	return s.d.Wakes.Put(ctx, w)
}

// Rouse drops task id's wake and returns the task where the wake said, as the wake verb does; a task with no
// wake is left as it is.
func (s *Service) Rouse(ctx context.Context, id task.ID) error {
	if s.d.Wakes == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok, err := s.d.Wakes.Of(ctx, id)
	if err != nil || !ok {
		return err
	}
	e, err := s.d.Store.Task(ctx, id)
	if err != nil {
		return err
	}
	if e.Open() {
		was := e
		if err := returnFrom(&e, w, s.d.States); err != nil {
			return err
		}
		if e.Asleep = false; !e.Same(was) {
			e.Updated = s.now()
			if err := s.d.Store.Apply(ctx, []task.Change{{Op: task.OpUpdate, Task: e, Why: "woke"}}); err != nil {
				return err
			}
		}
	}
	return s.d.Wakes.Drop(ctx, id)
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
		p.SetRole(s.d.States, task.RoleArea, e.Role(s.d.States, task.RoleArea), task.WriterPerson, now)
		cs = append(cs, task.Change{Op: task.OpCreate, Task: p, Why: "project"})
	default:
		return nil, fmt.Errorf("%w: project needs id or title", ErrArgs)
	}
	open, err := s.d.Store.Tasks(ctx, task.Query{})
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

// dismissOff dismisses task id, which has a unit or an item of a disabled plugin, with no handler: it detaches
// the disabled plugins' units and the items on them, and the task keeps its state. A derived task, whose id the
// unit it came from names, or one with no unit of an enabled plugin moves to the Trash whole. enabled caches the
// answers per unit.
func (s *Service) dismissOff(ctx context.Context, id task.ID, req Request, enabled map[unit.Unit]bool) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, err := s.d.Store.Task(ctx, id)
	if err != nil {
		return Result{}, err
	}
	if err := s.accepts(e, req.Verb); err != nil {
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
		if next, _, err = s.transition(e, req, wake.Wake{}, now); err != nil {
			return Result{}, err
		}
		why = string(req.Verb)
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

// verbEvents are the event of req's verb on e: its actor as "<actor>:<source>" and the session an agent ran it
// through, and as data the state it left with req's chain. None for a verb whose handler is seen, which clients send on their own.
func (s *Service) verbEvents(e task.Task, req Request, now time.Time) []port.Event {
	if s.d.States.Handler(string(req.Verb)) == handlerSeen {
		return nil
	}
	return []port.Event{{Unit: task.UnitOf(e.ID), Kind: task.VerbEventKind(req.Verb), Actor: string(req.Actor) + ":" + req.Source,
		Session: req.Session, Data: workflow.ChainData(string(e.State), req.Chain), At: now}}
}

// heldOpen refuses, with ErrVerb, a verb anyone but the user, a person or an agent through a session they drive,
// runs that would end e while a gate holds it.
func (s *Service) heldOpen(ctx context.Context, e task.Task, req Request) error {
	if req.Actor == task.ActorPerson || req.Actor == task.ActorAgent && req.Session != "" || !e.Open() || !s.ends(req.Verb) {
		return nil
	}
	if held, err := s.holds(ctx, e.ID); err != nil || !held {
		return err
	}
	return fmt.Errorf("%w: %s by %s: task %s holds an open gate for a person", ErrVerb, req.Verb, req.Actor, e.ID)
}

// ends says whether verb moves a task into a category that ends it.
func (s *Service) ends(verb task.Verb) bool {
	_, to, _ := s.d.States.Move(string(verb))
	c, ok := s.d.States.CategoryOf(to)
	return ok && (c == task.CategoryDone || c == task.CategoryDropped)
}

func (s *Service) holds(ctx context.Context, id task.ID) (bool, error) {
	if s.d.Gates == nil {
		return false, nil
	}
	return s.d.Gates.HoldsTask(ctx, id)
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
	p := proposal{Verb: req.Verb, Args: req.Args}
	if req.Actor == task.ActorWorkflow {
		p.From = req.Source
		if req.Chain.Depth > 0 {
			p.Chain = &req.Chain
		}
	}
	subject, err := json.Marshal(p)
	if err != nil {
		return Result{}, err
	}
	g.Task, g.Unit, g.Subject = e.ID, task.UnitOf(e.ID), string(subject)
	held, err := s.d.Gates.OfTask(ctx, e.ID)
	if err != nil {
		return Result{}, err
	}
	if slices.ContainsFunc(held, func(o gate.Gate) bool { return o.Status == gate.Open && o.Name == g.Name && sameProposal(o, p) }) {
		return Result{Task: e}, nil
	}
	if other := task.ID(req.Args[task.ArgTask]); other != "" {
		theirs, err := s.d.Gates.OfTask(ctx, other)
		if err != nil {
			return Result{}, err
		}
		if slices.ContainsFunc(theirs, func(o gate.Gate) bool {
			return o.Status == gate.Open && o.Name == g.Name && proposes(o, req.Verb, e.ID)
		}) {
			return Result{Task: e}, nil
		}
	}
	if s.d.GateText != nil {
		w, err := s.gateWords(ctx, e, req)
		if err != nil {
			return Result{}, err
		}
		if g.Text, err = s.d.GateText(ctx, g, w); err != nil {
			return Result{}, err
		}
	}
	wait, err := s.pairTakenApart(ctx, e, task.ID(req.Args[task.ArgTask]))
	if err != nil {
		return Result{}, err
	}
	if _, err := s.d.Gates.Open(ctx, g, wait); err != nil {
		return Result{}, err
	}
	return Result{Task: e}, nil
}

// gateWords are what the words of the gate req's verb opens on e read.
func (s *Service) gateWords(ctx context.Context, e task.Task, req Request) (GateWords, error) {
	w := GateWords{Verb: string(req.Verb), Task: e}
	id := req.Args[task.ArgTask]
	if id == "" {
		return w, nil
	}
	o, err := s.d.Store.Task(ctx, task.ID(id))
	if err != nil {
		return GateWords{}, err
	}
	w.Other = o
	if s.d.States.Handler(string(req.Verb)) == HandlerMerge {
		if w.Keep, w.Fold, err = s.survivor(ctx, req.Verb, e, o); err != nil {
			return GateWords{}, err
		}
	}
	return w, nil
}

// pairTakenApart says whether the user took e and other apart, so a gate proposing the pair waits for them.
func (s *Service) pairTakenApart(ctx context.Context, e task.Task, other task.ID) (bool, error) {
	if other == "" || s.d.TakenApart == nil {
		return false, nil
	}
	return s.d.TakenApart(ctx, e, other)
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
	if own, isTask := task.IDOf(g.Unit); isTask && own != t.ID {
		return false, fmt.Errorf("%w: gate %d proposed %s on %s, which was merged into %s since", ErrForbidden, req.Approved, req.Verb, own, t.ID)
	}
	if other := task.ID(p.Args[task.ArgTask]); other != "" {
		o, err := s.d.Store.Task(ctx, other)
		if err != nil && !errors.Is(err, ErrNoTask) {
			return false, err
		}
		if err != nil || o.ID != other || req.Args[task.ArgTask] != string(other) {
			return false, fmt.Errorf("%w: gate %d proposed %s of %s and %s, a pair that changed since", ErrForbidden, req.Approved, req.Verb, t.ID, other)
		}
		if req.Actor != task.ActorPerson {
			apart, err := s.pairTakenApart(ctx, t, other)
			if err != nil {
				return false, err
			}
			if apart {
				return false, fmt.Errorf("%w: gate %d proposed %s of %s and %s, a pair you took apart since", ErrForbidden, req.Approved, req.Verb, t.ID, other)
			}
		}
	}
	return false, nil
}

// proposes says whether gate g proposes verb with the other task id.
func proposes(g gate.Gate, verb task.Verb, id task.ID) bool {
	p, ok := ProposedVerb(g)
	return ok && p.Verb == verb && p.Args[task.ArgTask] == string(id)
}

// sameProposal says whether gate g proposes p's verb with p's arguments, whoever asked for it.
func sameProposal(g gate.Gate, p proposal) bool {
	o, ok := proposalOf(g)
	return ok && o.Verb == p.Verb && maps.Equal(o.Args, p.Args)
}

// proposal is the verb a gate a gated verb opened proposes, as its Subject keeps it, and the workflow that
// asked for it, "" for a person or an agent, with the chain of workflows that led it there.
type proposal struct {
	Verb  task.Verb         `json:"verb"`
	Args  map[string]string `json:"args,omitempty"`
	From  string            `json:"from,omitempty"`
	Chain *workflow.Chain   `json:"chain,omitempty"`
}

func proposalOf(g gate.Gate) (proposal, bool) {
	var p proposal
	if json.Unmarshal([]byte(g.Subject), &p) != nil || p.Verb == "" {
		return proposal{}, false
	}
	return p, true
}

// ProposedVerb is the request a gate a gated verb opened proposes: Do runs it once the gate is approved,
// without the verb's by, as the person who approved it, or as the workflow that asked for it when a rule
// or a window released the gate; false for a gate no verb opened.
func ProposedVerb(g gate.Gate) (Request, bool) {
	p, ok := proposalOf(g)
	if !ok {
		return Request{}, false
	}
	if g.Rule != "" && p.From != "" {
		req := Request{Verb: p.Verb, Args: p.Args, Actor: task.ActorWorkflow, Source: p.From, Approved: g.ID}
		if p.Chain != nil {
			req.Chain = *p.Chain
		}
		return req, true
	}
	return Request{Verb: p.Verb, Args: p.Args, Actor: task.ActorPerson, Source: "gate", Approved: g.ID}, true
}

// ProposalFrom is the workflow that asked for the verb gate g proposes, "" when a person or an agent did or
// no verb opened g.
func ProposalFrom(g gate.Gate) string {
	p, _ := proposalOf(g)
	return p.From
}

// writable refuses a task with a unit whose plugin is disabled; ok caches the answers per unit.
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

// Capture files a new task in the Inbox.
func (s *Service) Capture(ctx context.Context, title, notes string, u *unit.Unit) (task.Task, error) {
	if strings.TrimSpace(title) == "" {
		return task.Task{}, fmt.Errorf("%w: a capture needs a title", ErrArgs)
	}
	return s.create(ctx, title, notes, u, task.OriginUser, nil, "")
}

// Open creates a task another service owns the content of, such as a gate's: Inbox, random id. A task on a
// unit takes the kind its plugin gives its units' tasks (UnitFacts.Kind), as the plugin writes it, so the
// profile's rules for that kind apply to it; any other's kind is left to the profile.
func (s *Service) Open(ctx context.Context, r OpenRequest) (task.Task, error) {
	var kind string
	if r.Unit != nil && !r.Unit.IsZero() && s.d.Facts != nil {
		f, ok, err := s.d.Facts.UnitFacts(ctx, *r.Unit)
		if err != nil {
			return task.Task{}, err
		}
		if ok {
			kind = f.Kind
		}
	}
	return s.create(ctx, r.Title, "", r.Unit, cmp.Or(r.Origin, task.OriginMessage), r.Contexts, kind)
}

func (s *Service) create(ctx context.Context, title, notes string, u *unit.Unit, origin string, contexts []string, kind string) (task.Task, error) {
	now := s.now()
	e := task.Task{ID: task.NewID(), Title: title, Notes: notes, Origin: origin, Created: now, Updated: now}
	if err := e.MoveTo(task.CategoryInbox, s.d.States); err != nil {
		return task.Task{}, err
	}
	e.SetRole(s.d.States, task.RoleContexts, task.Value{List: slices.Clone(contexts)}, task.WriterPlugin, now)
	e.SetProp(task.PropKind, task.Value{Text: kind}, task.WriterPlugin, now)
	cs := []task.Change{{Op: task.OpCreate, Task: e, Why: string(origin)}}
	var evs []port.Event
	if u != nil && !u.IsZero() {
		e.Units = []unit.Unit{*u}
		cs[0].Task = e
		if origin == task.OriginUser {
			evs = createdEvents(cs, now)
		}
	}
	if err := s.d.Store.Apply(ctx, cs, evs...); err != nil {
		return task.Task{}, err
	}
	return e, nil
}

// Close moves a task opened by Open or Capture into the done or the dropped category; a derived task, or one
// whose unit's plugin is disabled, refuses it.
func (s *Service) Close(ctx context.Context, id task.ID, to task.Category) error {
	return s.close(ctx, id, to, nil)
}

// close moves task id into category to, done or dropped. With only, a task only refuses, or no longer stored, is
// left alone, and one of a disabled plugin closes too.
func (s *Service) close(ctx context.Context, id task.ID, to task.Category, only func(task.Task) bool) error {
	if to != task.CategoryDone && to != task.CategoryDropped {
		return fmt.Errorf("%w: close to %s", ErrArgs, to)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, err := s.d.Store.Task(ctx, id)
	if only != nil && (errors.Is(err, ErrNoTask) || err == nil && !only(e)) {
		return nil
	}
	if err != nil {
		return err
	}
	if only == nil {
		if e.Origin == task.OriginDerived {
			return fmt.Errorf("%w: close on a derived task", ErrVerb)
		}
		if err := s.writable(ctx, e, nil); err != nil {
			return err
		}
	}
	if e.Category == to {
		return nil
	}
	now := s.now()
	if err := e.MoveTo(to, s.d.States); err != nil {
		return err
	}
	e.Updated = now
	return s.d.Store.Apply(ctx, []task.Change{{Op: task.OpResolve, Task: e, Why: "closed"}})
}

// Retitle renames a task opened by Open or Capture, as one change; a derived task refuses it.
func (s *Service) Retitle(ctx context.Context, id task.ID, title string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, err := s.d.Store.Task(ctx, id)
	if err != nil {
		return err
	}
	if e.Origin == task.OriginDerived {
		return fmt.Errorf("%w: retitle on a derived task", ErrVerb)
	}
	if e.Title == title {
		return nil
	}
	e.Title, e.Updated = title, s.now()
	return s.d.Store.Apply(ctx, []task.Change{{Op: task.OpUpdate, Task: e, Why: "retitled"}})
}

// pending folds the changes of one Sync by task, in first-seen order.
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

// of is es with this Sync's changes over them, plus the tasks it created that belong to u.
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

// Sync reconciles the units named by a TopicItems, TopicSourceEvents or TopicCards note, in one Apply, then
// tells Activity the asleep tasks of those units a person's activity reached. A unit whose plugin is disabled
// keeps its tasks as they are.
func (s *Service) Sync(ctx context.Context, units []unit.Unit) error {
	ended, err := s.sync(ctx, units)
	s.settle(ctx, ended, "", task.ActorPlugin, "")
	return err
}

// sync is Sync under the lock, returning the open tasks it ended.
func (s *Service) sync(ctx context.Context, units []unit.Unit) ([]task.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	wasOpen := map[task.ID]bool{}
	var p pending
	var roused []task.ID
	seen := map[unit.Unit]bool{}
	for _, u := range units {
		if seen[u] {
			continue
		}
		seen[u] = true
		f, ok, err := s.d.Facts.UnitFacts(ctx, u)
		if err != nil {
			return nil, fmt.Errorf("facts of %s: %w", u, err)
		}
		if !ok {
			continue
		}
		f.Unit = u
		its, err := s.d.Items.Items(ctx, u)
		if err != nil {
			return nil, fmt.Errorf("items of %s: %w", u, err)
		}
		have, err := s.d.Store.TasksOf(ctx, u)
		if err != nil {
			return nil, err
		}
		for _, e := range have {
			wasOpen[e.ID] = wasOpen[e.ID] || e.Open()
		}
		cs, err := task.Reconcile(f, its, s.d.Me, p.of(have, u), s.d.States, now)
		if err != nil {
			return nil, fmt.Errorf("the tasks of %s: %w", u, err)
		}
		for _, c := range cs {
			p.put(c)
		}
		for _, e := range p.of(have, u) {
			if e.Snoozed() && task.ActiveSince(f, e) && !slices.Contains(roused, e.ID) {
				roused = append(roused, e.ID)
			}
		}
		for _, c := range task.Activity(f, its, s.d.Me, p.of(have, u), now) {
			p.put(c)
		}
	}
	var ended []task.Task
	if len(p.order) > 0 {
		cs, err := s.unheld(ctx, p.changes(), wasOpen)
		if err != nil {
			return nil, err
		}
		if len(cs) > 0 {
			if err := s.d.Store.Apply(ctx, cs, createdEvents(cs, now)...); err != nil {
				return nil, err
			}
		}
		for _, c := range cs {
			if wasOpen[c.Task.ID] && !c.Task.Open() {
				ended = append(ended, c.Task)
			}
		}
	}
	if s.d.Activity == nil {
		return ended, nil
	}
	var errs []error
	for _, id := range roused {
		errs = append(errs, s.d.Activity(ctx, id))
	}
	return ended, errors.Join(errs...)
}

// unheld are cs without the changes that would end a task a gate holds open: what the gate asks the person is
// still asked while the unit stays open.
func (s *Service) unheld(ctx context.Context, cs []task.Change, wasOpen map[task.ID]bool) ([]task.Change, error) {
	out := cs[:0:0]
	for _, c := range cs {
		if wasOpen[c.Task.ID] && !c.Task.Open() {
			held, err := s.holds(ctx, c.Task.ID)
			if err != nil {
				return nil, err
			}
			if held {
				continue
			}
		}
		out = append(out, c)
	}
	return out, nil
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
	return r.Recategorize(ctx, s.d.States.CategoryOf, s.now())
}

// Attach adds u to task id's units, after those it holds; a unit it holds already changes nothing.
func (s *Service) Attach(ctx context.Context, id task.ID, u unit.Unit) error {
	if u.IsZero() || u == task.UnitOf(id) {
		return fmt.Errorf("%w: attach %s to task %s", ErrArgs, u, id)
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

// TasksOf are the stored tasks that hold u or one of its items.
func (s *Service) TasksOf(ctx context.Context, u unit.Unit) ([]task.Task, error) {
	return s.d.Store.TasksOf(ctx, u)
}

// OpenTasksOf are the open tasks that hold u or one of its items, but those a view hides for a disabled
// plugin, in the order a view lists them before its sort.
func (s *Service) OpenTasksOf(ctx context.Context, u unit.Unit) ([]task.Task, error) {
	es, err := s.d.Store.TasksOf(ctx, u)
	if err != nil {
		return nil, err
	}
	hidden := s.hider()
	es = slices.DeleteFunc(es, func(e task.Task) bool { return !e.Open() || hidden != nil && hidden(e) })
	baseOrder(es)
	return es, nil
}

// ReopenOwn is u's own task when u's items key on u (task.UnitTask), moved back into the ready category,
// Changed, when it is done; ok is false when u's items key on no unit task or no task holds it. A dropped
// task stays dropped.
func (s *Service) ReopenOwn(ctx context.Context, u unit.Unit) (task.ID, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	its, err := s.d.Items.Items(ctx, u)
	if err != nil || !slices.ContainsFunc(its, func(it items.Item) bool { return it.TaskKey == u.String() }) {
		return "", false, err
	}
	ts, err := s.d.Store.TasksOf(ctx, u)
	if err != nil {
		return "", false, err
	}
	e, ok := task.UnitTask(ts, u)
	if !ok || e.Category != task.CategoryDone {
		return e.ID, ok, nil
	}
	if err := e.MoveTo(task.CategoryReady, s.d.States); err != nil {
		return "", false, err
	}
	e.Changed, e.Updated = true, s.now()
	return e.ID, true, s.d.Store.Apply(ctx, []task.Change{{Op: task.OpUpdate, Task: e, Why: "a gate opened on " + u.String()}})
}

// Ended says whether e, as a read of it returned it with err, is done or dropped; a task no longer stored has
// ended too.
func Ended(e task.Task, err error) (bool, error) {
	if errors.Is(err, ErrNoTask) {
		return true, nil
	}
	return err == nil && !e.Open(), err
}

// HeldOpen are the open tasks held only for gates (heldOnly).
func (s *Service) HeldOpen(ctx context.Context) ([]task.ID, error) {
	es, err := s.d.Store.Tasks(ctx, task.Query{})
	if err != nil {
		return nil, err
	}
	var out []task.ID
	for _, e := range es {
		if s.heldOnly(e) {
			out = append(out, e.ID)
		}
	}
	return out, nil
}

// EndHeld closes task id, held only for gates (heldOnly), into category c; any other task is left as it is.
func (s *Service) EndHeld(ctx context.Context, id task.ID, c task.Category) error {
	return s.close(ctx, id, c, s.heldOnly)
}

// heldOnly says whether e is open and held only for gates: opened to hold one, with no open item and no task
// merged into it.
func (s *Service) heldOnly(e task.Task) bool {
	return e.Origin == task.OriginGate && e.Open() && len(e.Items) == 0 && len(e.Role(s.d.States, task.RoleMerged).List) == 0
}

// UnitClosed ends the open tasks that hold u, its closure posted: each moves into the done category.
func (s *Service) UnitClosed(ctx context.Context, u unit.Unit) error {
	ended, err := s.unitClosed(ctx, u)
	if err != nil {
		return err
	}
	s.settle(ctx, ended, "", task.ActorPlugin, "")
	return nil
}

// unitClosed is UnitClosed under the lock, returning the tasks it ended.
func (s *Service) unitClosed(ctx context.Context, u unit.Unit) ([]task.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	es, err := s.d.Store.TasksOf(ctx, u)
	if err != nil {
		return nil, err
	}
	now := s.now()
	var cs []task.Change
	for _, e := range es {
		if !e.Open() || !slices.Contains(e.Units, u) {
			continue
		}
		if err := e.MoveTo(task.CategoryDone, s.d.States); err != nil {
			return nil, err
		}
		e.Updated = now
		cs = append(cs, task.Change{Op: task.OpResolve, Task: e, Why: "its unit " + u.String() + " closed"})
	}
	if len(cs) == 0 {
		return nil, nil
	}
	ended := make([]task.Task, len(cs))
	for i, c := range cs {
		ended[i] = c.Task
	}
	return ended, s.d.Store.Apply(ctx, cs)
}
