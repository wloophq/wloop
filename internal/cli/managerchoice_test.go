package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/workspace"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

// desktopLike honors only the default launch, as Claude Desktop does.
func desktopLike() *fake.Manager {
	return &fake.Manager{ManagerName: "desk", OnlyAgents: []string{"claude"}, Refuses: errors.New("loads the worktree's settings")}
}

func TestAnUntrustedPullRequestFallsThroughToTheNextManager(t *testing.T) {
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
