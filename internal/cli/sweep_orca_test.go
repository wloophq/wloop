package cli

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/daemon"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/meter/metertest"
	"github.com/wloophq/wloop/internal/turn"
	"github.com/wloophq/wloop/internal/workspace"
	"github.com/wloophq/wloop/internal/workspace/orca/orcatest"
)

// orcaFollowup is followupScenario on the orca manager over a fake runtime, behind the
// shared cache, warmed the way a fast cycle leaves it.
func orcaFollowup(t *testing.T) (*orcatest.Runtime, *testEnv) {
	t.Helper()
	_, te := scenario(t)
	rt := orcatest.Start(t, orcatest.World{
		Repos: []orcatest.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}},
		Worktrees: []orcatest.Worktree{{ID: "ws-rev", RepoID: "r1", Path: "/wt/rev", DisplayName: "REVIEW feat/x", Branch: "refs/heads/feat/x",
			Status: "reviewing", LinkedPR: 7, Agents: []orcatest.Agent{{Pane: "tab-rev:leaf-1", State: "done", StateStartedAt: testNow.Add(-10 * time.Minute).UnixMilli()}}}},
		Terminals: []orcatest.Terminal{{Handle: "term-rev", WorktreeID: "ws-rev", TabID: "tab-rev", Title: "review", Agent: "claude",
			LastOutputAt: testNow.Add(-10 * time.Minute).UnixMilli()}},
	})
	m := rt.Manager()
	c := workspace.NewCached(m, m, 2*time.Minute)
	te.WS = workspace.NewSet(c)
	if _, err := c.Cache.Read(context.Background(), wsdomain.PartAll); err != nil {
		t.Fatal(err)
	}
	return rt, te
}

func TestStartAddressRereadsAWaitingAgent(t *testing.T) {
	rt, te := orcaFollowup(t)
	rt.Update(func(w *orcatest.World) { w.Worktrees[0].Agents[0].State = "waiting" })
	ws, ok, err := te.findFresh(context.Background(), "ws-rev")
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	cfg, err := te.loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	ref := github.Ref{Owner: "acme", Repo: "api", Number: 7}
	_, _, _, err = te.startAddress(context.Background(), cfg, ws, turn.Request{Intent: turn.Intent(dintent.Address), Unit: ref.Unit()}, roundOptions{})
	if !errors.Is(err, errWaiting) {
		t.Fatalf("err = %v, want errWaiting", err)
	}
	if len(te.git.writes) != 0 {
		t.Fatalf("git writes %v", te.git.writes)
	}
}

func TestRemoveRereadsAnAgentThatStartedSinceTheSnapshot(t *testing.T) {
	_, te := finishedScenario(t, "MERGED")
	rt := orcatest.Start(t, orcatest.World{
		Repos: []orcatest.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}},
		Worktrees: []orcatest.Worktree{{ID: doneWS, RepoID: "r1", Path: "/wt/done", DisplayName: "REVIEW feat/done", Branch: "refs/heads/feat/done",
			LinkedPR: 12, Agents: []orcatest.Agent{{Pane: "tab-done:leaf-1", State: "done"}}}},
	})
	m := rt.Manager()
	c := workspace.NewCached(m, m, 2*time.Minute)
	te.WS = workspace.NewSet(c)
	if _, err := c.Cache.Read(context.Background(), wsdomain.PartAll); err != nil {
		t.Fatal(err)
	}
	rt.Update(func(w *orcatest.World) { w.Worktrees[0].Agents[0].State = "working" })

	ctx, sc := metertest.Start(t, context.Background())
	var out strings.Builder
	err := envActions{e: te.Env}.Remove(ctx, r12.Unit(), daemon.Call{Out: &out, WorkspaceID: doneWS})
	wantRefusal(t, err, "its agent is working")
	if n := metertest.Count(sc, metertest.Limit{Res: meter.Workspace, Op: "worktree rm"}); n != 0 {
		t.Fatalf("worktree rm ran %d times under a working agent", n)
	}
}
