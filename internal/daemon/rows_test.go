package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	src "github.com/wloophq/wloop/pkg/plugin/source"

	ghsource "github.com/wloophq/wloop/internal/adapter/source/github"
	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/core/turns/turnstest"
	dgh "github.com/wloophq/wloop/internal/domain/github"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/readmodel"
)

var fixtureNow = time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC) // Thursday

type fixture struct {
	t   *testing.T
	st  *inventory.Store
	db  *sqlite.DB
	s   *Server
	now time.Time
	mu  sync.Mutex
}

// newFixture returns a Server over an empty store whose viewer is fer, a
// member of acme/search, with robo as an automated reviewer and skipper as
// a reviewer the loop never addresses.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	stDB, err := sqlitetest.Open(filepath.Join(t.TempDir(), "inv.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	st := inventory.New(stDB)
	t.Cleanup(func() { stDB.Close() })
	cfg := config.Defaults()
	cfg.GitHub.AutomatedReviewers = []string{"robo"}
	cfg.GitHub.AddressSkipReviewers = []string{"skipper"}
	f := &fixture{t: t, st: st, db: stDB, now: fixtureNow}
	f.s = New(withStore(Deps{Cfg: cfg, Activity: stDB, Version: "test", Now: f.clock,
		Identity: viewerIs("fer", "acme/search"),
		Log:      func(string, ...any) {}}, st))
	f.s.token = ""
	stopOnCleanup(t, f.s)
	return f
}

func (f *fixture) clock() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *fixture) advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}

func ref(n int) github.Ref { return github.Ref{Owner: "acme", Repo: "api", Number: n} }

// open stores an open, green pull request pushed at pushed, plus extra events.
func (f *fixture) open(n int, author string, pushed time.Time, edit func(*dinv.PR), evs ...dinv.Event) {
	f.t.Helper()
	ctx := context.Background()
	pr := dinv.PR{Ref: ref(n), Title: "PR " + strconv.Itoa(n), Author: author, State: "OPEN", HeadOid: "h" + strconv.Itoa(n),
		ChecksState: "SUCCESS", Reasons: []string{"involved"}, UpdatedAt: pushed}
	if edit != nil {
		edit(&pr)
	}
	if err := f.st.UpsertPR(ctx, pr); err != nil {
		f.t.Fatal(err)
	}
	evs = append(evs, dinv.Event{PR: pr.Ref, Kind: dinv.KindPush, Key: pr.HeadOid, Actor: author, At: pushed})
	for i := range evs {
		evs[i].PR = pr.Ref
	}
	if _, err := f.st.AppendEvents(ctx, evs); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) rows(query string) []api.Row {
	f.t.Helper()
	var rows []api.Row
	if code := get(f.t, f.s, "/api/rows"+query, &rows); code != 200 {
		f.t.Fatalf("%s: %d", query, code)
	}
	return rows
}

func numbers(rows []api.Row) []int {
	out := []int{}
	for _, r := range rows {
		out = append(out, rowNumber(r))
	}
	return out
}

func failing(pr *dinv.PR) { pr.ChecksState = "FAILURE" }

func requests(logins ...string) func(*dinv.PR) {
	return func(pr *dinv.PR) { pr.ReviewRequests = logins }
}

func TestPRsSortStalledFirstThenOldest(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	// The store lists by updated_at desc, so it never returns this order by
	// itself; #5 (checks, 24h threshold) is stalled yet newer than #6.
	f.open(1, "ana", fixtureNow.Add(-1*time.Hour), requests("fer"))
	f.open(2, "ana", fixtureNow.Add(-100*time.Hour), requests("fer"))
	f.open(3, "ana", fixtureNow.Add(-5*time.Hour), requests("fer"))
	f.open(4, "ana", fixtureNow.Add(-120*time.Hour), requests("fer"))
	f.open(5, "ana", fixtureNow.Add(-30*time.Hour), failing)
	f.open(6, "ana", fixtureNow.Add(-40*time.Hour), requests("fer"))
	if got := numbers(f.rows("?view=all")); !slices.Equal(got, []int{4, 2, 5, 6, 3, 1}) {
		t.Fatalf("order %v", got)
	}
}

