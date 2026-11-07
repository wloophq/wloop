package sqlite

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func storeAtV10(t *testing.T) string {
	t.Helper()
	frozen, err := os.ReadFile(filepath.Join("testdata", "schema-v10.sql"))
	if err != nil {
		t.Fatal(err)
	}
	path := storeAt(t, schemaSet{10, string(frozen), nil})
	rawExec(t, path,
		`INSERT INTO drafts (provider, unit_key, items, kind, text, original, origin_kind, workflow, state, reason, created_at, updated_at, purpose, delivery, effects)
		 VALUES ('acme', 'C1/1', '["i1"]', 'nudge', 'any news?', 'any news?', 'auto', 'stalled', 'held', '', '2026-11-04T10:00:00Z', '2026-11-04T10:00:00Z', 'nudge', 'message', '["task.nudged"]')`,
		`INSERT INTO drafts (provider, unit_key, kind, origin_kind, state, reason, created_at, updated_at, purpose, delivery)
		 VALUES ('acme', 'C1/2', 'close', 'auto', 'dropped', 'user', '2026-11-04T11:00:00Z', '2026-11-04T11:00:00Z', 'close', 'message')`,
	)
	return path
}

func TestVersion11AddsWhyAndLanguageToDrafts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := storeAtV10(t)
	for range 2 {
		if _, err := current.migrate(ctx, path, migrateOpts(path)); err != nil {
			t.Fatal(err)
		}
	}
	if v := versionOf(t, path); v != 11 {
		t.Fatalf("version %d", v)
	}
	db, err := current.open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	list, err := db.Drafts().List(ctx, outbox.Filter{})
	if err != nil || len(list) != 2 {
		t.Fatalf("%d drafts, %v", len(list), err)
	}
	d, err := db.Drafts().Get(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if d.Why != "" || d.Language != "" {
		t.Errorf("why %q language %q, want both empty", d.Why, d.Language)
	}
	if d.Text != "any news?" || d.Origin.Workflow != "stalled" || !slices.Equal(d.Items, []string{"i1"}) || !slices.Equal(d.Effects, []string{"task.nudged"}) {
		t.Errorf("draft 1 lost data: %+v", d)
	}
	d.Why, d.Language = "no answer since 04/11", "pt"
	if _, err := db.Drafts().Update(ctx, d, draft.Held); err != nil {
		t.Fatal(err)
	}
	got, err := db.Drafts().Get(ctx, 1)
	if err != nil || got.Why != "no answer since 04/11" || got.Language != "pt" {
		t.Errorf("round trip: why %q language %q %v", got.Why, got.Language, err)
	}
}

func TestVersion11CreatesWorkflowSkips(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := storeAtV10(t)
	if _, err := current.migrate(ctx, path, migrateOpts(path)); err != nil {
		t.Fatal(err)
	}
	db, err := current.open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.r.Query(`SELECT name FROM pragma_table_info('workflow_skips') ORDER BY cid`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			t.Fatal(err)
		}
		cols = append(cols, c)
	}
	if want := []string{"provider", "unit_key", "workflow", "at"}; !slices.Equal(cols, want) {
		t.Errorf("columns %v, want %v", cols, want)
	}
}

func TestInsertKeepsWhyAndLanguage(t *testing.T) {
	t.Parallel()
	db, _ := openTemp(t)
	ctx := context.Background()
	d := draft.Draft{Unit: unit.Unit{Provider: "acme", Key: "C1/1"}, Kind: "nudge", Text: "x", Origin: draft.Origin{Kind: draft.OriginAuto}, State: draft.Held,
		Why: "w", Language: "pt-BR"}
	in, err := db.Drafts().Insert(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	got, err := db.Drafts().Get(ctx, in.ID)
	if err != nil || got.Why != "w" || got.Language != "pt-BR" {
		t.Errorf("why %q language %q %v", got.Why, got.Language, err)
	}
}
