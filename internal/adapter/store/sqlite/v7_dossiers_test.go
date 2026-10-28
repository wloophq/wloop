package sqlite

import (
	"context"
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/domain/dossier"
)

const (
	hexShip  = "00000000000000d1"
	hexDrop  = "00000000000000d2"
	hexPull  = "00000000000000d3"
	hexEnded = "00000000000000d4"
)

// v6DossiersFixture is a version 6 store with a project whose dossier is its slug's, a project whose archived
// slug dossier meets the dossier its entry already had, a pull request's dossier held by an open entry and a
// done one, and a thread's dossier no entry holds.
func v6DossiersFixture(t *testing.T) string {
	t.Helper()
	return v6Store(t,
		`INSERT INTO entries (id, kind, state, origin, created, updated, rev) VALUES
			('e-`+hexShip+`', 'project', 'next', 'user', '2026-10-01T00:00:00Z', 't', 1),
			('e-`+hexDrop+`', 'project', 'next', 'user', '2026-10-01T00:00:00Z', 't', 2),
			('e-`+hexPull+`', 'action', 'next', 'derived', '2026-10-03T00:00:00Z', 't', 3),
			('e-`+hexEnded+`', 'action', 'done', 'derived', '2026-10-02T00:00:00Z', 't', 4)`,
		`INSERT INTO projects (entry_id, slug) VALUES ('e-`+hexShip+`', 'ship-it'), ('e-`+hexDrop+`', 'drop-it')`,
		`INSERT INTO entry_units (entry_id, provider, unit_key, ord) VALUES ('e-`+hexPull+`', 'github', 'acme/api#7', 0),
			('e-`+hexEnded+`', 'github', 'acme/api#7', 0)`,
		`INSERT INTO dossiers (owner, rel_path, status, files, archived_at, purge_after) VALUES
			('project/ship-it', 'project/ship-it#', 'active', 3, '', ''),
			('project/drop-it', 'project/drop-it#', 'archived', 2, '2026-10-05T00:00:00Z', '2027-11-09T00:00:00Z'),
			('entry/e-`+hexDrop+`', 'entry/e-`+hexDrop+`#', 'active', 1, '', ''),
			('github/acme/api#7', 'github/acme/api%237#', 'active', 4, '', ''),
			('slack/T1/C1/1.1', 'slack/T1/C1/1.1#', 'active', 1, '', '')`,
	)
}

func TestProjectDossiersMoveToTheirTask(t *testing.T) {
	t.Parallel()
	path := v6DossiersFixture(t)
	db := migrateV6(t, path)
	raw := rawDB(t, path)
	ship, drop, pull := "t-"+hexShip, "t-"+hexDrop, "t-"+hexPull
	want := []string{
		"merged/project%2Fdrop-it/task/" + drop + " task/" + drop + "#/merged/project%2Fdrop-it archived 2 2027-11-09T00:00:00Z",
		"slack/T1/C1/1.1 slack/T1/C1/1.1# active 1 ",
		"task/" + ship + " task/" + ship + "# active 3 ",
		"task/" + drop + " task/" + drop + "# active 1 ",
		"task/" + pull + " task/" + pull + "# active 4 ",
	}
	got := strings1(t, raw, `SELECT owner || ' ' || rel_path || ' ' || status || ' ' || files || ' ' || purge_after FROM dossiers ORDER BY owner`)
	if !slices.Equal(got, want) {
		t.Errorf("dossiers\n%q\nwant\n%q", got, want)
	}
	wantMoves := []string{
		"project/drop-it# > task/" + drop + "#/merged/project%2Fdrop-it",
		"project/ship-it# > task/" + ship + "#",
		"github/acme/api%237# > task/" + pull + "#",
	}
	if got := strings1(t, raw, `SELECT from_path || ' > ' || to_path FROM dossier_moves ORDER BY seq`); !slices.Equal(got, wantMoves) {
		t.Errorf("moves\n%q\nwant\n%q", got, wantMoves)
	}
	if got := strings1(t, raw, `SELECT name FROM sqlite_schema WHERE name LIKE 'projects%'`); len(got) > 0 {
		t.Errorf("projects is left: %q", got)
	}
	all, err := db.Dossiers().List(context.Background())
	if err != nil || len(all) != len(want) {
		t.Fatalf("the store reads %d dossiers, %v", len(all), err)
	}
	for _, d := range all {
		if rel, err := dossier.Rel(d.Owner); err != nil || rel != d.RelPath {
			t.Errorf("%s lives at %q, its owner's directory is %q (%v)", d.Owner, d.RelPath, rel, err)
		}
	}
}

func TestAMigratedEndedTasksDossierGetsNoClosingCall(t *testing.T) {
	t.Parallel()
	path := v6Store(t,
		`INSERT INTO entries (id, kind, state, origin, created, updated, rev) VALUES
			('e-`+hexEnded+`', 'capture', 'done', 'user', '2026-10-01T00:00:00Z', '2026-10-04T09:00:00Z', 1),
			('e-`+hexPull+`', 'capture', 'next', 'user', '2026-10-01T00:00:00Z', '2026-10-04T09:00:00Z', 2)`,
		`INSERT INTO dossiers (owner, rel_path, status) VALUES
			('entry/e-`+hexEnded+`', 'entry/e-`+hexEnded+`#', 'active'), ('entry/e-`+hexPull+`', 'entry/e-`+hexPull+`#', 'active')`,
	)
	migrateV6(t, path)
	want := []string{"task/t-" + hexPull + " active ", "task/t-" + hexEnded + " resolved 2026-10-04T09:00:00Z"}
	if got := strings1(t, rawDB(t, path), `SELECT owner || ' ' || status || ' ' || resolved_at FROM dossiers ORDER BY owner`); !slices.Equal(got, want) {
		t.Fatalf("dossiers %q, want %q", got, want)
	}
}
