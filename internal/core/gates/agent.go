package gates

import (
	"context"

	"github.com/wloophq/wloop/internal/domain/gate"
)

// SupersedeForAgent closes open draft gate id as superseded because session, an agent, revised its draft,
// and returns the closed gate; the draft's owner opens the gate its new rev waits on, which stamps session
// among its Sessions. It refuses every gate but a draft's, and a session the gate does not stamp.
func (s *Service) SupersedeForAgent(ctx context.Context, id int64, session string) (gate.Gate, error) {
	panic("not implemented: T3")
}

// ApproveOption resolves question gate question by by, who picked an option that points to draft gate
// draft's draft at rev, then approves draft with the same resolver, checking the rev again: a draft
// revised since, or a resolver among draft's Sessions, is refused. Each step is idempotent, so a call cut
// short completes when it is made again.
func (s *Service) ApproveOption(ctx context.Context, question, draft gate.Gate, rev int64, by gate.Resolver) error {
	panic("not implemented: T3")
}
