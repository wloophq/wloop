package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/profile"
	"github.com/wloophq/wloop/internal/turn"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

// agentDaemon serves te's store and turns over a socket, recording the
// loop's events, and points te's command line at it.
func agentDaemon(t *testing.T, te *testEnv) {
	t.Helper()
	te.Rec = storeRecorder{te.Store}
	te.serve(t)
}

// inSession gives te the environment of an Orca agent session on handle.
func inSession(te *testEnv, handle string) {
	env := map[string]string{"ORCA_TERMINAL_HANDLE": handle, "ORCA_PANE_KEY": "tab-1:leaf-1", "CLAUDE_CODE_SESSION_ID": "sess-1"}
	te.Getenv = func(k string) string { return env[k] }
}

// sessionOf is the session the loop took the manager's handle as.
func (te *testEnv) sessionOf(t *testing.T, manager, handle string) string {
	t.Helper()
	id, ok, err := te.reg.SessionOf(context.Background(), manager, handle)
	if err != nil || !ok {
		t.Fatalf("no session of %s %s: %v", manager, handle, err)
	}
	return id
}

func TestInstructionsByRoundIgnoresLaterEdits(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	agentDaemon(t, te)
	user := t.TempDir()
	os.WriteFile(filepath.Join(user, "intent=review.md"), []byte("FIRST"), 0o600)
	te.cfg.Profile = user
	sent, err := te.sendTurn(context.Background(), te.cfg, turn.Request{Intent: turn.Intent(dintent.Review), Unit: r7.Unit(), Payload: &turn.PR{}})
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(user, "intent=review.md"), []byte("SECOND"), 0o600)
	if code := te.run("instructions", "review", r7.URL(), "--turn", sent.ID); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if out := te.out.String(); !strings.Contains(out, "FIRST") || strings.Contains(out, "SECOND") || !strings.HasPrefix(out, "Instructions "+sent.ID) {
		t.Fatalf("%s", out)
	}
}

func TestInstructionsUnknownRound(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	agentDaemon(t, te)
	if code := te.run("instructions", "review", r7.URL(), "--turn", "deadbeef0000"); code == 0 || !strings.Contains(te.err.String(), "deadbeef0000") {
		t.Fatalf("exit %d: %s", code, te.err)
	}
}

func TestInstructionsCurrentNamesTheExecutable(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	agentDaemon(t, te)
	te.Executable = "/Apps/My App/" + brand.Name
	te.gh.basics = openBasics
	user := t.TempDir()
	os.WriteFile(filepath.Join(user, "intent=review.md"), []byte("MY CRITERIA"), 0o600)
	te.cfg.Profile = user
	if code := te.run("instructions", "review", r7.URL()); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	out := te.out.String()
	if !regexp.MustCompile(`^Instructions [0-9a-f]{12} for review in ` + r7.Owner + `/` + r7.Repo + `\.`).MatchString(out) {
		t.Fatalf("first line: %q", strings.SplitN(out, "\n", 2)[0])
	}
	if !strings.Contains(out, "MY CRITERIA") || strings.Contains(out, "{{command}}") || !strings.Contains(out, `'/Apps/My App/`+brand.Name+`' turn end `) {
		t.Fatalf("%s", out)
	}
}

func TestInstructionsStopsOnAMissingProfile(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	te.cfg.Profile = "/nope"
	te.gh.basics = openBasics
	_, err := te.sendTurn(context.Background(), te.cfg, turn.Request{Intent: turn.Intent(dintent.Review), Unit: r7.Unit(), Payload: &turn.PR{}})
	var m profile.MissingError
	if !errors.As(err, &m) {
		t.Fatalf("%v", err)
	}
	if code := te.run("instructions", "review", r7.URL()); code == 0 || te.out.Len() != 0 {
		t.Fatalf("exit %d, out %q", code, te.out)
	}
}

func TestInstructionsRejectsAnUnknownKind(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	if code := te.run("instructions", "merge", r7.URL()); code == 0 {
		t.Fatal("accepted kind merge")
	}
}

func handScenario(t *testing.T, ws wsdomain.Workspace) (*fake.Manager, *testEnv) {
	t.Helper()
	ws.Path, ws.RepoID, ws.HostID = t.TempDir(), "r1", "local"
	m := &fake.Manager{RepoList: []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}}, WorkspaceList: []wsdomain.Workspace{ws}}
	te := newTestEnv(t, m)
	te.gh.basics = openBasics
	agentDaemon(t, te)
	te.Harness.Cwd = func() string { return ws.Path }
	return m, te
}

