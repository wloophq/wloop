package daemon

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// PluginRuntime is what an enabled plugin runs in the daemon; a nil field is
// absent.
type PluginRuntime struct {
	ID unit.ProviderID
	// Run is a long-running stream, run until the daemon stops and started
	// again streamRestart after it returns.
	Run    func(ctx context.Context) error
	Jobs   []PluginJob
	Health func(ctx context.Context) (api.SourceHealth, error)
	// WebAssets are served under /plugins/<id>/.
	WebAssets fs.FS
}

// PluginJob is a plugin's daemon job, named as the plugin names it.
type PluginJob struct {
	Name    string
	Every   time.Duration
	Timeout time.Duration // a run's limit; 0 is pluginTimeout
	Run     func(ctx context.Context) (summary, skipped string, err error)
}

const (
	streamRestart = 30 * time.Second
	pluginTimeout = 10 * time.Minute
	healthTimeout = 5 * time.Second
)

// writePluginDisabled answers 409 {"error": "<id> is disabled", "plugin": "<id>"}.
func writePluginDisabled(w http.ResponseWriter, p unit.ProviderID) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusConflict)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": string(p) + " is disabled", "plugin": string(p)})
}

func (s *Server) pluginEnabled(p unit.ProviderID) bool {
	return s.deps.PluginEnabled == nil || s.deps.PluginEnabled(p)
}

// refusePlugin writes the 409 and reports true when u's plugin is disabled.
func (s *Server) refusePlugin(w http.ResponseWriter, u unit.Unit) bool {
	if s.pluginEnabled(u.Provider) {
		return false
	}
	writePluginDisabled(w, u.Provider)
	return true
}

// refuseUnitOf is refusePlugin on the unit of returns, when it tells one;
// of is called only when a plugin may be disabled.
func (s *Server) refuseUnitOf(w http.ResponseWriter, of func() (unit.Unit, bool)) bool {
	if s.deps.PluginEnabled == nil {
		return false
	}
	u, ok := of()
	return ok && s.refusePlugin(w, u)
}

// gatePlugins wraps each mutating route whose pattern names {provider} so a
// disabled provider answers 409 before the handler.
func (s *Server) gatePlugins(rts []route) []route {
	for i, rt := range rts {
		method, path, _ := strings.Cut(rt.pattern, " ")
		if method == http.MethodGet || method == http.MethodHead || !strings.Contains(path, "{provider}") {
			continue
		}
		handle := rt.handle
		rts[i].handle = func(w http.ResponseWriter, r *http.Request) {
			if p := unit.ProviderID(r.PathValue("provider")); !s.pluginEnabled(p) {
				writePluginDisabled(w, p)
				return
			}
			handle(w, r)
		}
	}
	return rts
}

func (s *Server) handlePlugins(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, nonNil(s.deps.Plugins))
}

func nonNil[T any](v []T) []T {
	if v == nil {
		return []T{}
	}
	return v
}

// bookUnit is the unit of the book k finds.
func (s *Server) bookUnit(ctx context.Context, k turns.Key) (unit.Unit, bool) {
	if s.deps.Turns == nil {
		return unit.Unit{}, false
	}
	b, ok, err := s.deps.Turns.Store.BookOf(ctx, k)
	return b.Unit, ok && err == nil
}

// byHandUnit is the unit a turn by hand names: its address's, else the
// session's.
func (s *Server) byHandUnit(ctx context.Context, req api.ByHandRequest, handle string) (unit.Unit, bool) {
	if req.URL != "" {
		if s.deps.Units == nil {
			return unit.Unit{}, false
		}
		u, err := unitOfAddress(s.deps.Units, req.URL)
		return u, err == nil
	}
	if s.deps.ByHandRef == nil {
		return unit.Unit{}, false
	}
	u, err := s.deps.ByHandRef(ctx, req, handle)
	return u, err == nil
}

// planUnit is the unit of the first plan item of ids whose plugin is
// disabled.
func (s *Server) planUnit(ctx context.Context, p Plan, ids []string) (unit.Unit, bool) {
	for _, id := range ids {
		items, err := p.Items(ctx, automation.Filter{ID: id})
		if err != nil {
			continue
		}
		for _, it := range items {
			if !s.pluginEnabled(it.Unit.Provider) {
				return it.Unit, true
			}
		}
	}
	return unit.Unit{}, false
}

// registerPluginJobs adds the enabled plugins' jobs; a name another job
// holds is logged and left out.
func (s *Server) registerPluginJobs() {
	for _, rt := range s.deps.Runtimes {
		for _, pj := range rt.Jobs {
			if _, taken := s.jobs[pj.Name]; taken || pj.Run == nil || pj.Every <= 0 {
				s.logf("%s: job %q left out: its name is taken, or it has no run or interval", rt.ID, pj.Name)
				continue
			}
			timeout := pj.Timeout
			if timeout <= 0 {
				timeout = pluginTimeout
			}
			s.jobs[pj.Name] = newJob(pj.Name, pj.Every, timeout, pj.Run)
		}
	}
}

// startStreams runs each enabled plugin's stream on wg, once the start-up
// reconcile is done, until ctx ends.
func (s *Server) startStreams(ctx context.Context, wg *sync.WaitGroup) {
	for _, rt := range s.deps.Runtimes {
		if rt.Run != nil {
			wg.Go(func() {
				select {
				case <-s.reconcile.done:
					s.runStream(ctx, rt)
				case <-ctx.Done():
				}
			})
		}
	}
}

// runStream runs rt's stream until ctx ends, again streamRestart after
// each return.
func (s *Server) runStream(ctx context.Context, rt PluginRuntime) {
	for {
		err := safeStream(ctx, rt.Run)
		if ctx.Err() != nil {
			return
		}
		s.logf("%s: stream ended: %v; restarting in %v", rt.ID, err, streamRestart)
		select {
		case <-ctx.Done():
			return
		case <-s.streamClock.After(streamRestart):
		}
	}
}

func safeStream(ctx context.Context, run func(context.Context) error) error {
	_, _, err := safeRun(ctx, func(ctx context.Context) (string, string, error) { return "", "", run(ctx) })
	return err
}

// sourceWarnings are h's warnings, each led by its plugin's id unless it
// names it already.
func sourceWarnings(h api.SourceHealth) []string {
	out := make([]string, 0, len(h.Warnings))
	for _, w := range h.Warnings {
		if !strings.HasPrefix(w, string(h.Provider)+":") {
			w = string(h.Provider) + ": " + w
		}
		out = append(out, w)
	}
	return out
}

// sourceHealth is the health of every enabled plugin that reports one, in
// the plugins' order; never nil.
func (s *Server) sourceHealth(ctx context.Context) []api.SourceHealth {
	out := []api.SourceHealth{}
	for _, rt := range s.deps.Runtimes {
		if rt.Health == nil {
			continue
		}
		hctx, cancel := context.WithTimeout(ctx, healthTimeout)
		h, err := rt.Health(hctx)
		cancel()
		if err != nil {
			h = api.SourceHealth{Warnings: []string{"health: " + err.Error()}}
		}
		h.Provider = rt.ID
		h.Warnings = nonNil(h.Warnings)
		out = append(out, h)
	}
	return out
}
