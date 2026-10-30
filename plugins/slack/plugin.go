// Package slack is the Slack source plugin: it reads the conversations the
// user takes part in through Socket Mode, polling and search, keeps them in
// its own store, extracts their items through the host's agent, reports
// each thread and DM segment to the host as a snapshot, posts the drafts
// the user approves and serves the Slack tools of a session.
package slack

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/slack/auth"
	"github.com/wloophq/wloop/plugins/slack/config"
	dslack "github.com/wloophq/wloop/plugins/slack/domain"
	"github.com/wloophq/wloop/plugins/slack/extract"
	"github.com/wloophq/wloop/plugins/slack/host"
	"github.com/wloophq/wloop/plugins/slack/ingest"
	"github.com/wloophq/wloop/plugins/slack/send"
	slackapi "github.com/wloophq/wloop/plugins/slack/slackapi"
	"github.com/wloophq/wloop/plugins/slack/socket"
	"github.com/wloophq/wloop/plugins/slack/store"
	"github.com/wloophq/wloop/plugins/slack/unit"
)

// IngestEvery is the Slack ingestion job's interval.
const IngestEvery = time.Minute

// The jobs of the plugin's manifest, which the host runs through Ingest.
const (
	JobIngest  = "ingest"
	JobExtract = "extract"
)

// Skip texts of the ingestion job.
const (
	SkipNotConfigured = "slack: not configured"
	SkipNotLoggedIn   = "slack: not logged in"
	SkipNoStore       = "slack: no store"
)

// Slack are the handles the plugin builds over its configuration: the token
// store and the client that calls Slack with the user token. Tests set the
// others.
type Slack struct {
	Tokens *auth.Store
	Client *slackapi.Client
	// Open is apps.connections.open; nil calls Client.ConnectionsOpen with the app token.
	Open func(ctx context.Context) (string, error)
	// Dial nil is coder/websocket.
	Dial    func(ctx context.Context, url string) (socket.Socket, error)
	Metrics *Metrics
	// Now nil is the wall clock.
	Now func() time.Time
	// Debounce zero is Debounce.
	Debounce time.Duration
	// Recheck is how often an idle Run looks for a login; zero is IngestEvery.
	Recheck time.Duration
}

// plugin is the Slack plugin's state shared by its jobs, stream, reports
// and tools.
type plugin struct {
	all     config.Config
	cfg     config.Slack
	host    host.Broker
	db      *store.DB
	slack   Slack
	now     func() time.Time
	src     *Source
	events  *stream
	watches Watches
	poll    *poller
	log     func(format string, args ...any)
	logins  logins
	started time.Time
	job     *extract.Job
	reports *reporter
	brand   string
}

