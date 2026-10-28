package store

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/plugins/github/pr"
)

func TestUpsertKeepsReasonsUnionAndRequests(t *testing.T) {
	t.Parallel()
	db := open(t)
	ctx := context.Background()
	p := pr.PR{Ref: ref, Title: "one", Author: "ana", State: "OPEN", Reasons: []string{"author"}, ReviewRequests: []string{"me", "team:acme/search"}}
	upsert(t, db, p)
	p.Title = "two"
	p.Reasons = []string{"review-requested"}
	p.ReviewRequests = []string{"carla"}
	upsert(t, db, p)
	got, ok, err := db.PR(ctx, ref)
	if err != nil || !ok {
		t.Fatalf("read: %v %v", ok, err)
	}
	if got.Title != "two" || !slices.Equal(got.Reasons, []string{"author", "review-requested"}) {
		t.Fatalf("got %+v", got)
	}
	if !slices.Equal(got.ReviewRequests, []string{"carla"}) {
		t.Fatalf("requests are a snapshot, not a union: %v", got.ReviewRequests)
	}
}

func TestFailingChecksRoundTrip(t *testing.T) {
	t.Parallel()
	db := open(t)
	ctx := context.Background()
	checks := []pr.FailingCheck{
		{Name: "Build and test", URL: "https://github.com/acme/api/actions/runs/1/job/2"},
		{Name: "Preview site", Summary: "the preview build failed"},
	}
	upsert(t, db, pr.PR{Ref: ref, State: "OPEN", ChecksState: "FAILURE", FailingChecks: checks})
	got, _, err := db.PR(ctx, ref)
	if err != nil || !slices.Equal(got.FailingChecks, checks) {
		t.Fatalf("%+v %v", got.FailingChecks, err)
	}
	upsert(t, db, pr.PR{Ref: ref, State: "OPEN", ChecksState: "SUCCESS"})
	if got, _, err = db.PR(ctx, ref); err != nil || got.FailingChecks != nil {
		t.Fatalf("passing checks kept %+v %v", got.FailingChecks, err)
	}
}

func TestUnreadableFailingChecksDoNotHideThePullRequest(t *testing.T) {
	t.Parallel()
	db := open(t)
	ctx := context.Background()
	upsert(t, db, pr.PR{Ref: ref, State: "OPEN", ChecksState: "FAILURE", FailingChecks: []pr.FailingCheck{{Name: "lint"}}})
	exec(t, db, `UPDATE pull_requests SET failing_checks='{bad'`)
	got, ok, err := db.PR(ctx, ref)
	if err != nil || !ok || got.FailingChecks != nil || got.ChecksState != "FAILURE" {
		t.Fatalf("%+v %v %v", got, ok, err)
	}
	if prs, err := db.ListPRs(ctx, true); err != nil || len(prs) != 1 {
		t.Fatalf("list: %d %v", len(prs), err)
	}
}

func TestMergeableKeepsAKnownValueAndWhenItTurnedConflicting(t *testing.T) {
	t.Parallel()
	db := open(t)
	ctx := context.Background()
	t0 := time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC)
	step := func(mergeable string, at time.Time, wantValue string, wantAt time.Time) {
		t.Helper()
		upsert(t, db, pr.PR{Ref: ref, State: "OPEN", Mergeable: mergeable, LastSeenAt: at})
		p, _, err := db.PR(ctx, ref)
		if err != nil || p.Mergeable != wantValue || !p.MergeableAt.Equal(wantAt) {
			t.Fatalf("after %q at %v: %q at %v (%v), want %q at %v", mergeable, at, p.Mergeable, p.MergeableAt, err, wantValue, wantAt)
		}
	}
	step("CONFLICTING", t0, "CONFLICTING", t0)
	step("UNKNOWN", t0.Add(time.Hour), "CONFLICTING", t0)
	step("", t0.Add(2*time.Hour), "CONFLICTING", t0)
	step("CONFLICTING", t0.Add(3*time.Hour), "CONFLICTING", t0)
	step("MERGEABLE", t0.Add(4*time.Hour), "MERGEABLE", time.Time{})
	step("CONFLICTING", t0.Add(5*time.Hour), "CONFLICTING", t0.Add(5*time.Hour))
}

