package cli

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/adapter/agent/claude"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/api/client"
	"github.com/wloophq/wloop/internal/config"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/daemon"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/shell"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/gitx"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

// cloneAs has gh repo clone create a local repository whose origin is
// github.com/<origin>, recording each clone in clones.
func cloneAs(t *testing.T, origin string, clones *[]string) func(repo, path string) error {
	return func(repo, path string) error {
		*clones = append(*clones, repo+" "+path)
		for _, args := range [][]string{{"init", "-q", path}, {"-C", path, "remote", "add", "origin", "git@github.com:" + origin + ".git"},
			{"-C", path, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "init"}} {
			if out, err := exec.Command(gitx.Binary(), args...).CombinedOutput(); err != nil {
				t.Errorf("git %v: %v\n%s", args, err, out)
			}
		}
		return nil
	}
}

var capturedEntry = regexp.MustCompile(`captured (entry:e-[0-9a-f]+)`)

// startEnv serves a daemon whose manager knows no repository, cloning into
// a projects_dir whose acme project trusts its clones in an agent home.
func startEnv(t *testing.T) (*testEnv, *fake.Manager, string, string) {
	t.Helper()
	m := &fake.Manager{WorkspaceList: []wsdomain.Workspace{{ID: "ws-caller", Path: "/worktrees/caller", DisplayName: "caller", HostID: wsdomain.LocalHost}}}
	te := newTestEnv(t, m)
	te.Agents["claude"] = claude.Agent{UserHome: te.Home}
	home, file := untrustedHome(t)
	projects, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	te.cfg.ProjectsDir = projects
	te.cfg.AgentHome = home
	te.cfg.Projects = []config.Project{{Match: "acme/*", TrustClones: true}}
	te.git.toplevel = "/worktrees/caller"
	te.serveEntries(t)
	return te, m, projects, file
}

func TestStartCapturesClonesTrustsAndCreatesFromTheDefaultBase(t *testing.T) {
	te, m, projects, file := startEnv(t)
	var clones []string
	te.gh.clone = cloneAs(t, "acme/api", &clones)
	if code := te.run("start", "--repo", "acme/api", "Add retries to the webhook sender", "done when a failed delivery is retried"); code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, te.err, te.out)
	}
	entry := capturedEntry.FindStringSubmatch(te.out.String())
	if entry == nil {
		t.Fatalf("no entry captured:\n%s", te.out)
	}
	path := filepath.Join(projects, "api")
	if len(clones) != 1 || clones[0] != "acme/api "+path {
		t.Fatalf("clones %q", clones)
	}
	for _, c := range te.gh.underLock {
		if strings.HasPrefix(c, "repo clone") {
			t.Errorf("cloned under the loop mutex: %s", c)
		}
	}
	if raw, _ := os.ReadFile(file); !strings.Contains(string(raw), `"`+path+`": {`) {
		t.Fatalf("the clone is not trusted:\n%s", raw)
	}
	if len(m.Creates) != 1 {
		t.Fatalf("creates %+v", m.Creates)
	}
	short := strings.TrimPrefix(entry[1], "entry:e-")[:6]
	c := m.Creates[0]
	if c.Name != "add-retries-to-the-webhook-sender-done-when-a-"+short || !c.DefaultBase || c.Parent != "/worktrees/caller" {
		t.Fatalf("create %+v", c)
	}
	if !strings.HasSuffix(c.Name, "-"+short) {
		t.Fatalf("worktree %q, want it to end in the entry's short id %s", c.Name, short)
	}
	if len(m.Commands) != 1 || !strings.Contains(m.Commands[0], " --name '"+c.Name+"'") {
		t.Fatalf("commands %q", m.Commands)
	}
	if len(m.Prompts) != 1 {
		t.Fatalf("prompts %+v", m.Prompts)
	}
	round := regexp.MustCompile(`--turn ([0-9a-f]{12})`).FindStringSubmatch(m.Prompts[0].Text)
	if round == nil {
		t.Fatalf("prompt %q", m.Prompts[0].Text)
	}
	text := storedText(t, te, round[1])
	for _, want := range []string{"Add retries to the webhook sender done when a failed delivery is retried", "fresh worktree of `acme/api`", "turn end " + entry[1]} {
		if !strings.Contains(text, want) {
			t.Errorf("the instructions lack %q:\n%s", want, text)
		}
	}

	id := task.ID(strings.TrimPrefix(entry[1], "entry:"))
	books, err := te.Turns.Store.Books(context.Background(), task.UnitOf(id))
	if err != nil || len(books[task.UnitOf(id)].Turns) != 1 {
		t.Fatalf("books %+v, %v", books, err)
	}
	sent := books[task.UnitOf(id)].Turns[0]
	rec := record.Record{State: record.Done, Summary: "opened the pull request", Outcome: &record.Outcome{URL: "https://github.com/acme/api/pull/9"}}
	if _, err := te.Turns.RecordTurnEnd(context.Background(), turns.RecordRequest{Session: sent.SentTo, Turn: sent.ID, Unit: task.UnitOf(id), Record: rec}); err != nil {
		t.Fatal(err)
	}
	got, err := te.Env.gtd.Entry(context.Background(), id)
	if err != nil || !slices.Contains(got.Units, unit.Unit{Provider: unit.GitHub, Key: "acme/api#9"}) {
		t.Fatalf("the entry's units %v, %v", got.Units, err)
	}
}

