package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/core/outbox/outboxtest"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// gatedBases answers the live basis at once until hold is set, then only once release is closed, and counts reads.
type gatedBases struct {
	mu      sync.Mutex
	live    string
	err     error
	hold    bool
	release chan struct{}
	calls   int
}

func (b *gatedBases) Live(ctx context.Context, _ unit.ProviderID, _ unit.Unit, _ string) (string, error) {
	b.mu.Lock()
	b.calls++
	hold, release := b.hold, b.release
	b.mu.Unlock()
	if hold {
		select {
		case <-release:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.live, b.err
}

func (b *gatedBases) set(f func(*gatedBases)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	f(b)
}

// basisRig is the outbox rig with a review kind written against a basis, and drafts of it on r7u at the bases given.
func basisRig(t *testing.T, bases outbox.Bases, live func(string), at ...string) (*outboxRig, []draft.Draft) {
	t.Helper()
	r := newOutboxRig(t)
	t.Cleanup(r.s.Wait)
	var reads sync.WaitGroup
	t.Cleanup(reads.Wait)
	r.o.Bases = &outbox.RecentBases{Bases: bases, Clock: r.o.Clock, Fresh: 30 * time.Second, ReadTimeout: 10 * time.Second, Reads: &reads}
	r.o.Kinds = outboxtest.Kinds{Specs: []draft.KindSpec{{Provider: "github", Name: "review", Delivery: "review", Basis: draft.BasisAgent, Editable: []string{"text"}}},
		Defaults: map[string]string{"github": "comment"}}
	var ds []draft.Draft
	for i, b := range at {
		live(b)
		d, err := r.o.Propose(context.Background(), outbox.Proposal{Unit: r7u, Kind: draft.Kind("review"), Text: "review " + strconv.Itoa(i), Basis: b,
			Payload: json.RawMessage(`{"event":"COMMENT"}`), Origin: draft.Origin{Kind: draft.OriginAuto, Rule: "test", Key: strconv.Itoa(i)},
			Policy: draft.Fixed(draft.Approval{Mode: draft.Wait})})
		if err != nil {
			t.Fatal(err)
		}
		ds = append(ds, d)
	}
	return r, ds
}

func servedDrafts(t *testing.T, r *outboxRig) map[int64]api.Draft {
	t.Helper()
	var res api.DraftsResponse
	if code := get(t, r.s, api.Drafts+"?state=held", &res); code != 200 {
		t.Fatalf("GET: %d", code)
	}
	out := map[int64]api.Draft{}
	for _, d := range res.Drafts {
		out[d.ID] = d
	}
	return out
}

// TestCardsShareOneLiveBasisRead reads a unit's live basis once for every held draft of it a page shows, and
// again for the next page within the window not at all; each draft is stale by its own basis.
func TestCardsShareOneLiveBasisRead(t *testing.T) {
	t.Parallel()
	bases := &outboxtest.Bases{}
	r, ds := basisRig(t, bases, func(b string) { bases.Set(r7u, b) }, "c1", "c2", "c2")
	before := bases.Calls()
	page := servedDrafts(t, r)
	servedDrafts(t, r)
	if got := bases.Calls() - before; got != 1 {
		t.Errorf("%d live reads for two pages of three drafts of one unit, want 1", got)
	}
	for i, want := range []bool{true, false, false} {
		v := page[int64(ds[i].ID)]
		if v.Stale != want || v.StaleUnknown {
			t.Errorf("draft %d at %s: stale %v unknown %v, want stale %v and known", v.ID, v.Basis, v.Stale, v.StaleUnknown, want)
		}
	}
}

// TestAnUnansweredBasisReadLeavesStaleUnknown leaves a draft's staleness unknown, its approval offered, while the
// live read has not answered within the page's deadline or failed; once the read answers, the next page knows it.
func TestAnUnansweredBasisReadLeavesStaleUnknown(t *testing.T) {
	t.Parallel()
	bases := &gatedBases{release: make(chan struct{})}
	r, ds := basisRig(t, bases, func(b string) { bases.set(func(g *gatedBases) { g.live = b }) }, "c1")
	bases.set(func(g *gatedBases) { g.live, g.hold = "c2", true })
	r.s.whereLookup = time.Nanosecond
	v := servedDrafts(t, r)[int64(ds[0].ID)]
	if v.Stale || !v.StaleUnknown {
		t.Fatalf("while the read is on its way: stale %v unknown %v, want unknown", v.Stale, v.StaleUnknown)
	}
	if approve := v.Intents["approved"]; approve == "" {
		t.Error("an unknown staleness took the approval away")
	}
	close(bases.release)
	r.s.whereLookup = time.Minute
	deadline := time.Now().Add(10 * time.Second)
	for {
		v = servedDrafts(t, r)[int64(ds[0].ID)]
		if !v.StaleUnknown || time.Now().After(deadline) {
			break
		}
	}
	if !v.Stale || v.StaleUnknown {
		t.Fatalf("after the read answered: stale %v unknown %v, want stale", v.Stale, v.StaleUnknown)
	}

	failing := &gatedBases{}
	r2, ds2 := basisRig(t, failing, func(b string) { failing.set(func(g *gatedBases) { g.live = b }) }, "c1")
	failing.set(func(g *gatedBases) { g.err = errors.New("no answer: context deadline exceeded") })
	if v := servedDrafts(t, r2)[int64(ds2[0].ID)]; v.Stale || !v.StaleUnknown {
		t.Errorf("after a failed read: stale %v unknown %v, want unknown", v.Stale, v.StaleUnknown)
	}
}
