package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	sdk "github.com/wloophq/wloop/pkg/plugin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/github/api"
	"github.com/wloophq/wloop/plugins/github/collect"
	"github.com/wloophq/wloop/plugins/github/gh"
	"github.com/wloophq/wloop/plugins/github/git"
	"github.com/wloophq/wloop/plugins/github/plan"
	"github.com/wloophq/wloop/plugins/github/pr"
	"github.com/wloophq/wloop/plugins/github/row"
	"github.com/wloophq/wloop/plugins/github/store"
)

// The jobs plugin.toml declares, which Ingest runs.
const (
	JobFast = "fast"
	JobSlow = "slow"
)

// metaRev is the meta key of the rev the last report carried.
const metaRev = "snapshot_rev"

var (
	_ src.Ingester = (*Plugin)(nil)
	_ src.Lander   = (*Plugin)(nil)
)

// running is what a started plugin holds.
type running struct {
	now     func() time.Time
	db      *store.DB
	host    src.Host
	client  *api.Client
	col     *collect.Collector
	follows *collect.Follows

	cfgMu sync.Mutex
	cfg   settings

	reportMu sync.Mutex
	dirtyMu  sync.Mutex
	dirty    map[pr.Ref]bool

	loadedMu sync.Mutex
	loaded   bool
	loadErr  string

	// loop is the loop's name, as the items it asks of the user name it.
	loop string

	dueMu sync.Mutex
	// dues are when each pull request's report changes with time alone.
	dues    map[pr.Ref]time.Time
	dueWake chan struct{}

	git   plan.Git
	lists listings
	views presenting
}

func (p *Plugin) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

func (p *Plugin) brand() string {
	if b := sdk.Brand(); b != "" {
		return b
	}
	return "the loop"
}

// start builds what a started plugin runs over db and the host's broker.
func (p *Plugin) start(ctx context.Context, db *store.DB, host src.Host) *running {
	r := &running{now: p.now, db: db, host: host, dirty: map[pr.Ref]bool{}, loop: p.brand(), dues: map[pr.Ref]time.Time{},
		dueWake: make(chan struct{}, 1)}
	r.git = p.Git
	if r.git == nil {
		r.git = git.Exec{}
	}
	if host.Recorder != nil && host.History != nil {
		db.UseHost(eventHost{host})
	}
	db.UseClock(p.now)
	db.OnChange(func(c store.Change) { r.mark(c.Refs...) })
	o := p.API
	if o.GH == nil {
		o.GH = p.gh()
	}
	o.UserAgent, o.Meter = p.brand(), r.meter
	r.client = api.NewClient(o)
	r.reloadConfig(ctx)
	if limits, err := collect.StoredBudget(ctx, db); err == nil && limits != nil {
		r.client.Restore(limits)
	}
	r.col = &collect.Collector{Store: db, GH: r.client, Cfg: r.collectConfig, Now: p.now, Log: logf}
	if host.Reader != nil {
		r.col.WS = host.Reader
	}
	if host.Reporter != nil {
		r.col.Changed = func(ctx context.Context, topics ...sdk.Topic) {
			if err := host.Reporter.Changed(ctx, nil, topics); err != nil {
				logf("changed %v: %v", topics, err)
			}
		}
	}
	if host.Reporter == nil {
		r.loaded = true
	}
	r.follows = &collect.Follows{Store: db, Read: r.col.ReadOne, Save: r.col.Save, Now: p.now, Name: p.brand()}
	return r
}

// eventHost is the host's event log as the store keeps its events there.
type eventHost struct{ h src.Host }

func (e eventHost) RecordEvents(ctx context.Context, evs []src.Event) error {
	return e.h.Recorder.RecordEvents(ctx, evs)
}

func (e eventHost) Events(ctx context.Context, q src.EventQuery) ([]src.Event, error) {
	return e.h.History.Events(ctx, q)
}