func TestInstructionsByHandFindsThePullRequestOfTheWorktree(t *testing.T) {
	t.Parallel()
	_, te := handScenario(t, wsdomain.Workspace{ID: "ws-1", DisplayName: "REVIEW feat/x", LinkedPR: 7})
	if code := te.run("instructions", "review", "--", "look", "at", "the", "migration"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	out := te.out.String()
	if !strings.Contains(out, "# First review of acme/api#7, asked for by you.") || !strings.Contains(out, "look at the migration") {
		t.Fatal(out)
	}
	st := te.Store
	evs, _ := st.Events(context.Background(), r7)
	var kinds []string
	for _, ev := range evs {
		kinds = append(kinds, ev.Kind+" "+ev.Data)
	}
	if !slices.ContainsFunc(kinds, func(k string) bool {
		return strings.HasPrefix(k, dinv.KindRoundSent+" asked by you in a session (instructions ")
	}) ||
		!slices.Contains(kinds, dinv.KindGuidance+" look at the migration") {
		t.Fatalf("events %q", kinds)
	}
}

func TestInstructionsByHandWithoutAPullRequestRecordsNothing(t *testing.T) {
	t.Parallel()
	m := &fake.Manager{}
	te := newTestEnv(t, m)
	agentDaemon(t, te)
	dir := t.TempDir()
	te.Harness.Cwd = func() string { return dir }
	if code := te.run("instructions", "review"); code == 0 || !strings.Contains(te.err.String(), "no unit here; pass its target") || te.out.Len() != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if st := te.loadRounds(t); len(st.PRs) != 0 || len(m.Writes) != 0 {
		t.Fatalf("recorded: %v %v", st.PRs, m.Writes)
	}
}

func TestInstructionsByHandLinksAnUnlinkedReviewWorktree(t *testing.T) {
	t.Parallel()
	m, te := handScenario(t, wsdomain.Workspace{ID: "ws-2", DisplayName: "REVIEW feat/x"})
	if code := te.run("instructions", "review", r7.URL()); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if !slices.Contains(m.Writes, "link ws-2 7") || !strings.Contains(te.err.String(), "this worktree is now the review workspace of acme/api#7") {
		t.Fatalf("writes %v err %s", m.Writes, te.err)
	}
}

func TestInstructionsByHandNeverRelinksAWorktree(t *testing.T) {
	t.Parallel()
	m, te := handScenario(t, wsdomain.Workspace{ID: "ws-3", DisplayName: "REVIEW other", LinkedPR: 8})
	if code := te.run("instructions", "review", r7.URL()); code == 0 || len(m.Writes) != 0 || te.out.Len() != 0 {
		t.Fatalf("exit %d writes %v out %s", code, m.Writes, te.out)
	}
}

func TestInstructionsByHandLeavesAWorktreeOfAnotherRoleUnlinked(t *testing.T) {
	t.Parallel()
	m, te := handScenario(t, wsdomain.Workspace{ID: "ws-4", DisplayName: "feat-x"})
	if code := te.run("instructions", "review", r7.URL()); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if len(m.Writes) != 0 || !strings.Contains(te.err.String(), "this worktree is not linked to acme/api#7") {
		t.Fatalf("writes %v err %s", m.Writes, te.err)
	}
}

func TestAddressByHandOnSomeoneElsesPullRequestAsksAndChangesNothing(t *testing.T) {
	t.Parallel()
	m, te := handScenario(t, wsdomain.Workspace{ID: "ws-5", DisplayName: "feat-x"})
	te.gh.basics = addressBasics(t, obj{"__typename": "User", "login": "jo"})
	url := "https://github.com/acme/api/pull/21"
	if code := te.run("instructions", "address", url); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if out := te.out.String(); !strings.Contains(out, "Ask the user whether to take it over") || strings.Contains(out, "### 1. Sync") {
		t.Fatal(out)
	}
	st := te.Store
	if evs, _ := st.Events(context.Background(), pr.Ref{Owner: "acme", Repo: "api", Number: 21}); len(evs) != 0 || len(m.Writes) != 0 {
		t.Fatalf("events %v writes %v", evs, m.Writes)
	}
}

func TestAddressByHandTakeOverTakesOverThenRendersTheRound(t *testing.T) {
	t.Parallel()
	m, te := handScenario(t, wsdomain.Workspace{ID: "ws-rev", DisplayName: "REVIEW feat/theirs", LinkedPR: 20})
	te.gh.basics = adoptBasics
	url := "https://github.com/acme/api/pull/20"
	if code := te.run("instructions", "address", url, "--take-over", "--", "fix", "the", "nit"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if out := te.out.String(); !strings.Contains(out, "# Take over acme/api#20 and address it, asked for by you.") || !strings.Contains(out, "fix the nit") || strings.Contains(out, "rename") {
		t.Fatal(out)
	}
	if got := strings.Join(m.Writes, "\n"); !strings.HasPrefix(got, "rename ws-rev feat/theirs\nlink ws-rev 20") {
		t.Fatalf("writes:\n%s", got)
	}
}

func TestTakeOverByHandWritesTheDaemonsStore(t *testing.T) {
	t.Parallel()
	_, te := handScenario(t, wsdomain.Workspace{ID: "ws-rev", DisplayName: "REVIEW feat/theirs", LinkedPR: 20})
	te.gh.basics = adoptBasics
	blocked := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocked, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	te.StateDir = filepath.Join(blocked, "state")
	ref := pr.Ref{Owner: "acme", Repo: "api", Number: 20}
	if code := te.run("instructions", "address", ref.URL(), "--take-over"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if o, err := te.Store.Override(context.Background(), ref); err != nil || !o.Adopted {
		t.Fatalf("override %+v %v", o, err)
	}
}

func TestAddressByHandTakeOverElsewhereNamesTheWorktree(t *testing.T) {
	t.Parallel()
	m := &fake.Manager{RepoList: []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}}}
	te := newTestEnv(t, m)
	te.gh.basics = adoptBasics
	te.git.branch["/worktrees/feat/theirs"] = "feat/theirs"
	agentDaemon(t, te)
	dir := t.TempDir()
	te.Harness.Cwd = func() string { return dir }
	if code := te.run("instructions", "address", "--take-over", "https://github.com/acme/api/pull/20"); code == 0 || !strings.Contains(te.err.String(), "run this command there") || !strings.Contains(te.err.String(), "/worktrees/feat/theirs") || te.out.Len() != 0 {
		t.Fatalf("exit %d out %s err %s", code, te.out, te.err)
	}
}

func TestTakeOverAppliesOnlyToAddress(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	if code := te.run("instructions", "review", r7.URL(), "--take-over"); code == 0 || !strings.Contains(te.err.String(), "--take-over applies to address") {
		t.Fatalf("exit %d: %s", code, te.err)
	}
}

func TestGuidanceAfterTheSeparatorKeepsItsLeadingDash(t *testing.T) {
	t.Parallel()
	_, te := handScenario(t, wsdomain.Workspace{ID: "ws-1", DisplayName: "REVIEW feat/x", LinkedPR: 7})
	if code := te.run("instructions", "review", r7.URL(), "--", "-x", "first"); code != 0 || !strings.Contains(te.out.String(), "\n-x first\n") {
		t.Fatalf("exit %d: %s%s", code, te.out, te.err)
	}
}

func TestARoundTakesNoGuidance(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	if code := te.run("instructions", "review", r7.URL(), "--turn", "abc123abc123", "--", "more"); code == 0 || !strings.Contains(te.err.String(), "takes no input flags and no guidance") {
		t.Fatalf("exit %d: %s", code, te.err)
	}
}

func TestAnAllowedRoundFormNeverRunsATurnByHand(t *testing.T) {
	t.Parallel()
	for name, args := range map[string][]string{
		"empty turn":          {"--turn", "", r7.URL()},
		"old flag":            {"--sha=", r7.URL()},
		"turn then agent":     {"--turn", "--agent", r7.URL()},
		"turn then guidance":  {"--turn", "abc123abc123", r7.URL(), "--", "x"},
		"turn then take-over": {"--turn", "abc123abc123", "--take-over", r7.URL()},
	} {
		_, te := handScenario(t, wsdomain.Workspace{ID: "ws-1", DisplayName: "REVIEW feat/x", LinkedPR: 7})
		if code := te.run(append([]string{"instructions", "review"}, args...)...); code == 0 || te.out.Len() != 0 {
			t.Errorf("%s: exit %d out %s", name, code, te.out)
		}
		if s, _ := te.Rounds.Load(context.Background()); len(s.PRs[r7.String()].Marks) != 0 {
			t.Errorf("%s: recorded a round", name)
		}
	}
}

func TestGuidanceNamingAStoredTurnFlagIsGuidance(t *testing.T) {
	t.Parallel()
	_, te := handScenario(t, wsdomain.Workspace{ID: "ws-1", DisplayName: "REVIEW feat/x", LinkedPR: 7})
	if code := te.run("instructions", "review", r7.URL(), "--", "check", "--sha", "handling"); code != 0 || !strings.Contains(te.out.String(), "\ncheck --sha handling\n") {
		t.Fatalf("exit %d: %s%s", code, te.out, te.err)
	}
}

func TestTurnByHandStartsOnTheCaller(t *testing.T) {
	t.Parallel()
	_, te := handScenario(t, wsdomain.Workspace{ID: "ws-1", DisplayName: "REVIEW feat/x", LinkedPR: 7})
	inSession(te, "term_1")
	if code := te.run("instructions", "review", r7.URL()); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	b := bookOf(t, te, r7)
	if len(b.Turns) != 1 {
		t.Fatalf("turns %+v", b.Turns)
	}
	id := te.sessionOf(t, "orca", "term_1")
	if got := b.Turns[0]; got.State != dturn.Started || got.Session != id || got.Trigger != dturn.ByHand || !strings.HasPrefix(te.out.String(), "Instructions "+got.ID) {
		t.Fatalf("turn %+v, out:\n%s", got, te.out)
	}
	if s, ok, _ := te.reg.Registry.Session(context.Background(), id); !ok || s.Pane != "tab-1:leaf-1" || s.AgentSession != "sess-1" || s.Handle != "term_1" {
		t.Fatalf("session %+v %v", s, ok)
	}
}

func TestNudgeByHandStartsItsTurn(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	te.gh.basics = openBasics
	ctx := context.Background()
	if err := te.Store.UpsertPR(ctx, dinv.PR{Ref: r7, Title: "Batch export", Author: "ana", State: "OPEN", HeadOid: "h1", Reasons: []string{"repo"}, URL: r7.URL()}); err != nil {
		t.Fatal(err)
	}
	if _, err := te.Store.AppendEvents(ctx, []dinv.Event{{PR: r7, Kind: dinv.KindPush, Key: "h1", Actor: "ana", At: testNow.Add(-72 * time.Hour)}}); err != nil {
		t.Fatal(err)
	}
	agentDaemon(t, te)
	inSession(te, "term_1")
	if code := te.run("instructions", "nudge", r7.URL(), "--", "be", "brief"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	b := bookOf(t, te, r7)
	if len(b.Turns) != 1 {
		t.Fatalf("turns %+v", b.Turns)
	}
	if got := b.Turns[0]; got.Intent != dturn.Intent(dintent.Nudge) || got.State != dturn.Started || got.Session != te.sessionOf(t, "orca", "term_1") || got.Trigger != dturn.ByHand {
		t.Fatalf("turn %+v", got)
	}
	if out := te.out.String(); !strings.Contains(out, "be brief") || !strings.Contains(out, "@ana") {
		t.Fatalf("out:\n%s", out)
	}
}

func bookOf(t *testing.T, te *testEnv, ref pr.Ref) dturn.Book {
	t.Helper()
	books, err := te.Turns.Store.Books(context.Background(), pr.Ref(ref).Unit())
	if err != nil {
		t.Fatal(err)
	}
	return books[pr.Ref(ref).Unit()]
}

// hookDaemon answers the agent API's session calls with stop, and keeps the
// requests it reads.
type hookDaemon struct {
	mu      sync.Mutex
	stop    api.StopAnswer
	paths   []string
	handles []string
	bodies  []string
}

func (d *hookDaemon) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	d.mu.Lock()
	d.paths = append(d.paths, r.URL.Path)
	d.handles = append(d.handles, r.Header.Get(api.SessionHeader))
	d.bodies = append(d.bodies, string(body))
	d.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == api.AgentSessionsStop {
		_ = json.NewEncoder(w).Encode(d.stop)
		return
	}
	_, _ = w.Write([]byte("{}"))
}

// withStdin runs f with the process's standard input reading s.
func withStdin(t *testing.T, s string, f func()) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "stdin")
	if err := os.WriteFile(path, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
	in, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	saved := os.Stdin
	os.Stdin = in
	defer func() { os.Stdin = saved }()
	f()
}

