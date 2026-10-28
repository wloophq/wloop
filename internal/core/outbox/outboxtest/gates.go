package outboxtest

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/gates/gatestest"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
)

// DraftGate is the bundled gates.toml's draft gate.
var DraftGate = gates.Def{Name: "draft", Kind: gate.KindDraft, Label: "Send this draft?", Intents: []gates.Intent{
	{Name: "approve", Status: gate.Approved}, {Name: "discard", Status: gate.Rejected}}}

// Gates are an outbox's draft gates in memory, opened and resolved by a
// real gates service whose Publisher is the outbox Bind names.
type Gates struct {
	Service *gates.Service
	reader  gates.Reader
	outbox  atomic.Pointer[outbox.Service]
}

var _ outbox.Gates = (*Gates)(nil)

// NewGates are gates in memory, stamped by now.
func NewGates(now func() time.Time) *Gates {
	st := gatestest.NewStore(now)
	return GatesOn(st, st, now)
}

// GatesOn are the gates of st, read through r.
func GatesOn(st gates.Store, r gates.Reader, now func() time.Time) *Gates {
	g := &Gates{reader: r}
	g.Service = gates.New(gates.Deps{Store: st, Reader: r, Profile: profile{}, Clock: clock(now), Publisher: publisher{g}})
	return g
}

// Bind makes o hold its drafts on g, and g publish through o.
func (g *Gates) Bind(o *outbox.Service) *outbox.Service {
	g.outbox.Store(o)
	o.Gates = g
	return o
}

func (g *Gates) Open(ctx context.Context, gt gate.Gate, p gates.Policy) (gate.Gate, error) {
	return g.Service.Open(ctx, gt, p)
}

func (g *Gates) Supersede(ctx context.Context, id int64, by task.Actor) error {
	return g.Service.Supersede(ctx, id, by)
}

func (g *Gates) RecordSent(ctx context.Context, gt gate.Gate) (gate.Gate, error) {
	return g.Service.RecordSent(ctx, gt)
}

func (g *Gates) Where(ctx context.Context, q gates.Query) ([]gate.Gate, error) {
	return g.reader.Where(ctx, q)
}

// Decide resolves gate id with intent as a person.
func (g *Gates) Decide(ctx context.Context, id int64, intent string) error {
	return g.Service.Resolve(ctx, id, gates.Resolution{Intent: intent, By: task.ActorPerson})
}

// Approve approves draft id's gate as a person, as the daemon does.
func (g *Gates) Approve(ctx context.Context, id draft.ID) error {
	return g.decideDraft(ctx, id, "approve")
}

// Discard discards draft id's gate as a person.
func (g *Gates) Discard(ctx context.Context, id draft.ID) error {
	return g.decideDraft(ctx, id, "discard")
}

func (g *Gates) decideDraft(ctx context.Context, id draft.ID, intent string) error {
	o := g.outbox.Load()
	d, err := o.Store.Get(ctx, id)
	if err != nil {
		return err
	}
	gt, ok, err := o.GateOf(ctx, d)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("draft %d has no gate", id)
	}
	if intent == "approve" {
		if _, err := o.Prepare(ctx, id, d.Rev, nil); err != nil {
			return err
		}
	}
	return g.Decide(ctx, gt.ID, intent)
}

// Decided is how draft d's gate decided it as it opened: wait while it is
// open, delay while its window runs, send when a rule approved it, dry_run
// when one rejected it.
func (g *Gates) Decided(ctx context.Context, d draft.Draft) (draft.Approval, error) {
	gt, ok, err := g.outbox.Load().GateOf(ctx, d)
	if err != nil || !ok {
		return draft.Approval{}, fmt.Errorf("draft %d has no gate: %w", d.ID, err)
	}
	return Decided(gt), nil
}

// Decided is how draft gate g decided its draft as it opened.
func Decided(g gate.Gate) draft.Approval {
	switch {
	case g.Status == gate.Open && !g.Deadline.IsZero():
		return draft.Approval{Mode: draft.Delay, Window: g.Deadline.Sub(g.OpenedAt)}
	case g.Status == gate.Approved && g.By == task.ActorWorkflow && g.Rule != gates.RuleWindow:
		return draft.Approval{Mode: draft.Send}
	case g.Status == gate.Rejected && g.By == task.ActorWorkflow:
		return draft.Approval{Mode: draft.DryRun}
	}
	return draft.Approval{Mode: draft.Wait}
}

type profile struct{}

func (profile) Gate(name string) (gates.Def, bool) { return DraftGate, name == DraftGate.Name }

func (profile) FirstOf(k gate.Kind) (gates.Def, bool) { return DraftGate, k == gate.KindDraft }

type clock func() time.Time

func (c clock) Now() time.Time { return c() }

type publisher struct{ g *Gates }

func (p publisher) Publish(ctx context.Context, g gate.Gate) error {
	if o := p.g.outbox.Load(); o != nil {
		return o.Publish(ctx, g)
	}
	return nil
}
