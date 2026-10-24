package orca_test

import (
	"context"
	"testing"

	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/workspace/orca/orcatest"
)

func linkWorld() orcatest.World {
	return orcatest.World{
		Repos:     []orcatest.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}},
		Worktrees: []orcatest.Worktree{{ID: "r1::/wt/a", RepoID: "r1", Path: "/wt/a", DisplayName: "a", Branch: "refs/heads/a", LinkedPR: 3, Comment: "asks: keep both?"}},
	}
}

func worktree(rt *orcatest.Runtime) orcatest.Worktree {
	var w orcatest.Worktree
	rt.Update(func(x *orcatest.World) { w = x.Worktrees[0] })
	return w
}

var (
	threadA = unit.Unit{Provider: "thread", Key: "T000/C001/1.1"}
	threadB = unit.Unit{Provider: "thread", Key: "T000/C001/2.2"}
)

func TestOrcaLinkUnitGitHubSetsLinkedPR(t *testing.T) {
	rt := orcatest.Start(t, linkWorld())
	if err := rt.Manager().LinkUnit(context.Background(), "r1::/wt/a", unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}); err != nil {
		t.Fatal(err)
	}
	if w := worktree(rt); w.LinkedPR != 7 || w.Comment != "asks: keep both?" {
		t.Fatalf("worktree %+v", w)
	}
}

func TestOrcaLinkUnitOtherProviderLeavesTheCard(t *testing.T) {
	rt := orcatest.Start(t, linkWorld())
	m := rt.Manager()
	for _, u := range []unit.Unit{threadA, threadB} {
		if err := m.LinkUnit(context.Background(), "r1::/wt/a", u); err != nil {
			t.Fatal(err)
		}
	}
	if w := worktree(rt); w.LinkedPR != 3 || w.Comment != "asks: keep both?" {
		t.Fatalf("worktree %+v", w)
	}
}
