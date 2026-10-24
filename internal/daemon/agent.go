package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/turns"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/meter"
)

// RecordFileSlack is how much older than its turn's start a record file may
// be.
const RecordFileSlack = 2 * time.Second

const agentBodyMax = 1 << 20

// callerHandle is the process handle of the request, "" when it names none;
// a header that is not a handle is refused.
func callerHandle(w http.ResponseWriter, r *http.Request, required bool) (string, bool) {
	v := r.Header.Get(api.SessionHeader)
	if v == "" && !required {
		return "", true
	}
	h, err := api.SessionHandle(v)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return "", false
	}
	return h, true
}

// caller is the loop's session of the request's process handle, "" when it
// names none. With the manager that gave the handle, a process no session took
// becomes one; without, a handle no session took is refused.
func (s *Server) caller(w http.ResponseWriter, r *http.Request, required bool) (string, bool) {
	h, ok := callerHandle(w, r, required)
	if !ok || h == "" || s.deps.Turns == nil {
		return h, ok
	}
	ctx := r.Context()
	if m := r.Header.Get(api.ManagerHeader); m != "" {
		if len(s.deps.ManagerNames) > 0 && !slices.Contains(s.deps.ManagerNames, m) {
			writeError(w, http.StatusBadRequest, api.ManagerHeader+" names "+m+", which is not an enabled workspace manager")
			return "", false
		}
		id, err := s.deps.Turns.Resolve(ctx, m, h)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return "", false
		}
		return id, true
	}
	id, known, err := s.deps.Turns.SessionOfHandle(ctx, h)
	switch {
	case err != nil:
		writeError(w, http.StatusInternalServerError, err.Error())
		return "", false
	case !known:
		writeError(w, http.StatusConflict, api.ErrUnknownSession.Error())
		return "", false
	}
	return id, true
}

func decodeAgent(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, agentBodyMax)).Decode(v); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return false
	}
	return true
}

func (s *Server) agentTurns(w http.ResponseWriter) bool {
	if s.deps.Turns == nil {
		writeError(w, http.StatusServiceUnavailable, "the daemon keeps no turns")
		return false
	}
	return true
}

// writeAgentError answers a use case's error: a refusal is 409, a malformed
// record 400 with every problem, any other as an action's.
func writeAgentError(w http.ResponseWriter, err error) {
	var problems record.Problems
	var refusal turns.Refusal
	switch {
	case errors.As(err, &problems):
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "the record was refused; fix every problem and send it again", "detail": problems.Error(), "problems": []string(problems)})
	case errors.As(err, &refusal):
		writeError(w, http.StatusConflict, refusal.Reason)
	default:
		writeActionError(w, err)
	}
}

func (s *Server) handleTurnStart(w http.ResponseWriter, r *http.Request) {
	handle, ok := s.caller(w, r, false)
	if !ok {
		return
	}
	var req api.TurnStartRequest
	if !decodeAgent(w, r, &req) {
		return
	}
	if _, err := intentDef(s.loaded(), req.Intent); err != nil {
		writeAgentError(w, err)
		return
	}
	if req.Turn == "" {
		writeError(w, http.StatusBadRequest, "turn: required, the id of a turn")
		return
	}
	if s.refuseUnitOf(w, func() (unit.Unit, bool) { return s.bookUnit(r.Context(), turns.Key{Turn: req.Turn}) }) {
		return
	}
	if s.deps.Records == nil {
		writeError(w, http.StatusServiceUnavailable, "the daemon has no store")
		return
	}
	ctx := context.WithoutCancel(r.Context())
	stored, found, err := s.deps.Records.Turn(ctx, req.Turn)
	switch {
	case err != nil:
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	case !found:
		writeError(w, http.StatusNotFound, fmt.Sprintf("turn %s not found; turns are kept 30 days", req.Turn))
		return
	case stored.Kind != req.Intent:
		writeError(w, http.StatusConflict, fmt.Sprintf("turn %s is a %s turn", req.Turn, stored.Kind))
		return
	}
	if s.deps.Turns != nil {
		if err := s.startStored(ctx, handle, req.Turn); err != nil {
			writeAgentError(w, err)
			return
		}
	}
	writeJSON(w, api.TurnText{Text: stored.Text})
}

