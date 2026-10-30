package source_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/domain/link"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/plugins/codec"
	psource "github.com/wloophq/wloop/internal/plugins/source"
	"github.com/wloophq/wloop/pkg/plugin"
	"github.com/wloophq/wloop/pkg/plugin/manifest"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

func TestASnapshotsLinksAreTheUnitsFactsLinks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "store.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	c, err := codec.Of("acme", manifest.Codec{Keys: []string{"{thread}"}})
	if err != nil {
		t.Fatal(err)
	}
	b := &psource.Broker{ID: "acme", Units: psource.Units{Codecs: unit.NewRegistry(c, pr.Codec{})},
		Snapshots: psource.NewSnapshots(db.Snapshots()), Links: db.Links()}
	h := b.Host()
	at := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	thread := plugin.Unit{Provider: "acme", Key: "t1"}
	from := unit.Unit{Provider: "acme", Key: "t1"}
	want := pr.Ref{Owner: "acme", Repo: "api", Number: 12}.Unit()
	snap := src.Snapshot{Unit: thread, Rev: 1, At: at, Links: []src.Link{
		{URL: "https://github.com/acme/api/pull/12", Evidence: src.Anchor{Ref: "C1/1700000000.000100"}},
		{URL: "https://example.com/elsewhere"},
	}}
	if err := h.Reporter.Report(ctx, []src.Snapshot{snap}); err != nil {
		t.Fatal(err)
	}
	got, err := db.Links().From(ctx, from)
	if err != nil || len(got) != 1 || got[0].To != want || got[0].Kind != link.Mention || got[0].Origin != link.Facts ||
		got[0].Evidence.Ref != "C1/1700000000.000100" || got[0].Evidence.Unit != from {
		t.Fatalf("links %+v %v, want the pull request it names", got, err)
	}
	snap.Rev, snap.Links = 2, nil
	if err := h.Reporter.Report(ctx, []src.Snapshot{snap}); err != nil {
		t.Fatal(err)
	}
	if got, err := db.Links().From(ctx, from); err != nil || len(got) != 0 {
		t.Fatalf("links after a snapshot with none: %+v %v", got, err)
	}
}
