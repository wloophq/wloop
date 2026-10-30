package daemon

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/orca"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/ingest"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/core/turns/turnstest"
	coreviews "github.com/wloophq/wloop/internal/core/views"
	"github.com/wloophq/wloop/internal/core/views/viewstest"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/meter/metertest"
	"github.com/wloophq/wloop/internal/workspace"
	"github.com/wloophq/wloop/internal/workspace/orca/orcatest"
)

// listingJob is an ingestion job that lists the workspaces once, as the
// host lists them when the GitHub plugin's fast pass asks.
func listingJob(set *workspace.Set) ingest.Job {
	return ingest.Job{Every: 2 * time.Minute, Prepare: func(ctx context.Context, _ time.Time) (ingest.Plan, string, error) {
		set.Invalidate(wsdomain.PartMutable)
		if _, err := set.Repos(ctx); err != nil {
			return ingest.Plan{}, "", err
		}
		if _, _, err := set.ListAll(ctx); err != nil {
			return ingest.Plan{}, "", err
		}
		_, err := set.AgentActivity(ctx)
		return ingest.Plan{}, "listed", err
	}}
}

var liveColumns = orca.Columns{ReviewStatus: "reviewing", NeedsMeStatus: "needs-me", ReviewedStatus: "reviewed", InProgressStatus: "in-progress", CompletedStatus: "completed"}

// liveSessions registers 159 sessions over 82 workspaces of orcatest.Live, the first
// 31 on its listed terminals.
func liveSessions(reg *turnstest.Registry) {
	for k := range 159 {
		h := fmt.Sprintf("gone-%d", k)
		if k < 31 {
			h = orcatest.Handle(k)
		}
		reg.Put(turn.Session{ID: h, Manager: "orca", Handle: h, WorkspaceID: orcatest.WorktreeID(k % 82), Managed: true})
	}
}

func rpc(method string) metertest.Limit {
	return metertest.Limit{Res: meter.Workspace, Op: "rpc " + method}
}

func TestFastCycleOrcaCalls(t *testing.T) {
	t.Parallel()
	rt := orcatest.Start(t, orcatest.Live())
	m := rt.Manager()
	ws := workspace.NewCached(m, m, 2*time.Minute)
	a, err := orca.Ports(ws, liveColumns, func(context.Context) (map[unit.Unit]orca.Link, error) { return nil, nil })
	if err != nil {
		t.Fatal(err)
	}
	log := change.NewLog(change.DefaultWindow, nil)
	a.WatchAgents(context.Background(), func(units []unit.Unit) { log.Note(port.TopicAgents, units...) })
	reg := turnstest.NewRegistry()
	liveSessions(reg)
	d := testDeps(t)
	d.Collector, d.Ingest = failingSlow{}, []IngestJob{{ID: unit.GitHub, Job: listingJob(workspace.NewSet(ws))}}
	d.Turns = &turns.Service{Intents: turnstest.Intents(), Store: turnstest.NewStore(), Registry: reg, Sessions: turns.NewRouter(reg, a),
		Clock: fixedClock(time.Now()), FastJob: 2 * time.Minute}
	d.Views = &coreviews.Service{Books: turnstest.NewStore(), Intents: turnstest.Intents(), State: viewstest.NewState(),
		Sinks: []coreviews.Sink{{ViewSink: a, Placements: a}}}
	s := New(d)
	cycle := func(ctx context.Context) {
		s.jobs[ingestJobName(unit.GitHub)].once(ctx, t.Logf, func(string, string) {})
		s.jobs["fast"].once(ctx, t.Logf, func(string, string) {})
	}
	cycle(context.Background())

	gen := log.Gen()
	ctx, sc := metertest.Start(t, context.Background())
	cycle(ctx)
	for method, want := range map[string]int64{"worktree.list": 1, "worktree.ps": 1, "terminal.list": 0, "status.get": 0} {
		if n := metertest.Count(sc, rpc(method)); n != want {
			t.Errorf("rpc %s: %d calls, want %d", method, n, want)
		}
	}
	metertest.Within(t, sc, metertest.Budget{
		{Res: meter.Workspace}:                          2,
		{Res: meter.Workspace, Field: metertest.Spawns}: 0,
	})
	if c := log.Since(gen, port.TopicAgents); !c.Empty() {
		t.Errorf("an idle fast cycle noted the agents: %+v", c)
	}
	if t.Failed() {
		t.Log(metertest.Report(sc.Tally()))
	}
}

