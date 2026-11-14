package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/lock"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/core/turns/turnstest"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/profile"
	turnpkg "github.com/wloophq/wloop/internal/turn"
)

// probeLauncher sends the turn it is asked for and names term_probe as its
// session, as the CLI's launcher does through Service.Send and SentTo.
type probeLauncher struct {
	svc      *turns.Service
	launches []turns.Launch
	held     []bool
	err      error
}

func (l *probeLauncher) Launch(ctx context.Context, x turns.Launch) (turns.Outcome, error) {
	l.launches = append(l.launches, x)
	l.held = append(l.held, lock.Held(ctx))
	if l.err != nil {
		return turns.Outcome{}, l.err
	}
	if _, err := turnstest.Send(ctx, l.svc, turns.SendRequest{Unit: x.Unit, Intent: x.Intent, Turn: "probe1round", Trigger: x.Trigger, Records: true}); err != nil {
		return turns.Outcome{}, err
	}
	return turns.Outcome{ID: "probe1round", Submitted: true}, l.svc.SentTo(ctx, x.Unit, "probe1round", "term_probe")
}

// probeStarter starts svc's turns over the bundled intents, counting the
// guidance it takes and the Before phases it runs.
type probeStarter struct {
	fx    *turnstest.Effects
	takes int
}

func (p *probeStarter) Take(context.Context, unit.Unit) (string, error) {
	p.takes++
	return "kept for the next review", nil
}

func (p *probeStarter) Keep(context.Context, unit.Unit, string) error { return nil }

func startsProbes(t *testing.T, s *Server, svc *turns.Service) *probeStarter {
	t.Helper()
	cache := bundledIntents(t)
	s.deps.Intents = cache.Get
	p := &probeStarter{fx: &turnstest.Effects{}}
	svc.Starter = turnstest.Starter(turnstest.Defs{Registry: cache.Get().Registry, Edit: func(d *turns.Def) {
		d.HandsGuidance = slices.Contains(d.Effects, builtin.EffectGuidanceHanded)
	}}, svc.Store, nil)
	svc.Starter.Kept, svc.Starter.Effects = p, p.fx
	return p
}

func TestAProbeStartsThroughTheStarterAndTakesNoGuidance(t *testing.T) {
	t.Parallel()
	s, svc := agentServer(t)
	p := startsProbes(t, s, svc)
	l := &probeLauncher{svc: svc}
	svc.Launcher = l
	if rec := agentCall(t, s.Handler(), "POST", api.AdminProbe, "", `{"workspace_id":"ws-e2e"}`); rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if p.fx.Befores() != 1 || p.takes != 0 || len(l.launches) != 1 {
		t.Fatalf("%d before phases, %d takes, launches %+v; want one person's start that takes nothing", p.fx.Befores(), p.takes, l.launches)
	}
}

func TestProbeAPI(t *testing.T) {
	t.Parallel()
	s, svc := agentServer(t)
	startsProbes(t, s, svc)
	l := &probeLauncher{svc: svc}
	svc.Launcher = l
	rec := agentCall(t, s.Handler(), "POST", api.AdminProbe, "", `{"workspace_id":"ws-e2e","home":"/homes/a","model":"haiku"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var res api.ProbeResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if len(l.launches) != 1 || !l.held[0] {
		t.Fatalf("launches %+v, loop held %v", l.launches, l.held)
	}
	x := l.launches[0]
	if x.Intent != turn.Intent(dintent.Probe) || x.Trigger != turn.Automated || x.WorkspaceID != "ws-e2e" || x.Home != "/homes/a" || x.Model != "haiku" || x.ResumeOf != 0 ||
		refOf(x.Unit).Owner != api.ProbeOwner || refOf(x.Unit).Repo != api.ProbeRepo || refOf(x.Unit).Number != int(agentNow.Unix()) {
		t.Fatalf("launch %+v", x)
	}
	if res.PR != refOf(x.Unit).URL() || res.Turn != "probe1round" || res.Session != "term_probe" {
		t.Fatalf("result %+v", res)
	}

	again := agentCall(t, s.Handler(), "POST", api.AdminProbe, "", `{"workspace_id":"ws-e2e"}`)
	if again.Code != http.StatusOK || len(l.launches) != 2 || refOf(l.launches[1].Unit).Number != refOf(x.Unit).Number+1 {
		t.Fatalf("a second probe in the same second: %d %+v", again.Code, l.launches)
	}
}

func TestProbeAPIRefusesWithoutAWorkspaceAndReportsALaunchFailure(t *testing.T) {
	t.Parallel()
	s, svc := agentServer(t)
	startsProbes(t, s, svc)
	l := &probeLauncher{svc: svc}
	svc.Launcher = l
	if rec := agentCall(t, s.Handler(), "POST", api.AdminProbe, "", `{}`); rec.Code != http.StatusBadRequest || len(l.launches) != 0 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	l.err = errors.New("no workspace ws-gone in Orca")
	if rec := agentCall(t, s.Handler(), "POST", api.AdminProbe, "", `{"workspace_id":"ws-gone"}`); rec.Code != http.StatusInternalServerError || len(l.launches) != 1 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

// bundledIntents are the bundled profile's intents.
func bundledIntents(t *testing.T) *intentload.Cache {
	t.Helper()
	c, err := turnpkg.NewCache(func() []profile.Layer { return nil }, intentload.CacheOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return c
}
