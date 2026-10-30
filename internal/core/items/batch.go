package items

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/link"
	"github.com/wloophq/wloop/internal/domain/unit"
)

var provenances = []items.Provenance{items.Extracted, items.Derived, items.User, items.Turn, items.Effects}

func emptyResult() Result {
	return Result{Applied: []Applied{}, Proposed: []Proposal{}, Refused: []Refused{}}
}

func (l *Ledger) refuseDisabled(u unit.Unit) error {
	if l.Enabled == nil || l.Enabled(u.Provider) {
		return nil
	}
	return &DisabledError{Provider: u.Provider}
}

func (l *Ledger) now() time.Time {
	if l.Clock == nil {
		return time.Now().UTC()
	}
	return l.Clock.Now().UTC()
}

// batch is one call's working copy of the units it touches, and what it will commit.
type batch struct {
	l       *Ledger
	now     time.Time
	items   map[unit.Unit]map[string]*items.Item
	dirty   map[unit.Unit]map[string]bool
	open    []Proposal
	links   map[unit.Unit][]link.Link
	log     []LogEntry
	created []Proposal
	decided []int64
	noops   map[itemKey]string
	res     Result
	err     error
}

func (l *Ledger) begin(ctx context.Context, units []unit.Unit) (*batch, error) {
	b := &batch{l: l, now: l.now(), items: map[unit.Unit]map[string]*items.Item{},
		dirty: map[unit.Unit]map[string]bool{}, links: map[unit.Unit][]link.Link{}, res: emptyResult()}
	slices.SortFunc(units, func(a, b unit.Unit) int { return strings.Compare(a.String(), b.String()) })
	units = slices.Compact(units)
	if len(units) == 0 {
		return b, nil
	}
	got, err := l.Store.Load(ctx, units)
	if err != nil {
		return nil, err
	}
	for _, u := range units {
		m := make(map[string]*items.Item, len(got[u]))
		for _, it := range got[u] {
			m[it.ID] = &it
		}
		b.items[u] = m
	}
	return b, nil
}

