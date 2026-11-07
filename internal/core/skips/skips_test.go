package skips

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/unit"
)

var (
	thread = unit.Unit{Provider: "acme", Key: "C1/1"}
	other  = unit.Unit{Provider: "acme", Key: "C1/2"}
	at     = time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
)

type memStore struct {
	rows  map[unit.Unit][]string
	loads int
}

func (m *memStore) SaveSkip(_ context.Context, wf string, u unit.Unit, _ time.Time) error {
	if !slices.Contains(m.rows[u], wf) {
		m.rows[u] = append(m.rows[u], wf)
	}
	return nil
}

func (m *memStore) DeleteSkip(_ context.Context, wf string, u unit.Unit) error {
	m.rows[u] = slices.DeleteFunc(m.rows[u], func(w string) bool { return w == wf })
	return nil
}

func (m *memStore) AllSkips(context.Context) (map[unit.Unit][]string, error) {
	m.loads++
	out := map[unit.Unit][]string{}
	for u, ws := range m.rows {
		out[u] = slices.Clone(ws)
	}
	return out, nil
}

type memDrafts struct {
	drafts    []draft.Draft
	withdrawn map[draft.ID]string
}

func (m *memDrafts) List(_ context.Context, f outbox.Filter) ([]draft.Draft, error) {
	var out []draft.Draft
	for _, d := range m.drafts {
		if (f.Unit == nil || d.Unit == *f.Unit) && (f.Workflow == "" || d.Origin.Workflow == f.Workflow) &&
			(len(f.States) == 0 || slices.Contains(f.States, d.State)) {
			out = append(out, d)
		}
	}
	return out, nil
}

func (m *memDrafts) Withdraw(_ context.Context, id draft.ID, reason string) (draft.Draft, error) {
	m.withdrawn[id] = reason
	return draft.Draft{ID: id}, nil
}

type fixedClock struct{}

func (fixedClock) Now() time.Time { return at }

func newService(drafts ...draft.Draft) (*Service, *memStore, *memDrafts) {
	st := &memStore{rows: map[unit.Unit][]string{}}
	ds := &memDrafts{drafts: drafts, withdrawn: map[draft.ID]string{}}
	return &Service{Store: st, Drafts: ds, Clock: fixedClock{}}, st, ds
}

func held(id draft.ID, wf string, u unit.Unit) draft.Draft {
	return draft.Draft{ID: id, Unit: u, State: draft.Held, Origin: draft.Origin{Kind: draft.OriginAuto, Workflow: wf}}
}

func TestSkipWithdrawsTheWorkflowsHeldDraftsOnTheUnit(t *testing.T) {
	ctx := context.Background()
	sent := held(4, "nudge", thread)
	sent.State = draft.Sent
	s, _, ds := newService(held(1, "nudge", thread), held(2, "closure", thread), held(3, "nudge", other), sent)
	if err := s.Skip(ctx, "nudge", thread); err != nil {
		t.Fatal(err)
	}
	if len(ds.withdrawn) != 1 || ds.withdrawn[1] != draft.WithdrawSkipped {
		t.Fatalf("withdrawn %v, want draft 1 only, %q", ds.withdrawn, draft.WithdrawSkipped)
	}
	if ok, err := s.Skipped(ctx, "nudge", thread); err != nil || !ok {
		t.Fatalf("skipped %v %v", ok, err)
	}
	for _, c := range []struct {
		wf string
		u  unit.Unit
	}{{"closure", thread}, {"nudge", other}} {
		if ok, _ := s.Skipped(ctx, c.wf, c.u); ok {
			t.Errorf("%s on %s reads skipped", c.wf, c.u)
		}
	}
}

func TestUnskipLetsTheWorkflowRunAgain(t *testing.T) {
	ctx := context.Background()
	s, st, _ := newService()
	if err := s.Skip(ctx, "nudge", thread); err != nil {
		t.Fatal(err)
	}
	if err := s.Unskip(ctx, "nudge", thread); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.Skipped(ctx, "nudge", thread); ok || len(st.rows[thread]) != 0 {
		t.Fatalf("still skipped: %v, stored %v", ok, st.rows[thread])
	}
	if got, _ := s.Of(ctx, thread); len(got) != 0 {
		t.Fatalf("Of %v", got)
	}
}

func TestSkipsReadTheStoreOnce(t *testing.T) {
	ctx := context.Background()
	s, st, _ := newService()
	st.rows[thread] = []string{"nudge"}
	for range 3 {
		if ok, _ := s.Skipped(ctx, "nudge", thread); !ok {
			t.Fatal("a stored skip reads as none")
		}
	}
	if err := s.Skip(ctx, "closure", thread); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Of(ctx, thread); !slices.Equal(got, []string{"closure", "nudge"}) || st.loads != 1 {
		t.Fatalf("Of %v after %d loads", got, st.loads)
	}
}