func TestStartRefusesWhatItCannotWorkIn(t *testing.T) {
	t.Run("a repository that leaves its folder", func(t *testing.T) {
		te, m, _, _ := startEnv(t)
		var clones []string
		te.gh.clone = cloneAs(t, "acme/api", &clones)
		if code := te.run("start", "--repo", "acme/..", "fix it"); code == 0 || !strings.Contains(te.err.String(), `"acme/.." is not a repository`) {
			t.Fatalf("exit %d: %s", code, te.err)
		}
		if len(clones) != 0 || len(m.Creates) != 0 {
			t.Fatalf("clones %q, creates %+v", clones, m.Creates)
		}
	})
	t.Run("a folder that holds another repository", func(t *testing.T) {
		te, m, projects, file := startEnv(t)
		var clones []string
		cloneAs(t, "globex/api", &clones)("globex/api", filepath.Join(projects, "api"))
		if code := te.run("start", "--repo", "acme/api", "fix it"); code == 0 || !strings.Contains(te.err.String(), "is not a checkout of acme/api") {
			t.Fatalf("exit %d: %s", code, te.err)
		}
		if len(m.Creates) != 0 {
			t.Fatalf("creates %+v", m.Creates)
		}
		unchanged(t, file)
	})
	t.Run("a manager that cannot host", func(t *testing.T) {
		te, m, _, file := startEnv(t)
		var clones []string
		te.gh.clone = cloneAs(t, "acme/api", &clones)
		if code := te.run("start", "--repo", "acme/api", "--manager", "nowhere", "fix it"); code == 0 || !strings.Contains(te.err.String(), "nowhere") {
			t.Fatalf("exit %d: %s", code, te.err)
		}
		if len(clones) != 0 || len(m.Creates) != 0 || len(m.Prompts) != 0 {
			t.Fatalf("clones %q, creates %+v, prompts %+v", clones, m.Creates, m.Prompts)
		}
		unchanged(t, file)
		if !strings.Contains(te.err.String(), "start entry:e-") || !strings.Contains(te.err.String(), "--repo acme/api --manager nowhere") {
			t.Fatalf("the retry lacks the flags: %s", te.err)
		}
	})
	t.Run("an existing clone is not trusted", func(t *testing.T) {
		te, m, projects, file := startEnv(t)
		var clones []string
		cloneAs(t, "acme/api", &clones)("acme/api", filepath.Join(projects, "api"))
		te.run("start", "--repo", "acme/api", "fix it")
		unchanged(t, file)
		if len(m.Creates) != 1 {
			t.Fatalf("creates %+v: %s", m.Creates, te.err)
		}
	})
}

