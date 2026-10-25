package readmodel

import (
	"bytes"
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/catalog"
	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/meter"
)

const (
	pokeQuiet    = 250 * time.Millisecond
	pokeMaxDelay = time.Second
	clockTick    = time.Minute
	tombstoneMax = 1024
)

// Model serves every provider's rows from one immutable snapshot, rebuilt
// whole when a row input changed or the minute moved.
type Model struct {
	changes   port.Changes
	snap      Snapshotter
	pending   Pending
	agents    Agents
	settings  Settings
	providers []Provider
	epoch     string
	now       func() time.Time
	logf      func(format string, args ...any)

	mu  sync.Mutex // serializes rebuilds
	cur atomic.Pointer[snapshot]

	keepMu sync.Mutex
	keep   func(ctx context.Context, k Kept)
	kept   *snapshot
}

// Built is a served entry with what its row was derived from.
type Built struct {
	Entry
	Provider Provider
	In       any
}

// Kept is a snapshot as its keeper sees it: the base, the minute and every
// row, shared with the snapshot and read-only.
type Kept struct {
	Base Base
	At   time.Time
	Rows []Built
}

type snapshot struct {
	rev     uint64
	applied port.Generation
	at      time.Time // the minute the rows were derived at
	base    Base
	list    []*entry // in listing order
	byUnit  map[unit.Unit]*entry
	gone    map[unit.Unit]uint64 // tombstones: the Rev a unit left at
	// floor is the newest Rev of a dropped tombstone; a client behind it
	// may miss a removal.
	floor uint64
	// origin is the Rev of the model's first snapshot, the oldest a client
	// can hold.
	origin uint64
}

type entry struct {
	Entry
	provider Provider
	in       any
}

// New builds a model over the providers. A nil changes rebuilds on every
// read; nil pending, agents or settings leave their Base fields zero.
func New(changes port.Changes, snap Snapshotter, pending Pending, agents Agents, settings Settings, providers ...Provider) *Model {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return &Model{changes: changes, snap: snap, pending: pending, agents: agents, settings: settings, providers: providers,
		epoch: hex.EncodeToString(b), now: time.Now, logf: func(string, ...any) {}}
}

// UseClock sets the clock rows are derived at.
func (m *Model) UseClock(now func() time.Time) { m.now = now }

// UseKeeper sets the bookkeeping Refresh does once per new snapshot, off
// the read path: the writes the rows imply.
func (m *Model) UseKeeper(keep func(ctx context.Context, k Kept)) { m.keep = keep }

// UseLog sets where Run logs a failed rebuild.
func (m *Model) UseLog(logf func(format string, args ...any)) { m.logf = logf }

func (m *Model) fresh(s *snapshot) bool {
	return s != nil && m.changes != nil && m.changes.Since(s.applied, port.RowInputs).Empty() &&
		m.now().Truncate(time.Minute).Equal(s.at)
}

// sync returns a snapshot that holds every write noted before the call.
func (m *Model) sync(ctx context.Context) (*snapshot, error) {
	if s := m.cur.Load(); m.fresh(s) {
		return s, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if s := m.cur.Load(); m.fresh(s) {
		return s, nil
	}
	return m.rebuild(ctx)
}

type listing struct {
	p     Provider
	units []unit.Unit
	in    map[unit.Unit]any
}

func (m *Model) base(ctx context.Context) (Base, error) {
	var b Base
	if m.settings != nil {
		var err error
		if b, err = m.settings.Settings(ctx); err != nil {
			return Base{}, err
		}
	}
	if m.pending != nil {
		p, err := m.pending.Pending(ctx)
		if err != nil {
			return Base{}, err
		}
		b.Pending = p
	}
	if m.agents != nil {
		b.Agents, b.AgentsOK = m.agents.Agents()
	}
	return b, nil
}

// load reads every provider's listing and inputs inside one snapshot of the
// store.
func (m *Model) load(ctx context.Context) ([]listing, error) {
	loaded := make([]listing, len(m.providers))
	load := func(ctx context.Context) error {
		for i, p := range m.providers {
			units, err := p.Listed(ctx)
			if err != nil {
				return fmt.Errorf("%s rows: %w", p.Provider(), err)
			}
			in, err := p.Load(ctx, units)
			if err != nil {
				return fmt.Errorf("%s rows: %w", p.Provider(), err)
			}
			loaded[i] = listing{p: p, units: units, in: in}
		}
		return nil
	}
	var err error
	if m.snap != nil {
		err = m.snap.Snapshot(ctx, load)
	} else {
		err = load(ctx)
	}
	return loaded, err
}

// derive encodes every listed unit's row at the minute at, in listing
// order; the entries have no Rev.
func derive(base Base, loaded []listing, at time.Time) ([]*entry, error) {
	var out []*entry
	seen := map[unit.Unit]bool{}
	for _, l := range loaded {
		for _, u := range l.units {
			in, ok := l.in[u]
			if !ok || seen[u] {
				continue
			}
			seen[u] = true
			row, err := l.p.Derive(base, in, at)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", u, err)
			}
			row = withIntents(base, u, row)
			raw, err := json.Marshal(row)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", u, err)
			}
			out = append(out, &entry{Entry: Entry{Unit: u, Row: row, JSON: raw}, provider: l.p, in: in})
		}
	}
	return out, nil
}

