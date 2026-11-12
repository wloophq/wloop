package sqlite

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gate"
)

// TestVersion14MakesADraftsRevCountItsContent migrates a store as the installed migration 13 left it: draft 1
// held at rev 2 after its task's adoption, which the trigger carried its open gate to; draft 2 revised after
// its gate opened at rev 1; draft 3 at rev 2 after a broadcast change the trigger did not carry and no
// revision recorded. Only draft 1's gate still approves its content, and it sends.
func TestVersion14MakesADraftsRevCountItsContent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	frozen, err := os.ReadFile(filepath.Join("testdata", "schema-v13.sql"))
	if err != nil {
		t.Fatal(err)
	}
	path := storeAt(t, schemaSet{13, string(frozen), nil})
	const draftRow = `INSERT INTO drafts (id, provider, unit_key, items, kind, text, original, origin_kind, state, created_at, updated_at, purpose, delivery, effects, broadcast, rev)
		VALUES (%d, 'acme', 'C0123/1.2', '[]', 'reply', '%s', 'hi', 'turn', 'held', '2026-11-04T10:00:00.000000000Z', '2026-11-04T10:00:00.000000000Z', 'reply', 'message', '[]', '%s', %d)`
	const revRow = `INSERT INTO draft_revisions (draft_id, rev, text, by_kind, at) VALUES (%d, %d, '%s', 'session', '2026-11-04T10:00:00.000000000Z')`
	const gateRow = `INSERT INTO gates (provider, unit_key, kind, name, subject, status, intents, opened_at, approved_rev)
		VALUES ('acme', 'C0123/1.2', 'draft', 'draft', '%d', 'open', '["approve","discard"]', '2026-11-04T10:00:00.000000000Z', %d)`
	rawExec(t, path,
		fmt.Sprintf(draftRow, 1, "hi", "", 2), fmt.Sprintf(revRow, 1, 1, "hi"), fmt.Sprintf(gateRow, 1, 2),
		fmt.Sprintf(draftRow, 2, "revised", "", 3), fmt.Sprintf(revRow, 2, 1, "hi"), fmt.Sprintf(revRow, 2, 3, "revised"), fmt.Sprintf(gateRow, 2, 1),
		fmt.Sprintf(draftRow, 3, "hi", "on", 2), fmt.Sprintf(revRow, 3, 1, "hi"), fmt.Sprintf(gateRow, 3, 1),
	)
	if _, err := current.migrate(ctx, path, migrateOpts(path)); err != nil {
		t.Fatal(err)
	}
	db, err := current.open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.R().QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type = 'trigger' AND name = 'drafts_rev_keeps_approval'`).Scan(&n); err != nil || n != 0 {
		t.Fatal("the trigger that carried approvals in the store is still there")
	}
	gs := db.Gates(func() time.Time { return time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC) })
	want := map[int64]struct{ rev, version, gateRev int64 }{1: {1, 2, 1}, 2: {2, 3, 0}, 3: {1, 2, 0}}
	for id, w := range want {
		d, err := db.Drafts().Get(ctx, draft.ID(id))
		if err != nil {
			t.Fatal(err)
		}
		g, err := gs.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		revs, err := db.Drafts().Revisions(ctx, draft.ID(id))
		if err != nil || d.Rev != w.rev || d.Version != w.version || g.ApprovedRev != w.gateRev || revs[len(revs)-1].Rev != d.Rev {
			t.Errorf("draft %d at rev %d version %d, gate %d, revisions %+v %v; want %+v", id, d.Rev, d.Version, g.ApprovedRev, revs, err, w)
		}
	}
	held, _ := db.Drafts().Get(ctx, 1)
	approved, err := gs.Resolve(ctx, 1, gates.Resolution{Status: gate.Approved, Intent: "approve", By: "person"})
	if err != nil || approved.ApprovedRev != held.Rev {
		t.Fatalf("approved %+v %v", approved, err)
	}
	for _, step := range []struct{ from, to draft.State }{{draft.Held, draft.Queued}, {draft.Queued, draft.Sending}, {draft.Sending, draft.Sent}} {
		held.State = step.to
		if held, err = db.Drafts().Update(ctx, held, step.from); err != nil || held.Rev != approved.ApprovedRev {
			t.Fatalf("%s to %s: rev %d %v", step.from, step.to, held.Rev, err)
		}
	}
}
