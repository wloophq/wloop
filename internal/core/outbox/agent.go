package outbox

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// Caller is the agent session an agent's call on a draft comes from, and the units its turns are on.
type Caller struct {
	Session string
	Units   []unit.Unit
}

// Revisions are draft id's content revisions, oldest first.
func (s *Service) Revisions(ctx context.Context, id draft.ID) ([]draft.Revision, error) {
	return s.Store.Revisions(ctx, id)
}

// AgentRead is draft id, delivered, and its revisions, for c: a draft on, or proposed from, one of c's units.
// A draft out of c's reach is ErrOutOfScope.
func (s *Service) AgentRead(ctx context.Context, c Caller, id draft.ID) (draft.Draft, []draft.Revision, error) {
	d, err := s.Store.Get(ctx, id)
	if err != nil {
		return draft.Draft{}, nil, err
	}
	if err := c.reaches(d); err != nil {
		return draft.Draft{}, nil, err
	}
	revs, err := s.Store.Revisions(ctx, id)
	if err != nil {
		return draft.Draft{}, nil, err
	}
	return s.delivered(d), revs, nil
}

// AgentAmend applies a to held or failed draft id for c, at a.Rev, stamping a.By from c: a rev below 1 is
// ErrInvalid, a draft of another rev ErrRevConflict, one that is not of turn or session origin, or neither
// on nor proposed from c's units, ErrOutOfScope. The revision supersedes the draft's gate and opens a
// waiting one that stamps c.Session; a failed draft goes back to held.
func (s *Service) AgentAmend(ctx context.Context, c Caller, id draft.ID, a Amendment) (draft.Draft, error) {
	d, err := s.agentLoad(ctx, c, id, a.Rev)
	if err != nil {
		return draft.Draft{}, err
	}
	a.By = draft.Actor{Kind: draft.ActorSession, Session: c.Session}
	got, err := s.revise(ctx, d, a, c.Session)
	return got, revConflict(err)
}

// revConflict is err, a revision's, which also reads as ErrRevConflict when another write took the draft
// between its read and its own.
func revConflict(err error) error {
	if errors.Is(err, ErrConflict) && !errors.Is(err, ErrRevConflict) {
		return fmt.Errorf("%w: %w", ErrRevConflict, err)
	}
	return err
}

// AgentWithdraw drops held or failed draft id at rev for c, for reason, under AgentAmend's scope and rev
// checks; a held draft's gate is superseded by c's session.
func (s *Service) AgentWithdraw(ctx context.Context, c Caller, id draft.ID, rev int64, reason string) (draft.Draft, error) {
	d, err := s.agentLoad(ctx, c, id, rev)
	if err != nil {
		return draft.Draft{}, err
	}
	if d.State == draft.Held {
		g, ok, err := s.GateOf(ctx, d)
		if err != nil {
			return draft.Draft{}, err
		}
		if ok && g.Status != gate.Open {
			return draft.Draft{}, fmt.Errorf("draft %d: %w: its gate decided it meanwhile", id, ErrConflict)
		}
		if ok {
			if err := s.supersede(ctx, g, c.Session); err != nil {
				return draft.Draft{}, err
			}
		}
	}
	got, err := s.drop(ctx, d, reason)
	return got, revConflict(err)
}

// agentLoad is held or failed draft id, which c may revise at rev.
func (s *Service) agentLoad(ctx context.Context, c Caller, id draft.ID, rev int64) (draft.Draft, error) {
	if rev < 1 {
		return draft.Draft{}, fmt.Errorf("%w: an agent revises a draft at the rev it read, at least 1", ErrInvalid)
	}
	d, err := s.Store.Get(ctx, id)
	if err != nil {
		return draft.Draft{}, err
	}
	if err := c.reaches(d); err != nil {
		return draft.Draft{}, err
	}
	if !d.Origin.ByAgent() {
		return draft.Draft{}, fmt.Errorf("draft %d is of %s origin, which no agent revises: %w", id, d.Origin.Kind, ErrOutOfScope)
	}
	if d.State != draft.Held && d.State != draft.Failed {
		return draft.Draft{}, fmt.Errorf("draft %d is %s: %w: %w", id, d.State, ErrConflict, draft.ErrTransition)
	}
	if d.Rev != rev {
		return draft.Draft{}, fmt.Errorf("draft %d is at rev %d, not %d: %w", id, d.Rev, rev, ErrRevConflict)
	}
	return d, nil
}

// reaches refuses d, with ErrOutOfScope, unless c is a session and d is on, or was proposed from, one of
// c's units.
func (c Caller) reaches(d draft.Draft) error {
	switch {
	case c.Session == "":
		return fmt.Errorf("no agent session: %w", ErrOutOfScope)
	case !slices.ContainsFunc(c.Units, d.Concerns):
		return fmt.Errorf("draft %d is on %s: %w", d.ID, d.Unit, ErrOutOfScope)
	}
	return nil
}
