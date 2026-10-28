package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/pkg/plugin/store"
	"github.com/wloophq/wloop/plugins/github/pr"
)

// exportedTables are the GitHub tables as the host's core schema of
// version 6 held them, which its store migration exports: overrides keeps
// the host's columns beside the plugin's.
var exportedTables = []string{
	`CREATE TABLE pull_requests (
  owner TEXT NOT NULL, repo TEXT NOT NULL, number INTEGER NOT NULL,
  title TEXT NOT NULL DEFAULT '', author TEXT NOT NULL DEFAULT '',
  state TEXT NOT NULL DEFAULT 'OPEN', is_draft INTEGER NOT NULL DEFAULT 0,
  head_oid TEXT NOT NULL DEFAULT '', head_branch TEXT NOT NULL DEFAULT '', base_branch TEXT NOT NULL DEFAULT '',
  is_fork INTEGER NOT NULL DEFAULT 0, checks_state TEXT NOT NULL DEFAULT '', review_decision TEXT NOT NULL DEFAULT '',
  review_requests TEXT NOT NULL DEFAULT '',
  url TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT '', updated_at TEXT NOT NULL DEFAULT '', merged_at TEXT NOT NULL DEFAULT '', closed_at TEXT NOT NULL DEFAULT '',
  body_edited_at TEXT NOT NULL DEFAULT '', changed_files INTEGER NOT NULL DEFAULT 0,
  reasons TEXT NOT NULL DEFAULT '',
  ws_id TEXT NOT NULL DEFAULT '', ws_name TEXT NOT NULL DEFAULT '', ws_status TEXT NOT NULL DEFAULT '', ws_review INTEGER NOT NULL DEFAULT 0, ws_agent TEXT NOT NULL DEFAULT '',
  ws_comment TEXT NOT NULL DEFAULT '', ws_main INTEGER NOT NULL DEFAULT 0, ws_manager TEXT NOT NULL DEFAULT '',
  failing_checks TEXT NOT NULL DEFAULT '',
  mergeable TEXT NOT NULL DEFAULT '',
  mergeable_at TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (owner, repo, number))`,
	`CREATE TABLE pr_seen (owner TEXT NOT NULL, repo TEXT NOT NULL, number INTEGER NOT NULL, at TEXT NOT NULL, PRIMARY KEY (owner, repo, number)) WITHOUT ROWID`,
	`CREATE TABLE pr_details (owner TEXT NOT NULL, repo TEXT NOT NULL, number INTEGER NOT NULL, version INTEGER NOT NULL, read_at TEXT NOT NULL, body TEXT NOT NULL,
  PRIMARY KEY (owner, repo, number)) WITHOUT ROWID`,
	`CREATE TABLE fetches (workspace_id TEXT PRIMARY KEY, at TEXT NOT NULL) WITHOUT ROWID`,
	`CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
	`CREATE TABLE overrides (
  provider TEXT NOT NULL, unit_key TEXT NOT NULL,
  muted_until TEXT NOT NULL DEFAULT '', mute_until_state INTEGER NOT NULL DEFAULT 0,
  hold INTEGER NOT NULL DEFAULT 0, taken_over INTEGER NOT NULL DEFAULT 0,
  muted_kind TEXT NOT NULL DEFAULT '', muted_waiting_on TEXT NOT NULL DEFAULT '',
  next_guidance TEXT NOT NULL DEFAULT '',
  tracked INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (provider, unit_key))`,
}

func plantImport(t *testing.T, dir string) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(dir, store.ImportFile))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range append(slices.Clone(exportedTables),
		`INSERT INTO pull_requests (owner, repo, number, title, author, state, reasons, ws_id, ws_agent, failing_checks, updated_at)
		 VALUES ('acme', 'api', 1, 'Add retries', 'ana', 'OPEN', '["author","tracked"]', 'w1', '["working"]', '[{"name":"lint"}]', '2026-10-20T12:00:00.000000000Z')`,
		`INSERT INTO pull_requests (owner, repo, number, title, author, state) VALUES ('acme', 'web', 2, 'Fix the header', 'bruno', 'MERGED')`,
		`INSERT INTO pr_seen (owner, repo, number, at) VALUES ('acme', 'api', 1, '2026-10-20T13:00:00.000000000Z')`,
		`INSERT INTO pr_details (owner, repo, number, version, read_at, body) VALUES ('acme', 'api', 1, 2, '2026-10-20T12:30:00.000000000Z', '{"Labels":["bug"]}')`,
		`INSERT INTO fetches (workspace_id, at) VALUES ('w1', '2026-10-20T11:00:00.000000000Z')`,
		`INSERT INTO meta (key, value) VALUES ('viewer:github', 'ana'), ('fast_sweep_at', '2026-10-20T12:00:00.000000000Z')`,
		`INSERT INTO overrides (provider, unit_key, muted_until, hold, taken_over, tracked, next_guidance)
		 VALUES ('github', 'acme/api#1', '2026-10-30T00:00:00.000000000Z', 1, 1, 1, 'look at the tests'),
		        ('github', 'acme/web#2', '2026-10-30T00:00:00.000000000Z', 1, 0, 0, ''),
		        ('slack', 'T000AAA/C000AAA/1.1', '', 0, 0, 1, '')`,
	) {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
}

func TestGitHubImportsTheExportedStore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	plantImport(t, dir)
	db, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	p, ok, err := db.PR(ctx, ref)
	if err != nil || !ok || p.Title != "Add retries" || p.Author != "ana" || !slices.Equal(p.Reasons, []string{"author", pr.ReasonTracked}) ||
		p.Workspace.ID != "w1" || !slices.Equal(p.Workspace.AgentStates, []string{"working"}) || len(p.FailingChecks) != 1 ||
		!p.LastSeenAt.Equal(time.Date(2026, 10, 20, 13, 0, 0, 0, time.UTC)) {
		t.Fatalf("imported pull request %+v %v %v", p, ok, err)
	}
	if all, err := db.ListPRs(ctx, false); err != nil || len(all) != 2 {
		t.Fatalf("imported %d pull requests, %v", len(all), err)
	}
	if d, err := db.Details(ctx, []pr.Ref{ref}); err != nil || d[ref].Version != 2 || string(d[ref].Body) != `{"Labels":["bug"]}` {
		t.Fatalf("imported detail %+v %v", d, err)
	}
	if at, err := db.LastFetch(ctx, "w1"); err != nil || !at.Equal(time.Date(2026, 10, 20, 11, 0, 0, 0, time.UTC)) {
		t.Fatalf("imported fetch %v %v", at, err)
	}
	if v, err := db.Viewer(ctx); err != nil || v != "ana" {
		t.Fatalf("imported viewer %q %v", v, err)
	}
	if v, _ := db.Meta(ctx, "fast_sweep_at"); v != "2026-10-20T12:00:00.000000000Z" {
		t.Fatalf("imported meta %q", v)
	}
	if o, err := db.Override(ctx, ref); err != nil || !o.Adopted || !o.Tracked {
		t.Fatalf("imported override %+v %v", o, err)
	}
	var n int
	if err := db.db.QueryRowContext(ctx, `SELECT count(*) FROM overrides`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("%d overrides imported, want the one GitHub row with plugin columns set: %v", n, err)
	}
	if got, _ := db.Tracked(ctx); !slices.Equal(got, []pr.Ref{ref}) {
		t.Fatalf("tracked %v", got)
	}
	if _, err := os.Stat(filepath.Join(dir, store.ImportFile)); !os.IsNotExist(err) {
		t.Errorf("%s is still in place: %v", store.ImportFile, err)
	}
	if _, err := os.Stat(filepath.Join(dir, store.Imported)); err != nil {
		t.Errorf("no %s: %v", store.Imported, err)
	}
	if err := db.SetViewer(ctx, "bruno"); err != nil {
		t.Fatal(err)
	}
	db.Close()

	plantImport(t, dir)
	again, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if v, err := again.Viewer(ctx); err != nil || v != "bruno" {
		t.Fatalf("a second start imported again: viewer %q %v", v, err)
	}
}

func TestAStoreWithNoExportStartsEmpty(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := open(t)
	if all, err := db.ListPRs(ctx, false); err != nil || len(all) != 0 {
		t.Fatalf("%d pull requests, %v", len(all), err)
	}
}
