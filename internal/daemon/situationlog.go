package daemon

import (
	"context"

	"github.com/wloophq/wloop/internal/readmodel"
	"github.com/wloophq/wloop/internal/readmodel/prrow"
)

// keepRows is the read model's bookkeeping on each new snapshot: the
// situations that changed, the lapsed mutes, and once a minute the stale
// session intakes.
func (s *Server) keepRows(ctx context.Context, k readmodel.Kept) {
	s.recordSituations(ctx, k)
	err := s.expireLapsedMutes(ctx, k)
	if failing := s.mutesFailing.Swap(err != nil); err != nil && !failing {
		s.logf("expiring mutes: %v", err)
	}
	if s.intakes != nil && !k.At.Equal(s.intakesExpiredAt) {
		if err := s.expireSessionIntakes(ctx); err != nil {
			s.logf("expiring session intakes: %v", err)
			return
		}
		s.intakesExpiredAt = k.At
	}
}

// recordSituations writes, in one transaction, a situation event for each
// followed open pull request whose situation differs from its last one,
// logging the first failure of a streak only.
func (s *Server) recordSituations(ctx context.Context, k readmodel.Kept) {
	sits := prrow.Situations(k, s.deps.Now())
	if len(sits) == 0 {
		return
	}
	err := s.deps.Events.RecordSituations(ctx, sits)
	if failing := s.situationsFailing.Swap(err != nil); err != nil && !failing {
		s.logf("situations: %v", err)
	}
}
