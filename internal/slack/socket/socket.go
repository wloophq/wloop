// Package socket keeps a Slack Socket Mode connection.
package socket

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/slack"
)

type Envelope struct {
	ID, Type, EventID string // EventID from payload.event_id ("" for non-events_api)
	RetryAttempt      int
	RetryReason       string
	Payload           json.RawMessage
}

type StateKind string // "dialing", "hello", "disconnect", "dropped", "overflow"

const (
	StateDialing    StateKind = "dialing"
	StateHello      StateKind = "hello"
	StateDisconnect StateKind = "disconnect"
	StateDropped    StateKind = "dropped"
	StateOverflow   StateKind = "overflow"
)

type State struct {
	Kind           StateKind
	Reason         string        // disconnect reason ("warning", "refresh_requested", "link_disabled") or the drop error, URL-redacted
	NumConnections int           // from hello
	ApproxConnTime time.Duration // hello's debug_info.approximate_connection_time
	At             time.Time
}

type Socket interface {
	Read(ctx context.Context) ([]byte, error)
	Write(ctx context.Context, frame []byte) error
	Close(reason string) error
}

type Conn struct {
	Open    func(ctx context.Context) (wssURL string, err error)  // apps.connections.open with the app token
	Dial    func(ctx context.Context, url string) (Socket, error) // nil uses coder/websocket
	OnState func(State)                                           // nil ignores
	Log     func(format string, args ...any)
	Queue   int // handler buffer, default 4096

	minBackoff, maxBackoff time.Duration
	sleep                  func(ctx context.Context, d time.Duration) error
}

const (
	defaultQueue = 4096
	ackTimeout   = 10 * time.Second
)

var ErrInvalidAuth = errors.New("slack socket: invalid auth")

var errDisconnect = errors.New("server asked to disconnect")

// Run acks every envelope on the read loop before it is queued; handle runs on its own
// goroutine, so a slow handle never delays an ack. A full queue drops the envelope and
// reports State{Kind: "overflow"}. Backoff 1 s doubling to 30 s, reset after a hello.
// Returns ctx.Err() when ctx ends, after the running handle returns, or ErrInvalidAuth.
func (c *Conn) Run(ctx context.Context, handle func(Envelope)) error {
	size := c.Queue
	if size <= 0 {
		size = defaultQueue
	}
	queue := make(chan Envelope, size)
	hctx, stop := context.WithCancel(ctx)
	var wg sync.WaitGroup
	defer wg.Wait()
	defer stop()
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-hctx.Done():
				return
			case env := <-queue:
				handle(env)
			}
		}
	}()

	first, ceiling := c.backoffs()
	backoff := first
	for {
		hello, err := c.connect(ctx, queue)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, ErrInvalidAuth) {
			return err
		}
		if hello {
			backoff = first
		}
		if err := c.wait(ctx, backoff); err != nil {
			return ctx.Err()
		}
		backoff = min(backoff*2, ceiling)
	}
}

func (c *Conn) backoffs() (first, ceiling time.Duration) {
	first, ceiling = time.Second, 30*time.Second
	if c.minBackoff > 0 {
		first = c.minBackoff
	}
	if c.maxBackoff > 0 {
		ceiling = c.maxBackoff
	}
	return first, ceiling
}

