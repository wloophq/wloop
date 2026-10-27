package cli

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/config"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

func listDir(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names, err
}

const openBasics = `{"data":{"pr":{"pullRequest":{"number":7,"title":"Add x","state":"OPEN","headRefName":"feat/x","headRefOid":"c2","repository":{"nameWithOwner":"acme/api"}}}}}`

func TestReviewCreatesTheWorkspace(t *testing.T) {
	t.Parallel()
	m := &fake.Manager{RepoList: []wsdomain.Repo{
		{ID: "dup", Path: "/elsewhere/legacy", RemoteKey: "github.com/acme/api"},
		{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"},
	}}
	te := newTestEnv(t, m)
	te.gh.basics = openBasics
	te.git.branch["/worktrees/REVIEW feat/x"] = "me/REVIEW-feat-x"
	if code := te.run("review", "https://github.com/acme/api/pull/7", "focus on", " the\nmigration"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if got := strings.Join(m.Writes, "\n"); got != "create REVIEW feat/x\nsession ws-1\nsend term-ws-1\nlink ws-1 7" {
		t.Fatalf("writes:\n%s", got)
	}
	if got := m.WorkspaceList[0].RepoID; got != "r1" {
		t.Fatalf("created under repo %s, want the checkout named after the repository", got)
	}
	if h, intent, url, _ := promptOf(t, m.Prompts[0].Text); h != "First review of acme/api#7, asked for by you." || intent != "review" || url != "https://github.com/acme/api/pull/7" {
		t.Fatalf("prompt = %q", m.Prompts[0].Text)
	}
	if text := turnText(t, te, m.Prompts[0].Text); !strings.Contains(text, "## Guidance") || !strings.Contains(text, "the\nmigration") {
		t.Fatalf("turn:\n%s", text)
	}
	wantGit := strings.Join([]string{
		"/src/api: fetch -q origin +refs/pull/7/head:refs/remotes/origin/pr/7",
		"/worktrees/REVIEW feat/x: fetch -q origin +refs/heads/feat/x:refs/remotes/origin/feat/x",
		"/worktrees/REVIEW feat/x: branch -m me/REVIEW-feat-x feat/x",
		"/worktrees/REVIEW feat/x: branch --set-upstream-to=origin/feat/x",
	}, "\n")
	if got := strings.Join(te.git.writes, "\n"); got != wantGit {
		t.Fatalf("git writes:\n%s", got)
	}
}

func TestReviewContinuesAnExistingWorkspace(t *testing.T) {
	t.Parallel()
	m := &fake.Manager{
		RepoList: []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}},
		WorkspaceList: []wsdomain.Workspace{
			{ID: "other", RepoID: "r9", Path: "/wt/other", DisplayName: "REVIEW feat/x", HostID: "local"},
			{ID: "ws-rev", RepoID: "r1", Path: "/wt/rev", DisplayName: "REVIEW renamed", HostID: "local", LinkedPR: 7},
		},
		Activity: map[string]wsdomain.Activity{"ws-rev": {UserActive: true}},
	}
	te := newTestEnv(t, m)
	te.gh.basics = openBasics
	if code := te.run("review", "https://github.com/acme/api/pull/7"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if got := strings.Join(m.Writes, "\n"); got != "session ws-rev\nsend term-ws-rev\nlink ws-rev 7" {
		t.Fatalf("writes:\n%s", got)
	}
	if !strings.Contains(strings.Join(te.git.writes, "\n"), "/wt/rev: reset --hard -q refs/remotes/origin/pr/7") {
		t.Fatalf("git writes: %v", te.git.writes)
	}
}

func TestReviewRefusesBusyOrDirtyWorkspaces(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name  string
		busy  bool
		dirty bool
		want  string
	}{
		{"busy", true, false, "an agent is working"},
		{"dirty", false, true, "uncommitted changes"},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := &fake.Manager{
				RepoList:      []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}},
				WorkspaceList: []wsdomain.Workspace{{ID: "ws-rev", RepoID: "r1", Path: "/wt/rev", DisplayName: "REVIEW feat/x", HostID: "local"}},
				Activity:      map[string]wsdomain.Activity{"ws-rev": {Busy: c.busy}},
			}
			te := newTestEnv(t, m)
			te.gh.basics = openBasics
			te.git.dirty["/wt/rev"] = c.dirty
			if code := te.run("review", "https://github.com/acme/api/pull/7"); code == 0 {
				t.Fatal("expected failure")
			}
			if !strings.Contains(te.err.String(), c.want) {
				t.Fatalf("stderr = %s", te.err)
			}
			for _, w := range te.git.writes {
				if strings.Contains(w, "reset") {
					t.Fatalf("reset ran: %v", te.git.writes)
				}
			}
		})
	}
}

