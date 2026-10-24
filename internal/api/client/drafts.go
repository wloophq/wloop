package client

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/wloophq/wloop/internal/api"
)

// Drafts are the drafts in states, newest first; no state lists the proposed and failed ones.
func (c *Client) Drafts(ctx context.Context, states ...string) ([]api.Draft, error) {
	path := api.Drafts
	if len(states) > 0 {
		path += "?" + url.Values{"state": {strings.Join(states, ",")}}.Encode()
	}
	var res api.DraftsResponse
	err := c.do(ctx, ReadTimeout, http.MethodGet, path, nil, &res)
	return res.Drafts, err
}

// EntryDrafts are the drafts made for entry in any state, newest first.
func (c *Client) EntryDrafts(ctx context.Context, entry string) ([]api.Draft, error) {
	path := api.Drafts + "?" + url.Values{"entry": {entry}, "state": {strings.Join(entryDraftStates, ",")}}.Encode()
	var res api.DraftsResponse
	err := c.do(ctx, ReadTimeout, http.MethodGet, path, nil, &res)
	return res.Drafts, err
}

// entryDraftStates are every draft state but discarded.
var entryDraftStates = []string{"proposed", "approved", "sending", "sent", "failed"}

// CreateDraft holds a draft the user wrote in the Outbox, or approves it at once with req.Send.
func (c *Client) CreateDraft(ctx context.Context, req api.DraftCreate) (api.Draft, error) {
	var d api.Draft
	err := c.do(ctx, ReadTimeout, http.MethodPost, api.Drafts, req, &d)
	return d, err
}

func (c *Client) Draft(ctx context.Context, id int64) (api.Draft, error) {
	var d api.Draft
	err := c.do(ctx, ReadTimeout, http.MethodGet, api.DraftPath(id), nil, &d)
	return d, err
}

// DraftDo runs action, one of api.DraftActions, on draft id.
func (c *Client) DraftDo(ctx context.Context, id int64, action string, req api.DraftAction) (api.Draft, error) {
	var d api.Draft
	err := c.do(ctx, ReadTimeout, http.MethodPost, api.DraftPath(id, action), req, &d)
	return d, err
}
