package slack

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/wloophq/wloop/plugins/slack/config"
	"github.com/wloophq/wloop/plugins/slack/host/hosttest"
	"github.com/wloophq/wloop/plugins/slack/items"
	"github.com/wloophq/wloop/plugins/slack/store"
	"github.com/wloophq/wloop/plugins/slack/unit"
)

// unownedAge is the slack.items_unowned_age_days u's latest snapshot
// reports; -1 when h has none.
func unownedAge(h *hosttest.Host, u unit.Unit) int64 {
	s, ok := h.Snapshot(u)
	if !ok {
		return -1
	}
	for _, f := range s.Facts {
		if f.Name == "slack.items_unowned_age_days" {
			return f.Value.Int
		}
	}
	return -1
}

func TestAFactThatChangesWithTimeAloneIsReportedAgainWhenItChanges(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		db, err := store.Open(ctx, filepath.Join(dir, "state"))
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		h := &hosttest.Host{Now: time.Now}
		cfg := config.Config{Brand: "loop"}
		cfg.Slack.ClientID = "1.2"
		p := newPlugin(cfg, h.Broker(), db, Slack{Now: time.Now}, t.Logf)
		u := factsThread
		if err := db.Extraction().Save(ctx, store.ExtractionSave{States: map[unit.Unit]store.ExtractionState{u: {At: time.Now()}}}); err != nil {
			t.Fatal(err)
		}
		h.Put(items.Item{ID: "i1", Unit: u, Kind: items.Request, Summary: "who takes the release notes?", Origin: unit.Anchor{Unit: u, Ref: "m:i1"},
			Requester: items.Person{Key: "slack:U0000000009"}, Basis: items.NoBasis, State: items.Open, Confidence: items.High,
			Provenance: items.Extracted, CreatedAt: time.Now(), UpdatedAt: time.Now()})

		ctx, cancel := context.WithCancel(ctx)
		var wg sync.WaitGroup
		defer wg.Wait()
		defer cancel()
		wg.Go(func() { p.reports.run(ctx, time.Minute) })
		synctest.Wait()
		if got := unownedAge(h, u); got != 0 {
			t.Fatalf("first report: age %d, want 0", got)
		}
		reports := len(h.Reports())

		// The bubble starts on a Saturday: four days later is at least one
		// working day past the item's creation, with no store change or
		// note in between.
		time.Sleep(4 * 24 * time.Hour)
		synctest.Wait()
		if got := unownedAge(h, u); got < 1 || len(h.Reports()) == reports {
			t.Fatalf("after four days: age %d over %d reports, want a later report of a higher age", got, len(h.Reports())-reports)
		}
	})
}
