package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/meter"
)

// ReconcileWait is how long UserAPI waits for the start-up reconcile before
// it serves anyway.
const ReconcileWait = 30 * time.Second

const reconcileRunning = "reconcile still running"

type reconcileState struct {
	// ready closes when the reconcile ends or wait passes; done closes
	// only when it ends, and the jobs wait on it.
	ready, done chan struct{}
	wait        time.Duration

	mu     sync.Mutex
	report *api.ReconcileReport
	err    error
	late   bool
}

func (r *reconcileState) result() (rep *api.ReconcileReport, late bool, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.report, r.late, r.err
}

// runStartup runs the start-up reconcile and opens UserAPI when it ends, or
// when it runs past its wait.
func (s *Server) runStartup(ctx context.Context) {
	r := &s.reconcile
	done := r.done
	go func() {
		defer close(done)
		var rep api.ReconcileReport
		var err error
		phase(ctx, "startup", func(ctx context.Context) { rep, err = s.deps.Startup(ctx) })
		r.mu.Lock()
		r.report, r.err, r.late = &rep, err, false
		r.mu.Unlock()
		if err != nil {
			s.logf("reconcile: %v", err)
		}
		s.logf("reconcile: %d unrecorded, %d closed, %d sessions gone, %d view items rewritten",
			len(rep.Unrecorded), len(rep.Closed), len(rep.GoneSessions), rep.Rewritten)
		s.note(port.TopicJobs)
	}()
	select {
	case <-done:
	case <-time.After(r.wait):
		r.mu.Lock()
		r.late = true
		r.mu.Unlock()
		s.logf("%s after %v; serving", reconcileRunning, r.wait)
	case <-ctx.Done():
	}
	close(r.ready)
	<-done
}

// afterReconcile holds UserAPI and AdminAPI until the start-up reconcile
// opens them; health and AgentAPI answer at once.
func (s *Server) afterReconcile(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if strings.HasPrefix(p, "/api/") && p != "/api/health" && !strings.HasPrefix(p, api.AgentPrefix) {
			select {
			case <-s.reconcile.ready:
			case <-r.Context().Done():
				return
			}
		}
		h.ServeHTTP(w, r)
	})
}

// reconcileWarnings are the warnings the reconcile adds to health.
func (s *Server) reconcileWarnings() []string {
	_, late, err := s.reconcile.result()
	var warnings []string
	if late {
		warnings = append(warnings, reconcileRunning)
	}
	if err != nil {
		warnings = append(warnings, "reconcile: "+err.Error())
	}
	return warnings
}

// HomesMaxAge is how old the last check of the agent homes may be before a
// health read starts another in the background.
const HomesMaxAge = 10 * time.Minute

// homeCache holds the last check of the agent homes. A check in flight is
// shared by every caller; pending are the session homes no check has covered.
type homeCache struct {
	mu       sync.Mutex
	findings []api.HomeFinding
	err      error
	at       time.Time
	flight   chan struct{}
	covered  map[string]bool
	pending  map[string]bool
	queued   bool
}

// refreshHomes checks the agent homes, or waits for the check in flight, and
// checks again when a session home arrived meanwhile.
func (s *Server) refreshHomes(ctx context.Context) {
	if s.deps.Homes == nil {
		return
	}
	waited := false
	for {
		s.homes.mu.Lock()
		if f := s.homes.flight; f != nil {
			s.homes.mu.Unlock()
			select {
			case <-f:
			case <-ctx.Done():
				return
			}
			waited = true
			continue
		}
		if waited && len(s.homes.pending) == 0 {
			s.homes.mu.Unlock()
			return
		}
		f := make(chan struct{})
		s.homes.flight = f
		if s.homes.covered == nil {
			s.homes.covered = map[string]bool{}
		}
		for h := range s.homes.pending {
			s.homes.covered[h] = true
		}
		clear(s.homes.pending)
		s.homes.mu.Unlock()

		findings, err := s.deps.Homes(ctx)

		s.homes.mu.Lock()
		s.homes.findings, s.homes.err, s.homes.at = findings, err, s.deps.Now()
		s.homes.flight = nil
		s.homes.mu.Unlock()
		close(f)
		return
	}
}

// RecheckHomes checks the agent homes now; concurrent calls share one check.
func (s *Server) RecheckHomes(ctx context.Context) {
	s.refreshHomes(ctx)
	s.note(port.TopicJobs)
}