// withIntents sets the row's unit and, on a row of a provider other than GitHub, the intents' actions it
// offers: GitHub's deriver offers the whole catalog itself.
func withIntents(base Base, u unit.Unit, row api.Row) api.Row {
	row.Unit = u
	if u.Provider == unit.GitHub || base.Intents == nil {
		return row
	}
	row.Actions = catalog.UnitStates(catalog.Facts{Row: row, Tracked: true, Open: !row.Summary.Closed, Asked: base.Asked, Intents: base.Intents})
	row.SpecsVersion = base.SpecsVersion
	return row
}

// rebuild derives every row from one snapshot of the store; m.mu is held.
func (m *Model) rebuild(ctx context.Context) (*snapshot, error) {
	var gen port.Generation
	if m.changes != nil {
		gen = m.changes.Gen()
	}
	at := m.now().Truncate(time.Minute)
	base, err := m.base(ctx)
	if err != nil {
		return nil, err
	}
	loaded, err := m.load(ctx)
	if err != nil {
		return nil, err
	}
	entries, err := derive(base, loaded, at)
	if err != nil {
		return nil, err
	}
	prev := m.cur.Load()
	first := prev == nil
	if first {
		prev = &snapshot{}
	}
	next := &snapshot{rev: prev.rev, applied: gen, at: at, base: base, list: entries, byUnit: make(map[unit.Unit]*entry, len(entries)), gone: map[unit.Unit]uint64{}, floor: prev.floor, origin: prev.origin}
	var moved []*entry
	for _, e := range entries {
		if old, ok := prev.byUnit[e.Unit]; ok && bytes.Equal(old.JSON, e.JSON) {
			e.Rev = old.Rev
		} else {
			moved = append(moved, e)
		}
		next.byUnit[e.Unit] = e
	}
	var left []unit.Unit
	for _, e := range prev.list {
		if _, ok := next.byUnit[e.Unit]; !ok {
			left = append(left, e.Unit)
		}
	}
	for u, rev := range prev.gone {
		if _, back := next.byUnit[u]; !back {
			next.gone[u] = rev
		}
	}
	if len(moved) > 0 || len(left) > 0 {
		next.rev++
		for _, e := range moved {
			e.Rev = next.rev
		}
		for _, u := range left {
			next.gone[u] = next.rev
		}
		next.trim()
	}
	if first {
		next.origin = next.rev
	}
	m.cur.Store(next)
	return next, nil
}

// trim drops the oldest tombstones beyond tombstoneMax, raising floor.
func (s *snapshot) trim() {
	if len(s.gone) <= tombstoneMax {
		return
	}
	type stone struct {
		u   unit.Unit
		rev uint64
	}
	stones := make([]stone, 0, len(s.gone))
	for u, rev := range s.gone {
		stones = append(stones, stone{u, rev})
	}
	slices.SortFunc(stones, func(a, b stone) int {
		if c := cmp.Compare(a.rev, b.rev); c != 0 {
			return c
		}
		return api.CompareUnits(a.u, b.u)
	})
	for _, st := range stones[:len(stones)-tombstoneMax] {
		delete(s.gone, st.u)
		s.floor = max(s.floor, st.rev)
	}
}

