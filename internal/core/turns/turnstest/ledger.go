package turnstest

import (
	"context"
	"sync"
	"time"

	coreitems "github.com/wloophq/wloop/internal/core/items"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// Ledger is a turns.Ledger that applies every op but those it is told to
// refuse or propose, and keeps each call.
type Ledger struct {
	mu sync.Mutex
	// Refuse and Propose are, by op index, the ops answered refused (with
	// that reason) or proposed.
	Refuse  map[int]string
	Propose map[int]bool
	Err     error
	Calls   []LedgerCall
	Open    map[unit.Unit][]items.Item
	// ItemsErr fails Items.
	ItemsErr error
	Logged   []coreitems.LogEntry
}

type LedgerCall struct {
	Provenance items.Provenance
	Ops        []items.Op
}

func (l *Ledger) Apply(_ context.Context, prov items.Provenance, ops []items.Op) (coreitems.Result, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.Calls = append(l.Calls, LedgerCall{Provenance: prov, Ops: ops})
	if l.Err != nil {
		return coreitems.Result{}, l.Err
	}
	res := coreitems.Result{Applied: []coreitems.Applied{}, Proposed: []coreitems.Proposal{}, Refused: []coreitems.Refused{}}
	for i, o := range ops {
		switch {
		case l.Refuse[i] != "":
			res.Refused = append(res.Refused, coreitems.Refused{Index: i, Op: o, Reason: l.Refuse[i]})
		case l.Propose[i]:
			res.Proposed = append(res.Proposed, coreitems.Proposal{Unit: o.Unit, Item: o.Item, Op: o, Provenance: prov, State: coreitems.ProposalOpen})
		default:
			res.Applied = append(res.Applied, coreitems.Applied{Op: o, Item: items.Item{ID: o.Item, Unit: o.Unit}, Changed: true})
		}
	}
	return res, nil
}

func (l *Ledger) Items(_ context.Context, u unit.Unit) ([]items.Item, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.ItemsErr != nil {
		return nil, l.ItemsErr
	}
	return l.Open[u], nil
}

func (l *Ledger) Log(_ context.Context, u unit.Unit, since time.Time) ([]coreitems.LogEntry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []coreitems.LogEntry
	for _, e := range l.Logged {
		if e.Unit == u && !e.At.Before(since) {
			out = append(out, e)
		}
	}
	return out, nil
}

// Applied is every op the ledger was given, in order.
func (l *Ledger) Applied() []items.Op {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []items.Op
	for _, c := range l.Calls {
		out = append(out, c.Ops...)
	}
	return out
}
