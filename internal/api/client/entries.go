package client

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/wloophq/wloop/internal/api"
)

// EntryQuery selects a view's entries; Contexts must all match, and the Inbox ignores Area.
type EntryQuery struct {
	View     string
	Area     string
	Contexts []string
}

// GTD is the views, areas, contexts and verbs the daemon serves.
func (c *Client) GTD(ctx context.Context) (api.GTDCatalog, error) {
	var cat api.GTDCatalog
	err := c.do(ctx, ReadTimeout, http.MethodGet, api.GTDPath, nil, &cat)
	return cat, err
}

// EntriesSince is q's page since: every entry when since is zero or the daemon answers Full, else the entries
// changed after it and the ids that left the view.
func (c *Client) EntriesSince(ctx context.Context, q EntryQuery, since api.Since) (api.EntriesPage, error) {
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
	var page api.EntriesPage
	err := c.do(ctx, ReadTimeout, http.MethodGet, api.EntriesPath+"?"+v.Encode(), nil, &page)
	return page, err
}

// Entry is entry id with its cards and items.
func (c *Client) Entry(ctx context.Context, id string) (api.EntryView, error) {
	var e api.EntryView
	err := c.do(ctx, ReadTimeout, http.MethodGet, api.EntryPath(id), nil, &e)
	return e, err
}

// Capture files an entry in the Inbox.
func (c *Client) Capture(ctx context.Context, req api.CaptureRequest) (api.EntryView, error) {
	var e api.EntryView
	err := c.do(ctx, ReadTimeout, http.MethodPost, api.EntriesPath, req, &e)
	return e, err
}

// EntryDo runs verb on entry id.
func (c *Client) EntryDo(ctx context.Context, id, verb string, req api.EntryRequest) (api.EntryResult, error) {
	var res api.EntryResult
	err := c.do(ctx, actionTimeout, http.MethodPost, api.EntryPath(id, verb), req, &res)
	return res, err
}

// Counters are the counters of area, "" for every area.
func (c *Client) Counters(ctx context.Context, area string) (api.Counters, error) {
	path := api.CountersPath
	if area != "" {
		path += "?" + url.Values{"area": {area}}.Encode()
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
