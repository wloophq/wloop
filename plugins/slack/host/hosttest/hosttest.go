// Package hosttest is an in-memory host broker for the Slack plugin's tests:
// it records what the plugin reports, proposes and writes, applies item ops
// with the host ledger's rules, and answers asks from a script.
package hosttest

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wloophq/wloop/pkg/plugin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/slack/host"
	"github.com/wloophq/wloop/plugins/slack/items"
	"github.com/wloophq/wloop/plugins/slack/link"
	"github.com/wloophq/wloop/plugins/slack/unit"
)

// Host is a fake host. Its zero value is ready; set the fields that script
// it before the plugin calls it.
type Host struct {
	// Now is the ledger's clock; nil is a fixed instant.
	Now func() time.Time
	// AskFunc answers Services.Ask; nil answers ErrUnsupported.
	AskFunc func(ctx context.Context, req src.AskRequest) (src.AskResult, error)
	// ProfileDirs are read in order by Services.Profile; the first that
	// holds a path answers it.
	ProfileDirs []string
	// ProfileFiles answer Services.Profile before ProfileDirs.
	ProfileFiles map[string][]byte
	// Resolve is the unit a link op's address lands in; nil keeps the
	// address as a unit with no provider.
	Resolve func(address string) (unit.Unit, bool)
	// Configured is what Services.Config answers.
	Configured src.Config
	// Disabled says a provider is disabled: an ApplyItems with an op on one
	// of its units fails whole, as the host's ledger does.
	Disabled func(p unit.ProviderID) bool

	mu        sync.Mutex
	seq       int
	items     map[unit.Unit]map[string]*stored
	links     []link.Link
	proposals []items.Op
	ops       [][]src.ItemOp
	reports   [][]src.Snapshot
	latest    map[unit.Unit]src.Snapshot
	changed   []unit.Unit
	tasks     []src.Task
	sessions  map[string]src.Session
	gates     []src.GateRequest
	sent      []Sent
	moves     []src.UnitMove
	drafts    map[unit.Unit]int
	briefs    []src.Brief
	events    []src.Event
	marks     map[unit.Unit]map[string]string
	asks      []src.AskRequest
	samples   []src.Sample
}

type stored struct {
	seq int
	it  items.Item
}

// Sent is a RecordSent call.
type Sent struct {
	Draft   src.Draft
	Session string
	Tool    string
}

// Broker is the fake as the plugin's broker.
func (h *Host) Broker() host.Broker { return host.Broker{Host: h.Src()} }

// Src is the fake as the SDK's Host.
func (h *Host) Src() src.Host {
	return src.Host{Reporter: h, Ledger: h, Reader: h, Gates: h, Recorder: h, Services: h, History: h}
}

var fixed = time.Date(2026, 1, 5, 12, 0, 0, 0, time.UTC)

func (h *Host) now() time.Time {
	if h.Now != nil {
		return h.Now().UTC()
	}
	return fixed
}

// Report keeps each unit's latest snapshot.
func (h *Host) Report(_ context.Context, snaps []src.Snapshot) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.reports = append(h.reports, slices.Clone(snaps))
	if h.latest == nil {
		h.latest = map[unit.Unit]src.Snapshot{}
	}
	for _, s := range snaps {
		u := unit.Of(s.Unit)
		if s.Gone {
			delete(h.latest, u)
			continue
		}
		h.latest[u] = s
	}
	return nil
}

func (h *Host) Changed(_ context.Context, units []plugin.Unit, _ []plugin.Topic) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, w := range units {
		h.changed = append(h.changed, unit.Of(w))
	}
	return nil
}

// Snapshot is u's latest reported snapshot.
func (h *Host) Snapshot(u unit.Unit) (src.Snapshot, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s, ok := h.latest[u]
	return s, ok
}

