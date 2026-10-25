package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/turns"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/record"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/turn"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

type obj = map[string]any

func nodes(items ...obj) obj {
	if items == nil {
		items = []obj{}
	}
	return obj{"nodes": items}
}

func reviewerPR(number int, state string, reviews, comments []obj) obj {
	return obj{"pullRequest": obj{
		"number": number, "url": "", "title": "t", "state": state, "isDraft": false,
		"headRefName": "b", "headRefOid": "c2", "isCrossRepository": false, "authorAssociation": "MEMBER",
		"repository":          obj{"nameWithOwner": "acme/api"},
		"headRepositoryOwner": obj{"login": "acme"},
		"author":              obj{"login": "alice"},
		"reviews":             nodes(reviews...),
		"comments":            nodes(comments...),
		"reviewRequests":      nodes(),
		"commits":             nodes(obj{"commit": obj{"oid": "c2", "parents": obj{"totalCount": 1}}}),
	}}
}

func authorPR(number int, branch, decision, crOid string) obj {
	cr := nodes()
	if crOid != "" {
		cr = nodes(obj{"id": "x", "state": "CHANGES_REQUESTED", "submittedAt": "2026-10-08T10:00:00Z", "author": obj{"login": "bob"}, "commit": obj{"oid": crOid}})
	}
	return obj{
		"number": number, "url": "", "title": "t", "state": "OPEN", "isDraft": false,
		"headRefName": branch, "headRefOid": "h" + branch, "reviewDecision": decision,
		"repository":          obj{"nameWithOwner": "acme/api"},
		"headRepositoryOwner": obj{"login": "acme"},
		"author":              obj{"login": "me"},
		"latestReviews":       nodes(),
		"changesRequested":    cr,
		"headCommit":          nodes(obj{"commit": obj{"oid": "h" + branch, "statusCheckRollup": obj{"state": "SUCCESS"}}}),
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

const myReviewAt = "2026-10-08T12:00:00Z"

func scenario(t *testing.T) (*fake.Manager, *testEnv) {
	t.Helper()
	m := &fake.Manager{
		RepoList: []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}},
		WorkspaceList: []wsdomain.Workspace{
			{ID: "ws-rev", RepoID: "r1", Path: "/wt/rev", DisplayName: "REVIEW feat/x", Status: "reviewing", HostID: "local", LinkedPR: 7},
			{ID: "ws-merged", RepoID: "r1", Path: "/wt/merged", DisplayName: "REVIEW fix/y", Status: "reviewing", HostID: "local", LinkedPR: 8},
			{ID: "ws-done", RepoID: "r1", Path: "/wt/done", DisplayName: "REVIEW old", Status: "completed", HostID: "local", LinkedPR: 6},
			{ID: "ws-remote", RepoID: "r1", Path: "/wt/remote", DisplayName: "REVIEW far", Status: "reviewing", HostID: "ssh:box", LinkedPR: 5},
			{ID: "ws-auth", RepoID: "r1", Path: "/wt/auth", DisplayName: "feat-z", Branch: "feat/z", Status: "in-review", HostID: "local"},
			{ID: "ws-cr", RepoID: "r1", Path: "/wt/cr", DisplayName: "fix-w", Branch: "fix/w", Status: "in-review", HostID: "local", LinkedPR: 10},
			{ID: "ws-main", RepoID: "r1", Path: "/src/api", DisplayName: "api", HostID: "local", IsMain: true},
		},
		Sessions:   map[string]string{"ws-rev": "term-rev"},
		LastOutput: map[string]time.Time{"ws-rev": testNow.Add(-10 * time.Minute)},
	}
	te := newTestEnv(t, m)
	myReview := obj{"id": "r", "state": "CHANGES_REQUESTED", "submittedAt": myReviewAt, "author": obj{"login": "me"}, "commit": obj{"oid": "c2"}}
	authorComment := obj{"id": "c", "createdAt": "2026-10-08T13:00:00Z", "author": obj{"login": "alice"}}
	te.gh.reviewer = mustJSON(t, obj{"data": obj{
		"viewer": obj{"login": "me"},
		"pr0":    reviewerPR(7, "OPEN", []obj{myReview}, []obj{authorComment}),
		"pr1":    reviewerPR(8, "MERGED", nil, nil),
		"pr2":    reviewerPR(6, "MERGED", nil, nil),
	}})
	te.gh.author = mustJSON(t, obj{"data": obj{
		"viewer": obj{"login": "me"},
		"open":   nodes(authorPR(9, "feat/z", "APPROVED", ""), authorPR(10, "fix/w", "CHANGES_REQUESTED", "hfix/w")),
		"recent": nodes(),
	}})
	te.git.head["/wt/auth"] = "hfeat/z"
	return m, te
}

