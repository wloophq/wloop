package gates

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// RuleWindow is the Rule of a gate its passed Deadline resolved to its Default.
const RuleWindow = "window"

var (
	// ErrRefused is a resolution the gate does not accept: by an agent or a
	// plugin, by a rule the trusted policy does not hold, or by a window that
	// has not passed.
	ErrRefused = errors.New("the gate refuses the resolution")
	// ErrIntent is an intent the gate does not offer, or one gates.toml does
	// not declare for it.
	ErrIntent = errors.New("the gate offers no such intent")
	// ErrNoDef is a gate gates.toml does not declare, by name or by kind.
	ErrNoDef = errors.New("gates.toml declares no such gate")
)

// Def is a gate the profile's gates.toml declares: its kind, its words and
// its intents in the order clients offer them.
type Def struct {
	Name    string
	Kind    gate.Kind
	Label   string
	Intents []Intent
}

// Intent is what a gate is resolved with: the status gates.toml gives it,
// and whether it takes text.
type Intent struct {
	Name   string
	Status gate.Status
	Answer bool
}

// Profile is the gates the profile declares, as loaded at each call.
type Profile interface {
	Gate(name string) (Def, bool)
	FirstOf(k gate.Kind) (Def, bool)
}

// Decision is how an approval decides a gate: its Mode, a delay's Window,
// and Rule, the rule that decided, which a gate it releases records.
type Decision struct {
	Mode   gate.Mode
	Window time.Duration
	Rule   string
}

// Policy is a trusted approval: the profile's and the configuration's
// [gates.approval], never an agent's or a thread's text.
type Policy interface {
	Decide(ctx context.Context, g gate.Gate) (Decision, error)
}

// Tasks finds the task a gate's unit is on, and opens one when none is.
type Tasks interface {
	TasksOf(ctx context.Context, u unit.Unit) ([]task.Task, error)
	Open(ctx context.Context, r coretasks.OpenRequest) (task.Task, error)
}

// Events records the event a resolved step gate is, which workflows
// trigger on.
type Events interface {
	RecordEvents(ctx context.Context, evs ...port.Event) error
}

// Deps are what the service reads, writes and applies. Every consequence
// port is optional: a kind whose port is nil resolves with no consequence.
type Deps struct {
	Store   Store
	Reader  Reader
	Profile Profile
	// Tasks gives a gate the task of its unit; nil leaves it with none.
	Tasks Tasks
	// Policy decides a gate Open is given no policy for, and is the one a
	// resolution naming a rule is checked against; nil holds every gate.
	Policy Policy
	Clock  port.Clock

	Publisher Publisher
	Releaser  Releaser
	Deliverer Deliverer
	Decider   Decider
	Unblocker Unblocker
	// Events records a resolved step gate; nil records none.
	Events Events

	Log func(format string, args ...any)
}

// Service opens the gates and is the only code that resolves one.
type Service struct{ d Deps }

func New(d Deps) *Service { return &Service{d: d} }

func (s *Service) logf(format string, args ...any) {
	if s.d.Log != nil {
		s.d.Log(format, args...)
	}
}

// Open opens g under p, nil meaning the service's Policy. The gate takes
// its kind and intents from gates.toml: the gate g names, else the first of
// its kind. It takes the task of its unit, or a new capture task when no
// task holds it. A policy that releases it resolves it at once with its
// rule; a delay gives it a Deadline and the Default its approving intent; a
// dry_run rejects a draft gate and holds any other.
func (s *Service) Open(ctx context.Context, g gate.Gate, p Policy) (gate.Gate, error) {
	def, err := s.def(g)
	if err != nil {
		return gate.Gate{}, err
	}
	g.Name, g.Kind, g.Status = def.Name, def.Kind, gate.Open
	if len(g.Intents) == 0 {
		for _, in := range def.Intents {
			g.Intents = append(g.Intents, in.Name)
		}
	}
	if g.Task == "" {
		if g.Task, err = s.taskOf(ctx, g.Unit, def); err != nil {
			return gate.Gate{}, err
		}
	}
	dec := s.decide(ctx, g, cmp.Or(p, s.d.Policy))
	r, release := s.released(g, def, dec)
	if dec.Mode == gate.ModeDelay {
		approve, ok := statusIntent(g, def, gate.Approved)
		if !ok {
			return gate.Gate{}, fmt.Errorf("%w: gate %s has no approving intent for its window", ErrIntent, def.Name)
		}
		g.Deadline, g.Default = s.d.Clock.Now().Add(dec.Window), approve.Name
	}
	opened, err := s.d.Store.Open(ctx, g)
	if err != nil || !release {
		return opened, err
	}
	return s.close(ctx, opened, r)
}