// startStored starts the turn of id on handle's session. Instructions stored
// for no turn (a dry run, the take-over question) start nothing.
func (s *Server) startStored(ctx context.Context, handle, id string) error {
	b, ok, err := s.deps.Turns.Store.BookOf(ctx, turns.Key{Turn: id})
	if err != nil {
		return err
	}
	if _, found := b.ByID(id); !ok || !found {
		return nil
	}
	_, err = s.deps.Turns.Start(ctx, handle, id)
	return err
}

func (s *Server) handleTurnByHand(w http.ResponseWriter, r *http.Request) {
	handle, ok := s.caller(w, r, false)
	if !ok {
		return
	}
	var req api.ByHandRequest
	if !decodeAgent(w, r, &req) {
		return
	}
	if s.refuseUnitOf(w, func() (unit.Unit, bool) { return s.byHandUnit(r.Context(), req, handle) }) {
		return
	}
	if s.deps.TurnByHand == nil {
		writeError(w, http.StatusServiceUnavailable, "the daemon runs no turn by hand")
		return
	}
	if req.Cwd == "" {
		writeError(w, http.StatusBadRequest, "cwd is the directory of the session that asks")
		return
	}
	ctx := context.WithoutCancel(r.Context())
	if handle != "" && s.deps.Turns != nil {
		st := turns.SessionStart{Session: handle, Pane: req.Pane, AgentSession: req.AgentSession, Cwd: req.Cwd, Home: req.Home, Agent: req.Agent}
		if err := s.deps.Turns.SessionStarted(ctx, st); err != nil {
			s.logf("turn by hand: registering %s: %v", handle, err)
		} else {
			s.noteSessionHome(req.Home)
		}
	}
	l := s.loaded()
	d, err := intentDef(l, req.Intent)
	if err == nil && !d.UserFacing {
		err = badRequest{fmt.Sprintf("unknown intent %q; use %s", req.Intent, l.Registry.Names())}
	}
	if err != nil {
		writeAgentError(w, err)
		return
	}
	if req.Inputs, err = CheckInputs(l.Registry, d, req.Inputs, surfaceInstructions, "", false); err != nil {
		writeAgentError(w, err)
		return
	}
	req.Guidance = cutGuidance(d, req.Guidance)
	intake := s.intakes != nil && s.deps.ByHandRef != nil && d.Intake && d.Brief.Wait
	var u unit.Unit
	if intake || d.Prepare != "" {
		if u, err = s.byHandRef(ctx, req, handle); err != nil {
			writeAgentError(w, err)
			return
		}
	}
	if d.Prepare != "" {
		if req, err = s.prepareByHand(ctx, d, u, req); err != nil {
			writeAgentError(w, err)
			return
		}
	}
	if intake {
		s.turnByHandIntake(w, ctx, d, u, req, handle)
		return
	}
	res, err := s.deps.TurnByHand(ctx, req, handle)
	if err != nil {
		writeAgentError(w, err)
		return
	}
	writeJSON(w, res)
}

// surfaceInstructions is the input surface of a turn by hand.
const surfaceInstructions = "instructions"

func (s *Server) byHandRef(ctx context.Context, req api.ByHandRequest, handle string) (unit.Unit, error) {
	if s.deps.ByHandRef != nil {
		return s.deps.ByHandRef(ctx, req, handle)
	}
	return parsePRURL(req.URL)
}