func (b *batch) ids(u unit.Unit) []string {
	ids := make([]string, 0, len(b.items[u]))
	for id := range b.items[u] {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

// nextID is the next "i<N>" of u, one past the largest stored or created in this batch.
func (b *batch) nextID(u unit.Unit) string {
	n := 0
	for id := range b.items[u] {
		if v, err := strconv.Atoi(strings.TrimPrefix(id, "i")); err == nil && strings.HasPrefix(id, "i") && v > n {
			n = v
		}
	}
	return "i" + strconv.Itoa(n+1)
}

func (b *batch) refuse(i int, o items.Op, prov items.Provenance, reason string) {
	b.res.Refused = append(b.res.Refused, Refused{Index: i, Op: o, Reason: reason})
	b.log = append(b.log, LogEntry{Unit: o.Unit, Item: o.Item, Op: o, Provenance: prov, Outcome: OutcomeRefused, Reason: reason, At: b.now})
}

func (b *batch) noop(o items.Op, prov items.Provenance, cur items.Item, reason string) {
	b.res.Applied = append(b.res.Applied, Applied{Op: o, Item: cur, Changed: false})
	b.log = append(b.log, LogEntry{Unit: o.Unit, Item: o.Item, Op: o, Provenance: prov, Outcome: OutcomeNoop, Reason: reason, At: b.now})
}

// put stores next as the item o leaves; user marks the user's confirmation.
func (b *batch) put(o items.Op, prov items.Provenance, next items.Item, user bool) {
	next.UpdatedAt = b.now
	if user {
		next.Confirmed, next.UserAt = true, b.now
	}
	u := next.Unit
	if b.items[u] == nil {
		b.items[u] = map[string]*items.Item{}
	}
	b.items[u][next.ID] = &next
	if b.dirty[u] == nil {
		b.dirty[u] = map[string]bool{}
	}
	b.dirty[u][next.ID] = true
	b.res.Applied = append(b.res.Applied, Applied{Op: o, Item: next, Changed: true})
	b.log = append(b.log, LogEntry{Unit: u, Item: next.ID, Op: o, Provenance: prov, Outcome: OutcomeApplied, Reason: o.Reason, At: b.now})
}

func normOp(o items.Op) items.Op {
	if o.Anchor.Unit.IsZero() {
		o.Anchor.Unit = o.Unit
	}
	o.Anchor = normAnchor(o.Anchor)
	if o.Evidence != nil {
		ev := *o.Evidence
		if ev.Anchor.Unit.IsZero() {
			ev.Anchor.Unit = o.Unit
		}
		ev.Anchor = normAnchor(ev.Anchor)
		o.Evidence = &ev
	}
	return o
}

func normAnchor(a unit.Anchor) unit.Anchor {
	if !a.At.IsZero() {
		a.At = a.At.UTC()
	}
	return a
}

func (b *batch) apply(ctx context.Context, i int, o items.Op, prov items.Provenance) {
	o = normOp(o)
	if err := o.ValidateShape(); err != nil {
		b.refuse(i, o, prov, err.Error())
		return
	}
	u := o.Unit
	if o.Op == items.OpCreate && o.Item == "" {
		o.Item = b.nextID(u)
	}
	var cur *items.Item
	if o.Item != "" {
		cur = b.items[u][o.Item]
	}
	switch {
	case o.Op == items.OpCreate && cur == nil:
		b.put(o, prov, b.newItem(o, prov), prov == items.User)
		return
	case o.Op == items.OpLink:
		b.link(ctx, i, o, prov, cur)
		return
	case cur == nil:
		b.refuse(i, o, prov, fmt.Sprintf("item: unknown %q", o.Item))
		return
	case o.Op == items.OpEvidence:
		b.evidence(o, prov, *cur)
		return
	}
	next := mutate(*cur, o)
	changed := content(next) != content(*cur)
	switch {
	case prov == items.User && (changed || !cur.Confirmed):
		b.put(o, prov, next, true)
	case !changed:
		b.noop(o, prov, *cur, "")
	case cur.Confirmed && !(prov == items.Derived && o.Op == items.OpResolve && o.Evidence != nil && o.Evidence.Kind == items.EvidenceUnitEvent):
		b.propose(ctx, o, prov, *cur)
	default:
		b.put(o, prov, next, false)
	}
}

// newItem is the item a create of a new id makes.
func (b *batch) newItem(o items.Op, prov items.Provenance) items.Item {
	it := items.Item{ID: o.Item, Unit: o.Unit, Origin: o.Anchor, Basis: items.NoBasis, State: items.Open,
		Confidence: items.High, Provenance: prov, CreatedAt: b.now}
	applyFields(&it, o.Fields)
	return it
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

// content is what an op can change of an item, as comparable text.
func content(it items.Item) string {
	it.CreatedAt, it.UpdatedAt, it.UserAt = time.Time{}, time.Time{}, time.Time{}
	it.Confirmed, it.Provenance = false, ""
	raw, _ := json.Marshal(it)
	return string(raw)
}

func (b *batch) evidence(o items.Op, prov items.Provenance, cur items.Item) {
	if slices.ContainsFunc(cur.Trail, func(e items.Evidence) bool {
		return e.Kind == o.Evidence.Kind && e.Anchor.Unit == o.Evidence.Anchor.Unit && e.Anchor.Ref == o.Evidence.Anchor.Ref
	}) {
		b.noop(o, prov, cur, "")
		return
	}
	next := cur
	next.Trail = append(slices.Clone(cur.Trail), *o.Evidence)
	if n := len(next.Trail) - items.MaxTrail; n > 0 {
		next.Trail = next.Trail[n:]
	}
	b.put(o, prov, next, false)
}

func (b *batch) link(ctx context.Context, i int, o items.Op, prov items.Provenance, cur *items.Item) {
	if b.l.Links == nil {
		b.refuse(i, o, prov, "link: no link store")
		return
	}
	if o.Item != "" && cur == nil {
		b.refuse(i, o, prov, fmt.Sprintf("item: unknown %q", o.Item))
		return
	}
	var it items.Item
	if cur != nil {
		it = *cur
	}
	have, ok := b.links[o.Unit]
	if !ok {
		var err error
		if have, err = b.l.Links.From(ctx, o.Unit); err != nil {
			b.refuse(i, o, prov, "link: "+err.Error())
			return
		}
		b.links[o.Unit] = have
	}
	if slices.ContainsFunc(have, func(l link.Link) bool { return l.To == *o.To && l.Kind == o.LinkKind && l.Item == o.Item }) {
		b.noop(o, prov, it, "")
		return
	}
	origin := link.Extraction
	if prov == items.User {
		origin = link.User
	}
	l := link.Link{From: o.Unit, To: *o.To, Kind: o.LinkKind, Item: o.Item, Evidence: o.Anchor, Origin: origin, At: b.linkAt(o, prov, cur)}
	if err := b.l.Links.Put(ctx, l); err != nil {
		b.refuse(i, o, prov, "link: "+err.Error())
		return
	}
	b.links[o.Unit] = append(have, l)
	b.res.Applied = append(b.res.Applied, Applied{Op: o, Item: it, Changed: true})
	b.log = append(b.log, LogEntry{Unit: o.Unit, Item: o.Item, Op: o, Provenance: prov, Outcome: OutcomeApplied, Reason: o.Reason, At: b.now})
	b.settle(ctx, i, l, cur)
}

// settle applies, as derived, the op that settles cur through l from what l's target already shows.
func (b *batch) settle(ctx context.Context, i int, l link.Link, cur *items.Item) {
	if b.l.Settle == nil || cur == nil || cur.State != items.Open {
		return
	}
	op, ok, err := b.l.Settle(ctx, l, *cur)
	if err != nil {
		if b.l.Log != nil {
			b.l.Log("link %s to %s: settling %s: %v", l.From, l.To, l.Item, err)
		}
		return
	}
	if ok {
		b.apply(ctx, i, op, items.Derived)
	}
}

// ExtractedLinkLookback is how far before its item's creation an extracted link may start: the
// extractor's window holds older messages, as far back as the Slack backfill.
const ExtractedLinkLookback = 7 * 24 * time.Hour

// linkAt is when a link starts counting its target's events. Only an extracted link, whose
// evidence the extractor checked against its window, keeps an earlier time.
func (b *batch) linkAt(o items.Op, prov items.Provenance, cur *items.Item) time.Time {
	at := o.Anchor.At
	if prov != items.Extracted || at.IsZero() || !at.Before(b.now) {
		return b.now
	}
	created := b.now
	if cur != nil && !cur.CreatedAt.IsZero() {
		created = cur.CreatedAt
	}
	return maxTime(at, created.Add(-ExtractedLinkLookback))
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// DedupeKey is the identity of a proposal among the open ones: its op without anchor, reason,
// basis and evidence.
func DedupeKey(o items.Op) string {
	raw, _ := json.Marshal(struct {
		Unit       unit.Unit        `json:"unit"`
		Item       string           `json:"item"`
		Op         items.OpKind     `json:"op"`
		Fields     *items.Fields    `json:"fields,omitempty"`
		Assignees  []items.Person   `json:"assignees,omitempty"`
		Resolution items.Resolution `json:"resolution,omitempty"`
		To         *unit.Unit       `json:"to,omitempty"`
		LinkKind   link.Kind        `json:"link_kind,omitempty"`
	}{o.Unit, o.Item, o.Op, o.Fields, o.Assignees, o.Resolution, o.To, o.LinkKind})
	return string(raw)
}

func (b *batch) propose(ctx context.Context, o items.Op, prov items.Provenance, cur items.Item) {
	if b.open == nil {
		open, err := b.l.Store.Proposals(ctx, ProposalOpen)
		if err != nil {
			b.err = err
			return
		}
		b.open = append([]Proposal{}, open...)
	}
	key := DedupeKey(o)
	for _, p := range append(b.open, b.created...) {
		if DedupeKey(p.Op) == key {
			b.noop(o, prov, cur, fmt.Sprintf("already proposed (%d)", p.ID))
			return
		}
	}
	p := Proposal{Unit: o.Unit, Item: o.Item, Op: o, Provenance: prov, Reason: o.Reason, State: ProposalOpen, At: b.now}
	b.created = append(b.created, p)
	b.log = append(b.log, LogEntry{Unit: o.Unit, Item: o.Item, Op: o, Provenance: prov, Outcome: OutcomeProposed, Reason: "confirmed", At: b.now})
}

// reconcile makes the derived item d.ID of u equal d.
func (b *batch) reconcile(u unit.Unit, d items.Item, at unit.Anchor) {
	cur := b.items[u][d.ID]
	d = desiredItem(u, d, at, cur)
	if cur == nil {
		d.CreatedAt = b.now
		b.put(items.Op{Op: items.OpCreate, Unit: u, Item: d.ID, Anchor: at, Fields: fieldsOf(d)}, items.Derived, d, false)
		return
	}
	if cur.Provenance != items.Derived {
		return
	}
	next := d
	next.Trail, next.Confirmed, next.UserAt, next.CreatedAt = cur.Trail, cur.Confirmed, cur.UserAt, cur.CreatedAt
	if content(next) == content(*cur) {
		return
	}
	op := items.Op{Op: items.OpUpdate, Unit: u, Item: d.ID, Anchor: at, Fields: fieldsOf(d)}
	switch {
	case cur.State == items.Open && d.State == items.Resolved:
		op = items.Op{Op: items.OpResolve, Unit: u, Item: d.ID, Anchor: at, Resolution: d.Resolution, Evidence: &d.Evidence}
	case cur.State == items.Resolved && d.State == items.Open:
		op = items.Op{Op: items.OpReopen, Unit: u, Item: d.ID, Anchor: at}
	}
	if !cur.Confirmed {
		b.put(op, items.Derived, next, false)
		return
	}
	if op.Op == items.OpResolve {
		next = *cur
		next.State, next.Resolution, next.Evidence = items.Resolved, d.Resolution, d.Evidence
		b.put(op, items.Derived, next, false)
		return
	}
	key, want := itemKey{u, d.ID}, DedupeKey(op)
	if b.l.confirmedNoops[key] == want {
		b.res.Applied = append(b.res.Applied, Applied{Op: op, Item: *cur, Changed: false})
		return
	}
	if b.noops == nil {
		b.noops = map[itemKey]string{}
	}
	b.noops[key] = want
	b.noop(op, items.Derived, *cur, "confirmed")
}

// desiredItem fills d's defaults; an anchor d leaves empty keeps cur's, so a new row revision
// alone changes nothing.
func desiredItem(u unit.Unit, d items.Item, at unit.Anchor, cur *items.Item) items.Item {
	d.Unit, d.Provenance = u, items.Derived
	d.Trail, d.Confirmed, d.UserAt = nil, false, time.Time{}
	if d.State == "" {
		d.State = items.Open
	}
	if d.Origin.Ref == "" {
		d.Origin = at
		if cur != nil {
			d.Origin = cur.Origin
		}
	}
	if d.Basis == "" {
		d.Basis = items.NoBasis
	}
	if d.Confidence == "" {
		d.Confidence = items.High
	}
	if d.State == items.Resolved && d.Evidence == (items.Evidence{}) {
		d.Evidence = items.Evidence{Kind: items.EvidenceUnitEvent, Anchor: at}
		if cur != nil && cur.State == items.Resolved {
			d.Evidence = cur.Evidence
		}
	}
	if d.State == items.Open {
		d.Resolution, d.Evidence = "", items.Evidence{}
	}
	d.Origin = normAnchor(d.Origin)
	d.AssignedAt = normAnchor(d.AssignedAt)
	d.Evidence.Anchor = normAnchor(d.Evidence.Anchor)
	return d
}

func fieldsOf(it items.Item) *items.Fields {
	return &items.Fields{Kind: &it.Kind, Summary: &it.Summary, Context: &it.Context, Deadline: &it.Deadline,
		Ambiguity: &it.Ambiguity, Options: &it.Options, URLs: &it.URLs, Contexts: &it.Contexts, Initial: &it.Initial,
		Requester: &it.Requester, WaitingOn: &it.WaitingOn, Confidence: &it.Confidence}
}

func (b *batch) commit(ctx context.Context) (Result, error) {
	if b.err != nil {
		return Result{}, b.err
	}
	var c Commit
	for _, u := range sortedUnits(b.dirty) {
		for _, id := range sortedKeys(b.dirty[u]) {
			c.Items = append(c.Items, *b.items[u][id])
		}
	}
	c.Log = b.log
	c.Proposals = slices.Clone(b.created)
	c.Decided, c.DecidedAt = b.decided, b.now
	if len(c.Items)+len(c.Log)+len(c.Proposals)+len(c.Decided) == 0 {
		return b.res, nil
	}
	if err := b.l.Store.Commit(ctx, c); err != nil {
		return Result{}, err
	}
	b.rememberNoops()
	if len(b.created) == 0 {
		return b.res, nil
	}
	open, err := b.l.Store.Proposals(ctx, ProposalOpen)
	if err != nil {
		return Result{}, err
	}
	for _, p := range b.created {
		key := DedupeKey(p.Op)
		if i := slices.IndexFunc(open, func(q Proposal) bool { return DedupeKey(q.Op) == key }); i >= 0 {
			p.ID = open[i].ID
		}
		b.res.Proposed = append(b.res.Proposed, p)
	}
	return b.res, nil
}

func (b *batch) rememberNoops() {
	if b.l.confirmedNoops == nil {
		b.l.confirmedNoops = map[itemKey]string{}
	}
	for u, ids := range b.dirty {
		for id := range ids {
			delete(b.l.confirmedNoops, itemKey{u, id})
		}
	}
	for k, v := range b.noops {
		b.l.confirmedNoops[k] = v
	}
}

func sortedUnits[V any](m map[unit.Unit]V) []unit.Unit {
	out := make([]unit.Unit, 0, len(m))
	for u := range m {
		out = append(out, u)
	}
	slices.SortFunc(out, func(a, b unit.Unit) int { return strings.Compare(a.String(), b.String()) })
	return out
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
