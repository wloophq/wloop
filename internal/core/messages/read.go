package messages

import (
	"context"
	"errors"
	"fmt"

	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/message"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// ErrNoTurn refuses a read from a session with no turn on the unit.
var ErrNoTurn = errors.New("this session has no turn")

// ReadRequest is inbox_read: the answers to the messages of the session's turns.
type ReadRequest struct {
	Session string
	// Unit is the unit whose turns the session holds; zero is the unit of its newest turn.
	Unit unit.Unit
	// Message is the message to read; 0 reads every answer the agent has not read.
	Message int64
}

// Read returns the answers the user gave to the messages of the session's turns, oldest first, and marks them
// read: with a message, that message and every answer to it; without, every answer not read yet. An answer
// still waiting for the session to stop is not read: its notice is on the way.
func (s *Service) Read(ctx context.Context, r ReadRequest) ([]message.Conversation, error) {
	b, err := s.bookOf(ctx, r)
	if err != nil {
		return nil, err
	}
	mine := func(seq int64) bool {
		for _, t := range b.Turns {
			if t.Seq == seq {
				return r.Session != "" && (t.Session == r.Session || t.SentTo == r.Session)
			}
		}
		return false
	}
	readable := func(a turn.Answer) bool {
		return a.Route != turn.Queued && a.Route != turn.Pending && a.Route != turn.Direct && (mine(a.TurnSeq) || (r.Session != "" && a.Session == r.Session))
	}
	var out []message.Conversation
	if r.Message != 0 {
		m, ok, err := s.Store.Message(ctx, r.Message)
		if err != nil {
			return nil, err
		}
		if !ok || m.Unit != b.Unit || !mine(m.Origin.TurnSeq) {
			return nil, fmt.Errorf("%w: %d", ErrNoMessage, r.Message)
		}
		c := message.Conversation{Message: m}
		for _, a := range b.Answers {
			if a.Message == m.ID && readable(a) {
				c.Answers = append(c.Answers, a)
			}
		}
		out = append(out, c)
	} else {
		at := map[int64]int{}
		for _, a := range b.Answers {
			if !a.ReadAt.IsZero() || !readable(a) {
				continue
			}
			i, ok := at[a.Message]
			if !ok {
				m, err := s.messageOf(ctx, a.Message)
				if err != nil {
					return nil, err
				}
				i, at[a.Message] = len(out), len(out)
				out = append(out, message.Conversation{Message: m})
			}
			out[i].Answers = append(out[i].Answers, a)
		}
	}
	var ids []int64
	for _, c := range out {
		for _, a := range c.Answers {
			ids = append(ids, a.ID)
		}
	}
	if err := s.Turns.MarkRead(ctx, b.Unit, ids); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Service) bookOf(ctx context.Context, r ReadRequest) (turn.Book, error) {
	if !r.Unit.IsZero() {
		books, err := s.Books.Books(ctx, r.Unit)
		if err != nil {
			return turn.Book{}, err
		}
		return books[r.Unit], nil
	}
	b, ok, err := s.Books.BookOf(ctx, turns.Key{Session: r.Session})
	if err != nil {
		return turn.Book{}, err
	}
	if !ok || r.Session == "" {
		return turn.Book{}, ErrNoTurn
	}
	return b, nil
}

// messageOf is message id, zero for 0 or one no longer stored.
func (s *Service) messageOf(ctx context.Context, id int64) (message.Message, error) {
	if id == 0 {
		return message.Message{}, nil
	}
	m, _, err := s.Store.Message(ctx, id)
	return m, err
}

// Conversation is m and the answers the user gave to it.
func (s *Service) Conversation(ctx context.Context, m message.Message) (message.Conversation, error) {
	c := message.Conversation{Message: m}
	if m.Unit.IsZero() || (m.Origin.Kind != message.OriginTurn && m.Origin.Kind != message.OriginAgent) {
		return c, nil
	}
	books, err := s.Books.Books(ctx, m.Unit)
	if err != nil {
		return c, err
	}
	for _, a := range books[m.Unit].Answers {
		if a.Message == m.ID {
			c.Answers = append(c.Answers, a)
		}
	}
	return c, nil
}

// Question is the question message of the unit's turn while that turn asks: the open one, after reconciling
// it with the unit's book, or the one already answered, which a newer answer replaces or adds to.
func (s *Service) Question(ctx context.Context, u unit.Unit) (message.Message, bool, error) {
	if err := s.Reconcile(ctx, u); err != nil {
		return message.Message{}, false, err
	}
	open, err := s.Store.Open(ctx)
	if err != nil {
		return message.Message{}, false, err
	}
	for _, m := range open {
		if m.Kind == message.Question && m.Unit == u {
			return m, true, nil
		}
	}
	books, err := s.Books.Books(ctx, u)
	if err != nil {
		return message.Message{}, false, err
	}
	b := books[u]
	q, ok := asking(&b, s.Intents.Answerable())
	if !ok {
		return message.Message{}, false, nil
	}
	for i := len(b.Answers) - 1; i >= 0; i-- {
		if a := b.Answers[i]; a.TurnSeq == q.Seq && a.Message != 0 && !a.Posted {
			m, found, err := s.Store.Message(ctx, a.Message)
			if err != nil || !found || m.State != message.Answered {
				return message.Message{}, false, err
			}
			return m, true, nil
		}
	}
	return message.Message{}, false, nil
}

// asking is the turn of the book an answer addresses while it waits on the user.
func asking(b *turn.Book, answerable []string) (*turn.Turn, bool) {
	q, ok := b.Question(answerable)
	if !ok || (q.State != turn.Asked && !q.State.Stopped()) {
		return nil, false
	}
	return q, true
}