func seedRound(t *testing.T, te *testEnv, key string) {
	t.Helper()
	st, err := te.Rounds.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	st.RecordFollowup(key, "", testNow)
	if err := te.Rounds.Save(context.Background(), st); err != nil {
		t.Fatal(err)
	}
}

func TestReviewByHandKeepsAColdSession(t *testing.T) {
	t.Parallel()
	m, te := scenario(t)
	m.LastOutput["ws-rev"] = testNow.Add(-2 * time.Hour)
	te.gh.basics = openBasics
	if code := te.run("review", "https://github.com/acme/api/pull/7"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if writes := strings.Join(m.Writes, "\n"); strings.Contains(writes, "session ws-rev") || !strings.Contains(writes, "send term-rev") {
		t.Fatalf("writes:\n%s", writes)
	}
}

func TestFailingCommandStampsItsError(t *testing.T) {
	t.Parallel()
	_, te := scenario(t)
	if code := te.run("address", "not-a-url"); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if want := testNow.Format(time.DateTime) + " error: "; !strings.HasPrefix(te.err.String(), want) {
		t.Fatalf("stderr = %q, want prefix %q", te.err, want)
	}
}

type memRecorder struct {
	mu        sync.Mutex
	events    []dinv.Event
	overrides map[github.Ref]dinv.Override
	// failures fails that many Record calls first.
	failures  int
	questions map[github.Ref]map[string]bool
}

func (m *memRecorder) OpenQuestions(map[unit.Unit]dturn.Book) map[github.Ref]map[string]bool {
	return m.questions
}

func (m *memRecorder) Record(_ context.Context, ev dinv.Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failures > 0 {
		m.failures--
		return errors.New("database is locked")
	}
	if ev.Kind == dinv.KindRoundSkipped && ev.Key != "" && slices.ContainsFunc(m.events, func(e dinv.Event) bool { return e.PR == ev.PR && e.Kind == ev.Kind && e.Key == ev.Key }) {
		return nil
	}
	m.events = append(m.events, ev)
	return nil
}

func (m *memRecorder) RecordUnit(ctx context.Context, ev port.Event) error {
	ref, ok := port.PROf(ev)
	if !ok {
		return fmt.Errorf("the memory recorder keeps pull requests only, not %s", ev.Unit)
	}
	return m.Record(ctx, dinv.Event{PR: ref, Kind: ev.Kind, Key: ev.Key, At: ev.At, Data: ev.Data})
}

func (m *memRecorder) FirstRoundBlocks(context.Context) (map[unit.Unit]port.Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	byPR := map[github.Ref][]dinv.Event{}
	for _, e := range m.events {
		byPR[e.PR] = append(byPR[e.PR], e)
	}
	out := map[unit.Unit]port.Event{}
	for ref, evs := range byPR {
		if b, ok := dinv.ActiveBlock(evs, "me"); ok {
			out[ref.Unit()] = port.Event{Unit: ref.Unit(), Kind: b.Kind, Key: b.Key, Data: b.Data, At: b.At}
		}
	}
	return out, nil
}

func (m *memRecorder) Overrides(context.Context) (map[github.Ref]dinv.Override, error) {
	return m.overrides, nil
}

func (m *memRecorder) KeepNextGuidance(_ context.Context, ref github.Ref, guidance string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.overrides == nil {
		m.overrides = map[github.Ref]dinv.Override{}
	}
	o := m.overrides[ref]
	if o.NextGuidance == "" {
		o.PR, o.NextGuidance = ref, guidance
		m.overrides[ref] = o
	}
	return nil
}

func (m *memRecorder) TakeNextGuidance(_ context.Context, ref github.Ref) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.overrides[ref]
	if !ok {
		return "", nil
	}
	guidance := o.NextGuidance
	o.NextGuidance = ""
	m.overrides[ref] = o
	return guidance, nil
}

