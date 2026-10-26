package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/turns"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/turn"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/profile"
	turnpkg "github.com/wloophq/wloop/internal/turn"
)

// probeLauncher sends the turn it is asked for and names term_probe as its
// session, as the CLI's launcher does through Service.Send and SentTo.
type probeLauncher struct {
	svc      *turns.Service
	launches []turns.Launch
	err      error
}

func (l *probeLauncher) Launch(ctx context.Context, x turns.Launch) error {
	l.launches = append(l.launches, x)
	if l.err != nil {
		return l.err
	}
	if _, err := l.svc.Send(ctx, turns.SendRequest{Unit: x.Unit, Intent: x.Intent, Turn: "probe1round", Trigger: x.Trigger, Records: true}); err != nil {
		return err
	}
	return l.svc.SentTo(ctx, x.Unit, "probe1round", "term_probe")
}

func TestProbeAPI(t *testing.T) {
	t.Parallel()
	s, svc := agentServer(t)
	s.deps.Intents = bundledIntents(t).Get
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
	if len(l.launches) != 1 {
		t.Fatalf("launches %+v", l.launches)
	}
	x := l.launches[0]
	if x.Intent != turn.Intent(dintent.Probe) || x.Trigger != turn.Automated || x.WorkspaceID != "ws-e2e" || x.Home != "/homes/a" || x.Model != "haiku" || x.ResumeOf != 0 ||
		refOf(x.Unit).Owner != ProbeOwner || refOf(x.Unit).Repo != ProbeRepo || refOf(x.Unit).Number != int(agentNow.Unix()) {
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
	s.deps.Intents = bundledIntents(t).Get
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