func TestPRsIgnoreAutomatedAndSkippedReviewers(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	pushed := fixtureNow.Add(-2 * time.Hour)
	f.open(20, "ana", pushed, requests("fer"),
		dinv.Event{Kind: dinv.KindReview, Key: "r1", Actor: "robo", At: pushed.Add(time.Minute), Data: "CHANGES_REQUESTED:h20"})
	f.open(21, "ana", pushed, requests("fer"),
		dinv.Event{Kind: dinv.KindComment, Key: "c1", Actor: "skipper", At: pushed.Add(time.Minute)})
	for _, r := range f.rows("?view=all") {
		if r.Kind != "review" || r.Summary.WaitsOn != "me" {
			t.Errorf("#%d: %s waiting on %s", rowNumber(r), r.Kind, r.Summary.WaitsOn)
		}
	}
}

func TestPRsViews(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	at := fixtureNow.Add(-time.Hour)
	f.open(30, "ana", at, nil)
	f.open(31, "ana", at, nil)
	f.open(32, "ana", at, nil)
	f.open(33, "ana", at, func(pr *dinv.PR) { pr.Reasons = nil })
	f.st.SetWorkspace(ctx, ref(30), dinv.Workspace{ID: "w30", IsReview: true, AgentStates: []turn.AgentState{turn.AgentWorking}})
	f.st.SetWorkspace(ctx, ref(31), dinv.Workspace{ID: "w31", IsReview: true, AgentStates: []turn.AgentState{turn.AgentWaiting}})
	f.st.SetOverride(ctx, dinv.Override{PR: ref(32), Adopted: true})
	for _, tc := range []struct {
		view string
		want []int
	}{
		{"loop", []int{30, 31}},
		{"needs-me", []int{31}},
		{"mine", []int{32}},
		{"all", []int{30, 31, 32}},
	} {
		got := numbers(f.rows("?view=" + tc.view))
		slices.Sort(got)
		if !slices.Equal(got, tc.want) {
			t.Errorf("view=%s: got %v, want %v", tc.view, got, tc.want)
		}
	}
}

func TestMyConflictingPullRequestNeedsMe(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	at := fixtureNow.Add(-time.Hour)
	conflicting := func(pr *dinv.PR) {
		pr.ReviewDecision, pr.Mergeable, pr.BaseBranch = "APPROVED", "CONFLICTING", "main"
	}
	f.open(40, "fer", at, conflicting)
	f.open(41, "ana", at, conflicting)
	rows := f.rows("?view=all")
	for _, r := range rows {
		if r.Kind != "conflicts" {
			t.Fatalf("%d: %s", rowNumber(r), r.Kind)
		}
	}
	if got := numbers(f.rows("?view=needs-me")); !slices.Equal(got, []int{40}) {
		t.Fatalf("needs-me %v", got)
	}
}

func TestClosedAndMergedPullRequestsAreInNoOpenView(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	at := fixtureNow.Add(-time.Hour)
	f.open(50, "fer", at, func(pr *dinv.PR) { pr.State, pr.ClosedAt, pr.ChecksState = "CLOSED", at, "FAILURE" })
	f.open(51, "ana", at, func(pr *dinv.PR) {
		pr.State, pr.ClosedAt, pr.MergedAt, pr.ReviewRequests = "MERGED", at, at, []string{"fer"}
	})
	f.open(52, "ana", fixtureNow.Add(-30*24*time.Hour), func(pr *dinv.PR) { pr.State, pr.ClosedAt = "CLOSED", at })
	for _, n := range []int{50, 51, 52} {
		f.st.SetWorkspace(ctx, ref(n), dinv.Workspace{ID: "w" + strconv.Itoa(n), IsReview: true, AgentStates: []turn.AgentState{turn.AgentWaiting}})
		f.st.SetOverride(ctx, dinv.Override{PR: ref(n), Adopted: true})
	}
	for _, view := range []string{api.ViewNeedsMe, api.ViewStalled, api.ViewMine, api.ViewLoop} {
		if got := numbers(f.rows("?view=" + view)); len(got) != 0 {
			t.Errorf("view=%s lists closed pull requests %v", view, got)
		}
	}
}

func TestPRsJSONHasEmptyArraysAndSnakeCase(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.open(33, "ana", fixtureNow.Add(-time.Hour), func(pr *dinv.PR) { pr.Reasons, pr.State = nil, dgh.StateMerged })
	rec := httptest.NewRecorder()
	f.s.Handler().ServeHTTP(rec, apiRequest("GET", "/api/rows?view=all", nil))
	body := rec.Body.String()
	for _, want := range []string{`"views":[]`, `"reasons":[]`, `"agent_states":[]`, `"unit":{"provider":"github","key":"acme/api#33"}`, `"label":"acme/api#33"`, `"link":"https://github.com/acme/api/pull/33"`, `"is_review":false`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s in %s", want, body)
		}
	}
	if strings.Contains(body, "null") {
		t.Errorf("null in %s", body)
	}
}

