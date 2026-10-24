// Package testsink is a test-only view sink: every status is its own column.
package testsink

import (
	"context"
	"github.com/wloophq/wloop/internal/domain/unit"
	"slices"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/core/views"
	"github.com/wloophq/wloop/internal/domain/view"
)

var managed = map[view.Role][]string{
	view.ReviewRole: {string(view.Working), string(view.NeedsMe), string(view.Reviewed), string(view.Done), string(view.Completed)},
	view.AuthorRole: {string(view.NeedsMe), string(view.InProgress)},
}

var authorColumns = []string{string(view.NeedsMe), string(view.InProgress), string(view.InReview), string(view.Ready), string(view.Completed)}

func (s *Sink) managedFor(role view.Role) []string {
	if role == view.AuthorRole && s.authorColumns {
		return authorColumns
	}
	return managed[role]
}

// WithAuthorColumns makes the author role own in-review, ready and completed.
func (s *Sink) WithAuthorColumns() *Sink {
	s.authorColumns = true
	return s
}

type card struct {
	column, comment string
	unread          bool
}

type Sink struct {
	name    string
	mu      sync.Mutex
	cards   map[unit.Unit]card
	batches [][]view.Item
	fail    error

	authorColumns bool
}

var (
	_ views.ViewSink        = (*Sink)(nil)
	_ views.PlacementReader = (*Sink)(nil)
)

func New(name string) *Sink { return &Sink{name: name, cards: map[unit.Unit]card{}} }

func (s *Sink) Name() string { return s.name }

func (s *Sink) Status(role view.Role, column string) view.Status {
	if !slices.Contains(s.managedFor(role), column) {
		return view.None
	}
	return view.Status(column)
}

func (s *Sink) Manages(role view.Role, column string) bool {
	return slices.Contains(s.managedFor(role), column)
}

// Fail makes every later Apply and Placements return err; nil heals it.
func (s *Sink) Fail(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fail = err
}

// Batches is every batch Apply was given, failed ones included.
func (s *Sink) Batches() [][]view.Item {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.batches)
}

func (s *Sink) Apply(_ context.Context, items []view.Item, force map[unit.Unit]bool) ([]view.Applied, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.batches = append(s.batches, slices.Clone(items))
	if s.fail != nil {
		return nil, s.fail
	}
	var out []view.Applied
	for _, it := range items {
		if it.Status == view.None {
			continue
		}
		c := s.cards[it.Unit]
		a := view.Applied{Unit: it.Unit, Role: it.Role, Status: it.Status, Unread: it.Unread, Summary: it.Summary, At: time.Now()}
		if force[it.Unit] || c.column == "" || s.Manages(it.Role, c.column) {
			c.column = string(it.Status)
			a.Column = c.column
		} else {
			a.Override = c.column
		}
		c.comment, c.unread = it.Summary, it.Unread
		s.cards[it.Unit] = c
		out = append(out, a)
	}
	return out, nil
}

func (s *Sink) Placements(context.Context) (map[unit.Unit]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail != nil {
		return nil, s.fail
	}
	out := make(map[unit.Unit]string, len(s.cards))
	for ref, c := range s.cards {
		out[ref] = c.column
	}
	return out, nil
}

// Place puts the card in column the way the user would.
func (s *Sink) Place(ref unit.Unit, column string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.cards[ref]
	c.column = column
	s.cards[ref] = c
}

func (s *Sink) Card(ref unit.Unit) (column, comment string, unread bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.cards[ref]
	return c.column, c.comment, c.unread
}

func (s *Sink) Column(status view.Status) string { return string(status) }

func (s *Sink) Unmanaged() string { return "parked" }