func hookSession(te *testEnv, d *hookDaemon) {
	env := map[string]string{"ORCA_TERMINAL_HANDLE": "term_1", "ORCA_PANE_KEY": "tab:leaf", "ORCA_WORKTREE_ID": "w1", "CLAUDE_CONFIG_DIR": "/h", "CLAUDE_CODE_SESSION_ID": "s"}
	te.Getenv = func(k string) string { return env[k] }
	te.Socket = serveSocket(te.t, d)
}

func TestHookStopOutputIsPinned(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	d := &hookDaemon{stop: api.StopAnswer{Block: true, Reason: "owe a record"}}
	hookSession(te, d)
	code := -1
	withStdin(t, `{"session_id":"s","prompt_id":"p","last_assistant_message":"m","transcript_path":"/t/s.jsonl"}`, func() { code = te.run("hook", "stop") })
	if code != 0 || te.out.String() != `{"decision":"block","reason":"owe a record"}`+"\n" {
		t.Fatalf("exit %d, out %q, err %q", code, te.out.String(), te.err.String())
	}
	if len(d.bodies) != 1 || d.paths[0] != api.AgentSessionsStop || d.handles[0] != "term_1" || d.bodies[0] != `{"agent_session":"s","prompt_id":"p","last_message":"m","agent":"claude","transcript":"/t/s.jsonl"}` {
		t.Fatalf("requests %q %q %q", d.paths, d.handles, d.bodies)
	}
}

