package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/domain/record"
)

// Drafts are an agent session's calls on its drafts, the agent route's own.
type Drafts interface {
	Read(ctx context.Context, session string, id int64) (api.AgentDraft, error)
	Update(ctx context.Context, session string, u api.AgentDraftUpdate) (api.AgentDraft, error)
	Withdraw(ctx context.Context, session string, w api.AgentDraftWithdrawal) (api.AgentDraft, error)
}

const (
	draftReadName     = "draft_read"
	draftUpdateName   = "draft_update"
	draftWithdrawName = "draft_withdraw"
)

// DraftTools are the core's draft tools, as an agent calls them through brand.ToolPrefix.
func DraftTools() []string {
	return []string{brand.ToolPrefix() + draftReadName, brand.ToolPrefix() + draftUpdateName, brand.ToolPrefix() + draftWithdrawName}
}

func draftID() *schema {
	one := 1
	return &schema{Type: "integer", Minimum: &one}
}

func (s *Server) draftTools() []registered {
	return []registered{
		{tool{Name: draftReadName, InputSchema: object(map[string]*schema{"id": draftID()}, "id")}, true, s.draftRead},
		{tool{Name: draftUpdateName, InputSchema: object(map[string]*schema{
			"id": draftID(), "rev": draftID(), "text": str(record.TextMax), "payload": {Type: "object"}, "basis": str(200),
		}, "id", "rev")}, true, s.draftUpdate},
		{tool{Name: draftWithdrawName, InputSchema: object(map[string]*schema{"id": draftID(), "rev": draftID(), "reason": str(40)}, "id", "rev")},
			true, s.draftWithdraw},
	}
}

// DraftJSON is how the draft tools and the agent's draft command show a draft.
func DraftJSON(d api.AgentDraft) string {
	raw, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err.Error()
	}
	return string(raw)
}

func decodeStrict(arguments json.RawMessage, v any) error {
	dec := json.NewDecoder(bytes.NewReader(arguments))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.Decode(new(json.RawMessage)) != io.EOF {
		return errors.New("one JSON object")
	}
	return nil
}

func (s *Server) draftRead(ctx context.Context, session string, arguments json.RawMessage) any {
	var a struct {
		ID int64 `json:"id"`
	}
	if err := decodeStrict(arguments, &a); err != nil || a.ID <= 0 {
		return s.badArguments(ctx, draftReadName, err)
	}
	d, err := s.Drafts.Read(ctx, session, a.ID)
	return s.draftAnswer(ctx, draftReadName, "read", d, err)
}

func (s *Server) draftUpdate(ctx context.Context, session string, arguments json.RawMessage) any {
	var a api.AgentDraftUpdate
	if err := decodeStrict(arguments, &a); err != nil || a.ID <= 0 || a.Rev < 1 {
		return s.badArguments(ctx, draftUpdateName, err)
	}
	d, err := s.Drafts.Update(ctx, session, a)
	return s.draftAnswer(ctx, draftUpdateName, "updated", d, err)
}

func (s *Server) draftWithdraw(ctx context.Context, session string, arguments json.RawMessage) any {
	var a api.AgentDraftWithdrawal
	if err := decodeStrict(arguments, &a); err != nil || a.ID <= 0 || a.Rev < 1 {
		return s.badArguments(ctx, draftWithdrawName, err)
	}
	d, err := s.Drafts.Withdraw(ctx, session, a)
	return s.draftAnswer(ctx, draftWithdrawName, "withdrawn", d, err)
}

func (s *Server) draftAnswer(ctx context.Context, tool, done string, d api.AgentDraft, err error) map[string]any {
	var name string
	switch {
	case err == nil:
		return s.answer(ctx, tool, done, fields{"Draft": DraftJSON(d)}, false)
	case errors.Is(err, outbox.ErrNotFound):
		name = "not_found"
	case errors.Is(err, outbox.ErrOutOfScope):
		name = "out_of_scope"
	case tool != draftReadName && errors.Is(err, outbox.ErrRevConflict):
		name = "rev_conflict"
	case tool == draftUpdateName && errors.Is(err, outbox.ErrStale):
		name = "stale"
	default:
		name = "failed"
	}
	return s.answer(ctx, tool, name, fields{"Error": err.Error()}, true)
}
