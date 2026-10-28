// Package intaketest holds in-memory fakes of the intake ports.
package intaketest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/core/intake"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// Store is an in-memory intake.Store that keeps one open intake per pull
// request and intent, as the store's partial unique index does, and refuses
// a write under a canceled context, as the database does.
type Store struct {
	// Err, when set, is asked before every Accept, Save and Open, with the
	// op's name and the intake to write; its error fails the call.
	Err func(op string, i intake.Intake) error

	mu      sync.Mutex
	seq     int64
	intakes map[int64]intake.Intake
	events  []port.Event
}

// FailWith makes every later Accept, Save and Open ask fn first.
func (s *Store) FailWith(fn func(op string, i intake.Intake) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Err = fn
}

func NewStore() *Store { return &Store{intakes: map[int64]intake.Intake{}} }

func clone(i intake.Intake) intake.Intake {
	i.Request = slices.Clone(i.Request)
	i.Log = slices.Clone(i.Log)
	return i
}

func checkEvents(evs []port.Event) error {
	for _, ev := range evs {
		if ev.At.IsZero() {
			return fmt.Errorf("the %s event has no time", ev.Kind)
		}
	}
	return nil
}

func (s *Store) check(ctx context.Context, op string, i intake.Intake, evs []port.Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.Err != nil {
		if err := s.Err(op, i); err != nil {
			return err
		}
	}
	return checkEvents(evs)
}

func (s *Store) Accept(ctx context.Context, i intake.Intake, evs ...port.Event) (intake.Intake, bool, error) {
	if err := s.check(ctx, "accept", i, evs); err != nil {
		return intake.Intake{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, o := range s.sorted() {
		if o.State.Open() && o.Unit == i.Unit && o.Intent == i.Intent {
			return clone(o), true, nil
		}
	}
	s.seq++
	i.Seq = s.seq
	s.intakes[i.Seq] = clone(i)
	for _, ev := range evs {
		ev.Key = strconv.FormatInt(i.Seq, 10)
		s.events = append(s.events, ev)
	}
	return clone(i), false, nil
}

func (s *Store) Save(ctx context.Context, i intake.Intake, evs ...port.Event) error {
	if err := s.check(ctx, "save", i, evs); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.intakes[i.Seq]; !ok {
		return fmt.Errorf("intake %d is not stored", i.Seq)
	}
	if i.State.Open() {
		for _, o := range s.sorted() {
			if o.Seq != i.Seq && o.State.Open() && o.Unit == i.Unit && o.Intent == i.Intent {
				return fmt.Errorf("intake %d is already open for %s %s", o.Seq, i.Unit, i.Intent)
			}
		}
	}
	s.intakes[i.Seq] = clone(i)
	s.events = append(s.events, evs...)
	return nil
}

func (s *Store) Intake(_ context.Context, seq int64) (intake.Intake, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i, ok := s.intakes[seq]
	return clone(i), ok, nil
}

func (s *Store) Open(ctx context.Context) ([]intake.Intake, error) {
	if err := s.check(ctx, "open", intake.Intake{}, nil); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var open []intake.Intake
	for _, i := range s.sorted() {
		if i.State.Open() {
			open = append(open, clone(i))
		}
	}
	return open, nil
}

// Put stores i as it is, as a daemon that stopped would have left it.
func (s *Store) Put(i intake.Intake) intake.Intake {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	i.Seq = s.seq
	s.intakes[i.Seq] = clone(i)
	return clone(i)
}

// Events lists the stored events, in the order they were saved.
func (s *Store) Events() []port.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.events)
}

func (s *Store) sorted() []intake.Intake {
	seqs := slices.Sorted(maps.Keys(s.intakes))
	out := make([]intake.Intake, 0, len(seqs))
	for _, seq := range seqs {
		out = append(out, s.intakes[seq])
	}
	return out
}

// Briefs answers Ready from a script, the last answer repeating.
type Briefs struct {
	// OnReady, when set, runs inside every Ready before it answers.
	OnReady func(ctx context.Context)
	// UnwantErr is what every Unwant answers.
	UnwantErr error

	mu       sync.Mutex
	answers  []intake.Readiness
	asked    []bool
	unwanted []unit.Unit
	changed  map[unit.Unit]chan struct{}
}

