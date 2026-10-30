package cli

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/adapter/store/inventory/inventorytest"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	wt "github.com/wloophq/wloop/internal/core/workspaces/workspacestest"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/placement"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	psource "github.com/wloophq/wloop/internal/plugins/source"
	"github.com/wloophq/wloop/internal/readmodel"
	"github.com/wloophq/wloop/internal/readmodel/prrow/ghreporttest"
	src "github.com/wloophq/wloop/pkg/plugin/source"
	ghplugin "github.com/wloophq/wloop/plugins/github"
)

// pluginReports are the GitHub plugin's last snapshots, by unit.
type pluginReports map[unit.Unit]src.Snapshot

func (p pluginReports) Units(_ context.Context, id unit.ProviderID) ([]unit.Unit, error) {
	var out []unit.Unit
	for u := range p {
		if u.Provider == id {
			out = append(out, u)
		}
	}
	return out, nil
}

func (p pluginReports) Reported(_ context.Context, units []unit.Unit) (map[unit.Unit]src.Snapshot, error) {
	out := map[unit.Unit]src.Snapshot{}
	for _, u := range units {
		if s, ok := p[u]; ok {
			out[u] = s
		}
	}
	return out, nil
}

// reportAgainstHostCopy has te's GitHub plugin report reported, while te's
// host keeps copy of the same pull request, or none when copy is nil; the
// readers under test read te's reports, not the harness's planner inputs.
func reportAgainstHostCopy(t *testing.T, te *testEnv, reported dinv.PR, copy *dinv.PR) {
	t.Helper()
	ctx := context.Background()
	if err := te.Store.SetViewer(ctx, unit.GitHub, "ana"); err != nil {
		t.Fatal(err)
	}
	if copy != nil {
		if err := inventorytest.Of(te.Store).UpsertPR(ctx, *copy); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := json.Marshal(ghplugin.Data{PR: ghreporttest.PluginPR(reported)})
	if err != nil {
		t.Fatal(err)
	}
	reports := pluginReports{reported.Ref.Unit(): {Unit: psource.Wire(reported.Ref.Unit()), Data: raw}}
	te.Env.reports = func(*inventory.Store) readmodel.Reports { return reports }
	te.Env.planReads = nil
}

var reportedRef = pr.Ref{Owner: "acme", Repo: "api", Number: 7}

func TestAPlacementReadsTheAuthorThePluginReported(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, nil)
	reportAgainstHostCopy(t, te, dinv.PR{Ref: reportedRef, Author: "ana", State: "OPEN"}, &dinv.PR{Ref: reportedRef, Author: "bruno", State: "OPEN"})
	p, err := te.Env.placements(te.cfg).Place(context.Background(), reportedRef.Unit(), "")
	if err != nil || p.Role != placement.RoleAuthor {
		t.Fatalf("placement %+v %v, want the reported author's", p, err)
	}
}

func TestAPlannedRoundGoesWithThePullRequestThePluginReported(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, nil)
	reportAgainstHostCopy(t, te, dinv.PR{Ref: reportedRef, Author: "bruno", State: "OPEN", HeadOid: "plugin-head"},
		&dinv.PR{Ref: reportedRef, Author: "bruno", State: "OPEN", HeadOid: "host-head"})
	got, err := te.Env.keptPR(context.Background(), reportedRef.Unit())
	if err != nil || got.HeadRefOid != "plugin-head" {
		t.Fatalf("the round's pull request %+v %v", got.Facts, err)
	}
}

func TestAWorkspaceStepTargetsTheHeadThePluginReported(t *testing.T) {
	t.Parallel()
	r := newRemovalRig(t, wt.Checkout{})
	reportAgainstHostCopy(t, r.te, dinv.PR{Ref: reportedRef, Author: "bruno", State: "OPEN", HeadOid: "plugin-head"},
		&dinv.PR{Ref: reportedRef, Author: "bruno", State: "OPEN", HeadOid: "host-head"})
	_, info, err := r.s.target(context.Background(), stepCall{u: r.u})
	if err != nil || info.Head != "plugin-head" {
		t.Fatalf("target %+v %v", info, err)
	}
}

func TestAWorkspaceHoldsThePullRequestThePluginReported(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, nil)
	reportAgainstHostCopy(t, te, dinv.PR{Ref: reportedRef, Author: "ana", State: "MERGED", HeadBranch: "feat/x"},
		&dinv.PR{Ref: reportedRef, Author: "ana", State: "OPEN", HeadBranch: "feat/x"})
	snap := &wsdomain.Snapshot{Repos: []wsdomain.Repo{{ID: "r1", RemoteKey: "github.com/acme/api", Path: "/src/api"}},
		Workspaces: []wsdomain.Workspace{{ID: "dev", RepoID: "r1", Path: "/wt/dev", Branch: "feat/x", HostID: wsdomain.LocalHost, LinkedPR: 7}}}
	held, err := te.Env.workspaceHoldings().Held(context.Background(), snap, te.cfg)
	if err != nil {
		t.Fatal(err)
	}
	h, ok := held.Of(snap.Workspaces[0])
	if !ok || h.State != "MERGED" {
		t.Fatalf("holding %+v %v, want the reported state", h, ok)
	}
}

func TestTheGitHubFactsListThePullRequestsThePluginReported(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, nil)
	reportAgainstHostCopy(t, te, dinv.PR{Ref: reportedRef, Author: "bruno", State: "OPEN", Reasons: []string{"requested"}}, nil)
	mod, err := githubHostModule(te.cfg, moduleDeps{Inventory: te.Store, Reports: te.Env.sourceReports(te.Store), Clock: clockFunc(te.Now)})
	if err != nil {
		t.Fatal(err)
	}
	units, err := mod.Facts.List(context.Background(), unit.GitHub)
	if err != nil || !slices.Contains(units, reportedRef.Unit()) {
		t.Fatalf("listed %v %v, want the reported pull request the host keeps no copy of", units, err)
	}
}
