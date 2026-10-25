// Package items is the items ledger: the only writer of a unit's items.
package items

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/unit"
)

type Applier interface {
	Apply(ctx context.Context, prov items.Provenance, ops []items.Op) (Result, error)
	Items(ctx context.Context, u unit.Unit) ([]items.Item, error)
}

type Reconciler interface {
	Reconcile(ctx context.Context, u unit.Unit, desired []items.Item, at unit.Anchor, absent items.Resolution) (Result, error)
}

// DisabledError is returned, with nothing applied, when an op or a proposal is on a unit of a
// disabled plugin; the daemon answers it with 409. A link to such a unit is stored.
type DisabledError struct{ Provider unit.ProviderID }

func (e *DisabledError) Error() string { return string(e.Provider) + " is disabled" }

// Ledger serializes Apply, Reconcile and Decide; a process keeps one Ledger per store.
type Ledger struct {
	Store   Store
	Links   port.LinkStore             // nil refuses link ops
	Enabled func(unit.ProviderID) bool // nil: every provider enabled
	Clock   port.Clock
	Log     func(string, ...any)

	mu sync.Mutex
	// confirmedNoops is the last derived value logged as a confirmed noop per item, so an
	// unchanged disagreement is logged once.
	confirmedNoops map[itemKey]string
}

type itemKey struct {
	unit unit.Unit
	id   string
}

