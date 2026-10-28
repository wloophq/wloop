package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/daemon"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/gitx"
	"github.com/wloophq/wloop/internal/workspace"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

var r12 = github.Ref{Owner: "acme", Repo: "api", Number: 12}

const doneWS = "ws-done"

func basicsJSON(state string, fork bool) string {
	f := "false"
	if fork {
		f = "true"
	}
	return `{"data":{"pr":{"pullRequest":{"number":12,"title":"Done","state":"` + state + `","headRefName":"feat/done","headRefOid":"c12","isCrossRepository":` + f + `,"repository":{"nameWithOwner":"acme/api"}}}}}`
}

func finishedScenario(t *testing.T, state string) (*fake.Manager, *testEnv) {
	t.Helper()
	m := &fake.Manager{
		RepoList: []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}, {ID: "r2", Path: "/src/web", RemoteKey: "github.com/acme/web"}},
		WorkspaceList: []wsdomain.Workspace{
			{ID: "main", RepoID: "r1", Path: "/src/api", Branch: "feat/done", HostID: "local", IsMain: true},
			{ID: "remote", RepoID: "r1", Path: "/box/done", DisplayName: "remote", LinkedPR: 12, HostID: "ssh:box"},
			{ID: doneWS, RepoID: "r1", Path: "/wt/done", DisplayName: "REVIEW feat/done", Branch: "feat/done", HostID: "local", LinkedPR: 12},
		},
	}
	te := newTestEnv(t, m)
	te.gh.basics = basicsJSON(state, false)
	return m, te
}

func removeVia(te *testEnv, id string) error {
	var out strings.Builder
	return envActions{e: te.Env}.Remove(context.Background(), r12.Unit(), daemon.Call{Out: &out, WorkspaceID: id})
}

func forceRemoveVia(te *testEnv, id string) error {
	var out strings.Builder
	return envActions{e: te.Env}.Remove(context.Background(), r12.Unit(), daemon.Call{Out: &out, WorkspaceID: id, Force: true})
}

func wantRefusal(t *testing.T, err error, want string) {
	t.Helper()
	var refused daemon.Refusal
	if !errors.As(err, &refused) || !strings.Contains(refused.Reason, want) {
		t.Fatalf("err = %v, want a refusal with %q", err, want)
	}
}

func TestRemoveDeletesTheLinkedCleanWorkspace(t *testing.T) {
	t.Parallel()
	m, te := finishedScenario(t, "MERGED")
	if err := removeVia(te, doneWS); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(m.Writes, []string{"remove ws-done"}) {
		t.Fatalf("writes %v", m.Writes)
	}
}

func TestRemovingAnOpenPullRequestsWorkspaceOptsOutOfItsFirstRound(t *testing.T) {
	t.Parallel()
	m, te := finishedScenario(t, "OPEN")
	if err := removeVia(te, doneWS); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(m.Writes, []string{"remove ws-done"}) {
		t.Fatalf("writes %v", m.Writes)
	}
	st, err := te.Rounds.Load(context.Background())
	if err != nil || !st.FirstRoundSkipped(r12.String()) {
		t.Fatalf("first round not skipped: %v", err)
	}
	m, te = finishedScenario(t, "OPEN")
	te.git.unpushed["/wt/done"] = 3
	wantRefusal(t, removeVia(te, doneWS), "3 commits not pushed")
	if len(m.Writes) != 0 {
		t.Fatalf("writes %v", m.Writes)
	}
}

