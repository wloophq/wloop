package daemon

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/web"
)

// Handler is the API and the cockpit without authentication, for the socket.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	for _, rt := range s.routes() {
		mux.Handle(rt.pattern, scopedHandler(meter.Route(rt.pattern), rt.handle))
	}
	mux.HandleFunc("GET /api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "no such endpoint "+r.URL.Path)
	})
	mux.Handle("GET /", web.Handler(s.deps.Version))
	return s.versioned(s.afterReconcile(mux))
}

func scopedHandler(name string, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, scope := meter.Start(r.Context(), name)
		defer scope.End()
		h.ServeHTTP(w, r.WithContext(ctx))
	})
}

type route struct {
	pattern string
	handle  http.HandlerFunc
}

// routes serves every operation of api.Operations, once each.
func (s *Server) routes() []route {
	return s.gatePlugins([]route{
		{"GET /api/health", s.handleHealth},
		{"GET " + api.Rows, s.handleRows},
		{"GET " + api.Catalog, s.handleCatalog},
		{"GET " + api.Resolve, s.handleResolve},
		{"GET " + api.UnitDetail, s.handleDetail},
		{"GET " + api.UnitDetail + "/checks", s.handleChecks},
		{"POST /api/jobs/{name}", s.handleJobs},
		{"POST " + api.UnitDetail + "/{action}", s.handleAction},
		{"GET " + api.Actions, s.handleActions},
		{"GET " + api.Keys, s.handleKeys},
		{"GET /api/events", s.handleEvents},
		{"GET /api/activity", s.handleActivity},
		{"POST /api/explore", s.handleExplore},
		{"GET /api/explore/recent", s.handleExploreRecent},
		{"POST /api/pause", s.handlePause},
		{"POST /api/app", s.handleApp},
		{"POST /api/workspaces/remove-done", s.handleRemoveDone},
		{"GET " + api.Intakes, s.handleIntake},
		{"POST " + api.IntakeWithout, s.handleIntakeWithout},
		{"POST " + api.IntakeCancel, s.handleIntakeCancel},
		{"GET " + api.Plan, s.handlePlan},
		{"POST " + api.Plan, s.handlePlanAct},
		{"GET " + api.Plugins, s.handlePlugins},
		{"GET " + api.PluginAssets, s.handlePluginAsset},
		{"GET " + api.ViewsPath, s.handleViews},
		{"GET " + api.TasksPath, s.handleTasks},
		{"POST " + api.TasksPath, s.handleCapture},
		{"GET " + api.TaskDetail, s.handleTask},
		{"POST " + api.TaskVerb, s.handleTaskVerb},
		{"GET " + api.TimelinePath, owedBy("T1")},
		{"GET " + api.RunsPath, owedBy("U1")},
		{"POST " + api.ChatPath, owedBy("U2")},
		{"POST " + api.AttachPath, owedBy("U1")},
		{"GET " + api.GatesPath, owedBy("G2")},
		{"POST " + api.GatePath, owedBy("G2")},
		{"POST " + api.RunOpen, owedBy("U1")},
		{"POST " + api.RunEnter, owedBy("U1")},
		{"GET " + api.RunTranscript, owedBy("U1")},
		{"GET " + api.CountersPath, s.handleCounters},
		{"GET " + api.Drafts, s.handleDrafts},
		{"POST " + api.Drafts, s.handleDraftCreate},
		{"GET " + api.DraftDetail, s.handleDraft},
		{"POST " + api.DraftActionPath, s.handleDraftAction},
		{"GET " + api.DigestPath, s.handleDigest},
		{"GET " + api.ReviewPath, s.handleReview},
		{"POST " + api.ReviewStepDone, s.handleReviewStepDone},

		{"POST " + api.AgentTurnsStart, s.handleTurnStart},
		{"POST " + api.AgentTurnsByHand, s.handleTurnByHand},
		{"POST " + api.AgentTurnsEnd, s.handleTurnEnd},
		{"POST " + api.AgentSessionsStart, s.handleSessionStart},
		{"POST " + api.AgentSessionsStop, s.handleSessionStop},
		{"POST " + api.AgentSessionsPrompt, s.handleSessionPrompt},
		{"GET " + api.AgentAnswer, s.handleTurnAnswer},

		{"POST " + api.AdminDryRunTurn, s.handleDryRunTurn},
		{"GET " + api.AdminDoctor, s.handleDoctor},
		{"GET " + api.AdminTurn, s.handleTurnShow},
		{"POST " + api.AdminProbe, s.handleProbe},
		{"POST " + api.AdminGitAccess, s.handleGitAccess},
		{"GET " + api.AdminTimings, s.handleTimings},
		{"GET " + api.AdminFacts, s.handleFacts},
		{"GET " + api.AdminMeter, s.handleMeter},
	})
}