// Resolve closes open gate id with r. It accepts a person's intent, the
// Default of a gate whose Deadline passed with the rule window, and a rule
// the service's trusted Policy holds for the gate now; it refuses anything
// else, an agent's or a plugin's resolution above all. Status comes from
// the intent's declaration in gates.toml. It then applies the one
// consequence the gate's kind names.
func (s *Service) Resolve(ctx context.Context, id int64, r Resolution) error {
	_, err := s.resolve(ctx, id, r, s.d.Clock.Now())
	return err
}

// Supersede closes open gate id as superseded by by, its subject gone or
// replaced, with no consequence: the subject's owner settles the subject.
// It refuses an agent: no agent closes an open gate.
func (s *Service) Supersede(ctx context.Context, id int64, by task.Actor) error {
	if by == task.ActorAgent || by == "" {
		return fmt.Errorf("%w: gate %d is superseded by its subject's owner, not by %s", ErrRefused, id, cmp.Or(string(by), "no one"))
	}
	g, err := s.d.Reader.Get(ctx, id)
	if err != nil {
		return err
	}
	if g.Status != gate.Open {
		return fmt.Errorf("%w: %d is %s", ErrResolved, id, g.Status)
	}
	_, err = s.d.Store.Resolve(ctx, id, Resolution{Status: gate.Superseded, By: by})
	return err
}

// RecordSent records g, a post a tool made at once, as a gate already
// approved by g.By with g.Rule, worded by gates.toml and on the task of its
// unit when one holds it. It resolves no gate and applies no consequence.
func (s *Service) RecordSent(ctx context.Context, g gate.Gate) (gate.Gate, error) {
	sent, ok := s.d.Store.(Sent)
	if !ok {
		return gate.Gate{}, errors.New("the gates store records no sent post")
	}
	def, err := s.def(g)
	if err != nil {
		return gate.Gate{}, err
	}
	g.Name, g.Kind = def.Name, def.Kind
	if len(g.Intents) == 0 {
		for _, in := range def.Intents {
			g.Intents = append(g.Intents, in.Name)
		}
	}
	if approve, ok := statusIntent(g, def, gate.Approved); ok {
		g.Intent = approve.Name
	}
	if g.Task == "" {
		if id, ok := task.IDOf(g.Unit); ok {
			g.Task = id
		} else if s.d.Tasks != nil && !g.Unit.IsZero() {
			ts, err := s.d.Tasks.TasksOf(ctx, g.Unit)
			if err != nil {
				return gate.Gate{}, err
			}
			if len(ts) > 0 {
				g.Task = ts[0].ID
			}
		}
	}
	return sent.RecordSent(ctx, g)
}

// Tick resolves every open gate whose Deadline is not after now to its
// Default, with the rule window.
func (s *Service) Tick(ctx context.Context, now time.Time) error {
	due, err := s.d.Store.Due(ctx, now)
	if err != nil {
		return err
	}
	var errs []error
	for _, g := range due {
		if _, err := s.resolve(ctx, g.ID, Resolution{Rule: RuleWindow}, now); err != nil {
			errs = append(errs, fmt.Errorf("gate %d: %w", g.ID, err))
		}
	}
	return errors.Join(errs...)
}

// NextDeadline is the earliest Deadline of an open gate, zero when none
// has one.
func (s *Service) NextDeadline(ctx context.Context) (time.Time, error) {
	open, err := s.d.Reader.Where(ctx, Query{})
	if err != nil {
		return time.Time{}, err
	}
	var next time.Time
	for _, g := range open {
		if !g.Deadline.IsZero() && (next.IsZero() || g.Deadline.Before(next)) {
			next = g.Deadline
		}
	}
	return next, nil
}