func TestViewerCachedForTTLAndStaleOnFailure(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.open(12, "bob", fixtureNow.Add(-time.Hour), requests("fer"))
	var calls int
	var fail bool
	f.s.deps.Identity = func(context.Context) (src.Identity, error) {
		calls++
		if fail {
			return src.Identity{}, errors.New("github down")
		}
		return identityOf("fer"), nil
	}
	needsMe := func() int { return len(f.rows("?view=needs-me")) }
	if needsMe() != 1 || needsMe() != 1 || calls != 1 {
		t.Fatalf("first fetch: %d calls", calls)
	}
	f.advance(9 * time.Minute)
	if needsMe(); calls != 1 {
		t.Fatalf("refetched inside the TTL: %d calls", calls)
	}
	f.advance(2 * time.Minute)
	fail = true
	if needsMe() != 1 || calls != 2 {
		t.Fatalf("stale login not served: %d calls", calls)
	}
	if needsMe(); calls != 2 {
		t.Fatalf("failure not cached: %d calls", calls)
	}
	f.advance(2 * time.Minute)
	fail = false
	if needsMe() != 1 || calls != 3 {
		t.Fatalf("no retry after the failure window: %d calls", calls)
	}
}

func TestViewerFailureAtStartupIsCachedBriefly(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	var calls int
	f.s.deps.Identity = func(context.Context) (src.Identity, error) {
		calls++
		return src.Identity{}, errors.New("github down")
	}
	for range 2 {
		if code := get(t, f.s, "/api/rows", nil); code != http.StatusInternalServerError {
			t.Fatalf("%d", code)
		}
	}
	if calls != 1 {
		t.Fatalf("%d calls", calls)
	}
	f.advance(2 * time.Minute)
	get(t, f.s, "/api/rows", nil)
	if calls != 2 {
		t.Fatalf("no retry: %d calls", calls)
	}
}

func TestPostJobs(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.s.jobs["fast"] = newJob("fast", time.Hour, time.Minute, func(context.Context) (string, string, error) { return "", "", nil })
	for path, want := range map[string]int{"/api/jobs/fast": http.StatusAccepted, "/api/jobs/nope": http.StatusNotFound} {
		rec := httptest.NewRecorder()
		f.s.Handler().ServeHTTP(rec, apiRequest("POST", path, nil))
		if rec.Code != want {
			t.Errorf("%s: %d, want %d", path, rec.Code, want)
		}
	}
	select {
	case <-f.s.jobs["fast"].kick:
	default:
		t.Fatal("fast not triggered")
	}
}

func TestLoopAndMineViewsDoNotNeedAScopeReason(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	at := fixtureNow.Add(-time.Hour)
	outOfScope := func(pr *dinv.PR) { pr.Reasons = nil }
	f.open(40, "ana", at, outOfScope)
	f.open(41, "ana", at, outOfScope)
	f.open(42, "ana", at, outOfScope)
	f.open(43, "ana", at, func(pr *dinv.PR) { pr.Reasons, pr.ReviewRequests = nil, []string{"fer"} })
	f.st.SetWorkspace(ctx, ref(40), dinv.Workspace{ID: "w40", IsReview: true, AgentStates: []turn.AgentState{turn.AgentWorking}})
	f.st.SetWorkspace(ctx, ref(41), dinv.Workspace{ID: "w41", IsReview: true, AgentStates: []turn.AgentState{turn.AgentWaiting}})
	f.st.SetOverride(ctx, dinv.Override{PR: ref(42), Adopted: true})
	for _, tc := range []struct {
		view string
		want []int
	}{
		{"loop", []int{40, 41}},
		{"needs-me", []int{41}},
		{"mine", []int{42}},
	} {
		got := numbers(f.rows("?view=" + tc.view))
		slices.Sort(got)
		if !slices.Equal(got, tc.want) {
			t.Errorf("view=%s: got %v, want %v", tc.view, got, tc.want)
		}
	}
}

