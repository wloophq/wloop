package sqlite

import (
	"slices"
	"testing"
)

func TestV7BriefsMovesTagsIntoRecords(t *testing.T) {
	t.Parallel()
	path := v6Store(t,
		`INSERT INTO brief_runs (id, trigger, version, attempt_of, started_at, ended_at, outcome, brief, tags, provider, unit_key) VALUES
			(1, 'background', 'b1', 'a', '2026-10-01T09:00:00.000000000Z', '2026-10-01T09:01:00.000000000Z', 'ok',
				'{"goal":"Old","risk":"low","tags":["lang:go"]}', '["lang:go"]', 'github', 'acme/api#7'),
			(2, 'refresh', 'b1', 'a', '2026-10-02T09:00:00.000000000Z', '2026-10-02T09:01:00.000000000Z', 'ok',
				'{"goal":"Export the ledger","risk":"high","size":"m","tags":["lang:go"]}', '["lang:go"]', 'github', 'acme/api#7'),
			(3, 'extraction', 'x1', 'c', '2026-10-03T09:00:00.000000000Z', '2026-10-03T09:00:30.000000000Z', 'ok',
				'{"goal":"Answer Ana","kind":"question","tags":[]}', '[]', 'slack', 'T1/C1/1.1'),
			(4, 'background', 'b1', 'a', '2026-10-03T09:00:00.000000000Z', '', 'failed', '', '', 'github', 'acme/api#8')`,
		`INSERT INTO unit_briefs (provider, unit_key, run, last_run) VALUES ('github', 'acme/api#7', 2, 2), ('slack', 'T1/C1/1.1', 3, 3),
			('github', 'acme/api#8', 0, 4)`,
		`INSERT INTO unit_tags (provider, unit_key, key, value, run) VALUES ('github', 'acme/api#7', 'lang', 'go', 2),
			('github', 'acme/api#7', 'area', 'api', 2)`,
	)
	migrateV6(t, path)
	raw := rawDB(t, path)
	got := strings1(t, raw, `SELECT provider || ' ' || unit_key || ' ' || key || ' ' || actor || ' ' || at || ' ' || data FROM events
		WHERE kind = 'record' ORDER BY provider, unit_key`)
	want := []string{
		`github acme/api#7 brief agent 2026-10-02T09:01:00.000000000Z {"goal":"Export the ledger","risk":"high","size":"m","tags":["area:api","lang:go"]}`,
		`slack T1/C1/1.1 brief plugin 2026-10-03T09:00:30.000000000Z {"goal":"Answer Ana","kind":"question","tags":[]}`,
	}
	if !slices.Equal(got, want) {
		t.Fatalf("records\n%q\nwant\n%q", got, want)
	}
	if n := strings1(t, raw, `SELECT name FROM sqlite_master WHERE name LIKE 'unit_tags%'`); len(n) != 0 {
		t.Fatalf("unit_tags stays: %q", n)
	}
	if runs := strings1(t, raw, `SELECT id FROM brief_runs ORDER BY id`); len(runs) != 4 {
		t.Fatalf("the run log %q", runs)
	}
}