// versioned refuses an API request of another major than the daemon's, but
// a read of /api/health, which every version makes. The version comes in the
// header, or, on a read, in the query parameter api for an EventSource.
func (s *Server) versioned(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") || ((r.Method == http.MethodGet || r.Method == http.MethodHead) && r.URL.Path == "/api/health") {
			h.ServeHTTP(w, r)
			return
		}
		v := r.Header.Get(api.Header)
		if v == "" && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
			v = r.URL.Query().Get("api")
		}
		if api.Major(v) != api.Major(api.Version) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUpgradeRequired)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error": fmt.Sprintf("this client speaks API %s and the daemon %s; restart the older one", cmp.Or(v, "0"), api.Version),
				"fix":   s.deps.Restart,
			})
			return
		}
		h.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(append(body, '\n'))
}

// notImplemented answers 501 for a route of the task model's contract that task owes.
func notImplemented(w http.ResponseWriter, task string) {
	writeError(w, http.StatusNotImplemented, "not implemented: "+task)
}

func owedBy(task string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { notImplemented(w, task) }
}

func writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// writeConflict answers 409 with the conflict's message and its refusal code.
func writeConflict(w http.ResponseWriter, c conflict) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusConflict)
	_ = json.NewEncoder(w).Encode(struct {
		Error   string `json:"error"`
		Refusal string `json:"refusal,omitempty"`
	}{c.msg, c.refusal})
}

// writeRefusal answers 409 with the reason, the message of the tool that
// refused and whether force would get past it.
func writeRefusal(w http.ResponseWriter, r Refusal) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusConflict)
	_ = json.NewEncoder(w).Encode(struct {
		Error     string `json:"error"`
		Detail    string `json:"detail,omitempty"`
		Source    string `json:"source,omitempty"`
		Forceable bool   `json:"forceable,omitempty"`
	}{r.Reason, r.Detail, r.Source, r.Forceable})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.noteConfigStale()
	h := api.Health{
		Version:            s.deps.Version,
		Build:              web.Build(s.deps.Version),
		Jobs:               s.jobStatuses(),
		Warnings:           s.warnings.list(),
		Viewer:             s.cachedLogin(),
		StalledReviewHours: time.Duration(s.deps.Cfg.GitHub.Stalled.Review).Hours(),
		WorkspaceManagers:  s.deps.WorkspaceManagers,
		Store:              s.deps.Store,
	}
	if s.deps.PRs != nil {
		h.RateLimit = s.graphQLBudget()
		h.SlowSync = s.rateLimitLow()
	}
	if s.deps.GitAccess != nil {
		var err error
		if h.GitAccess, err = s.deps.GitAccess.Hosts(r.Context()); err != nil {
			s.logf("health: git access: %v", err)
		}
	}
	if s.deps.Pauses != nil {
		p, err := s.deps.Pauses.Pause(r.Context())
		if err != nil {
			s.logf("health: pause: %v", err)
		}
		h.Paused, h.PausedAt = p.On, p.At
	}
	if app := s.app.Get(); app != nil {
		h.App = app
		if w := appWarning(*app); w != "" {
			h.Warnings = append(h.Warnings, w)
		}
	}
	if h.Notifier = s.hub.has(notifierClient); !h.Notifier && s.deps.NotifierStart != "" {
		h.Warnings = append(h.Warnings, "menu bar not running: notifications are off; "+s.deps.NotifierStart)
	}
	if s.deps.BriefStatus != nil {
		st, err := s.deps.BriefStatus.Status(r.Context())
		if err != nil {
			s.logf("health: brief: %v", err)
		} else {
			h.Brief = wireBriefStatus(st)
			if st.LastError != "" {
				h.Warnings = append(h.Warnings, "brief: "+st.LastError)
			}
		}
		open, since, lastErr, next := s.deps.BriefStatus.Breaker()
		h.Briefs = &api.BriefsHealth{Open: open, Since: since, Error: lastErr, Next: next}
		if open {
			h.Warnings = append(h.Warnings, breakerWarning(*h.Briefs, s.deps.Now()))
		}
	}
	if why := s.deps.BriefsUnavailable; why != "" {
		h.Briefs = &api.BriefsHealth{Unavailable: why}
		h.Warnings = append(h.Warnings, "brief: "+why+"; no briefs")
	}
	h.Warnings = append(h.Warnings, s.reconcileWarnings()...)
	h.Warnings = append(h.Warnings, s.homeWarnings()...)
	l := s.loaded()
	if w := profileWarning(l, s.heldProblems(l)); w != "" {
		h.Warnings = append(h.Warnings, w)
	}
	for _, src := range s.sourceHealth(r.Context()) {
		h.Warnings = append(h.Warnings, sourceWarnings(src)...)
	}
	writeJSON(w, h)
}