// meter sends a sample of the client's to the host, under the meter scope
// of the host's call it serves.
func (r *running) meter(ctx context.Context, s api.Sample) {
	if r.host.Services == nil {
		return
	}
	err := r.host.Services.Meter(ctx, src.Sample{Scope: src.CallOf(ctx).MeterScope, Resource: "github", Op: s.Op, Spawn: s.Spawn,
		Duration: s.Dur, Failed: s.Failed, Points: s.Points})
	if err != nil && !errors.Is(err, src.ErrUnsupported) {
		logf("meter: %v", err)
	}
}

// reloadConfig reads the configuration in force, keeping the last one
// when the read fails.
func (r *running) reloadConfig(ctx context.Context) {
	var cfg src.Config
	if r.host.Services != nil {
		var err error
		if cfg, err = r.host.Services.Config(ctx); err != nil && !errors.Is(err, src.ErrUnsupported) {
			logf("config: %v", err)
			return
		}
	}
	s, err := settingsOf(cfg)
	if err != nil {
		logf("config: %v", err)
		return
	}
	r.cfgMu.Lock()
	r.cfg = s
	r.cfgMu.Unlock()
	r.client.SetFloors(api.DefaultFloors(s.Collect.RateLimitFraction))
}

func (r *running) settings() settings {
	r.cfgMu.Lock()
	defer r.cfgMu.Unlock()
	return r.cfg
}

func (r *running) collectConfig() collect.Config {
	r.cfgMu.Lock()
	defer r.cfgMu.Unlock()
	return r.cfg.Collect
}

func (r *running) mark(refs ...pr.Ref) {
	r.dirtyMu.Lock()
	defer r.dirtyMu.Unlock()
	for _, ref := range refs {
		r.dirty[ref] = true
	}
}

// Data is a pull request's snapshot data: its row, its detail and whether
// the user tracks it.
type Data struct {
	PR            pr.PR         `json:"pr"`
	Detail        *store.Detail `json:"detail,omitempty"`
	DetailVersion int           `json:"detail_version,omitempty"`
	DetailReadAt  time.Time     `json:"detail_read_at,omitzero"`
	Tracked       bool          `json:"tracked,omitempty"`
	// Row is the source part of the pull request's row payload.
	Row *row.Payload `json:"row,omitempty"`
	// Adopted says the pull request is taken over into the loop.
	Adopted bool `json:"adopted,omitempty"`
	// Plan is the planner's part.
	Plan *PlanData `json:"plan,omitempty"`
}

// report reports the pull requests whose rows changed since the last
// report; with all, every stored one.
func (r *running) report(ctx context.Context, all bool) error {
	if r.host.Reporter == nil {
		return nil
	}
	r.reportMu.Lock()
	defer r.reportMu.Unlock()
	r.dirtyMu.Lock()
	refs := make([]pr.Ref, 0, len(r.dirty))
	for ref := range r.dirty {
		refs = append(refs, ref)
	}
	r.dirty = map[pr.Ref]bool{}
	r.dirtyMu.Unlock()
	if all {
		rows, err := r.db.ListPRs(ctx, false)
		if err != nil {
			return err
		}
		refs = refs[:0]
		for _, p := range rows {
			refs = append(refs, p.Ref)
		}
	}
	if len(refs) == 0 {
		return nil
	}
	slices.SortFunc(refs, pr.Compare)
	for i, chunk := range slices.Collect(slices.Chunk(refs, reportChunk)) {
		snaps, err := r.snapshots(ctx, chunk)
		if err == nil {
			err = r.host.Reporter.Report(ctx, snaps)
		}
		if err != nil {
			r.mark(refs[i*reportChunk:]...)
			return err
		}
	}
	return nil
}

// reportChunk bounds the snapshots of one Report call.
const reportChunk = 200