func TestStartupReconcileOrcaCalls(t *testing.T) {
	t.Parallel()
	rt := orcatest.Start(t, orcatest.Live())
	m := rt.Manager()
	a, err := orca.Ports(workspace.NewCached(m, m, 2*time.Minute), liveColumns, nil)
	if err != nil {
		t.Fatal(err)
	}
	reg := turnstest.NewRegistry()
	liveSessions(reg)
	svc := &turns.Service{Intents: turnstest.Intents(), Store: turnstest.NewStore(), Registry: reg, Sessions: turns.NewRouter(reg, a), Clock: fixedClock(time.Now()), FastJob: 2 * time.Minute}
	ctx, sc := metertest.Start(t, context.Background())
	rep, err := svc.Reconcile(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.GoneSessions) != 128 {
		t.Errorf("%d gone sessions, want 128", len(rep.GoneSessions))
	}
	metertest.Within(t, sc, metertest.Budget{
		{Res: meter.Workspace}:                          3,
		{Res: meter.Workspace, Field: metertest.Spawns}: 0,
	})
}

func TestHookReloadsPanesAndNotesAgents(t *testing.T) {
	world := orcatest.Live()
	world.Worktrees[0].Agents[0].State = "working"
	rt := orcatest.Start(t, world)
	m := rt.Manager()
	ws := workspace.NewCached(m, m, 2*time.Minute)
	ref := pr.Ref{Owner: "acme", Repo: "repo-0", Number: 1}
	a, err := orca.Ports(ws, liveColumns, func(context.Context) (map[unit.Unit]orca.Link, error) {
		return map[unit.Unit]orca.Link{ref.Unit(): {WorkspaceID: orcatest.WorktreeID(0), Review: true}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	log := change.NewLog(change.DefaultWindow, nil)
	a.WatchAgents(context.Background(), func(units []unit.Unit) { log.Note(port.TopicAgents, units...) })
	s, svc := agentServer(t)
	svc.Sessions = turns.NewRouter(svc.Registry, a)
	if _, err := a.Agents(context.Background()); err != nil {
		t.Fatal(err)
	}

	ctx, sc := metertest.Start(t, context.Background())
	ws.Cache.SetBase(ctx)
	gen := log.Gen()
	rt.Update(func(w *orcatest.World) { w.Worktrees[0].Agents[0].State = "done" })
	if rec := agentCall(t, s.Handler(), "POST", api.AgentSessionsStop, orcatest.Handle(0), `{"agent_session":"s-0"}`); rec.Code != http.StatusOK {
		t.Fatalf("stop: %d %s", rec.Code, rec.Body)
	}
	waitFor(t, func() bool { return slices.Contains(log.Since(gen, port.TopicAgents).Units, ref.Unit()) })
	agents, err := a.Agents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if i := slices.IndexFunc(agents, func(x turns.AgentObservation) bool { return x.WorkspaceID == orcatest.WorktreeID(0) }); i < 0 || agents[i].State != turn.AgentDone {
		t.Fatalf("agents after the stop: %+v", agents)
	}
	metertest.Within(t, sc, metertest.Budget{
		rpc("worktree.ps"):                              1,
		rpc("terminal.list"):                            1,
		{Res: meter.Workspace, Field: metertest.Spawns}: 0,
	})
	if n := metertest.Count(sc, rpc("worktree.ps")); n != 1 {
		t.Errorf("the stop reloaded worktree.ps %d times, want 1", n)
	}
}
