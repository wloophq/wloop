package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/catalog"
	"github.com/wloophq/wloop/internal/core/intake"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/duration"
	dgh "github.com/wloophq/wloop/internal/domain/github"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/readmodel/prrow"
	"github.com/wloophq/wloop/internal/situation"
)

// Actions are the operations with side effects outside the store. The daemon
// runs each one but Open under its loop mutex.
type Actions interface {
	Open(ctx context.Context, u unit.Unit, c Call) error // Focus the workspace, else the PR in the browser
	Adopt(ctx context.Context, u unit.Unit, c Call) error
	// Intent runs a round of the named intent; Call.Inputs holds the
	// request's inputs and those its prepare filled.
	Intent(ctx context.Context, name string, u unit.Unit, c Call) error
	Run(ctx context.Context, u unit.Unit, c Call) error // sends the held round now; never counts against the cap
	// Remove deletes the local workspace of a merged or closed pull request,
	// or answers a Refusal when it would lose work.
	Remove(ctx context.Context, u unit.Unit, c Call) error
}

// Unblocker is Actions that lifts the start block of a round.
type Unblocker interface {
	// TrustAndRun trusts the folder the round's start block names, in the
	// agent home the round ran with or, unknown, in every agent home the
	// loop knows, lifts the block and sends the round now.
	TrustAndRun(ctx context.Context, u unit.Unit, c Call) error
	// Unblock marks the trigger of the round a start block holds handled,
	// so the round waits for a new one; the caller records the block lifted.
	Unblock(ctx context.Context, u unit.Unit, c Call) error
}

// unblocking calls do on Actions that lift a start block.
func unblocking(do func(un Unblocker, ctx context.Context, u unit.Unit, c Call) error) func(a Actions, ctx context.Context, u unit.Unit, c Call) error {
	return func(a Actions, ctx context.Context, u unit.Unit, c Call) error {
		un, ok := a.(Unblocker)
		if !ok {
			return errActionsUnavailable
		}
		return do(un, ctx, u, c)
	}
}

// Refusal is an action that would do harm, with the reason and its detail.
// Forceable marks a refusal that asking again with force would get past.
type Refusal struct {
	Reason, Detail string
	// Source names the tool whose message Detail is, "" for the loop's own.
	Source    string
	Forceable bool
}

func (e Refusal) Error() string { return e.Reason }

// Call is what a delegated action gets besides the pull request.
type Call struct {
	Guidance string
	// AgentHome is the caller's agent home; empty means the one the
	// project's settings name.
	AgentHome string
	// Out receives what the action reports, sent back to the caller.
	Out io.Writer
	// WorkspaceID is the workspace the inventory links to the pull request,
	// "" when none; remove acts on exactly that one.
	WorkspaceID string
	// Force lets remove past the workspace manager's terminal check.
	Force bool
	// Inputs are the intent's inputs, normalized to their types.
	Inputs map[string]any
	// Items are the unit's item ids and Task the GTD entry the action was asked from; the turn's
	// drafts are tied to them.
	Items []string
	Task  string
	// Manager and Parent are the request's: the workspace manager of the
	// worktree the turn creates and the caller's worktree.
	Manager, Parent string
}

// AheadPreparer is Actions that readies, before an intent's round takes the
// loop mutex, what the round needs and may take long to get, such as a
// clone of its repository.
type AheadPreparer interface {
	PrepareAhead(ctx context.Context, name string, u unit.Unit, c Call) error
}

const askedByYou = "asked by you"

var (
	errActionsUnavailable = errors.New("actions unavailable")
	errTurnsUnavailable   = errors.New("turns unavailable")
	errPlanUnavailable    = errors.New("the plan is unavailable")
)

type badRequest struct{ msg string }

func (e badRequest) Error() string { return e.msg }

// BadRequest is an error the daemon answers with 400.
func BadRequest(msg string) error { return badRequest{msg} }

type tooManyRequests struct{ msg string }

func (e tooManyRequests) Error() string { return e.msg }

type badGateway struct{ msg string }

func (e badGateway) Error() string { return e.msg }

type notFound struct{ msg string }

func (e notFound) Error() string { return e.msg }

// situationNow derives the situation the pull request would show unmuted.
type situationNow func() (api.Kind, string, error)

type delegated struct {
	call  func(a Actions, ctx context.Context, u unit.Unit, c Call) error
	loop  bool                   // writes to the loop, so waits on its mutex
	mark  func(o *dinv.Override) // nil leaves the override alone
	event string                 // "" records nothing
	data  string
	// unlink clears the workspace link, which the next sweep would clear.
	unlink bool
	// force always forces, as remove_force does.
	force bool
	// full reads the pull request in full ahead of the loop mutex.
	full bool
}