// newPlugin builds the plugin over db, the plugin's store; a nil db is a
// plugin with no store, which stays idle.
func newPlugin(cfg config.Config, h host.Broker, db *store.DB, s Slack, log func(string, ...any)) *plugin {
	p := &plugin{all: cfg, cfg: cfg.Slack, host: h, db: db, slack: s, log: log, brand: cfg.Brand}
	p.now = s.Now
	if p.now == nil {
		p.now = time.Now
	}
	p.started = p.now()
	if p.slack.Metrics == nil {
		p.slack.Metrics = &Metrics{now: p.now}
	}
	if s.Client != nil && s.Client.OnRateLimited == nil {
		s.Client.OnRateLimited = p.slack.Metrics.RateLimited
	}
	if s.Client != nil && s.Client.Limits == nil {
		s.Client.Limits = &slackapi.Limiter{}
	}
	if db == nil {
		return p
	}
	sdb := db.Slack()
	segs := Segments{Store: db.Segments().Rehoming(gates{h}), Clock: clockFunc(p.now)}
	p.watches = Watches{DB: sdb}
	a := newAdmitter(cfg.Slack.Ingest)
	a.db, a.log = sdb, p.logf
	a.users = &userCache{load: userSource{db: sdb, client: s.Client, now: p.now}.fetch, now: p.now}
	heat := HeatScheduler{DB: sdb, Heat: LedgerHeat{Items: h.Load, WarmFor: cfg.Items.Heat.WarmForOr, Now: p.now}, Segments: segs, Log: p.logf}
	src := &Source{db: sdb, store: &Store{DB: sdb, Segments: segs, Scheduler: heat, Now: p.now}, client: s.Client, admit: a, segments: segs,
		log: p.logf}
	a.viewer = func() string { _, u := src.viewer(); return u }
	if s.Client != nil {
		src.search = newSearcher(s.Client, sdb, cfg.Slack.Ingest.Terms, cfg.Slack.Ingest.Since, src.viewer, p.now, p.slack.Metrics, p.logf)
	}
	src.inbox = newInbox(s.Debounce, p.touch, p.logf)
	a.pending = src.inbox.message
	p.src = src
	p.poll = newPoller(p)
	p.poll.Search = p.search
	p.events = &stream{now: p.now, team: func() string { t, _ := src.viewer(); return t }, metrics: p.slack.Metrics, log: p.logf,
		deliver: p.deliver}
	p.job = p.extraction()
	p.reports = newReporter(p)
	return p
}

// gates are the host's drafts as the segment map moves them.
type gates struct{ h host.Broker }

func (g gates) RehomeDrafts(ctx context.Context, moves []src.UnitMove) (int, error) {
	if g.h.Host.Gates == nil {
		return 0, nil
	}
	return g.h.Host.Gates.RehomeDrafts(ctx, moves)
}

// ready says why the plugin is idle, and learns the viewer from the token.
// It never deletes a token.
func (p *plugin) ready(ctx context.Context) (skip string, err error) {
	switch {
	case !p.cfg.Configured() || p.slack.Tokens == nil || p.slack.Client == nil:
		return SkipNotConfigured, nil
	case p.src == nil:
		return SkipNoStore, nil
	}
	tok, skip, err := p.logins.get(p.slack.Tokens, p.now())
	if err != nil || skip != "" {
		return skip, err
	}
	return "", p.src.setViewer(ctx, tok.Team, tok.User)
}

// ingestJob is one run of the ingestion job.
func (p *plugin) ingestJob(ctx context.Context) (summary, skipped string, err error) {
	skip, err := p.ready(ctx)
	if err != nil {
		return "", "", err
	}
	if skip != "" {
		return "skipped", skip, nil
	}
	if skip := p.poll.budget(); skip != "" {
		return "skipped", skip, nil
	}
	rep, err := ingest.Run(ctx, p.registration(), p.now())
	return p.after(ctx, rep, err)
}

func (p *plugin) registration() ingest.Registration {
	return ingest.Registration{Source: polled{p.src, p.poll}, Store: p.src.store, Valid: dslack.ValidKey}
}

func (p *plugin) touch(ctx context.Context, objects []string) error {
	_, err := ingest.Touch(ctx, p.registration(), objects, p.now())
	p.poll.touched(objects, err)
	return err
}

// run streams the socket into the inbox until ctx ends. Idle, it never
// dials and returns nil when ctx ends: without a config, a store or the
// socket it waits; logged out it looks for a login every Recheck.
func (p *plugin) run(ctx context.Context) error {
	for {
		skip, err := p.ready(ctx)
		if err != nil {
			return err
		}
		if skip == "" && p.cfg.Ingest.SocketOn() {
			return p.stream(ctx)
		}
		wait := p.slack.Recheck
		if wait <= 0 {
			wait = IngestEvery
		}
		if skip != SkipNotLoggedIn {
			wait = 0
		}
		if !sleep(ctx, wait) {
			return nil
		}
	}
}

