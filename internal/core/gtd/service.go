package gtd

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/gtd"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// Deps is what the service runs on.
type Deps struct {
	Store Store
	Items Items
	Facts Facts
	Me    gtd.Me
	Clock port.Clock
	// StaleWaitingDays is how many working days a waiting entry waits before AutoFocus stars it.
	StaleWaitingDays int
	// Disabled are the providers whose plugin is disabled now; nil: none is.
	Disabled func() []unit.ProviderID
}

type handlerKey struct {
	kind gtd.Kind
	verb gtd.Verb
}

type Service struct {
	d Deps
	// mu serializes the service's read-modify-write of entries.
	mu       sync.Mutex
	reg      sync.RWMutex
	handlers map[handlerKey]VerbHandler
	views    map[gtd.View]viewSource
}

func New(d Deps) *Service {
	return &Service{d: d, handlers: map[handlerKey]VerbHandler{}, views: map[gtd.View]viewSource{}}
}

func (s *Service) now() time.Time { return s.d.Clock.Now() }

// Register makes h run verb on entries of kind, before any state change.
func (s *Service) Register(kind gtd.Kind, verb gtd.Verb, h VerbHandler) {
	s.reg.Lock()
	defer s.reg.Unlock()
	s.handlers[handlerKey{kind, verb}] = h
}

func (s *Service) handler(kind gtd.Kind, verb gtd.Verb) VerbHandler {
	s.reg.RLock()
	defer s.reg.RUnlock()
	return s.handlers[handlerKey{kind, verb}]
}

