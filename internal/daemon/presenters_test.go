package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/source/github/present"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/port"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/readmodel"
	"github.com/wloophq/wloop/pkg/plugin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/pkg/plugin/source/sourcetest"
)

// presenterStub is a source plugin whose units offer close and refuse, and
// whose view threads answers its query back.
type presenterStub struct {
	*sourcetest.Stub
	mu      sync.Mutex
	actions []src.ActionRequest
	views   []src.ViewRequest
}

func (p *presenterStub) Action(_ context.Context, req src.ActionRequest) (json.RawMessage, error) {
	p.mu.Lock()
	p.actions = append(p.actions, req)
	p.mu.Unlock()
	switch req.Action {
	case "close":
		return json.Marshal(api.PluginActionResult{Output: []string{"closed " + req.Unit.Key}})
	case "refuse":
		return nil, fmt.Errorf("%w: %s has nothing to refuse", src.ErrUnfit, req.Unit.Key)
	}
	return nil, src.ErrUnsupported
}

func (p *presenterStub) View(_ context.Context, req src.ViewRequest) (json.RawMessage, error) {
	p.mu.Lock()
	p.views = append(p.views, req)
	p.mu.Unlock()
	if req.View != "threads" {
		return nil, fmt.Errorf("%w: no view %s", src.ErrUnsupported, req.View)
	}
	return json.Marshal(map[string]any{"view": req.View, "query": req.Query})
}

func (p *presenterStub) Identity(context.Context) (src.Identity, error) { return src.Identity{}, nil }

func (p *presenterStub) seen() ([]src.ActionRequest, []src.ViewRequest) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.actions), slices.Clone(p.views)
}

// snapshotReports are a plugin's last reports.
type snapshotReports map[unit.Unit]src.Snapshot

func (r snapshotReports) Units(_ context.Context, p unit.ProviderID) ([]unit.Unit, error) {
	var out []unit.Unit
	for _, u := range slices.SortedFunc(maps.Keys(r), api.CompareUnits) {
		if u.Provider == p {
			out = append(out, u)
		}
	}
	return out, nil
}

func (r snapshotReports) Reported(_ context.Context, units []unit.Unit) (map[unit.Unit]src.Snapshot, error) {
	out := map[unit.Unit]src.Snapshot{}
	for _, u := range units {
		if s, ok := r[u]; ok {
			out[u] = s
		}
	}
	return out, nil
}

var acmeThread = unit.Unit{Provider: "acme", Key: "t1"}

// pluginServer is a daemon over the reported unit acme:t1 of the running
// plugin stub, reached over a real go-plugin connection; enabled says
// whether the plugin is.
func pluginServer(t *testing.T, stub *presenterStub, enabled bool) *Server {
	t.Helper()
	at := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	reports := snapshotReports{acmeThread: {Unit: plugin.Unit{Provider: "acme", Key: "t1"}, Rev: 1, At: at,
		Situation: &src.Situation{Kind: "waiting", WaitingOn: "me", Since: at}, Card: &src.Card{Title: "Ana asks for the export"},
		Offers: []src.Offer{{Action: "close"}, {Action: "refuse"}}}}
	client := sourcetest.Launch(t, "acme", stub, src.Host{})
	d := Deps{Units: unit.NewRegistry(unit.OpaqueCodec{ID: "acme"}), Cfg: config.Defaults(), Version: "test", Now: func() time.Time { return at.Add(time.Hour) },
		Log: t.Logf,
		RowProviders: []func(readmodel.RowDeps) readmodel.Provider{func(rd readmodel.RowDeps) readmodel.Provider {
			return readmodel.NewReported(readmodel.ReportedSource{RowDeps: rd, ID: "acme", Reports: reports})
		}},
		PluginPresenter: func(p unit.ProviderID) (Presenter, bool) { return client, p == "acme" },
		PluginEnabled:   func(p unit.ProviderID) bool { return p != "acme" || enabled },
	}
	s := New(d)
	s.token = ""
	stopOnCleanup(t, s)
	return s
}

func TestAPluginActionRunsThroughTheProtocol(t *testing.T) {
	t.Parallel()
	stub := &presenterStub{Stub: sourcetest.NewStub()}
	s := pluginServer(t, stub, true)
	rec := post(t, s, api.UnitPath(acmeThread, "close"), `{"guidance":"done here"}`)
	var res api.ActionResult
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &res) != nil || !slices.Equal(res.Output, []string{"closed t1"}) {
		t.Fatalf("close: %d %s", rec.Code, rec.Body)
	}
	actions, _ := stub.seen()
	if len(actions) != 1 || actions[0].Action != "close" || actions[0].Unit != (plugin.Unit{Provider: "acme", Key: "t1"}) {
		t.Fatalf("the plugin was asked %+v", actions)
	}
	var args api.PluginActionArgs
	if err := json.Unmarshal(actions[0].Args, &args); err != nil || args.Request.Guidance != "done here" || args.Unmuted == nil || args.Unmuted.Unit != acmeThread {
		t.Fatalf("args %s: %v", actions[0].Args, err)
	}
	if rec := post(t, s, api.UnitPath(acmeThread, "refuse"), `{}`); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "t1 has nothing to refuse") ||
		strings.Contains(rec.Body.String(), src.ErrUnfit.Error()) {
		t.Errorf("a refusal: %d %s", rec.Code, rec.Body)
	}
	if rec := post(t, s, api.UnitPath(acmeThread, "reopen"), `{}`); rec.Code != http.StatusNotFound {
		t.Errorf("an action the row does not offer: %d %s", rec.Code, rec.Body)
	}
	if actions, _ := stub.seen(); len(actions) != 2 {
		t.Errorf("the plugin was asked %d actions, want close and refuse", len(actions))
	}
}

