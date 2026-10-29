package daemon

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory/inventorytest"

	"github.com/wloophq/wloop/internal/adapter/store/storetest"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brief"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/meter/metertest"
)

func flowBudget(sql, rows, readTx int64) metertest.Budget {
	return metertest.Budget{
		{Res: meter.SQL}:                        sql,
		{Res: meter.SQL, Field: metertest.Rows}: rows,
		{Res: meter.Tx, Op: "read"}:             readTx,
		{Res: meter.Tx, Op: "write"}:            0,
		{Res: meter.Workspace}:                  0,
	}
}

func TestBudgets(t *testing.T) {
	t.Parallel()
	probe := func(t *testing.T, s *Server, path string) *meter.Scope {
		t.Helper()
		ctx, sc := metertest.Start(t, context.Background())
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, apiRequest("GET", path, nil).WithContext(ctx))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: %d %s", path, rec.Code, rec.Body)
		}
		return sc
	}
	_, f0 := newLiveServer(t)
	var turned pr.Ref
	for _, ref := range f0.Open {
		if slices.Contains(f0.WithTurns, ref.Unit()) {
			turned = ref
			break
		}
	}
	detail := api.UnitPath(turned.Unit())
	flows := []struct {
		name, path string
		settle     bool
		cold, warm metertest.Budget
	}{
		// The rows read the reported units and their reports apart: one
		// statement and one row per pull request more than the inventory's.
		{"rows", api.Rows, false, flowBudget(11, 8294, 1), flowBudget(0, 0, 0)},
		{"unit detail", detail, true, flowBudget(15, 8297, 2), flowBudget(4, 2, 1)},
		{"actions", api.Actions, false, flowBudget(0, 0, 0), flowBudget(0, 0, 0)},
		{"health", "/api/health", false, flowBudget(4, 141, 0), flowBudget(2, 1, 0)},
		{"activity", "/api/activity", false, flowBudget(2, 266, 0), flowBudget(2, 266, 0)},
	}
	for _, fl := range flows {
		t.Run(fl.name, func(t *testing.T) {
			t.Parallel()
			s, f := liveServer(t)
			if fl.settle {
				settledBrief(t, s, f, turned)
			}
			cold := probe(t, s, fl.path)
			t.Logf("cold GET %s:\n%s", fl.path, metertest.Report(cold.Tally()))
			metertest.Within(t, cold, fl.cold)
			warm := probe(t, s, fl.path)
			t.Logf("warm GET %s:\n%s", fl.path, metertest.Report(warm.Tally()))
			metertest.Within(t, warm, fl.warm)
		})
	}
}

type failingSource struct{}

func (failingSource) Facts(context.Context, github.Ref) (brief.Input, error) {
	return brief.Input{}, errors.New("no source")
}

func (failingSource) Diff(context.Context, github.Ref, int) (string, bool, error) {
	return "", false, errors.New("no source")
}

// settledBrief gives s a brief queue and lets the refresh a detail would
// start run to its failure first, so serving the detail starts none.
func settledBrief(t *testing.T, s *Server, f *storetest.Fixture, ref pr.Ref) {
	t.Helper()
	q := brief.New(brief.Deps{Store: f.Inventory, PRs: inventorytest.Of(f.Inventory), Source: failingSource{}, Taxonomy: func(github.Ref) string { return brief.DefaultTaxonomy },
		Retry: time.Hour, Changes: f.Log, Now: func() time.Time { return f.Now }, Log: func(string, ...any) {}})
	withBriefs(&s.deps, q)
	ctx := context.Background()
	p, ok, err := inventorytest.Of(f.Inventory).PR(ctx, github.Ref(ref))
	if err != nil || !ok {
		t.Fatalf("pull request %v: %v %v", ref, ok, err)
	}
	if err := q.RefreshIfStale(ctx, p); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		rec, _, err := f.Inventory.BriefRecord(ctx, github.Ref(ref))
		if err != nil {
			t.Fatal(err)
		}
		if rec.Error != "" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the refresh of %v did not settle: %+v", ref, rec)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
