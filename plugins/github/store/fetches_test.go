package store

import (
	"context"
	"testing"
	"time"
)

func TestFetchesKeepTheLastOfEachWorkspace(t *testing.T) {
	t.Parallel()
	db := open(t)
	ctx := context.Background()
	if at, err := db.LastFetch(ctx, "w1"); err != nil || !at.IsZero() {
		t.Fatalf("never fetched: %v %v", at, err)
	}
	for _, at := range []time.Time{noteAt, noteAt.Add(time.Hour)} {
		if err := db.MarkFetch(ctx, "w1", at); err != nil {
			t.Fatal(err)
		}
	}
	if at, err := db.LastFetch(ctx, "w1"); err != nil || !at.Equal(noteAt.Add(time.Hour)) {
		t.Fatalf("last fetch %v %v", at, err)
	}
	if err := db.MarkFetch(ctx, "w1", time.Time{}); err == nil {
		t.Fatal("a fetch without a time was kept")
	}
}
