package outbox

import (
	"context"

	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// Caller is the agent session an agent's call on a draft comes from, and the units its turns are on.
type Caller struct {
	Session string
	Units   []unit.Unit
}

// Revisions are draft id's content revisions, oldest first.
func (s *Service) Revisions(ctx context.Context, id draft.ID) ([]draft.Revision, error) {
	panic("not implemented: T3")
}

// AgentRead is draft id, delivered, and its revisions, for c. A draft out of c's reach is ErrOutOfScope.
func (s *Service) AgentRead(ctx context.Context, c Caller, id draft.ID) (draft.Draft, []draft.Revision, error) {
	panic("not implemented: T3")
}

// AgentAmend applies a to held or failed draft id for c, at a.Rev: a.Rev < 1 is ErrInvalid, a draft of
// another rev ErrRevConflict, one that is not of turn or session origin, or neither on c's units nor of an
// Origin.Unit among them, ErrOutOfScope. It stamps a.By from c, ignoring the caller's. The revision
// supersedes the draft's gate and opens a waiting one that stamps c.Session; a failed draft goes back to
// held.
func (s *Service) AgentAmend(ctx context.Context, c Caller, id draft.ID, a Amendment) (draft.Draft, error) {
	panic("not implemented: T3")
}

// AgentWithdraw drops draft id at rev for c, for reason, under AgentAmend's scope and rev checks: rev < 1
// is ErrInvalid.
func (s *Service) AgentWithdraw(ctx context.Context, c Caller, id draft.ID, rev int64, reason string) (draft.Draft, error) {
	panic("not implemented: T3")
}