func TestHookWithAnUnknownAgentAllows(t *testing.T) {
	for event, says := range map[string]string{"stop": brand.Command + " hook stop: allowing:", "session-start": brand.Command + " hook session-start: going on:"} {
		te := newTestEnv(t, &fake.Manager{})
		d := &hookDaemon{stop: api.StopAnswer{Block: true, Reason: "owe a record"}}
		hookSession(te, d)
		code := -1
		withStdin(t, `{"session_id":"s"}`, func() { code = te.run("hook", event, "--agent", "nope") })
		if code != 0 || te.out.Len() != 0 || len(d.bodies) != 0 || !strings.Contains(te.err.String(), says) || !strings.Contains(te.err.String(), `"nope"`) {
			t.Fatalf("%s: exit %d, out %q, err %q, requests %q", event, code, te.out.String(), te.err.String(), d.paths)
		}
	}
}

func TestHookNamesTheAgentKind(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	d := &hookDaemon{stop: api.StopAnswer{Block: true, Reason: "owe a record"}}
	hookSession(te, d)
	code := -1
	withStdin(t, `{"session_id":"s"}`, func() { code = te.run("hook", "stop", "--agent", "claude") })
	if code != 0 || te.out.String() != `{"decision":"block","reason":"owe a record"}`+"\n" {
		t.Fatalf("exit %d, out %q, err %q", code, te.out.String(), te.err.String())
	}
}

