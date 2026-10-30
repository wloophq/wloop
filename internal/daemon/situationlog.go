package daemon

import (
	"context"

	"github.com/wloophq/wloop/internal/readmodel"
)

// keepRows is the read model's bookkeeping on each new snapshot: the
// plugins' own, and once a minute the stale session intakes.
func (s *Server) keepRows(ctx context.Context, k readmodel.Kept) {
	err := s.keepPlugins(ctx, k)
	if failing := s.mutesFailing.Swap(err != nil); err != nil && !failing {
		s.logf("plugin bookkeeping: %v", err)
	}
	if s.intakes != nil && !k.At.Equal(s.intakesExpiredAt) {
		if err := s.expireSessionIntakes(ctx); err != nil {
			s.logf("expiring session intakes: %v", err)
			return
		}
		s.intakesExpiredAt = k.At
	}
}
