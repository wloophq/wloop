package storetest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
)

func count(t *testing.T, f *Fixture, query string) int {
	t.Helper()
	var n int
	if err := f.DB.R().QueryRowContext(context.Background(), query).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

func TestSeededMatchesSizes(t *testing.T) {
	f := Seeded(t, Live)
	for _, c := range []struct {
		query string
		want  int
	}{
		{`SELECT count(*) FROM pull_requests`, Live.PRs},
		{`SELECT count(*) FROM pull_requests WHERE state = 'OPEN'`, Live.OpenPRs},
		{`SELECT count(*) FROM events`, Live.Events},
		{`SELECT count(*) FROM turns`, Live.Turns},
		{`SELECT count(DISTINCT provider || ':' || unit_key) FROM turns`, Live.TurnPRs},
		{`SELECT count(*) FROM sessions`, Live.Sessions},
		{`SELECT count(*) FROM sessions WHERE closed_at = ''`, Live.OpenSessions},
		{`SELECT count(*) FROM pull_requests WHERE ws_id != ''`, Live.LinkedPRs},
		{`SELECT count(*) FROM pull_requests WHERE ws_id != '' AND ws_agent != ''`, Live.AgentLinkedPRs},
		{`SELECT count(*) FROM brief_runs`, Live.BriefRuns},
	} {
		if got := count(t, f, c.query); got != c.want {
			t.Errorf("%s = %d, want %d", c.query, got, c.want)
		}
	}
	if len(f.Open) != Live.OpenPRs || len(f.Closed) != Live.PRs-Live.OpenPRs || len(f.WithTurns) != Live.TurnPRs || len(f.Handles) != Live.Sessions {
		t.Errorf("fixture lists: %d open, %d closed, %d with turns, %d handles", len(f.Open), len(f.Closed), len(f.WithTurns), len(f.Handles))
	}
	books, err := f.Turns.Books(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range f.WithTurns {
		if _, ok := books[u]; !ok {
			t.Errorf("WithTurns names %s, which has no book", u)
		}
	}
	open, err := f.Inventory.ListPRs(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	refs := make([]pr.Ref, len(open))
	for i, p := range open {
		refs[i] = p.Ref
	}
	slices.SortFunc(refs, compareRefs)
	if !slices.Equal(refs, f.Open) {
		t.Errorf("open pull requests %v, Open lists %v", refs, f.Open)
	}
	sessions, err := f.Turns.Sessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for i, s := range sessions {
		if s.ID != f.Handles[i] {
			t.Fatalf("open session %d is %s, Handles lists %s first", i, s.ID, f.Handles[i])
		}
	}
	mix, err := kindCounts(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	for kind, share := range EventMix {
		if want := share * float64(Live.Events); kind != dinv.KindReview && !slices.Contains(derived, kind) && float64(mix[kind]) < want-0.5 {
			t.Errorf("%d %s events, want at least %.0f", mix[kind], kind, want)
		}
	}
	t.Logf("events by kind: %v", mix)
}

func TestSeededHistoryIsCoherent(t *testing.T) {
	f := Seeded(t, Live)
	for _, c := range []struct {
		what, query string
		want        int
	}{
		{"opened events", `SELECT count(*) FROM events WHERE kind = 'opened'`, Live.PRs},
		{"pull requests opened once at creation", `SELECT count(*) FROM events e JOIN pull_requests p ON e.provider = 'github' AND e.unit_key = p.owner || '/' || p.repo || '#' || p.number
			WHERE e.kind = 'opened' AND e.at = p.created_at`, Live.PRs},
		{"merged events off a merge", `SELECT count(*) FROM events e JOIN pull_requests p ON e.provider = 'github' AND e.unit_key = p.owner || '/' || p.repo || '#' || p.number
			WHERE e.kind = 'merged' AND (p.state != 'MERGED' OR e.at != p.merged_at)`, 0},
		{"merged pull requests without a merged event", `SELECT count(*) FROM pull_requests p WHERE state = 'MERGED'
			AND NOT EXISTS (SELECT 1 FROM events e WHERE e.kind = 'merged' AND e.provider = 'github' AND e.unit_key = p.owner || '/' || p.repo || '#' || p.number)`, 0},
		{"closed events off a close", `SELECT count(*) FROM events e JOIN pull_requests p ON e.provider = 'github' AND e.unit_key = p.owner || '/' || p.repo || '#' || p.number
			WHERE e.kind = 'closed' AND (p.state != 'CLOSED' OR e.at != p.closed_at)`, 0},
		{"events outside their pull request's life", `SELECT count(*) FROM events e JOIN pull_requests p ON e.provider = 'github' AND e.unit_key = p.owner || '/' || p.repo || '#' || p.number
			WHERE e.at < p.created_at OR (p.closed_at != '' AND e.at > p.closed_at)`, 0},
		{"brief_ended events of no run", `SELECT count(*) FROM events e WHERE e.kind = 'brief_ended'
			AND NOT EXISTS (SELECT 1 FROM brief_runs r WHERE r.id = CAST(e.key AS INTEGER) AND e.provider = 'github' AND e.unit_key = r.owner || '/' || r.repo || '#' || r.number)`, 0},
		{"brief_ended events", `SELECT count(*) FROM events WHERE kind = 'brief_ended'`, Live.BriefRuns},
		{"session events on a pull request", `SELECT count(*) FROM events WHERE kind IN ('session_idle', 'session_active') AND provider = 'github'`, 0},
		{"turn_ended events without their record", `SELECT count(*) FROM events e WHERE e.kind = 'turn_ended'
			AND NOT EXISTS (SELECT 1 FROM turns t JOIN turn_records r ON r.turn_seq = t.seq
				WHERE e.key = t.turn_id || ':' || substr(r.hash, 1, 12))`, 0},
		{"ended turns without a record", `SELECT count(*) FROM turns t WHERE t.state IN ('done', 'settled', 'failed', 'unrecorded', 'asked')
			AND NOT EXISTS (SELECT 1 FROM turn_records r WHERE r.turn_seq = t.seq)`, 0},
		{"asked turns without an answer", `SELECT count(*) FROM turns t WHERE t.state = 'asked'
			AND NOT EXISTS (SELECT 1 FROM turn_answers a WHERE a.turn_seq = t.seq)`, 0},
		{"replaced turns without a successor", `SELECT count(*) FROM turns WHERE state = 'replaced' AND replaced_by = 0`, 0},
	} {
		if got := count(t, f, c.query); got != c.want {
			t.Errorf("%s: %d, want %d", c.what, got, c.want)
		}
	}
	if n := count(t, f, `SELECT count(*) FROM events WHERE kind IN ('session_idle', 'session_active') AND provider = 'session'`); n == 0 {
		t.Error("no session events on the unit-less rows")
	}
	if n := count(t, f, `SELECT count(*) FROM turn_records`); n == 0 {
		t.Error("no turn records")
	}
	for _, st := range tailStates {
		if n := count(t, f, `SELECT count(*) FROM turns WHERE state = '`+string(st)+`'`); n == 0 {
			t.Errorf("no %s turn", st)
		}
	}
}

// digest is a sha256 per table over its rows in column order.
func digest(t *testing.T, f *Fixture) map[string]string {
	t.Helper()
	ctx := context.Background()
	rows, err := f.DB.R().QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_stat%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, name)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, table := range tables {
		cols, err := f.DB.R().QueryContext(ctx, `SELECT * FROM "`+table+`" LIMIT 0`)
		if err != nil {
			t.Fatal(err)
		}
		names, err := cols.Columns()
		cols.Close()
		if err != nil {
			t.Fatal(err)
		}
		order := make([]string, len(names))
		for i := range names {
			order[i] = strconv.Itoa(i + 1)
		}
		rows, err := f.DB.R().QueryContext(ctx, `SELECT * FROM "`+table+`" ORDER BY `+strings.Join(order, ", "))
		if err != nil {
			t.Fatal(err)
		}
		h := sha256.New()
		vals := make([]any, len(names))
		ptrs := make([]any, len(names))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		for rows.Next() {
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			fmt.Fprintf(h, "%q\n", vals)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		out[table] = hex.EncodeToString(h.Sum(nil))
	}
	return out
}

func TestSeededIsDeterministic(t *testing.T) {
	var digests []map[string]string
	for i := range 2 {
		path := filepath.Join(t.TempDir(), fmt.Sprintf("seed-%d.db", i))
		if err := seedFile(path, Live); err != nil {
			t.Fatal(err)
		}
		f, err := open(path)
		if err != nil {
			t.Fatal(err)
		}
		digests = append(digests, digest(t, f))
		f.DB.Close()
	}
	digests = append(digests, digest(t, Seeded(t, Live)))
	if len(digests[0]) == 0 {
		t.Fatal("no tables")
	}
	for table, want := range digests[0] {
		for i, d := range digests[1:] {
			if d[table] != want {
				t.Errorf("table %s differs between seed 0 and %d", table, i+1)
			}
		}
	}
}

func TestSeededCopyIsIsolated(t *testing.T) {
	ctx := context.Background()
	a, b := Seeded(t, Live), Seeded(t, Live)
	ref := pr.Ref{Owner: "org-9", Repo: "repo-9", Number: 1}
	if err := a.Inventory.UpsertPR(ctx, dinv.PR{Ref: ref, State: "OPEN", LastSeenAt: a.Now}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := a.Inventory.PR(ctx, ref); err != nil || !ok {
		t.Fatalf("written copy: found %v, %v", ok, err)
	}
	if _, ok, err := b.Inventory.PR(ctx, ref); err != nil || ok {
		t.Fatalf("other copy: found %v, %v", ok, err)
	}
	if got := count(t, Seeded(t, Live), `SELECT count(*) FROM pull_requests`); got != Live.PRs {
		t.Fatalf("a later copy has %d pull requests, want %d", got, Live.PRs)
	}
}

// TestSeededTime budgets the fastest of several runs: under load most runs
// stall, and the fastest stays near the unloaded cost, so the budgets
// catch a tenfold regression without failing on a busy machine.
func TestSeededTime(t *testing.T) {
	if race {
		t.Skip("timings under the race detector are not budgeted")
	}
	first := fastest(2, func() {
		if _, err := build(Live); err != nil {
			t.Fatal(err)
		}
	})
	Seeded(t, Live)
	perCopy := fastest(30, func() { Seeded(t, Live) })
	t.Logf("first seed %v, copy %v", first, perCopy)
	if first > 5*time.Second {
		t.Errorf("first seed took %v, want under 5s", first)
	}
	if perCopy > 100*time.Millisecond {
		t.Errorf("a copy took %v, want under 100ms", perCopy)
	}
}

func fastest(n int, run func()) time.Duration {
	best := time.Duration(math.MaxInt64)
	for range n {
		start := time.Now()
		run()
		best = min(best, time.Since(start))
	}
	return best
}

func TestEmptyHasNoPullRequests(t *testing.T) {
	f := Empty(t)
	if got := count(t, f, `SELECT count(*) FROM pull_requests`); got != 0 {
		t.Fatalf("%d pull requests in an empty store", got)
	}
}
