package workspace_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/unit"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/workspace"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

// custody is an in-memory workspace.Custody that can drop a worktree.
type custody struct {
	byPath  map[string]wsdomain.Custody
	dropped []string
}

func newCustody(cs ...wsdomain.Custody) *custody {
	c := &custody{byPath: map[string]wsdomain.Custody{}}
	for _, x := range cs {
		c.byPath[x.Path] = x
	}
	return c
}

func (c *custody) Worktrees(context.Context) ([]wsdomain.Custody, error) {
	var out []wsdomain.Custody
	for _, x := range c.byPath {
		out = append(out, x)
	}
	return out, nil
}

func (c *custody) Keep(_ context.Context, x wsdomain.Custody) error {
	c.byPath[x.Path] = x
	return nil
}

func (c *custody) LinkUnit(_ context.Context, path string, u unit.Unit) error {
	x := c.byPath[path]
	x.Units = append(x.Units, u)
	c.byPath[path] = x
	return nil
}

func (c *custody) Drop(_ context.Context, path string) error {
	delete(c.byPath, path)
	c.dropped = append(c.dropped, path)
	return nil
}

// twoManagers are orca and desktop, in that order, both listing /wt/a.
func twoManagers() (*fake.Manager, *fake.Manager) {
	orca := &fake.Manager{ManagerName: "orca", WorkspaceList: []wsdomain.Workspace{
		{ID: "o1", Path: "/wt/a"}, {ID: "o2", Path: "/wt/b", LinkedPR: 3},
	}}
	desktop := &fake.Manager{ManagerName: "desktop", WorkspaceList: []wsdomain.Workspace{
		{ID: "d1", Path: "/wt/a"}, {ID: "d2", Path: "/wt/c"},
	}}
	return orca, desktop
}

func ids(ws []wsdomain.Workspace) []string {
	var out []string
	for _, w := range ws {
		out = append(out, w.Manager+":"+w.ID)
	}
	return out
}

func list(t *testing.T, s *workspace.Set) []wsdomain.Workspace {
	t.Helper()
	ws, err := s.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return ws
}

func TestSetListSettlesAPathTwoManagersList(t *testing.T) {
	orca, desktop := twoManagers()
	s := workspace.NewSet(orca, desktop)
	if got := ids(list(t, s)); !slices.Equal(got, []string{"orca:o1", "orca:o2", "desktop:d2"}) {
		t.Fatalf("by priority: %v", got)
	}

	s.UseCustody(newCustody(wsdomain.Custody{Path: "/wt/a", Manager: "desktop"}))
	if got := ids(list(t, s)); !slices.Equal(got, []string{"orca:o2", "desktop:d1", "desktop:d2"}) {
		t.Fatalf("by custody: %v", got)
	}

	s = workspace.NewSet(desktop, orca)
	if got := ids(list(t, s)); !slices.Equal(got, []string{"desktop:d1", "desktop:d2", "orca:o2"}) {
		t.Fatalf("by priority, desktop first: %v", got)
	}
}

func TestSetListGivesAWorktreeToTheManagerThatCreatedIt(t *testing.T) {
	orca, desktop := twoManagers()
	desktop.WorkspaceList[0].Created = true
	s := workspace.NewSet(orca, desktop)
	if got := ids(list(t, s)); !slices.Equal(got, []string{"orca:o2", "desktop:d1", "desktop:d2"}) {
		t.Fatalf("over priority: %v", got)
	}
	s.UseCustody(newCustody(wsdomain.Custody{Path: "/wt/a", Manager: "orca"}))
	if got := ids(list(t, s)); !slices.Equal(got, []string{"orca:o2", "desktop:d1", "desktop:d2"}) {
		t.Fatalf("over a custody the loop found: %v", got)
	}
	s.UseCustody(newCustody(wsdomain.Custody{Path: "/wt/a", Manager: "orca", CreatedBy: "orca"}))
	if got := ids(list(t, s)); !slices.Equal(got, []string{"orca:o1", "orca:o2", "desktop:d2"}) {
		t.Fatalf("custody of a worktree the loop created: %v", got)
	}
}

func TestSetListingsAreEveryManagersRowOfAPath(t *testing.T) {
	orca, desktop := twoManagers()
	s := workspace.NewSet(desktop, orca)
	if got := ids(s.Listings(context.Background(), "/wt/a")); !slices.Equal(got, []string{"desktop:d1", "orca:o1"}) {
		t.Fatalf("Listings = %v", got)
	}
}

func TestSetListLinksThePullRequestOfCustody(t *testing.T) {
	orca, desktop := twoManagers()
	s := workspace.NewSet(orca, desktop)
	s.UseCustody(newCustody(
		wsdomain.Custody{Path: "/wt/c", Manager: "desktop", Units: []unit.Unit{{Provider: "slack", Key: "T/C/1.1"}, {Provider: unit.GitHub, Key: "acme/api#7"}}},
		wsdomain.Custody{Path: "/wt/b", Manager: "orca", Units: []unit.Unit{{Provider: unit.GitHub, Key: "acme/api#9"}}},
	))
	linked := map[string]int{}
	for _, w := range list(t, s) {
		linked[w.ID] = w.LinkedPR
	}
	if linked["d2"] != 7 || linked["o2"] != 3 || linked["o1"] != 0 {
		t.Fatalf("linked %v, want d2 from custody and o2 kept as its manager shows it", linked)
	}
}