// snapshots are the reports of refs at now, the planner's facts and data
// with them; what planning leaves behind is settled with the host.
func (r *running) snapshots(ctx context.Context, refs []pr.Ref) ([]src.Snapshot, error) {
	at := r.now()
	rows, in, err := r.rowsWith(ctx, refs, at)
	if err != nil {
		return nil, err
	}
	pin, err := r.planInputs(ctx, refs, in.events, in.books)
	if err != nil {
		return nil, err
	}
	planner := plan.New(ctx, pin)
	settled := map[pr.Ref]plan.Settlement{}
	rev, err := r.nextRev(ctx)
	if err != nil {
		return nil, err
	}
	var out []src.Snapshot
	for _, d := range rows {
		ref := d.row.PR.Ref
		res, err := planner.Derive(ref)
		if err != nil {
			return nil, err
		}
		d.data.Plan = &PlanData{Rank: planner.RankOf(ref, res), Board: planner.BoardOf(ref, res)}
		if x := planner.Settle(ref, res); len(x.Marks)+len(x.Events) > 0 {
			settled[ref] = x
		}
		raw, err := json.Marshal(d.data)
		if err != nil {
			return nil, err
		}
		s := d.row.Report()
		s.Unit, s.Rev, s.At, s.Data = ref.Unit(), rev, at, raw
		s.Facts = append(s.Facts, planner.Facts(ref, res)...)
		out = append(out, s)
		due := d.row.Due()
		if t := res.Due(at); !t.IsZero() && (due.IsZero() || t.Before(due)) {
			due = t
		}
		r.dueAt(ref, due)
	}
	if err := r.settle(ctx, settled); err != nil {
		logf("settling the rounds: %v", err)
	}
	return out, nil
}

// derived is a stored pull request's source row and the data it reports.
type derived struct {
	row  row.Row
	data Data
}

// rows are the source rows of the stored pull requests of refs at at.
func (r *running) rows(ctx context.Context, refs []pr.Ref, at time.Time) ([]derived, error) {
	out, _, err := r.rowsWith(ctx, refs, at)
	return out, err
}

// rowsWith are rows, with what they read of the host.
func (r *running) rowsWith(ctx context.Context, refs []pr.Ref, at time.Time) ([]derived, rowInputs, error) {
	rows, err := r.db.PRs(ctx, refs)
	if err != nil {
		return nil, rowInputs{}, err
	}
	details, err := r.db.Details(ctx, refs)
	if err != nil {
		return nil, rowInputs{}, err
	}
	tracked, err := r.db.Tracked(ctx)
	if err != nil {
		return nil, rowInputs{}, err
	}
	in, err := r.rowInputs(ctx, refs)
	if err != nil {
		return nil, rowInputs{}, err
	}
	var out []derived
	for _, ref := range refs {
		p, ok := rows[ref]
		if !ok {
			continue
		}
		o, err := r.db.Override(ctx, ref)
		if err != nil {
			return nil, rowInputs{}, err
		}
		d := Data{PR: p, Tracked: slices.Contains(tracked, ref), Adopted: o.Adopted}
		ri := in.of(ref)
		ri.PR, ri.Override, ri.Now = p, o, at
		if det, ok := details[ref]; ok {
			d.Detail, d.DetailVersion, d.DetailReadAt = &det, det.Version, det.ReadAt
			ri.Detail = &det
		}
		x := row.Derive(ri)
		d.Row = &x.Payload
		out = append(out, derived{row: x, data: d})
	}
	return out, in, nil
}

// rowInputs are what the rows of refs read besides their pull requests:
// the viewer, the configuration, and from the host their events and the
// address turns of their books.
type rowInputs struct {
	base      row.Input
	events    map[pr.Ref][]pr.Event
	addressed map[pr.Ref]time.Time
	books     map[pr.Ref]src.Book
}

func (in rowInputs) of(ref pr.Ref) row.Input {
	out := in.base
	out.Events, out.AddressedAt = in.events[ref], in.addressed[ref]
	return out
}

