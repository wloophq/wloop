package sqlite

import (
	"context"
	"testing"
	"time"
)

func init() {
	reads = append(reads, "SlackStore.RowObjects")
}

func TestRowObjectsAreWatchedOrRecent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openExtraction(t)
	s := db.Slack()
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	objs := []ObjectFacts{
		{Object: ObjectRow{Object: "C000AAA/1700000001.000100", Conversation: "C000AAA", TS: "1700000001.000100", Kind: SlackThread,
			LastActivityAt: now.Add(-time.Hour)}},
		{Object: ObjectRow{Object: "C000AAA/1700000002.000100", Conversation: "C000AAA", TS: "1700000002.000100", Kind: SlackThread,
			LastActivityAt: now.Add(-30 * 24 * time.Hour)}},
		{Object: ObjectRow{Object: "C000AAA/1700000003.000100", Conversation: "C000AAA", TS: "1700000003.000100", Kind: SlackThread,
			LastActivityAt: now.Add(-30 * 24 * time.Hour)}},
	}
	if _, err := s.Save(ctx, FactsRows{Team: "T000AAA", Objects: objs}, now); err != nil {
		t.Fatal(err)
	}
	for _, o := range objs[:2] {
		if err := s.SetWatched(ctx, o.Object.Object, false, ""); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.RowObjects(ctx, now.Add(-14*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Object != objs[0].Object.Object || got[1].Object != objs[2].Object.Object || !got[1].Watched {
		t.Fatalf("row objects %+v", got)
	}
}
