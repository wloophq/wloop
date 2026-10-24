package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/pr"
)

// adminTimeout covers an evaluation of the plan and a turn's rendering,
// plus the start-up reconcile the daemon may still be running.
const adminTimeout = 15 * time.Minute

// DryRunTurn has the daemon store the review turn of a profile try.
func (c *Client) DryRunTurn(ctx context.Context, req api.DryRunTurnRequest) (api.DryRunTurn, error) {
	var res api.DryRunTurn
	err := c.do(ctx, adminTimeout, http.MethodPost, api.AdminDryRunTurn, req, &res)
	return res, err
}

// Doctor runs doctor's checks that read the loop's state.
func (c *Client) Doctor(ctx context.Context, q api.DoctorQuery) (api.DoctorReport, error) {
	v := url.Values{}
	if q.Repo != "" {
		v.Set("repo", q.Repo)
	}
	if q.PR != "" {
		v.Set("pr", q.PR)
	}
	path := api.AdminDoctor
	if len(v) > 0 {
		path += "?" + v.Encode()
	}
	var res api.DoctorReport
	err := c.do(ctx, adminTimeout, http.MethodGet, path, nil, &res)
	return res, err
}

// TurnBook is the pull request's turns with their instructions and events.
func (c *Client) TurnBook(ctx context.Context, ref pr.Ref) (api.TurnBook, error) {
	var res api.TurnBook
	err := c.do(ctx, adminTimeout, http.MethodGet, api.AdminTurn+"?pr="+url.QueryEscape(ref.URL()), nil, &res)
	return res, err
}

// Timings is what the loop recorded over the last days.
func (c *Client) Timings(ctx context.Context, days int) (api.TimingsReport, error) {
	var res api.TimingsReport
	err := c.do(ctx, adminTimeout, http.MethodGet, api.AdminTimings+"?since="+strconv.Itoa(days), nil, &res)
	return res, err
}

// Facts are what the pull request's guide is matched against.
func (c *Client) Facts(ctx context.Context, ref pr.Ref) (api.Facts, error) {
	var res api.Facts
	err := c.do(ctx, adminTimeout, http.MethodGet, api.AdminFacts+"?pr="+url.QueryEscape(ref.URL()), nil, &res)
	return res, err
}

// RecordGitAccess has the daemon keep a git access outcome this process saw.
func (c *Client) RecordGitAccess(ctx context.Context, r api.GitAccessRecord) error {
	return c.do(ctx, ReadTimeout, http.MethodPost, api.AdminGitAccess, r, nil)
}

// Meter is the metered cost of every scope since the daemon started.
func (c *Client) Meter(ctx context.Context) (api.MeterReport, error) {
	var res api.MeterReport
	err := c.do(ctx, adminTimeout, http.MethodGet, api.AdminMeter, nil, &res)
	return res, err
}