func (s *Service) resolve(ctx context.Context, id int64, r Resolution, now time.Time) (gate.Gate, error) {
	g, err := s.d.Reader.Get(ctx, id)
	if err != nil {
		return gate.Gate{}, err
	}
	if g.Status != gate.Open {
		return g, fmt.Errorf("%w: %d is %s", ErrResolved, id, g.Status)
	}
	def, err := s.def(g)
	if err != nil {
		return gate.Gate{}, err
	}
	switch {
	case r.By == task.ActorPerson && r.Rule == "":
	case r.Rule == RuleWindow && (r.By == "" || r.By == task.ActorWorkflow):
		if g.Deadline.IsZero() || now.Before(g.Deadline) || g.Default == "" || r.Intent != "" && r.Intent != g.Default {
			return gate.Gate{}, fmt.Errorf("%w: gate %d has no window passed to its default", ErrRefused, id)
		}
		r.Intent, r.By = g.Default, task.ActorWorkflow
	case r.By == task.ActorWorkflow && r.Rule != "":
		held, ok := s.released(g, def, s.decide(ctx, g, s.d.Policy))
		if !ok || held.Rule != r.Rule || r.Intent != "" && r.Intent != held.Intent {
			return gate.Gate{}, fmt.Errorf("%w: the policy does not release gate %d by rule %q", ErrRefused, id, r.Rule)
		}
		r = held
	default:
		return gate.Gate{}, fmt.Errorf("%w: gate %d is resolved by a person or a trusted rule, not by %s", ErrRefused, id, cmp.Or(string(r.By), "no one"))
	}
	if r, err = withStatus(g, def, r); err != nil {
		return gate.Gate{}, err
	}
	return s.close(ctx, g, r)
}

// released is the resolution dec gives g when it resolves it at once.
func (s *Service) released(g gate.Gate, def Def, dec Decision) (Resolution, bool) {
	want := gate.Approved
	switch {
	case dec.Mode.Releases():
	case dec.Mode == gate.ModeDryRun && g.Kind == gate.KindDraft:
		want = gate.Rejected
	default:
		return Resolution{}, false
	}
	in, ok := statusIntent(g, def, want)
	if !ok {
		s.logf("gate %s of kind %s held: %s releases it, but it has no %s intent", def.Name, g.Kind, dec.Mode, want)
		return Resolution{}, false
	}
	return Resolution{Status: want, Intent: in.Name, By: task.ActorWorkflow, Rule: cmp.Or(dec.Rule, string(dec.Mode))}, true
}

func (s *Service) close(ctx context.Context, g gate.Gate, r Resolution) (gate.Gate, error) {
	closed, err := s.d.Store.Resolve(ctx, g.ID, r)
	if err != nil {
		return closed, err
	}
	return closed, s.apply(ctx, closed)
}

// decide is p's decision on g; with no policy, or one that fails, g waits.
func (s *Service) decide(ctx context.Context, g gate.Gate, p Policy) Decision {
	if p == nil {
		return Decision{Mode: gate.ModeWait}
	}
	dec, err := p.Decide(ctx, g)
	switch {
	case err != nil:
		s.logf("gate %s of kind %s on %s waits: its approval failed: %v", g.Name, g.Kind, g.Unit, err)
		return Decision{Mode: gate.ModeWait}
	case !dec.Mode.AppliesTo(g.Kind):
		s.logf("gate %s of kind %s on %s waits: %s applies to a draft gate only", g.Name, g.Kind, g.Unit, dec.Mode)
		return Decision{Mode: gate.ModeWait}
	case dec.Mode == gate.ModeDelay && dec.Window <= 0:
		s.logf("gate %s of kind %s on %s waits: a delay with no window", g.Name, g.Kind, g.Unit)
		return Decision{Mode: gate.ModeWait}
	}
	return dec
}

