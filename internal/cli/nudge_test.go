package cli

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/config"
	dgh "github.com/wloophq/wloop/internal/domain/github"
	"github.com/wloophq/wloop/internal/domain/pr"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

const nudgeLine = "Waiting on: @ana since 2026-10-05, changes requested."

func nudgeManager(ws ...wsdomain.Workspace) *fake.Manager {
	return &fake.Manager{
		RepoList:      []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api", DefaultBranch: "main"}},
		WorkspaceList: ws,
	}
}

var (
	reviewWS = wsdomain.Workspace{ID: "ws-rev", RepoID: "r1", Path: "/wt/rev", DisplayName: "REVIEW feat/x", Branch: "feat/x", HostID: "local", LinkedPR: 7}
	mainWS   = wsdomain.Workspace{ID: "ws-main", RepoID: "r1", Path: "/src/api", DisplayName: "api", Branch: "main", HostID: "local", IsMain: true}
)

func TestNudgeRunsInANewSessionOfTheReviewWorkspace(t *testing.T) {
	m := nudgeManager(reviewWS)
	m.Sessions = map[string]string{"ws-rev": "term-live"}
	te := newTestEnv(t, m)
	te.gh.basics = openBasics
	if err := nudgePR(context.Background(), te.Env, te.cfg, r7, nudgeLine, false, "the migration", ""); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(m.Writes, []string{"session ws-rev", "send term-ws-rev"}) {
		t.Fatalf("writes %v", m.Writes)
	}
	if len(m.Prompts) != 1 {
		t.Fatalf("prompts %+v", m.Prompts)
	}
	if h, _, _, _ := promptOf(t, m.Prompts[0].Text); h != "Nudge on acme/api#7, asked for by you." {
		t.Fatalf("prompts %+v", m.Prompts)
	}
	if text := turnText(t, te, m.Prompts[0].Text); !strings.Contains(text, nudgeLine) || !strings.Contains(text, "the migration") {
		t.Fatalf("turn:\n%s", text)
	}
	if !strings.Contains(te.out.String(), "nudge in REVIEW feat/x") {
		t.Fatalf("out: %s", te.out)
	}
}

func TestOnlyANudgeOnTheMaintainersReadsTheirSection(t *testing.T) {
	for _, c := range []struct {
		situation   string
		maintainers bool
	}{
		{"Waiting on: the maintainers (no reviewer requested) since 2026-10-05, no reviewer.", true},
		{nudgeLine, false},
	} {
		m := nudgeManager(mainWS)
		te := newTestEnv(t, m)
		te.gh.basics = openBasics
		if err := nudgePR(context.Background(), te.Env, te.cfg, r7, c.situation, c.maintainers, "", ""); err != nil {
			t.Fatal(err)
		}
		if len(m.Prompts) != 1 {
			t.Fatalf("%s: prompts %+v", c.situation, m.Prompts)
		}
		if got := strings.Contains(turnText(t, te, m.Prompts[0].Text), "When it waits on the maintainers"); got != c.maintainers {
			t.Errorf("%s: the maintainers' section rendered: %v", c.situation, got)
		}
	}
}

