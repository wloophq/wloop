// Package messagestest holds an in-memory message store and the contract every message store meets.
package messagestest

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/domain/message"
)

type Store struct {
	mu   sync.Mutex
	rows []message.Message
}

var _ message.Store = (*Store)(nil)

func New() *Store { return &Store{} }

func (s *Store) find(m message.Message) int {
	if m.ID != 0 {
		return slices.IndexFunc(s.rows, func(r message.Message) bool { return r.ID == m.ID })
	}
	return slices.IndexFunc(s.rows, func(r message.Message) bool {
		switch m.Origin.Kind {
		case message.OriginLedger:
			return r.Origin.Kind == message.OriginLedger && r.Origin.Proposal == m.Origin.Proposal
		case message.OriginCore, message.OriginWorkflow:
			return r.State == message.Open && r.Unit == m.Unit && r.Origin.Kind == m.Origin.Kind && r.Origin.Workflow == m.Origin.Workflow
		case message.OriginTurn:
			return r.State == message.Open && r.Unit == m.Unit && r.Origin.Kind == m.Origin.Kind && r.Origin.TurnSeq == m.Origin.TurnSeq && r.Kind == m.Kind
		}
		return false
	})
}

func (s *Store) Upsert(_ context.Context, m message.Message) (message.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.find(m)
	if i < 0 {
		if m.ID != 0 {
			return message.Message{}, fmt.Errorf("no message %d", m.ID)
		}
		if m.State == "" {
			m.State = message.Open
		}
		m.ID = int64(len(s.rows) + 1)
		m.Options = slices.Clone(m.Options)
		if len(m.Options) == 0 {
			m.Options = nil
		}
		m.CreatedAt, m.ClosedAt = m.CreatedAt.UTC(), m.ClosedAt.UTC()
		s.rows = append(s.rows, m)
		return m, nil
	}
	r := &s.rows[i]
	if m.EntryID != "" {
		r.EntryID = m.EntryID
	}
	r.Text, r.Recommend, r.Confirm, r.Origin.Session = m.Text, m.Recommend, m.Confirm, m.Origin.Session
	r.Options = slices.Clone(m.Options)
	if len(r.Options) == 0 {
		r.Options = nil
	}
	return *r, nil
}

func (s *Store) Message(_ context.Context, id int64) (message.Message, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := slices.IndexFunc(s.rows, func(r message.Message) bool { return r.ID == id })
	if i < 0 {
		return message.Message{}, false, nil
	}
	return s.rows[i], true, nil
}

func (s *Store) ByEntry(_ context.Context, entry string) (message.Message, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.rows) - 1; i >= 0; i-- {
		if s.rows[i].EntryID == entry {
			return s.rows[i], true, nil
		}
	}
	return message.Message{}, false, nil
}

func (s *Store) Open(context.Context) ([]message.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []message.Message
	for _, r := range s.rows {
		if r.State == message.Open {
			out = append(out, r)
		}
	}
	return out, nil
}

func (s *Store) Close(_ context.Context, id int64, st message.State, answer string, at time.Time) error {
	if st == message.Open || st == "" {
		return fmt.Errorf("closing message %d as %q", id, st)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	i := slices.IndexFunc(s.rows, func(r message.Message) bool { return r.ID == id })
	if i < 0 {
		return fmt.Errorf("no message %d", id)
	}
	if r := &s.rows[i]; r.State == message.Open {
		r.State, r.Answer, r.ClosedAt = st, answer, at.UTC()
	}
	return nil
}
