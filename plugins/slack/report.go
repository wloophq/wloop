package slack

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	sdk "github.com/wloophq/wloop/pkg/plugin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
	dslack "github.com/wloophq/wloop/plugins/slack/domain"
	"github.com/wloophq/wloop/plugins/slack/host"
	"github.com/wloophq/wloop/plugins/slack/items"
	slackrows "github.com/wloophq/wloop/plugins/slack/rows"
	"github.com/wloophq/wloop/plugins/slack/send"
	"github.com/wloophq/wloop/plugins/slack/store"
	"github.com/wloophq/wloop/plugins/slack/unit"
)

// The situation kind of a Slack unit whose verdict resolved it with no item
// open: the host's kind of a closed row.
const kindDone = "done"

// reportedCursor holds the units the plugin last reported, so a restart
// reports the ones that left as gone.
const reportedCursor = "reported_units"

// reporter reports the plugin's units to the host: every listed unit once
// it starts, then the units each store commit or host note changed, and
// every unit again when a reported fact changes with time alone.
type reporter struct {
	p    *plugin
	wake chan struct{}

	mu    sync.Mutex
	dirty map[unit.Unit]bool
	all   bool
	due   time.Time
	sent  map[unit.Unit][32]byte
	// full is set once a report of every listed unit reached the host.
	full bool
}

func newReporter(p *plugin) *reporter {
	r := &reporter{p: p, wake: make(chan struct{}, 1), dirty: map[unit.Unit]bool{}, all: true, sent: map[unit.Unit][32]byte{}}
	p.db.OnChange(r.changed)
	r.poke()
	return r
}

func (r *reporter) changed(c store.Change) {
	if c.All {
		r.markAll()
		return
	}
	r.mark(c.Units...)
}

// mark asks for a report of units.
func (r *reporter) mark(units ...unit.Unit) {
	r.mu.Lock()
	for _, u := range units {
		if u.Provider == dslack.Provider {
			r.dirty[u] = true
		}
	}
	r.mu.Unlock()
	r.poke()
}

// markAll asks for a report of every listed unit.
func (r *reporter) markAll() {
	r.mu.Lock()
	r.all = true
	r.mu.Unlock()
	r.poke()
}

func (r *reporter) poke() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// loaded says a report of every listed unit reached the host.
func (r *reporter) loaded() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.full
}

// run reports until ctx ends; a failed report is retried after retry.
func (r *reporter) run(ctx context.Context, retry time.Duration) {
	for {
		r.mu.Lock()
		due := r.due
		r.mu.Unlock()
		var timer <-chan time.Time
		var t *time.Timer
		if !due.IsZero() {
			t = time.NewTimer(max(due.Sub(r.p.now()), 0))
			timer = t.C
		}
		fired := false
		select {
		case <-ctx.Done():
		case <-r.wake:
		case <-timer:
			fired = true
		}
		if t != nil {
			t.Stop()
		}
		if ctx.Err() != nil {
			return
		}
		if fired {
			r.mu.Lock()
			r.due = time.Time{}
			r.mu.Unlock()
			r.markAll()
			continue
		}
		if err := r.flush(ctx); err != nil && ctx.Err() == nil {
			r.p.logf("slack report: %v", err)
			if !sleep(ctx, retry) {
				return
			}
			r.poke()
		}
	}
}

// flush reports what is dirty; on failure what it took is dirty again.
func (r *reporter) flush(ctx context.Context) error {
	r.mu.Lock()
	all, dirty := r.all, r.dirty
	r.all, r.dirty = false, map[unit.Unit]bool{}
	r.mu.Unlock()
	if !all && len(dirty) == 0 {
		return nil
	}
	err := r.report(ctx, all, slices.Collect(maps.Keys(dirty)))
	if err != nil {
		r.mu.Lock()
		r.all = r.all || all
		maps.Copy(r.dirty, dirty)
		r.mu.Unlock()
	}
	return err
}

// report sends the snapshots of units, of every listed unit when all, and
// a gone snapshot of each unit reported before and no longer listed.
func (r *reporter) report(ctx context.Context, all bool, units []unit.Unit) error {
	p := r.p
	rows := p.rowsSource()
	listed, err := rows.Listed(ctx)
	if err != nil {
		return err
	}
	if !p.cfg.Configured() {
		listed = nil
	}
	if all {
		units = append(units, listed...)
		prev, err := r.reported(ctx)
		if err != nil {
			return err
		}
		units = append(units, prev...)
	}
	slices.SortFunc(units, unit.Compare)
	units = slices.Compact(units)
	ins, err := rows.Load(ctx, units)
	if err != nil {
		return err
	}
	tasks, err := p.tasks(ctx, units)
	if err != nil {
		return err
	}
	now := p.now()
	var snaps []src.Snapshot
	var due time.Time
	r.mu.Lock()
	sent := maps.Clone(r.sent)
	r.mu.Unlock()
	for _, u := range units {
		in, ok := ins[u]
		if !ok || !slices.Contains(listed, u) {
			if _, was := sent[u]; was || all {
				snaps = append(snaps, src.Snapshot{Unit: u.Wire(), Gone: true, At: now})
				delete(sent, u)
			}
			continue
		}
		snap, next := p.snapshot(u, in, tasks[u.Wire()], now)
		if !next.IsZero() && (due.IsZero() || next.Before(due)) {
			due = next
		}
		sum := digest(snap)
		if old, was := sent[u]; was && old == sum {
			continue
		}
		snap.Rev = uint64(now.UnixNano())
		sent[u] = sum
		snaps = append(snaps, snap)
	}
	if len(snaps) > 0 {
		if p.host.Host.Reporter == nil {
			return src.ErrUnsupported
		}
		if err := p.host.Host.Reporter.Report(ctx, snaps); err != nil {
			return err
		}
	}
	r.mu.Lock()
	r.sent = sent
	if all {
		r.full = true
	}
	switch {
	case all:
		r.due = due
	case !due.IsZero() && (r.due.IsZero() || due.Before(r.due)):
		r.due = due
	}
	r.mu.Unlock()
	if all {
		return r.keep(ctx, slices.Collect(maps.Keys(sent)))
	}
	return nil
}