func TestStartWithoutARepositoryRunsInTheNeutralRepository(t *testing.T) {
	te, m, _, _ := startEnv(t)
	if code := te.run("start", "tell the team the export ships Friday"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	writes := strings.Join(m.Writes, "\n")
	if !strings.Contains(writes, "repo add "+te.Env.neutralDir()) || len(m.Prompts) != 1 {
		t.Fatalf("writes:\n%s\nprompts %+v", writes, m.Prompts)
	}
	if len(m.Commands) != 1 || !strings.Contains(m.Commands[0], " --name 'tell the team the export ships Friday'") {
		t.Fatalf("commands %q", m.Commands)
	}
}

func TestStartRunsOnlyOnAnEntryTheUserCaptured(t *testing.T) {
	te, m, _, _ := startEnv(t)
	ctx := context.Background()
	derived, err := te.Env.gtd.Open(ctx, coretasks.OpenRequest{Kind: task.KindMessage, Title: "ship the export", Origin: task.OriginMessage})
	if err != nil {
		t.Fatal(err)
	}
	if code := te.run("start", "entry:"+string(derived.ID)); code == 0 || !strings.Contains(te.err.String(), "Start runs only on an entry you captured") {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if len(m.Prompts) != 0 {
		t.Fatalf("prompts %+v", m.Prompts)
	}
	captured, err := te.Env.gtd.Capture(ctx, "ship the export", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	v, err := client.New(te.Socket).Entry(ctx, string(captured.ID))
	if err != nil {
		t.Fatal(err)
	}
	if i := slices.IndexFunc(v.Actions, func(a api.ActionState) bool { return a.ID == "start" }); i < 0 || !v.Actions[i].Enabled || v.Actions[i].Label != "Start" {
		t.Fatalf("the captured entry's actions %+v", v.Actions)
	}
	v, err = client.New(te.Socket).Entry(ctx, string(derived.ID))
	if err != nil {
		t.Fatal(err)
	}
	if i := slices.IndexFunc(v.Actions, func(a api.ActionState) bool { return a.ID == "start" }); i < 0 || v.Actions[i].Enabled {
		t.Fatalf("the derived entry's actions %+v", v.Actions)
	}
}

func TestAStartAgainOnTheEntryReusesItsRepositoryAndWorktree(t *testing.T) {
	te, m, _, _ := startEnv(t)
	var clones []string
	te.gh.clone = cloneAs(t, "acme/api", &clones)
	if code := te.run("start", "--repo", "acme/api", "add retries"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	entry := capturedEntry.FindStringSubmatch(te.out.String())
	if entry == nil {
		t.Fatalf("no entry captured:\n%s", te.out)
	}
	if code := te.run("start", entry[1]); code != 0 {
		t.Fatalf("again: exit %d: %s", code, te.err)
	}
	if len(clones) != 1 || len(m.Creates) != 1 || len(m.Prompts) != 2 {
		t.Fatalf("clones %q, creates %+v, prompts %d", clones, m.Creates, len(m.Prompts))
	}
	if m.Prompts[1].Handle != m.Prompts[0].Handle {
		t.Fatalf("the second start ran in %s, the first in %s", m.Prompts[1].Handle, m.Prompts[0].Handle)
	}
}

func TestStartRunsInTheProjectsAgentHome(t *testing.T) {
	te, m, _, _ := startEnv(t)
	home, file := untrustedHome(t)
	te.cfg.Projects = []config.Project{{Match: "acme/*", AgentHome: home, TrustClones: true}}
	te.serveEntries(t)
	var clones []string
	te.gh.clone = cloneAs(t, "acme/api", &clones)
	if code := te.run("start", "--repo", "acme/api", "fix it"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if raw, _ := os.ReadFile(file); !strings.Contains(string(raw), "/api\": {") {
		t.Fatalf("the project's home does not trust the clone:\n%s", raw)
	}
	if len(m.Commands) != 1 || !strings.HasPrefix(m.Commands[0], "CLAUDE_CONFIG_DIR="+shell.Quote(home)+" ") {
		t.Fatalf("commands %q, want them in %s", m.Commands, home)
	}
}

func TestStartTakesACheckoutWhoseOriginIsAnSSHAlias(t *testing.T) {
	te, m, projects, _ := startEnv(t)
	var clones []string
	path := filepath.Join(projects, "api")
	cloneAs(t, "acme/api", &clones)("acme/api", path)
	if out, err := exec.Command(gitx.Binary(), "-C", path, "remote", "set-url", "origin", "git@github-work:acme/api.git").CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if code := te.run("start", "--repo", "acme/api", "fix it"); code != 0 || len(m.Creates) != 1 {
		t.Fatalf("exit %d, creates %+v: %s", code, m.Creates, te.err)
	}
}

func TestAHiddenStartActionStillRefusesADerivedEntry(t *testing.T) {
	te, m, _, _ := startEnv(t)
	raw, err := os.ReadFile(filepath.Join("..", "..", "profiles", "default", "intents", "start", "intent.toml"))
	if err != nil {
		t.Fatal(err)
	}
	manifest := strings.Replace(string(raw), "[action]\n", "[action]\nwhen = 'entry.captured_by_user'\n", 1)
	manifest = regexp.MustCompile(`(?s)\[\[action.refuse\]\].*?why  = "[^"]*"\n`).ReplaceAllString(manifest, "")
	body, err := os.ReadFile(filepath.Join("..", "..", "profiles", "default", "intents", "start", "instructions.md.tmpl"))
	if err != nil {
		t.Fatal(err)
	}
	userProfile(t, te, map[string]string{"intents/start/intent.toml": manifest, "intents/start/instructions.md.tmpl": string(body)})
	te.serveEntries(t)
	derived, err := te.Env.gtd.Open(context.Background(), coretasks.OpenRequest{Kind: task.KindMessage, Title: "ship the export", Origin: task.OriginMessage})
	if err != nil {
		t.Fatal(err)
	}
	if code := te.run("start", "entry:"+string(derived.ID)); code == 0 || len(m.Prompts) != 0 {
		t.Fatalf("exit %d, prompts %+v: %s", code, m.Prompts, te.err)
	}
}

func TestAFailedTrustLeavesNoCloneAndTheRetryTrustsANewOne(t *testing.T) {
	te, _, projects, file := startEnv(t)
	var clones []string
	te.gh.clone = cloneAs(t, "acme/api", &clones)
	writeFile(t, file, "{not json")
	if code := te.run("start", "--repo", "acme/api", "fix it"); code == 0 {
		t.Fatalf("exit 0 with an unreadable agent home: %s", te.out)
	}
	entry := capturedEntry.FindStringSubmatch(te.out.String())
	if entry == nil {
		t.Fatalf("no entry captured:\n%s", te.out)
	}
	path := filepath.Join(projects, "api")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the clone whose trust failed is still at %s: %v", path, err)
	}
	writeFile(t, file, untrustedJSON)
	if code := te.run("start", entry[1], "--repo", "acme/api"); code != 0 {
		t.Fatalf("retry: exit %d: %s", code, te.err)
	}
	if len(clones) != 2 {
		t.Fatalf("clones %q, want the retry to clone again", clones)
	}
	if raw, _ := os.ReadFile(file); !strings.Contains(string(raw), `"`+path+`": {`) {
		t.Fatalf("the retry's clone is not trusted:\n%s", raw)
	}
}

func TestARoundTheDaemonDidNotPrepareClonesAndTrusts(t *testing.T) {
	te, m, projects, file := startEnv(t)
	var clones []string
	te.gh.clone = cloneAs(t, "acme/api", &clones)
	ctx := context.Background()
	e, err := te.Env.gtd.Capture(ctx, "add retries", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	c := daemon.Call{Inputs: map[string]any{"repo": "acme/api"}, Out: io.Discard}
	if err := (envActions{te.Env, nil}).Intent(ctx, "start", task.UnitOf(e.ID), c); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(projects, "api")
	if len(clones) != 1 || len(m.Prompts) != 1 {
		t.Fatalf("clones %q, prompts %d", clones, len(m.Prompts))
	}
	if raw, _ := os.ReadFile(file); !strings.Contains(string(raw), `"`+path+`": {`) {
		t.Fatalf("the round's clone is not trusted:\n%s", raw)
	}
}