// AheadReader is Actions that reads, before an action takes the loop mutex,
// what the action asks GitHub, so that it asks nothing while holding it; full
// also reads the pull request in full.
type AheadReader interface {
	ReadAhead(ctx context.Context, u unit.Unit, full bool) context.Context
}

func (s *Server) readAhead(ctx context.Context, u unit.Unit, full bool) context.Context {
	if a, ok := s.deps.Actions.(AheadReader); ok {
		return a.ReadAhead(ctx, u, full)
	}
	return ctx
}

// readAheadQueued reads ahead the pull requests of units whose book holds an
// answer queued for delivery, which may launch a turn.
func (s *Server) readAheadQueued(ctx context.Context, units ...unit.Unit) context.Context {
	if _, ok := s.deps.Actions.(AheadReader); !ok || s.deps.Turns == nil {
		return ctx
	}
	books, err := s.deps.Turns.Store.Books(ctx, units...)
	if err != nil {
		return ctx
	}
	for _, u := range units {
		if !isPullRequest(u) || !queued(books[u]) {
			continue
		}
		ctx = s.readAhead(ctx, u, false)
	}
	return ctx
}

func queued(b turn.Book) bool {
	return slices.ContainsFunc(b.Answers, func(a turn.Answer) bool { return a.Route == turn.Queued })
}

var delegatedActions = map[string]delegated{
	"open":         {call: Actions.Open},
	"take_over":    {loop: true, event: dinv.KindAdopted, mark: func(o *dinv.Override) { o.Adopted = true }, call: Actions.Adopt},
	"run_now":      {loop: true, full: true, event: dinv.KindRoundSent, data: "run now", call: Actions.Run},
	"trust_run":    {loop: true, full: true, event: dinv.KindRoundSent, data: "trusted its folder and ran", call: unblocking(Unblocker.TrustAndRun)},
	"unblock":      {loop: true, event: dinv.KindRoundUnblocked, data: "dismissed by you", call: unblocking(Unblocker.Unblock)},
	"remove":       {loop: true, event: dinv.KindRemoved, unlink: true, call: Actions.Remove},
	"remove_force": {loop: true, event: dinv.KindRemoved, unlink: true, force: true, call: Actions.Remove},
}

var overrideActions = map[string]func(o *dinv.Override, req api.ActionRequest, now time.Time, current situationNow) (event, data string, err error){
	"mute": func(o *dinv.Override, req api.ActionRequest, now time.Time, current situationNow) (string, string, error) {
		if req.Until == "" && !req.UntilState {
			return "", "", badRequest{"mute needs until or until_state"}
		}
		if req.Until != "" && req.UntilState {
			return "", "", badRequest{"mute takes until or until_state, not both"}
		}
		clearMute(o)
		data := "until state change"
		if req.UntilState {
			kind, waitingOn, err := current()
			if err != nil {
				return "", "", err
			}
			o.MuteUntilStateChange, o.MutedKind, o.MutedWaitingOn = true, string(kind), waitingOn
		} else {
			d, err := duration.Parse(req.Until)
			if err != nil {
				return "", "", badRequest{"mute " + err.Error()}
			}
			o.MutedUntil = now.Add(d)
			data = "for " + req.Until
		}
		return dinv.KindMuted, data, nil
	},
	"unmute": func(o *dinv.Override, _ api.ActionRequest, _ time.Time, _ situationNow) (string, string, error) {
		clearMute(o)
		return dinv.KindUnmuted, "", nil
	},
	"pause": func(o *dinv.Override, _ api.ActionRequest, _ time.Time, _ situationNow) (string, string, error) {
		o.Hold = true
		return dinv.KindHeld, "", nil
	},
	"resume": func(o *dinv.Override, _ api.ActionRequest, _ time.Time, _ situationNow) (string, string, error) {
		o.Hold = false
		return dinv.KindReleased, "", nil
	},
	"track": func(o *dinv.Override, _ api.ActionRequest, _ time.Time, _ situationNow) (string, string, error) {
		o.Tracked = true
		return dinv.KindTracked, "", nil
	},
	"untrack": func(o *dinv.Override, _ api.ActionRequest, _ time.Time, _ situationNow) (string, string, error) {
		o.Tracked = false
		return dinv.KindUntracked, "", nil
	},
}

// roundActions act on the pending round of the unit's plan.
var roundActions = map[string]bool{"skip": true, "guide": true}