// withTurns gives the server the store's turns and a fake app whose agents
// are agents.
func (f *fixture) withTurns(agents ...turns.AgentObservation) *sqlite.Store {
	repo := sqlite.New(f.db)
	sessions := turnstest.NewSessions()
	sessions.SetAgents(agents...)
	f.s.deps.Turns = &turns.Service{Intents: turnstest.Intents(), Store: repo, Registry: repo, Sessions: sessions}
	f.s.deps.Sessions = sessions
	f.s.deps.Agents = snapshotOf(sessions)
	return repo
}

// snapshotOf stands for the workspace snapshot: the fake's agents as it
// holds them now, unknown while its reads fail.
func snapshotOf(s *turnstest.Sessions) readmodel.Agents {
	return readmodel.AgentsFunc(func() ([]turns.AgentObservation, bool) {
		agents, err := s.Agents(context.Background())
		return agents, err == nil
	})
}

// turn stores a turn of intent on n in state, with a record unless started.
func (f *fixture) turn(repo *sqlite.Store, n int, intent turn.Intent, state turn.State, session string) {
	f.t.Helper()
	at := fixtureNow.Add(-30 * time.Minute)
	ref := pr.Ref(ref(n))
	b := turn.Book{Unit: ref.Unit(), Turns: []turn.Turn{{Intent: intent, Role: "pr." + string(intent), ID: "r" + strconv.Itoa(n), Trigger: turn.Automated, Head: "h", Session: session,
		State: state, SentAt: at, StartedAt: at}}}
	if state != turn.Started {
		b.Records = []turn.Record{{Session: session, Hash: "x", At: at, Record: record.Record{State: record.State(state), Summary: "keep it?"}}}
	}
	if _, err := repo.Save(context.Background(), b, port.Event{Unit: ref.Unit(), Kind: turns.KindTurnSent, Key: "r", At: at}); err != nil {
		f.t.Fatal(err)
	}
}

func TestATurnThatWaitsOnMeIsInNeedsMe(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	repo := f.withTurns(turns.AgentObservation{WorkspaceID: "w3", Pane: "t:1", State: turn.AgentWaiting})
	at := fixtureNow.Add(-time.Hour)
	f.open(20, "fer", at, requests("carla"))
	f.open(21, "ana", at, func(pr *dinv.PR) { pr.Reasons = nil; pr.ReviewRequests = []string{"carla"} })
	f.open(22, "fer", at, requests("carla"))
	f.open(23, "fer", at, requests("carla"))
	f.turn(repo, 20, turn.Intent(dintent.Address), turn.Asked, "term-20")
	f.turn(repo, 21, turn.Intent(dintent.Review), turn.Unrecorded, "term-21")
	f.turn(repo, 22, turn.Intent(dintent.Address), turn.Started, "term-22")
	if err := repo.SaveSession(ctx, turn.Session{ID: "term-22", Pane: "t:1", WorkspaceID: "w3"}, port.Event{Kind: turns.KindSessionLaunched, Key: "term-22", At: at}); err != nil {
		t.Fatal(err)
	}
	parked := dinv.Workspace{ID: "w4", DisplayName: "feat", Status: "needs-me", Comment: "triage asks: keep it?"}
	author := dinv.Workspace{ID: "w2", DisplayName: "other", Status: "in-review"}
	if err := f.st.ReplaceWorkspaces(ctx, map[github.Ref]dinv.Workspace{ref(21): author, ref(23): parked}); err != nil {
		t.Fatal(err)
	}
	rows := f.rows("?view=" + api.ViewNeedsMe)
	got := numbers(rows)
	slices.Sort(got)
	if !slices.Equal(got, []int{20, 21, 22}) {
		t.Fatalf("needs-me %v", got)
	}
	want := map[int]api.Kind{20: "loop_waiting", 21: "loop_stopped", 22: "loop_waiting"}
	for _, r := range rows {
		if r.Kind != want[rowNumber(r)] {
			t.Fatalf("%+v", r)
		}
	}
}