func TestRemoveRefusesWhatWouldLoseWork(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		state string
		edit  func(m *fake.Manager, te *testEnv)
		want  string
	}{
		"unpushed": {"MERGED", func(_ *fake.Manager, te *testEnv) { te.git.unpushed["/wt/done"] = 3 }, "3 commits not pushed"},
		"no fetch": {"MERGED", func(_ *fake.Manager, te *testEnv) { te.git.fetchFails["/wt/done"] = true }, "could not fetch"},
		"ignored": {"MERGED", func(_ *fake.Manager, te *testEnv) {
			te.git.ignored["/wt/done"] = []string{".loop/notes.md", "node_modules/x/y.js", "web/node_modules/", "pkg/__init__.pyc", "docs/.DS_Store", ".coverage", "site/_build/", ".docusaurus/", ".env"}
		}, "1 ignored file that may matter (e.g. .env)"},
		"busy":    {"MERGED", func(m *fake.Manager, _ *testEnv) { m.Activity = map[string]wsdomain.Activity{doneWS: {Busy: true}} }, "agent is working"},
		"waiting": {"MERGED", func(m *fake.Manager, _ *testEnv) { m.Activity = map[string]wsdomain.Activity{doneWS: {Waiting: true}} }, "agent is waiting"},
		"user active": {"MERGED", func(m *fake.Manager, _ *testEnv) {
			m.Activity = map[string]wsdomain.Activity{doneWS: {UserActive: true}}
		}, "you have it open"},
	} {
		t.Run(name, func(t *testing.T) {
			m, te := finishedScenario(t, tc.state)
			if tc.edit != nil {
				tc.edit(m, te)
			}
			err := removeVia(te, doneWS)
			wantRefusal(t, err, tc.want)
			var refused daemon.Refusal
			if errors.As(err, &refused); !refused.Forceable {
				t.Fatalf("a check's refusal is not forceable: %+v", refused)
			}
			if slices.ContainsFunc(m.Writes, func(w string) bool { return strings.HasPrefix(w, "remove") }) {
				t.Fatalf("writes %v", m.Writes)
			}
		})
	}
}

