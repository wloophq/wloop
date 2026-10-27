package slack

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	factslack "github.com/wloophq/wloop/internal/adapter/facts/slack"
	slackrows "github.com/wloophq/wloop/internal/adapter/source/slack/rows"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/ingest"
	"github.com/wloophq/wloop/internal/core/port"
	dslack "github.com/wloophq/wloop/internal/domain/slack"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/readmodel"
	slackapi "github.com/wloophq/wloop/internal/slack"
	"github.com/wloophq/wloop/internal/slack/auth"
	"github.com/wloophq/wloop/internal/slack/send"
	"github.com/wloophq/wloop/internal/slack/socket"
	sourcereg "github.com/wloophq/wloop/internal/source"
)

// IngestEvery is the Slack ingestion job's interval.
const IngestEvery = time.Minute

// Skip texts of the ingestion job.
const (
	SkipNotConfigured = "slack: not configured"
	SkipNotLoggedIn   = "slack: not logged in"
	SkipNoStore       = "slack: no store"
)

// Slack are the handles the CLI builds for the plugin: the token store and
// the client that calls Slack with the user token. Tests set the others.
type Slack struct {
	Tokens *auth.Store
	Client *slackapi.Client
	// Open is apps.connections.open; nil calls Client.ConnectionsOpen with the app token.
	Open func(ctx context.Context) (string, error)
	// Dial nil is coder/websocket.
	Dial    func(ctx context.Context, url string) (socket.Socket, error)
	Metrics *Metrics
	// Now nil is the daemon's clock.
	Now func() time.Time
	// Debounce zero is Debounce.
	Debounce time.Duration
	// Recheck is how often an idle Run looks for a login; zero is IngestEvery.
	Recheck time.Duration
}

// plugin is the Slack module's state shared by its job, stream and track.
type plugin struct {
	cfg     config.Slack
	deps    sourcereg.Deps
	slack   Slack
	now     func() time.Time
	src     *Source
	events  *stream
	watches Watches
	poll    *poller
	log     func(format string, args ...any)
	logins  logins
	started time.Time
	briefs  *briefs
}

// Module is the Slack source: an ingestion job every minute, the socket
// stream and follows. Without a store, a login or an app token it stays idle.
func Module(cfg config.Config, d sourcereg.Deps, s Slack) (sourcereg.Module, error) {
	p := newPlugin(cfg.Slack, d, s)
	m := p.module()
	m.Jobs = append(m.Jobs, p.extraction(cfg)...)
	if p.briefs.job != nil {
		m.Briefs = p.briefs
	}
	if p.src != nil {
		m.Payload = p.payloads(cfg.Slack.Turns.MaxChars)
		pl := p.places(cfg)
		m.PlacementHints, m.PlacementDefaults = pl, pl
	}
	return m, nil
}

func (p *plugin) module() sourcereg.Module {
	m := sourcereg.Module{
		ID:      dslack.Provider,
		Codec:   dslack.Codec(),
		Ingest:  ingest.Job{Every: IngestEvery, Prepare: p.prepare},
		Health:  p.health,
		Rows:    p.rows,
		Card:    Card,
		Run:     p.run,
		Track:   p.track,
		Untrack: p.untrack,

		Publisher: p.publisher(),
		Reacts:    true,
		OnPosted:  p.onPosted(),
	}
	if p.cfg.Configured() {
		m.Identity = p.identity
	}
	if p.deps.DB != nil {
		m.Facts = factslack.New(p.deps.DB, clockFunc(p.now))
	}
	if p.deps.DB != nil && p.slack.Client != nil && p.slack.Tokens != nil {
		m.Resolve = p.resolve
	}
	return m
}

func (p *plugin) rows(rd readmodel.RowDeps) readmodel.Provider {
	if p.src == nil || !p.cfg.Configured() {
		return noRows{}
	}
	return slackrows.New(slackrows.Source{DB: p.deps.DB, Viewer: p.src.store.Viewer, Now: p.now, Activity: rd.Activity})
}