func (r *running) rowInputs(ctx context.Context, refs []pr.Ref) (rowInputs, error) {
	viewer, err := r.db.Viewer(ctx)
	if err != nil {
		return rowInputs{}, err
	}
	teams, err := collect.MyTeams(ctx, r.db)
	if err != nil {
		return rowInputs{}, err
	}
	r.cfgMu.Lock()
	cfg := r.cfg
	r.cfgMu.Unlock()
	out := rowInputs{base: row.Input{Viewer: viewer, Teams: teams, Ignore: cfg.Ignore, Stalled: cfg.Stalled, Loop: r.loop,
		MaxDetailAge: cfg.Collect.Slow + 2*cfg.Fast}, events: map[pr.Ref][]pr.Event{}, addressed: map[pr.Ref]time.Time{},
		books: map[pr.Ref]src.Book{}}
	units := make([]sdk.Unit, len(refs))
	for i, ref := range refs {
		units[i] = ref.Unit()
	}
	if r.host.History != nil {
		evs, err := r.host.History.Events(ctx, src.EventQuery{Units: units})
		if err != nil && !errors.Is(err, src.ErrUnsupported) {
			return rowInputs{}, err
		}
		for _, e := range evs {
			ref, ok := pr.RefOf(e.Unit)
			if !ok {
				continue
			}
			out.events[ref] = append(out.events[ref], pr.Event{PR: ref, Kind: e.Kind, Key: e.Key, Actor: e.Actor, At: e.At, Data: string(e.Data)})
		}
	}
	if r.host.Reader != nil {
		books, err := r.host.Reader.Books(ctx, units)
		if err != nil && !errors.Is(err, src.ErrUnsupported) {
			return rowInputs{}, err
		}
		for _, b := range books {
			if ref, ok := pr.RefOf(b.Unit); ok {
				out.books[ref] = b
				if at, ok := addressedAt(b); ok {
					out.addressed[ref] = at
				}
			}
		}
	}
	return out, nil
}

// The roles plugin.toml declares, which addressedAt reads.
const (
	roleReview  = "pr.review"
	roleAddress = "pr.address"
)

// addressedAt is when b's driving turn reached its state, for an address
// turn: the newest turn of a role that drives the row, else the newest.
func addressedAt(b src.Book) (time.Time, bool) {
	var newest, driving *src.Turn
	for i, t := range b.Turns {
		if newest == nil || t.Seq > newest.Seq {
			newest = &b.Turns[i]
		}
		if (t.Role == roleReview || t.Role == roleAddress) && (driving == nil || t.Seq > driving.Seq) {
			driving = &b.Turns[i]
		}
	}
	if driving == nil {
		driving = newest
	}
	if driving == nil || driving.Role != roleAddress {
		return time.Time{}, false
	}
	return driving.At, true
}

// nextRev is a rev above every rev the plugin reported before, kept across
// its restarts.
func (r *running) nextRev(ctx context.Context) (uint64, error) {
	raw, err := r.db.Meta(ctx, metaRev)
	if err != nil {
		return 0, err
	}
	var last uint64
	if raw != "" {
		if last, err = strconv.ParseUint(raw, 10, 64); err != nil {
			return 0, fmt.Errorf("meta %s: %w", metaRev, err)
		}
	}
	next := last + 1
	return next, r.db.SetMeta(ctx, metaRev, strconv.FormatUint(next, 10))
}

// dueAt asks for a report of ref at t, when its report changes with time
// alone; zero asks for none.
func (r *running) dueAt(ref pr.Ref, t time.Time) {
	r.dueMu.Lock()
	if t.IsZero() {
		delete(r.dues, ref)
	} else {
		r.dues[ref] = t
	}
	r.dueMu.Unlock()
	select {
	case r.dueWake <- struct{}{}:
	default:
	}
}