// prepareByHand runs the intent's prepare for a turn by hand, whose
// request then carries what it filled.
func (s *Server) prepareByHand(ctx context.Context, d dintent.Def, u unit.Unit, req api.ByHandRequest) (api.ByHandRequest, error) {
	prepare, ok := prepares[d.Prepare]
	if !ok {
		return req, fmt.Errorf("%s: the daemon has no prepare %s", d.Name, d.Prepare)
	}
	c := Call{Guidance: req.Guidance, Inputs: maps.Clone(req.Inputs)}
	if _, err := prepare(s, ctx, u, &c); err != nil {
		return req, err
	}
	req.Guidance, req.Inputs = c.Guidance, c.Inputs
	return req, nil
}

func (s *Server) handleTurnEnd(w http.ResponseWriter, r *http.Request) {
	handle, ok := s.caller(w, r, false)
	if !ok || !s.agentTurns(w) {
		return
	}
	var req api.TurnEndRequest
	if !decodeAgent(w, r, &req) {
		return
	}
	var problems record.Problems
	if req.Turn == "" {
		problems = append(problems, "turn: required; the id on the first line of the turn's instructions")
	}
	var u unit.Unit
	if req.PR != "" {
		p, err := s.parseUnitArg(req.PR)
		if err != nil {
			problems = append(problems, "pr: "+err.Error())
		}
		u = p
	}
	var rec record.Record
	if len(req.Record) == 0 || string(req.Record) == "null" {
		problems = append(problems, "record: required")
	} else if parsed, err := record.Parse(req.Record); err != nil {
		problems = append(problems, "record: "+err.Error())
	} else {
		rec = parsed
	}
	if len(problems) > 0 {
		writeAgentError(w, problems)
		return
	}
	ctx := context.WithoutCancel(r.Context())
	if err := s.recordIsFresh(ctx, req); err != nil {
		writeAgentError(w, err)
		return
	}
	got, err := s.deps.Turns.RecordTurnEnd(ctx, turns.RecordRequest{Session: handle, AgentSession: req.AgentSession, Turn: req.Turn, Unit: u, Via: "socket", Record: rec})
	if err != nil {
		writeAgentError(w, err)
		return
	}
	ended := api.TurnEnded{PR: unitArg(got.Unit), State: string(got.State), Added: got.Added, Note: got.Note}
	if d := got.DroppedNote(); d != "" && ended.Note != "" {
		ended.Note += "; " + d
	} else if d != "" {
		ended.Note = d
	}
	if got.Ledger != nil {
		l := wireItemsResult(*got.Ledger)
		ended.Ledger = &l
	}
	writeJSON(w, ended)
}

// recordIsFresh refuses a record file written before its turn started: a
// file an earlier turn left behind.
func (s *Server) recordIsFresh(ctx context.Context, req api.TurnEndRequest) error {
	if req.WrittenAt.IsZero() {
		return nil
	}
	b, ok, err := s.deps.Turns.Store.BookOf(ctx, turns.Key{Turn: req.Turn})
	if err != nil || !ok {
		return err
	}
	t, found := b.ByID(req.Turn)
	if !found {
		return nil
	}
	start := t.StartedAt
	if start.IsZero() {
		start = t.SentAt
	}
	if req.WrittenAt.Before(start.Add(-RecordFileSlack)) {
		const stamp = "2006-01-02 15:04:05"
		return turns.Refusal{Reason: fmt.Sprintf("the record file was written at %s, before turn %s started at %s; write it again for this turn",
			req.WrittenAt.Local().Format(stamp), req.Turn, start.Local().Format(stamp))}
	}
	return nil
}