func (s *Server) handleActivity(w http.ResponseWriter, r *http.Request) {
	limit := activityDefault
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > activityMax {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("limit must be between 1 and %d", activityMax))
			return
		}
		limit = n
	}
	items, err := s.activity(r.Context(), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, items)
}

// handleRemoveDone removes the workspace of every row of the Done view, or
// the Approved one, whose checkout holds nothing that is not on GitHub.
func (s *Server) handleRemoveDone(w http.ResponseWriter, r *http.Request) {
	if s.deps.Actions == nil {
		writeError(w, http.StatusServiceUnavailable, errActionsUnavailable.Error())
		return
	}
	var req struct {
		View string `json:"view"`
	}
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
			return
		}
	}
	view := cmp.Or(req.View, api.ViewDone)
	if view != api.ViewDone && view != api.ViewApproved {
		writeError(w, http.StatusBadRequest, "view is done or approved")
		return
	}
	ctx := context.WithoutCancel(r.Context())
	rows, err := s.rows(ctx, view)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	res := removeDoneResult()
	for _, row := range rows {
		u := row.Unit
		if !isPullRequest(u) || !s.pluginEnabled(u.Provider) {
			continue
		}
		var out bytes.Buffer
		if err := s.act(ctx, "remove", u, api.ActionRequest{}, &out); err != nil {
			kept := api.Kept{Named: named(u), Reason: err.Error()}
			var refused Refusal
			if errors.As(err, &refused) {
				kept.Detail, kept.Source, kept.Forceable = refused.Detail, refused.Source, refused.Forceable
				s.logDetail("remove", u, refused)
			}
			res.Kept = append(res.Kept, kept)
			continue
		}
		res.Removed = append(res.Removed, named(u))
	}
	writeJSON(w, res)
}

func (s *Server) handlePause(w http.ResponseWriter, r *http.Request) {
	if s.deps.Pauses == nil {
		writeError(w, http.StatusServiceUnavailable, "the daemon has no store")
		return
	}
	var req api.PauseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	ctx := context.WithoutCancel(r.Context())
	if err := s.setPause(ctx, req); err != nil {
		writeActionError(w, err)
		return
	}
	writeJSON(w, req)
}

// setPause pauses or resumes what req names. Everything is the pause's On;
// a repository is in its Repos; a pull request is its override's Hold.
// Resuming everything clears all three; resuming less than what a wider
// pause holds is refused.
func (s *Server) setPause(ctx context.Context, req api.PauseRequest) error {
	prs := make([]unit.Unit, 0, len(req.PRs))
	for _, key := range req.PRs {
		u, err := s.deps.Units.Parse(string(s.deps.Forge), key)
		if err != nil || refOf(u).Number <= 0 {
			return badRequest{key + " is not an owner/repo#n"}
		}
		prs = append(prs, u)
	}
	for _, repo := range req.Repos {
		if owner, name, ok := strings.Cut(repo, "/"); !ok || owner == "" || name == "" || strings.ContainsAny(name, "/# ") {
			return badRequest{repo + " is not an owner/repo"}
		}
	}
	s.pauseMu.Lock()
	defer s.pauseMu.Unlock()
	p, err := s.deps.Pauses.Pause(ctx)
	if err != nil {
		return err
	}
	everything := len(req.Repos) == 0 && len(prs) == 0
	if !req.Paused && !everything {
		if p.On {
			return conflict{msg: "everything is paused; resume with no argument resumes everything"}
		}
		for _, u := range prs {
			if ref := refOf(u); p.HoldsRepo(ref.Owner + "/" + ref.Repo) {
				return conflict{msg: ref.Owner + "/" + ref.Repo + " is paused as a whole; resume it to resume " + u.Label()}
			}
		}
	}
	before := p
	switch {
	case everything && req.Paused:
		p.On = true
	case everything:
		p.On, p.Repos = false, nil
	case req.Paused:
		for _, repo := range req.Repos {
			if !p.HoldsRepo(repo) {
				p.Repos = append(p.Repos, repo)
			}
		}
	default:
		p.Repos = slices.DeleteFunc(p.Repos, func(held string) bool {
			return slices.ContainsFunc(req.Repos, func(repo string) bool { return strings.EqualFold(repo, held) })
		})
	}
	if p.On != before.On || !slices.Equal(p.Repos, before.Repos) {
		p.At, p.Why = s.deps.Now(), ""
		if err := s.deps.Pauses.SetPause(ctx, p); err != nil {
			return err
		}
	}
	if everything && !req.Paused {
		if prs, err = s.pausedPRs(ctx); err != nil {
			return err
		}
	}
	action := map[bool]string{true: "pause", false: "resume"}[req.Paused]
	for _, u := range prs {
		if _, err := s.applyOverride(ctx, action, u, api.ActionRequest{}); err != nil {
			return err
		}
	}
	if req.Paused && p.Any() && s.deps.Plan != nil {
		if err := s.dropPaused(ctx, p, "automatic rounds paused"); err != nil {
			s.logf("pause: %v", err)
		}
	}
	return nil
}