// Snapshots are the latest snapshot of every unit reported and not gone.
func (h *Host) Snapshots() []src.Snapshot {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]src.Snapshot, 0, len(h.latest))
	for _, s := range h.latest {
		out = append(out, s)
	}
	slices.SortFunc(out, func(a, b src.Snapshot) int { return unit.Compare(unit.Of(a.Unit), unit.Of(b.Unit)) })
	return out
}

// Reports are every Report call's snapshots, in order.
func (h *Host) Reports() [][]src.Snapshot {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.reports)
}

// ApplyItems applies the plugin's ops as extracted, as the host does.
func (h *Host) ApplyItems(_ context.Context, ops []src.ItemOp) ([]src.ItemResult, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ops = append(h.ops, slices.Clone(ops))
	for _, w := range ops {
		if p := unit.ProviderID(w.Unit.Provider); w.Unit.Provider != "" && h.Disabled != nil && h.Disabled(p) {
			return nil, fmt.Errorf("%s is disabled", p)
		}
	}
	out := make([]src.ItemResult, len(ops))
	for i, w := range ops {
		id, err := h.apply(items.Extracted, host.OpOf(w))
		out[i] = src.ItemResult{Item: id}
		if err != nil {
			out[i].Error = err.Error()
		}
	}
	return out, nil
}

// Apply applies ops as prov, as another writer of the ledger would (the
// user, a turn); it returns the refusals.
func (h *Host) Apply(prov items.Provenance, ops ...items.Op) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	var errs []error
	for _, o := range ops {
		if _, err := h.apply(prov, o); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Ops are every ApplyItems call's ops, in order.
func (h *Host) Ops() [][]src.ItemOp {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.ops)
}

// Proposals are the ops the ledger held for the user because they would
// change an item the user confirmed.
func (h *Host) Proposals() []items.Op {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.proposals)
}

// Links are the links the ledger keeps.
func (h *Host) Links() []link.Link {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.links)
}

// Items are u's items, oldest first.
func (h *Host) Items(u unit.Unit) []items.Item {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.itemsOf(u, false)
}

// Put stores it as is, as an item already in the ledger.
func (h *Host) Put(its ...items.Item) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, it := range its {
		h.put(it)
	}
}

func (h *Host) put(it items.Item) {
	if h.items == nil {
		h.items = map[unit.Unit]map[string]*stored{}
	}
	if h.items[it.Unit] == nil {
		h.items[it.Unit] = map[string]*stored{}
	}
	if cur, ok := h.items[it.Unit][it.ID]; ok {
		cur.it = it
		return
	}
	h.seq++
	h.items[it.Unit][it.ID] = &stored{seq: h.seq, it: it}
}

func (h *Host) itemsOf(u unit.Unit, openOnly bool) []items.Item {
	var ss []*stored
	for _, s := range h.items[u] {
		if !openOnly || s.it.State == items.Open {
			ss = append(ss, s)
		}
	}
	slices.SortFunc(ss, func(a, b *stored) int {
		return cmp.Or(a.it.CreatedAt.Compare(b.it.CreatedAt), cmp.Compare(a.seq, b.seq))
	})
	out := make([]items.Item, len(ss))
	for i, s := range ss {
		out[i] = s.it
	}
	return out
}

func (h *Host) QueryItems(_ context.Context, q src.ItemQuery) ([]src.Item, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var units []unit.Unit
	if len(q.Units) == 0 {
		for u := range h.items {
			units = append(units, u)
		}
	}
	for _, w := range q.Units {
		units = append(units, unit.Of(w))
	}
	slices.SortFunc(units, unit.Compare)
	units = slices.Compact(units)
	var out []src.Item
	for _, u := range units {
		for _, it := range h.itemsOf(u, q.OpenOnly) {
			out = append(out, host.WireItem(it))
		}
	}
	return out, nil
}

func (h *Host) nextID(u unit.Unit) string {
	n := 0
	for id := range h.items[u] {
		if v, err := strconv.Atoi(strings.TrimPrefix(id, "i")); err == nil && strings.HasPrefix(id, "i") && v > n {
			n = v
		}
	}
	return "i" + strconv.Itoa(n+1)
}