func TestRemoveActsOnExactlyTheLinkedWorkspace(t *testing.T) {
	t.Parallel()
	m, te := finishedScenario(t, "MERGED")
	m.WorkspaceList = append(m.WorkspaceList, wsdomain.Workspace{ID: "ws-other", RepoID: "r1", Path: "/wt/other", DisplayName: "feat-done", Branch: "feat/done", HostID: "local", LinkedPR: 12})
	if err := removeVia(te, "ws-other"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(m.Writes, []string{"remove ws-other"}) {
		t.Fatalf("writes %v", m.Writes)
	}
}

func TestRemoveRefusesALinkThatIsGoneOrNotThisPullRequests(t *testing.T) {
	t.Parallel()
	for id, want := range map[string]string{"ws-gone": "is gone", "main": "main checkout", "remote": "not local"} {
		m, te := finishedScenario(t, "MERGED")
		wantRefusal(t, removeVia(te, id), want)
		if len(m.Writes) != 0 {
			t.Fatalf("%s: writes %v", id, m.Writes)
		}
	}
	m, te := finishedScenario(t, "MERGED")
	m.WorkspaceList = append(m.WorkspaceList, wsdomain.Workspace{ID: "ws-web", RepoID: "r2", Path: "/wt/web", DisplayName: "web", HostID: "local"})
	wantRefusal(t, removeVia(te, "ws-web"), "not of acme/api")
}

func TestRemoveWithoutALinkFallsBackCarefully(t *testing.T) {
	t.Parallel()
	unlinked := func(m *fake.Manager) {
		m.WorkspaceList[2].LinkedPR = 0
	}
	m, te := finishedScenario(t, "MERGED")
	unlinked(m)
	if err := removeVia(te, ""); err != nil {
		t.Fatalf("one same-repo workspace on the branch: %v", err)
	}

	m, te = finishedScenario(t, "MERGED")
	unlinked(m)
	te.gh.basics = basicsJSON("MERGED", true)
	wantRefusal(t, removeVia(te, ""), "no local workspace")

	m, te = finishedScenario(t, "MERGED")
	unlinked(m)
	m.WorkspaceList = append(m.WorkspaceList, wsdomain.Workspace{ID: "ws-twin", RepoID: "r1", Path: "/wt/twin", DisplayName: "twin", Branch: "feat/done", HostID: "local"})
	err := removeVia(te, "")
	wantRefusal(t, err, "2 workspaces")
	if !strings.Contains(err.Error(), "/wt/twin") || len(m.Writes) != 0 {
		t.Fatalf("err %v writes %v", err, m.Writes)
	}
}

// orcaFailure is an Orca error with its own message, as the adapter returns.
type orcaFailure struct{ msg string }

func (e orcaFailure) Error() string  { return "orca worktree rm: " + e.msg }
func (e orcaFailure) Detail() string { return e.msg }

type removeFails struct {
	*fake.Manager
	err error
}

func (r removeFails) Remove(context.Context, string, bool) error { return r.err }

const stuckPTY = "runtime_error: Failed to physically stop every PTY for worktree: ws-done \u2014 the terminal sweep failed: connect ENOENT /Users/x/Library/Application Support/orca/daemon/daemon-v37.sock. Retry with force delete (--force) to remove it anyway."

func TestRemoveMapsOrcasFailureToARefusalWithItsMessage(t *testing.T) {
	t.Parallel()
	for msg, want := range map[string]struct {
		reason    string
		forceable bool
	}{stuckPTY: {"its terminals could not be stopped; Orca could not confirm they exited", true}, "runtime_error: worktree contains modified or untracked files": {"Orca refused to remove it", true}} {
		m, te := finishedScenario(t, "MERGED")
		m.ManagerName = "orca"
		te.Env.WS = workspace.NewSet(removeFails{m, orcaFailure{msg}})
		err := removeVia(te, doneWS)
		wantRefusal(t, err, want.reason)
		var refused daemon.Refusal
		errors.As(err, &refused)
		if refused.Detail != msg || refused.Forceable != want.forceable || refused.Source != "orca" {
			t.Fatalf("%+v", refused)
		}
	}
}

func TestUncommittedFilesAreLeftToOrca(t *testing.T) {
	t.Parallel()
	m, te := finishedScenario(t, "MERGED")
	te.git.dirty["/wt/done"] = true
	te.git.untracked["/wt/done"] = 2
	if err := removeVia(te, doneWS); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(m.Writes, []string{"remove ws-done"}) {
		t.Fatalf("writes %v", m.Writes)
	}
}

func TestForceRemovesWhatTheChecksWouldRefuse(t *testing.T) {
	t.Parallel()
	m, te := finishedScenario(t, "MERGED")
	m.Activity = map[string]wsdomain.Activity{doneWS: {Busy: true}}
	te.git.unpushed["/wt/done"] = 3
	te.git.ignored["/wt/done"] = []string{"credential.json"}
	te.git.fetchFails["/wt/done"] = true
	if err := forceRemoveVia(te, doneWS); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(m.Writes, []string{"remove --force ws-done"}) {
		t.Fatalf("writes %v", m.Writes)
	}
	if len(te.git.writes) != 0 {
		t.Fatalf("force ran git: %v", te.git.writes)
	}
	m, te = finishedScenario(t, "OPEN")
	te.git.unpushed["/wt/done"] = 3
	if err := forceRemoveVia(te, doneWS); err != nil || !slices.Equal(m.Writes, []string{"remove --force ws-done"}) {
		t.Fatalf("force on an open pull request: %v %v", err, m.Writes)
	}
}

func TestIgnoredRefusalListsWhatMatters(t *testing.T) {
	t.Parallel()
	_, te := finishedScenario(t, "MERGED")
	for i := range 12 {
		te.git.ignored["/wt/done"] = append(te.git.ignored["/wt/done"], fmt.Sprintf("secret-%02d.json", i))
	}
	err := removeVia(te, doneWS)
	var refused daemon.Refusal
	errors.As(err, &refused)
	lines := strings.Split(refused.Detail, "\n")
	if !strings.Contains(refused.Reason, "12 ignored files") || len(lines) != 11 || lines[0] != "secret-00.json" || lines[10] != "and 2 more" || refused.Source != "" {
		t.Fatalf("%+v", refused)
	}
}

func TestACleanNestedCloneIsLossless(t *testing.T) {
	t.Parallel()
	m, te := finishedScenario(t, "MERGED")
	dir := t.TempDir()
	m.WorkspaceList[2].Path = dir
	os.MkdirAll(filepath.Join(dir, "op-web", ".git"), 0o700)
	te.git.ignored[dir] = []string{"op-web/", "op-web/src/app.js"}
	if err := removeVia(te, doneWS); err != nil {
		t.Fatalf("a clean, pushed nested clone loses nothing: %v", err)
	}
	m, te = finishedScenario(t, "MERGED")
	dir = t.TempDir()
	m.WorkspaceList[2].Path = dir
	os.MkdirAll(filepath.Join(dir, "op-web", ".git"), 0o700)
	te.git.ignored[dir] = []string{"op-web/"}
	te.git.unpushed[filepath.Join(dir, "op-web")] = 1
	wantRefusal(t, removeVia(te, doneWS), "1 ignored file that may matter (e.g. op-web/)")
}

func TestRemoveAcceptsCommitsThePullRequestHolds(t *testing.T) {
	t.Parallel()
	m, te := finishedScenario(t, "MERGED")
	te.git.unpushed["/wt/done"] = 2
	te.git.head["/wt/done"] = "c12"
	if err := removeVia(te, doneWS); err != nil {
		t.Fatalf("the branch was deleted after the merge, but HEAD is the PR head: %v", err)
	}
	m2, te2 := finishedScenario(t, "MERGED")
	te2.git.unpushed["/wt/done"] = 1
	te2.git.head["/wt/done"] = "older"
	te2.git.ancestor["/wt/done"] = true
	if err := removeVia(te2, doneWS); err != nil {
		t.Fatalf("HEAD is behind the PR head: %v", err)
	}
	if !slices.Contains(m.Writes, "remove ws-done") || !slices.Contains(m2.Writes, "remove ws-done") {
		t.Fatalf("writes %v %v", m.Writes, m2.Writes)
	}
	if !slices.ContainsFunc(te2.git.writes, func(w string) bool { return strings.Contains(w, "refs/pull/12/head") }) {
		t.Fatalf("the PR head was not fetched before the ancestry check: %v", te2.git.writes)
	}
}

func TestRemoveRoutesToTheDaemon(t *testing.T) {
	t.Parallel()
	te, calls := daemonForActions(t)
	if code := te.run("remove", "https://github.com/acme/api/pull/12"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if code := te.run("remove", "--force", "https://github.com/acme/api/pull/12"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	got := calls()
	posts := slices.DeleteFunc(slices.Clone(got), func(c string) bool { return !strings.HasPrefix(c, "POST /api/units/github/acme/api#12/remove") })
	if len(posts) != 2 || strings.Contains(posts[0], `"force":true`) || !strings.Contains(posts[1], `"force":true`) {
		t.Fatalf("calls %v", got)
	}
}

func TestRemoveDoesNotCountIgnoredSymlinks(t *testing.T) {
	t.Parallel()
	m, te := finishedScenario(t, "MERGED")
	dir := t.TempDir()
	m.WorkspaceList[2].Path = dir
	os.MkdirAll(filepath.Join(dir, ".claude", "skills"), 0o700)
	if err := os.Symlink(t.TempDir(), filepath.Join(dir, ".claude", "skills", "x")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), "main.env"), filepath.Join(dir, ".env")); err != nil {
		t.Fatal(err)
	}
	te.git.ignored[dir] = []string{".claude/skills/x", ".env"}
	if err := removeVia(te, doneWS); err != nil {
		t.Fatalf("a symlink into the main checkout loses nothing: %v", err)
	}
}

// commitDuringChecks makes a commit appear once the checks have read the
// unpushed count, as an agent committing in that window would.
type commitDuringChecks struct {
	*fakeGit
	reads int
}

func (c *commitDuringChecks) Run(ctx context.Context, dir string, env []string, args ...string) ([]byte, error) {
	if args[0] == "rev-list" {
		c.reads++
		if c.reads > 1 {
			return []byte("c0ffee\n"), nil
		}
	}
	return c.fakeGit.Run(ctx, dir, env, args...)
}

func TestRemoveRefusesWhenSomethingChangedDuringTheChecks(t *testing.T) {
	t.Parallel()
	m, te := finishedScenario(t, "MERGED")
	te.Env.Git = gitx.Git{R: &commitDuringChecks{fakeGit: te.git}}
	wantRefusal(t, removeVia(te, doneWS), "changed while it was checked")
	if slices.Contains(m.Writes, "remove ws-done") {
		t.Fatalf("writes %v", m.Writes)
	}
}

func TestRemoveFetchesWithoutPruning(t *testing.T) {
	t.Parallel()
	_, te := finishedScenario(t, "MERGED")
	te.git.upstream["/wt/done"] = "feat/done"
	te.git.onRemote["feat/done"] = true
	if err := removeVia(te, doneWS); err != nil {
		t.Fatal(err)
	}
	w := strings.Join(te.git.writes, "\n")
	if !strings.Contains(w, "/wt/done: fetch -q origin") || strings.Contains(w, "--prune") {
		t.Fatalf("git writes:\n%s", w)
	}
}

func TestRemoveWithItsBranchGoneTrustsOnlyThePullRequest(t *testing.T) {
	t.Parallel()
	m, te := finishedScenario(t, "CLOSED")
	te.git.upstream["/wt/done"] = "feat/done"
	te.git.head["/wt/done"] = "pushed-after-close"
	wantRefusal(t, removeVia(te, doneWS), "is gone from origin")
	if len(m.Writes) != 0 {
		t.Fatalf("writes %v", m.Writes)
	}
	_, te = finishedScenario(t, "MERGED")
	te.git.upstream["/wt/done"] = "feat/done"
	te.git.head["/wt/done"] = "c12"
	if err := removeVia(te, doneWS); err != nil {
		t.Fatalf("the PR head is HEAD: %v", err)
	}
}

func TestRemovePrintsTheToolsMessageOnASecondLine(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	te.Socket = filepath.Join(shortDir(t), "daemon.sock")
	ln, err := net.Listen("unix", te.Socket)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/remove") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			io.WriteString(w, `{"error":"its terminals could not be stopped","detail":"runtime_error: Failed to physically stop every PTY","source":"orca","forceable":true}`)
			return
		}
		io.WriteString(w, `{"ok":true}`)
	})}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	if code := te.run("remove", "https://github.com/acme/api/pull/12"); code == 0 {
		t.Fatal("a refused remove exited 0")
	}
	lines := strings.Split(strings.TrimSpace(te.err.String()), "\n")
	if len(lines) != 3 || !strings.Contains(lines[0], "its terminals could not be stopped") || lines[1] != "orca: runtime_error: Failed to physically stop every PTY" ||
		lines[2] != "add --force to remove it anyway; that deletes for good what the refusal names" {
		t.Fatalf("stderr:\n%s", te.err)
	}
}