// Apply applies ops in order, each applied, proposed, refused or a no-op, and commits the
// outcome of the batch at once.
func (l *Ledger) Apply(ctx context.Context, prov items.Provenance, ops []items.Op) (Result, error) {
	if !slices.Contains(provenances, prov) {
		return Result{}, fmt.Errorf("unknown provenance %q", prov)
	}
	var units []unit.Unit
	for _, o := range ops {
		if o.Unit.IsZero() {
			continue
		}
		if err := l.refuseDisabled(o.Unit); err != nil {
			return Result{}, err
		}
		units = append(units, o.Unit)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	b, err := l.begin(ctx, units)
	if err != nil {
		return Result{}, err
	}
	for i, o := range ops {
		b.apply(ctx, i, o, prov)
	}
	return b.commit(ctx)
}

// Reconcile makes the derived items of u equal desired: create or update each, and resolve an
// open derived item absent from desired with Resolution absent (superseded when "") and
// evidence {unit_event, at}. Desired items carry their final State and Resolution. A derived
// change to a confirmed item is logged noop with reason "confirmed" and never proposed.
// Reconcile on a disabled provider returns DisabledError and writes nothing.
func (l *Ledger) Reconcile(ctx context.Context, u unit.Unit, desired []items.Item, at unit.Anchor, absent items.Resolution) (Result, error) {
	if u.IsZero() {
		return Result{}, errors.New("reconcile: no unit")
	}
	if err := l.refuseDisabled(u); err != nil {
		return Result{}, err
	}
	if at.Unit.IsZero() {
		at.Unit = u
	}
	if err := at.Valid(); err != nil {
		return Result{}, fmt.Errorf("reconcile %s: anchor: %w", u, err)
	}
	at = normAnchor(at)
	if absent == "" {
		absent = items.Superseded
	}
	seen := make(map[string]bool, len(desired))
	for _, d := range desired {
		switch {
		case d.ID == "":
			return Result{}, fmt.Errorf("reconcile %s: a desired item without an id", u)
		case seen[d.ID]:
			return Result{}, fmt.Errorf("reconcile %s: item %s desired twice", u, d.ID)
		}
		seen[d.ID] = true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	b, err := l.begin(ctx, []unit.Unit{u})
	if err != nil {
		return Result{}, err
	}
	for _, d := range desired {
		b.reconcile(u, d, at)
	}
	for _, id := range b.ids(u) {
		cur := b.items[u][id]
		if seen[id] || cur.Provenance != items.Derived || cur.State != items.Open {
			continue
		}
		next := *cur
		ev := items.Evidence{Kind: items.EvidenceUnitEvent, Anchor: at}
		next.State, next.Resolution, next.Evidence = items.Resolved, absent, ev
		op := items.Op{Op: items.OpResolve, Unit: u, Item: id, Anchor: at, Resolution: absent, Evidence: &ev}
		b.put(op, items.Derived, next, false)
	}
	return b.commit(ctx)
}

func (l *Ledger) Items(ctx context.Context, u unit.Unit) ([]items.Item, error) {
	got, err := l.Store.Load(ctx, []unit.Unit{u})
	if err != nil {
		return nil, err
	}
	return got[u], nil
}

func (l *Ledger) Query(ctx context.Context, q Query) ([]items.Item, error) {
	return l.Store.Query(ctx, q)
}

func (l *Ledger) Proposals(ctx context.Context, state ProposalState) ([]Proposal, error) {
	return l.Store.Proposals(ctx, state)
}

// Decide settles a proposal; accept applies its Op as user.
func (l *Ledger) Decide(ctx context.Context, id int64, accept bool) (Result, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	open, err := l.Store.Proposals(ctx, ProposalOpen)
	if err != nil {
		return Result{}, err
	}
	i := slices.IndexFunc(open, func(p Proposal) bool { return p.ID == id })
	if i < 0 {
		return Result{}, fmt.Errorf("%w: %d", ErrNoProposal, id)
	}
	p := open[i]
	if err := l.refuseDisabled(p.Unit); err != nil {
		return Result{}, err
	}
	if !accept {
		if _, err := l.Store.SetProposal(ctx, id, ProposalRejected); err != nil {
			return Result{}, err
		}
		return emptyResult(), nil
	}
	b, err := l.begin(ctx, []unit.Unit{p.Unit})
	if err != nil {
		return Result{}, err
	}
	b.apply(ctx, 0, p.Op, items.User)
	if len(b.res.Refused) == 0 {
		p.State = ProposalAccepted
		b.settled = append(b.settled, p)
	}
	return b.commit(ctx)
}

type Query struct {
	Units     []unit.Unit
	State     items.State
	WaitingOn []string // person keys
	Providers []unit.ProviderID
}

type Result struct {
	Applied  []Applied  `json:"applied"`
	Proposed []Proposal `json:"proposed"`
	Refused  []Refused  `json:"refused"`
}

type Applied struct {
	Op      items.Op   `json:"op"`
	Item    items.Item `json:"item"`
	Changed bool       `json:"changed"`
}

type Refused struct {
	Index  int      `json:"index"` // position in the ops passed
	Op     items.Op `json:"op"`
	Reason string   `json:"reason"`
}

type ProposalState string // "open", "accepted", "rejected"

const (
	ProposalOpen     ProposalState = "open"
	ProposalAccepted ProposalState = "accepted"
	ProposalRejected ProposalState = "rejected"
)

// ErrNoProposal is Decide's error for an id that names no open proposal.
var ErrNoProposal = errors.New("no open proposal")

type Proposal struct {
	ID         int64            `json:"id"`
	Unit       unit.Unit        `json:"unit"`
	Item       string           `json:"item"`
	Op         items.Op         `json:"op"`
	Provenance items.Provenance `json:"provenance"`
	Reason     string           `json:"reason"`
	State      ProposalState    `json:"state"`
	At         time.Time        `json:"at"`
}

// Reader is the read half of Store.
type Reader interface {
	Load(ctx context.Context, units []unit.Unit) (map[unit.Unit][]items.Item, error)
	Query(ctx context.Context, q Query) ([]items.Item, error)
	Proposals(ctx context.Context, state ProposalState) ([]Proposal, error)
	Log(ctx context.Context, u unit.Unit, since time.Time) ([]LogEntry, error) // op outcomes, for history
}

type Store interface {
	Reader
	Commit(ctx context.Context, c Commit) error // items, op log, proposals in one transaction; notes TopicItems when anything but log rows changed
	SetProposal(ctx context.Context, id int64, state ProposalState) (Proposal, error)
}

type Commit struct {
	Items     []items.Item
	Log       []LogEntry
	Proposals []Proposal
}

// Outcomes of an op in the log.
const (
	OutcomeApplied  = "applied"
	OutcomeNoop     = "noop"
	OutcomeProposed = "proposed"
	OutcomeRefused  = "refused"
)

type LogEntry struct {
	Unit       unit.Unit
	Item       string
	Op         items.Op
	Provenance items.Provenance
	Outcome    string // applied, noop, proposed, refused
	Reason     string
	At         time.Time
}
