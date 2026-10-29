package inventorytest

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/port"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/meter/metertest"
)

const (
	linkedPRs = 51
	totalPRs  = 216
)

func seedLinked(t *testing.T, s *Store) map[github.Ref]dinv.Workspace {
	t.Helper()
	ctx := context.Background()
	links := map[github.Ref]dinv.Workspace{}
	for n := 1; n <= totalPRs; n++ {
		r := github.Ref{Owner: "acme", Repo: "api", Number: n}
		upsert(t, s, openPR(r, "author"))
		if n <= linkedPRs {
			links[r] = dinv.Workspace{ID: "w", DisplayName: "ws", Status: "idle", Comment: "c"}
		}
	}
	if err := s.ReplaceWorkspaces(ctx, links); err != nil {
		t.Fatal(err)
	}
	return links
}

func walBytes(t *testing.T, s *Store, path string) int64 {
	t.Helper()
	rows, err := s.db.R().QueryContext(context.Background(), `PRAGMA wal_checkpoint(TRUNCATE)`)
	if err != nil {
		t.Fatal(err)
	}
	rows.Close()
	info, err := os.Stat(path + "-wal")
	if err != nil {
		t.Fatal(err)
	}
	return info.Size()
}

func TestReplaceWorkspacesUnchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wal.db")
	log := change.NewLog(change.DefaultWindow, nil)
	db, err := sqlite.Open(path, log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	s := Of(inventory.New(db))
	links := seedLinked(t, s)
	if walBytes(t, s, path) != 0 {
		t.Fatal("the checkpoint left frames in the WAL")
	}
	ctx, sc := metertest.Start(t, context.Background())
	got, notes := noted(t, log, func() error { return s.ReplaceWorkspaces(ctx, links) })
	if notes != 0 || got.Topics != 0 {
		t.Fatalf("an unchanged link set noted %d times: %+v", notes, got)
	}
	if n := metertest.Count(sc, metertest.Limit{Res: meter.SQL, Op: "exec"}); n != 0 {
		t.Fatalf("%d SQL execs, want 0\n%s", n, metertest.Report(sc.Tally()))
	}
	info, err := os.Stat(path + "-wal")
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 0 {
		t.Fatalf("an unchanged link set wrote %d bytes of WAL, want 0", info.Size())
	}
}

func TestReplaceWorkspacesOneMoved(t *testing.T) {
	s, log := openNoted(t)
	links := seedLinked(t, s)
	from, to := github.Ref{Owner: "acme", Repo: "api", Number: 1}, github.Ref{Owner: "acme", Repo: "api", Number: 100}
	links[to] = links[from]
	delete(links, from)
	ctx, sc := metertest.Start(t, context.Background())
	got, n := noted(t, log, func() error { return s.ReplaceWorkspaces(ctx, links) })
	if n != 1 || got.Topics != port.TopicPRs|port.TopicFollow {
		t.Fatalf("%d notes of topics %b", n, got.Topics)
	}
	if want := unitsOf([]github.Ref{from, to}); len(got.Units) != 2 || got.Units[0] != want[0] || got.Units[1] != want[1] {
		t.Fatalf("noted units %v, want %v", got.Units, want)
	}
	if n := metertest.Count(sc, metertest.Limit{Res: meter.SQL, Op: "exec"}); n != 2 {
		t.Fatalf("%d UPDATEs, want 2\n%s", n, metertest.Report(sc.Tally()))
	}
}