func digest(s src.Snapshot) [32]byte {
	raw, _ := json.Marshal(s)
	return sha256.Sum256(raw)
}

// reported are the units the last full report named.
func (r *reporter) reported(ctx context.Context) ([]unit.Unit, error) {
	raw, err := r.p.db.Slack().Cursor(ctx, reportedCursor)
	if err != nil || raw == "" {
		return nil, err
	}
	var keys []string
	if err := json.Unmarshal([]byte(raw), &keys); err != nil {
		return nil, nil
	}
	out := make([]unit.Unit, len(keys))
	for i, k := range keys {
		out[i] = unit.Unit{Provider: dslack.Provider, Key: k}
	}
	return out, nil
}

func (r *reporter) keep(ctx context.Context, units []unit.Unit) error {
	slices.SortFunc(units, unit.Compare)
	keys := make([]string, len(units))
	for i, u := range units {
		keys[i] = u.Key
	}
	raw, err := json.Marshal(keys)
	if err != nil {
		return err
	}
	return r.p.db.Slack().SetCursor(ctx, reportedCursor, string(raw))
}

// rowsSource is what the units' rows read.
func (p *plugin) rowsSource() *slackrows.Provider {
	return slackrows.New(slackrows.Source{DB: p.db, Viewer: p.src.store.Viewer, Now: p.now, Items: p.host.Load,
		Open: func(ctx context.Context) (map[unit.Unit][]items.Item, error) { return p.host.Open(ctx, nil) }})
}

// tasks are the host's tasks linked to units, by unit; none when the host
// does not say.
func (p *plugin) tasks(ctx context.Context, units []unit.Unit) (map[sdk.Unit][]src.Task, error) {
	out := map[sdk.Unit][]src.Task{}
	if p.host.Host.Reader == nil || len(units) == 0 {
		return out, nil
	}
	wire := make([]sdk.Unit, len(units))
	for i, u := range units {
		wire[i] = u.Wire()
	}
	ts, err := p.host.Host.Reader.Tasks(ctx, wire)
	if errors.Is(err, src.ErrUnsupported) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	for _, t := range ts {
		for _, u := range t.Units {
			out[u] = append(out[u], t)
		}
	}
	return out, nil
}

// snapshot is what the plugin reports of u, and when a fact of it next
// changes with time alone.
func (p *plugin) snapshot(u unit.Unit, in *slackrows.Input, tasks []src.Task, now time.Time) (src.Snapshot, time.Time) {
	row := slackrows.Derive(in)
	facts, next := unitFacts(u, in, tasks, p.cfg.EndedAfterFor(), now)
	s := src.Snapshot{Unit: u.Wire(), At: now, Data: row.Payload, Facts: facts, Views: row.Views, Links: unitLinks(u, in)}
	sit := &src.Situation{Kind: string(row.Kind), WaitingOn: row.Summary.WaitsOn, Since: row.Summary.Since, Reason: row.Summary.Reason}
	if row.Summary.Closed {
		sit.Kind = kindDone
	}
	s.Situation = sit
	if c, err := Card(row); err == nil {
		s.Card = &c
	}
	for id, vs := range row.Facets {
		if len(vs) > 0 {
			if s.Facets == nil {
				s.Facets = map[string]string{}
			}
			s.Facets[id] = vs[0]
		}
	}
	for _, it := range in.Items() {
		if it.State == "open" {
			s.Items = append(s.Items, host.WireItem(it))
		}
	}
	return s, next
}

// unitLinks are the addresses u's stored messages link to, other than
// Slack's own messages, which the host resolves with every plugin's codec.
func unitLinks(u unit.Unit, in *slackrows.Input) []src.Link {
	var out []src.Link
	seen := map[string]bool{}
	conv := in.Key().Conversation
	for _, m := range in.Messages() {
		if m.Deleted {
			continue
		}
		for _, raw := range m.Links {
			url := strings.TrimSpace(raw)
			if url == "" || seen[url] || send.IsMessage(url) {
				continue
			}
			seen[url] = true
			at := dslack.TSTime(m.TS)
			out = append(out, src.Link{URL: url, Kind: "mention",
				Evidence: src.Anchor{Unit: u.Wire(), Ref: send.Ref(conv, m.TS), Permalink: m.Permalink, At: at}})
		}
	}
	return out
}