func (c *Conn) wait(ctx context.Context, d time.Duration) error {
	if c.sleep != nil {
		return c.sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

type frame struct {
	Type           string          `json:"type"`
	EnvelopeID     string          `json:"envelope_id"`
	RetryAttempt   int             `json:"retry_attempt"`
	RetryReason    string          `json:"retry_reason"`
	Reason         string          `json:"reason"`
	NumConnections int             `json:"num_connections"`
	Payload        json.RawMessage `json:"payload"`
	DebugInfo      struct {
		ApproximateConnectionTime int `json:"approximate_connection_time"`
	} `json:"debug_info"`
}

// connect runs one connection until it ends; hello reports whether Slack greeted it.
func (c *Conn) connect(ctx context.Context, queue chan<- Envelope) (hello bool, err error) {
	c.state(State{Kind: StateDialing})
	u, err := c.Open(ctx)
	if err != nil {
		if invalidAuth(err) {
			c.logf("slack socket: %s", scrub(err.Error()))
			return false, fmt.Errorf("%w (%s)", ErrInvalidAuth, scrub(err.Error()))
		}
		return false, c.dropped("open: " + err.Error())
	}
	dial := c.Dial
	if dial == nil {
		dial = dialWebsocket
	}
	sock, err := dial(ctx, u)
	if err != nil {
		return false, c.dropped("dial " + RedactURL(u) + ": " + err.Error())
	}
	defer func() { _ = sock.Close("closing") }()
	for {
		data, err := sock.Read(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return hello, ctx.Err()
			}
			return hello, c.dropped("read: " + err.Error())
		}
		var f frame
		if err := json.Unmarshal(data, &f); err != nil {
			c.logf("slack socket: unparseable frame of %d bytes", len(data))
			continue
		}
		if f.EnvelopeID != "" {
			ack, _ := json.Marshal(map[string]string{"envelope_id": f.EnvelopeID})
			actx, cancel := context.WithTimeout(ctx, ackTimeout)
			err := sock.Write(actx, ack)
			cancel()
			if err != nil {
				return hello, c.dropped("ack: " + err.Error())
			}
		}
		switch f.Type {
		case "hello":
			hello = true
			c.state(State{Kind: StateHello, NumConnections: f.NumConnections,
				ApproxConnTime: time.Duration(f.DebugInfo.ApproximateConnectionTime) * time.Second})
		case "disconnect":
			c.state(State{Kind: StateDisconnect, Reason: f.Reason})
			c.logf("slack socket: disconnect requested (%s)", f.Reason)
			return hello, errDisconnect
		default:
			if f.EnvelopeID == "" {
				continue
			}
			env := Envelope{ID: f.EnvelopeID, Type: f.Type, RetryAttempt: f.RetryAttempt,
				RetryReason: f.RetryReason, Payload: f.Payload}
			if f.Type == "events_api" {
				var p struct {
					EventID string `json:"event_id"`
				}
				_ = json.Unmarshal(f.Payload, &p)
				env.EventID = p.EventID
			}
			select {
			case queue <- env:
			default:
				c.state(State{Kind: StateOverflow, Reason: f.Type})
				c.logf("slack socket: handler queue full, dropped a %s envelope", f.Type)
			}
		}
	}
}

func (c *Conn) dropped(msg string) error {
	msg = scrub(msg)
	c.state(State{Kind: StateDropped, Reason: msg})
	c.logf("slack socket: %s", msg)
	return errors.New(msg)
}

func (c *Conn) state(s State) {
	if c.OnState == nil {
		return
	}
	s.At = time.Now()
	c.OnState(s)
}

func (c *Conn) logf(format string, args ...any) {
	if c.Log != nil {
		c.Log(format, args...)
	}
}

var authCodes = []string{"invalid_auth", "not_authed", "account_inactive", "token_revoked", "token_expired", "not_allowed_token_type"}

func invalidAuth(err error) bool {
	var api *slack.APIError
	return errors.As(err, &api) && slices.Contains(authCodes, api.Code)
}

// RedactURL keeps scheme, host and path; the query (ticket, app_id) never leaves the package.
func RedactURL(u string) string {
	p, err := url.Parse(u)
	if err != nil || p.Scheme == "" || p.Host == "" {
		return "[url]"
	}
	return p.Scheme + "://" + p.Host + p.EscapedPath()
}

var (
	urlRE   = regexp.MustCompile(`[A-Za-z][A-Za-z0-9+.-]*://[^\s"'<>]+`)
	queryRE = regexp.MustCompile(`(?i)[?&]?\b(ticket|app_id)=[^&\s"'<>]*`)
)

func scrub(s string) string {
	s = urlRE.ReplaceAllStringFunc(s, RedactURL)
	return queryRE.ReplaceAllString(s, "")
}
