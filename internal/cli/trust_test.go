package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/agent/claude"
	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/daemon"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/workspace"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

const untrustedJSON = `{"numStartups":3,"projects":{"/elsewhere":{"hasTrustDialogAccepted":true}}}`

const importsScreen = "Allow external CLAUDE.md file imports?\n> 1. Yes, allow external imports\n  2. No, disable external imports"

const mcpScreen = "  3 new MCP servers found in this project\n  Select any you wish to enable.\n\n  ❯ [✔] playwright"

// untrustedHome is an agent home Claude Code ran in that trusts no folder
// of the tests.
func untrustedHome(t *testing.T) (string, string) {
	t.Helper()
	home := t.TempDir()
	file := filepath.Join(home, ".claude.json")
	writeFile(t, file, untrustedJSON)
	return home, file
}

func unchanged(t *testing.T, file string) {
	t.Helper()
	if raw, _ := os.ReadFile(file); string(raw) != untrustedJSON {
		t.Errorf("%s changed:\n%s", file, raw)
	}
}

func contains(t *testing.T, got string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("lacks %q:\n%s", w, got)
		}
	}
}

func lacks(t *testing.T, got string, unwanted ...string) {
	t.Helper()
	for _, w := range unwanted {
		if strings.Contains(got, w) {
			t.Errorf("has %q:\n%s", w, got)
		}
	}
}

// gitRepo is a new repository with one commit, by its real path.
func gitRepo(t *testing.T) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "repo")
	for _, args := range [][]string{{"init", "-q", repo}, {"-C", repo, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "init"}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	real, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

var trustedBasics = strings.Replace(openBasics, `"headRefOid":"c2",`, `"headRefOid":"c2","authorAssociation":"MEMBER",`, 1)

func newRepo() *fake.Manager {
	return &fake.Manager{RepoList: []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}}}
}

func TestAnUntrustedWorktreeFailsBeforeItsSessionStarts(t *testing.T) {
	m := newRepo()
	te := newTestEnv(t, m)
	home, file := untrustedHome(t)
	te.cfg.AgentHome = home
	te.gh.basics = trustedBasics
	te.git.branch["/worktrees/REVIEW feat/x"] = "feat/x"
	if code := te.run("review", r7.URL()); code != 1 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	contains(t, te.err.String(),
		"untrusted folder blocks the agent: /worktrees/REVIEW feat/x is not trusted in the agent home "+home+"; to trust it, run: "+brand.Name+" trust --home "+home+" '/worktrees/REVIEW feat/x'")
	if got := strings.Join(m.Writes, "\n"); got != "create REVIEW feat/x\nremove --force ws-1" {
		t.Errorf("writes:\n%s", got)
	}
	unchanged(t, file)
}

