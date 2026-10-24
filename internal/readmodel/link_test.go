package readmodel_test

import (
	"context"
	"testing"

	"github.com/wloophq/wloop/internal/adapter/store/storetest"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestRowCarriesServerLinkAndLabel(t *testing.T) {
	ctx := context.Background()
	f := storetest.Seeded(t, storetest.Live)
	ref := f.Open[0]
	stored, ok, err := f.Inventory.PR(ctx, ref)
	if err != nil || !ok {
		t.Fatalf("%s: %v %v", ref, ok, err)
	}
	stored.URL = "https://ghe.example.com/" + ref.Owner + "/" + ref.Repo + "/pull/" + "7"
	if err := f.Inventory.UpsertPR(ctx, stored); err != nil {
		t.Fatal(err)
	}
	r := newRig(t, f)
	page := r.rows(r.m, api.Since{})
	if len(page.Rows) == 0 {
		t.Fatal("no rows")
	}
	for _, row := range page.Rows {
		got, err := pr.ParseKey(row.Unit.Key)
		if err != nil || row.Unit.Provider != unit.GitHub {
			t.Fatalf("row of unit %v: %v", row.Unit, err)
		}
		want := got.URL()
		if got == ref {
			want = stored.URL
		}
		if row.Link != want || row.Label != got.String() {
			t.Errorf("%s: link %q label %q, want %q and %q", got, row.Link, row.Label, want, got.String())
		}
	}
}