func normAnchor(a unit.Anchor, u unit.Unit) unit.Anchor {
	if a.Unit.IsZero() {
		a.Unit = u
	}
	if !a.At.IsZero() {
		a.At = a.At.UTC()
	}
	return a
}

// apply is the host ledger's rule for one op: a change to an item the user
// confirmed is a proposal, unless the user makes it or it is a resolve a
// unit's event proves.
func (h *Host) apply(prov items.Provenance, o items.Op) (string, error) {
	o.Anchor = normAnchor(o.Anchor, o.Unit)
	if o.Evidence != nil {
		ev := *o.Evidence
		ev.Anchor = normAnchor(ev.Anchor, o.Unit)
		o.Evidence = &ev
	}
	if err := o.ValidateShape(); err != nil {
		return o.Item, err
	}
	u := o.Unit
	if o.Op == items.OpCreate && o.Item == "" {
		o.Item = h.nextID(u)
	}
	var cur *items.Item
	if s, ok := h.items[u][o.Item]; ok && o.Item != "" {
		cur = &s.it
	}
	now := h.now()
	user := prov == items.User
	switch {
	case o.Op == items.OpCreate && cur == nil:
		it := items.Item{ID: o.Item, Unit: u, Origin: o.Anchor, Basis: items.NoBasis, State: items.Open, Confidence: items.High,
			Provenance: prov, CreatedAt: now, UpdatedAt: now}
		applyFields(&it, o.Fields)
		if user {
			it.Confirmed, it.UserAt = true, now
		}
		h.put(it)
		return o.Item, nil
	case o.Op == items.OpLink:
		return o.Item, h.link(o, prov, cur)
	case cur == nil:
		return o.Item, fmt.Errorf("item: unknown %q", o.Item)
	case o.Op == items.OpEvidence:
		if slices.ContainsFunc(cur.Trail, func(e items.Evidence) bool {
			return e.Kind == o.Evidence.Kind && e.Anchor.Unit == o.Evidence.Anchor.Unit && e.Anchor.Ref == o.Evidence.Anchor.Ref
		}) {
			return o.Item, nil
		}
		next := *cur
		next.Trail = append(slices.Clone(cur.Trail), *o.Evidence)
		if n := len(next.Trail) - items.MaxTrail; n > 0 {
			next.Trail = next.Trail[n:]
		}
		next.UpdatedAt = now
		h.put(next)
		return o.Item, nil
	}
	next := mutate(*cur, o)
	changed := content(next) != content(*cur)
	switch {
	case user && (changed || !cur.Confirmed):
		next.Confirmed, next.UserAt, next.UpdatedAt = true, now, now
		h.put(next)
	case !changed:
	case cur.Confirmed && !(prov == items.Derived && o.Op == items.OpResolve && o.Evidence != nil && o.Evidence.Kind == items.EvidenceUnitEvent):
		h.proposals = append(h.proposals, o)
	default:
		next.UpdatedAt = now
		h.put(next)
	}
	return o.Item, nil
}

func (h *Host) link(o items.Op, prov items.Provenance, cur *items.Item) error {
	if o.Item != "" && cur == nil {
		return fmt.Errorf("item: unknown %q", o.Item)
	}
	to := *o.To
	if to.Provider == "" && h.Resolve != nil {
		got, ok := h.Resolve(to.Key)
		if !ok {
			return fmt.Errorf("the link target %q names no unit", to.Key)
		}
		to = got
	}
	if slices.ContainsFunc(h.links, func(l link.Link) bool {
		return l.From == o.Unit && l.To == to && l.Kind == o.LinkKind && l.Item == o.Item
	}) {
		return nil
	}
	origin := link.Extraction
	if prov == items.User {
		origin = link.User
	}
	h.links = append(h.links, link.Link{From: o.Unit, To: to, Kind: o.LinkKind, Item: o.Item, Evidence: o.Anchor, Origin: origin, At: h.now()})
	return nil
}

