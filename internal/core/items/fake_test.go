package items

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/link"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// memStore is Store in memory; it round-trips through JSON as the sqlite store does.
type memStore struct {
	items     map[unit.Unit][]items.Item
	log       []LogEntry
	proposals []Proposal
	commits   int
}

func newMem() *memStore { return &memStore{items: map[unit.Unit][]items.Item{}} }

func roundTrip[T any](v T) T {
	raw, _ := json.Marshal(v)
	var out T
	_ = json.Unmarshal(raw, &out)
	return out
}

func (m *memStore) Load(_ context.Context, units []unit.Unit) (map[unit.Unit][]items.Item, error) {
	out := map[unit.Unit][]items.Item{}
	for _, u := range units {
		if its := m.items[u]; len(its) > 0 {
			out[u] = roundTrip(its)
		}
	}
	return out, nil
}

func (m *memStore) Query(_ context.Context, q Query) ([]items.Item, error) {
	var out []items.Item
	for _, its := range m.items {
		for _, it := range its {
			if q.State != "" && it.State != q.State {
				continue
			}
			out = append(out, it)
		}
	}
	return out, nil
}

func (m *memStore) Proposals(_ context.Context, state ProposalState) ([]Proposal, error) {
	var out []Proposal
	for _, p := range m.proposals {
		if state == "" || p.State == state {
			out = append(out, roundTrip(p))
		}
	}
	return out, nil
}

func (m *memStore) Log(_ context.Context, u unit.Unit, since time.Time) ([]LogEntry, error) {
	var out []LogEntry
	for _, e := range m.log {
		if e.Unit == u && !e.At.Before(since) {
			out = append(out, e)
		}
	}
	return out, nil
}

func (m *memStore) Commit(_ context.Context, c Commit) error {
	m.commits++
	for _, it := range roundTrip(c.Items) {
		its := m.items[it.Unit]
		if i := slices.IndexFunc(its, func(o items.Item) bool { return o.ID == it.ID }); i >= 0 {
			its[i] = it
		} else {
			m.items[it.Unit] = append(its, it)
		}
	}
	m.log = append(m.log, c.Log...)
	for _, p := range c.Proposals {
		if p.ID != 0 {
			continue
		}
		p.ID = int64(len(m.proposals) + 1)
		m.proposals = append(m.proposals, roundTrip(p))
	}
	return nil
}

func (m *memStore) outcomes() []string {
	var out []string
	for _, e := range m.log {
		out = append(out, e.Outcome)
	}
	return out
}

type fakeLinks struct {
	put []link.Link
}

func (f *fakeLinks) ReplaceFacts(context.Context, []unit.Unit, []link.Link, time.Time) error {
	return nil
}

func (f *fakeLinks) Put(_ context.Context, l link.Link) error {
	f.put = append(f.put, l)
	return nil
}

func (f *fakeLinks) From(_ context.Context, u unit.Unit) ([]link.Link, error) {
	var out []link.Link
	for _, l := range f.put {
		if l.From == u {
			out = append(out, l)
		}
	}
	return out, nil
}

func (f *fakeLinks) To(context.Context, []unit.Unit) ([]link.Link, error) { return nil, nil }

type clock struct{ t time.Time }

func (c *clock) Now() time.Time { return c.t }

var (
	thread = unit.Unit{Provider: "slack", Key: "C000AAA/1.2"}
	pull   = unit.Unit{Provider: unit.GitHub, Key: "o/r#1"}
	t0     = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
)

func newLedger(s *memStore) (*Ledger, *clock) {
	c := &clock{t: t0}
	return &Ledger{Store: s, Clock: c}, c
}

func ptr[T any](v T) *T { return &v }

func anchor(ref string) unit.Anchor { return unit.Anchor{Ref: ref} }

func create(u unit.Unit, id, summary string) items.Op {
	return items.Op{Op: items.OpCreate, Unit: u, Item: id, Anchor: anchor("m:" + summary),
		Fields: &items.Fields{Kind: ptr(items.Request), Summary: ptr(summary)}}
}

func update(u unit.Unit, id, summary, ref string) items.Op {
	return items.Op{Op: items.OpUpdate, Unit: u, Item: id, Anchor: anchor(ref), Fields: &items.Fields{Summary: ptr(summary)}}
}

func apply(t *testing.T, l *Ledger, prov items.Provenance, ops ...items.Op) Result {
	t.Helper()
	res, err := l.Apply(context.Background(), prov, ops)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func itemOf(t *testing.T, l *Ledger, u unit.Unit, id string) items.Item {
	t.Helper()
	its, err := l.Items(context.Background(), u)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range its {
		if it.ID == id {
			return it
		}
	}
	t.Fatalf("no item %s in %v", id, its)
	return items.Item{}
}