func TestReviewNeedsProjectsDirToClone(t *testing.T) {
	t.Parallel()
	m := &fake.Manager{}
	te := newTestEnv(t, m)
	te.gh.basics = openBasics
	if code := te.run("review", "https://github.com/acme/api/pull/7"); code == 0 {
		t.Fatal("expected failure")
	}
	if !strings.Contains(te.err.String(), "projects_dir is not set") {
		t.Fatalf("stderr = %s", te.err)
	}
	if len(m.Writes) != 0 {
		t.Fatalf("writes: %v", m.Writes)
	}
}

func TestReviewRejectsClosedPRs(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	te.gh.basics = strings.Replace(openBasics, `"OPEN"`, `"MERGED"`, 1)
	if code := te.run("review", "https://github.com/acme/api/pull/7"); code == 0 || !strings.Contains(te.err.String(), "no review on acme/api#7") {
		t.Fatalf("exit %d, stderr %s", code, te.err)
	}
}

func TestReviewNamesTheSessionFromTheProfile(t *testing.T) {
	t.Parallel()
	m := &fake.Manager{RepoList: []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}}}
	te := newTestEnv(t, m)
	te.gh.basics = openBasics
	te.git.branch["/worktrees/REVIEW feat/x"] = "me/REVIEW-feat-x"
	if code := te.run("review", "https://github.com/acme/api/pull/7"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if len(m.Commands) != 1 || !strings.Contains(m.Commands[0], " --name 'review acme/api#7 09/10 15:00' ") {
		t.Fatalf("commands = %q", m.Commands)
	}
}

func TestReviewStartsTheAgentInTheProjectProfile(t *testing.T) {
	t.Parallel()
	m := &fake.Manager{RepoList: []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}}}
	te := newTestEnv(t, m)
	te.cfg.Projects = []config.Project{
		{Match: "other/*", AgentHome: "/wrong"},
		{Match: "acme/*", AgentHome: "/home/me/.claude-personal"},
	}
	te.gh.basics = openBasics
	te.git.branch["/worktrees/REVIEW feat/x"] = "me/REVIEW-feat-x"
	if code := te.run("review", "https://github.com/acme/api/pull/7"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if got := strings.Join(launches(m.Commands), "|"); got != "CLAUDE_CONFIG_DIR='/home/me/.claude-personal' claude" {
		t.Fatalf("agent command = %q", got)
	}
	if len(m.Prompts) != 1 {
		t.Fatalf("prompts = %+v", m.Prompts)
	}
	if h, _, _, _ := promptOf(t, m.Prompts[0].Text); h != "First review of acme/api#7, asked for by you." || strings.Contains(turnText(t, te, m.Prompts[0].Text), "## Guidance") {
		t.Fatalf("prompts = %+v", m.Prompts)
	}
}

func TestReviewStartsTheProjectsAgent(t *testing.T) {
	t.Parallel()
	m := &fake.Manager{RepoList: []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}}}
	te := newTestEnv(t, m)
	te.withEcho()
	te.cfg.Projects = []config.Project{{Match: "acme/*", Agent: "echo"}}
	te.gh.basics = openBasics
	te.git.branch["/worktrees/REVIEW feat/x"] = "me/REVIEW-feat-x"
	if code := te.run("review", "https://github.com/acme/api/pull/7"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if got := strings.Join(launches(m.Commands), "|"); got != "cat" {
		t.Fatalf("agent command = %q", got)
	}
}

func TestReviewRemembersTheCallerProfile(t *testing.T) {
	t.Parallel()
	m := &fake.Manager{RepoList: []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}}}
	te := newTestEnv(t, m)
	te.withCallerHome("/home/me/.claude-personal")
	te.gh.basics = openBasics
	te.git.branch["/worktrees/REVIEW feat/x"] = "me/REVIEW-feat-x"
	if code := te.run("review", "https://github.com/acme/api/pull/7"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if got := strings.Join(launches(m.Commands), "|"); got != "CLAUDE_CONFIG_DIR='/home/me/.claude-personal' claude" {
		t.Fatalf("agent command = %q", got)
	}
	st, err := te.Rounds.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := st.AgentHome("acme/api#7"); got != "/home/me/.claude-personal" {
		t.Fatalf("stored profile = %q", got)
	}
}

