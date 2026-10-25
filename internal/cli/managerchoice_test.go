package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/workspace"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

// desktopLike honors only the default launch, as Claude Desktop does.
func desktopLike() *fake.Manager {
	return &fake.Manager{ManagerName: "desk", OnlyAgents: []string{"claude"}, Refuses: errors.New("loads the worktree's settings")}
}

func TestAnUntrustedPullRequestFallsThroughToTheNextManager(t *testing.T) {
	t.Parallel()
	desk, orca := desktopLike(), &fake.Manager{ManagerName: "orca"}
	te := newTestEnv(t, desk)
	te.WS = workspace.NewSet(desk, orca)
	te.cfg.WorkspaceManagers = []string{"desk", "orca"}
	ctx := context.Background()
	pr := &github.PullRequest{}

	m, err := te.chooseManager(ctx, te.cfg, roundOptions{pr: pr, trusted: true})
	if err != nil || m.Name() != "desk" {
		t.Fatalf("a trusted pull request went to %v, %v", m, err)
	}
	m, err = te.chooseManager(ctx, te.cfg, roundOptions{pr: pr, trusted: false})
	if err != nil || m.Name() != "orca" {
		t.Fatalf("an untrusted pull request went to %v, %v", m, err)
	}

	te.cfg.WorkspaceManagers = []string{"desk"}
	_, err = te.chooseManager(ctx, te.cfg, roundOptions{pr: pr, trusted: false})
	if err == nil || !strings.Contains(err.Error(), "desk: loads the worktree's settings") {
		t.Fatalf("no manager left: %v", err)
	}
}

func TestAnUnreachableManagerTakesNoNewSession(t *testing.T) {
	t.Parallel()
	desk, orca := desktopLike(), &fake.Manager{ManagerName: "orca"}
	desk.Unreachable = workspace.ErrUnreachable
	te := newTestEnv(t, desk)
	te.WS = workspace.NewSet(desk, orca)
	te.cfg.WorkspaceManagers = []string{"desk", "orca"}
	m, err := te.chooseManager(context.Background(), te.cfg, roundOptions{})
	if err != nil || m.Name() != "orca" {
		t.Fatalf("with the first manager down the session went to %v, %v", m, err)
	}
}

// madeByDesk is an address round's pull request whose worktree desk created
// and orca, configured first, also lists.
func madeByDesk(t *testing.T) (desk, orca *fake.Manager, te *testEnv) {
	t.Helper()
	const repo, path = "/src/api", "/src/api/.claude/worktrees/brave-lovelace"
	desk = desktopLike()
	desk.Refuses = nil
	desk.RepoList = []wsdomain.Repo{{ID: repo, Path: repo, RemoteKey: "github.com/acme/api"}}
	desk.WorkspaceList = []wsdomain.Workspace{{ID: path, RepoID: repo, Path: path, DisplayName: "brave-lovelace", Branch: "feat/z", HostID: "local", Created: true}}
	orca = &fake.Manager{ManagerName: "orca",
		RepoList:      []wsdomain.Repo{{ID: "r1", Path: repo, RemoteKey: "github.com/acme/api"}},
		WorkspaceList: []wsdomain.Workspace{{ID: "r1::" + path, RepoID: "r1", Path: path, DisplayName: "brave-lovelace", Branch: "feat/z", HostID: "local"}},
	}
	te = newTestEnv(t, orca)
	te.WS = workspace.NewSet(orca, desk)
	te.cfg.WorkspaceManagers = []string{"orca", "desk"}
	te.gh.basics = `{"data":{"pr":{"pullRequest":{"number":9,"title":"Add z","state":"OPEN","headRefName":"feat/z","headRefOid":"hfeat/z","repository":{"nameWithOwner":"acme/api"},"author":{"__typename":"User","login":"me"}}}}}`
	te.git.head[path] = "hfeat/z"
	te.git.ancestor[path] = true
	return desk, orca, te
}

func sessionsStarted(m *fake.Manager) []string {
	var out []string
	for _, w := range m.Writes {
		if strings.HasPrefix(w, "session ") {
			out = append(out, w)
		}
	}
	return out
}

func TestARoundContinuesInTheManagerThatCreatedTheWorktree(t *testing.T) {
	t.Parallel()
	desk, orca, te := madeByDesk(t)
	if code := te.run("address", "https://github.com/acme/api/pull/9"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if got := sessionsStarted(desk); len(got) != 1 || len(sessionsStarted(orca)) != 0 {
		t.Fatalf("desk started %v, orca %v", got, sessionsStarted(orca))
	}
}

func TestARoundFallsThroughWhenTheCreatorCannotHostIt(t *testing.T) {
	t.Parallel()
	desk, orca, te := madeByDesk(t)
	desk.Refuses = errors.New("runs only the default home")
	if code := te.run("address", "https://github.com/acme/api/pull/9"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if got := sessionsStarted(orca); len(got) != 1 || got[0] != "session r1::/src/api/.claude/worktrees/brave-lovelace" || len(sessionsStarted(desk)) != 0 {
		t.Fatalf("orca started %v, desk %v", got, sessionsStarted(desk))
	}
	if !strings.Contains(te.err.String(), "Orca launches this session (desk: runs only the default home)") {
		t.Fatalf("no reason logged: %s", te.err)
	}
}