// watchDue runs report with the pull requests whose due instant passed,
// each time one does, until ctx ends.
func (r *running) watchDue(ctx context.Context, report func(context.Context, []pr.Ref) error) {
	for {
		r.dueMu.Lock()
		var due time.Time
		for _, t := range r.dues {
			if due.IsZero() || t.Before(due) {
				due = t
			}
		}
		r.dueMu.Unlock()
		var fire <-chan time.Time
		var t *time.Timer
		if !due.IsZero() {
			t = time.NewTimer(max(due.Sub(r.now()), 0))
			fire = t.C
		}
		fired := false
		select {
		case <-ctx.Done():
		case <-r.dueWake:
		case <-fire:
			fired = true
		}
		if t != nil {
			t.Stop()
		}
		if ctx.Err() != nil {
			return
		}
		if !fired {
			continue
		}
		now := r.now()
		var refs []pr.Ref
		r.dueMu.Lock()
		for ref, at := range r.dues {
			if !at.After(now) {
				refs = append(refs, ref)
				delete(r.dues, ref)
			}
		}
		r.dueMu.Unlock()
		slices.SortFunc(refs, pr.Compare)
		if err := report(ctx, refs); err != nil && ctx.Err() == nil {
			logf("report at its due time: %v", err)
		}
	}
}

// The host's retention, which the plugin applies to its own store: a pull
// request merged or closed that long ago goes with every row of it, and
// fetches that old.
const (
	endedRetention   = 400 * 24 * time.Hour
	fetchesRetention = 30 * 24 * time.Hour
)

// retain drops what the host's retention ends from the store and reports
// each dropped pull request gone.
func (r *running) retain(ctx context.Context) error {
	now := r.now()
	dropped, err := r.db.Retain(ctx, now.Add(-endedRetention), now.Add(-fetchesRetention))
	if err != nil || len(dropped) == 0 {
		return err
	}
	r.dirtyMu.Lock()
	for _, ref := range dropped {
		delete(r.dirty, ref)
	}
	r.dirtyMu.Unlock()
	if r.host.Reporter == nil {
		return nil
	}
	gone := make([]src.Snapshot, len(dropped))
	for i, ref := range dropped {
		gone[i] = src.Snapshot{Unit: ref.Unit(), At: now, Gone: true}
	}
	return r.host.Reporter.Report(ctx, gone)
}

// load applies the retention, then reports every stored pull request once,
// which the host waits for before it counts the plugin loaded.
func (r *running) load(ctx context.Context) {
	err := r.retain(ctx)
	if rerr := r.report(ctx, true); err == nil {
		err = rerr
	}
	r.loadedMu.Lock()
	defer r.loadedMu.Unlock()
	r.loaded = true
	if err != nil {
		r.loadErr = err.Error()
	}
}

func (r *running) isLoaded() (bool, string) {
	r.loadedMu.Lock()
	defer r.loadedMu.Unlock()
	return r.loaded, r.loadErr
}

func (p *Plugin) running() (*running, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.run == nil {
		return nil, fmt.Errorf("%w: the plugin has not started", src.ErrUnavailable)
	}
	return p.run, nil
}

// Ingest runs one of the manifest's jobs, then reports what it changed.
func (p *Plugin) Ingest(ctx context.Context, req src.IngestRequest) (src.IngestResult, error) {
	r, err := p.running()
	if err != nil {
		return src.IngestResult{}, err
	}
	r.reloadConfig(ctx)
	ctx = api.WithPriority(ctx, api.PriorityBackground)
	var rep collect.Report
	switch req.Job {
	case JobFast:
		r.lists.forget()
		rep, err = r.col.Fast(ctx)
	case JobSlow:
		rep, err = r.col.Slow(ctx)
		if err == nil {
			err = r.retain(ctx)
		}
	default:
		return src.IngestResult{}, fmt.Errorf("%w: no job %s", src.ErrUnsupported, req.Job)
	}
	if err == nil {
		collect.UntrackFinished(ctx, r.db, p.now, logf)
	}
	if rerr := r.report(ctx, false); rerr != nil && err == nil {
		err = rerr
	}
	summary, skipped, err := collect.Outcome(rep, err)
	return src.IngestResult{Summary: summary, Skipped: skipped}, err
}

