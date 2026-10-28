package daemon

import (
	"context"
	"net/http"
	"strconv"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/catalog"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

// RunHost reaches a run's workspace and session in their workspace manager.
type RunHost interface {
	// Focus brings the workspace on screen in the manager's app.
	Focus(ctx context.Context, manager, workspaceID string) error
	// Wake gives the dormant session of u a live process again in its manager.
	Wake(ctx context.Context, u unit.Unit, sess turn.Session) error
}

// transcriptPage is how many lines a transcript read without limit returns.
const transcriptPage = 200

func (s *Server) handleRuns(w http.ResponseWriter, r *http.Request) {
	g, ok := s.tasks(w)
	if !ok || !s.agentTurns(w) {
		return
	}
	ctx := r.Context()
	e, err := g.Task(ctx, task.ID(r.PathValue("id")))
	if err != nil {
		writeTasksError(w, err)
		return
	}
	runs, err := s.runViews(ctx, task.UnitOf(e.ID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, runs)
}

// runViews are the runs of u as the task's page shows them, newest first.
func (s *Server) runViews(ctx context.Context, u unit.Unit) ([]api.RunView, error) {
	if s.deps.Turns == nil {
		return nil, nil
	}
	runs, err := s.deps.Turns.RunsOf(ctx, u)
	if err != nil {
		return nil, err
	}
	out := make([]api.RunView, len(runs))
	for i, r := range runs {
		out[i] = s.runView(r)
	}
	return out, nil
}

func (s *Server) runView(r turns.Run) api.RunView {
	facts := catalog.RunFacts{
		Workspace:  r.Manager != "" && r.Workspace != "" && s.deps.RunHost != nil,
		Session:    r.Session != "" && r.State != string(turn.SessionClosed),
		Transcript: r.HasTranscript && s.deps.Transcripts != nil,
	}
	return api.RunView{Seq: r.Turn.Seq, Intent: string(r.Turn.Intent), State: string(r.Turn.State), Manager: r.Manager,
		Workspace: r.Workspace, Worktree: r.Worktree, Session: r.Session, SessionState: r.State, Resume: r.Resume,
		Actions: catalog.RunActions(facts)}
}

// run is the run the request's seq names, false once it answered.
func (s *Server) run(w http.ResponseWriter, r *http.Request) (turns.Run, unit.Unit, bool) {
	if !s.agentTurns(w) {
		return turns.Run{}, unit.Unit{}, false
	}
	seq, err := strconv.ParseInt(r.PathValue("seq"), 10, 64)
	if err != nil || seq <= 0 {
		writeError(w, http.StatusBadRequest, "a run is named by its turn's seq, a positive number")
		return turns.Run{}, unit.Unit{}, false
	}
	run, u, ok, err := s.deps.Turns.RunOf(r.Context(), seq)
	switch {
	case err != nil:
		writeError(w, http.StatusInternalServerError, err.Error())
		return turns.Run{}, unit.Unit{}, false
	case !ok:
		writeError(w, http.StatusNotFound, "no run "+strconv.FormatInt(seq, 10))
		return turns.Run{}, unit.Unit{}, false
	}
	return run, u, true
}

// offers answers 409 unless the run offers the run action name.
func (s *Server) offers(w http.ResponseWriter, run turns.Run, name string) bool {
	for _, a := range s.runView(run).Actions {
		if a == name {
			return true
		}
	}
	if s.deps.RunHost == nil && name != catalog.RunTranscript {
		writeError(w, http.StatusServiceUnavailable, "the daemon reaches no workspace manager")
		return false
	}
	writeError(w, http.StatusConflict, "run "+strconv.FormatInt(run.Turn.Seq, 10)+" does not offer "+name)
	return false
}

func (s *Server) handleRunOpen(w http.ResponseWriter, r *http.Request) {
	run, _, ok := s.run(w, r)
	if !ok || !s.offers(w, run, catalog.RunOpen) {
		return
	}
	if err := s.deps.RunHost.Focus(r.Context(), run.Manager, run.Workspace); err != nil {
		writeActionError(w, err)
		return
	}
	writeJSON(w, struct{}{})
}

// handleRunEnter brings the run's session on screen: a live one at once, a
// dormant one after its manager wakes the unit's newest dormant session in
// the run's workspace.
func (s *Server) handleRunEnter(w http.ResponseWriter, r *http.Request) {
	run, u, ok := s.run(w, r)
	if !ok || !s.offers(w, run, catalog.RunEnter) {
		return
	}
	ctx := context.WithoutCancel(r.Context())
	if run.State != string(turn.SessionActive) {
		sess, _, found, err := s.deps.Turns.DormantFor(ctx, u, run.Manager, run.Workspace, false)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !found {
			writeError(w, http.StatusConflict, "run "+strconv.FormatInt(run.Turn.Seq, 10)+" has no session its manager can wake")
			return
		}
		s.deps.LoopMu.Lock()
		err = s.deps.RunHost.Wake(ctx, u, sess)
		s.deps.LoopMu.Unlock()
		if err != nil {
			writeActionError(w, err)
			return
		}
	}
	if err := s.deps.RunHost.Focus(ctx, run.Manager, run.Workspace); err != nil {
		writeActionError(w, err)
		return
	}
	writeJSON(w, struct{}{})
}

func (s *Server) handleRunTranscript(w http.ResponseWriter, r *http.Request) {
	run, _, ok := s.run(w, r)
	if !ok || !s.offers(w, run, catalog.RunTranscript) {
		return
	}
	from, limit, ok := pageQuery(w, r)
	if !ok {
		return
	}
	lines, total, err := s.deps.Transcripts.Page(r.Context(), run.Turn.AgentSession, from, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	page := api.TranscriptPage{Lines: make([]api.TranscriptLine, len(lines)), Total: total}
	for i, l := range lines {
		page.Lines[i] = api.TranscriptLine{Role: l.Role, Text: l.Text, At: l.At}
	}
	writeJSON(w, page)
}

// pageQuery reads the query parameters from, 0 when absent, and limit,
// transcriptPage when absent.
func pageQuery(w http.ResponseWriter, r *http.Request) (from, limit int, ok bool) {
	from, limit = 0, transcriptPage
	for name, dst := range map[string]*int{"from": &from, "limit": &limit} {
		v := r.URL.Query().Get(name)
		if v == "" {
			continue
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, name+" is a number of lines, 0 or more")
			return 0, 0, false
		}
		*dst = n
	}
	return from, limit, true
}

// handleAttach makes the calling session a run of the task: the daemon
// starts the turn of the intent holding the attach role on it by hand, and
// the session takes the task as its unit.
func (s *Server) handleAttach(w http.ResponseWriter, r *http.Request) {
	handle, ok := s.caller(w, r, true)
	if !ok || !s.agentTurns(w) {
		return
	}
	g, ok := s.tasks(w)
	if !ok {
		return
	}
	var req api.AttachRequest
	if !decodeAgent(w, r, &req) {
		return
	}
	if req.Cwd == "" {
		writeError(w, http.StatusBadRequest, "cwd is the directory of the session that attaches")
		return
	}
	if s.deps.TurnByHand == nil {
		writeError(w, http.StatusServiceUnavailable, "the daemon runs no turn by hand")
		return
	}
	ctx := context.WithoutCancel(r.Context())
	e, err := g.Task(ctx, task.ID(r.PathValue("id")))
	if err != nil {
		writeTasksError(w, err)
		return
	}
	u := task.UnitOf(e.ID)
	d, ok := s.loaded().Registry.ByRole(builtin.RoleAttach, u.Provider)
	if !ok {
		writeError(w, http.StatusConflict, "no intent of the profile holds the role "+builtin.RoleAttach)
		return
	}
	st := turns.SessionStart{Session: handle, Pane: req.Pane, AgentSession: req.AgentSession, Cwd: req.Cwd, Home: req.Home, Agent: req.Agent}
	if err := s.deps.Turns.SessionStarted(ctx, st); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.noteSessionHome(req.Home)
	res, err := s.deps.TurnByHand(ctx, api.ByHandRequest{Intent: string(d.Name), URL: u.String(), Agent: req.Agent, Cwd: req.Cwd,
		Pane: req.Pane, AgentSession: req.AgentSession, Home: req.Home}, handle)
	if err != nil {
		writeAgentError(w, err)
		return
	}
	if err := s.deps.Turns.ClaimFor(ctx, handle, u); err != nil {
		writeAgentError(w, err)
		return
	}
	writeJSON(w, res)
}
