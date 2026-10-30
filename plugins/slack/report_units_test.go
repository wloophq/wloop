package slack

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/wloophq/wloop/plugins/slack/config"
	dslack "github.com/wloophq/wloop/plugins/slack/domain"
	"github.com/wloophq/wloop/plugins/slack/host/hosttest"
	"github.com/wloophq/wloop/plugins/slack/items"
	"github.com/wloophq/wloop/plugins/slack/store"
	"github.com/wloophq/wloop/plugins/slack/unit"
)

// TestTheReportCarriesOnlyUnitsTheHostKnows: the extraction's cursors and a DM conversation's state are
// units of the plugin's store, never of the host, so a report never names them.
func TestTheReportCarriesOnlyUnitsTheHostKnows(t *testing.T) {
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
		cursor := unit.Unit{Provider: dslack.Provider}
		conversation := unit.Unit{Provider: dslack.Provider, Key: "T000AAA/D000AAA"}
		states := map[unit.Unit]store.ExtractionState{u: {At: time.Now()}, cursor: {LastSeq: 7, At: time.Now()}, conversation: {At: time.Now()}}
		if err := db.Extraction().Save(ctx, store.ExtractionSave{States: states}); err != nil {
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
		if snap, ok := h.Snapshot(u); !ok || snap.Gone {
			t.Fatalf("the thread was not reported: %+v %v", snap, ok)
		}
		for _, rep := range h.Reports() {
			for _, s := range rep {
				if dslack.ValidKey(s.Unit.Key) != nil {
					t.Errorf("reported %q, which the host refuses", s.Unit.Key)
				}
			}
		}
	})
}
