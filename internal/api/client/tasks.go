package client

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/wloophq/wloop/internal/api"
)

// TaskQuery selects a view's entries; Contexts must all match, and the Inbox ignores Area.
type TaskQuery struct {
	View     string
	Area     string
	Contexts []string
}

// Views is the views, areas, contexts and verbs the daemon serves.
func (c *Client) Views(ctx context.Context) (api.ViewsCatalog, error) {
	var cat api.ViewsCatalog
	err := c.do(ctx, ReadTimeout, http.MethodGet, api.ViewsPath, nil, &cat)
	return cat, err
}

// TasksSince is q's page since: every entry when since is zero or the daemon answers Full, else the entries
// changed after it and the ids that left the view.
func (c *Client) TasksSince(ctx context.Context, q TaskQuery, since api.Since) (api.TasksPage, error) {
	v := url.Values{}
	v.Set("view", q.View)
	if q.Area != "" {
		v.Set("area", q.Area)
	}
	if len(q.Contexts) > 0 {
		v.Set("context", strings.Join(q.Contexts, ","))
	}
	if s := since.String(); s != "" {
		v.Set("since", s)
	}
	var page api.TasksPage
	err := c.do(ctx, ReadTimeout, http.MethodGet, api.TasksPath+"?"+v.Encode(), nil, &page)
	return page, err
}

// Task is entry id with its cards and items.
func (c *Client) Task(ctx context.Context, id string) (api.TaskView, error) {
	var e api.TaskView
	err := c.do(ctx, ReadTimeout, http.MethodGet, api.TaskPath(id), nil, &e)
	return e, err
}

// Capture files an entry in the Inbox.
func (c *Client) Capture(ctx context.Context, req api.CaptureRequest) (api.TaskView, error) {
	var e api.TaskView
	err := c.do(ctx, ReadTimeout, http.MethodPost, api.TasksPath, req, &e)
	return e, err
}

// TaskDo runs verb on entry id.
func (c *Client) TaskDo(ctx context.Context, id, verb string, req api.TaskRequest) (api.TaskResult, error) {
	var res api.TaskResult
	err := c.do(ctx, actionTimeout, http.MethodPost, api.TaskPath(id, verb), req, &res)
	return res, err
}

// Counters are the counters of area, "" for every area, of the entries holding every term.
func (c *Client) Counters(ctx context.Context, area string, terms ...string) (api.Counters, error) {
	q := url.Values{}
	if area != "" {
		q.Set("area", area)
	}
	if len(terms) > 0 {
		q[api.CountersTerm] = terms
	}
	path := api.CountersPath
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var out api.Counters
	err := c.do(ctx, ReadTimeout, http.MethodGet, path, nil, &out)
	return out, err
}

// Digest is the daily digest of day, "YYYY-MM-DD".
func (c *Client) Digest(ctx context.Context, day string) (api.Digest, error) {
	var out api.Digest
	err := c.do(ctx, ReadTimeout, http.MethodGet, api.DigestPath+"?"+url.Values{"day": {day}}.Encode(), nil, &out)
	return out, err
}
