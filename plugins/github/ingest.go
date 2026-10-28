package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
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
	"github.com/wloophq/wloop/plugins/github/pr"
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
}

func (p *Plugin) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

func (p *Plugin) brand() string {
	if b := os.Getenv(sdk.BrandEnv); b != "" {
		return b
	}
	return "the loop"
}

// start builds what a started plugin runs over db and the host's broker.
func (p *Plugin) start(ctx context.Context, db *store.DB, host src.Host) *running {
	r := &running{now: p.now, db: db, host: host, dirty: map[pr.Ref]bool{}}
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

func (r *running) snapshots(ctx context.Context, refs []pr.Ref) ([]src.Snapshot, error) {
	rows, err := r.db.PRs(ctx, refs)
	if err != nil {
		return nil, err
	}
	details, err := r.db.Details(ctx, refs)
	if err != nil {
		return nil, err
	}
	tracked, err := r.db.Tracked(ctx)
	if err != nil {
		return nil, err
	}
	rev, err := r.nextRev(ctx)
	if err != nil {
		return nil, err
	}
	at := r.now()
	var out []src.Snapshot
	for _, ref := range refs {
		row, ok := rows[ref]
		if !ok {
			continue
		}
		d := Data{PR: row, Tracked: slices.Contains(tracked, ref)}
		if det, ok := details[ref]; ok {
			d.Detail, d.DetailVersion, d.DetailReadAt = &det, det.Version, det.ReadAt
		}
		raw, err := json.Marshal(d)
		if err != nil {
			return nil, err
		}
		out = append(out, src.Snapshot{Unit: ref.Unit(), Rev: rev, At: at, Data: raw})
	}
	return out, nil
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

// load reports every stored pull request once, which the host waits for
// before it counts the plugin loaded.
func (r *running) load(ctx context.Context) {
	err := r.report(ctx, true)
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
		rep, err = r.col.Fast(ctx)
	case JobSlow:
		rep, err = r.col.Slow(ctx)
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
