package daemon

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// The outbox calls the agent route makes, compiled and never run.
var _ = func(ctx context.Context, s *outbox.Service) error {
	c := outbox.Caller{Session: "s-octocat-1", Units: []unit.Unit{{Provider: "github", Key: "acme/api#7"}}}
	payload := json.RawMessage(`{"event":"COMMENT"}`)
	d, err := s.Propose(ctx, outbox.Proposal{Unit: c.Units[0], Kind: "review", Text: "Looks fine.", Payload: payload, Basis: "4f9c2d1", Hold: true,
		Origin: draft.Origin{Kind: draft.OriginSession, Session: c.Session}})
	if err != nil {
		return err
	}
	if _, _, err := s.AgentRead(ctx, c, d.ID); err != nil {
		return err
	}
	text, basis := "Looks fine now.", "5a0d3e2"
	d, err = s.AgentAmend(ctx, c, d.ID, outbox.Amendment{Text: &text, Payload: &payload, Basis: &basis, Rev: d.Rev,
		By: draft.Actor{Kind: draft.ActorSession, Session: c.Session}})
	if errors.Is(err, outbox.ErrRevConflict) || errors.Is(err, outbox.ErrOutOfScope) || errors.Is(err, outbox.ErrStale) {
		return err
	}
	_, err = s.AgentWithdraw(ctx, c, d.ID, d.Rev, draft.WithdrawSuperseded)
	return err
}