func TestABlockedStartExplainsWhatTheUserAnswers(t *testing.T) {
	start := func(t *testing.T, m *fake.Manager, basics, home string) *testEnv {
		t.Helper()
		te := newTestEnv(t, m)
		te.cfg.AgentHome = home
		te.gh.basics = basics
		te.git.branch["/worktrees/REVIEW feat/x"] = "feat/x"
		if code := te.run("review", r7.URL()); code != 1 {
			t.Fatalf("exit %d: %s", code, te.err)
		}
		return te
	}
	trustDialog := func() *fake.Manager {
		m := newRepo()
		m.Blocked = workspace.ReasonTrust
		return m
	}
	t.Run("the trust dialog of a trusted pull request names the fix", func(t *testing.T) {
		home := t.TempDir()
		te := start(t, trustDialog(), trustedBasics, home)
		contains(t, te.err.String(),
			"untrusted folder blocks the agent: /worktrees/REVIEW feat/x is not trusted in the agent home "+home+", so its terminal term-ws-1 waits on the trust dialog; to trust it, run: "+brand.Name+" trust --home "+home+" '/worktrees/REVIEW feat/x'; closed its session "+te.sessionOf(t, "fake", "term-ws-1"))
		lacks(t, te.err.String(), workspace.ReasonTrust)
		if entries, _ := os.ReadDir(home); len(entries) != 0 {
			t.Errorf("wrote into the home: %v", entries)
		}
	})
	t.Run("the trust dialog of an untrusted pull request is the user's to answer", func(t *testing.T) {
		m := trustDialog()
		te := start(t, m, openBasics, t.TempDir())
		contains(t, te.err.String(), "so its terminal term-ws-1 waits on the trust dialog; it stays open for you to decide, then run the round again")
		lacks(t, te.err.String(), brand.Name+" trust")
		if got := strings.Join(m.Writes, "\n"); got != "create REVIEW feat/x\nsession ws-1" {
			t.Errorf("writes:\n%s", got)
		}
	})
	t.Run("the trust dialog in the terminal's own home names no home", func(t *testing.T) {
		te := start(t, trustDialog(), trustedBasics, "")
		contains(t, te.err.String(), "is not trusted in the agent home of its terminal (set agent_home to have "+brand.Name+" name it)")
		lacks(t, te.err.String(), brand.Name+" trust", ".claude")
	})
	t.Run("the imports dialog of a round by hand stays open", func(t *testing.T) {
		defer func(d time.Duration) { mcpWait = d }(mcpWait)
		mcpWait = 50 * time.Millisecond
		m := newRepo()
		m.Screen = importsScreen
		te := newTestEnv(t, m)
		te.cfg.AgentHome = t.TempDir()
		te.gh.basics = trustedBasics
		te.git.branch["/worktrees/REVIEW feat/x"] = "feat/x"
		te.reg.withoutMCP("term-ws-1")
		if code := te.run("review", r7.URL()); code != 1 {
			t.Fatalf("exit %d", code)
		}
		contains(t, te.err.String(),
			"the agent waits on Claude Code's question whether to allow the external CLAUDE.md imports of /worktrees/REVIEW feat/x; "+brand.Name+" leaves the answer to you: give it once in a Claude Code session there with the agent home "+te.cfg.AgentHome+"; it stays open for you to decide, then run the round again")
		lacks(t, te.err.String(), "closed its session", claude.ImportsQuestion)
	})
	t.Run("the imports dialog of a session that initializes gets no prompt", func(t *testing.T) {
		m := newRepo()
		m.Screen = importsScreen
		te := start(t, m, trustedBasics, t.TempDir())
		contains(t, te.err.String(), "external CLAUDE.md imports", "it stays open for you to decide")
		if len(m.Prompts) != 0 {
			t.Fatalf("typed into the imports dialog: %+v", m.Prompts)
		}
	})
	t.Run("the imports dialog of an untrusted pull request stays open", func(t *testing.T) {
		m := newRepo()
		m.Screen = importsScreen
		te := start(t, m, openBasics, "")
		contains(t, te.err.String(), "external CLAUDE.md imports", "with the agent home of its terminal", "it stays open for you to decide")
		if got := strings.Join(m.Writes, "\n"); got != "create REVIEW feat/x\nsession ws-1" {
			t.Errorf("writes:\n%s", got)
		}
	})
	t.Run("the MCP dialog of a round by hand stays open", func(t *testing.T) {
		defer func(d time.Duration) { mcpWait = d }(mcpWait)
		mcpWait = 50 * time.Millisecond
		m := newRepo()
		m.Screen = mcpScreen
		te := newTestEnv(t, m)
		te.cfg.AgentHome = t.TempDir()
		te.gh.basics = trustedBasics
		te.git.branch["/worktrees/REVIEW feat/x"] = "feat/x"
		te.reg.withoutMCP("term-ws-1")
		if code := te.run("review", r7.URL()); code != 1 {
			t.Fatalf("exit %d", code)
		}
		contains(t, te.err.String(),
			"the agent waits on Claude Code's question which MCP servers of the .mcp.json of /worktrees/REVIEW feat/x to enable; "+brand.Name+" never answers it: answer it in a Claude Code session there; it stays open for you to decide, then run the round again")
		lacks(t, te.err.String(), "has no "+brand.Name+" MCP tool", claude.MCPQuestion)
		if got := strings.Join(m.Writes, "\n"); got != "create REVIEW feat/x\nsession ws-1" || len(m.Prompts) != 0 {
			t.Errorf("writes:\n%s\nprompts %+v", got, m.Prompts)
		}
	})
	t.Run("a session that never initializes on another screen is only abandoned", func(t *testing.T) {
		defer func(d time.Duration) { mcpWait = d }(mcpWait)
		mcpWait = 50 * time.Millisecond
		m := newRepo()
		m.Screen = "> "
		te := newTestEnv(t, m)
		te.gh.basics = trustedBasics
		te.git.branch["/worktrees/REVIEW feat/x"] = "feat/x"
		te.reg.withoutMCP("term-ws-1")
		if code := te.run("review", r7.URL()); code != 1 {
			t.Fatalf("exit %d", code)
		}
		contains(t, te.err.String(), "has no "+brand.Name+" MCP tool")
		lacks(t, te.err.String(), "imports")
	})
}