func TestAnAskedReviewOutsideTheScopeIsInNeedsMe(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	repo := f.withTurns()
	f.open(444, "bruno", fixtureNow.Add(-time.Hour), func(pr *dinv.PR) { pr.Reasons = nil })
	ws := dinv.Workspace{ID: "w", DisplayName: "REVIEW fix/billing", Status: "reviewing", IsReview: true, AgentStates: []turn.AgentState{turn.AgentDone}}
	if err := f.st.ReplaceWorkspaces(ctx, map[github.Ref]dinv.Workspace{ref(444): ws}); err != nil {
		t.Fatal(err)
	}
	f.turn(repo, 444, turn.Intent(dintent.Review), turn.Asked, "term-444")
	rows := f.rows("?view=" + api.ViewNeedsMe)
	if len(rows) != 1 || rows[0].Unit != ref(444).Unit() || rows[0].Kind != "loop_waiting" || rows[0].Summary.Reason != "keep it?" {
		t.Fatalf("%+v", rows)
	}
	if got := numbers(f.rows("?view=" + api.ViewStalled)); len(got) != 0 {
		t.Fatalf("a loop workspace counted as the area: stalled %v", got)
	}
}

func TestRowCarriesTheHeadBranch(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.open(50, "ana", fixtureNow.Add(-time.Hour), func(pr *dinv.PR) { pr.HeadBranch = "ana/batch-export" })
	rows := f.rows("?view=all")
	if len(rows) != 1 || payload(rows[0]).Head != "ana/batch-export" {
		t.Fatalf("rows: %+v", rows)
	}
	rec := httptest.NewRecorder()
	f.s.Handler().ServeHTTP(rec, apiRequest("GET", "/api/rows?view=all", nil))
	if !strings.Contains(rec.Body.String(), `"head":"ana/batch-export"`) {
		t.Fatalf("JSON lacks the head: %s", rec.Body)
	}
}

func TestATrackedPullRequestIsInScopeLikeAnyOther(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	old := fixtureNow.Add(-5 * 24 * time.Hour)
	outside := func(pr *dinv.PR) { pr.Reasons, pr.ReviewRequests = nil, []string{"bob"} }
	for _, n := range []int{60, 61} {
		f.open(n, "zoe", old, outside, dinv.Event{Kind: dinv.KindReviewRequest, Key: "bob", Actor: "zoe", At: old})
	}
	f.st.SetOverride(ctx, dinv.Override{PR: ref(60), Tracked: true})
	rows := f.rows("?view=stalled")
	if len(rows) != 1 || rows[0].Unit != ref(60).Unit() || !slices.Equal(payload(rows[0]).Reasons, []string{dinv.ReasonTracked}) {
		t.Fatalf("stalled: %+v", rows)
	}
}

func TestRowHeadIsOneLineOfText(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.open(51, "ana", fixtureNow.Add(-time.Hour), func(pr *dinv.PR) { pr.HeadBranch = "ana/\u202efix\u200b-\u2066x" })
	if rows := f.rows("?view=all"); len(rows) != 1 || payload(rows[0]).Head != "ana/fix-x" {
		t.Fatalf("rows: %+q", payload(rows[0]).Head)
	}
}