func (s *Server) handleSessionStart(w http.ResponseWriter, r *http.Request) {
	handle, ok := s.caller(w, r, true)
	if !ok || !s.agentTurns(w) {
		return
	}
	var req api.SessionStartRequest
	if !decodeAgent(w, r, &req) {
		return
	}
	if s.refuseUnitOf(w, func() (unit.Unit, bool) { return s.bookUnit(r.Context(), turns.Key{Session: handle}) }) {
		return
	}
	st := turns.SessionStart{Session: handle, Pane: req.Pane, WorkspaceID: req.WorktreeID, AgentSession: req.AgentSession, Source: req.Source, Cwd: req.Cwd, Home: req.Home,
		Agent: req.Agent, Transcript: req.Transcript}
	s.refreshSessions()
	if err := s.deps.Turns.SessionStarted(context.WithoutCancel(r.Context()), st); err != nil {
		writeAgentError(w, err)
		return
	}
	s.noteSessionHome(req.Home)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSessionStop(w http.ResponseWriter, r *http.Request) {
	handle, ok := s.caller(w, r, true)
	if !ok || !s.agentTurns(w) {
		return
	}
	var req api.StopRequest
	if !decodeAgent(w, r, &req) {
		return
	}
	s.refreshSessions()
	ans, err := s.deps.Turns.Stop(context.WithoutCancel(r.Context()), turns.StopRequest{Session: handle, AgentSession: req.AgentSession, PromptID: req.PromptID, LastMessage: req.LastMessage,
		Agent: req.Agent, Transcript: req.Transcript})
	if err != nil {
		writeAgentError(w, err)
		return
	}
	writeJSON(w, api.StopAnswer{Block: ans.Block, Reason: ans.Reason})
	if !ans.Block {
		s.deliverAfterStop(handle)
	}
}

func (s *Server) handleSessionPrompt(w http.ResponseWriter, r *http.Request) {
	handle, ok := s.caller(w, r, true)
	if !ok || !s.agentTurns(w) {
		return
	}
	var req api.PromptRequest
	if !decodeAgent(w, r, &req) {
		return
	}
	if s.refuseUnitOf(w, func() (unit.Unit, bool) { return s.bookUnit(r.Context(), turns.Key{Session: handle}) }) {
		return
	}
	if err := s.deps.Turns.PromptSubmitted(context.WithoutCancel(r.Context()), handle, req.Prompt); err != nil {
		writeAgentError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// refreshSessions reloads the agents and terminals a hook just changed, so the
// next reads see them.
func (s *Server) refreshSessions() {
	if r, ok := s.deps.Turns.Sessions.(turns.Refresher); ok {
		r.Refresh()
	}
}

// deliverAfterStop types the answer queued for the handle's session once
// the hook got its reply: the agent starts no turn before its stop ends.
// After waiting on the loop, the session may be busy again, so Orca decides.
func (s *Server) deliverAfterStop(handle string) {
	s.driving.Go(func() {
		var err error
		phase(s.life, meter.Loop("answer"), func(ctx context.Context) { err = s.deliverStopped(ctx, handle) })
		if err != nil && s.life.Err() == nil {
			s.logf("queued answer for %s: %v", handle, err)
		}
	})
}

func (s *Server) deliverStopped(ctx context.Context, handle string) error {
	t := s.deps.Turns
	if b, ok, err := t.Store.BookOf(ctx, turns.Key{Session: handle}); err == nil && ok {
		ctx = s.readAheadQueued(ctx, b.Unit)
	}
	if s.deps.LoopMu.TryLock() {
		defer s.deps.LoopMu.Unlock()
		return t.DeliverStopped(ctx, handle)
	}
	s.deps.LoopMu.Lock()
	defer s.deps.LoopMu.Unlock()
	b, ok, err := t.Store.BookOf(ctx, turns.Key{Session: handle})
	if err != nil || !ok {
		return err
	}
	return t.Deliver(ctx, b.Unit)
}

func (s *Server) handleTurnAnswer(w http.ResponseWriter, r *http.Request) {
	handle, ok := s.caller(w, r, false)
	if !ok || !s.agentTurns(w) {
		return
	}
	u, err := s.parseUnitArg(r.URL.Query().Get("pr"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "pr: "+err.Error())
		return
	}
	text, found, err := s.deps.Turns.NewestAnswer(r.Context(), u, handle)
	switch {
	case err != nil:
		writeAgentError(w, err)
	case !found:
		writeError(w, http.StatusNotFound, "no answer stored for "+u.Label())
	default:
		writeJSON(w, api.TurnText{Text: text})
	}
}
