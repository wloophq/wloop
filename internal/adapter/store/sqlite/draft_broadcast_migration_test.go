package sqlite

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/wloophq/wloop/internal/domain/draft"
)

func TestVersion12AddsBroadcastToDrafts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	frozen, err := os.ReadFile(filepath.Join("testdata", "schema-v11.sql"))
	if err != nil {
		t.Fatal(err)
	}
	path := storeAt(t, schemaSet{11, string(frozen), nil})
	rawExec(t, path,
		`INSERT INTO drafts (provider, unit_key, items, kind, text, original, origin_kind, state, reason, created_at, updated_at, purpose, delivery, effects, why)
		 VALUES ('acme', 'D1/seg/s1', '["i1"]', 'nudge', 'any news?', 'any news?', 'auto', 'held', '', '2026-11-04T10:00:00Z', '2026-11-04T10:00:00Z', 'nudge', 'message', '[]', 'quiet for a week')`,
	)
	if _, err := current.migrate(ctx, path, migrateOpts(path)); err != nil {
		t.Fatal(err)
	}
	db, err := current.open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	d, err := db.Drafts().Get(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if d.Broadcast != draft.NoBroadcast || d.Text != "any news?" || d.Why != "quiet for a week" {
		t.Fatalf("migrated draft %+v", d)
	}
	d.Broadcast = draft.BroadcastOn
	if _, err := db.Drafts().Update(ctx, d, draft.Held); err != nil {
		t.Fatal(err)
	}
	if got, err := db.Drafts().Get(ctx, 1); err != nil || got.Broadcast != draft.BroadcastOn {
		t.Fatalf("round trip: %q %v", got.Broadcast, err)
	}
}
