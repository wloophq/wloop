package cli

import (
	"context"
	"encoding/json"
	"io"
	"time"

	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/api/client"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/round"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/intent"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/daemon"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/pr"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/meter/metertest"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

// watchLoop starts te's daemon, if not yet, and has te's fake GitHub record
// the calls made while that daemon's loop mutex is held.
func watchLoop(t *testing.T, te *testEnv) {
	t.Helper()
	if te.Socket == "" {
		te.serve(t)
	}
	te.gh.mu.Lock()
	defer te.gh.mu.Unlock()
	te.gh.loop = te.loop
}

func wantNothingUnderLock(t *testing.T, te *testEnv) {
	t.Helper()
	te.gh.mu.Lock()
	defer te.gh.mu.Unlock()
	if len(te.gh.underLock) != 0 {
		t.Fatalf("GitHub requests under the loop mutex:\n%s", strings.Join(te.gh.underLock, "\n"))
	}
	if len(te.gh.calls) == 0 {
		t.Fatal("the action asked GitHub nothing at all; the test proves nothing")
	}
}

func TestAReviewActionAsksGitHubNothingUnderTheLoop(t *testing.T) {
	t.Parallel()
	m := &fake.Manager{RepoList: []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}}}
	te := newTestEnv(t, m)
	te.gh.basics = openBasics
	watchLoop(t, te)
	if code := te.run("review", "https://github.com/acme/api/pull/7"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if !slices.Contains(m.Writes, "create REVIEW feat/x") {
		t.Fatalf("writes %v", m.Writes)
	}
	wantNothingUnderLock(t, te)
}

func TestATakeOverAsksGitHubNothingUnderTheLoop(t *testing.T) {
	t.Parallel()
	m := &fake.Manager{
		RepoList:      []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}},
		WorkspaceList: []wsdomain.Workspace{{ID: "ws-rev", RepoID: "r1", Path: "/wt/rev", DisplayName: "REVIEW feat/theirs", Status: "needs-me", HostID: "local", LinkedPR: 20}},
	}
	te := newTestEnv(t, m)
	te.gh.basics = adoptBasics
	te.git.branch["/wt/rev"] = "feat/theirs"
	watchLoop(t, te)
	if code := te.run("take-over", "https://github.com/acme/api/pull/20"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if !slices.Contains(m.Writes, "link ws-rev 20") {
		t.Fatalf("writes %v", m.Writes)
	}
	wantNothingUnderLock(t, te)
}

// actAhead runs act with what ReadAhead read of ref before, and fails t on
// any GitHub request act makes: the daemon runs act under the loop mutex.
func actAhead(t *testing.T, te *testEnv, ref github.Ref, full bool, act func(ctx context.Context) error) {
	t.Helper()
	ctx := te.ReadAhead(context.Background(), ref, full)
	mctx, sc := metertest.Start(t, ctx)
	if err := act(mctx); err != nil {
		t.Fatalf("%v\n%s", err, te.err)
	}
	metertest.Within(t, sc, metertest.Budget{{Res: meter.GitHub}: 0})
}

func TestARunNowReadAheadAsksGitHubNothing(t *testing.T) {
	t.Parallel()
	sc := followupScenario(t)
	exhaustCap(t, sc)
	sc.te.gh.basics = openBasics
	actAhead(t, sc.te, sc.ref, true, func(ctx context.Context) error {
		return envActions{sc.env, nil}.Run(ctx, sc.ref.Unit(), daemon.Call{Out: io.Discard})
	})
	if len(sc.ws.Prompts) != 1 {
		t.Fatalf("prompts %v", sc.ws.Prompts)
	}
}

func TestARemoveReadAheadAsksGitHubNothing(t *testing.T) {
	t.Parallel()
	m, te := finishedScenario(t, "MERGED")
	actAhead(t, te, r12, false, func(ctx context.Context) error {
		return envActions{e: te.Env}.Remove(ctx, r12.Unit(), daemon.Call{Out: io.Discard, WorkspaceID: doneWS})
	})
	if !slices.Contains(m.Writes, "remove ws-done") {
		t.Fatalf("writes %v", m.Writes)
	}
}