func TestRowIsStableAcrossAMinute(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	old := fixtureNow.Add(-3 * time.Hour)
	edge := fixtureNow.Add(-48*time.Hour + 30*time.Second)
	f.open(74, "ana", edge, nil, dinv.Event{Kind: dinv.KindReviewRequest, Key: "fer", Actor: "ana", At: edge})
	f.open(70, "ana", old, nil, dinv.Event{Kind: dinv.KindReviewRequest, Key: "fer", Actor: "ana", At: old})
	f.open(71, "fer", old, failing)
	f.open(72, "bob", old, nil, dinv.Event{Kind: dinv.KindReviewRequest, Key: "fer", Actor: "bob", At: old},
		dinv.Event{Kind: dinv.KindMuted, Actor: "fer", At: old.Add(time.Hour)})
	if err := f.st.SetOverride(ctx, dinv.Override{PR: ref(72), MutedUntil: fixtureNow.Add(24 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	f.open(73, "bob", old, nil, dinv.Event{Kind: dinv.KindReviewRequest, Key: "fer", Actor: "bob", At: old})
	if err := f.st.SetOverride(ctx, dinv.Override{PR: ref(73), MutedUntil: fixtureNow.Add(24 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	encode := func() string {
		raw, err := json.Marshal(f.rows("?view=all"))
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	before := encode()
	f.advance(59 * time.Second)
	if after := encode(); after != before {
		t.Fatalf("rows changed within a minute:\n%s\n%s", before, after)
	}
}

// countingSessions counts the agents reads that reach the workspace manager.
type countingSessions struct {
	*turnstest.Sessions
	reads atomic.Int64
}

func (c *countingSessions) Agents(ctx context.Context) ([]turns.AgentObservation, error) {
	c.reads.Add(1)
	return c.Sessions.Agents(ctx)
}

func TestReadPathSpawnsNothing(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	log := change.NewLog(change.DefaultWindow, nil)
	f.s.deps.Changes = log
	repo := sqlite.New(f.db)
	sessions := &countingSessions{Sessions: turnstest.NewSessions()}
	f.s.deps.Turns = &turns.Service{Intents: turnstest.Intents(), Store: repo, Registry: repo, Sessions: sessions}
	f.s.deps.Sessions = sessions
	agents := []turns.AgentObservation{{WorkspaceID: "w20", Pane: "t:20", State: turn.AgentWorking}}
	var mu sync.Mutex
	f.s.deps.Agents = readmodel.AgentsFunc(func() ([]turns.AgentObservation, bool) {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(agents), true
	})
	at := fixtureNow.Add(-time.Hour)
	for _, n := range []int{20, 21, 22} {
		f.open(n, "fer", at, requests("carla"))
		handle := "term-" + strconv.Itoa(n)
		f.turn(repo, n, turn.Intent(dintent.Address), turn.Started, handle)
		s := turn.Session{ID: handle, Pane: "t:" + strconv.Itoa(n), WorkspaceID: "w" + strconv.Itoa(n)}
		if err := repo.SaveSession(ctx, s, port.Event{Kind: turns.KindSessionLaunched, Key: handle, At: at}); err != nil {
			t.Fatal(err)
		}
	}
	if got := numbers(f.rows("?view=" + api.ViewNeedsMe)); len(got) != 0 {
		t.Fatalf("needs-me %v with every agent working", got)
	}
	var d api.Detail
	if code := get(t, f.s, "/api/units/github/acme%2Fapi%2320", &d); code != 200 {
		t.Fatalf("detail: %d", code)
	}
	if rec := post(t, f.s, "/api/units/github/acme%2Fapi%2321/mute", `{"until":"2d"}`); rec.Code != 200 {
		t.Fatalf("mute: %d %s", rec.Code, rec.Body)
	}
	mu.Lock()
	agents[0].State = turn.AgentWaiting
	mu.Unlock()
	log.Note(port.TopicAgents, ref(20).Unit())
	if got := numbers(f.rows("?view=" + api.ViewNeedsMe)); !slices.Equal(got, []int{20}) {
		t.Fatalf("needs-me %v after the agents note, want [20]", got)
	}
	if n := sessions.reads.Load(); n != 0 {
		t.Fatalf("the read path asked the workspace manager for the agents %d times", n)
	}
}

func TestNoSnapshotMarksWaitingUnread(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	repo := f.withTurns(turns.AgentObservation{WorkspaceID: "w3", Pane: "t:1", State: turn.AgentWaiting})
	f.s.deps.Agents = readmodel.AgentsFunc(func() ([]turns.AgentObservation, bool) { return nil, false })
	f.open(22, "fer", fixtureNow.Add(-time.Hour), requests("carla"))
	f.turn(repo, 22, turn.Intent(dintent.Address), turn.Started, "term-22")
	if err := repo.SaveSession(ctx, turn.Session{ID: "term-22", Pane: "t:1", WorkspaceID: "w3"}, port.Event{Kind: turns.KindSessionLaunched, Key: "term-22", At: fixtureNow}); err != nil {
		t.Fatal(err)
	}
	if got := numbers(f.rows("?view=" + api.ViewNeedsMe)); len(got) != 0 {
		t.Fatalf("needs-me %v while the agents are unknown", got)
	}
	facts, err := turnFacts(f.s, ctx, f.s.loaded().Registry)
	if err != nil {
		t.Fatal(err)
	}
	if fact := facts[ref(22)]; !fact.Unread || fact.Waiting {
		t.Fatalf("started turn with unknown agents: %+v", fact)
	}
}

func TestUnknownAgentsKeepAMuteUntilStateChange(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	repo := f.withTurns()
	var mu sync.Mutex
	agents, known := []turns.AgentObservation{{WorkspaceID: "w3", Pane: "t:1", State: turn.AgentWaiting}}, true
	f.s.deps.Agents = readmodel.AgentsFunc(func() ([]turns.AgentObservation, bool) {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(agents), known
	})
	f.open(22, "fer", fixtureNow.Add(-time.Hour), requests("carla"))
	f.turn(repo, 22, turn.Intent(dintent.Address), turn.Started, "term-22")
	if err := repo.SaveSession(ctx, turn.Session{ID: "term-22", Pane: "t:1", WorkspaceID: "w3"}, port.Event{Kind: turns.KindSessionLaunched, Key: "term-22", At: fixtureNow}); err != nil {
		t.Fatal(err)
	}
	asks := f.rows("?view=all")[0]
	if asks.Kind != "loop_waiting" {
		t.Fatalf("row %s, want the agent asking", asks.Kind)
	}
	mute := dinv.Override{PR: ref(22), MuteUntilStateChange: true, MutedKind: string(asks.Kind), MutedWaitingOn: asks.Summary.WaitsOn}
	if err := f.st.SetOverride(ctx, mute); err != nil {
		t.Fatal(err)
	}
	unmuted := func() int {
		evs, err := f.st.Events(ctx, ref(22))
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, e := range evs {
			if e.Kind == dinv.KindUnmuted {
				n++
			}
		}
		return n
	}
	refresh := func() {
		t.Helper()
		if err := f.s.readModel().Refresh(ctx); err != nil {
			t.Fatal(err)
		}
	}
	kept := func(when string) {
		t.Helper()
		o, err := f.st.Override(ctx, ref(22))
		if err != nil {
			t.Fatal(err)
		}
		if !o.MuteUntilStateChange || unmuted() != 0 {
			t.Fatalf("%s: override %+v, %d unmuted events", when, o, unmuted())
		}
	}

	mu.Lock()
	agents, known = nil, false
	mu.Unlock()
	if rows := f.rows("?view=all"); len(rows) != 1 || rows[0].Kind != "muted" {
		t.Fatalf("rows while the agents are unknown: %+v", rows)
	}
	kept("a rows read")
	refresh()
	kept("the keeper's refresh")
	if err := f.s.expireMutes(ctx); err != nil {
		t.Fatal(err)
	}
	kept("expireMutes")

	mu.Lock()
	agents, known = []turns.AgentObservation{{WorkspaceID: "w3", Pane: "t:1", State: turn.AgentWaiting}}, true
	mu.Unlock()
	if rows := f.rows("?view=all"); len(rows) != 1 || rows[0].Kind != "muted" {
		t.Fatalf("rows once the agent is seen asking again: %+v", rows)
	}
	refresh()
	kept("the agents known again")

	mu.Lock()
	agents[0].State = turn.AgentWorking
	mu.Unlock()
	refresh()
	o, err := f.st.Override(ctx, ref(22))
	if err != nil {
		t.Fatal(err)
	}
	if o.MuteUntilStateChange || unmuted() != 1 {
		t.Fatalf("a known state change kept the mute: %+v, %d unmuted events", o, unmuted())
	}
}

// rowNumber is the number of a row's pull request.
func rowNumber(r api.Row) int {
	return refOf(r.Unit).Number
}

func TestTeamChangeReachesRowsInsideTheViewerTTL(t *testing.T) {
	t.Parallel()
	log := change.NewLog(change.DefaultWindow, nil)
	stDB, err := sqlitetest.Open(filepath.Join(t.TempDir(), "inv.db"), log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stDB.Close() })
	st := inventory.New(stDB)
	ctx := context.Background()
	setTeams := func(teams ...string) {
		if err := ghsource.KeepIdentity(ctx, st, identityOf("fer", teams...)); err != nil {
			t.Fatal(err)
		}
	}
	setTeams("acme/search")
	f := &fixture{t: t, st: st, db: stDB, now: fixtureNow}
	f.s = New(withStore(Deps{Changes: log, Activity: stDB, Cfg: config.Defaults(), Version: "test", Now: f.clock,
		Identity: func(ctx context.Context) (src.Identity, error) {
			teams, err := ghsource.MyTeams(ctx, st)
			return identityOf("fer", teams...), err
		},
		Log: func(string, ...any) {}}, st))
	f.s.token = ""
	stopOnCleanup(t, f.s)
	f.open(13, "bob", fixtureNow.Add(-time.Hour), requests("team:acme/web"))
	if got := numbers(f.rows("?view=needs-me")); len(got) != 0 {
		t.Fatalf("needs-me before joining acme/web: %v", got)
	}
	setTeams("acme/search", "acme/web")
	f.advance(time.Minute)
	if got := numbers(f.rows("?view=needs-me")); !slices.Equal(got, []int{13}) {
		t.Fatalf("needs-me after joining acme/web: got %v, want [13]", got)
	}
}