// sleep waits d, forever when d is zero; false when ctx ended first.
func sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		<-ctx.Done()
		return false
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func (p *plugin) stream(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		p.src.inbox.run(ctx)
	}()
	go func() {
		defer wg.Done()
		p.poll.run(ctx)
	}()
	conn := &socket.Conn{Open: p.open, Dial: p.slack.Dial, OnState: p.onState, Log: p.logf}
	err := conn.Run(ctx, func(env socket.Envelope) { p.events.handle(ctx, env) })
	p.logins.forget(err)
	cancel()
	wg.Wait()
	return err
}

func (p *plugin) open(ctx context.Context) (string, error) {
	if p.slack.Open != nil {
		return p.slack.Open(ctx)
	}
	app, err := p.slack.Tokens.AppToken()
	if err != nil {
		return "", err
	}
	return p.slack.Client.ConnectionsOpen(ctx, app)
}

// deliver admits d into the inbox; a failed admission is logged, and the
// catch-up repairs what it lost.
func (p *plugin) deliver(ctx context.Context, d dslack.Delta) {
	v, err := p.src.admit.admit(ctx, d)
	if err != nil {
		if ctx.Err() == nil {
			p.logf("slack admit %s/%s: %v", d.Conversation, d.TS, err)
		}
		return
	}
	if v.rule == "" {
		return
	}
	p.slack.Metrics.Admitted(d.Via)
	p.src.inbox.add(v.object, v.kind, d)
}

// errNotFound is a unit that is not Slack's.
var errNotFound = errors.New("not found")

// track watches a Slack unit's object for the user.
func (p *plugin) track(ctx context.Context, u unit.Unit, follow bool) error {
	k, err := dslack.ParseUnit(u)
	if err != nil {
		return fmt.Errorf("%w: %v", errNotFound, err)
	}
	if p.src == nil {
		return errors.New("slack: no store")
	}
	if !follow {
		return nil
	}
	ref, err := p.objectOf(ctx, k)
	if err != nil {
		return err
	}
	return p.watches.Watch(ctx, ref, dslack.ReasonUserFollow)
}

// untrack stops watching a unit's object. Unwatch clears the watch whatever
// other reasons the object holds.
func (p *plugin) untrack(ctx context.Context, u unit.Unit) error {
	k, err := dslack.ParseUnit(u)
	if err != nil {
		return fmt.Errorf("%w: %v", errNotFound, err)
	}
	if p.src == nil {
		return errors.New("slack: no store")
	}
	ref, err := p.objectOf(ctx, k)
	if err != nil {
		return err
	}
	return p.watches.Unwatch(ctx, ref)
}

// objectOf is the object a unit lives in: its DM conversation for a segment
// or a thread in a DM, else its thread.
func (p *plugin) objectOf(ctx context.Context, k dslack.Key) (dslack.ObjectRef, error) {
	if k.Segment != "" {
		return dslack.ObjectRef{Conversation: k.Conversation}, nil
	}
	kind, err := p.conversationKind(ctx, k.Conversation)
	if err != nil {
		return dslack.ObjectRef{}, err
	}
	if kind.DM() {
		return dslack.ObjectRef{Conversation: k.Conversation}, nil
	}
	return dslack.ObjectRef{Conversation: k.Conversation, TS: k.TS}, nil
}

func (p *plugin) logf(format string, args ...any) {
	if p.log != nil {
		p.log(format, args...)
	}
}

// resolve is the unit a message ref or permalink lands in: the tracked
// direct message segment or thread it is in, else its thread.
func (p *plugin) resolve(ctx context.Context, address string) (unit.Unit, bool, error) {
	if !send.IsMessage(address) || p.db == nil || p.slack.Tokens == nil || p.slack.Client == nil {
		return unit.Unit{}, false, nil
	}
	tok, err := p.slack.Tokens.Load()
	if err != nil {
		return unit.Unit{}, false, err
	}
	u, err := LandingOver(p.db, p.log).MessageUnit(ctx, p.slack.Client, tok.Team, address)
	return u, err == nil, err
}
