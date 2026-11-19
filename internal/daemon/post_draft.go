package daemon

import (
	"context"
	"fmt"
	"net/url"
	"strconv"

	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/task"
)

// PostDraft approves held draft id at rev through session at once, as a write operation with draft false asks:
// posted is true once the draft is on its way, approved by the session or already by a rule of the user's. A
// session the user does not drive approves nothing: posted is false and the draft waits for the user.
func (s *Server) PostDraft(ctx context.Context, session string, id draft.ID, rev int64) (draft.Draft, bool, error) {
	o := s.deps.Outbox
	if o == nil {
		return draft.Draft{}, false, errOutboxUnavailable
	}
	d, err := o.Store.Get(ctx, id)
	if err != nil {
		return draft.Draft{}, false, err
	}
	if d.State != draft.Held {
		return d, d.State == draft.Queued || d.State == draft.Sending || d.State == draft.Sent, nil
	}
	driven := false
	if s.deps.Turns != nil && session != "" {
		if driven, err = s.deps.Turns.UserDriven(ctx, session); err != nil {
			return d, false, err
		}
	}
	if !driven {
		return d, false, nil
	}
	got, err := s.draftDo(ctx, id, "approve", draftAction{Rev: &rev}, requester{Actor: task.ActorAgent, Session: session, Driven: true})
	if err != nil {
		return d, false, fmt.Errorf("draft %d waits for the user, not posted: %w", id, err)
	}
	return got, true, nil
}

// DraftLink is the cockpit's address of draft id's card, "" until the cockpit listens.
func (s *Server) DraftLink(id draft.ID) string {
	port := s.cockpitPort.Load()
	if port == 0 {
		return ""
	}
	return "http://127.0.0.1:" + strconv.Itoa(int(port)) + "/#task=" + url.QueryEscape(draftPrefix+strconv.FormatInt(int64(id), 10))
}