func mutate(it items.Item, o items.Op) items.Item {
	switch o.Op {
	case items.OpCreate, items.OpUpdate:
		applyFields(&it, o.Fields)
	case items.OpAssign:
		basis := it.Basis
		if o.Basis != "" {
			basis = o.Basis
		}
		if samePeople(it.Assignees, o.Assignees) && basis == it.Basis {
			return it
		}
		it.Assignees, it.Basis, it.AssignedAt = slices.Clone(o.Assignees), basis, o.Anchor
	case items.OpResolve:
		if it.State == items.Resolved && it.Resolution == o.Resolution {
			return it
		}
		ev := items.Evidence{Kind: items.EvidenceMessage, Anchor: o.Anchor}
		if o.Evidence != nil {
			ev = *o.Evidence
		}
		it.State, it.Resolution, it.Evidence = items.Resolved, o.Resolution, ev
	case items.OpReopen:
		it.State, it.Resolution, it.Evidence = items.Open, "", items.Evidence{}
	}
	return it
}

func applyFields(it *items.Item, f *items.Fields) {
	if f == nil {
		return
	}
	set(&it.Kind, f.Kind)
	set(&it.Summary, f.Summary)
	set(&it.Context, f.Context)
	set(&it.Deadline, f.Deadline)
	set(&it.Ambiguity, f.Ambiguity)
	set(&it.Options, f.Options)
	set(&it.URLs, f.URLs)
	set(&it.Contexts, f.Contexts)
	set(&it.Initial, f.Initial)
	set(&it.Requester, f.Requester)
	set(&it.WaitingOn, f.WaitingOn)
	set(&it.Confidence, f.Confidence)
}

func set[T any](dst *T, v *T) {
	if v != nil {
		*dst = *v
	}
}

func samePeople(a, b []items.Person) bool {
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return string(ja) == string(jb)
}

func content(it items.Item) string {
	it.CreatedAt, it.UpdatedAt, it.UserAt = time.Time{}, time.Time{}, time.Time{}
	it.Confirmed, it.Provenance = false, ""
	raw, _ := json.Marshal(it)
	return string(raw)
}

// SetTasks sets the tasks Reader.Tasks answers from.
func (h *Host) SetTasks(ts ...src.Task) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.tasks = slices.Clone(ts)
}

// SetSession sets the session Reader.Session answers for handle.
func (h *Host) SetSession(handle string, s src.Session) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.sessions == nil {
		h.sessions = map[string]src.Session{}
	}
	h.sessions[handle] = s
}

func (h *Host) Tasks(_ context.Context, units []plugin.Unit) ([]src.Task, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []src.Task
	for _, t := range h.tasks {
		if slices.ContainsFunc(t.Units, func(u plugin.Unit) bool { return slices.Contains(units, u) }) {
			out = append(out, t)
		}
	}
	return out, nil
}

func (h *Host) Books(context.Context, []plugin.Unit) ([]src.Book, error) { return nil, nil }

func (h *Host) RoundState(context.Context, plugin.Unit) (src.RoundState, error) {
	return src.RoundState{}, src.ErrUnsupported
}

func (h *Host) Workspaces(context.Context, string) (src.Listing, error) { return src.Listing{}, nil }

// Session answers a session SetSession set, else one with no unit.
func (h *Host) Session(_ context.Context, handle string) (src.Session, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.sessions[handle], nil
}

func (h *Host) OpenGate(_ context.Context, req src.GateRequest) (src.GateRef, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.gates = append(h.gates, req)
	return src.GateRef{Gate: "draft", Draft: int64(len(h.gates) + len(h.sent))}, nil
}

func (h *Host) RecordSent(_ context.Context, d src.Draft, session, tool string) (src.GateRef, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sent = append(h.sent, Sent{Draft: d, Session: session, Tool: tool})
	return src.GateRef{Gate: "draft", Draft: int64(len(h.gates) + len(h.sent))}, nil
}

