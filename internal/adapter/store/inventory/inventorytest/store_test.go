package inventorytest

import (
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/core/port"
	dgh "github.com/wloophq/wloop/internal/domain/github"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/meter/metertest"
)

var ref = github.Ref{Owner: "acme", Repo: "api", Number: 1}

func open(t *testing.T) *Store {
	t.Helper()
	return openAt(t, filepath.Join(t.TempDir(), "nested", "inv.db"))
}

func openAt(t *testing.T, path string) *Store {
	t.Helper()
	db, err := sqlite.Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return Of(inventory.New(db))
}

func upsert(t *testing.T, s *Store, pr dinv.PR) {
	t.Helper()
	if err := s.UpsertPR(context.Background(), pr); err != nil {
		t.Fatal(err)
	}
}

func TestFailingChecksRoundTrip(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	checks := []dinv.FailingCheck{
		{Name: "Build and test", URL: "https://github.com/acme/api/actions/runs/1/job/2"},
		{Name: "Preview site", Summary: "Build ou deploy do preview falhou"},
	}
	upsert(t, s, dinv.PR{Ref: ref, State: "OPEN", ChecksState: "FAILURE", FailingChecks: checks})
	got, _, err := s.PR(ctx, ref)
	if err != nil || !slices.Equal(got.FailingChecks, checks) {
		t.Fatalf("%+v %v", got.FailingChecks, err)
	}
	upsert(t, s, dinv.PR{Ref: ref, State: "OPEN", ChecksState: "SUCCESS"})
	if got, _, err = s.PR(ctx, ref); err != nil || got.FailingChecks != nil {
		t.Fatalf("passing checks kept %+v %v", got.FailingChecks, err)
	}
}

func TestUnreadableFailingChecksDoNotHideThePullRequest(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	upsert(t, s, dinv.PR{Ref: ref, State: "OPEN", ChecksState: "FAILURE", FailingChecks: []dinv.FailingCheck{{Name: "lint"}}})
	sqlitetest.Exec(t, s.db, `UPDATE pull_requests SET failing_checks='{bad'`)
	got, ok, err := s.PR(ctx, ref)
	if err != nil || !ok || got.FailingChecks != nil || got.ChecksState != "FAILURE" {
		t.Fatalf("%+v %v %v", got, ok, err)
	}
	if prs, err := s.ListPRs(ctx, true); err != nil || len(prs) != 1 {
		t.Fatalf("list: %d %v", len(prs), err)
	}
}

func TestAppendEventsIsIdempotent(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	evs := []dinv.Event{
		{PR: ref, Kind: dinv.KindPush, Key: "abc", Actor: "ana", At: at},
		{PR: ref, Kind: dinv.KindReview, Key: "r1", Actor: "me", At: at.Add(time.Hour), Data: "APPROVED:abc"},
		{PR: ref, Kind: dinv.KindRoundSent, Actor: "me", At: at.Add(2 * time.Hour)},
	}
	n, err := s.AppendEvents(ctx, evs)
	if err != nil || n != 3 {
		t.Fatalf("first append: %d %v", n, err)
	}
	evs[0].At = at.Add(5 * time.Hour)
	evs[1].At = at.Add(5 * time.Hour)
	n, err = s.AppendEvents(ctx, evs)
	if err != nil || n != 0 {
		t.Fatalf("second append (keyed events seen later): %d %v", n, err)
	}
	n, err = s.AppendEvents(ctx, []dinv.Event{{PR: ref, Kind: dinv.KindRoundSent, Actor: "me", At: at.Add(3 * time.Hour)}})
	if err != nil || n != 1 {
		t.Fatalf("unkeyed event at a new time: %d %v", n, err)
	}
	got, err := s.Events(ctx, ref)
	if err != nil || len(got) != 4 || got[0].Kind != dinv.KindPush || got[1].Data != "APPROVED:abc" {
		t.Fatalf("events: %+v %v", got, err)
	}
}

