package orca_test

import (
	"context"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/orca"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/view"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/meter/metertest"
	"github.com/wloophq/wloop/internal/workspace"
	"github.com/wloophq/wloop/internal/workspace/orca/orcatest"
)

func TestAdapterWritesInvalidate(t *testing.T) {
	ref := pr.Ref{Owner: "acme", Repo: "repo-0", Number: 1}
	links := func(context.Context) (map[unit.Unit]orca.Link, error) {
		return map[unit.Unit]orca.Link{ref.Unit(): {WorkspaceID: orcatest.WorktreeID(0), Review: true}}, nil
	}
	for _, c := range []struct {
		name  string
		write func(ctx context.Context, a *orca.Adapter) error
		want  wsdomain.Part
	}{
		{"a card", func(ctx context.Context, a *orca.Adapter) error {
			_, err := a.Apply(ctx, []view.Item{{Unit: ref.Unit(), Role: view.ReviewRole, Status: view.NeedsMe, Summary: "asks"}}, nil)
			return err
		}, wsdomain.PartWorkspaces},
		{"a close", func(ctx context.Context, a *orca.Adapter) error { return a.Close(ctx, orcatest.Handle(0)) }, wsdomain.PartPanes | wsdomain.PartTerminals},
		{"a typed line", func(ctx context.Context, a *orca.Adapter) error {
			_, err := a.Type(ctx, orcatest.Handle(0), "hello")
			return err
		}, wsdomain.PartPanes},
		{"a wait for the prompt", func(ctx context.Context, a *orca.Adapter) error {
			_, err := a.WaitIdle(ctx, orcatest.Handle(0), time.Second)
			return err
		}, wsdomain.PartPanes},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := orcatest.Start(t, orcatest.Live()).Manager()
			ws := workspace.NewCached(m, m, 2*time.Minute)
			a, err := orca.Ports(ws, cols, links)
			if err != nil {
				t.Fatal(err)
			}
			bg := context.Background()
			if _, err := ws.Cache.Read(bg, wsdomain.PartMutable); err != nil {
				t.Fatal(err)
			}
			if err := c.write(bg, a); err != nil {
				t.Fatal(err)
			}
			ctx, sc := metertest.Start(t, bg)
			if _, err := ws.Cache.Read(ctx, wsdomain.PartMutable); err != nil {
				t.Fatal(err)
			}
			for part, method := range map[wsdomain.Part]string{wsdomain.PartWorkspaces: "worktree.list", wsdomain.PartPanes: "worktree.ps", wsdomain.PartTerminals: "terminal.list"} {
				want := int64(0)
				if c.want&part != 0 {
					want = 1
				}
				if n := metertest.Count(sc, metertest.Limit{Res: meter.Workspace, Op: "rpc " + method}); n != want {
					t.Errorf("rpc %s after %s: %d calls, want %d", method, c.name, n, want)
				}
			}
		})
	}
}