// SetDrafts says u has n drafts, which RehomeDrafts moves and counts.
func (h *Host) SetDrafts(u unit.Unit, n int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.drafts == nil {
		h.drafts = map[unit.Unit]int{}
	}
	h.drafts[u] = n
}

// Drafts are how many drafts u has.
func (h *Host) Drafts(u unit.Unit) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.drafts[u]
}

func (h *Host) RehomeDrafts(_ context.Context, moves []src.UnitMove) (int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.moves = append(h.moves, moves...)
	if h.drafts == nil {
		h.drafts = map[unit.Unit]int{}
	}
	n := 0
	for _, m := range moves {
		from, to := unit.Of(m.From), unit.Of(m.To)
		if from == to {
			continue
		}
		n += h.drafts[from]
		h.drafts[to] += h.drafts[from]
		delete(h.drafts, from)
	}
	return n, nil
}

func (h *Host) SetOverride(context.Context, plugin.Unit, map[string]string, map[string]string) (bool, error) {
	return false, src.ErrUnsupported
}

// Gates are the OpenGate calls.
func (h *Host) Gates() []src.GateRequest {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.gates)
}

// SentDrafts are the RecordSent calls.
func (h *Host) SentDrafts() []Sent {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.sent)
}

// Moves are the RehomeDrafts moves.
func (h *Host) Moves() []src.UnitMove {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.moves)
}

func (h *Host) RecordEvents(_ context.Context, events []src.Event) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.events = append(h.events, events...)
	return nil
}

func (h *Host) SetMarks(_ context.Context, u plugin.Unit, marks map[string]string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.marks == nil {
		h.marks = map[unit.Unit]map[string]string{}
	}
	m := h.marks[unit.Of(u)]
	if m == nil {
		m = map[string]string{}
		h.marks[unit.Of(u)] = m
	}
	for k, v := range marks {
		if v == "" {
			delete(m, k)
			continue
		}
		m[k] = v
	}
	return nil
}

func (h *Host) PutBrief(_ context.Context, b src.Brief) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.briefs = append(h.briefs, b)
	return nil
}

// Briefs are the PutBrief calls.
func (h *Host) Briefs() []src.Brief {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.briefs)
}

// Events are the recorded events.
func (h *Host) Events(_ context.Context, q src.EventQuery) ([]src.Event, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []src.Event
	for _, e := range h.events {
		if len(q.Units) > 0 && !slices.Contains(q.Units, e.Unit) {
			continue
		}
		if len(q.Kinds) > 0 && !slices.Contains(q.Kinds, e.Kind) {
			continue
		}
		if e.At.Before(q.Since) {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

func (h *Host) Ask(ctx context.Context, req src.AskRequest) (src.AskResult, error) {
	h.mu.Lock()
	h.asks = append(h.asks, req)
	ask := h.AskFunc
	h.mu.Unlock()
	if ask == nil {
		return src.AskResult{}, src.ErrUnsupported
	}
	return ask(ctx, req)
}

// Asks are the Ask calls.
func (h *Host) Asks() []src.AskRequest {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.asks)
}

func (h *Host) Meter(_ context.Context, s src.Sample) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.samples = append(h.samples, s)
	return nil
}

// Samples are the metered samples.
func (h *Host) Samples() []src.Sample {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.samples)
}

func (h *Host) Config(context.Context) (src.Config, error) { return h.Configured, nil }

func (h *Host) Profile(_ context.Context, path string) ([]byte, error) {
	if b, ok := h.ProfileFiles[path]; ok {
		return slices.Clone(b), nil
	}
	for _, d := range h.ProfileDirs {
		b, err := os.ReadFile(filepath.Join(d, filepath.FromSlash(path)))
		if err == nil {
			return b, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	return nil, fmt.Errorf("%w: profile file %s", src.ErrNotFound, path)
}