// Rows is the view's rows: all of them when since is zero, of another
// epoch, ahead of Rev or behind the tombstones, else those that changed
// after since and the units that left. The rows are copies the caller may
// change.
func (m *Model) Rows(ctx context.Context, view string, since api.Since) (api.RowsPage, error) {
	s, err := m.sync(ctx)
	if err != nil {
		return api.RowsPage{}, err
	}
	page := api.RowsPage{Epoch: m.epoch, Rev: s.rev, Rows: []api.Row{}, Removed: []unit.Unit{}}
	page.Full = since == (api.Since{}) || since.Epoch != m.epoch || since.Rev > s.rev || since.Rev < s.floor
	for _, e := range s.list {
		if !page.Full && e.Rev <= since.Rev {
			continue
		}
		if e.Row.In(view) {
			page.Rows = append(page.Rows, cloneRow(e.Row))
		} else if !page.Full {
			page.Removed = append(page.Removed, e.Unit)
		}
	}
	if !page.Full {
		for u, rev := range s.gone {
			if rev > since.Rev {
				page.Removed = append(page.Removed, u)
			}
		}
		slices.SortFunc(page.Removed, api.CompareUnits)
	}
	slices.SortStableFunc(page.Rows, api.CompareRows)
	return page, nil
}

// Row is a copy of u's row, derived for a request that asks for an action;
// ok is false for a unit no provider lists.
func (m *Model) Row(ctx context.Context, u unit.Unit, asked catalog.Asked) (api.Row, bool, error) {
	s, err := m.sync(ctx)
	if err != nil {
		return api.Row{}, false, err
	}
	e, ok := s.byUnit[u]
	if !ok {
		return api.Row{}, false, nil
	}
	if asked.Name == "" && asked.Inputs == nil {
		return cloneRow(e.Row), true, nil
	}
	b := s.base
	b.Asked = asked
	r, err := e.provider.Derive(b, e.in, s.at)
	r = withIntents(b, u, r)
	return cloneRow(r), err == nil, err
}

// Labels are the labels of units in the latest snapshot, such as
// "owner/repo#12" or "#channel"; a unit it does not list has none. It never
// rebuilds the rows.
func (m *Model) Labels(units []unit.Unit) map[unit.Unit]string {
	s := m.cur.Load()
	if s == nil {
		return nil
	}
	out := make(map[unit.Unit]string, len(units))
	for _, u := range units {
		if e, ok := s.byUnit[u]; ok && e.Row.Label != "" {
			out[u] = e.Row.Label
		}
	}
	return out
}

// Presentation is a unit's label, title and link as its row shows them.
type Presentation struct{ Label, Title, Link string }

// Presentations are the presentations of the units the snapshot lists, without a sync.
func (m *Model) Presentations(units []unit.Unit) map[unit.Unit]Presentation {
	s := m.cur.Load()
	if s == nil {
		return nil
	}
	out := make(map[unit.Unit]Presentation, len(units))
	for _, u := range units {
		if e, ok := s.byUnit[u]; ok {
			out[u] = Presentation{Label: e.Row.Label, Title: e.Row.Title, Link: e.Row.Link}
		}
	}
	return out
}

// Read is one unit's entry of a snapshot: the served row, what it derives
// from and the base it was derived with.
type Read struct {
	Row  api.Row
	In   any
	Base Base
	At   time.Time
	p    Provider
}

// Derive is the unit's row from base and in, at the minute of the snapshot.
func (r Read) Derive(base Base, in any) (api.Row, error) { return r.p.Derive(base, in, r.At) }

// Read is u's entry; ok is false for a unit no provider lists. The row is a
// copy the caller may change; In is shared and must not be.
func (m *Model) Read(ctx context.Context, u unit.Unit) (Read, bool, error) {
	s, err := m.sync(ctx)
	if err != nil {
		return Read{}, false, err
	}
	e, ok := s.byUnit[u]
	if !ok {
		return Read{}, false, nil
	}
	return Read{Row: cloneRow(e.Row), In: e.in, Base: s.base, At: s.at, p: e.provider}, true, nil
}

