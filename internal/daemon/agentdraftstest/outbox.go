// Package agentdraftstest is a fake of the outbox's agent API, which keeps drafts in memory and answers
// by their state: a draft out of the caller's units or not of turn or session origin is out of scope, one
// at another rev a conflict.
package agentdraftstest

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/domain/draft"
)

// Outbox is the fake. Its zero value keeps no draft.
type Outbox struct {
	mu      sync.Mutex
	drafts  map[draft.ID]draft.Draft
	revs    map[draft.ID][]draft.Revision
	callers []outbox.Caller
	At      time.Time
}

// Put stores d and its first revision, by its proposer.
func (o *Outbox) Put(d draft.Draft) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.drafts == nil {
		o.drafts, o.revs = map[draft.ID]draft.Draft{}, map[draft.ID][]draft.Revision{}
	}
	if d.Rev == 0 {
		d.Rev = 1
	}
	o.drafts[d.ID] = d
	o.revs[d.ID] = append(o.revs[d.ID], draft.Revision{Rev: d.Rev, Text: d.Text, Payload: d.Payload, Basis: d.Basis,
		By: draft.Actor{Kind: draft.ActorSession, Session: d.Origin.Session}, At: o.At})
}

// Edit revises draft id as a person does, outside the agent API.
func (o *Outbox) Edit(id draft.ID, text string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	d := o.drafts[id]
	d.Text, d.Rev = text, d.Rev+1
	o.drafts[id] = d
	o.revs[id] = append(o.revs[id], draft.Revision{Rev: d.Rev, Text: text, Payload: d.Payload, Basis: d.Basis, By: draft.Actor{Kind: draft.ActorPerson}, At: o.At})
}

// Get is draft id as stored.
func (o *Outbox) Get(id draft.ID) draft.Draft {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.drafts[id]
}

// Callers are the callers of every call, in order.
func (o *Outbox) Callers() []outbox.Caller {
	o.mu.Lock()
	defer o.mu.Unlock()
	return slices.Clone(o.callers)
}

func (o *Outbox) reach(c outbox.Caller, id draft.ID) (draft.Draft, error) {
	o.callers = append(o.callers, c)
	d, ok := o.drafts[id]
	switch {
	case !ok:
		return draft.Draft{}, outbox.ErrNotFound
	case d.Origin.Kind != draft.OriginTurn && d.Origin.Kind != draft.OriginSession, !slices.Contains(c.Units, d.Unit):
		return draft.Draft{}, outbox.ErrOutOfScope
	}
	return d, nil
}

func (o *Outbox) AgentRead(_ context.Context, c outbox.Caller, id draft.ID) (draft.Draft, []draft.Revision, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	d, err := o.reach(c, id)
	if err != nil {
		return draft.Draft{}, nil, err
	}
	return d, slices.Clone(o.revs[id]), nil
}

func (o *Outbox) AgentAmend(_ context.Context, c outbox.Caller, id draft.ID, a outbox.Amendment) (draft.Draft, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	d, err := o.reach(c, id)
	if err != nil {
		return draft.Draft{}, err
	}
	if a.Rev != d.Rev {
		return draft.Draft{}, outbox.ErrRevConflict
	}
	if a.Text != nil {
		d.Text = *a.Text
	}
	if a.Payload != nil {
		d.Payload = json.RawMessage(*a.Payload)
	}
	if a.Basis != nil {
		d.Basis = *a.Basis
	}
	if d.State == draft.Failed {
		d.State = draft.Held
	}
	d.Rev++
	o.drafts[id] = d
	o.revs[id] = append(o.revs[id], draft.Revision{Rev: d.Rev, Text: d.Text, Payload: d.Payload, Basis: d.Basis, By: a.By, At: o.At})
	return d, nil
}

func (o *Outbox) AgentWithdraw(_ context.Context, c outbox.Caller, id draft.ID, rev int64, reason string) (draft.Draft, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	d, err := o.reach(c, id)
	if err != nil {
		return draft.Draft{}, err
	}
	if rev != d.Rev {
		return draft.Draft{}, outbox.ErrRevConflict
	}
	d.State, d.Reason = draft.Dropped, reason
	o.drafts[id] = d
	return d, nil
}
