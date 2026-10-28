package brief

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/core/intake"
	"github.com/wloophq/wloop/internal/core/turns"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/github"
)

// TestAMigratedPRBriefIsNotDueAgain briefs two pull requests under v3, one
// ok and one given up while a first turn wanted it, migrates the store to v4
// and runs the queue on it: neither is briefed again, the ok brief is ready
// and the given-up one still answers that it gave up.
func TestAMigratedPRBriefIsNotDueAgain(t *testing.T) {
	ctx := context.Background()
	version := Version(DefaultTaxonomy)
	edited := t0.Add(-48 * time.Hour)
	okRef, gaveRef := github.Ref{Owner: "acme", Repo: "api", Number: 1}, github.Ref{Owner: "acme", Repo: "api", Number: 2}
	pr := func(ref github.Ref) dinv.PR {
		return dinv.PR{Ref: ref, State: "OPEN", HeadOid: "h1", BodyEditedAt: edited, ChangedFiles: 3, Reasons: []string{"repo"},
			UpdatedAt: t0.Add(-time.Hour)}
	}
	ts := func(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000000000Z") }
	attempt := attemptOf(pr(okRef), version)
	db := sqlitetest.Migrated(t, filepath.Join(t.TempDir(), "state", "store.db"), 3,
		fmt.Sprintf(`INSERT INTO brief_runs (id, owner, repo, number, trigger, version, attempt_of, head_oid, body_edited_at, changed_files,
			started_at, ended_at, outcome, brief, files, tags)
			VALUES (1, 'acme', 'api', 1, 'background', '%s', '%s', 'h1', '%s', 3, '%s', '%s', 'ok',
			'{"goal":"g","context":"","kind":"fix","size":"s","risk":"low","risk_why":"","open_questions":[],"tags":[]}', '["a.go","b.go","c.go"]', '')`,
			version, attempt, ts(edited), ts(t0.Add(-24*time.Hour)), ts(t0.Add(-24*time.Hour))),
		fmt.Sprintf(`INSERT INTO brief_runs (id, owner, repo, number, trigger, version, attempt_of, head_oid, body_edited_at, changed_files,
			started_at, ended_at, outcome, error)
			VALUES (2, 'acme', 'api', 2, 'wanted', '%s', '%s', 'h1', '%s', 3, '%s', '%s', 'failed', 'boom')`,
			version, attempt, ts(edited), ts(t0.Add(-2*time.Hour)), ts(t0.Add(-2*time.Hour))),
		`INSERT INTO pr_briefs (owner, repo, number, run, last_run) VALUES ('acme', 'api', 1, 1, 1)`,
		fmt.Sprintf(`INSERT INTO pr_briefs (owner, repo, number, failures, attempt_of, last_run, wanted_at, gave_up_at, gave_up_reason)
			VALUES ('acme', 'api', 2, 3, '%s', 2, '%s', '%s', 'gave up after 3 attempts')`, attempt, ts(t0.Add(-3*time.Hour)), ts(t0.Add(-2*time.Hour))),
	)
	st := inventory.New(db)
	for _, ref := range []github.Ref{okRef, gaveRef} {
		if err := st.UpsertPR(ctx, pr(ref)); err != nil {
			t.Fatal(err)
		}
	}
	run := &fakeRunner{fail: map[string]error{}}
	q := New(Deps{Store: st, Source: &fakeSource{fail: map[string]error{}}, Runner: run, Taxonomy: func(github.Ref) string { return DefaultTaxonomy },
		Home: func(github.Ref) string { return "" }, Tier: turns.TierFast, MaxDiffBytes: 1000, Retry: time.Hour, Enabled: true,
		Now: func() time.Time { return t0 }, Log: func(string, ...any) {}})
	for range 2 {
		if _, err := q.Run(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := q.scan(ctx); err != nil {
		t.Fatal(err)
	}
	q.wg.Wait()
	if run.calls != 0 {
		t.Fatalf("the migrated store briefed %d pull requests again", run.calls)
	}
	if rd, err := q.peekPR(ctx, okRef, true); err != nil || rd.State != Ready || rd.Brief == nil || rd.Brief.Goal != "g" {
		t.Errorf("the ok brief reads %+v, %v", rd, err)
	}
	if rd, err := q.peekPR(ctx, gaveRef, true); err != nil || rd.State != Without || rd.Cause != intake.CauseGaveUp || rd.Attempt != 3 {
		t.Errorf("the given-up brief reads %+v, %v", rd, err)
	}
	if run.calls != 0 {
		t.Fatalf("peeking briefed %d pull requests", run.calls)
	}
}

// TestAnAttemptStoredInVersion6StillMatchesAfterTheUpgrade keeps a failed
// attempt of a pull request in a version 6 store, its attempt_of written as
// version 6 wrote it, migrates the store and runs the queue inside the
// attempt's backoff: the attempt is the same one, so nothing is briefed
// again, which would be a paid call.
func TestAnAttemptStoredInVersion6StillMatchesAfterTheUpgrade(t *testing.T) {
	ctx := context.Background()
	version := Version(DefaultTaxonomy)
	edited := t0.Add(-48 * time.Hour)
	ref := github.Ref{Owner: "acme", Repo: "api", Number: 2}
	ts := func(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000000000Z") }
	stored := fmt.Sprintf("%s %s %s %d", version, "h1", edited.UTC().Format(time.RFC3339), 3)
	attempted := t0.Add(-10 * time.Minute)
	db := sqlitetest.Migrated(t, filepath.Join(t.TempDir(), "state", "store.db"), 6,
		fmt.Sprintf(`INSERT INTO brief_runs (id, provider, unit_key, intent, trigger, version, attempt_of, revision, input, started_at, ended_at, outcome, error)
			VALUES (1, 'github', 'acme/api#2', 'brief', 'background', '%s', '%s', 'h1 %s', '{"changed_files":3}', '%s', '%s', 'failed', 'boom')`,
			version, stored, ts(edited), ts(attempted), ts(attempted)),
		fmt.Sprintf(`INSERT INTO unit_briefs (provider, unit_key, failures, attempt_of, last_run, retry_at)
			VALUES ('github', 'acme/api#2', 1, '%s', 1, '%s')`, stored, ts(attempted.Add(time.Hour))),
	)
	st := inventory.New(db)
	pr := dinv.PR{Ref: ref, State: "OPEN", HeadOid: "h1", BodyEditedAt: edited, ChangedFiles: 3, Reasons: []string{"repo"}, UpdatedAt: t0.Add(-time.Hour)}
	if err := st.UpsertPR(ctx, pr); err != nil {
		t.Fatal(err)
	}
	rec, ok, err := st.BriefRecord(ctx, ref)
	if err != nil || !ok || rec.AttemptOf != stored {
		t.Fatalf("the upgraded record %+v %v %v, want attempt_of %q", rec, ok, err, stored)
	}
	if got := AttemptKey(version, AtOf(pr)); got != stored {
		t.Fatalf("the attempt now is %q, the stored one %q", got, stored)
	}
	run := &fakeRunner{fail: map[string]error{}}
	q := New(Deps{Store: st, Source: &fakeSource{fail: map[string]error{}}, Runner: run, Taxonomy: func(github.Ref) string { return DefaultTaxonomy },
		Home: func(github.Ref) string { return "" }, Tier: turns.TierFast, MaxDiffBytes: 1000, Retry: time.Hour, Enabled: true,
		Now: func() time.Time { return t0 }, Log: func(string, ...any) {}})
	for range 2 {
		if _, err := q.Run(ctx); err != nil {
			t.Fatal(err)
		}
	}
	q.wg.Wait()
	if run.calls != 0 {
		t.Fatalf("the upgrade made the failed attempt a new one: %d briefs", run.calls)
	}
}