func newPlugin(cfg config.Slack, d sourcereg.Deps, s Slack) *plugin {
	p := &plugin{cfg: cfg, deps: d, slack: s, log: d.Log}
	p.briefs = &briefs{p: p}
	switch {
	case s.Now != nil:
		p.now = s.Now
	case d.Clock != nil:
		p.now = d.Clock.Now
	default:
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
	if d.DB == nil {
		return p
	}
	db := d.DB.Slack()
	segs := Segments{Store: d.DB.Segments(), Clock: d.Clock}
	p.watches = Watches{DB: db}
	a := newAdmitter(cfg.Ingest)
	a.db, a.log = db, p.logf
	a.users = &userCache{db: db, client: s.Client, now: p.now}
	heat := HeatScheduler{DB: db, Heat: sqlite.HeatReader{DB: d.DB, Clock: clockFunc(p.now)}, Segments: segs, Log: p.logf}
	src := &Source{db: db, store: &Store{DB: db, Segments: segs, Scheduler: heat, Now: p.now}, client: s.Client, admit: a, segments: segs,
		links: d.Links != nil, units: d.Units, log: p.logf}
	a.viewer = func() string { _, u := src.viewer(); return u }
	if s.Client != nil {
		src.search = newSearcher(s.Client, db, cfg.Ingest.Terms, cfg.Ingest.Since, src.viewer, p.now, p.slack.Metrics, p.logf)
	}
	src.inbox = newInbox(s.Debounce, p.touch, p.logf)
	a.pending = src.inbox.message
	p.src = src
	p.poll = newPoller(p)
	p.poll.Search = p.search
	p.events = &stream{now: p.now, team: func() string { t, _ := src.viewer(); return t }, metrics: p.slack.Metrics, log: p.logf,
		deliver: p.deliver}
	return p
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

func (p *plugin) prepare(ctx context.Context, _ time.Time) (ingest.Plan, string, error) {
	skip, err := p.ready(ctx)
	if err != nil || skip != "" {
		return ingest.Plan{}, skip, err
	}
	if skip := p.poll.budget(); skip != "" {
		return ingest.Plan{}, skip, nil
	}
	return ingest.Plan{Registration: p.registration(), After: p.after}, "", nil
}

func (p *plugin) registration() ingest.Registration {
	r := ingest.Registration{Source: polled{p.src, p.poll}, Store: p.src.store, Codec: dslack.Codec(), Links: p.deps.Links}
	if p.deps.Units != nil {
		r.Units = p.deps.Units()
	}
	return r
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

// track watches a Slack unit's object for the user once the track is admitted.
func (p *plugin) track(_ context.Context, u unit.Unit) (port.TrackRead, error) {
	k, err := dslack.ParseUnit(u)
	if err != nil {
		return port.TrackRead{}, fmt.Errorf("%w: %v", port.ErrNotFound, err)
	}
	if p.src == nil {
		return port.TrackRead{}, errors.New("slack: no store")
	}
	return port.TrackRead{Unit: u, Save: func(ctx context.Context) error {
		ref, err := p.objectOf(ctx, k)
		if err != nil {
			return err
		}
		return p.watches.Watch(ctx, ref, dslack.ReasonUserFollow)
	}}, nil
}

// untrack stops watching a unit's object. Unwatch clears the watch whatever
// other reasons the object holds.
func (p *plugin) untrack(ctx context.Context, u unit.Unit) error {
	k, err := dslack.ParseUnit(u)
	if err != nil {
		return fmt.Errorf("%w: %v", port.ErrNotFound, err)
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

// noRows serves no Slack rows: the plugin has no store or Slack is not configured.
type noRows struct{}

func (noRows) Provider() unit.ProviderID { return dslack.Provider }

func (noRows) Listed(context.Context) ([]unit.Unit, error) { return nil, nil }

func (noRows) Load(context.Context, []unit.Unit) (map[unit.Unit]any, error) { return nil, nil }

func (noRows) Derive(readmodel.Base, any, time.Time) (api.Row, error) {
	return api.Row{}, errors.New("slack: no rows yet")
}

// resolve is the unit a message ref or permalink lands in: the tracked
// direct message segment or thread it is in, else its thread.
func (p *plugin) resolve(ctx context.Context, address string) (unit.Unit, bool, error) {
	if !send.IsMessage(address) {
		return unit.Unit{}, false, nil
	}
	tok, err := p.slack.Tokens.Load()
	if err != nil {
		return unit.Unit{}, false, err
	}
	u, err := LandingOver(p.deps.DB, p.log).MessageUnit(ctx, p.slack.Client, tok.Team, address)
	return u, err == nil, err
}