// recheckHomesInBackground starts at most one goroutine at a time, which
// checks until no session home is pending and the result is fresh, or the
// daemon stops. It stays queued while it checks, so a read in between starts
// no second one.
func (s *Server) recheckHomesInBackground() {
	s.homes.mu.Lock()
	if s.homes.queued {
		s.homes.mu.Unlock()
		return
	}
	s.homes.queued = true
	s.homes.mu.Unlock()
	s.driving.Go(func() {
		for {
			s.homes.mu.Lock()
			if s.life.Err() != nil || len(s.homes.pending) == 0 && !s.homesStaleLocked() {
				s.homes.queued = false
				s.homes.mu.Unlock()
				return
			}
			s.homes.mu.Unlock()
			s.RecheckHomes(s.life)
		}
	})
}

// noteSessionHome rechecks the homes in the background when home is one no
// check has covered.
func (s *Server) noteSessionHome(home string) {
	if home == "" || s.deps.Homes == nil {
		return
	}
	s.homes.mu.Lock()
	known := s.homes.covered[home] || s.homes.pending[home]
	if !known {
		if s.homes.pending == nil {
			s.homes.pending = map[string]bool{}
		}
		s.homes.pending[home] = true
	}
	s.homes.mu.Unlock()
	if !known {
		s.recheckHomesInBackground()
	}
}

// homesStale reports whether the last finished check is older than HomesMaxAge
// and none runs.
func (s *Server) homesStale() bool {
	s.homes.mu.Lock()
	defer s.homes.mu.Unlock()
	return s.homesStaleLocked()
}

func (s *Server) homesStaleLocked() bool {
	return s.deps.Homes != nil && s.homes.flight == nil && !s.homes.at.IsZero() && s.deps.Now().Sub(s.homes.at) > HomesMaxAge
}

// homeWarnings are the cached findings a command fixes, and the failed
// check; a finding with no fix is not a warning.
func (s *Server) homeWarnings() []string {
	if s.homesStale() {
		s.recheckHomesInBackground()
	}
	s.homes.mu.Lock()
	defer s.homes.mu.Unlock()
	var out []string
	for _, f := range s.homes.findings {
		if f.Fix != "" {
			out = append(out, f.Warning())
		}
	}
	if s.homes.err != nil {
		out = append(out, "agent homes: "+s.homes.err.Error())
	}
	return out
}

// homeChecks are the homes' findings as doctor lines, from a check run now.
func (s *Server) homeChecks(ctx context.Context) []api.DoctorCheck {
	s.refreshHomes(ctx)
	s.homes.mu.Lock()
	defer s.homes.mu.Unlock()
	var out []api.DoctorCheck
	for _, f := range s.homes.findings {
		m := api.DoctorInfo
		if f.Fix != "" {
			m = api.DoctorMissing
		}
		out = append(out, api.DoctorCheck{Mark: m, Name: "agent home " + f.Home + ": " + f.Entry, Info: f.Problem, Fix: f.Fix})
	}
	if s.homes.err != nil {
		out = append(out, api.DoctorCheck{Mark: api.DoctorMissing, Name: "agent homes", Info: s.homes.err.Error()})
	}
	return out
}

func decodeAdmin(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(v); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return false
	}
	return true
}

func (s *Server) handleDryRunTurn(w http.ResponseWriter, r *http.Request) {
	if s.deps.DryRunTurn == nil {
		writeError(w, http.StatusServiceUnavailable, "this daemon keeps no dry-run turn")
		return
	}
	var req api.DryRunTurnRequest
	if !decodeAdmin(w, r, &req) {
		return
	}
	if _, err := parsePRURL(req.PR); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	t, err := s.deps.DryRunTurn(context.WithoutCancel(r.Context()), req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, t)
}

