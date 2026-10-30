package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
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

// TaskDrafts are the drafts made for task in any state, newest first.
func (c *Client) TaskDrafts(ctx context.Context, task string) ([]api.Draft, error) {
	path := api.Drafts + "?" + url.Values{"task": {task}, "state": {strings.Join(taskDraftStates, ",")}}.Encode()
	var res api.DraftsResponse
	err := c.do(ctx, ReadTimeout, http.MethodGet, path, nil, &res)
	return res.Drafts, err
}

// taskDraftStates are every draft state but dropped.
var taskDraftStates = []string{"held", "queued", "sending", "sent", "failed"}

// CreateDraft holds a draft the user wrote in the Outbox, or approves it at once with req.Send.
func (c *Client) CreateDraft(ctx context.Context, req api.DraftCreate) (api.Draft, error) {
	var d api.Draft
	err := c.do(ctx, ReadTimeout, http.MethodPost, api.Drafts, req, &d)
	return d, err
}

// ErrNoDraft is a draft id the daemon does not know.
var ErrNoDraft = errors.New("no such draft")

// Draft is draft id, with its gate.
func (c *Client) Draft(ctx context.Context, id int64) (api.Draft, error) {
	var res api.DraftsResponse
	path := api.Drafts + "?" + url.Values{"id": {strconv.FormatInt(id, 10)}}.Encode()
	if err := c.do(ctx, ReadTimeout, http.MethodGet, path, nil, &res); err != nil {
		return api.Draft{}, err
	}
	if len(res.Drafts) != 1 {
		return api.Draft{}, fmt.Errorf("draft %d: %w", id, ErrNoDraft)
	}
	return res.Drafts[0], nil
}

// Gates are the open gates.
func (c *Client) Gates(ctx context.Context) ([]api.GateView, error) {
	var gs []api.GateView
	err := c.do(ctx, ReadTimeout, http.MethodGet, api.GatesPath, nil, &gs)
	return gs, err
}

// ResolveGate resolves gate id with intent, its args carrying an answer, or a draft gate's text and rev.
func (c *Client) ResolveGate(ctx context.Context, id int64, intent string, req api.TaskRequest) (api.GateView, error) {
	var g api.GateView
	path := api.GatesPath + "/" + strconv.FormatInt(id, 10) + "/" + url.PathEscape(intent)
	err := c.do(ctx, ReadTimeout, http.MethodPost, path, req, &g)
	return g, err
}