func TestAPluginViewIsServedUnderItsId(t *testing.T) {
	t.Parallel()
	stub := &presenterStub{Stub: sourcetest.NewStub()}
	s := pluginServer(t, stub, true)
	var got struct {
		View  string            `json:"view"`
		Query map[string]string `json:"query"`
	}
	if code := get(t, s, api.PluginViewPath("acme", "threads")+"?channel=c1", &got); code != http.StatusOK || got.View != "threads" || got.Query["channel"] != "c1" {
		t.Fatalf("GET: %d %+v", code, got)
	}
	rec := post(t, s, api.PluginViewPath("acme", "threads"), `{"channel":"c2"}`)
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &got) != nil || got.Query["channel"] != "c2" {
		t.Fatalf("POST: %d %s", rec.Code, rec.Body)
	}
	if code := get(t, s, api.PluginViewPath("acme", "nothing"), nil); code != http.StatusNotFound {
		t.Errorf("a view the plugin lacks: %d", code)
	}
	if code := get(t, s, api.PluginViewPath("other", "threads"), nil); code != http.StatusNotFound {
		t.Errorf("a plugin that runs nowhere: %d", code)
	}
	if _, views := stub.seen(); len(views) != 3 {
		t.Errorf("the plugin served %d views, want 3", len(views))
	}
	off := pluginServer(t, &presenterStub{Stub: sourcetest.NewStub()}, false)
	if code := get(t, off, api.PluginViewPath("acme", "threads"), nil); code != http.StatusConflict {
		t.Errorf("a disabled plugin's view: %d", code)
	}
}

// overrideSpy is the broker's Gates and Reader, recording each SetOverride.
type overrideSpy struct {
	src.Gates
	src.Reader
	mu   sync.Mutex
	sets []map[string][2]string
}

func (o *overrideSpy) SetOverride(ctx context.Context, u plugin.Unit, set, ifMatch map[string]string) (bool, error) {
	o.mu.Lock()
	rec := map[string][2]string{}
	for n, v := range set {
		rec[n] = [2]string{v, ifMatch[n]}
	}
	o.sets = append(o.sets, rec)
	o.mu.Unlock()
	return o.Gates.SetOverride(ctx, u, set, ifMatch)
}

func (o *overrideSpy) written() []map[string][2]string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return slices.Clone(o.sets)
}

// actionSpy is a presenter recording the actions the daemon asks of it.
type actionSpy struct {
	Presenter
	mu      sync.Mutex
	actions []string
}

func (a *actionSpy) Action(ctx context.Context, req src.ActionRequest) (json.RawMessage, error) {
	a.mu.Lock()
	a.actions = append(a.actions, req.Action)
	a.mu.Unlock()
	return a.Presenter.Action(ctx, req)
}

func (a *actionSpy) asked() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.actions)
}

func TestMuteGoesThroughThePlugin(t *testing.T) {
	t.Parallel()
	spy := &overrideSpy{}
	presenter := &actionSpy{}
	s := newServerWith(t, func(d *Deps) {
		g := githubOf(d)
		build := d.Presenters[unit.GitHub]
		d.Presenters[unit.GitHub] = func(h PresenterHost) Presenter {
			build(h)
			spy.Gates, spy.Reader = g.Host.Gates, g.Host.Reader
			gd := *g
			gd.Host.Gates, gd.Host.Reader = spy, spy
			presenter.Presenter = present.New(gd, present.Host(h))
			return presenter
		}
	})
	ctx := context.Background()
	if rec := post(t, s, "/api/units/github/acme%2Fapi%237/mute", `{"until":"2d"}`); rec.Code != http.StatusOK {
		t.Fatalf("mute: %d %s", rec.Code, rec.Body)
	}
	until := s.deps.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339Nano)
	sets := spy.written()
	if !slices.Equal(presenter.asked(), []string{present.ActionMute}) || len(sets) != 1 || sets[0][port.OverrideMutedUntil] != [2]string{until, ""} {
		t.Fatalf("asked %v, set %v", presenter.asked(), sets)
	}
	if o, _ := s.store().Override(ctx, r7); !o.MutedUntil.Equal(s.deps.Now().Add(48 * time.Hour)) {
		t.Fatalf("the override %+v", o)
	}
	if rec := post(t, s, "/api/units/github/acme%2Fapi%237/unmute", `{}`); rec.Code != http.StatusOK {
		t.Fatalf("unmute: %d %s", rec.Code, rec.Body)
	}
	if sets = spy.written(); len(sets) != 2 || sets[1][port.OverrideMutedUntil] != [2]string{"", until} {
		t.Fatalf("the unmute set %v, want muted_until cleared while it held %s", sets, until)
	}
	if rec := post(t, s, "/api/units/github/acme%2Fapi%237/mute", `{"until_state":true}`); rec.Code != http.StatusOK {
		t.Fatalf("mute until state: %d %s", rec.Code, rec.Body)
	}
	o, _ := s.store().Override(ctx, r7)
	if !o.MuteUntilStateChange || o.MutedKind == "" {
		t.Fatalf("the mute until state change %+v", o)
	}
	if n := countEvents(t, s, dinv.KindMuted); n != 2 {
		t.Errorf("%d muted events, want 2", n)
	}
}
