// Package client is the Go client of the daemon's API, over its Unix socket.
package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/domain/unit"
)

const (
	pingTimeout = 5 * time.Second
	// ReadTimeout outlasts the daemon's start-up reconcile, which holds
	// UserAPI for up to 30 seconds.
	ReadTimeout = 45 * time.Second
	// actionTimeout covers the daemon waiting on its loop mutex for a running
	// sweep (up to its 15 minute timeout) before the action itself.
	actionTimeout = 30 * time.Minute
	// exploreTimeout covers a login lookup and two tries of a GitHub search
	// through the daemon.
	exploreTimeout = 90 * time.Second
)

// Client talks to the daemon over its Unix socket.
type Client struct {
	socket string
	http   *http.Client
	// Manager names the workspace manager of the agent session the client
	// calls for, "" when unknown; it goes with every call that names a handle.
	Manager string
}

// DaemonLabel is the launchd label of the daemon's LaunchAgent.
var DaemonLabel = brand.LaunchAgent()

// Kickstart is the command that starts the installed daemon.
func Kickstart() string {
	return fmt.Sprintf("launchctl kickstart gui/%d/%s", os.Getuid(), DaemonLabel)
}

// ErrDown is a daemon that does not listen on the socket.
var ErrDown = fmt.Errorf(brand.Name+"'s daemon is not running; start it with `%s` (installed by `"+brand.Command+" app install`), or run `"+brand.Command+" serve` in a terminal", Kickstart())

// errHeld is a request the daemon held past its timeout.
const errHeld = "the daemon did not answer in time; at start-up it holds every request but health and the agents' until its start-up reconcile ends, and its log says how far that got"

func New(socket string) *Client {
	return &Client{socket: socket, http: &http.Client{Transport: UnixTransport(socket)}}
}

// UnixTransport sends every request to the daemon's Unix socket.
func UnixTransport(socket string) *http.Transport {
	return &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		},
	}
}

// send sets the API version on req and wraps a failed dial in ErrDown.
func (c *Client) send(req *http.Request) (*http.Response, error) {
	req.Header.Set(api.Header, api.Version)
	resp, err := c.http.Do(req)
	var op *net.OpError
	if errors.As(err, &op) && op.Op == "dial" {
		return nil, fmt.Errorf("%w: %w", ErrDown, err)
	}
	return resp, err
}

func (c *Client) Ping(ctx context.Context) error {
	return c.do(ctx, pingTimeout, http.MethodGet, "/api/health", nil, nil)
}

func (c *Client) Health(ctx context.Context) (api.Health, error) {
	var h api.Health
	err := c.do(ctx, ReadTimeout, http.MethodGet, "/api/health", nil, &h)
	return h, err
}

func (c *Client) Actions(ctx context.Context) (api.ActionsResponse, error) {
	var res api.ActionsResponse
	err := c.do(ctx, ReadTimeout, http.MethodGet, api.Actions, nil, &res)
	return res, err
}

// Catalog is the served views, facets and situations.
func (c *Client) Catalog(ctx context.Context) (api.CatalogResponse, error) {
	var res api.CatalogResponse
	err := c.do(ctx, ReadTimeout, http.MethodGet, api.Catalog, nil, &res)
	return res, err
}

func (c *Client) Explore(ctx context.Context, query string) (api.ExploreResult, error) {
	var res api.ExploreResult
	err := c.do(ctx, exploreTimeout, http.MethodPost, "/api/explore", api.ExploreRequest{Query: query}, &res)
	return res, err
}

// Rows is every row.
func (c *Client) Rows(ctx context.Context) ([]api.Row, error) {
	page, err := c.RowsSince(ctx, api.Since{})
	return page.Rows, err
}

// RowsSince is the page of rows since: every row when since is zero or the
// daemon answers Full, else the rows changed after it and the units that
// left.
func (c *Client) RowsSince(ctx context.Context, since api.Since) (api.RowsPage, error) {
	q := url.Values{}
	if s := since.String(); s != "" {
		q.Set("since", s)
	}
	var page api.RowsPage
	err := c.do(ctx, ReadTimeout, http.MethodGet, api.Rows+"?"+q.Encode(), nil, &page)
	return page, err
}

// Action returns the lines the action reported.
func (c *Client) Action(ctx context.Context, u unit.Unit, action string, req api.ActionRequest) ([]string, error) {
	res, err := c.Ask(ctx, u, action, req)
	return res.Output, err
}

// Ask runs the action and returns the daemon's answer whole: the intake
// that accepted it, for a review, an address or a run.
func (c *Client) Ask(ctx context.Context, u unit.Unit, action string, req api.ActionRequest) (api.ActionResult, error) {
	var res api.ActionResult
	err := c.do(ctx, actionTimeout, http.MethodPost, api.UnitPath(u, action), req, &res)
	return res, err
}

func intakePath(pattern string, seq int64) string {
	return strings.Replace(pattern, "{seq}", strconv.FormatInt(seq, 10), 1)
}