// ReadMany is the entries of the units any provider lists, from one
// snapshot, with the base and the minute it holds.
func (m *Model) ReadMany(ctx context.Context, units []unit.Unit) (map[unit.Unit]Read, Base, time.Time, error) {
	s, err := m.sync(ctx)
	if err != nil {
		return nil, Base{}, time.Time{}, err
	}
	out := make(map[unit.Unit]Read, len(units))
	for _, u := range units {
		if e, ok := s.byUnit[u]; ok {
			out[u] = Read{Row: cloneRow(e.Row), In: e.in, Base: s.base, At: s.at, p: e.provider}
		}
	}
	return out, s.base, s.at, nil
}

// Base is what the current snapshot's rows share, and the minute they were
// derived at.
func (m *Model) Base(ctx context.Context) (Base, time.Time, error) {
	s, err := m.sync(ctx)
	if err != nil {
		return Base{}, time.Time{}, err
	}
	return s.base, s.at, nil
}

// Run keeps the snapshot current until ctx ends: it rebuilds on a row
// input's note, debounced, and on the minute, and publishes a Change each
// time the revision moved, starting from the model's first snapshot, since
// a client may have read it before Run started.
func (m *Model) Run(ctx context.Context, publish func(api.Change)) {
	var poke <-chan struct{}
	if m.changes != nil {
		p, cancel := change.Poke(m.changes, port.RowInputs)
		defer cancel()
		poke = p
	}
	var published uint64
	moved := func(s *snapshot) {
		if s.rev != published {
			publish(s.changeSince(m.epoch, published))
			published = s.rev
		}
	}
	if s, err := m.refresh(ctx); err != nil {
		m.logf("readmodel: %v", err)
	} else {
		published = s.origin
		moved(s)
	}
	tick := nextMinute()
	var quiet, late <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-poke:
			quiet = time.After(pokeQuiet)
			if late == nil {
				late = time.After(pokeMaxDelay)
			}
			continue
		case <-quiet:
		case <-late:
		case <-tick:
			tick = nextMinute()
		}
		quiet, late = nil, nil
		s, err := m.refresh(ctx)
		if err != nil {
			m.logf("readmodel: %v", err)
			continue
		}
		moved(s)
	}
}

// Since is what a client at since missed: the units whose row changed or
// left after it, or All when since is of another epoch, ahead of Rev or
// behind the tombstones. A zero since missed nothing, so its Change only
// says where the rows are.
func (m *Model) Since(ctx context.Context, since api.Since) (api.Change, error) {
	s, err := m.sync(ctx)
	if err != nil {
		return api.Change{}, err
	}
	switch {
	case since == (api.Since{}):
		return api.Change{Type: "changed", Epoch: m.epoch, Rev: s.rev}, nil
	case since.Epoch != m.epoch:
		return api.Change{Type: "changed", Epoch: m.epoch, Rev: s.rev, All: true}, nil
	}
	return s.changeSince(m.epoch, since.Rev), nil
}

// nextMinute fires at the next wall-clock minute, when the rows' minute
// moves.
func nextMinute() <-chan time.Time {
	now := time.Now()
	return time.After(now.Truncate(clockTick).Add(clockTick).Sub(now))
}

// refresh is sync under the read model's own meter scope, then the
// keeper's bookkeeping on a snapshot it has not kept yet.
func (m *Model) refresh(ctx context.Context) (*snapshot, error) {
	ctx, scope := meter.Start(ctx, meter.Loop("readmodel"))
	defer scope.End()
	s, err := m.sync(ctx)
	if err != nil || m.keep == nil {
		return s, err
	}
	m.keepMu.Lock()
	defer m.keepMu.Unlock()
	if m.kept == s {
		return s, nil
	}
	m.kept = s
	m.keep(ctx, s.kept())
	return s, nil
}

func (s *snapshot) kept() Kept {
	k := Kept{Base: s.base, At: s.at, Rows: make([]Built, len(s.list))}
	for i, e := range s.list {
		k.Rows[i] = Built{Entry: e.Entry, Provider: e.provider, In: e.in}
	}
	return k
}

// Current is the snapshot that holds every write noted before the call, as
// its keeper sees it. Its rows are shared: read them, never change them.
func (m *Model) Current(ctx context.Context) (Kept, error) {
	s, err := m.sync(ctx)
	if err != nil {
		return Kept{}, err
	}
	return s.kept(), nil
}