// Do runs req.Verb on entry id: a registered handler first, else the state change. An entry of a unit whose
// plugin is disabled refuses every verb with ErrPluginDisabled but dismiss, which moves a derived entry, or one
// whose units are all of disabled plugins, to the Trash whole, and detaches a disabled plugin's units from any other.
func (s *Service) Do(ctx context.Context, id gtd.ID, req Request) (Result, error) {
	if !known(req.Verb) {
		return Result{}, fmt.Errorf("%w: unknown verb %q", ErrArgs, req.Verb)
	}
	if src, ok := s.sourceOf(id); ok {
		e, found, err := src.Entry(ctx, id)
		if err != nil {
			return Result{}, err
		}
		if !found {
			return Result{}, fmt.Errorf("%w: %s", ErrNoEntry, id)
		}
		if h := s.handler(e.Kind, req.Verb); h != nil {
			return h(ctx, e, req)
		}
		return Result{}, fmt.Errorf("%w: %s on a %s entry", ErrVerb, req.Verb, e.Kind)
	}
	e, err := s.d.Store.Entry(ctx, id)
	if err != nil {
		return Result{}, err
	}
	enabled := map[unit.Unit]bool{}
	if err := s.writable(ctx, e, enabled); err != nil {
		if req.Verb != gtd.VerbDismiss || !errors.Is(err, ErrPluginDisabled) {
			return Result{}, err
		}
		return s.dismissOff(ctx, id, req, enabled)
	}
	if h := s.handler(e.Kind, req.Verb); h != nil {
		return h(ctx, e, req)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, err = s.d.Store.Entry(ctx, id); err != nil {
		return Result{}, err
	}
	now := s.now()
	var cs []gtd.Change
	if req.Verb == gtd.VerbProject {
		cs, err = s.project(ctx, e, req.Args, now)
	} else {
		var next gtd.Entry
		next, err = transition(e, req, now)
		cs = []gtd.Change{{Op: gtd.OpUpdate, Entry: next, Why: string(req.Verb)}}
	}
	if err != nil {
		return Result{}, err
	}
	if err := s.d.Store.Apply(ctx, cs, verbEvents(e, req, now)...); err != nil {
		return Result{}, err
	}
	return Result{Entry: cs[len(cs)-1].Entry}, nil
}

// project attaches e to the project args["id"] names, or to a new one titled args["title"], after its last action.
func (s *Service) project(ctx context.Context, e gtd.Entry, args map[string]string, now time.Time) ([]gtd.Change, error) {
	if err := accepts(e, gtd.VerbProject); err != nil {
		return nil, err
	}
	var cs []gtd.Change
	var p gtd.Entry
	switch id, title := args["id"], strings.TrimSpace(args["title"]); {
	case id != "":
		got, err := s.d.Store.Entry(ctx, gtd.ID(id))
		if err != nil {
			return nil, err
		}
		if got.Kind != gtd.KindProject {
			return nil, fmt.Errorf("%w: %s is not a project", ErrArgs, id)
		}
		p = got
	case title != "":
		p = gtd.Entry{ID: gtd.NewID(), Kind: gtd.KindProject, State: gtd.Next, Title: title,
			Sequential: args["sequential"] == "true", Area: e.Area, Origin: gtd.OriginUser, Created: now, Updated: now}
		cs = append(cs, gtd.Change{Op: gtd.OpCreate, Entry: p, Why: "project"})
	default:
		return nil, fmt.Errorf("%w: project needs id or title", ErrArgs)
	}
	open, err := s.d.Store.Entries(ctx, gtd.Query{View: gtd.ViewOpen})
	if err != nil {
		return nil, err
	}
	last := 0
	for _, o := range open {
		if o.Project == p.ID && o.ID != e.ID {
			last = max(last, o.Position)
		}
	}
	e.Project, e.Position, e.Updated = p.ID, last+1, now
	if pos, err := strconv.Atoi(args["position"]); err == nil {
		e.Position = pos
	}
	return append(cs, gtd.Change{Op: gtd.OpUpdate, Entry: e, Why: "project"}), nil
}

// dismissOff dismisses entry id, which has a unit or an item of a disabled plugin, with no handler: it detaches
// the disabled plugins' units and the items on them, and the entry keeps its state. A derived entry, whose id the
// unit it came from names, or one with no unit of an enabled plugin moves to the Trash whole. enabled caches the
// answers per unit.
func (s *Service) dismissOff(ctx context.Context, id gtd.ID, req Request, enabled map[unit.Unit]bool) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, err := s.d.Store.Entry(ctx, id)
	if err != nil {
		return Result{}, err
	}
	if err := accepts(e, gtd.VerbDismiss); err != nil {
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
	if len(off) == 0 || e.Origin == gtd.OriginDerived || !slices.ContainsFunc(e.Units, func(u unit.Unit) bool { return !isOff(u) }) {
		if next, err = transition(e, Request{Verb: gtd.VerbDismiss}, now); err != nil {
			return Result{}, err
		}
		why = string(gtd.VerbDismiss)
	} else {
		next.Units = slices.DeleteFunc(slices.Clone(e.Units), isOff)
		next.Items = slices.DeleteFunc(slices.Clone(e.Items), func(r gtd.ItemRef) bool { return isOff(r.Unit) })
		next.Updated = now
		for _, p := range off {
			why += " " + string(p)
		}
	}
	if err := s.d.Store.Apply(ctx, []gtd.Change{{Op: gtd.OpUpdate, Entry: next, Why: why}}, verbEvents(e, req, now)...); err != nil {
		return Result{}, err
	}
	return Result{Entry: next}, nil
}

// RecordVerb records that a person ran req's verb on e, once the handler or
// the service that runs it succeeded.
func (s *Service) RecordVerb(ctx context.Context, e gtd.Entry, req Request) error {
	evs := verbEvents(e, req, s.now())
	if len(evs) == 0 {
		return nil
	}
	return s.d.Store.Apply(ctx, nil, evs...)
}

// verbEvents are the event of req's verb on e, which left e's state; none
// for a verb that records nothing or an automated one.
func verbEvents(e gtd.Entry, req Request, now time.Time) []port.Event {
	kind := gtd.VerbEventKind(req.Verb)
	if req.Automated || !slices.Contains(gtd.VerbEventKinds, kind) {
		return nil
	}
	return []port.Event{{Unit: gtd.UnitOf(e.ID), Kind: kind, Actor: req.Source, Data: string(e.State), At: now}}
}

// writable refuses an entry with a unit whose plugin is disabled; ok caches the answers per unit.
func (s *Service) writable(ctx context.Context, e gtd.Entry, ok map[unit.Unit]bool) error {
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
func (s *Service) Capture(ctx context.Context, title, notes string, u *unit.Unit) (gtd.Entry, error) {
	if strings.TrimSpace(title) == "" {
		return gtd.Entry{}, fmt.Errorf("%w: a capture needs a title", ErrArgs)
	}
	return s.create(ctx, gtd.KindCapture, title, notes, u, gtd.OriginUser, nil)
}

// Open creates an entry another service owns the content of (messages): Kind, Inbox, random id.
func (s *Service) Open(ctx context.Context, r OpenRequest) (gtd.Entry, error) {
	if r.Kind == "" {
		return gtd.Entry{}, fmt.Errorf("%w: open needs a kind", ErrArgs)
	}
	return s.create(ctx, r.Kind, r.Title, "", r.Unit, cmp.Or(r.Origin, gtd.OriginMessage), r.Contexts)
}

func (s *Service) create(ctx context.Context, kind gtd.Kind, title, notes string, u *unit.Unit, origin string, contexts []string) (gtd.Entry, error) {
	now := s.now()
	e := gtd.Entry{ID: gtd.NewID(), Kind: kind, State: gtd.Inbox, Title: title, Notes: notes, Origin: origin,
		Contexts: slices.Clone(contexts), Created: now, Updated: now}
	if u != nil && !u.IsZero() {
		e.Units = []unit.Unit{*u}
	}
	if err := s.d.Store.Apply(ctx, []gtd.Change{{Op: gtd.OpCreate, Entry: e, Why: string(origin)}}); err != nil {
		return gtd.Entry{}, err
	}
	return e, nil
}

// Close moves an entry opened by Open or Capture to Done or Trash; a derived entry, or one whose unit's plugin is
// disabled, refuses it.
func (s *Service) Close(ctx context.Context, id gtd.ID, to gtd.State) error {
	if to != gtd.Done && to != gtd.Trash {
		return fmt.Errorf("%w: close to %s", ErrArgs, to)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, err := s.d.Store.Entry(ctx, id)
	if err != nil {
		return err
	}
	if e.Origin == gtd.OriginDerived {
		return fmt.Errorf("%w: close on a derived entry", ErrVerb)
	}
	if err := s.writable(ctx, e, nil); err != nil {
		return err
	}
	if e.State == to {
		return nil
	}
	now := s.now()
	e.State, e.Snooze, e.Updated = to, gtd.Snooze{}, now
	return s.d.Store.Apply(ctx, []gtd.Change{{Op: gtd.OpResolve, Entry: e, Why: "closed"}})
}

// Retitle renames an entry opened by Open or Capture, as one change; a derived entry refuses it.
func (s *Service) Retitle(ctx context.Context, id gtd.ID, title string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, err := s.d.Store.Entry(ctx, id)
	if err != nil {
		return err
	}
	if e.Origin == gtd.OriginDerived {
		return fmt.Errorf("%w: retitle on a derived entry", ErrVerb)
	}
	if e.Title == title {
		return nil
	}
	e.Title, e.Updated = title, s.now()
	return s.d.Store.Apply(ctx, []gtd.Change{{Op: gtd.OpUpdate, Entry: e, Why: "retitled"}})
}

// pending folds the changes of one Sync by entry, in first-seen order.
type pending struct {
	order []gtd.ID
	byID  map[gtd.ID]gtd.Change
}

func (p *pending) put(c gtd.Change) {
	if p.byID == nil {
		p.byID = map[gtd.ID]gtd.Change{}
	}
	old, ok := p.byID[c.Entry.ID]
	if !ok {
		p.order = append(p.order, c.Entry.ID)
	} else {
		if old.Op == gtd.OpCreate {
			c.Op = gtd.OpCreate
		}
		c.Why = old.Why + "; " + c.Why
	}
	p.byID[c.Entry.ID] = c
}

func (p *pending) overlay(es []gtd.Entry) []gtd.Entry {
	out := slices.Clone(es)
	have := map[gtd.ID]bool{}
	for i, e := range out {
		have[e.ID] = true
		if c, ok := p.byID[e.ID]; ok {
			out[i] = c.Entry
		}
	}
	for _, id := range p.order {
		if !have[id] {
			out = append(out, p.byID[id].Entry)
		}
	}
	return out
}

// of is es with this Sync's changes over them, plus the entries it created that belong to u.
func (p *pending) of(es []gtd.Entry, u unit.Unit) []gtd.Entry {
	return slices.DeleteFunc(p.overlay(es), func(e gtd.Entry) bool { return !belongs(e, u) })
}

func belongs(e gtd.Entry, u unit.Unit) bool {
	return slices.Contains(e.Units, u) || slices.ContainsFunc(e.Items, func(r gtd.ItemRef) bool { return r.Unit == u })
}

func (p *pending) changes() []gtd.Change {
	out := make([]gtd.Change, 0, len(p.order))
	for _, id := range p.order {
		out = append(out, p.byID[id])
	}
	return out
}

// Sync reconciles the units named by a TopicItems, TopicSourceEvents or TopicCards note, then AutoFocus and
// Wake for all, in one Apply. A unit whose plugin is disabled keeps its entries as they are. A snooze that ends
// reconciles its entry's units after waking it, so a still-muted unit snoozes it again before anything sees it
// in the Inbox.
func (s *Service) Sync(ctx context.Context, units []unit.Unit) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	open, err := s.d.Store.Entries(ctx, gtd.Query{View: gtd.ViewOpen})
	if err != nil {
		return err
	}
	var p pending
	enabled := map[unit.Unit]bool{}
	units = slices.Clone(units)
	for _, e := range open {
		woke, ok := gtd.Wake(e, now)
		if !ok {
			continue
		}
		switch err := s.writable(ctx, e, enabled); {
		case errors.Is(err, ErrPluginDisabled):
			continue
		case err != nil:
			return err
		}
		p.put(gtd.Change{Op: gtd.OpUpdate, Entry: woke, Why: "snooze ended"})
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
		have, err := s.d.Store.EntriesOf(ctx, u)
		if err != nil {
			return err
		}
		for _, c := range gtd.Reconcile(f, its, s.d.Me, p.of(have, u), now) {
			p.put(c)
		}
		for _, c := range gtd.Activity(f, p.of(have, u), now) {
			p.put(c)
		}
	}
	for _, e := range p.overlay(open) {
		if !e.Live() {
			continue
		}
		next, why := e, []string{}
		if woke, ok := gtd.Wake(next, now); ok {
			next, why = woke, append(why, "snooze ended")
		}
		if f, ok := gtd.AutoFocus(next, s.d.StaleWaitingDays, now); ok {
			next.Focus, next.Updated = f, now
			why = append(why, "focus "+cmp.Or(f.Auto, "cleared"))
		}
		if len(why) == 0 {
			continue
		}
		switch err := s.writable(ctx, e, enabled); {
		case errors.Is(err, ErrPluginDisabled):
		case err != nil:
			return err
		default:
			p.put(gtd.Change{Op: gtd.OpUpdate, Entry: next, Why: strings.Join(why, ", ")})
		}
	}
	if len(p.order) == 0 {
		return nil
	}
	return s.d.Store.Apply(ctx, p.changes())
}

// Attach adds u to entry id's units, after those it holds; a unit it holds already changes nothing.
func (s *Service) Attach(ctx context.Context, id gtd.ID, u unit.Unit) error {
	if u.IsZero() || u == gtd.UnitOf(id) {
		return fmt.Errorf("%w: attach %s to entry %s", ErrArgs, u, id)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, err := s.d.Store.Entry(ctx, id)
	if err != nil {
		return err
	}
	if slices.Contains(e.Units, u) {
		return nil
	}
	e.Units, e.Updated = append(slices.Clone(e.Units), u), s.now()
	return s.d.Store.Apply(ctx, []gtd.Change{{Op: gtd.OpUpdate, Entry: e, Why: "attached " + u.String()}})
}