func TestATurnByHandAsksGitHubNothingUnderTheLoop(t *testing.T) {
	t.Parallel()
	_, te := handScenario(t, wsdomain.Workspace{ID: "ws-1", DisplayName: "REVIEW feat/x", LinkedPR: 7})
	watchLoop(t, te)
	if code := te.run("instructions", "review"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if !strings.Contains(te.out.String(), "# First review of acme/api#7, asked for by you.") {
		t.Fatal(te.out.String())
	}
	wantNothingUnderLock(t, te)
}

func TestALaunchedTurnOfAStoredPullRequestAsksGitHubNothing(t *testing.T) {
	t.Parallel()
	_, te := scenario(t)
	te.gh.basics = openBasics
	ctx := context.Background()
	if err := te.syncInventory(ctx); err != nil {
		t.Fatal(err)
	}
	ref := pr.Ref(r7)
	saved, err := te.repo.Save(ctx, dturn.Book{Unit: ref.Unit(), Turns: []dturn.Turn{{Intent: dturn.Intent(dintent.Review), ID: "r0", Trigger: dturn.Automated, Head: "c2", State: dturn.Unrecorded, SentAt: testNow}}},
		port.Event{Unit: ref.Unit(), Kind: turns.KindTurnSent, Key: "r0", At: testNow})
	if err != nil {
		t.Fatal(err)
	}
	mctx, sc := metertest.Start(t, ctx)
	if err := (cliLauncher{te.Env}).Launch(mctx, turns.Launch{Unit: ref.Unit(), Intent: dturn.Intent(dintent.Review), ResumeOf: saved.Turns[0].Seq, Answer: "publish"}); err != nil {
		t.Fatalf("%v\n%s", err, te.err)
	}
	metertest.Within(t, sc, metertest.Budget{{Res: meter.GitHub}: 0})
	if st := te.loadRounds(t); st.Mark(r7.String(), round.Reviewed) == "" {
		t.Fatal("the launched round marked no reviews covered")
	}
}

// askedAuthorTurn has the review workspace's agent ask the user a question
// on acme/api#7 at head c2, with the inventory holding the pull request as
// it was then, and returns the moment the answer comes.
func askedAuthorTurn(t *testing.T) (*fake.Manager, *testEnv) {
	t.Helper()
	ctx := context.Background()
	m := warmReviewWorkspace()
	te := newTestEnv(t, m)
	te.gh.basics = openBasics
	te.Rec = storeRecorder{te.Store}
	userProfile(t, te, map[string]string{"intent=review.md": "Review well."})
	if code := te.run("ask-author", r7.URL(), "--topic", "the retry cap"); code != 0 {
		t.Fatalf("ask-author: exit %d: %s", code, te.err)
	}
	handle, round := m.Prompts[0].Handle, askRound(t, m.Prompts[0].Text)
	if _, err := te.Turns.Start(ctx, handle, round); err != nil {
		t.Fatal(err)
	}
	rec := record.Record{State: record.Asked, Summary: "asked what to ask", Resume: "post the question the user picks",
		Question: &record.Question{Kind: "free", Text: "Ask whether the retry cap is configurable?"}}
	if _, err := te.Turns.RecordTurnEnd(ctx, turns.RecordRequest{Session: handle, Turn: round, Via: "mcp", Record: rec}); err != nil {
		t.Fatal(err)
	}
	if err := te.syncInventory(ctx); err != nil {
		t.Fatal(err)
	}
	te.Now = func() time.Time { return testNow.Add(2 * time.Hour) }
	return m, te
}

func answerByAction(te *testEnv) error {
	_, err := client.New(te.Socket).Action(context.Background(), r7.Unit(), "answer", api.ActionRequest{Guidance: "yes, ask it", Source: "cockpit"})
	return err
}

func TestAnAnswerAfterAPushResumesWithTheHeadMoved(t *testing.T) {
	t.Parallel()
	m, te := askedAuthorTurn(t)
	te.gh.mu.Lock()
	te.gh.basics = strings.Replace(openBasics, `"headRefOid":"c2"`, `"headRefOid":"c9"`, 1)
	te.gh.mu.Unlock()
	watchLoop(t, te)
	if err := answerByAction(te); err != nil {
		t.Fatalf("%v\n%s", err, te.err)
	}
	if len(m.Prompts) != 2 {
		t.Fatalf("prompts %+v", m.Prompts)
	}
	if text := storedText(t, te, askRound(t, m.Prompts[1].Text)); !strings.Contains(text, intent.DefaultHeadMoved) {
		t.Fatalf("the resume does not say the head moved:\n%s", text)
	}
	wantNothingUnderLock(t, te)
}

func TestAnAnswerAfterAMergeIsRefused(t *testing.T) {
	t.Parallel()
	m, te := askedAuthorTurn(t)
	te.gh.mu.Lock()
	te.gh.basics = strings.Replace(openBasics, `"state":"OPEN"`, `"state":"MERGED"`, 1)
	te.gh.mu.Unlock()
	if err := answerByAction(te); err == nil || !strings.Contains(err.Error(), "is MERGED") {
		t.Fatalf("err = %v", err)
	}
	if len(m.Prompts) != 1 {
		t.Fatalf("a turn was launched on a merged pull request: %+v", m.Prompts)
	}
}

func TestARunNowWithoutAReviewWorkspaceReadsOnlyTheBasicsAhead(t *testing.T) {
	t.Parallel()
	_, te := scenario(t)
	te.gh.basics = strings.NewReplacer(`"number":7`, `"number":11`, `"headRefName":"feat/x"`, `"headRefName":"feat/unreviewed"`).Replace(openBasics)
	if err := te.Store.SetViewer(context.Background(), unit.GitHub, "me"); err != nil {
		t.Fatal(err)
	}
	ctx, sc := metertest.Start(t, context.Background())
	te.ReadAhead(ctx, github.Ref{Owner: "acme", Repo: "api", Number: 11}, true)
	metertest.Within(t, sc, metertest.Budget{{Res: meter.GitHub}: 1})
}

func TestAnActionRefusesAPullRequestTheInventorySawCloseWhileItWaited(t *testing.T) {
	t.Parallel()
	_, te := scenario(t)
	te.gh.basics = openBasics
	ctx := te.ReadAhead(context.Background(), r7, false)
	editReviewed(t, te, "pr0", func(pr obj) {
		pr["state"], pr["updatedAt"] = "CLOSED", testNow.Add(time.Minute).UTC().Format(time.RFC3339)
	})
	if err := te.syncInventory(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := te.basics(ctx, r7); err == nil || !strings.Contains(err.Error(), "changed on GitHub while this waited for the loop") {
		t.Fatalf("err = %v", err)
	}
}

// editReviewed changes the pull request under review at alias in te's fake
// GitHub, as a push or a close after the collector's last read would.
func editReviewed(t *testing.T, te *testEnv, alias string, edit func(pr obj)) {
	t.Helper()
	te.gh.mu.Lock()
	defer te.gh.mu.Unlock()
	var payload struct {
		Data map[string]obj `json:"data"`
	}
	if err := json.Unmarshal([]byte(te.gh.reviewer), &payload); err != nil {
		t.Fatal(err)
	}
	edit(payload.Data[alias]["pullRequest"].(obj))
	te.gh.reviewer = mustJSON(t, obj{"data": payload.Data})
}