func TestReviewIgnoresTheDefaultProfile(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		basics string
		want   []string
	}{
		"fork PR: started by " + brand.StateDir() + " too": {basics: strings.Replace(openBasics, `"headRefOid"`, `"isCrossRepository":true,"headRefOid"`, 1), want: []string{"claude"}},
		"same repository: started after the branch check":  {basics: openBasics, want: []string{"claude"}},
	} {
		t.Run(name, func(t *testing.T) {
			m := &fake.Manager{RepoList: []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}}}
			te := newTestEnv(t, m)
			te.withCallerHome(te.Home + "/.claude")
			te.gh.basics = c.basics
			te.git.branch["/worktrees/REVIEW feat/x"] = "me/REVIEW-feat-x"
			if code := te.run("review", "https://github.com/acme/api/pull/7"); code != 0 {
				t.Fatalf("exit %d: %s", code, te.err)
			}
			if !slices.Equal(launches(m.Commands), c.want) {
				t.Fatalf("launch commands %v, want %v", m.Commands, c.want)
			}
		})
	}
}

func TestReviewKeepsTheBranchWhenTheNameIsTakenOrTheHeadIsAFork(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		basics string
		taken  bool
	}{
		"local branch with the head name exists": {basics: openBasics, taken: true},
		"head branch lives in a fork":            {basics: strings.Replace(openBasics, `"headRefOid"`, `"isCrossRepository":true,"headRefOid"`, 1)},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			m := &fake.Manager{RepoList: []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}}}
			te := newTestEnv(t, m)
			te.gh.basics = tc.basics
			te.git.branch["/worktrees/REVIEW feat/x"] = "me/REVIEW-feat-x"
			te.git.branches["feat/x"] = tc.taken
			if code := te.run("review", "https://github.com/acme/api/pull/7"); code != 0 {
				t.Fatalf("exit %d: %s", code, te.err)
			}
			for _, w := range te.git.writes {
				if strings.Contains(w, "branch -m") {
					t.Fatalf("renamed the branch: %s", w)
				}
			}
			if !strings.Contains(strings.Join(m.Writes, "\n"), "link ws-1 7") {
				t.Fatalf("PR not linked: %v", m.Writes)
			}
		})
	}
}

func TestReviewRefusesABranchCheckedOutElsewhere(t *testing.T) {
	t.Parallel()
	for name, w := range map[string]wsdomain.Workspace{
		"main checkout":    {ID: "main-api", RepoID: "r1", Path: "/src/api", DisplayName: "api", Branch: "feat/x", HostID: "local", IsMain: true},
		"author worktree":  {ID: "ws-x", RepoID: "r1", Path: "/wt/x", DisplayName: "x", Branch: "feat/x", HostID: "local"},
		"other review one": {ID: "ws-r", RepoID: "r1", Path: "/wt/r", DisplayName: "REVIEW old", Branch: "feat/x", HostID: "local"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			m := &fake.Manager{RepoList: []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}}, WorkspaceList: []wsdomain.Workspace{w}}
			te := newTestEnv(t, m)
			te.gh.basics = openBasics
			if code := te.run("review", "https://github.com/acme/api/pull/7"); code == 0 {
				t.Fatalf("reviewed in a second worktree: %s", te.out)
			}
			if !strings.Contains(te.err.String(), "feat/x is checked out in "+w.Path) {
				t.Fatalf("err: %s", te.err)
			}
			if len(m.Writes) != 0 || len(m.Prompts) != 0 {
				t.Fatalf("writes %v prompts %+v", m.Writes, m.Prompts)
			}
		})
	}
}

func TestReviewRemovesTheWorkspaceWhenTheAgentDoesNotGetThePrompt(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		set   func(*fake.Manager)
		wants string
	}{
		"agent does not start": {func(m *fake.Manager) { m.StartFails = errors.New("tui-idle timeout") }, "create REVIEW feat/x\nremove --force ws-1"},
		"prompt not sent":      {func(m *fake.Manager) { m.SendFails = errors.New("terminal gone") }, "create REVIEW feat/x\nsession ws-1\nremove --force ws-1"},
	} {
		t.Run(name, func(t *testing.T) {
			m := &fake.Manager{RepoList: []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}}}
			c.set(m)
			te := newTestEnv(t, m)
			te.gh.basics = openBasics
			if code := te.run("review", "https://github.com/acme/api/pull/7"); code == 0 {
				t.Fatalf("kept a review workspace without an agent: %s", te.out)
			}
			if got := strings.Join(m.Writes, "\n"); got != c.wants || len(m.WorkspaceList) != 0 {
				t.Fatalf("writes:\n%s\nworkspaces %+v", got, m.WorkspaceList)
			}
		})
	}
}