func TestListOpenOnlyAndByBranch(t *testing.T) {
	t.Parallel()
	db := open(t)
	ctx := context.Background()
	upsert(t, db, pr.PR{Ref: ref, State: "OPEN", HeadBranch: "feat/x"})
	upsert(t, db, pr.PR{Ref: pr.Ref{Owner: "acme", Repo: "api", Number: 2}, State: "MERGED", HeadBranch: "feat/x"})
	open, err := db.ListPRs(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	all, err := db.ListPRs(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 1 || len(all) != 2 {
		t.Fatalf("open %d all %d", len(open), len(all))
	}
}

func TestWorkspaceAndMeta(t *testing.T) {
	t.Parallel()
	db := open(t)
	ctx := context.Background()
	upsert(t, db, pr.PR{Ref: ref, State: "OPEN"})
	if err := db.SetWorkspace(ctx, ref, pr.Workspace{ID: "w1", DisplayName: "REVIEW x", Status: "reviewing", IsReview: true, AgentStates: []string{"working"}, Comment: "r2 held: approve?"}); err != nil {
		t.Fatal(err)
	}
	got, _, err := db.PR(ctx, ref)
	if err != nil || got.Workspace.ID != "w1" || !slices.Equal(got.Workspace.AgentStates, []string{"working"}) || got.Workspace.Comment != "r2 held: approve?" {
		t.Fatalf("workspace: %+v %v", got.Workspace, err)
	}
	if err := db.ReplaceWorkspaces(ctx, nil); err != nil {
		t.Fatal(err)
	}
	got, _, err = db.PR(ctx, ref)
	if err != nil || got.Workspace.ID != "" || got.Workspace.AgentStates != nil || got.Workspace.Comment != "" {
		t.Fatalf("workspace not cleared: %+v %v", got.Workspace, err)
	}
	if err := db.SetMeta(ctx, "fast_sweep_at", "2026-10-01T12:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if v, err := db.Meta(ctx, "fast_sweep_at"); err != nil || v != "2026-10-01T12:00:00Z" {
		t.Fatalf("meta: %q %v", v, err)
	}
	if v, err := db.Meta(ctx, "absent"); err != nil || v != "" {
		t.Fatalf("absent meta: %q %v", v, err)
	}
}

func TestUnknownAgentStatesRoundTrip(t *testing.T) {
	t.Parallel()
	db := open(t)
	ctx := context.Background()
	upsert(t, db, pr.PR{Ref: ref, State: "OPEN"})
	states := []string{"working", "idle"}
	if err := db.SetWorkspace(ctx, ref, pr.Workspace{ID: "w1", AgentStates: states}); err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := db.db.QueryRowContext(ctx, `SELECT ws_agent FROM pull_requests WHERE number=?`, ref.Number).Scan(&stored); err != nil || stored != `["working","idle"]` {
		t.Fatalf("stored %q, %v", stored, err)
	}
	got, _, err := db.PR(ctx, ref)
	if err != nil || !slices.Equal(got.Workspace.AgentStates, states) {
		t.Fatalf("agent states %v, %v", got.Workspace.AgentStates, err)
	}
}

func TestListColumnsKeepCommas(t *testing.T) {
	t.Parallel()
	db := open(t)
	ctx := context.Background()
	upsert(t, db, pr.PR{Ref: ref, State: "OPEN", Reasons: []string{"a,b"}, ReviewRequests: []string{"x, y"}})
	states := []string{"waiting, input", "working"}
	if err := db.SetWorkspace(ctx, ref, pr.Workspace{ID: "w1", AgentStates: states}); err != nil {
		t.Fatal(err)
	}
	got, _, err := db.PR(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.Workspace.AgentStates, states) {
		t.Fatalf("agent states: %q", got.Workspace.AgentStates)
	}
	if !slices.Equal(got.Reasons, []string{"a,b"}) || !slices.Equal(got.ReviewRequests, []string{"x, y"}) {
		t.Fatalf("lists: %q %q", got.Reasons, got.ReviewRequests)
	}
	upsert(t, db, pr.PR{Ref: ref, State: "OPEN", Reasons: []string{"c"}})
	got, _, err = db.PR(ctx, ref)
	if err != nil || strings.Join(got.Reasons, "|") != "a,b|c" {
		t.Fatalf("reasons union: %q %v", got.Reasons, err)
	}
}

func TestUnparsableTimestampIsAnError(t *testing.T) {
	t.Parallel()
	db := open(t)
	ctx := context.Background()
	upsert(t, db, pr.PR{Ref: ref, State: "OPEN", UpdatedAt: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)})
	exec(t, db, `UPDATE pull_requests SET updated_at='2026-10-01 12:00'`)
	if _, _, err := db.PR(ctx, ref); err == nil {
		t.Error("PR: want error for unparsable updated_at")
	}
	if _, err := db.ListPRs(ctx, false); err == nil {
		t.Error("ListPRs: want error for unparsable updated_at")
	}
}

func TestListPRsOrderIsStable(t *testing.T) {
	t.Parallel()
	db := open(t)
	ctx := context.Background()
	later := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	for _, r := range []pr.Ref{
		{Owner: "acme", Repo: "api", Number: 1},
		{Owner: "acme", Repo: "web", Number: 5},
		{Owner: "acme", Repo: "api", Number: 3},
		{Owner: "beta", Repo: "api", Number: 9},
	} {
		upsert(t, db, pr.PR{Ref: r, State: "OPEN"})
	}
	upsert(t, db, pr.PR{Ref: pr.Ref{Owner: "zeta", Repo: "z", Number: 1}, State: "OPEN", UpdatedAt: later})
	prs, err := db.ListPRs(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, p := range prs {
		got = append(got, p.Ref.String())
	}
	want := "zeta/z#1 acme/api#3 acme/api#1 acme/web#5 beta/api#9"
	if strings.Join(got, " ") != want {
		t.Fatalf("order: %v, want %s", got, want)
	}
}

func TestReplaceWorkspacesSwapsEveryLink(t *testing.T) {
	t.Parallel()
	db := open(t)
	ctx := context.Background()
	other := pr.Ref{Owner: "acme", Repo: "api", Number: 2}
	upsert(t, db, pr.PR{Ref: ref, State: "OPEN"})
	upsert(t, db, pr.PR{Ref: other, State: "OPEN"})
	if err := db.SetWorkspace(ctx, ref, pr.Workspace{ID: "w1", IsReview: true}); err != nil {
		t.Fatal(err)
	}
	if err := db.ReplaceWorkspaces(ctx, map[pr.Ref]pr.Workspace{other: {ID: "w2", AgentStates: []string{"working"}}}); err != nil {
		t.Fatal(err)
	}
	a, _, _ := db.PR(ctx, ref)
	b, _, _ := db.PR(ctx, other)
	if a.Workspace.ID != "" || a.Workspace.IsReview {
		t.Fatalf("stale link kept: %+v", a.Workspace)
	}
	if b.Workspace.ID != "w2" || len(b.Workspace.AgentStates) != 1 {
		t.Fatalf("new link missing: %+v", b.Workspace)
	}
}

func TestReplaceWorkspacesUnchangedWritesNothing(t *testing.T) {
	t.Parallel()
	db := open(t)
	ctx := context.Background()
	links := map[pr.Ref]pr.Workspace{}
	for i := 1; i <= 3; i++ {
		r := pr.Ref{Owner: "acme", Repo: "api", Number: i}
		upsert(t, db, openPR(r, "author"))
		links[r] = pr.Workspace{ID: fmt.Sprint("w", i), Status: "working"}
	}
	if err := db.ReplaceWorkspaces(ctx, links); err != nil {
		t.Fatal(err)
	}
	writes := rowWrites(t, db, "pull_requests")
	log := listen(db)
	if got := log.during(t, func() error { return db.ReplaceWorkspaces(ctx, links) }); len(got) != 0 {
		t.Fatalf("unchanged links heard %+v", got)
	}
	if n := writes(); n != 0 {
		t.Fatalf("%d pull_requests writes, want 0", n)
	}
	moved := map[pr.Ref]pr.Workspace{}
	for r, ws := range links {
		moved[r] = ws
	}
	moved[ref] = pr.Workspace{ID: "w9"}
	got := log.during(t, func() error { return db.ReplaceWorkspaces(ctx, moved) })
	if n := writes(); n != 1 || len(got) != 1 || !slices.Equal(got[0].Refs, []pr.Ref{ref}) {
		t.Fatalf("one moved link: %d writes, heard %+v", n, got)
	}
}

func TestPRKeepsBodyEditAndChangedFiles(t *testing.T) {
	t.Parallel()
	db := open(t)
	ctx := context.Background()
	edited := time.Date(2026, 10, 5, 8, 0, 0, 0, time.UTC)
	upsert(t, db, pr.PR{Ref: ref, State: "OPEN", BodyEditedAt: edited, ChangedFiles: 4})
	got, _, err := db.PR(ctx, ref)
	if err != nil || !got.BodyEditedAt.Equal(edited) || got.ChangedFiles != 4 {
		t.Fatalf("%+v %v", got, err)
	}
	edited = edited.Add(time.Hour)
	upsert(t, db, pr.PR{Ref: ref, State: "OPEN", BodyEditedAt: edited, ChangedFiles: 9})
	if got, _, err = db.PR(ctx, ref); err != nil || !got.BodyEditedAt.Equal(edited) || got.ChangedFiles != 9 {
		t.Fatalf("after a push: %+v %v", got, err)
	}
}

func TestPRsChunks(t *testing.T) {
	t.Parallel()
	db := open(t)
	ctx := context.Background()
	refs := make([]pr.Ref, 1200)
	var stored []pr.PR
	for i := range refs {
		refs[i] = pr.Ref{Owner: "acme", Repo: "api", Number: i + 1}
		if i%2 == 0 {
			stored = append(stored, pr.PR{Ref: refs[i], State: pr.StateOpen, Title: fmt.Sprint("t", i)})
		}
	}
	if err := db.SavePRs(ctx, stored, noteAt); err != nil {
		t.Fatal(err)
	}
	got, err := db.PRs(ctx, refs)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 600 || got[refs[10]].Title != "t10" {
		t.Fatalf("%d pull requests, #11 %+v", len(got), got[refs[10]])
	}
	if _, ok := got[refs[1]]; ok {
		t.Fatal("an unstored pull request was returned")
	}
	if none, err := db.PRs(ctx, nil); err != nil || len(none) != 0 {
		t.Fatalf("no refs: %v %v", none, err)
	}
}

func TestAnUnchangedPullRequestIsNotRewrittenNorHeard(t *testing.T) {
	t.Parallel()
	db := open(t)
	ctx := context.Background()
	p := openPR(ref, "author")
	upsert(t, db, p)
	writes := rowWrites(t, db, "pull_requests")
	log := listen(db)
	if got := log.during(t, func() error { return db.UpsertPR(ctx, p) }); len(got) != 0 {
		t.Fatalf("the same row heard %+v", got)
	}
	if n := writes(); n != 0 {
		t.Fatalf("%d rewrites of the same row", n)
	}
	p.Title = "renamed"
	got := log.during(t, func() error { return db.UpsertPR(ctx, p) })
	if len(got) != 1 || !slices.Equal(got[0].Refs, []pr.Ref{ref}) {
		t.Fatalf("a new title heard %+v", got)
	}
}