// Refresh brings the snapshot up to date and runs the keeper on it if it
// is new, as Run does on each note and minute.
func (m *Model) Refresh(ctx context.Context) error {
	_, err := m.refresh(ctx)
	return err
}

// checkAttempts bounds how often Check starts over when a row input is
// noted while it loads.
const checkAttempts = 3

// Served tells whether the model has built a snapshot yet.
func (m *Model) Served() bool { return m.cur.Load() != nil }

// Check derives every row from scratch at the snapshot's minute and lists
// each unit whose served row differs, with the fields that differ; none
// when the snapshot matches or none was served yet.
func (m *Model) Check(ctx context.Context) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.Served() {
		return nil
	}
	var out []string
	for range checkAttempts {
		s := m.cur.Load()
		if !m.fresh(s) {
			var err error
			if s, err = m.rebuild(ctx); err != nil {
				return []string{"rebuilding: " + err.Error()}
			}
		}
		base, err := m.base(ctx)
		if err != nil {
			return []string{"reading the base: " + err.Error()}
		}
		loaded, err := m.load(ctx)
		if err != nil {
			return []string{"loading: " + err.Error()}
		}
		fresh, err := derive(base, loaded, s.at)
		if err != nil {
			return []string{"deriving: " + err.Error()}
		}
		out = s.differences(fresh)
		if len(out) == 0 || m.changes == nil || m.changes.Since(s.applied, port.RowInputs).Empty() {
			return out
		}
	}
	return out
}

// differences lists the units whose row in s differs from fresh.
func (s *snapshot) differences(fresh []*entry) []string {
	var out []string
	seen := make(map[unit.Unit]bool, len(fresh))
	for _, f := range fresh {
		seen[f.Unit] = true
		e, ok := s.byUnit[f.Unit]
		switch {
		case !ok:
			out = append(out, f.Unit.String()+": missing from the read model")
		case !bytes.Equal(e.JSON, f.JSON):
			out = append(out, f.Unit.String()+": differs in "+strings.Join(fieldsDiffering(e.JSON, f.JSON), ", "))
		}
	}
	for _, e := range s.list {
		if !seen[e.Unit] {
			out = append(out, e.Unit.String()+": served but no longer listed")
		}
	}
	slices.Sort(out)
	return out
}

// fieldsDiffering are the top-level JSON fields whose values differ
// between a and b.
func fieldsDiffering(a, b []byte) []string {
	var x, y map[string]json.RawMessage
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return []string{"row"}
	}
	var out []string
	for k, v := range x {
		if w, ok := y[k]; !ok || !bytes.Equal(v, w) {
			out = append(out, k)
		}
	}
	for k := range y {
		if _, ok := x[k]; !ok {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out
}

// changeSince lists the units whose row changed or left after rev.
func (s *snapshot) changeSince(epoch string, rev uint64) api.Change {
	c := api.Change{Type: "changed", Epoch: epoch, Rev: s.rev}
	if rev < s.floor || rev > s.rev {
		c.All = true
		return c
	}
	for _, e := range s.list {
		if e.Rev > rev {
			c.Units = append(c.Units, e.Unit)
		}
	}
	for u, r := range s.gone {
		if r > rev {
			c.Units = append(c.Units, u)
		}
	}
	slices.SortFunc(c.Units, api.CompareUnits)
	return c
}

// FacetWaitsOn is the facet of whom a row waits on, by login or team.
const FacetWaitsOn = "waits-on"

// cloneRow copies everything of r a caller could change in place, so no
// caller reaches the snapshot's rows.
func cloneRow(r api.Row) api.Row {
	r.Views, r.Actions, r.Items, r.Payload = slices.Clone(r.Views), slices.Clone(r.Actions), slices.Clone(r.Items), slices.Clone(r.Payload)
	if r.Facets != nil {
		f := make(map[string][]string, len(r.Facets))
		for k, v := range r.Facets {
			f[k] = slices.Clone(v)
		}
		r.Facets = f
	}
	if r.Pending != nil {
		p := *r.Pending
		r.Pending = &p
	}
	if r.Question != nil {
		q := *r.Question
		q.Options = slices.Clone(q.Options)
		r.Question = &q
	}
	return r
}