// intakeRouteActions change the open intake of the pull request.
var intakeRouteActions = map[string]func(s *Server, ctx context.Context, i intake.Intake) (string, error){
	"intake_without": func(s *Server, ctx context.Context, i intake.Intake) (string, error) {
		if i.Source == sourceSession {
			return "", conflict{msg: catalog.SessionWithout(i.Seq)}
		}
		return fmt.Sprintf("intake %d starts without the brief", i.Seq), s.intakes.Without(ctx, i.Seq)
	},
	"intake_cancel": func(s *Server, ctx context.Context, i intake.Intake) (string, error) {
		if err := s.intakes.Cancel(ctx, i.Seq); err != nil {
			return "", err
		}
		s.sessionAsks.forget(i.Seq)
		return fmt.Sprintf("intake %d canceled", i.Seq), nil
	},
}

// turnActions act on the pull request's turn book alone, so they need no
// inventory row. The ones that may type or launch hold the loop mutex: the
// launcher runs round code that expects it held.
var turnActions = map[string]func(s *Server, ctx context.Context, u unit.Unit, req api.ActionRequest, out io.Writer) error{
	"answer": (*Server).answer,
	"continue": func(s *Server, ctx context.Context, u unit.Unit, _ api.ActionRequest, out io.Writer) error {
		ctx = s.readAhead(ctx, u, false)
		s.deps.LoopMu.Lock()
		defer s.deps.LoopMu.Unlock()
		if err := s.deps.Turns.Continue(ctx, u); err != nil {
			return err
		}
		fmt.Fprintln(out, "the agent continues on "+u.Label())
		return nil
	},
	"dismiss": func(s *Server, ctx context.Context, u unit.Unit, _ api.ActionRequest, out io.Writer) error {
		if err := s.deps.Turns.Dismiss(ctx, u); err != nil {
			return err
		}
		fmt.Fprintln(out, "question on "+u.Label()+" dismissed")
		return nil
	},
}

var answerSources = []turn.Source{turn.Cockpit, turn.Notification, turn.TUI, turn.Terminal}

// answer answers the unit's open question message, as the Inbox answers it.
func (s *Server) answer(ctx context.Context, u unit.Unit, req api.ActionRequest, out io.Writer) error {
	if s.deps.Messages == nil {
		return errors.New("the Inbox is not wired")
	}
	m, ok, err := s.deps.Messages.Question(ctx, u)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w on %s", turn.ErrNoQuestion, u.Label())
	}
	return s.answerMessage(ctx, m, req, out)
}

func clearMute(o *dinv.Override) {
	o.MutedUntil, o.MuteUntilStateChange, o.MutedKind, o.MutedWaitingOn = time.Time{}, false, "", ""
}

// isBuiltin tells a built-in action of the catalog's dispatch tables.
func isBuiltin(name string) bool {
	_, isOverride := overrideActions[name]
	_, isDelegated := delegatedActions[name]
	isRound := roundActions[name]
	_, isTurn := turnActions[name]
	_, isIntake := intakeRouteActions[name]
	return isOverride || isDelegated || isRound || isTurn || isIntake || name == "brief_refresh"
}

func (s *Server) handleAction(w http.ResponseWriter, r *http.Request) {
	if d, ok := s.loaded().Registry.Get(dintent.Name(r.PathValue("action"))); ok && d.Runs(string(turn.RunnerHeadless)) {
		s.runModes(w, r, d)
		return
	}
	s.runAction(w, r)
}

func (s *Server) runAction(w http.ResponseWriter, r *http.Request) {
	u, ok := s.parseUnit(r)
	if !ok {
		writeNoUnit(w, r)
		return
	}
	name := r.PathValue("action")
	l := s.loaded()
	if !isBuiltin(name) && !isIntent(l, name) {
		writeError(w, http.StatusNotFound, "no action "+strconv.Quote(name))
		return
	}
	if err := unitAction(l, name, u, s.deps.Follows); err != nil {
		writeActionError(w, err)
		return
	}
	if why, err := s.taskRefusal(r.Context(), l, name, u); err != nil || why != "" {
		if err == nil {
			err = Refusal{Reason: why}
		}
		writeActionError(w, err)
		return
	}
	var req api.ActionRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	if err := s.checkActionInputs(l, name, &req); err != nil {
		writeActionError(w, err)
		return
	}
	ctx := context.WithoutCancel(r.Context())
	row := api.Row{Unit: u}
	if _, ok := turnActions[name]; !ok && isPullRequest(u) {
		if intakeIntent(l, name) && !slices.Contains(intakeSources, req.Source) {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("source %q is not one of %s", req.Source, strings.Join(intakeSources, ", ")))
			return
		}
		if s.intakes != nil && intakeIntent(l, name) {
			if err := s.expireSessionIntakes(ctx); err != nil {
				writeActionError(w, err)
				return
			}
		}
		var st api.ActionState
		var err error
		row, st, err = s.offered(ctx, u, catalog.Asked{Name: name, Inputs: req.Inputs}, req)
		if err != nil {
			writeActionError(w, err)
			return
		}
		u = row.Unit
		if name == "run_now" && strings.HasPrefix(st.Basis, catalog.BasisPending) {
			line, err := s.runPending(ctx, u, req)
			if err != nil {
				writeActionError(w, err)
				return
			}
			writeJSON(w, api.ActionResult{OK: true, Output: []string{line}})
			return
		}
	}
	if name == "brief_refresh" {
		s.refreshBrief(w, r, u)
		return
	}
	if change, ok := intakeRouteActions[name]; ok {
		s.changeIntakeOf(w, ctx, u, change)
		return
	}
	if s.intakes != nil && intakeIntent(l, name) {
		s.acceptAction(w, r, name, row, req)
		return
	}
	var out bytes.Buffer
	err := s.act(ctx, name, u, req, &out)
	var refused Refusal
	if errors.As(err, &refused) {
		s.logDetail(name, u, refused)
	}
	if d := delegatedActions[name]; d.unlink {
		s.rememberForceable(u, err)
	}
	if err != nil {
		writeActionError(w, err)
		return
	}
	writeJSON(w, api.ActionResult{OK: true, Output: lines(out.String())})
}