// apply runs the consequence g's kind names.
func (s *Service) apply(ctx context.Context, g gate.Gate) error {
	var err error
	switch g.Kind {
	case gate.KindDraft:
		if s.d.Publisher != nil {
			err = s.d.Publisher.Publish(ctx, g)
		}
	case gate.KindPlan:
		if s.d.Releaser != nil {
			err = s.d.Releaser.Release(ctx, g)
		}
	case gate.KindQuestion, gate.KindPost:
		if s.d.Deliverer != nil {
			err = s.d.Deliverer.Deliver(ctx, g)
		}
	case gate.KindProposal:
		if s.d.Decider != nil {
			err = s.d.Decider.Decide(ctx, g)
		}
	case gate.KindTrust:
		if s.d.Unblocker != nil {
			err = s.d.Unblocker.Unblock(ctx, g)
		}
	case gate.KindStep:
		if s.d.Events != nil {
			err = s.d.Events.RecordEvents(ctx, resolvedEvent(g))
		}
	}
	if err != nil {
		return fmt.Errorf("gate %d resolved %s, its consequence failed: %w", g.ID, g.Status, err)
	}
	return nil
}

// def is g's declaration: by its name, else the first gate of its kind.
func (s *Service) def(g gate.Gate) (Def, error) {
	var def Def
	ok := false
	if s.d.Profile != nil {
		if g.Name != "" {
			def, ok = s.d.Profile.Gate(g.Name)
		} else {
			def, ok = s.d.Profile.FirstOf(g.Kind)
		}
	}
	switch {
	case !ok:
		return Def{}, fmt.Errorf("%w: %s", ErrNoDef, cmp.Or(g.Name, "of kind "+string(g.Kind)))
	case g.Kind != "" && def.Kind != g.Kind:
		return Def{}, fmt.Errorf("%w: gate %s is of kind %s, not %s", ErrNoDef, def.Name, def.Kind, g.Kind)
	}
	return def, nil
}

// withStatus is r with the status gates.toml gives its intent, which g
// must offer; an answer only for an intent that takes text, and one it
// needs.
func withStatus(g gate.Gate, def Def, r Resolution) (Resolution, error) {
	i := slices.IndexFunc(def.Intents, func(in Intent) bool { return in.Name == r.Intent })
	if i < 0 || !slices.Contains(g.Intents, r.Intent) {
		return Resolution{}, fmt.Errorf("%w: gate %d has no intent %q", ErrIntent, g.ID, r.Intent)
	}
	in := def.Intents[i]
	switch {
	case in.Answer && r.Answer == "":
		return Resolution{}, fmt.Errorf("%w: %s needs an answer", ErrIntent, in.Name)
	case !in.Answer && r.Answer != "":
		return Resolution{}, fmt.Errorf("%w: %s takes no answer", ErrIntent, in.Name)
	}
	r.Status = in.Status
	return r, nil
}

// statusIntent is the first intent g offers that gates.toml resolves to want.
func statusIntent(g gate.Gate, def Def, want gate.Status) (Intent, bool) {
	for _, name := range g.Intents {
		i := slices.IndexFunc(def.Intents, func(in Intent) bool { return in.Name == name })
		if i >= 0 && def.Intents[i].Status == want && !def.Intents[i].Answer {
			return def.Intents[i], true
		}
	}
	return Intent{}, false
}

// taskOf is the task holding u: u's own when it is a task, else the first
// open task that holds it, else a new capture task named by the gate's
// label: a gate on a done or dropped task would wait where no list shows it.
func (s *Service) taskOf(ctx context.Context, u unit.Unit, def Def) (task.ID, error) {
	if id, ok := task.IDOf(u); ok {
		return id, nil
	}
	if s.d.Tasks == nil {
		return "", nil
	}
	r := coretasks.OpenRequest{Kind: task.KindCapture, Title: def.Label, Origin: task.OriginGate}
	if !u.IsZero() {
		ts, err := s.d.Tasks.TasksOf(ctx, u)
		if err != nil {
			return "", err
		}
		if i := slices.IndexFunc(ts, func(t task.Task) bool { return t.Category != task.CategoryDone && t.Category != task.CategoryDropped }); i >= 0 {
			return ts[i].ID, nil
		}
		r.Unit = &u
	}
	t, err := s.d.Tasks.Open(ctx, r)
	return t.ID, err
}

// resolvedEvent is the event resolved step gate g records: on its task, or
// its unit when it has none.
func resolvedEvent(g gate.Gate) port.Event {
	u := g.Unit
	if g.Task != "" {
		u = task.UnitOf(g.Task)
	}
	return port.Event{Unit: u, Kind: gate.EventResolved, Key: fmt.Sprintf("%s/%d", g.Name, g.ID), Actor: string(g.By), Data: g.Intent, At: g.ClosedAt}
}