func refOf(u sdk.Unit) (pr.Ref, error) {
	ref, ok := pr.RefOf(u)
	if !ok {
		return pr.Ref{}, fmt.Errorf("%w: %s is not a pull request", src.ErrNotFound, u.Key)
	}
	return ref, nil
}

// Track reads a pull request in full and follows it once the host, which
// admitted the read, asks to.
func (p *Plugin) Track(ctx context.Context, req src.TrackRequest) (src.Tracked, error) {
	r, err := p.running()
	if err != nil {
		return src.Tracked{}, err
	}
	ref, err := refOf(req.Unit)
	if err != nil {
		return src.Tracked{}, err
	}
	if !req.Follow {
		return r.follows.ReadTrack(ctx, ref)
	}
	t, err := r.follows.Follow(ctx, ref)
	if rerr := r.report(ctx, false); rerr != nil && err == nil {
		err = rerr
	}
	return t, err
}

// Untrack stops tracking a pull request.
func (p *Plugin) Untrack(ctx context.Context, u sdk.Unit) error {
	r, err := p.running()
	if err != nil {
		return err
	}
	ref, err := refOf(u)
	if err != nil {
		return err
	}
	err = r.follows.Untrack(ctx, ref)
	if rerr := r.report(ctx, false); rerr != nil && err == nil {
		err = rerr
	}
	return err
}

// Resolve is the pull request a page address or a key names.
func (p *Plugin) Resolve(_ context.Context, address string) (sdk.Unit, bool, error) {
	if ref, err := pr.ParseURL(address); err == nil {
		return ref.Unit(), true, nil
	}
	if ref, err := pr.ParseKey(strings.TrimSpace(address)); err == nil {
		return ref.Unit(), true, nil
	}
	return sdk.Unit{}, false, nil
}

// Locate is the pull request of the branch the checkout at dir has, as gh
// reads it.
func (p *Plugin) Locate(ctx context.Context, dir string) (sdk.Unit, bool, error) {
	args := []string{"pr", "view", "--json", "url", "--jq", ".url"}
	var out []byte
	var err error
	if d, ok := p.gh().(gh.DirRunner); ok && dir != "" {
		out, err = d.RunIn(ctx, dir, args...)
	} else if dir != "" {
		return sdk.Unit{}, false, fmt.Errorf("gh pr view: this runner cannot run in %s", dir)
	} else {
		out, err = p.gh().Run(ctx, args...)
	}
	if err != nil {
		return sdk.Unit{}, false, err
	}
	ref, err := pr.ParseURL(strings.TrimSpace(string(out)))
	if err != nil {
		return sdk.Unit{}, false, err
	}
	return ref.Unit(), true, nil
}

// Checkout is the repository, head and base a pull request's work checks
// out, as the store last read them.
func (p *Plugin) Checkout(ctx context.Context, u sdk.Unit) (src.Checkout, bool, error) {
	ref, ok := pr.RefOf(u)
	if !ok {
		return src.Checkout{}, false, nil
	}
	out := src.Checkout{Repo: ref.Owner + "/" + ref.Repo}
	r, err := p.running()
	if err != nil {
		return out, true, nil
	}
	row, found, err := r.db.PR(ctx, ref)
	if err != nil {
		return src.Checkout{}, false, err
	}
	if found {
		out.Branch, out.Head, out.Base, out.Fork = row.HeadBranch, row.HeadOid, row.BaseBranch, row.IsFork
	}
	return out, true, nil
}

func (p *Plugin) Landed(context.Context, src.Draft) (sdk.Unit, bool, error) {
	return sdk.Unit{}, false, src.ErrUnsupported
}

func (p *Plugin) WriteState(context.Context, src.StateWrite) (src.Evidence, error) {
	return src.Evidence{}, src.ErrUnsupported
}

// logf logs through the SDK's logger, as stdout carries the handshake.
func logf(format string, args ...any) { sdk.Logger().Info(fmt.Sprintf(format, args...)) }