// pausedPRs are the pull requests paused one by one.
func (s *Server) pausedPRs(ctx context.Context) ([]unit.Unit, error) {
	if s.deps.Overrides == nil {
		return nil, nil
	}
	all, err := s.deps.Overrides.Overrides(ctx)
	if err != nil {
		return nil, err
	}
	var out []unit.Unit
	for u, o := range all {
		if o.Hold {
			out = append(out, u)
		}
	}
	slices.SortFunc(out, func(a, b unit.Unit) int { return strings.Compare(a.String(), b.String()) })
	return out, nil
}

func (s *Server) handleApp(w http.ResponseWriter, r *http.Request) {
	var st api.AppStatus
	if err := json.NewDecoder(r.Body).Decode(&st); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	if !slices.Contains(appStates, st.State) || !slices.Contains(notificationStates, st.Notifications) {
		writeError(w, http.StatusBadRequest, "state must be running, notifications authorized, provisional, denied, not-determined or empty")
		return
	}
	if s.app.Set(st) {
		s.note(port.TopicApp)
	}
	writeJSON(w, st)
}

// rateLimitLow says the client refuses background reads: the fast sweep
// skips its turns while it holds.
func (s *Server) rateLimitLow() bool {
	return s.deps.Budget != nil && s.deps.Budget.Low()
}

func (s *Server) graphQLBudget() api.RateLimit {
	if s.deps.Budget == nil {
		return api.RateLimit{}
	}
	return s.deps.Budget.RateLimit()
}

func (s *Server) handleRows(w http.ResponseWriter, r *http.Request) {
	since, err := api.ParseSince(r.URL.Query().Get("since"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	page, err := s.readModel().Rows(r.Context(), api.ViewAll, since)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	page.Counters = s.counters(r.Context())
	page.Loading = s.loading()
	writeJSON(w, page)
}

// loading are the plugins still loading, by id.
func (s *Server) loading() []api.Loading {
	if s.deps.Loading == nil {
		return nil
	}
	got := s.deps.Loading()
	var out []api.Loading
	for _, id := range slices.Sorted(maps.Keys(got)) {
		out = append(out, api.Loading{Plugin: id, Reason: got[id]})
	}
	return out
}

// parseUnit is the unit a unit route names, of any provider the daemon
// registered, whose codec validates the key.
func (s *Server) parseUnit(r *http.Request) (unit.Unit, bool) {
	if s.deps.Units == nil {
		return unit.Unit{}, false
	}
	u, err := s.deps.Units.Parse(r.PathValue("provider"), r.PathValue("key"))
	if err != nil {
		return unit.Unit{}, false
	}
	return u, true
}

// parsePR is the pull request a unit route names; the routes that act on a
// pull request take no other unit.
func (s *Server) parsePR(r *http.Request) (unit.Unit, bool) {
	u, ok := s.parseUnit(r)
	return u, ok && isPullRequest(u)
}

// writeNoUnit answers a unit route whose unit the daemon does not serve.
func writeNoUnit(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotFound, fmt.Sprintf("no pull request %s:%s; a unit route takes provider github and a key such as owner/repo#12, path-escaped", r.PathValue("provider"), r.PathValue("key")))
}

func (s *Server) handleDetail(w http.ResponseWriter, r *http.Request) {
	u, ok := s.parseUnit(r)
	if !ok {
		writeNoUnit(w, r)
		return
	}
	ctx := r.Context()
	d, found, err := s.detail(ctx, u)
	if err == nil {
		found, err = s.withCard(ctx, u, &d, found)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, u.Label()+" is not in the inventory")
		return
	}
	writeJSON(w, d)
}

func (s *Server) handleJobs(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !s.RunJob(name) {
		writeError(w, http.StatusNotFound, "no job "+strconv.Quote(name))
		return
	}
	w.WriteHeader(http.StatusAccepted)
}
