package readmodel_test

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/plugins/codec"
	psource "github.com/wloophq/wloop/internal/plugins/source"
	"github.com/wloophq/wloop/internal/readmodel"
	"github.com/wloophq/wloop/pkg/plugin"
	"github.com/wloophq/wloop/pkg/plugin/manifest"
	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/pkg/plugin/source/sourcetest"
)

// pollUntil polls cond; its deadline only guards a hang.
func pollUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("never: %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestChangedWakesTheReadModel(t *testing.T) {
	one := unit.Unit{Provider: "acme", Key: "one"}
	r := &reports{snaps: map[unit.Unit]src.Snapshot{one: {Unit: plugin.Unit{Provider: "acme", Key: "one"}, Rev: 1,
		Situation: &src.Situation{Kind: "waiting", Reason: "ana asked"}}}}
	log := change.NewLog(change.DefaultWindow, nil)
	m := readmodel.New(log, nil, nil, nil, nil, readmodel.NewReported(readmodel.ReportedSource{ID: "acme", Reports: r}))
	c, err := codec.Of("acme", manifest.Codec{Keys: []string{"{name}"}})
	if err != nil {
		t.Fatal(err)
	}
	broker := &psource.Broker{ID: "acme", Units: psource.Units{Codecs: unit.NewRegistry(c)},
		Config: func() config.Config { return config.Config{} }, Note: func(tp port.Topic, us ...unit.Unit) { log.Note(tp, us...) }}
	stub := sourcetest.NewStub()
	sourcetest.Launch(t, "acme", stub, broker.Host())

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() { m.Run(ctx, func(api.Change) {}) })
	t.Cleanup(func() {
		cancel()
		wg.Wait()
	})
	pollUntil(t, "the first snapshot", func() bool { return r.reads.Load() > 0 })
	read := r.reads.Load()
	if err := stub.Host().Changed(ctx, []plugin.Unit{{Provider: "acme", Key: "one"}}, nil); err != nil {
		t.Fatal(err)
	}
	pollUntil(t, "the read model reads the reports again", func() bool { return r.reads.Load() > read })
	if got := log.Since(0, port.TopicPRs); len(got.Units) != 1 || got.Units[0] != one {
		t.Errorf("the change log holds %+v, want acme:one's unit data", got)
	}
	if err := stub.Host().Changed(ctx, []plugin.Unit{{Provider: "beta", Key: "two"}}, nil); err == nil {
		t.Error("a plugin noted another plugin's unit")
	}
}

func TestAStalePluginsRowsKeepTheLastSnapshot(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "store.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	log := change.NewLog(change.DefaultWindow, nil)
	snaps := psource.NewSnapshots(db.Snapshots())
	snaps.OnStale(func(unit.ProviderID) { log.NoteAll(port.TopicPRs) })
	ctx := context.Background()
	one := unit.Unit{Provider: "acme", Key: "one"}
	if err := snaps.Put(ctx, map[unit.Unit]src.Snapshot{one: {Unit: psource.Wire(one), Rev: 3,
		Situation: &src.Situation{Kind: "waiting", WaitingOn: "me", Reason: "ana asked"}, Card: &src.Card{Title: "deploy?"}}}); err != nil {
		t.Fatal(err)
	}
	m := readmodel.New(log, nil, nil, nil, nil, readmodel.NewReported(readmodel.ReportedSource{ID: "acme", Reports: snaps, Stale: snaps.Stale}))
	row := func(what string) (api.Row, uint64) {
		t.Helper()
		page, err := m.Rows(ctx, api.ViewAll, api.Since{})
		if err != nil || len(page.Rows) != 1 {
			t.Fatalf("%s: rows %+v, %v", what, page.Rows, err)
		}
		return page.Rows[0], page.Rev
	}
	fresh, rev := row("served")
	if fresh.Stale || fresh.Title != "deploy?" {
		t.Fatalf("a running plugin's row %+v", fresh)
	}
	snaps.SetDown("acme", true)
	stale, staleRev := row("down")
	if !stale.Stale || stale.Title != fresh.Title || stale.Summary != fresh.Summary || staleRev == rev {
		t.Fatalf("a down plugin's row %+v at rev %d, want its last snapshot marked stale past %d", stale, staleRev, rev)
	}
	snaps.SetDown("acme", false)
	if back, _ := row("up again"); back.Stale {
		t.Fatalf("a plugin back up still stale: %+v", back)
	}
}

func TestAReportedRowOffersThePluginsActions(t *testing.T) {
	one := unit.Unit{Provider: "acme", Key: "one"}
	r := &reports{snaps: map[unit.Unit]src.Snapshot{one: {Unit: psource.Wire(one), Rev: 1,
		Situation: &src.Situation{Kind: "waiting"}, Offers: []src.Offer{{Action: "acme.snooze"}, {Action: "acme.snooze"}}}}}
	m := readmodel.New(change.NewLog(change.DefaultWindow, nil), nil, nil, nil, nil, readmodel.NewReported(readmodel.ReportedSource{ID: "acme", Reports: r}))
	page, err := m.Rows(context.Background(), api.ViewAll, api.Since{})
	if err != nil || len(page.Rows) != 1 {
		t.Fatalf("rows %+v, %v", page.Rows, err)
	}
	if got := page.Rows[0].Actions; len(got) != 1 || got[0].ID != "acme.snooze" || !got[0].Enabled {
		t.Fatalf("actions %+v, want the plugin's offer once", got)
	}
}
