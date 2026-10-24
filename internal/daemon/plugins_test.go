package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestWritePluginDisabled(t *testing.T) {
	rec := httptest.NewRecorder()
	writePluginDisabled(rec, "slack")
	if rec.Code != http.StatusConflict || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("%d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["error"] != "slack is disabled" || body["plugin"] != "slack" || len(body) != 2 {
		t.Errorf("%v", body)
	}
}

func TestRefusePlugin(t *testing.T) {
	gh := unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}
	sl := unit.Unit{Provider: "slack", Key: "T1/C1/1.2"}
	all := &Server{}
	rec := httptest.NewRecorder()
	if all.refusePlugin(rec, gh) || rec.Code != http.StatusOK || rec.Body.Len() != 0 {
		t.Errorf("a nil PluginEnabled refused: %d %s", rec.Code, rec.Body)
	}
	s := &Server{deps: Deps{PluginEnabled: func(p unit.ProviderID) bool { return p != "slack" }}}
	rec = httptest.NewRecorder()
	if s.refusePlugin(rec, gh) || rec.Body.Len() != 0 {
		t.Errorf("an enabled plugin refused: %s", rec.Body)
	}
	rec = httptest.NewRecorder()
	if !s.refusePlugin(rec, sl) || rec.Code != http.StatusConflict {
		t.Errorf("a disabled plugin passed: %d", rec.Code)
	}
}

// streamClock records each wait and fires it when the test says.
type streamClock struct {
	waits chan time.Duration
	fire  chan time.Time
}

func (c streamClock) Now() time.Time { return agentNow }

func (c streamClock) After(d time.Duration) <-chan time.Time {
	c.waits <- d
	return c.fire
}

func TestPluginRunRestarts(t *testing.T) {
	runs := make(chan struct{}, 4)
	var logged atomic.Value
	s := New(Deps{Log: func(f string, args ...any) { logged.Store(fmt.Sprintf(f, args...)) }})
	clock := streamClock{waits: make(chan time.Duration, 4), fire: make(chan time.Time)}
	s.streamClock = clock
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.runStream(ctx, PluginRuntime{ID: "thread", Run: func(context.Context) error {
			runs <- struct{}{}
			return errors.New("socket closed")
		}})
		close(done)
	}()
	<-runs
	if d := <-clock.waits; d != 30*time.Second {
		t.Fatalf("waits %v before a restart", d)
	}
	if got, _ := logged.Load().(string); !strings.Contains(got, "thread") || !strings.Contains(got, "socket closed") {
		t.Fatalf("logged %q", got)
	}
	select {
	case <-runs:
		t.Fatal("restarted before 30 s")
	default:
	}
	clock.fire <- agentNow
	<-runs
	<-clock.waits
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the stream did not stop with its context")
	}
}

func TestPluginJobsRegistered(t *testing.T) {
	var n atomic.Int32
	s := newServerWith(t, func(d *Deps) {
		d.Runtimes = []PluginRuntime{{ID: "thread", Jobs: []PluginJob{{Name: "extract:thread", Every: 20 * time.Millisecond, Run: func(context.Context) (string, string, error) {
			n.Add(1)
			return "extracted", "", nil
		}}}}}
	})
	var h api.Health
	if code := get(t, s, "/api/health", &h); code != http.StatusOK {
		t.Fatalf("health %d", code)
	}
	i := slices.IndexFunc(h.Jobs, func(j api.JobStatus) bool { return j.Name == "extract:thread" })
	if i < 0 || h.Jobs[i].Every != 20*time.Millisecond {
		t.Fatalf("jobs %+v", h.Jobs)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.jobs["extract:thread"].loop(ctx, s.logf, s.jobDone)
	waitFor(t, func() bool { return n.Load() >= 3 })
}

func TestAPluginHealthWarningReachesTheWarnings(t *testing.T) {
	s := newServerWith(t, func(d *Deps) {
		d.Runtimes = []PluginRuntime{
			{ID: "slack", Health: func(context.Context) (api.SourceHealth, error) {
				return api.SourceHealth{Warnings: []string{"slack: not logged in", "socket reconnecting"}}, nil
			}},
			{ID: "broken", Health: func(context.Context) (api.SourceHealth, error) { return api.SourceHealth{}, errors.New("no token") }},
			{ID: "quiet"},
		}
	})
	var h api.Health
	if code := get(t, s, "/api/health", &h); code != http.StatusOK {
		t.Fatalf("health %d", code)
	}
	for _, want := range []string{"slack: not logged in", "slack: socket reconnecting", "broken: health: no token"} {
		if !slices.Contains(h.Warnings, want) {
			t.Errorf("warnings %q lack %q", h.Warnings, want)
		}
	}
}

func TestPluginsServed(t *testing.T) {
	plugins := []api.Plugin{{ID: unit.GitHub, Enabled: false}, {ID: "thread", Enabled: true, UI: &api.PluginUI{TUIViews: []string{"t"}}}}
	s := newServerWith(t, func(d *Deps) { d.Plugins = plugins })
	var got []api.Plugin
	if code := get(t, s, api.Plugins, &got); code != http.StatusOK || !reflect.DeepEqual(got, plugins) {
		t.Fatalf("%d %+v", code, got)
	}
	s = newServer(t)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, apiRequest("GET", api.Plugins, nil))
	if strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("no plugin: %s", rec.Body)
	}
}

func TestPluginJobTimeout(t *testing.T) {
	run := func(context.Context) (string, string, error) { return "", "", nil }
	s := newServerWith(t, func(d *Deps) {
		d.Runtimes = []PluginRuntime{{ID: "thread", Jobs: []PluginJob{
			{Name: "long", Every: time.Minute, Timeout: 25 * time.Minute, Run: run},
			{Name: "plain", Every: time.Minute, Run: run},
		}}}
	})
	if got := s.jobs["long"].timeout; got != 25*time.Minute {
		t.Fatalf("long job timeout %v", got)
	}
	if got := s.jobs["plain"].timeout; got != pluginTimeout {
		t.Fatalf("plain job timeout %v", got)
	}
}

func TestNegativePluginJobTimeoutIsTheDefault(t *testing.T) {
	s := newServerWith(t, func(d *Deps) {
		d.Runtimes = []PluginRuntime{{ID: "thread", Jobs: []PluginJob{{Name: "neg", Every: time.Minute, Timeout: -time.Second,
			Run: func(context.Context) (string, string, error) { return "", "", nil }}}}}
	})
	if got := s.jobs["neg"].timeout; got != pluginTimeout {
		t.Fatalf("timeout %v", got)
	}
}