// Intake reads intake seq with its log lines past after, waiting up to wait
// for a new line or its end.
func (c *Client) Intake(ctx context.Context, seq int64, after int, wait time.Duration) (api.Intake, error) {
	q := url.Values{}
	q.Set("after", strconv.Itoa(after))
	q.Set("wait", strconv.Itoa(int(wait/time.Second)))
	var in api.Intake
	err := c.do(ctx, ReadTimeout+wait, http.MethodGet, intakePath(api.Intakes, seq)+"?"+q.Encode(), nil, &in)
	return in, err
}

// IntakeWithout starts a waiting intake without the brief.
func (c *Client) IntakeWithout(ctx context.Context, seq int64) error {
	return c.do(ctx, ReadTimeout, http.MethodPost, intakePath(api.IntakeWithout, seq), nil, nil)
}

func (c *Client) IntakeCancel(ctx context.Context, seq int64) error {
	return c.do(ctx, ReadTimeout, http.MethodPost, intakePath(api.IntakeCancel, seq), nil, nil)
}

func (c *Client) Trigger(ctx context.Context, job string) error {
	return c.do(ctx, ReadTimeout, http.MethodPost, "/api/jobs/"+url.PathEscape(job), nil, nil)
}

func (c *Client) do(ctx context.Context, timeout time.Duration, method, path string, in, out any) error {
	return c.doAs(ctx, timeout, "", method, path, in, out)
}

// doAs is do on behalf of the agent session handle, "" for none.
func (c *Client) doAs(ctx context.Context, timeout time.Duration, handle, method, path string, in, out any) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://"+brand.Name+path, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if handle != "" {
		req.Header.Set(api.SessionHeader, handle)
		if c.Manager != "" {
			req.Header.Set(api.ManagerHeader, c.Manager)
		}
	}
	resp, err := c.send(req)
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%w; %s", err, errHeld)
	}
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return refused(resp)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// StartTurn starts the stored turn of a round on the handle's session and
// returns its instructions.
func (c *Client) StartTurn(ctx context.Context, handle string, req api.TurnStartRequest) (api.TurnText, error) {
	var res api.TurnText
	err := c.doAs(ctx, ReadTimeout, handle, http.MethodPost, api.AgentTurnsStart, req, &res)
	return res, err
}

// TurnByHand has the daemon run a turn the user asked for in the handle's
// session; it waits on the loop like an action.
func (c *Client) TurnByHand(ctx context.Context, handle string, req api.ByHandRequest) (api.ByHandResult, error) {
	var res api.ByHandResult
	err := c.doAs(ctx, actionTimeout, handle, http.MethodPost, api.AgentTurnsByHand, req, &res)
	return res, err
}

func (c *Client) EndTurn(ctx context.Context, handle string, req api.TurnEndRequest) (api.TurnEnded, error) {
	var res api.TurnEnded
	err := c.doAs(ctx, ReadTimeout, handle, http.MethodPost, api.AgentTurnsEnd, req, &res)
	return res, err
}

func (c *Client) SessionStart(ctx context.Context, handle string, req api.SessionStartRequest) error {
	return c.doAs(ctx, ReadTimeout, handle, http.MethodPost, api.AgentSessionsStart, req, nil)
}

func (c *Client) Stop(ctx context.Context, handle string, req api.StopRequest) (api.StopAnswer, error) {
	var res api.StopAnswer
	err := c.doAs(ctx, ReadTimeout, handle, http.MethodPost, api.AgentSessionsStop, req, &res)
	return res, err
}

func (c *Client) Prompt(ctx context.Context, handle string, req api.PromptRequest) error {
	return c.doAs(ctx, ReadTimeout, handle, http.MethodPost, api.AgentSessionsPrompt, req, nil)
}

// TurnAnswer reads the answers to the caller's Inbox messages on the unit arg
// names (a pull request URL or "<provider>:<key>"): the unread ones, or every
// answer to message when it is not 0.
func (c *Client) TurnAnswer(ctx context.Context, handle, arg string, message int64) (api.TurnText, error) {
	var res api.TurnText
	q := url.Values{"pr": {arg}}
	if message != 0 {
		q.Set("message", strconv.FormatInt(message, 10))
	}
	err := c.doAs(ctx, ReadTimeout, handle, http.MethodGet, api.AgentAnswer+"?"+q.Encode(), nil, &res)
	return res, err
}

// Probe has the daemon send a probe turn; it waits on the loop like an action.
func (c *Client) Probe(ctx context.Context, req api.ProbeRequest) (api.ProbeResult, error) {
	var res api.ProbeResult
	err := c.do(ctx, actionTimeout, http.MethodPost, api.AdminProbe, req, &res)
	return res, err
}

