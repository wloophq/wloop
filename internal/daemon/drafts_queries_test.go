package daemon

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/meter"
)

// draftsStatements is how many SQL statements GET path runs.
func draftsStatements(t *testing.T, r *outboxRig, path string) int64 {
	t.Helper()
	ctx, scope := meter.Start(context.Background(), "drafts")
	rec := httptest.NewRecorder()
	r.s.Handler().ServeHTTP(rec, apiRequest("GET", path, nil).WithContext(ctx))
	if rec.Code != 200 {
		t.Fatalf("GET %s: %d %s", path, rec.Code, rec.Body)
	}
	return scope.End().Total(meter.SQL).Calls
}

func TestListingDraftsReadsTheStoreAFixedNumberOfTimes(t *testing.T) {
	t.Parallel()
	statements := func(t *testing.T, n int) map[string]int64 {
		r := newOutboxRig(t)
		var ids []string
		for i := range n {
			u := unit.Unit{Provider: unit.GitHub, Key: fmt.Sprintf("acme/api#%d", 100+i)}
			d := r.propose(t, u, fmt.Sprintf("note %d", i), draft.Wait)
			ids = append(ids, strconv.FormatInt(int64(d.ID), 10))
		}
		return map[string]int64{
			"state": draftsStatements(t, r, api.Drafts+"?state=held"),
			"id":    draftsStatements(t, r, api.Drafts+"?id="+strings.Join(ids, ",")),
		}
	}
	one, many := statements(t, 1), statements(t, 50)
	for _, by := range []string{"state", "id"} {
		t.Logf("drafts by %s: 1 draft %d statements, 50 drafts %d", by, one[by], many[by])
		if many[by] != one[by] {
			t.Errorf("drafts by %s: 1 draft reads in %d statements, 50 in %d", by, one[by], many[by])
		}
	}
}

func TestASyncWithNothingToChangeReadsTheStoreAFixedNumberOfTimes(t *testing.T) {
	t.Parallel()
	statements := func(t *testing.T, n int) (first, again int64) {
		r := newTasksRig(t, nil)
		var units []unit.Unit
		for i := range n {
			u := unit.Unit{Provider: unit.GitHub, Key: fmt.Sprintf("acme/api#%d", 100+i)}
			if _, err := r.ledger.Apply(context.Background(), items.User, []items.Op{owedToMe(u, "i1")}); err != nil {
				t.Fatal(err)
			}
			units = append(units, u)
		}
		sync := func() int64 {
			ctx, scope := meter.Start(context.Background(), "sync")
			if err := r.g.Sync(ctx, units); err != nil {
				t.Fatal(err)
			}
			return scope.End().Total(meter.SQL).Calls
		}
		return sync(), sync()
	}
	oneFirst, oneAgain := statements(t, 1)
	manyFirst, manyAgain := statements(t, 50)
	t.Logf("sync creating tasks: 1 unit %d statements, 50 units %d", oneFirst, manyFirst)
	t.Logf("sync with nothing to change: 1 unit %d statements, 50 units %d", oneAgain, manyAgain)
	if manyAgain != oneAgain {
		t.Errorf("a sync with nothing to change: 1 unit reads in %d statements, 50 in %d", oneAgain, manyAgain)
	}
}

func BenchmarkListingDrafts(b *testing.B) {
	r := newOutboxRig(b)
	var ids []string
	for i := range 50 {
		d, err := r.o.Propose(context.Background(), outbox.Proposal{Unit: unit.Unit{Provider: unit.GitHub, Key: fmt.Sprintf("acme/api#%d", 100+i)},
			Kind: draft.Kind("reply"), Text: fmt.Sprintf("note %d", i), Origin: draft.Origin{Kind: draft.OriginAuto, Rule: "test", Key: strconv.Itoa(i)},
			Policy: draft.Fixed(draft.Approval{Mode: draft.Wait})})
		if err != nil {
			b.Fatal(err)
		}
		ids = append(ids, strconv.FormatInt(int64(d.ID), 10))
	}
	for _, path := range []string{api.Drafts + "?state=held", api.Drafts + "?id=" + strings.Join(ids, ",")} {
		b.Run(strings.SplitN(strings.TrimPrefix(path, api.Drafts+"?"), "=", 2)[0], func(b *testing.B) {
			for b.Loop() {
				rec := httptest.NewRecorder()
				r.s.Handler().ServeHTTP(rec, apiRequest("GET", path, nil))
				if rec.Code != 200 {
					b.Fatalf("GET %s: %d", path, rec.Code)
				}
			}
		})
	}
}

func BenchmarkSyncingUnits(b *testing.B) {
	r := newTasksRig(b, nil)
	var units []unit.Unit
	for i := range 50 {
		u := unit.Unit{Provider: unit.GitHub, Key: fmt.Sprintf("acme/api#%d", 100+i)}
		if _, err := r.ledger.Apply(context.Background(), items.User, []items.Op{owedToMe(u, "i1")}); err != nil {
			b.Fatal(err)
		}
		units = append(units, u)
	}
	if err := r.g.Sync(context.Background(), units); err != nil {
		b.Fatal(err)
	}
	for b.Loop() {
		if err := r.g.Sync(context.Background(), units); err != nil {
			b.Fatal(err)
		}
	}
}