func (s *Server) handleDoctor(w http.ResponseWriter, r *http.Request) {
	q := api.DoctorQuery{Repo: r.URL.Query().Get("repo"), PR: r.URL.Query().Get("pr")}
	if q.PR != "" {
		if _, err := parsePRURL(q.PR); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	var rep api.DoctorReport
	if s.deps.Doctor != nil {
		var err error
		if rep, err = s.deps.Doctor(r.Context(), q); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	rep.Checks = append(rep.Checks, s.homeChecks(r.Context())...)
	rec, late, err := s.reconcile.result()
	switch {
	case late:
		rep.Checks = append(rep.Checks, api.DoctorCheck{Mark: api.DoctorInfo, Name: "reconcile", Info: "still running"})
	case rec != nil:
		rep.Reconcile = rec
		rep.Checks = append(rep.Checks, reconcileChecks(*rec, err)...)
	}
	rep.Checks = append(append([]api.DoctorCheck{}, rep.Checks...), loadChecks(s.deps.Meter, s.deps.Now())...)
	if len(s.deps.RowProviders) > 0 {
		rep.Checks = append(rep.Checks, s.readModelCheck(r.Context()))
	}
	writeJSON(w, rep)
}

func (s *Server) handleMeter(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.wireMeter(s.deps.Now()))
}

func reconcileChecks(rep api.ReconcileReport, err error) []api.DoctorCheck {
	info := fmt.Sprintf("at start-up, %s: %d unrecorded, %d closed by my review, %d sessions gone, %d view items rewritten",
		rep.At.Local().Format("2006-01-02 15:04"), len(rep.Unrecorded), len(rep.Closed), len(rep.GoneSessions), rep.Rewritten)
	out := []api.DoctorCheck{{Mark: api.DoctorInfo, Name: "reconcile", Info: info}}
	if err != nil {
		out[0] = api.DoctorCheck{Mark: api.DoctorMissing, Name: "reconcile", Info: err.Error() + "; " + info, Fix: "see the daemon's log"}
	}
	return out
}

func (s *Server) handleTurnShow(w http.ResponseWriter, r *http.Request) {
	u, err := parsePRURL(r.URL.Query().Get("pr"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.deps.Turns == nil {
		writeError(w, http.StatusServiceUnavailable, "the daemon keeps no turns")
		return
	}
	books, err := s.deps.Turns.Store.Books(r.Context(), u)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	b, ok := books[u]
	if !ok || len(b.Turns) == 0 {
		writeError(w, http.StatusNotFound, "no turn of "+u.Label())
		return
	}
	out := api.TurnBook{Unit: named(u), Turns: make([]api.TurnShown, 0, len(b.Turns))}
	for _, t := range b.Turns {
		shown := api.TurnShown{Turn: t, Records: []turn.Record{}, Answers: []turn.Answer{}}
		if s.deps.Records != nil {
			stored, found, err := s.deps.Records.Turn(r.Context(), t.ID)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			if found {
				shown.Instructions = stored.Text
			}
		}
		for _, rec := range b.Records {
			if rec.TurnSeq == t.Seq {
				shown.Records = append(shown.Records, rec)
			}
		}
		for _, a := range b.Answers {
			if a.TurnSeq == t.Seq {
				shown.Answers = append(shown.Answers, a)
			}
		}
		out.Turns = append(out.Turns, shown)
	}
	writeJSON(w, out)
}

const (
	ReadModelCheckEnv   = "LOOP_READMODEL_CHECK"
	readModelCheckEvery = 10 * time.Minute
	readModelWarning    = "read model"
	readModelShown      = 5
)

// checkReadModel compares every served row with a fresh derive, keeps the
// mismatches as a health warning and logs each one.
func (s *Server) checkReadModel(ctx context.Context) []string {
	ctx, scope := meter.Start(ctx, meter.Loop("readmodel check"))
	defer scope.End()
	diffs := s.readModel().Check(ctx)
	for _, d := range diffs {
		s.logf("readmodel: mismatch %s", d)
	}
	s.warnings.set(readModelWarning, readModelSummary(diffs))
	return diffs
}

func (s *Server) checkReadModelEvery(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.checkReadModel(ctx)
		}
	}
}

// readModelSummary names the first mismatches, "" for none.
func readModelSummary(diffs []string) string {
	if len(diffs) == 0 {
		return ""
	}
	shown := diffs[:min(len(diffs), readModelShown)]
	text := fmt.Sprintf("%d rows differ from a fresh derive: %s", len(diffs), strings.Join(shown, "; "))
	if len(diffs) > len(shown) {
		text += "; ..."
	}
	return text
}

func (s *Server) readModelCheck(ctx context.Context) api.DoctorCheck {
	if !s.readModel().Served() {
		return api.DoctorCheck{Mark: api.DoctorInfo, Name: "read model", Info: "no rows served yet"}
	}
	diffs := s.checkReadModel(ctx)
	if len(diffs) == 0 {
		return api.DoctorCheck{Mark: api.DoctorInfo, Name: "read model", Info: "every row matches a fresh derive"}
	}
	return api.DoctorCheck{Mark: api.DoctorMissing, Name: "read model", Info: readModelSummary(diffs), Fix: "see the daemon's log for the differing fields"}
}
