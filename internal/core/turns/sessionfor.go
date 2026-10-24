package turns

import (
	"cmp"
	"context"
	"slices"
	"time"

	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// KindSessionUnit is the event of a session taking the unit of its first turn; Key = handle, Data = the unit.
const KindSessionUnit = "session_unit"

type Live struct {
	Session    turn.Session
	LastOutput time.Time
}

// SessionFor is the newest-started open session of u in that workspace whose handle the
// Sessions port lists as a usable terminal of the workspace, with that terminal's LastOutput
// for the warm rule.
func (s *Service) SessionFor(ctx context.Context, u unit.Unit, workspaceID string) (Live, bool, error) {
	if u.IsZero() || workspaceID == "" {
		return Live{}, false, nil
	}
	all, err := s.Registry.Sessions(ctx)
	if err != nil {
		return Live{}, false, err
	}
	var mine []turn.Session
	for _, ses := range all {
		if ses.Unit == u && ses.WorkspaceID == workspaceID && ses.ClosedAt.IsZero() {
			mine = append(mine, ses)
		}
	}
	if len(mine) == 0 {
		return Live{}, false, nil
	}
	terms, err := s.Sessions.Terminals(ctx, workspaceID)
	if err != nil {
		return Live{}, false, err
	}
	live := make(map[string]time.Time, len(terms))
	for _, t := range terms {
		live[t.Session] = t.LastOutput
	}
	slices.SortStableFunc(mine, func(a, b turn.Session) int {
		return cmp.Or(b.StartedAt.Compare(a.StartedAt), cmp.Compare(b.ID, a.ID))
	})
	for _, ses := range mine {
		if out, ok := live[ses.ID]; ok {
			return Live{Session: ses, LastOutput: out}, true, nil
		}
	}
	return Live{}, false, nil
}

// claimSession gives handle's session the unit u while it has none. A
// registry that cannot be read is logged by the turn's own read of it.
func (s *Service) claimSession(ctx context.Context, handle string, u unit.Unit) {
	if handle == "" || u.IsZero() {
		return
	}
	s.mu.Lock()
	cur, ok, err := s.Registry.Session(ctx, handle)
	if err != nil || !ok || !cur.Unit.IsZero() {
		s.mu.Unlock()
		return
	}
	cur.Unit = u
	ev := s.event(unit.Unit{}, KindSessionUnit, handle, u.String())
	err = s.Registry.SaveSession(ctx, cur, ev)
	s.mu.Unlock()
	if err != nil {
		s.logf("giving the session %s the unit %s: %v", handle, u.Label(), err)
		return
	}
	s.publish(ev)
}