func TestTrustCommandTrustsTheRepositoryOnlyWhenTheUserRunsIt(t *testing.T) {
	root := gitRepo(t)
	wt := filepath.Join(t.TempDir(), "wt")
	if out, err := exec.Command("git", "-C", root, "worktree", "add", "-q", "-b", "feature", wt).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	m := &fake.Manager{WorkspaceList: []wsdomain.Workspace{{ID: "ws-9", Path: wt, DisplayName: "REVIEW feature"}}}
	te := newTestEnv(t, m)
	te.Agents["claude"] = claude.Agent{UserHome: te.Home}
	home, file := untrustedHome(t)

	if code := te.run("trust", "--home", home, "REVIEW feature"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	raw, _ := os.ReadFile(file)
	if !strings.Contains(te.out.String(), "trusted "+root+" in "+file) || !strings.Contains(string(raw), `"`+root+`": {`) || !strings.Contains(string(raw), `"numStartups": 3`) {
		t.Fatalf("stdout:\n%s\nfile:\n%s", te.out, raw)
	}
	if strings.Contains(string(raw), `"`+wt+`"`) {
		t.Errorf("trusted the worktree key too:\n%s", raw)
	}

	te.out.Reset()
	if code := te.run("trust", wt, "--exact", "--home", home); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if raw, _ := os.ReadFile(file); !strings.Contains(string(raw), `"`+wt+`": {`) {
		t.Fatalf("--exact did not trust %s:\n%s", wt, raw)
	}
	if code := te.run("trust", "--home", home); code != 1 || !strings.Contains(te.err.String(), "usage: trust") {
		t.Fatalf("exit %d: %s", code, te.err)
	}
}

func blocksOf(rec *memRecorder, ref github.Ref) []dinv.Event {
	var out []dinv.Event
	for _, e := range rec.events {
		if e.PR == ref && (e.Kind == dinv.KindRoundBlocked || e.Kind == dinv.KindRoundUnblocked || e.Kind == dinv.KindRoundSkipped) {
			out = append(out, e)
		}
	}
	return out
}

// blindHomes makes the trust command's default home and an Orca-like
// profile home exist, plus a project's agent home, and returns them.
func blindHomes(t *testing.T, te *testEnv) (def, profile, project string) {
	t.Helper()
	te.Agents["claude"] = claude.Agent{UserHome: te.Home}
	def, profile, project = filepath.Join(te.Home, ".claude"), filepath.Join(te.Home, "profiles", "p1", "home"), filepath.Join(te.Home, ".claude-work")
	for _, d := range []string{def, profile, project} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	all := map[string]ManagerEntry{}
	for name, entry := range te.managers() {
		entry.Homes = func(string, string) []string { return []string{profile} }
		all[name] = entry
	}
	te.Managers = all
	te.cfg.Projects = append(te.cfg.Projects, config.Project{Match: "other/*", AgentHome: project})
	return def, profile, project
}

var pr9 = github.Ref{Owner: "acme", Repo: "api", Number: 9}

const pr9Basics = `{"data":{"pr":{"pullRequest":{"number":9,"title":"Add z","state":"OPEN","headRefName":"feat/z","baseRefName":"main","headRefOid":"hfeat/z","repository":{"nameWithOwner":"acme/api"},"author":{"__typename":"User","login":"me"}}}}}`

// blockedTriage is the triage round of acme/api#9 blocked on /wt/auth,
// untrusted in the agent home the config names, as a sweep records it.
func blockedTriage(t *testing.T) (*fake.Manager, *testEnv, *memRecorder, string) {
	t.Helper()
	m, te := reviewedAuthorScenario(t)
	m.Sessions = nil
	home, file := untrustedHome(t)
	te.cfg.AgentHome = home
	te.gh.basics = pr9Basics
	b := inventory.StartBlock{Question: turns.QuestionTrust, Root: "/wt/auth", Folder: "/wt/auth", Home: home, Homes: []string{home}, Round: "triage", Fingerprint: "2026-10-08T10:00:00Z"}
	rec := &memRecorder{events: []dinv.Event{{PR: pr9, Kind: dinv.KindRoundBlocked, Key: b.Key(testNow), At: testNow, Data: "could not start: /wt/auth is not trusted in " + home}}}
	te.Rec = rec
	return m, te, rec, file
}

func TestTrustAndRunTrustsTheBlockedFolderAndStartsTheRound(t *testing.T) {
	m, te, rec, file := blockedTriage(t)
	var out strings.Builder
	if err := (envActions{e: te.Env}).TrustAndRun(context.Background(), pr9.Unit(), daemon.Call{Out: &out}); err != nil {
		t.Fatalf("%v\nout:\n%s\nstderr:\n%s", err, &out, te.err)
	}
	if raw, _ := os.ReadFile(file); !strings.Contains(string(raw), `"/wt/auth": {`) {
		t.Fatalf("not trusted:\n%s", raw)
	}
	contains(t, out.String(), "trusted /wt/auth in "+file)
	evs := blocksOf(rec, pr9)
	if len(evs) != 2 || evs[1].Kind != dinv.KindRoundUnblocked || evs[1].Data != "you trusted its folder" {
		t.Fatalf("events %+v", evs)
	}
	if len(m.Prompts) != 1 || !strings.Contains(m.Prompts[0].Text, "/pull/9") {
		t.Fatalf("prompts %+v\nout:\n%s\nstderr:\n%s", m.Prompts, &out, te.err)
	}
}

func TestTrustBlockedExactTrustsTheWorktreeAlone(t *testing.T) {
	_, te, _, file := blockedTriage(t)
	home := te.cfg.AgentHome
	b := inventory.StartBlock{Question: turns.QuestionTrust, Root: "/repo/api", Folder: "/wt/review-api-9", Home: home, Homes: []string{home}, Round: "first round"}
	if blockedIntent(b) != dintent.Review || blockedIntent(inventory.StartBlock{Round: "triage"}) != dintent.Address {
		t.Fatal("blockedIntent")
	}
	if err := te.trustBlocked(context.Background(), te.cfg, b, true); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(file)
	if !strings.Contains(string(raw), `"/wt/review-api-9": {`) || strings.Contains(string(raw), `"/repo/api"`) {
		t.Fatalf("exact trust:\n%s", raw)
	}
	if err := te.trustBlocked(context.Background(), te.cfg, b, false); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(file); !strings.Contains(string(raw), `"/repo/api": {`) {
		t.Fatalf("repository trust:\n%s", raw)
	}
}

func TestTrustAndRunInTheTerminalsOwnHomeTrustsTheHomesTheBlockNames(t *testing.T) {
	m, te := reviewedAuthorScenario(t)
	def, profile, project := blindHomes(t, te)
	te.gh.basics = pr9Basics
	b := inventory.StartBlock{Question: turns.QuestionTrust, Root: "/wt/auth", Folder: "/wt/auth", Homes: []string{def, profile}, Round: "triage"}
	rec := &memRecorder{events: []dinv.Event{{PR: pr9, Kind: dinv.KindRoundBlocked, Key: b.Key(testNow), At: testNow, Data: "could not start"}}}
	te.Rec = rec
	var out strings.Builder
	if err := (envActions{e: te.Env}).TrustAndRun(context.Background(), pr9.Unit(), daemon.Call{Out: &out}); err != nil {
		t.Fatalf("%v\nout:\n%s", err, &out)
	}
	for _, f := range []string{filepath.Join(te.Home, ".claude.json"), filepath.Join(profile, ".claude.json")} {
		if raw, _ := os.ReadFile(f); !strings.Contains(string(raw), `"/wt/auth"`) {
			t.Errorf("%s does not trust /wt/auth:\n%s", f, raw)
		}
		contains(t, out.String(), "trusted /wt/auth in "+f)
	}
	if _, err := os.Stat(filepath.Join(project, ".claude.json")); !os.IsNotExist(err) {
		t.Errorf("trusted in a project's agent home: %v", err)
	}
	if len(m.Prompts) != 1 {
		t.Fatalf("prompts %+v", m.Prompts)
	}
}

func TestARoundByHandPastTheBlockedQuestionLiftsTheBlock(t *testing.T) {
	m, te, rec, file := blockedTriage(t)
	writeFile(t, file, `{"projects":{"/wt/auth":{"hasTrustDialogAccepted":true}}}`)
	m.Screen = mcpScreen
	defer func(d time.Duration) { mcpWait = d }(mcpWait)
	mcpWait = 50 * time.Millisecond
	te.reg.withoutMCP("term-ws-auth")
	err := (envActions{e: te.Env}).Run(context.Background(), pr9.Unit(), daemon.Call{Out: &strings.Builder{}})
	if err == nil || !strings.Contains(err.Error(), "which MCP servers") {
		t.Fatalf("Run = %v\nstderr:\n%s", err, te.err)
	}
	evs := blocksOf(rec, pr9)
	if len(evs) != 2 || evs[1].Kind != dinv.KindRoundUnblocked || evs[1].Data != "a later session got past its question" {
		t.Fatalf("events %+v", evs)
	}
}

func TestARoundByHandStoppedOnTheBlockedQuestionKeepsTheBlock(t *testing.T) {
	m, te, rec, _ := blockedTriage(t)
	m.Blocked = workspace.ReasonTrust
	if err := (envActions{e: te.Env}).Run(context.Background(), pr9.Unit(), daemon.Call{Out: &strings.Builder{}}); err == nil {
		t.Fatal("Run on an untrusted folder succeeded")
	}
	if evs := blocksOf(rec, pr9); len(evs) != 1 {
		t.Fatalf("events %+v", evs)
	}
}
