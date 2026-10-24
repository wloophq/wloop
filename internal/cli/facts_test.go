package cli

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/turns"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

func TestInventoryFactsAreMyReviewsSince(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	ctx := context.Background()
	facts := inventoryFacts{te.Store}
	since := testNow.Add(-time.Hour)
	review := func(key, actor, data string, at time.Time) dinv.Event {
		return dinv.Event{PR: r7, Kind: dinv.KindReview, Key: key, Actor: actor, Data: data, At: at}
	}
	if _, err := te.Store.AppendEvents(ctx, []dinv.Event{
		review("old", "me", "APPROVED:c1", since.Add(-time.Minute)),
		review("bob", "bob", "APPROVED:c2", testNow),
		review("cr", "Me", "CHANGES_REQUESTED:c2", testNow),
		review("cm", "me", "COMMENTED:c2", testNow.Add(time.Minute)),
		review("cr:DISMISSED", "me", "DISMISSED:c2", testNow.Add(2*time.Minute)),
		{PR: r7, Kind: dinv.KindComment, Key: "c", Actor: "me", At: testNow},
	}); err != nil {
		t.Fatal(err)
	}
	if got, err := facts.MyReviews(ctx, pr.Ref(r7).Unit(), since); err != nil || len(got) != 0 {
		t.Fatalf("before the viewer is known: %+v %v", got, err)
	}
	if err := te.Store.SetViewer(ctx, unit.GitHub, "me"); err != nil {
		t.Fatal(err)
	}
	got, err := facts.MyReviews(ctx, pr.Ref(r7).Unit(), since)
	if err != nil {
		t.Fatal(err)
	}
	slices.SortFunc(got, func(a, b turns.MyReview) int { return a.At.Compare(b.At) })
	want := []turns.MyReview{
		{ID: "cr", State: "CHANGES_REQUESTED", URL: r7.URL(), At: testNow},
		{ID: "cm", State: "COMMENTED", URL: r7.URL(), At: testNow.Add(time.Minute)},
	}
	if len(got) != len(want) {
		t.Fatalf("reviews %+v", got)
	}
	for i := range want {
		if got[i].ID != want[i].ID || got[i].State != want[i].State || got[i].URL != want[i].URL || !got[i].At.Equal(want[i].At) {
			t.Fatalf("reviews %+v, want %+v", got, want)
		}
	}
}