func TestReviewReportsAnUnobservedSubmissionOnCreate(t *testing.T) {
	t.Parallel()
	m := &fake.Manager{RepoList: []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}}, NotSubmitted: true}
	te := newTestEnv(t, m)
	te.gh.basics = openBasics
	if code := te.run("review", "https://github.com/acme/api/pull/7"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if !strings.Contains(te.out.String(), "created REVIEW feat/x (submission not observed; check the session)") {
		t.Fatalf("out: %s", te.out)
	}
}

func TestReviewKeepsTheGuidanceOfARoundWhosePromptDidNotLand(t *testing.T) {
	t.Parallel()
	m := &fake.Manager{RepoList: []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}}, NotSubmitted: true}
	te := newTestEnv(t, m)
	te.gh.basics = openBasics
	agentDaemon(t, te)
	ref := github.Ref{Owner: "acme", Repo: "api", Number: 7}
	url := "https://github.com/acme/api/pull/7"

	if code := te.run("review", url, "look at the migration first"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if !strings.Contains(te.out.String(), "your guidance is kept for the next round") {
		t.Fatalf("out: %s", te.out)
	}
	m.NotSubmitted = false
	if code := te.run("review", url); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if got := m.Prompts[len(m.Prompts)-1].Text; !strings.Contains(turnText(t, te, got), "look at the migration first") {
		t.Fatalf("the retry lost the guidance: %q", got)
	}
	if code := te.run("review", url); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if got := m.Prompts[len(m.Prompts)-1].Text; strings.Contains(turnText(t, te, got), "## Guidance") {
		t.Fatalf("guidance applied to a second round that landed: %q", got)
	}
	if o, _ := te.Store.Override(context.Background(), ref); o.NextGuidance != "" {
		t.Fatalf("%+v", o)
	}
}

func TestReviewGuidanceReplacesTheKeptOne(t *testing.T) {
	t.Parallel()
	m := &fake.Manager{RepoList: []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}}}
	te := newTestEnv(t, m)
	te.gh.basics = openBasics
	agentDaemon(t, te)
	ref := github.Ref{Owner: "acme", Repo: "api", Number: 7}
	if err := te.Store.KeepNextGuidance(context.Background(), ref, "an old ask"); err != nil {
		t.Fatal(err)
	}
	if code := te.run("review", "https://github.com/acme/api/pull/7", "only the tests"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if got := turnText(t, te, m.Prompts[len(m.Prompts)-1].Text); !strings.Contains(got, "only the tests") || strings.Contains(got, "an old ask") {
		t.Fatalf("prompt %q", got)
	}
	if o, _ := te.Store.Override(context.Background(), ref); o.NextGuidance != "" {
		t.Fatalf("the replaced guidance stayed for the next round: %+v", o)
	}
}

func guidanceEvents(rec *memRecorder) []string {
	var out []string
	for _, ev := range rec.events {
		if ev.Kind == dinv.KindGuidance {
			out = append(out, ev.Data)
		}
	}
	return out
}

func TestEveryGuidanceSentIsRecordedWhetherOrNotItsPromptLands(t *testing.T) {
	t.Parallel()
	m := &fake.Manager{RepoList: []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}}, NotSubmitted: true}
	te := newTestEnv(t, m)
	te.gh.basics = openBasics
	rec := &memRecorder{}
	te.Rec = rec
	url := "https://github.com/acme/api/pull/7"
	if code := te.run("review", url, "look at the migration first"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	m.NotSubmitted = false
	if code := te.run("review", url, "then the rollback"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if code := te.run("review", url); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if got := guidanceEvents(rec); !slices.Equal(got, []string{"look at the migration first", "then the rollback"}) {
		t.Fatalf("guidance events %q", got)
	}
}

func TestReviewDiscardsAWorkspaceOrcaPutOnARenamedBranch(t *testing.T) {
	t.Parallel()
	m := &fake.Manager{ManagerName: "orca", BranchSuffix: "-2", RepoList: []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}}}
	te := newTestEnv(t, m)
	te.gh.basics = strings.Replace(openBasics, `"headRefName":"feat/x"`, `"headRefName":"feat"`, 1)
	if code := te.run("review", "https://github.com/acme/api/pull/7"); code == 0 {
		t.Fatal("kept a workspace on a renamed branch")
	}
	if want := "Orca created REVIEW feat on branch feat-2 instead of feat, which is taken; removed it"; !strings.Contains(te.err.String(), want) {
		t.Fatalf("stderr %q, want %q", te.err, want)
	}
}
