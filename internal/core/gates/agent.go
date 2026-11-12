package gates

import (
	"context"

	"github.com/wloophq/wloop/internal/domain/gate"
)

// SupersedeForAgent closes open draft gate id as superseded because session, an agent, revised its draft,
// with no consequence, as Supersede does. It accepts any non-empty session and refuses every gate but a
// draft's; it returns the closed gate, whose Sessions plus session the outbox stamps on the gate it opens.
func (s *Service) SupersedeForAgent(ctx context.Context, id int64, session string) (gate.Gate, error) {
	panic("not implemented: T3")
}

// ApproveOption resolves question gate question with r, the option picked (Intent, Answer) and who picked
// it (By, Session), when that option points to drafted's draft at rev; then it approves drafted with the
// same resolver. It checks first and resolves nothing when rev is not drafted.ApprovedRev or r.Session is
// among drafted.Sessions. Each step is idempotent, so a call cut short completes when it is made again.
func (s *Service) ApproveOption(ctx context.Context, question gate.Gate, r Resolution, drafted gate.Gate, rev int64) error {
	panic("not implemented: T3")
}