// offered is the state of the action on the pull request's row now; an
// action the row lacks or disables, or one whose basis changed, is a
// conflict. A forced remove on a row with no stored workspace link is the
// CLI's remove --force, which finds the workspace itself.
func (s *Server) offered(ctx context.Context, u unit.Unit, asked catalog.Asked, req api.ActionRequest) (api.Row, api.ActionState, error) {
	name, basis := asked.Name, req.Basis
	row, err := s.rowAsked(ctx, u, asked)
	if err != nil {
		return row, api.ActionState{}, err
	}
	st, ok := offerOf(row, name)
	switch {
	case basis != "" && (!ok || st.Basis != basis):
		c := conflict{msg: "the row changed: " + changedWhy(name, basis, st, ok)}
		if ok && st.Reason != "" {
			c.refusal = st.Refusal
		}
		return row, st, c
	case !ok && name == "remove" && req.Force && workspaceID(row) == "":
		return row, st, nil
	case !ok:
		return row, st, conflict{NotOffered(name, u), api.RefusalNotOffered}
	case !st.Enabled:
		return row, st, conflict{st.Reason, st.Refusal}
	}
	return row, st, nil
}

// changedWhy words what the row offers now in place of the basis the
// client saw.
func changedWhy(name, basis string, st api.ActionState, ok bool) string {
	switch {
	case ok && st.Reason != "":
		return st.Reason
	case ok && strings.HasPrefix(st.Basis, catalog.BasisPending):
		return "a round is pending now"
	case ok && strings.HasPrefix(st.Basis, catalog.BasisHeld):
		return strings.TrimPrefix(st.Basis, catalog.BasisHeld)
	case strings.HasPrefix(basis, catalog.BasisPending):
		return "the pending round already went out"
	case ok:
		return "it now rests on " + st.Basis
	}
	return "it no longer offers " + name
}

// rememberForceable keeps a remove refusal that force gets past, so the
// row offers remove_force, and forgets it once a remove goes through.
func (s *Server) rememberForceable(u unit.Unit, err error) {
	var refused Refusal
	switch {
	case errors.As(err, &refused) && refused.Forceable:
		if prev, ok := s.forceable.Swap(forceableKey(u), refused.Reason); !ok || prev != refused.Reason {
			s.note(port.TopicRefusals, u)
		}
	case err == nil:
		if _, ok := s.forceable.LoadAndDelete(forceableKey(u)); ok {
			s.note(port.TopicRefusals, u)
		}
	}
}

// changeIntakeOf runs change on the waiting intake of the unit.
func (s *Server) changeIntakeOf(w http.ResponseWriter, ctx context.Context, u unit.Unit, change func(s *Server, ctx context.Context, i intake.Intake) (string, error)) {
	if s.intakes == nil {
		writeError(w, http.StatusServiceUnavailable, errIntakesUnavailable.Error())
		return
	}
	open, err := s.waitingIntakes(ctx)
	if err != nil {
		writeActionError(w, err)
		return
	}
	i := intake.Intake{}
	if j := slices.IndexFunc(open[u], func(i intake.Intake) bool { return i.State.Open() && i.State != intake.Starting }); j >= 0 {
		i = open[u][j]
	}
	if i.Seq == 0 {
		writeError(w, http.StatusConflict, "no intake waits on "+u.Label())
		return
	}
	done, err := change(s, ctx, i)
	switch {
	case errors.Is(err, intake.ErrNotWaiting):
		writeError(w, http.StatusConflict, err.Error())
		return
	case err != nil:
		writeActionError(w, err)
		return
	}
	if i.Source != sourceSession {
		if again, ok, err := s.deps.Intakes.Intake(ctx, i.Seq); err == nil && ok && again.State.Open() {
			s.drive(i.Seq)
		}
	}
	writeJSON(w, api.ActionResult{OK: true, Output: []string{done}})
}

