package viewstest

import (
	"context"
	"fmt"
	"github.com/wloophq/wloop/internal/domain/unit"
	"maps"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/views"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/view"
)

type Option func(*config)

type config struct{ t testing.TB }

// Strict makes a fake fail t on a save that carries no event.
func Strict(t testing.TB) Option { return func(c *config) { c.t = t } }

// State is an in-memory views.State.
type State struct {
	cfg config

	mu      sync.Mutex
	applied map[string]map[view.Key]view.Applied
	events  []port.Event
}

var _ views.State = (*State)(nil)

func NewState(opts ...Option) *State {
	s := &State{applied: map[string]map[view.Key]view.Applied{}}
	for _, o := range opts {
		o(&s.cfg)
	}
	return s
}

// Put stores a for sink without events.
func (s *State) Put(sink string, a view.Applied) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.put(sink, a)
}

func (s *State) put(sink string, a view.Applied) {
	if s.applied[sink] == nil {
		s.applied[sink] = map[view.Key]view.Applied{}
	}
	a.At = a.At.UTC().Round(0)
	s.applied[sink][a.Key()] = a
}

// Events is every event the saves wrote, in order.
func (s *State) Events() []port.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.events)
}

func (s *State) Applied(_ context.Context, sink string) (map[view.Key]view.Applied, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return maps.Clone(s.applied[sink]), nil
}

func (s *State) SaveApplied(_ context.Context, sink string, a view.Applied, evs ...port.Event) error {
	if err := s.check("SaveApplied", evs); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.put(sink, a)
	s.add(evs)
	return nil
}

func (s *State) SaveFailure(_ context.Context, evs ...port.Event) error {
	if err := s.check("SaveFailure", evs); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.add(evs)
	return nil
}

func (s *State) check(save string, evs []port.Event) error {
	if s.cfg.t != nil && len(evs) == 0 {
		s.cfg.t.Helper()
		s.cfg.t.Errorf("viewstest: State.%s without an event", save)
	}
	for _, ev := range evs {
		if ev.At.IsZero() {
			return fmt.Errorf("the %s event has no time", ev.Kind)
		}
	}
	return nil
}

func (s *State) add(evs []port.Event) {
	for _, ev := range evs {
		ev.At = ev.At.UTC()
		if ev.Key != "" && slices.ContainsFunc(s.events, func(o port.Event) bool { return o.Unit == ev.Unit && o.Kind == ev.Kind && o.Key == ev.Key }) {
			continue
		}
		for ev.Key == "" && slices.ContainsFunc(s.events, func(o port.Event) bool {
			return o.Key == "" && o.Unit == ev.Unit && o.Kind == ev.Kind && o.At.Equal(ev.At)
		}) {
			ev.At = ev.At.Add(time.Nanosecond)
		}
		s.events = append(s.events, ev)
	}
}

// RunState checks a views.State; newState returns an empty one. events
// reads back the events the store wrote.
func RunState(t *testing.T, newState func(t *testing.T) views.State, events func(t *testing.T, s views.State) []port.Event) {
	ctx := context.Background()
	at := time.Date(2026, 10, 17, 12, 0, 0, 123456789, time.UTC)
	r7 := pr.Ref{Owner: "acme", Repo: "api", Number: 7}.Unit()
	r8 := pr.Ref{Owner: "acme", Repo: "web", Number: 8}.Unit()
	review := func(ref unit.Unit) view.Key { return view.Key{Unit: ref, Role: view.ReviewRole} }
	save := func(t *testing.T, s views.State, sink string, a view.Applied) {
		t.Helper()
		if err := s.SaveApplied(ctx, sink, a, port.Event{Unit: a.Unit, Kind: views.KindViewApplied, At: at}); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("save applied replaces the row of the sink and role", func(t *testing.T) {
		s := newState(t)
		if got, err := s.Applied(ctx, "orca"); err != nil || len(got) != 0 {
			t.Fatalf("a new state: %v %v", got, err)
		}
		first := view.Applied{Unit: r7, Role: view.ReviewRole, Status: view.NeedsMe, Unread: true, Summary: "asks: keep both?", Override: "parked", At: at}
		author := view.Applied{Unit: r7, Role: view.AuthorRole, Status: view.InProgress, Column: "in-progress", At: at}
		save(t, s, "orca", first)
		save(t, s, "orca", author)
		save(t, s, "orca", view.Applied{Unit: r8, Role: view.ReviewRole, Status: view.Working, Column: "reviewing", At: at})
		save(t, s, "other", view.Applied{Unit: r7, Role: view.ReviewRole, Status: view.Done, At: at})
		got, err := s.Applied(ctx, "orca")
		if err != nil || len(got) != 3 || got[review(r7)] != first || got[author.Key()] != author {
			t.Fatalf("got %+v %v\nwant %+v and %+v", got, err, first, author)
		}
		second := view.Applied{Unit: r7, Role: view.ReviewRole, Status: view.Reviewed, Column: "reviewed", At: at.Add(time.Minute)}
		save(t, s, "orca", second)
		got, err = s.Applied(ctx, "orca")
		if err != nil || len(got) != 3 || got[review(r7)] != second || got[author.Key()] != author {
			t.Fatalf("got %+v %v\nwant %+v", got, err, second)
		}
		if other, _ := s.Applied(ctx, "other"); len(other) != 1 || other[review(r7)].Status != view.Done {
			t.Fatalf("another sink: %+v", other)
		}
	})

	t.Run("a save whose events cannot be written stores nothing", func(t *testing.T) {
		s := newState(t)
		if err := s.SaveApplied(ctx, "orca", view.Applied{Unit: r7, Role: view.ReviewRole, Status: view.Working, At: at}, port.Event{Unit: r7, Kind: views.KindViewApplied}); err == nil {
			t.Fatal("saved an event with no time")
		}
		if got, _ := s.Applied(ctx, "orca"); len(got) != 0 {
			t.Fatalf("the row was stored: %+v", got)
		}
	})

	t.Run("a failure stores its events", func(t *testing.T) {
		s := newState(t)
		ev := port.Event{Unit: r7, Kind: views.KindViewSinkFailed, Data: "orca: board is down", At: at}
		if err := s.SaveFailure(ctx, ev, ev); err != nil {
			t.Fatal(err)
		}
		if err := s.SaveFailure(ctx, port.Event{Unit: r7, Kind: views.KindViewSinkFailed}); err == nil {
			t.Fatal("saved an event with no time")
		}
		got := events(t, s)
		if len(got) != 2 || got[0].Kind != views.KindViewSinkFailed || got[0].Data != ev.Data || got[0].Unit != r7 || !got[1].At.After(got[0].At) {
			t.Fatalf("events = %+v", got)
		}
		if rows, _ := s.Applied(ctx, "orca"); len(rows) != 0 {
			t.Fatalf("a failure stored a row: %+v", rows)
		}
	})
}
