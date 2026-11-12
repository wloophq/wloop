package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/wloophq/wloop/internal/api"
)

// ProposeDraft proposes req on behalf of the agent session handle.
func (c *Client) ProposeDraft(ctx context.Context, handle string, req api.AgentDraftProposal) (api.AgentDraft, error) {
	var res api.AgentDraft
	err := c.doAs(ctx, ReadTimeout, handle, http.MethodPost, api.AgentDrafts, req, &res)
	return res, err
}

// ReadDraft reads draft id on behalf of the agent session handle.
func (c *Client) ReadDraft(ctx context.Context, handle string, id int64) (api.AgentDraft, error) {
	var res api.AgentDraft
	q := url.Values{"id": {strconv.FormatInt(id, 10)}}
	err := c.doAs(ctx, ReadTimeout, handle, http.MethodGet, api.AgentDrafts+"?"+q.Encode(), nil, &res)
	return res, err
}

// UpdateDraft revises a draft on behalf of the agent session handle.
func (c *Client) UpdateDraft(ctx context.Context, handle string, req api.AgentDraftUpdate) (api.AgentDraft, error) {
	var res api.AgentDraft
	err := c.doAs(ctx, ReadTimeout, handle, http.MethodPatch, api.AgentDrafts, req, &res)
	return res, err
}

// WithdrawDraft withdraws a draft on behalf of the agent session handle.
func (c *Client) WithdrawDraft(ctx context.Context, handle string, req api.AgentDraftWithdrawal) (api.AgentDraft, error) {
	var res api.AgentDraft
	err := c.doAs(ctx, ReadTimeout, handle, http.MethodDelete, api.AgentDrafts, req, &res)
	return res, err
}