func TestSetListToleratesOneFailingManager(t *testing.T) {
	orca, desktop := twoManagers()
	orca.Unreachable = errors.New("orca down")
	s := workspace.NewSet(orca, desktop)
	if got := ids(list(t, s)); !slices.Equal(got, []string{"desktop:d1", "desktop:d2"}) {
		t.Fatalf("one failing: %v", got)
	}
	desktop.Unreachable = errors.New("desktop down")
	if _, err := s.List(context.Background()); err == nil {
		t.Fatal("every manager failing is no error")
	}
}

func TestSetReachable(t *testing.T) {
	ctx := context.Background()
	orca, desktop := twoManagers()
	orca.Unreachable = errors.New("orca down")
	s := workspace.NewSet(orca, desktop)
	if err := s.Reachable(ctx); err != nil {
		t.Fatalf("one manager up: %v", err)
	}
	if got := s.Unreachable(ctx); len(got) != 1 || got["orca"] == nil {
		t.Fatalf("Unreachable %v", got)
	}
	desktop.Unreachable = errors.New("desktop down")
	if err := s.Reachable(ctx); err == nil {
		t.Fatal("every manager down is reachable")
	}
	if err := workspace.NewSet().Reachable(ctx); !errors.Is(err, workspace.ErrUnreachable) {
		t.Fatalf("no manager: %v", err)
	}
}

func TestSetRemoveAndUnregisterRouteByID(t *testing.T) {
	ctx := context.Background()
	orca, desktop := twoManagers()
	s := workspace.NewSet(orca, desktop)
	c := newCustody(
		wsdomain.Custody{Path: "/wt/a", Manager: "desktop"},
		wsdomain.Custody{Path: "/wt/b", Manager: "orca"},
	)
	s.UseCustody(c)
	if err := s.Remove(ctx, "d1", true); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(desktop.Writes, []string{"remove --force d1"}) || len(orca.Writes) != 0 {
		t.Fatalf("writes orca %v, desktop %v", orca.Writes, desktop.Writes)
	}
	if err := s.Unregister(ctx, "o2"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(orca.Writes, []string{"unregister o2"}) {
		t.Fatalf("writes orca %v", orca.Writes)
	}
	if !slices.Equal(c.dropped, []string{"/wt/a", "/wt/b"}) || len(c.byPath) != 0 {
		t.Fatalf("dropped %v, left %v", c.dropped, c.byPath)
	}
	if err := s.Remove(ctx, "nobody", false); !errors.Is(err, workspace.ErrUnknownWorkspace) {
		t.Fatalf("an unknown id: %v", err)
	}
}

// partialWorkspaces lists the fake's workspaces as an incomplete listing.
type partialWorkspaces struct{ *fake.Manager }

func (p partialWorkspaces) Workspaces(ctx context.Context) ([]wsdomain.Workspace, wsdomain.Scope, error) {
	ws, sc, err := p.Manager.Workspaces(ctx)
	sc.Complete = false
	return ws, sc, err
}

func TestSetReadMergesSnapshots(t *testing.T) {
	ctx := context.Background()
	orca, desktop := twoManagers()
	desktop.Activity = map[string]wsdomain.Activity{"d1": {Busy: true}}
	co := workspace.NewCached(orca, orca, time.Minute)
	cd := workspace.NewCached(desktop, partialWorkspaces{desktop}, time.Minute)
	s := workspace.NewSet(co, cd)
	snap, err := s.Read(ctx, wsdomain.PartAll)
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(snap.Workspaces); !slices.Equal(got, []string{"orca:o2", "desktop:d1", "desktop:d2"}) {
		t.Fatalf("workspaces %v, want /wt/a settled to the manager with an agent there", got)
	}
	if !snap.WorkspacesComplete("orca") || snap.WorkspacesComplete("desktop") || snap.WorkspaceScope.Complete {
		t.Fatalf("scopes %+v, merged %+v", snap.Scopes, snap.WorkspaceScope)
	}
	if want := co.Snapshot().Last().Gen + cd.Snapshot().Last().Gen; snap.Gen != want || want == 0 {
		t.Fatalf("Gen %d, want the sum %d", snap.Gen, want)
	}
	if len(snap.Panes) != 1 || snap.Panes[0].Manager != "desktop" {
		t.Fatalf("panes %+v", snap.Panes)
	}
}

func TestSetReadLeavesAFailingManagerOut(t *testing.T) {
	ctx := context.Background()
	orca, desktop := twoManagers()
	orca.Unreachable = errors.New("orca down")
	s := workspace.NewSet(workspace.NewCached(orca, orca, time.Minute), workspace.NewCached(desktop, desktop, time.Minute))
	snap, err := s.Read(ctx, wsdomain.PartAll)
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(snap.Workspaces); !slices.Equal(got, []string{"desktop:d1", "desktop:d2"}) {
		t.Fatalf("workspaces %v", got)
	}
	if snap.WorkspacesComplete("orca") || !snap.WorkspacesComplete("desktop") || snap.WorkspaceScope.Complete {
		t.Fatalf("scopes %+v, merged %+v", snap.Scopes, snap.WorkspaceScope)
	}
	desktop.Unreachable = errors.New("desktop down")
	s = workspace.NewSet(workspace.NewCached(orca, orca, time.Minute), workspace.NewCached(desktop, desktop, time.Minute))
	if _, err := s.Read(ctx, wsdomain.PartAll); err == nil {
		t.Fatal("every manager failing is no error")
	}
}