func (m *memRecorder) kinds() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for _, e := range m.events {
		out = append(out, e.Kind)
	}
	return out
}

type sweepCase struct {
	env *Env
	te  *testEnv
	ws  *fake.Manager
	ref github.Ref
}

func asSweep(m *fake.Manager, te *testEnv) sweepCase {
	return sweepCase{env: te.Env, te: te, ws: m}
}

// followupScenario keeps only the review workspace whose author answered, so
// the sweep plans exactly one follow-up.
func followupScenario(t *testing.T) sweepCase {
	t.Helper()
	sc := asSweep(scenario(t))
	sc.ws.WorkspaceList = slices.DeleteFunc(sc.ws.WorkspaceList, func(w wsdomain.Workspace) bool { return w.ID != "ws-rev" })
	sc.ref = github.Ref{Owner: "acme", Repo: "api", Number: 7}
	return sc
}

func addressScenario(t *testing.T) sweepCase {
	t.Helper()
	sc := asSweep(reviewedAuthorScenario(t))
	sc.ref = github.Ref{Owner: "acme", Repo: "api", Number: 9}
	return sc
}

func exhaustCap(t *testing.T, sc sweepCase) {
	t.Helper()
	sc.te.cfg.Rounds.PerPRPerDay = 1
	seedRound(t, sc.te, sc.ref.String())
}

func TestSweepRecordsDistinctTimesPerPRAndKind(t *testing.T) {
	t.Parallel()
	store := openInventory(t, t.TempDir())
	s := &sweep{e: &Env{Rec: storeRecorder{store: store}}, now: testNow}
	st := step{pr: github.Ref{Owner: "acme", Repo: "api", Number: 7}}
	s.record(context.Background(), st, dinv.KindStatus, "reviewed")
	s.record(context.Background(), st, dinv.KindStatus, "completed")
	s.record(context.Background(), st, dinv.KindRoundSent, "")
	evs, err := store.Events(context.Background(), st.pr)
	if err != nil || len(evs) != 3 {
		t.Fatalf("events: %+v %v", evs, err)
	}
	var status []time.Time
	for _, ev := range evs {
		switch ev.Kind {
		case dinv.KindStatus:
			status = append(status, ev.At)
		case dinv.KindRoundSent:
			if !ev.At.Equal(testNow) {
				t.Fatalf("round_sent at %v, want %v", ev.At, testNow)
			}
		}
	}
	if len(status) != 2 || status[0].Equal(status[1]) {
		t.Fatalf("status events share the time %v, the inventory would keep one", status)
	}
}

