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

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/catalog"
	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/intake"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/turns"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/intent/builtin"
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

// BlockReader is Actions that names the start block holding a unit's round:
// the subject of the trust gate it waits on.
type BlockReader interface {
	StartBlockKey(ctx context.Context, u unit.Unit) (key string, ok bool, err error)
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
	// WorkspaceID is the workspace remove acts on; "" is the one its unit
	// is linked to.
	WorkspaceID string
	// Force lets remove past the workspace manager's terminal check.
	Force bool
	// Inputs are the intent's inputs, normalized to their types.
	Inputs map[string]any
	// Items are the unit's item ids and Task the task task the action was asked from; the turn's
	// drafts are tied to them.
	Items []string
	Task  string
	// Manager and Parent are the request's: the workspace manager of the
	// worktree the turn creates and the caller's worktree.
	Manager, Parent string
	// Block is the start block a trust gate names, which TrustAndRun and
	// Unblock refuse to act past once another block replaced it; "" acts on
	// the unit's block.
	Block string
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
	errStoreUnavailable   = unavailable{"the daemon has no store"}
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

type delegated struct {
	call  func(a Actions, ctx context.Context, u unit.Unit, c Call) error
	loop  bool   // writes to the loop, so waits on its mutex
	event string // "" records nothing
	data  string
	// remove removes the unit's workspace, which unlinks it.
	remove bool
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
		if !s.forge(u) || !queued(books[u]) {
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
	"take_over":    {loop: true, event: builtin.EventAdopted, call: Actions.Adopt},
	"remove":       {loop: true, event: builtin.EventRemoved, remove: true, call: Actions.Remove},
	"remove_force": {loop: true, event: builtin.EventRemoved, remove: true, force: true, call: Actions.Remove},
}

// holdActions set or clear the user's hold of a unit's rounds, with the
// event each records.
var holdActions = map[string]struct {
	hold  bool
	event string
}{
	"pause":  {true, builtin.EventHeld},
	"resume": {false, builtin.EventReleased},
}

// followActions go to the unit's source, which keeps its follows.
var followActions = map[string]bool{"track": true, "untrack": true}

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

var answerSources = []turn.Source{turn.Cockpit, turn.Notification, turn.TUI, turn.Terminal}

// isBuiltin tells a built-in action the daemon runs itself, an intent of
// a row gate of l among them; any other action a unit's row offers that no
// intent names is its plugin's.
func isBuiltin(l *intentload.Loaded, name string) bool {
	_, isHold := holdActions[name]
	_, isDelegated := delegatedActions[name]
	_, isIntake := intakeRouteActions[name]
	_, isGate := catalog.RowGateIntent(l, name)
	return isHold || isDelegated || followActions[name] || isIntake || isGate || name == "brief_refresh"
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
	actor := task.ActorPerson
	if _, ok := catalog.RowGateIntent(l, name); ok {
		if actor, ok = requestActor(w, r); !ok {
			return
		}
		if actor != task.ActorPerson {
			writeError(w, http.StatusForbidden, name+" resolves a gate as a person; an agent's session does not")
			return
		}
	}
	plugin := !isBuiltin(l, name) && !isIntent(l, name)
	if plugin && !s.pluginActs(r.Context(), u, name) {
		writeError(w, http.StatusNotFound, "no action "+strconv.Quote(name))
		return
	}
	if !plugin {
		if err := unitAction(l, name, u, s.forge(u), s.deps.Follows); err != nil {
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
	if g, ok := questionIntent(l, name); ok {
		var out bytes.Buffer
		if err := s.actOnGate(ctx, g, u, req, actor, &out); err != nil {
			writeActionError(w, err)
			return
		}
		writeJSON(w, api.ActionResult{OK: true, Output: lines(out.String())})
		return
	}
	row := api.Row{Unit: u}
	if plugin || s.forge(u) {
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
		if g, ok := catalog.RowGateIntent(l, name); ok && (!catalog.RunsHeld(l, name) || strings.HasPrefix(st.Basis, catalog.BasisPending)) {
			var out bytes.Buffer
			if err := s.actOnGate(ctx, g, u, req, actor, &out); err != nil {
				writeActionError(w, err)
				return
			}
			writeJSON(w, api.ActionResult{OK: true, Output: lines(out.String())})
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
	if d := delegatedActions[name]; d.remove {
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
// conflict. A forced remove on a row that does not offer remove is the
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
	case !ok && name == "remove" && req.Force:
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
	case errors.Is(err, gates.ErrRefused), errors.Is(err, gates.ErrIntent), errors.Is(err, gates.ErrNoGate), errors.Is(err, gates.ErrResolved):
		writeGateError(w, err)
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
	if isBuiltin(l, name) {
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
	l := s.loaded()
	_, isDelegated := delegatedActions[name]
	if !isBuiltin(l, name) {
		if isIntent(l, name) {
			return s.actIntent(ctx, name, u, req, out)
		}
		return s.pluginAction(ctx, name, u, req, out)
	}
	if isDelegated && s.deps.Actions == nil {
		return errActionsUnavailable
	}
	if g, ok := catalog.RowGateIntent(l, name); ok {
		if catalog.RunsHeld(l, name) {
			if s.deps.Actions == nil {
				return errActionsUnavailable
			}
			return s.delegate(ctx, runHeld, u, Call{Guidance: req.Guidance, AgentHome: req.AgentHome, Out: out})
		}
		return s.actOnGate(ctx, g, u, req, task.ActorPerson, out)
	}
	if followActions[name] {
		if !sourceFollowed(name, u, s.deps.Follows) {
			return notFound{name + " " + u.Label() + ": no source follows it"}
		}
		return s.sourceFollow(ctx, name, u)
	}
	if _, ok := holdActions[name]; ok {
		return s.applyHold(ctx, name, u)
	}
	d := delegatedActions[name]
	call := Call{Guidance: req.Guidance, AgentHome: req.AgentHome, Out: out}
	if d.remove {
		call.Force = req.Force || d.force
	}
	return s.delegate(ctx, d, u, call)
}

// delegate runs d on u with call: under the loop when it writes to it, the
// pending round dropped first when it sends one, its event recorded after.
func (s *Server) delegate(ctx context.Context, d delegated, u unit.Unit, call Call) error {
	if d.loop && !LoopHeld(ctx) {
		ctx = s.readAhead(ctx, u, d.full)
		s.deps.LoopMu.Lock()
		defer s.deps.LoopMu.Unlock()
		ctx = withLoopHeld(ctx)
	}
	if d.event == builtin.EventRoundSent {
		if err := s.dropPending(ctx, u, askedByYou); err != nil {
			return err
		}
	}
	if err := d.call(s.deps.Actions, ctx, u, call); err != nil {
		return err
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
		if err := s.prepare(ctx, d, u, &call); err != nil {
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

// applyHold sets or clears the user's hold of u's rounds, records it, and
// drops the pending round a pause holds.
func (s *Server) applyHold(ctx context.Context, name string, u unit.Unit) error {
	if s.deps.Overrides == nil {
		return errStoreUnavailable
	}
	a := holdActions[name]
	hold := ""
	if a.hold {
		hold = port.OverrideTrue
	}
	if _, err := s.deps.Overrides.SetOverride(ctx, u, map[string]string{port.OverrideHold: hold}, nil); err != nil {
		return err
	}
	if err := s.recordEvent(ctx, u, a.event, ""); err != nil {
		return err
	}
	if a.hold {
		if err := s.dropPending(ctx, u, "paused by you"); err != nil {
			s.logf("%s: pause: dropping the pending round: %v", u.Label(), err)
		}
	}
	return nil
}

// unmutedRow derives u's row as it stands without the user's mute and
// pending round.
func (s *Server) unmutedRow(ctx context.Context, u unit.Unit) (api.Row, error) {
	rd, ok, err := s.readRow(ctx, u)
	if err != nil {
		return api.Row{}, err
	}
	if !ok {
		row, found, err := s.pluginRow(ctx, api.UnitRowQuery{Unit: u, Unmuted: true, Kept: true})
		if err == nil && !found {
			err = notFound{u.Label() + " is not in the inventory"}
		}
		return row, err
	}
	base := rd.Base
	base.Pending, base.Asked = nil, catalog.Asked{}
	return rd.Derive(base, rd.Unmuted())
}

func (s *Server) recordEvent(ctx context.Context, u unit.Unit, kind, data string) error {
	return s.deps.Events.RecordEvent(ctx, port.Event{Unit: u, Kind: kind, At: s.deps.Now(), Data: data})
}

// refreshBrief drops a unit's brief and runs the brief job, which
// makes it again first among those due.
func (s *Server) refreshBrief(w http.ResponseWriter, r *http.Request, u unit.Unit) {
	if s.deps.Brief == nil {
		writeError(w, http.StatusConflict, "the brief is off; set brief.enabled in the config")
		return
	}
	if err := s.deps.Brief.Forget(r.Context(), u); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.RunJob("brief")
	writeJSON(w, api.ActionResult{OK: true, Output: []string{"brief of " + u.Label() + " requested"}})
}