func TestEventsOrderBySubSecondTime(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	if _, err := s.AppendEvents(ctx, []dinv.Event{
		{PR: ref, Kind: dinv.KindComment, Key: "b", At: at.Add(500 * time.Millisecond)},
		{PR: ref, Kind: dinv.KindComment, Key: "a", At: at},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Events(ctx, ref)
	if err != nil || len(got) != 2 {
		t.Fatalf("events: %+v %v", got, err)
	}
	if got[0].Key != "a" || got[1].Key != "b" {
		t.Fatalf("order: %+v", got)
	}
}

func TestOverrideRoundTrip(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	if err := s.SetOverride(ctx, dinv.Override{PR: ref, Hold: true}); err != nil {
		t.Fatal(err)
	}
	o, err := s.Override(ctx, ref)
	if err != nil || !o.Hold {
		t.Fatalf("override: %+v %v", o, err)
	}
	all, err := s.Overrides(ctx)
	if err != nil || len(all) != 1 {
		t.Fatalf("overrides: %v %v", all, err)
	}
	if o, err := s.Override(ctx, github.Ref{Owner: "x", Repo: "y", Number: 9}); err != nil || o.Hold || o.PR.Number != 9 {
		t.Fatalf("absent override: %+v %v", o, err)
	}
}

func TestListOpenOnlyAndByBranch(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	upsert(t, s, dinv.PR{Ref: ref, State: "OPEN", HeadBranch: "feat/x"})
	upsert(t, s, dinv.PR{Ref: github.Ref{Owner: "acme", Repo: "api", Number: 2}, State: "MERGED", HeadBranch: "feat/x"})
	open, err := s.ListPRs(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	all, err := s.ListPRs(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 1 || len(all) != 2 {
		t.Fatalf("open %d all %d", len(open), len(all))
	}
}

func TestWorkspaceAndMeta(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	upsert(t, s, dinv.PR{Ref: ref, State: "OPEN"})
	if err := s.SetWorkspace(ctx, ref, dinv.Workspace{ID: "w1", DisplayName: "REVIEW x", Status: "reviewing", IsReview: true, AgentStates: []turn.AgentState{turn.AgentWorking}, Comment: "r2 held: approve?"}); err != nil {
		t.Fatal(err)
	}
	got, _, err := s.PR(ctx, ref)
	if err != nil || got.Workspace.ID != "w1" || len(got.Workspace.AgentStates) != 1 || got.Workspace.AgentStates[0] != turn.AgentWorking || got.Workspace.Comment != "r2 held: approve?" {
		t.Fatalf("workspace: %+v %v", got.Workspace, err)
	}
	if err := s.ReplaceWorkspaces(ctx, nil); err != nil {
		t.Fatal(err)
	}
	got, _, err = s.PR(ctx, ref)
	if err != nil || got.Workspace.ID != "" || got.Workspace.AgentStates != nil || got.Workspace.Comment != "" {
		t.Fatalf("workspace not cleared: %+v %v", got.Workspace, err)
	}
	if err := s.SetMeta(ctx, "fast_sweep_at", "2026-10-01T12:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if v, err := s.Meta(ctx, "fast_sweep_at"); err != nil || v != "2026-10-01T12:00:00Z" {
		t.Fatalf("meta: %q %v", v, err)
	}
	if v, err := s.Meta(ctx, "absent"); err != nil || v != "" {
		t.Fatalf("absent meta: %q %v", v, err)
	}
}

func TestUnknownAgentStatesRoundTrip(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	upsert(t, s, dinv.PR{Ref: ref, State: "OPEN"})
	states := []turn.AgentState{turn.AgentWorking, turn.ParseAgentState("idle")}
	if err := s.SetWorkspace(ctx, ref, dinv.Workspace{ID: "w1", AgentStates: states}); err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := s.db.R().QueryRowContext(ctx, `SELECT ws_agent FROM pull_requests WHERE number=?`, ref.Number).Scan(&stored); err != nil || stored != `["working","idle"]` {
		t.Fatalf("stored %q, %v", stored, err)
	}
	got, _, err := s.PR(ctx, ref)
	if err != nil || !slices.Equal(got.Workspace.AgentStates, states) || got.Workspace.AgentStates[1].String() != "idle" {
		t.Fatalf("agent states %v, %v", got.Workspace.AgentStates, err)
	}
}

func TestEventsByPRIsOneStatement(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	refs := make([]github.Ref, 3*eventsChunk+1)
	evs := make([]dinv.Event, len(refs))
	for i := range refs {
		refs[i] = github.Ref{Owner: "acme", Repo: "api", Number: i + 1}
		evs[i] = dinv.Event{PR: refs[i], Kind: dinv.KindComment, Key: fmt.Sprint(i + 1), At: at}
	}
	if _, err := s.AppendEvents(ctx, append(evs, dinv.Event{PR: github.Ref{Owner: "acme", Repo: "web", Number: 1}, Kind: dinv.KindComment, Key: "x", At: at})); err != nil {
		t.Fatal(err)
	}
	mctx, sc := metertest.Start(t, ctx)
	got, err := s.EventsByPR(mctx, refs)
	if err != nil {
		t.Fatal(err)
	}
	metertest.Within(t, sc, metertest.Budget{{Res: meter.SQL}: 1})
	if len(got) != len(refs) {
		t.Fatalf("got %d refs, want %d", len(got), len(refs))
	}
	for _, r := range refs {
		if evs := got[r]; len(evs) != 1 || evs[0].PR != r || evs[0].Key != fmt.Sprint(r.Number) {
			t.Fatalf("%v: %+v", r, evs)
		}
	}
	if none, err := s.EventsByPR(ctx, nil); err != nil || len(none) != 0 {
		t.Fatalf("no refs: %v %v", none, err)
	}
	rows, err := s.db.R().QueryContext(ctx, `EXPLAIN QUERY PLAN SELECT `+eventColumns+` FROM events
		WHERE (provider, unit_key) IN (SELECT value->>0, value->>1 FROM json_each(?))
		ORDER BY at, id`, `[["github","acme/api#1"]]`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if !slices.ContainsFunc(plan, func(d string) bool { return strings.Contains(d, "events USING INDEX events_unit") }) {
		t.Fatalf("the events read scans: %q", plan)
	}
}

func TestListColumnsKeepCommas(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	upsert(t, s, dinv.PR{Ref: ref, State: "OPEN", Reasons: []string{"a,b"}, ReviewRequests: []string{"x, y"}})
	states := []turn.AgentState{turn.ParseAgentState("waiting, input"), turn.AgentWorking}
	if err := s.SetWorkspace(ctx, ref, dinv.Workspace{ID: "w1", AgentStates: states}); err != nil {
		t.Fatal(err)
	}
	got, _, err := s.PR(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.Workspace.AgentStates, states) || got.Workspace.AgentStates[0].String() != "waiting, input" {
		t.Fatalf("agent states: %q", got.Workspace.AgentStates)
	}
	if len(got.Reasons) != 1 || got.Reasons[0] != "a,b" || len(got.ReviewRequests) != 1 || got.ReviewRequests[0] != "x, y" {
		t.Fatalf("lists: %q %q", got.Reasons, got.ReviewRequests)
	}
	upsert(t, s, dinv.PR{Ref: ref, State: "OPEN", Reasons: []string{"c"}})
	got, _, err = s.PR(ctx, ref)
	if err != nil || strings.Join(got.Reasons, "|") != "a,b|c" {
		t.Fatalf("reasons union: %q %v", got.Reasons, err)
	}
}

func TestUnparsableTimestampIsAnError(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	upsert(t, s, dinv.PR{Ref: ref, State: "OPEN", UpdatedAt: at})
	if _, err := s.AppendEvents(ctx, []dinv.Event{{PR: ref, Kind: dinv.KindReady, At: at}}); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`UPDATE pull_requests SET updated_at='2026-10-01T12:00:00Z'`,
		`UPDATE events SET at='2026-10-01 12:00'`,
	} {
		sqlitetest.Exec(t, s.db, q)
	}
	if _, _, err := s.PR(ctx, ref); err == nil {
		t.Error("PR: want error for unparsable updated_at")
	}
	if _, err := s.ListPRs(ctx, false); err == nil {
		t.Error("ListPRs: want error for unparsable updated_at")
	}
	if _, err := s.EventsByPR(ctx, []github.Ref{ref}); err == nil {
		t.Error("EventsByPR: want error for unparsable at")
	}
}

func TestAppendEventsRejectsZeroAt(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	n, err := s.AppendEvents(ctx, []dinv.Event{{PR: ref, Kind: dinv.KindPush, Key: "abc", At: at}, {PR: ref, Kind: dinv.KindReady}})
	if err == nil || !strings.Contains(err.Error(), dinv.KindReady) {
		t.Fatalf("want error naming %q, got %d %v", dinv.KindReady, n, err)
	}
	if got, err := s.Events(ctx, ref); err != nil || len(got) != 0 {
		t.Fatalf("batch must not be partially stored: %+v %v", got, err)
	}
}

func TestTransactionsTakeTheWriteLockAtBegin(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inv.db")
	s := openAt(t, path)
	_, err := s.db.Write(context.Background(), func(*sqlite.Tx) error {
		if !sqlitetest.WriteLockHeld(t, path) {
			t.Error("a second writer began while a store transaction was open")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestListPRsOrderIsStable(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	later := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	for _, r := range []github.Ref{
		{Owner: "acme", Repo: "api", Number: 1},
		{Owner: "acme", Repo: "web", Number: 5},
		{Owner: "acme", Repo: "api", Number: 3},
		{Owner: "beta", Repo: "api", Number: 9},
	} {
		upsert(t, s, dinv.PR{Ref: r, State: "OPEN"})
	}
	upsert(t, s, dinv.PR{Ref: github.Ref{Owner: "zeta", Repo: "z", Number: 1}, State: "OPEN", UpdatedAt: later})
	prs, err := s.ListPRs(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, pr := range prs {
		got = append(got, fmt.Sprintf("%s/%s#%d", pr.Ref.Owner, pr.Ref.Repo, pr.Ref.Number))
	}
	want := "zeta/z#1 acme/api#3 acme/api#1 acme/web#5 beta/api#9"
	if strings.Join(got, " ") != want {
		t.Fatalf("order: %v, want %s", got, want)
	}
}

func TestReplaceWorkspacesSwapsEveryLink(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	other := github.Ref{Owner: "acme", Repo: "api", Number: 2}
	upsert(t, s, dinv.PR{Ref: ref, State: "OPEN"})
	upsert(t, s, dinv.PR{Ref: other, State: "OPEN"})
	if err := s.SetWorkspace(ctx, ref, dinv.Workspace{ID: "w1", IsReview: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceWorkspaces(ctx, map[github.Ref]dinv.Workspace{other: {ID: "w2", AgentStates: []turn.AgentState{turn.AgentWorking}}}); err != nil {
		t.Fatal(err)
	}
	a, _, _ := s.PR(ctx, ref)
	b, _, _ := s.PR(ctx, other)
	if a.Workspace.ID != "" || a.Workspace.IsReview {
		t.Fatalf("stale link kept: %+v", a.Workspace)
	}
	if b.Workspace.ID != "w2" || len(b.Workspace.AgentStates) != 1 {
		t.Fatalf("new link missing: %+v", b.Workspace)
	}
}

func TestNextAnswerIsKeptTakenOnceAndSurvivesOverrideWrites(t *testing.T) {
	st := open(t)
	ctx := context.Background()
	ref := github.Ref{Owner: "acme", Repo: "api", Number: 7}
	if err := st.SetNextGuidance(ctx, ref, "only the nits"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetOverride(ctx, dinv.Override{PR: ref, Hold: true}); err != nil {
		t.Fatal(err)
	}
	o, err := st.Override(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	if !o.Hold || o.NextGuidance != "only the nits" {
		t.Fatalf("SetOverride clobbered the next answer: %+v", o)
	}
	guidance, err := st.TakeNextGuidance(ctx, ref)
	if err != nil || guidance != "only the nits" {
		t.Fatalf("%q %v", guidance, err)
	}
	guidance, err = st.TakeNextGuidance(ctx, ref)
	if err != nil || guidance != "" {
		t.Fatalf("taken twice: %q %v", guidance, err)
	}
	if o, _ := st.Override(ctx, ref); !o.Hold {
		t.Fatal("TakeNextGuidance cleared the hold")
	}
}

func TestKeptGuidanceYieldsToNewerGuidance(t *testing.T) {
	st := open(t)
	ctx := context.Background()
	ref := github.Ref{Owner: "acme", Repo: "api", Number: 7}
	if err := st.KeepNextGuidance(ctx, ref, "check the migration"); err != nil {
		t.Fatal(err)
	}
	if err := st.KeepNextGuidance(ctx, ref, "an older one"); err != nil {
		t.Fatal(err)
	}
	if o, _ := st.Override(ctx, ref); o.NextGuidance != "check the migration" {
		t.Fatalf("kept guidance replaced newer guidance: %+v", o)
	}
	guidance, err := st.TakeNextGuidance(ctx, ref)
	if err != nil || guidance != "check the migration" {
		t.Fatalf("%q %v", guidance, err)
	}
	if o, _ := st.Override(ctx, ref); o.NextGuidance != "" {
		t.Fatalf("%+v", o)
	}
	if guidance, err := st.TakeNextGuidance(ctx, github.Ref{Owner: "acme", Repo: "api", Number: 8}); err != nil || guidance != "" {
		t.Fatalf("no row: %q %v", guidance, err)
	}
}

func TestRecentActivityNewestFirstAcrossPullRequests(t *testing.T) {
	st := open(t)
	ctx := context.Background()
	a := github.Ref{Owner: "acme", Repo: "api", Number: 7}
	b := github.Ref{Owner: "acme", Repo: "web", Number: 3}
	t0 := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	for _, r := range []github.Ref{a, b} {
		if err := st.UpsertPR(ctx, dinv.PR{Ref: r, Title: "T", State: "OPEN"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.AppendEvents(ctx, []dinv.Event{
		{PR: a, Kind: dinv.KindPush, Key: "h1", Actor: "ana", At: t0},
		{PR: b, Kind: dinv.KindReview, Key: "r1", Actor: "bob", At: t0.Add(2 * time.Hour), Data: "APPROVED:h9"},
		{PR: a, Kind: dinv.KindComment, Key: "c1", Actor: "bob", At: t0.Add(time.Hour)},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := st.RecentActivity(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Key != "r1" || got[1].Key != "c1" {
		t.Fatalf("%+v", got)
	}
}

func TestRecentActivityLeavesOutTheBookkeeping(t *testing.T) {
	st := open(t)
	ctx := context.Background()
	a := github.Ref{Owner: "acme", Repo: "api", Number: 7}
	t0 := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	if err := st.UpsertPR(ctx, dinv.PR{Ref: a, Title: "T", State: "OPEN"}); err != nil {
		t.Fatal(err)
	}
	evs := []dinv.Event{{PR: a, Kind: dinv.KindPush, Key: "h1", Actor: "ana", At: t0}}
	for i, kind := range dinv.BookkeepingKinds() {
		evs = append(evs, dinv.Event{PR: a, Kind: kind, Key: kind, At: t0.Add(time.Duration(i+1) * time.Minute)})
	}
	if _, err := st.AppendEvents(ctx, evs); err != nil {
		t.Fatal(err)
	}
	got, err := st.RecentActivity(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Key != "h1" {
		t.Fatalf("%+v", got)
	}
	all, err := st.Events(ctx, a)
	if err != nil || len(all) != len(evs) {
		t.Fatalf("the pull request keeps %d of %d events: %v", len(all), len(evs), err)
	}
}

func TestRecentActivityOrphanEventsTakeNoLimitSlot(t *testing.T) {
	st := open(t)
	ctx := context.Background()
	a := github.Ref{Owner: "acme", Repo: "api", Number: 7}
	orphan := github.Ref{Owner: "acme", Repo: "gone", Number: 1}
	t0 := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	if err := st.UpsertPR(ctx, dinv.PR{Ref: a, Title: "T", State: "OPEN"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AppendEvents(ctx, []dinv.Event{
		{PR: a, Kind: dinv.KindPush, Key: "h1", At: t0},
		{PR: a, Kind: dinv.KindComment, Key: "c1", At: t0.Add(time.Minute)},
		{PR: orphan, Kind: dinv.KindPush, Key: "o1", At: t0.Add(2 * time.Minute)},
		{PR: orphan, Kind: dinv.KindPush, Key: "o2", At: t0.Add(3 * time.Minute)},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := st.RecentActivity(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Key != "c1" || got[1].Key != "h1" || got[0].Title != "T" {
		t.Fatalf("%+v", got)
	}
}

func TestFirstRoundBlocks(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC)
	a, b, c := github.Ref{Owner: "acme", Repo: "api", Number: 1}, github.Ref{Owner: "acme", Repo: "api", Number: 2}, github.Ref{Owner: "acme", Repo: "api", Number: 3}
	for _, ev := range []dinv.Event{
		{PR: a, Kind: dinv.KindRoundBlocked, At: at, Data: "old"},
		{PR: a, Kind: dinv.KindRoundBlocked, At: at.Add(time.Hour), Data: "new"},
		{PR: b, Kind: dinv.KindRoundBlocked, At: at, Data: "x"},
		{PR: b, Kind: dinv.KindRoundUnblocked, At: at.Add(time.Hour), Data: "the branch is free"},
		{PR: c, Kind: dinv.KindRoundSent, At: at},
	} {
		if err := s.RecordEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.FirstRoundBlocks(ctx)
	if err != nil || len(got) != 1 || got[a.Unit()].Data != "new" || got[a.Unit()].Unit != a.Unit() {
		t.Fatalf("%v %v", got, err)
	}
}

func TestAStartBlockAnOlderReleaseWroteReadsWithoutAConflictHead(t *testing.T) {
	key := strings.Join([]string{"2026-10-09T10:00:00Z", "trust", "/src/api", "", "", "", "triage", "f1", "c2"}, "\n")
	b, ok := inventory.StartBlockOf(port.Event{Kind: dinv.KindRoundBlocked, Key: key})
	want := inventory.StartBlock{Question: "trust", Root: "/src/api", Round: "triage", Skip: map[string]string{"addressed.reviews": "f1", "addressed.checks": "c2"}}
	if !ok || !reflect.DeepEqual(b, want) {
		t.Fatalf("%+v %v", b, ok)
	}
}

func TestAStartBlockWithoutAConflictHeadKeepsTheKeyAnOlderReleaseReads(t *testing.T) {
	at := time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC)
	if n := len(strings.Split(inventory.StartBlock{Round: "triage"}.Key(at), "\n")); n != 9 {
		t.Fatalf("%d parts", n)
	}
	key := inventory.StartBlock{Round: "follow-up", Skip: map[string]string{"plugin.x": "1"}}.Key(at)
	if b, ok := inventory.StartBlockOf(port.Event{Kind: dinv.KindRoundBlocked, Key: key}); !ok || !maps.Equal(b.Skip, map[string]string{"plugin.x": "1"}) {
		t.Fatalf("a block's skip marks: %+v %v", b, ok)
	}
}

func TestStoredStartBlocksReadByUnit(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC)
	pull := github.Ref{Owner: "acme", Repo: "api", Number: 7}
	thread := unit.Unit{Provider: "thread", Key: "1700000000.1"}
	want := inventory.StartBlock{Question: "project-mcp", Root: "/src/api", Home: "/h", Homes: []string{"/h"}, Round: "first round", Skip: map[string]string{"plugin.x": "1", "plugin.y": ""}}
	if err := s.RecordEvent(ctx, dinv.Event{PR: pull, Kind: dinv.KindRoundBlocked, Key: want.Key(at), At: at, Data: "asked"}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordUnitEvent(ctx, port.Event{Unit: thread, Kind: dinv.KindRoundBlocked, Key: want.Key(at), At: at, Data: "asked"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.FirstRoundBlocks(ctx)
	if err != nil || len(got) != 2 {
		t.Fatalf("%v %v", got, err)
	}
	for _, u := range []unit.Unit{pull.Unit(), thread} {
		b, ok := inventory.StartBlockOf(got[u])
		if !ok || !reflect.DeepEqual(b, want) || got[u].Unit != u {
			t.Errorf("%s: %+v %v", u, b, ok)
		}
	}
	if err := s.RecordUnitEvent(ctx, port.Event{Unit: thread, Kind: dinv.KindRoundUnblocked, At: at.Add(time.Minute), Data: "passed"}); err != nil {
		t.Fatal(err)
	}
	if got, err = s.FirstRoundBlocks(ctx); err != nil || len(got) != 1 {
		t.Fatalf("after the thread's unblock: %v %v", got, err)
	}
}

func TestABlockEndsWhereItsRowStopsShowingIt(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	if err := s.SetViewer(ctx, unit.GitHub, "Me"); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC)
	sent, reviewed, other, held := github.Ref{Owner: "acme", Repo: "api", Number: 1}, github.Ref{Owner: "acme", Repo: "api", Number: 2}, github.Ref{Owner: "acme", Repo: "api", Number: 3}, github.Ref{Owner: "acme", Repo: "api", Number: 4}
	var evs []dinv.Event
	for _, ref := range []github.Ref{sent, reviewed, other, held} {
		evs = append(evs, dinv.Event{PR: ref, Kind: dinv.KindRoundBlocked, At: at, Data: "blocked"})
	}
	evs = append(evs,
		dinv.Event{PR: sent, Kind: dinv.KindRoundSent, At: at.Add(time.Hour)},
		dinv.Event{PR: reviewed, Kind: dinv.KindReview, Key: "r1", Actor: "me", At: at.Add(time.Hour), Data: "COMMENTED:h"},
		dinv.Event{PR: other, Kind: dinv.KindReview, Key: "r2", Actor: "bob", At: at.Add(time.Hour), Data: "COMMENTED:h"},
		dinv.Event{PR: held, Kind: dinv.KindRoundHeld, At: at.Add(time.Hour)},
	)
	for _, ev := range evs {
		if err := s.RecordEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.FirstRoundBlocks(ctx)
	if err != nil || len(got) != 1 || got[other.Unit()].Data != "blocked" {
		t.Fatalf("%v %v", got, err)
	}
	for _, ref := range []github.Ref{sent, reviewed, other, held} {
		all, err := s.Events(ctx, ref)
		if err != nil {
			t.Fatal(err)
		}
		_, shown := dinv.ActiveBlock(all, "Me")
		if _, kept := got[ref.Unit()]; shown != kept {
			t.Errorf("%s: the row shows the block %v, the sweep keeps it %v", ref, shown, kept)
		}
	}
}

func TestPRKeepsBodyEditAndChangedFiles(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	edited := time.Date(2026, 10, 5, 8, 0, 0, 0, time.UTC)
	if err := s.UpsertPR(ctx, dinv.PR{Ref: ref, State: "OPEN", BodyEditedAt: edited, ChangedFiles: 4}); err != nil {
		t.Fatal(err)
	}
	got, _, err := s.PR(ctx, ref)
	if err != nil || !got.BodyEditedAt.Equal(edited) || got.ChangedFiles != 4 {
		t.Fatalf("%+v %v", got, err)
	}
	edited = edited.Add(time.Hour)
	if err := s.UpsertPR(ctx, dinv.PR{Ref: ref, State: "OPEN", BodyEditedAt: edited, ChangedFiles: 9}); err != nil {
		t.Fatal(err)
	}
	if got, _, err = s.PR(ctx, ref); err != nil || !got.BodyEditedAt.Equal(edited) || got.ChangedFiles != 9 {
		t.Fatalf("after a push: %+v %v", got, err)
	}
}

func TestTheTrackedReasonFollowsTheOverride(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	other := github.Ref{Owner: "acme", Repo: "web", Number: 3}
	reasons := func(r github.Ref) []string {
		pr, _, _ := s.PR(ctx, r)
		return pr.Reasons
	}
	upsert(t, s, dinv.PR{Ref: ref, State: "OPEN", Reasons: []string{"repo"}})
	if err := s.SetOverride(ctx, dinv.Override{PR: ref, Tracked: true, Hold: true}); err != nil {
		t.Fatal(err)
	}
	if got := reasons(ref); !slices.Equal(got, []string{"repo", dinv.ReasonTracked}) {
		t.Fatalf("after track: %v", got)
	}
	if err := s.SetReasons(ctx, ref, []string{"author"}); err != nil {
		t.Fatal(err)
	}
	upsert(t, s, dinv.PR{Ref: ref, State: "OPEN"})
	if got := reasons(ref); !slices.Equal(got, []string{"author", dinv.ReasonTracked}) {
		t.Fatalf("a recompute from the scope and a sweep's read dropped the track: %v", got)
	}
	if err := s.SetOverride(ctx, dinv.Override{PR: other, Tracked: true}); err != nil {
		t.Fatal(err)
	}
	upsert(t, s, dinv.PR{Ref: other, State: "OPEN"})
	if got := reasons(other); !slices.Equal(got, []string{dinv.ReasonTracked}) {
		t.Fatalf("tracked before it was stored: %v", got)
	}
	if got, _ := s.Tracked(ctx); !slices.Equal(got, []github.Ref{ref, other}) {
		t.Fatalf("tracked: %v", got)
	}
	if err := s.SetOverride(ctx, dinv.Override{PR: ref, Hold: true}); err != nil {
		t.Fatal(err)
	}
	if got := reasons(ref); !slices.Equal(got, []string{"author"}) {
		t.Fatalf("untrack keeps the other reasons only: %v", got)
	}
	if o, _ := s.Override(ctx, ref); o.Tracked || !o.Hold {
		t.Fatalf("override %+v", o)
	}
}

func TestSaveTurnSharesParts(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	t0 := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	count := func() int {
		var n int
		if err := s.db.R().QueryRowContext(ctx, `SELECT count(*) FROM instruction_parts`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	a := "intro\n# One\nsame\n## Two\nfirst\n### Three\nend"
	b := "intro\n# One\nsame\n## Two\nsecond\n### Three\nend"
	if err := s.SaveTurn(ctx, dinv.StoredTurn{ID: "a00000000000", Kind: "review", Repo: "acme/api", Text: a}, t0); err != nil {
		t.Fatal(err)
	}
	if got := count(); got != 4 {
		t.Fatalf("first text stores %d parts, want 4", got)
	}
	if err := s.SaveTurn(ctx, dinv.StoredTurn{ID: "b00000000000", Kind: "review", Repo: "acme/api", Text: b}, t0); err != nil {
		t.Fatal(err)
	}
	if got := count(); got != 5 {
		t.Fatalf("second text stores %d parts in all, want 5", got)
	}
	for id, want := range map[string]string{"a00000000000": a, "b00000000000": b} {
		if got, ok, err := s.Turn(ctx, id); err != nil || !ok || got.Text != want {
			t.Fatalf("%s: %+v %v %v", id, got, ok, err)
		}
	}
}

func TestSaveTurnNeverPrunes(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	t0 := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	if err := s.SaveTurn(ctx, dinv.StoredTurn{ID: "old000000000", Kind: "review", Repo: "acme/api", Text: "OLD"}, t0); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveTurn(ctx, dinv.StoredTurn{ID: "new000000000", Kind: "review", Repo: "acme/api", Text: "NEW"}, t0.Add(400*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got, ok, _ := s.Turn(ctx, "old000000000"); !ok || got.Text != "OLD" {
		t.Fatal("SaveTurn pruned an older turn")
	}
}

func TestSavingATurnAgainKeepsItReadable(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	t0 := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	in := dinv.StoredTurn{ID: "same00000000", Kind: "review", Repo: "acme/api", Text: "T\n# H\nU"}
	if err := s.SaveTurn(ctx, in, t0); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveTurn(ctx, in, t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got, ok, _ := s.Turn(ctx, in.ID); !ok || got.Text != in.Text {
		t.Fatal("the round just sent cannot read its instructions")
	}
}

func TestATurnKeepsItsTextAndSources(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	t0 := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	in := dinv.StoredTurn{ID: "abc123abc123", Kind: "review", Repo: "acme/api", Text: "T", Sources: []string{"user: /p/review.md", "project: /q/review.md"}, BundledSHA: "b"}
	if err := s.SaveTurn(ctx, in, t0); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.Turn(ctx, in.ID)
	if err != nil || !ok || got.Text != "T" || got.Kind != "review" || len(got.Sources) != 2 || got.Sources[1] != "project: /q/review.md" || got.BundledSHA != "b" {
		t.Fatalf("%+v %v %v", got, ok, err)
	}
	if _, ok, _ := s.Turn(ctx, "nope00000000"); ok {
		t.Fatal("a turn that was never saved")
	}
}

func TestMetaPrefixed(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	for k, v := range map[string]string{"git_access/github.com": "a", "git_access/gitlab.com": "b", "git_accessory": "c", "other": "d"} {
		if err := s.SetMeta(ctx, k, v); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.MetaPrefixed(ctx, "git_access/")
	if err != nil || len(got) != 2 || got["git_access/github.com"] != "a" || got["git_access/gitlab.com"] != "b" {
		t.Fatalf("%v %v", got, err)
	}
}

func TestPRsChunks(t *testing.T) {
	s := open(t)
	ctx, sc := metertest.Start(t, context.Background())
	refs := make([]github.Ref, 1200)
	for i := range refs {
		refs[i] = github.Ref{Owner: "acme", Repo: "api", Number: i + 1}
		if i%2 == 0 {
			upsert(t, s, dinv.PR{Ref: refs[i], State: dgh.StateOpen, Title: fmt.Sprint("t", i)})
		}
	}
	before := metertest.Count(sc, metertest.Limit{Res: meter.SQL})
	got, err := s.PRs(ctx, refs)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 600 || got[refs[10]].Title != "t10" {
		t.Fatalf("%d pull requests, #11 %+v", len(got), got[refs[10]])
	}
	if _, ok := got[refs[1]]; ok {
		t.Fatal("an unstored pull request was returned")
	}
	if n := metertest.Count(sc, metertest.Limit{Res: meter.SQL}) - before; n != 3 {
		t.Fatalf("%d statements for 1200 refs, want 3", n)
	}
	if none, err := s.PRs(ctx, nil); err != nil || len(none) != 0 {
		t.Fatalf("no refs: %v %v", none, err)
	}
}

func TestReviewEndedIsOneQuery(t *testing.T) {
	s := open(t)
	ctx, sc := metertest.Start(t, context.Background())
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	refs := []github.Ref{{Owner: "acme", Repo: "api", Number: 1}, {Owner: "acme", Repo: "api", Number: 2}, {Owner: "acme", Repo: "web", Number: 1}}
	for _, r := range refs[:2] {
		if err := s.RecordEvent(ctx, dinv.Event{PR: r, Kind: dinv.KindReviewEnded, Key: "MERGED", At: at}); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 40 {
		if err := s.RecordEvent(ctx, dinv.Event{PR: refs[2], Kind: dinv.KindStatus, At: at.Add(time.Duration(i) * time.Minute)}); err != nil {
			t.Fatal(err)
		}
	}
	before := metertest.Count(sc, metertest.Limit{Res: meter.SQL, Op: "query"})
	got, err := s.Ended(ctx, dinv.KindReviewEnded, refs)
	if err != nil {
		t.Fatal(err)
	}
	if !got[refs[0]] || !got[refs[1]] || got[refs[2]] || len(got) != 2 {
		t.Fatalf("ended = %v", got)
	}
	if n := metertest.Count(sc, metertest.Limit{Res: meter.SQL, Op: "query"}) - before; n != 1 {
		t.Fatalf("Ended ran %d queries, want 1\n%s", n, metertest.Report(sc.Tally()))
	}
}

func TestPRsWithOverridesMatchesTheirSeparateReads(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	for i := range 4 {
		ref := github.Ref{Owner: "acme", Repo: "api", Number: i + 1}
		if err := s.UpsertPR(ctx, dinv.PR{Ref: ref, Title: fmt.Sprint(i), State: "OPEN", UpdatedAt: at.Add(time.Duration(i) * time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	set := []dinv.Override{
		{PR: github.Ref{Owner: "acme", Repo: "api", Number: 2}, Hold: true, NextGuidance: "keep the tests"},
		{PR: github.Ref{Owner: "acme", Repo: "api", Number: 3}, Adopted: true, NextGuidance: "split the commit"},
		{PR: github.Ref{Owner: "acme", Repo: "api", Number: 4}, Tracked: true, NextGuidance: "rebase first"},
	}
	for _, o := range set {
		if err := s.SetOverride(ctx, o); err != nil {
			t.Fatal(err)
		}
		if err := s.SetNextGuidance(ctx, o.PR, o.NextGuidance); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetOverride(ctx, dinv.Override{PR: github.Ref{Owner: "acme", Repo: "gone", Number: 9}, Hold: true}); err != nil {
		t.Fatal(err)
	}
	mctx, sc := metertest.Start(t, ctx)
	prs, overrides, err := s.PRsWithOverrides(mctx)
	if err != nil {
		t.Fatal(err)
	}
	metertest.Within(t, sc, metertest.Budget{{Res: meter.SQL}: 2})
	want, err := s.ListPRs(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(prs, want) {
		t.Fatalf("pull requests %+v, want %+v", prs, want)
	}
	all, err := s.Overrides(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(overrides) != len(set) {
		t.Fatalf("overrides %+v, want %d", overrides, len(set))
	}
	for _, o := range set {
		if !reflect.DeepEqual(overrides[o.PR], all[o.PR]) || !reflect.DeepEqual(overrides[o.PR], o) {
			t.Fatalf("override %+v, separate read %+v, written %+v", overrides[o.PR], all[o.PR], o)
		}
	}
}

func TestAdoptAndGuidanceClearWriteTheirColumnsAlone(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	if err := s.SetOverride(ctx, dinv.Override{PR: ref, Hold: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetNextGuidance(ctx, ref, "ask alice"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAdopted(ctx, ref); err != nil {
		t.Fatal(err)
	}
	clear := map[string]string{port.OverrideNextGuidance: ""}
	if cleared, err := s.db.SetOverride(ctx, ref.Unit(), clear, map[string]string{port.OverrideNextGuidance: "ask bob"}); err != nil || cleared {
		t.Fatalf("cleared another guidance: %v %v", cleared, err)
	}
	if cleared, err := s.db.SetOverride(ctx, ref.Unit(), clear, map[string]string{port.OverrideNextGuidance: "ask alice"}); err != nil || !cleared {
		t.Fatalf("cleared %v %v", cleared, err)
	}
	o, err := s.Override(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	if !o.Adopted || !o.Hold || o.NextGuidance != "" {
		t.Fatalf("%+v", o)
	}
}