func openInventory(t *testing.T, dir string) *inventory.Store {
	t.Helper()
	stDB, err := sqlitetest.Open(filepath.Join(dir, "store.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stDB.Close() })
	return inventory.New(stDB)
}

// saveReview stores a done review turn at head c1 whose record published a
// verdict.
func saveReview(t *testing.T, te *testEnv, ref github.Ref, trigger dturn.Trigger) {
	t.Helper()
	saveReviewAt(t, te, ref, trigger, testNow.Add(-2*time.Hour))
}

func saveReviewAt(t *testing.T, te *testEnv, ref github.Ref, trigger dturn.Trigger, at time.Time) {
	t.Helper()
	round := strings.ReplaceAll(ref.String(), "/", "-")
	b := dturn.Book{Unit: pr.Ref(ref).Unit(), Turns: []dturn.Turn{{Intent: dturn.Intent(dintent.Review), ID: round, Trigger: trigger, Head: "c1", Session: "term-old",
		State: dturn.Done, SentAt: at, StartedAt: at, EndedAt: at.Add(time.Minute)}}}
	b.Records = []dturn.Record{{Session: "term-old", Hash: "h-" + round, Head: "c1", At: at.Add(time.Minute), Record: record.Record{State: record.Done, Summary: "asked for a test",
		Outcome: &record.Outcome{Verdict: "request_changes", URL: ref.URL()}}}}
	if _, err := te.repo.Save(context.Background(), b, port.Event{Unit: pr.Ref(ref).Unit(), Kind: turns.KindTurnSent, Key: round, At: at}); err != nil {
		t.Fatal(err)
	}
	te.Rec = storeRecorder{te.Store}
}

// setReviewer answers the reviewer read with one pull request per ref, in
// order, each with my review on c1 and the author's comment after it, edited
// by edit.
func setReviewer(t *testing.T, te *testEnv, refs []github.Ref, edit func(obj)) {
	t.Helper()
	data := obj{"viewer": obj{"login": "me"}}
	for i, ref := range refs {
		myReview := obj{"id": "r", "state": "CHANGES_REQUESTED", "submittedAt": myReviewAt, "author": obj{"login": "me"}, "commit": obj{"oid": "c1"}}
		p := reviewerPR(ref.Number, "OPEN", []obj{myReview}, nil)["pullRequest"].(obj)
		p["repository"] = obj{"nameWithOwner": ref.Owner + "/" + ref.Repo}
		p["headRefName"] = "feat/x"
		if edit != nil {
			edit(p)
		}
		data[fmt.Sprintf("pr%d", i)] = obj{"pullRequest": p}
	}
	te.gh.reviewer = mustJSON(t, obj{"data": data})
}

// lostWorkspaceScenario is a pull request I reviewed in a workspace the user
// deleted, with every session closed, and a push since.
func lostWorkspaceScenario(t *testing.T) sweepCase {
	t.Helper()
	sc := followupScenario(t)
	sc.ws.WorkspaceList, sc.ws.Sessions = nil, nil
	sc.te.gh.basics = openBasics
	saveReview(t, sc.te, sc.ref, dturn.Automated)
	setReviewer(t, sc.te, []github.Ref{sc.ref}, nil)
	return sc
}

func TestALeftoverBranchAtAStoredHeadIsResetOnlyWithTheFlag(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name   string
		flag   bool
		tip    string
		create bool
	}{{"stored head, flag on", true, "c1", true}, {"stored head, flag off", false, "c1", false}, {"unknown tip, flag on", true, "c9", false}} {
		t.Run(c.name, func(t *testing.T) {
			sc := lostWorkspaceScenario(t)
			sc.te.cfg.FollowupsWithoutWorkspace = c.flag
			sc.te.git.tips["feat/x"] = c.tip
			pr, err := sc.te.openPR(context.Background(), sc.ref)
			if err != nil {
				t.Fatal(err)
			}
			sent, err := sc.te.sendTurn(context.Background(), sc.te.cfg, turn.Request{Intent: turn.Intent(dintent.Review), Trigger: turn.ByHand, Unit: sc.ref.Unit(), Payload: turnPR(pr)})
			if err != nil {
				t.Fatal(err)
			}
			_, err = sc.te.openReview(context.Background(), sc.te.cfg, sc.ref, pr, sent, roundOptions{allowUserActive: true})
			created := slices.Contains(sc.ws.Writes, "create REVIEW feat/x")
			if created != c.create || (err == nil) != c.create {
				t.Fatalf("created %v, err %v, writes %v", created, err, sc.ws.Writes)
			}
			if want := map[bool]string{true: "c2", false: c.tip}[c.create]; sc.te.git.tips["feat/x"] != want {
				t.Fatalf("branch at %s, want %s", sc.te.git.tips["feat/x"], want)
			}
		})
	}
}