func TestNudgeWorkspaceOrder(t *testing.T) {
	cfg := config.Defaults()
	repos := nudgeManager().RepoList
	pr := &github.PullRequest{Facts: pr.Facts{HeadRefName: "feat/x", State: dgh.StateOpen}}
	onHead := mainWS
	onHead.Branch = "feat/x"
	remoteMain := mainWS
	remoteMain.HostID = "ssh:box"
	remoteReview := reviewWS
	remoteReview.HostID = "ssh:box"
	authorWS := wsdomain.Workspace{ID: "ws-dev", RepoID: "r1", Path: "/wt/dev", DisplayName: "feat/x", Branch: "feat/x", HostID: "local", LinkedPR: 7}
	otherRepo := mainWS
	otherRepo.RepoID = "r2"
	for _, c := range []struct {
		name  string
		all   []wsdomain.Workspace
		mine  bool
		want  string
		where string
		asked bool
	}{
		{"the review workspace first", []wsdomain.Workspace{mainWS, authorWS, reviewWS}, false, "ws-rev", "review workspace", false},
		{"the review workspace over a main checkout on the head branch", []wsdomain.Workspace{onHead, reviewWS}, false, "ws-rev", "review workspace", false},
		{"then the author worktree, over the main checkout", []wsdomain.Workspace{mainWS, authorWS}, false, "ws-dev", "author workspace", false},
		{"the main checkout when the pull request has no workspace", []wsdomain.Workspace{mainWS}, false, "ws-main", "main checkout", false},
		{"a main checkout on my head branch is my author workspace", []wsdomain.Workspace{onHead}, true, "ws-main", "author workspace", true},
		{"a main checkout on someone else's head branch is nothing", []wsdomain.Workspace{onHead}, false, "", "", true},
		{"a remote main checkout is skipped", []wsdomain.Workspace{remoteMain, reviewWS}, false, "ws-rev", "review workspace", false},
		{"remote checkouts only", []wsdomain.Workspace{remoteMain, remoteReview}, false, "", "", false},
		{"another repository's checkout", []wsdomain.Workspace{otherRepo}, false, "", "", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			asked := false
			mine := func() (bool, error) { asked = true; return c.mine, nil }
			w, where, err := nudgeWorkspace(cfg, "Orca", repos, c.all, r7, pr, mine)
			if c.want == "" {
				if err == nil || !strings.Contains(err.Error(), "no workspace of the pull request and no Orca checkout of acme/api") {
					t.Fatalf("got %s (%s), err %v", w.ID, where, err)
				}
			} else if err != nil || w.ID != c.want || where != c.where {
				t.Fatalf("got %s (%s), err %v; want %s (%s)", w.ID, where, err, c.want, c.where)
			}
			if asked != c.asked {
				t.Fatalf("asked the viewer: %v", asked)
			}
		})
	}
}

func TestNudgeRefusesAClosedPullRequestBeforeStartingAnything(t *testing.T) {
	m := nudgeManager(mainWS)
	te := newTestEnv(t, m)
	te.gh.basics = strings.Replace(openBasics, `"state":"OPEN"`, `"state":"MERGED"`, 1)
	err := nudgePR(context.Background(), te.Env, te.cfg, r7, nudgeLine, false, "", "")
	if err == nil || !strings.Contains(err.Error(), "is MERGED") {
		t.Fatalf("err %v", err)
	}
	if len(m.Writes) != 0 {
		t.Fatalf("writes %v", m.Writes)
	}
}

func TestNudgeFallsBackToTheMainCheckout(t *testing.T) {
	m := nudgeManager(mainWS)
	te := newTestEnv(t, m)
	te.gh.basics = openBasics
	if err := nudgePR(context.Background(), te.Env, te.cfg, r7, nudgeLine, false, "", ""); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(m.Writes, []string{"session ws-main", "send term-ws-main"}) {
		t.Fatalf("writes %v", m.Writes)
	}
	if len(m.Prompts) != 1 {
		t.Fatalf("prompts %+v", m.Prompts)
	}
	if strings.Contains(m.Prompts[0].Text, "--style") {
		t.Fatalf("a style passed with no file: %q", m.Prompts[0].Text)
	}
}

func TestNudgeWithoutACheckoutStartsNothing(t *testing.T) {
	m := &fake.Manager{ManagerName: "orca"}
	te := newTestEnv(t, m)
	te.gh.basics = openBasics
	err := nudgePR(context.Background(), te.Env, te.cfg, r7, nudgeLine, false, "", "")
	if err == nil || !strings.Contains(err.Error(), "no workspace of the pull request and no Orca checkout of acme/api on its default branch to run the nudge from; add the repository to Orca") {
		t.Fatalf("err %v", err)
	}
	if len(m.Writes) != 0 || len(te.git.writes) != 0 {
		t.Fatalf("writes %v, git %v", m.Writes, te.git.writes)
	}
}

func TestNudgeStartsTheAgentInTheProjectsProfile(t *testing.T) {
	m := nudgeManager(mainWS)
	te := newTestEnv(t, m)
	te.gh.basics = openBasics
	if err := nudgePR(context.Background(), te.Env, te.cfg, r7, nudgeLine, false, "", "/home/me/.claude-work"); err != nil {
		t.Fatal(err)
	}
	if len(m.Commands) != 1 || launches(m.Commands)[0] != "CLAUDE_CONFIG_DIR='/home/me/.claude-work' claude" {
		t.Fatalf("commands %v", m.Commands)
	}
}