func TestRemoveActsOnTheLinkedWorkspaceAndUnlinksIt(t *testing.T) {
	t.Parallel()
	m, te := finishedScenario(t, "MERGED")
	ctx := context.Background()
	if err := te.Store.UpsertPR(ctx, dinv.PR{Ref: r12, Title: "Done", State: "MERGED", Reasons: []string{"author"}}); err != nil {
		t.Fatal(err)
	}
	if err := te.Store.SetWorkspace(ctx, r12, dinv.Workspace{ID: doneWS, DisplayName: "REVIEW feat/done"}); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := (envActions{e: te.Env, store: te.Store}).Remove(ctx, r12.Unit(), daemon.Call{Out: &out}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(m.Writes, []string{"remove ws-done"}) {
		t.Fatalf("writes %v", m.Writes)
	}
	if p, _, _ := te.Store.PR(ctx, r12); p.Workspace.ID != "" {
		t.Fatalf("still linked to %+v", p.Workspace)
	}
}

func TestAForcedRemoveRefusesTheLinkedMainCheckout(t *testing.T) {
	t.Parallel()
	m, te := finishedScenario(t, "MERGED")
	ctx := context.Background()
	if err := te.Store.UpsertPR(ctx, dinv.PR{Ref: r12, Title: "Done", State: "MERGED", Reasons: []string{"author"}}); err != nil {
		t.Fatal(err)
	}
	if err := te.Store.SetWorkspace(ctx, r12, dinv.Workspace{ID: "main", DisplayName: "api", IsMain: true}); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	err := (envActions{e: te.Env, store: te.Store}).Remove(ctx, r12.Unit(), daemon.Call{Out: &out, Force: true})
	wantRefusal(t, err, "main checkout")
	if len(m.Writes) != 0 {
		t.Fatalf("writes %v", m.Writes)
	}
}

func TestRemovingAnotherWorkspaceKeepsTheLink(t *testing.T) {
	t.Parallel()
	m, te := finishedScenario(t, "MERGED")
	ctx := context.Background()
	if err := te.Store.UpsertPR(ctx, dinv.PR{Ref: r12, Title: "Done", State: "MERGED", Reasons: []string{"author"}}); err != nil {
		t.Fatal(err)
	}
	linked := dinv.Workspace{ID: "ws-elsewhere", DisplayName: "REVIEW feat/elsewhere"}
	if err := te.Store.SetWorkspace(ctx, r12, linked); err != nil {
		t.Fatal(err)
	}
	if err := (envActions{e: te.Env, store: te.Store}).Remove(ctx, r12.Unit(), daemon.Call{Out: io.Discard, WorkspaceID: doneWS}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(m.Writes, []string{"remove ws-done"}) {
		t.Fatalf("writes %v", m.Writes)
	}
	if p, _, _ := te.Store.PR(ctx, r12); p.Workspace.ID != linked.ID {
		t.Fatalf("the link to %s became %+v after removing %s", linked.ID, p.Workspace, doneWS)
	}
}