// Plan is the workflows' plan: with q.Trigger, what an evaluation of that
// trigger would decide now, changing nothing; without it, the stored plan.
func (c *Client) Plan(ctx context.Context, q api.PlanQuery) (api.PlanResponse, error) {
	v := url.Values{}
	for k, val := range map[string]string{"workflow": q.Workflow, "provider": q.Provider, "trigger": q.Trigger, "unit": q.Unit} {
		if val != "" {
			v.Set(k, val)
		}
	}
	if q.Closed {
		v.Set("closed", "1")
	}
	path := api.Plan
	if len(v) > 0 {
		path += "?" + v.Encode()
	}
	timeout := ReadTimeout
	if q.Trigger != "" || q.Unit != "" {
		timeout = adminTimeout
	}
	var res api.PlanResponse
	err := c.do(ctx, timeout, http.MethodGet, path, nil, &res)
	return res, err
}

// ActOnPlan acts on plan items and returns them as the action left them,
// with an error line per item it could not act on.
func (c *Client) ActOnPlan(ctx context.Context, req api.PlanActRequest) (api.PlanResponse, error) {
	var res api.PlanResponse
	err := c.do(ctx, actionTimeout, http.MethodPost, api.Plan, req, &res)
	return res, err
}

func (c *Client) SetPause(ctx context.Context, req api.PauseRequest) error {
	return c.do(ctx, ReadTimeout, http.MethodPost, "/api/pause", req, nil)
}

func (c *Client) ReportApp(ctx context.Context, st api.AppStatus) error {
	return c.do(ctx, ReadTimeout, http.MethodPost, "/api/app", st, nil)
}

// eventsIdle is how long a stream may go without a line, pings included,
// before Events takes it for dead.
var eventsIdle = 60 * time.Second

// Events delivers each change the daemon publishes, those not yet received
// merged into one, and closes the channel when the stream ends, goes silent
// or ctx is done. A non-zero since resumes from there: the first change
// says what was missed. The daemon knows the subscriber by client.
func (c *Client) Events(ctx context.Context, client string, since api.Since) (<-chan api.Change, error) {
	ctx, cancel := context.WithCancel(ctx)
	q := url.Values{"client": {client}}
	if s := since.String(); s != "" {
		q.Set("since", s)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+brand.Name+"/api/events?"+q.Encode(), nil)
	if err != nil {
		cancel()
		return nil, err
	}
	resp, err := c.send(req) //nolint:bodyclose // the reading goroutine closes it
	if err != nil {
		cancel()
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		err := refused(resp)
		resp.Body.Close()
		cancel()
		return nil, err
	}
	lines := make(chan string)
	go func() {
		defer close(lines)
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			select {
			case lines <- sc.Text():
			case <-ctx.Done():
				return
			}
		}
	}()
	ch := make(chan api.Change)
	go func() {
		defer close(ch)
		defer cancel()
		defer resp.Body.Close()
		idle := time.NewTimer(eventsIdle)
		defer idle.Stop()
		var pending *api.Change
		for {
			var out chan<- api.Change
			var next api.Change
			if pending != nil {
				out, next = ch, *pending
			}
			select {
			case line, ok := <-lines:
				if !ok {
					return
				}
				idle.Reset(eventsIdle)
				data, isData := strings.CutPrefix(line, "data:")
				if !isData {
					continue
				}
				var c api.Change
				if err := json.Unmarshal([]byte(strings.TrimSpace(data)), &c); err != nil {
					c = api.Change{Type: "changed", All: true, Health: true}
				}
				if pending == nil {
					pending = &c
				} else {
					pending.Merge(c)
				}
			case out <- next:
				pending = nil
			case <-idle.C:
				return
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

// refused reads the daemon's answer to a failed request.
func refused(resp *http.Response) error {
	var e struct {
		Error     string `json:"error"`
		Detail    string `json:"detail"`
		Source    string `json:"source"`
		Forceable bool   `json:"forceable"`
		Fix       string `json:"fix"`
		Refusal   string `json:"refusal"`
	}
	raw, _ := io.ReadAll(resp.Body)
	if json.Unmarshal(raw, &e) != nil || e.Error == "" {
		e.Error = fmt.Sprintf("%s: %s", resp.Status, bytes.TrimSpace(raw))
	}
	return &daemonError{msg: e.Error, detail: e.Detail, source: e.Source, forceable: e.Forceable, fix: e.Fix, refusal: e.Refusal}
}

// daemonError is the daemon's answer to a failed request, with the message of
// the tool that refused, when there is one.
type daemonError struct {
	msg, detail, source string
	forceable           bool
	// fix is the command the daemon names to get past the refusal.
	fix string
	// refusal is the code of the profile text msg is (api.RefusalNotOffered,
	// api.BuiltinWhy, api.RefuseCase), "" for another refusal.
	refusal string
}

// Refusal is the refusal's code, "" when it has none.
func (e *daemonError) Refusal() string { return e.refusal }

func (e *daemonError) Error() string  { return "daemon: " + e.msg }
func (e *daemonError) Detail() string { return e.detail }

// DetailSource names the tool the detail comes from, "" for the loop.
func (e *daemonError) DetailSource() string { return e.source }

// Hint tells what to try next, "" for nothing.
func (e *daemonError) Hint() string {
	if e.fix != "" {
		return e.fix
	}
	if e.forceable {
		return "add --force to remove it anyway; that deletes for good what the refusal names"
	}
	return ""
}