// runPending sends the pending round of the pull request as the user's approval;
// one that went out since the row offered it is a conflict, never a held run.
func (s *Server) runPending(ctx context.Context, u unit.Unit, req api.ActionRequest) (string, error) {
	if s.deps.Plan != nil {
		a := workflow.ActionApply
		if strings.TrimSpace(req.Guidance) != "" {
			a = workflow.ActionGuide
		}
		return s.actOnRound(ctx, u, a, req.Guidance)
	}
	return "", conflict{msg: "the row changed: the pending round already went out"}
}

// writeActionError answers an action's error with its status.
func writeActionError(w http.ResponseWriter, err error) {
	var bad badRequest
	var missing notFound
	var refused Refusal
	var tooMany tooManyRequests
	var gateway badGateway
	var turnRefused turns.Refusal
	var invalid turns.InvalidAnswer
	var joinRefused conflict
	var down unavailable
	switch {
	case errors.As(err, &down):
		writeError(w, http.StatusServiceUnavailable, down.msg)
	case errors.As(err, &joinRefused):
		writeConflict(w, joinRefused)
	case errors.As(err, &invalid):
		writeError(w, http.StatusBadRequest, invalid.Reason)
	case errors.Is(err, turn.ErrNoQuestion):
		writeError(w, http.StatusConflict, err.Error())
	case errors.As(err, &turnRefused):
		writeRefusal(w, Refusal{Reason: turnRefused.Reason})
	case errors.As(err, &bad):
		writeError(w, http.StatusBadRequest, bad.msg)
	case errors.As(err, &tooMany):
		writeError(w, http.StatusTooManyRequests, tooMany.msg)
	case errors.As(err, &gateway):
		writeError(w, http.StatusBadGateway, gateway.msg)
	case errors.As(err, &refused):
		writeRefusal(w, refused)
	case errors.As(err, &missing):
		writeError(w, http.StatusNotFound, missing.msg)
	case errors.Is(err, errActionsUnavailable), errors.Is(err, errTurnsUnavailable), errors.Is(err, errPlanUnavailable), errors.Is(err, turns.ErrNoIntents):
		writeError(w, http.StatusServiceUnavailable, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

func (s *Server) logDetail(action string, u unit.Unit, r Refusal) {
	if r.Detail != "" {
		s.logf("%s %s: %s: %s", action, u.Label(), r.Reason, r.Detail)
	}
}

func lines(s string) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// checkActionInputs refuses inputs on a built-in action, and checks those
// of an intent against its definition: only the command line sends any.
func (s *Server) checkActionInputs(l *intentload.Loaded, name string, req *api.ActionRequest) error {
	if len(req.Inputs) == 0 {
		return nil
	}
	if isBuiltin(name) {
		return badRequest{name + " takes no inputs"}
	}
	d, err := intentDef(l, name)
	if err != nil {
		return err
	}
	surface := ""
	if req.Source == sourceCLI {
		surface = catalog.SurfaceCLI
	}
	req.Inputs, err = CheckInputs(l.Registry, d, req.Inputs, surface, req.Source, false)
	return err
}

func (s *Server) act(ctx context.Context, name string, u unit.Unit, req api.ActionRequest, out io.Writer) error {
	_, isDelegated := delegatedActions[name]
	if !isBuiltin(name) {
		return s.actIntent(ctx, name, u, req, out)
	}
	if isDelegated && s.deps.Actions == nil {
		return errActionsUnavailable
	}
	if act, ok := turnActions[name]; ok {
		if s.deps.Turns == nil {
			return errTurnsUnavailable
		}
		return act(s, ctx, u, req, out)
	}
	if roundActions[name] {
		if s.deps.Plan == nil {
			return errPlanUnavailable
		}
		line, err := s.actOnRound(ctx, u, workflow.Action(name), req.Guidance)
		if err == nil && line != "" {
			fmt.Fprintln(out, line)
		}
		return err
	}
	if sourceFollowed(name, u, s.deps.Follows) {
		return s.sourceFollow(ctx, name, u)
	}
	if _, ok := overrideActions[name]; ok {
		_, err := s.applyOverride(ctx, name, u, req)
		return err
	}
	d := delegatedActions[name]
	if d.loop && !LoopHeld(ctx) {
		ctx = s.readAhead(ctx, u, d.full)
		s.deps.LoopMu.Lock()
		defer s.deps.LoopMu.Unlock()
	}
	if d.event == dinv.KindRoundSent {
		if err := s.dropPending(ctx, u, askedByYou); err != nil {
			return err
		}
	}
	call := Call{Guidance: req.Guidance, AgentHome: req.AgentHome, Out: out}
	if d.unlink {
		pr, _, err := s.deps.PRs.PR(ctx, u)
		if err != nil {
			return err
		}
		call.WorkspaceID = pr.Workspace.ID
		call.Force = req.Force || d.force
	}
	if err := d.call(s.deps.Actions, ctx, u, call); err != nil {
		return err
	}
	if d.unlink {
		if err := s.deps.PRs.SetWorkspace(ctx, u, dinv.Workspace{}); err != nil {
			return err
		}
	}
	if d.mark != nil {
		_, err := s.updateOverride(ctx, u, func(o *dinv.Override) error { d.mark(o); return nil })
		if err != nil {
			return err
		}
	}
	if d.event == "" {
		return nil
	}
	return s.recordEvent(ctx, u, d.event, d.data)
}

// actIntent runs a round of an intent as its manifest declares it: under
// the loop, the pending round dropped first when its effects say so, its
// prepare run, its guidance cut at guidance.max, and the events its effects
// name recorded after it.
func (s *Server) actIntent(ctx context.Context, name string, u unit.Unit, req api.ActionRequest, out io.Writer) error {
	d, err := intentDef(s.loaded(), name)
	if err != nil {
		return err
	}
	if s.deps.Actions == nil {
		return errActionsUnavailable
	}
	inputs, err := d.NormalizeAll(req.Inputs)
	if err != nil {
		return badRequest{err.Error()}
	}
	call := Call{Guidance: cutGuidance(d, req.Guidance), AgentHome: req.AgentHome, Out: out, Inputs: inputs, Items: req.Items, Task: req.Task,
		Manager: req.Manager, Parent: req.Parent}
	if a, ok := s.deps.Actions.(AheadPreparer); ok && !LoopHeld(ctx) {
		if err := a.PrepareAhead(ctx, name, u, call); err != nil {
			return err
		}
	}
	if !LoopHeld(ctx) {
		ctx = s.readAhead(ctx, u, false)
		s.deps.LoopMu.Lock()
		defer s.deps.LoopMu.Unlock()
	}
	effects := actionEffectsOf(d)
	for _, e := range effects {
		if e.before != nil {
			if err := e.before(s, ctx, u); err != nil {
				return err
			}
		}
	}
	if d.Prepare != "" {
		prepare, ok := prepares[d.Prepare]
		if !ok {
			return fmt.Errorf("%s: the daemon has no prepare %s", name, d.Prepare)
		}
		if err := prepare(s, ctx, u, &call); err != nil {
			return err
		}
	}
	if err := s.deps.Actions.Intent(ctx, name, u, call); err != nil {
		return err
	}
	for _, e := range effects {
		if e.after != nil {
			if err := e.after(s, ctx, u); err != nil {
				return err
			}
		}
	}
	return nil
}

// applyOverride runs an override action under overrideMu and returns the
// unit it applied to, for track the name GitHub gives the pull request.
func (s *Server) applyOverride(ctx context.Context, name string, u unit.Unit, req api.ActionRequest) (unit.Unit, error) {
	apply := overrideActions[name]
	var read port.TrackRead
	if name == "track" {
		var err error
		if read, err = s.readForTrack(ctx, u); err != nil {
			return u, err
		}
		u = read.Unit
	}
	s.overrideMu.Lock()
	defer s.overrideMu.Unlock()
	switch name {
	case "track":
		var err error
		if u, err = s.admitTrack(ctx, u, read); err != nil {
			return u, err
		}
	case "untrack":
		known, tracked, err := s.trackedAs(ctx, u)
		if err != nil {
			return u, err
		}
		if !tracked {
			return u, badRequest{u.Label() + " is not tracked"}
		}
		u = known
	}
	var kind, data string
	changed, err := s.updateOverrideLocked(ctx, u, func(o *dinv.Override) (err error) {
		kind, data, err = apply(o, req, s.deps.Now(), func() (api.Kind, string, error) { return s.unmutedSituation(ctx, u) })
		return err
	})
	if err != nil || (!changed && (name == "track" || name == "untrack")) {
		return u, err
	}
	if err := s.recordEvent(ctx, u, kind, data); err != nil {
		return u, err
	}
	if name == "pause" {
		if err := s.dropPending(ctx, u, "paused by you"); err != nil {
			s.logf("%s: pause: dropping the pending round: %v", u.Label(), err)
		}
	}
	return u, nil
}

// trackMax bounds what every fast sweep reads by reference for tracking
// alone: three batches of InventoryByRef.
const trackMax = 50

// trackedAs answers the tracked unit that matches u under any case.
func (s *Server) trackedAs(ctx context.Context, u unit.Unit) (unit.Unit, bool, error) {
	tracked, err := s.deps.PRs.Tracked(ctx)
	if err != nil {
		return u, false, err
	}
	known, ok := trackedIn(s.deps.Units, tracked, u)
	return known, ok, nil
}

func trackedIn(codecs *unit.Registry, tracked []unit.Unit, u unit.Unit) (unit.Unit, bool) {
	if i := slices.IndexFunc(tracked, func(t unit.Unit) bool { return codecs.Same(t, u) }); i >= 0 {
		return tracked[i], true
	}
	return u, false
}

// trackSlot answers the tracked unit that matches u under any case, and
// refuses a new one past trackMax.
func (s *Server) trackSlot(ctx context.Context, u unit.Unit) (unit.Unit, bool, error) {
	tracked, err := s.deps.PRs.Tracked(ctx)
	if err != nil {
		return u, false, err
	}
	if known, ok := trackedIn(s.deps.Units, tracked, u); ok {
		return known, true, nil
	}
	if len(tracked) >= trackMax {
		names := make([]string, len(tracked))
		for i, t := range tracked {
			names[i] = t.Label()
		}
		return u, false, badRequest{fmt.Sprintf("already tracking %d pull requests (%s); untrack one first", len(tracked), strings.Join(names, ", "))}
	}
	return u, false, nil
}

// admitTrack checks, under overrideMu, the slot and the state of a pull
// request about to be tracked, and only then stores the read, so a refused
// track leaves no row.
func (s *Server) admitTrack(ctx context.Context, u unit.Unit, read port.TrackRead) (unit.Unit, error) {
	u, _, err := s.trackSlot(ctx, u)
	if err != nil {
		return u, err
	}
	state := read.State
	if state == "" {
		pr, ok, err := s.deps.PRs.PR(ctx, u)
		if err != nil {
			return u, err
		}
		if ok {
			state = pr.State
		}
	}
	if state != "" && state != dgh.StateOpen {
		return u, badRequest{fmt.Sprintf("%s is %s; "+brand.Name+" follows open pull requests", u.Label(), strings.ToLower(state))}
	}
	if read.Save == nil {
		return u, nil
	}
	return u, read.Save(ctx)
}

// readForTrack reads a pull request about to be tracked, so a nudge right
// after the track finds its situation, and returns its name as GitHub gives
// it. It runs without overrideMu: a slow read must not stall the cockpit.
// The caller checks the slot again under the lock.
func (s *Server) readForTrack(ctx context.Context, u unit.Unit) (port.TrackRead, error) {
	known, tracked, err := s.trackSlot(ctx, u)
	if err != nil || tracked || s.track == nil {
		return port.TrackRead{Unit: known}, err
	}
	read, err := s.track(ctx, u)
	var low *dgh.BudgetError
	switch {
	case errors.As(err, &low):
		return read, tooManyRequests{"track " + u.Label() + ": " + low.Error() + "; try again after the reset"}
	case errors.Is(err, port.ErrNotFound):
		return read, notFound{"track " + u.Label() + ": " + err.Error()}
	case errors.Is(err, port.ErrGitHub):
		return read, badGateway{"track " + u.Label() + ": " + err.Error()}
	}
	return read, err
}

// untrackFinished clears the track of pull requests a read showed merged or
// closed; from then on they are followed like any finished pull request.
func (s *Server) untrackFinished(ctx context.Context) {
	tracked, err := s.deps.PRs.Tracked(ctx)
	if err != nil {
		s.logf("untrack finished: %v", err)
		return
	}
	stored, err := s.deps.PRs.PRs(ctx, tracked)
	if err != nil {
		s.logf("untrack finished: %v", err)
		return
	}
	for _, u := range tracked {
		pr, ok := stored[u]
		if !ok || pr.State == dgh.StateOpen {
			continue
		}
		changed, err := s.updateOverride(ctx, u, func(o *dinv.Override) error { o.Tracked = false; return nil })
		if err == nil && changed {
			err = s.recordEvent(ctx, u, dinv.KindUntracked, strings.ToLower(pr.State))
		}
		if err != nil {
			s.logf("%s: untrack finished: %v", u.Label(), err)
		}
	}
}

func (s *Server) unmutedSituation(ctx context.Context, u unit.Unit) (api.Kind, string, error) {
	um, err := s.unmutedRow(ctx, u)
	return um.row.Kind, um.row.Summary.WaitsOn, err
}

// unmutedRow derives the row as it stands without the user's override and
// pending round, with the events and the deriver it came from.
func (s *Server) unmutedRow(ctx context.Context, u unit.Unit) (unmuted, error) {
	rd, ok, err := s.readRow(ctx, u)
	if err != nil {
		return unmuted{}, err
	}
	if !ok {
		st, found, err := s.storedRead(ctx, u, catalog.Asked{}, true)
		if err != nil {
			return unmuted{}, err
		}
		if !found {
			return unmuted{}, notFound{u.Label() + " is not in the inventory"}
		}
		var b turn.Book
		if st.book != nil {
			b = *st.book
		}
		return unmuted{row: st.row, evs: st.evs, book: b, d: st.d}, nil
	}
	base := rd.Base
	base.Pending, base.Asked = nil, catalog.Asked{}
	row, err := rd.Derive(base, prrow.Unmuted(rd.In))
	if err != nil {
		return unmuted{}, err
	}
	b, _ := prrow.BookOf(rd.In)
	return unmuted{row: row, evs: prrow.EventsOf(rd.In), book: b, d: s.deriverAt(base, rd.At)}, nil
}

// unmuted is a pull request's row without the user's mute, with what a
// nudge reads beside it.
type unmuted struct {
	row  api.Row
	evs  []dinv.Event
	book turn.Book
	d    prrow.Deriver
}

// updateOverride changes the stored override and reports whether it
// changed. Every writer of overrides in the daemon goes through overrideMu,
// so none writes back a field another one just changed.
func (s *Server) updateOverride(ctx context.Context, u unit.Unit, change func(o *dinv.Override) error) (bool, error) {
	s.overrideMu.Lock()
	defer s.overrideMu.Unlock()
	return s.updateOverrideLocked(ctx, u, change)
}

func (s *Server) updateOverrideLocked(ctx context.Context, u unit.Unit, change func(o *dinv.Override) error) (bool, error) {
	for range overrideAttempts {
		o, err := s.deps.Overrides.Override(ctx, u)
		if err != nil {
			return false, err
		}
		o.PR = refOf(u)
		before := o
		if err := change(&o); err != nil {
			return false, err
		}
		if o == before {
			return false, nil
		}
		written, err := s.deps.Overrides.SetOverrideIf(ctx, before, o)
		if err != nil || written {
			return written, err
		}
	}
	return false, fmt.Errorf("%s: the override kept changing while it was written", u.Label())
}

// overrideAttempts bounds the rereads of an override another writer, such
// as a take-over from a session, changed between read and write.
const overrideAttempts = 5

func (s *Server) recordEvent(ctx context.Context, u unit.Unit, kind, data string) error {
	return s.deps.Events.RecordEvent(ctx, dinv.Event{PR: refOf(u), Kind: kind, At: s.deps.Now(), Data: data})
}

// prepareNudge refuses when nobody but the user is to be asked, or the
// same wait was already nudged, and fills who, the situation and whether
// it asks the maintainers.
func (s *Server) prepareNudge(ctx context.Context, u unit.Unit, c *Call) error {
	um, err := s.unmutedRow(ctx, u)
	if err != nil {
		return err
	}
	r, d := um.row, um.d
	p, _ := prrow.PayloadOf(r)
	who, why := prrow.NudgeTargets(r, p.Author, d.Viewer, d.Ignore)
	if refusal := prrow.NudgeRefusal(refOf(u), who, why, r.Summary.Since, um.evs, prrow.Unstarted(um.book), d.Now); refusal != "" {
		return badRequest{refusal}
	}
	if c.Inputs == nil {
		c.Inputs = map[string]any{}
	}
	c.Inputs["situation"] = nudgeSituation(who, r)
	c.Inputs["waits_on_maintainers"] = slices.Equal(who, []string{prrow.NudgeMaintainers})
	c.Inputs["who"] = slices.Clone(who)
	return nil
}

func nudgeSituation(who []string, r api.Row) string {
	since := ""
	if !r.Summary.Since.IsZero() {
		since = " since " + r.Summary.Since.Local().Format(time.RFC3339)
	}
	// The kind's label, not the reason: a reason can carry CI job names.
	return fmt.Sprintf("Waiting on: %s%s, %s.", strings.Join(who, ", "), since, situation.Kind(r.Kind).Label())
}

// refreshBrief drops a pull request's brief and runs the brief job, which
// makes it again first among those due.
func (s *Server) refreshBrief(w http.ResponseWriter, r *http.Request, u unit.Unit) {
	if s.deps.Brief == nil {
		writeError(w, http.StatusConflict, "the brief is off; set brief.enabled in the config")
		return
	}
	if err := s.deps.Brief.Forget(r.Context(), refOf(u)); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.RunJob("brief")
	writeJSON(w, api.ActionResult{OK: true, Output: []string{"brief of " + u.Label() + " requested"}})
}

// workspaceID is the workspace of a GitHub row, "" for none or another
// provider's row.
func workspaceID(r api.Row) string {
	p, _ := prrow.PayloadOf(r)
	return p.Workspace.ID
}