func TestSessionStartRequestIsPinned(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	d := &hookDaemon{}
	hookSession(te, d)
	code := -1
	withStdin(t, `{"session_id":"s","source":"startup","cwd":"/c","transcript_path":"/t/s.jsonl"}`, func() { code = te.run("hook", "session-start") })
	if code != 0 || te.out.Len() != 0 {
		t.Fatalf("exit %d, out %q, err %q", code, te.out.String(), te.err.String())
	}
	want := `{"pane":"tab:leaf","worktree_id":"w1","agent_session":"s","source":"startup","cwd":"/c","home":"/h","agent":"claude","transcript":"/t/s.jsonl"}`
	if len(d.bodies) != 1 || d.paths[0] != api.AgentSessionsStart || d.handles[0] != "term_1" || d.bodies[0] != want {
		t.Fatalf("requests %q %q %q", d.paths, d.handles, d.bodies)
	}
}

func TestPromptRequestIsPinned(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	d := &hookDaemon{}
	hookSession(te, d)
	code := -1
	withStdin(t, `{"session_id":"s","hook_event_name":"UserPromptSubmit","prompt":"and the tests?"}`, func() { code = te.run("hook", "prompt", "--agent", "claude") })
	if code != 0 || te.out.Len() != 0 {
		t.Fatalf("exit %d, out %q, err %q", code, te.out.String(), te.err.String())
	}
	if len(d.bodies) != 1 || d.paths[0] != api.AgentSessionsPrompt || d.handles[0] != "term_1" || d.bodies[0] != `{"prompt":"and the tests?"}` {
		t.Fatalf("requests %q %q %q", d.paths, d.handles, d.bodies)
	}
}