func NewBriefs(answers ...intake.Readiness) *Briefs {
	return &Briefs{answers: answers, changed: map[unit.Unit]chan struct{}{}}
}

func (b *Briefs) Ready(ctx context.Context, _ unit.Unit, first bool, _ []string) (intake.Readiness, error) {
	if b.OnReady != nil {
		b.OnReady(ctx)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.asked = append(b.asked, first)
	if len(b.answers) == 0 {
		return intake.Readiness{}, errors.New("intaketest: no readiness scripted")
	}
	rd := b.answers[0]
	if len(b.answers) > 1 {
		b.answers = b.answers[1:]
	}
	return rd, nil
}

// Answer replaces the script Ready answers from.
func (b *Briefs) Answer(answers ...intake.Readiness) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.answers = answers
}

func (b *Briefs) Changed(_ context.Context, ref unit.Unit) (<-chan struct{}, func()) {
	b.mu.Lock()
	ch, ok := b.changed[ref]
	if !ok {
		ch = make(chan struct{})
		b.changed[ref] = ch
	}
	b.mu.Unlock()
	return ch, func() {}
}

// End ends ref's current brief run: every Changed of it closes.
func (b *Briefs) End(ref unit.Unit) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if ch, ok := b.changed[ref]; ok {
		close(ch)
		delete(b.changed, ref)
	}
}

func (b *Briefs) Unwant(_ context.Context, ref unit.Unit) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.unwanted = append(b.unwanted, ref)
	return b.UnwantErr
}

// Unwanted lists the refs of every Unwant call.
func (b *Briefs) Unwanted() []unit.Unit {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.unwanted)
}

// Asked lists the first argument of every Ready call.
func (b *Briefs) Asked() []bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.asked)
}

// Firsts answers that every pull request's next turn is its first, or that
// none is.
type Firsts bool

func (f Firsts) First(context.Context, unit.Unit) (bool, error) { return bool(f), nil }

// FirstsFunc is a Firsts that runs itself.
type FirstsFunc func(ctx context.Context, ref unit.Unit) (bool, error)

func (f FirstsFunc) First(ctx context.Context, ref unit.Unit) (bool, error) { return f(ctx, ref) }

// Starter runs Fn, or answers Next when Fn is nil, and keeps every intake it
// was asked to start.
type Starter struct {
	Fn   func(ctx context.Context, i intake.Intake, out io.Writer) (intake.StartedTurn, error)
	Next intake.StartedTurn

	mu    sync.Mutex
	calls []intake.Intake
}

func (s *Starter) Start(ctx context.Context, i intake.Intake, out io.Writer) (intake.StartedTurn, error) {
	s.mu.Lock()
	s.calls = append(s.calls, clone(i))
	s.mu.Unlock()
	if s.Fn != nil {
		return s.Fn(ctx, i, out)
	}
	return s.Next, nil
}

func (s *Starter) Calls() []intake.Intake {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.calls)
}

// Payloads stores the guidance as the whole payload, guidance:<text>.
type Payloads struct{}

func (Payloads) WithGuidance(_ []byte, guidance string) ([]byte, error) {
	return []byte("guidance:" + guidance), nil
}

type Clock struct {
	mu sync.Mutex
	t  time.Time
}

func NewClock(t time.Time) *Clock { return &Clock{t: t} }

func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type Publisher struct {
	mu  sync.Mutex
	evs []port.Event
}

func (p *Publisher) Publish(evs ...port.Event) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.evs = append(p.evs, evs...)
}

func (p *Publisher) Events() []port.Event {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.evs)
}

// Timers is an After whose channels fire only when Fire is called.
type Timers struct {
	mu    sync.Mutex
	asked []time.Duration
	chans []chan time.Time
}

func (t *Timers) After(d time.Duration) <-chan time.Time {
	t.mu.Lock()
	defer t.mu.Unlock()
	ch := make(chan time.Time, 1)
	t.asked = append(t.asked, d)
	t.chans = append(t.chans, ch)
	return ch
}

// Fire fires every timer asked so far.
func (t *Timers) Fire() {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, ch := range t.chans {
		select {
		case ch <- time.Time{}:
		default:
		}
	}
	t.chans = nil
}

func (t *Timers) Asked() []time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	return slices.Clone(t.asked)
}
