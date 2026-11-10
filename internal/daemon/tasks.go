package daemon

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/api/lens"
	"github.com/wloophq/wloop/internal/catalog"
	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/messages"
	"github.com/wloophq/wloop/internal/core/port"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/core/timeline"
	"github.com/wloophq/wloop/internal/domain/draft"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/keys"
	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/readmodel"
)

const (
	tasksBodyMax = 64 << 10
	// The Sync worker folds a burst of notes as the row feed does, and wakes each minute for the snoozes and
	// stars that come due with no note.
	syncQuiet    = 250 * time.Millisecond
	syncMaxDelay = time.Second
	syncTick     = time.Minute
)

// syncInputs are the notes whose units Sync reconciles.
const syncInputs = port.TopicItems | port.TopicSourceEvents | port.TopicCards | port.TopicGates

// taskInputs are the notes after which a change carries the counters: a view reads the tasks, their gates,
// their wakes, their units' records, their links and the profile's facets.
const taskInputs = port.TopicTasks | port.TopicGates | port.TopicWakes | port.TopicDrafts | port.TopicRecords | port.TopicLinks |
	port.TopicPlacements | port.TopicConfig

// planInputs are the notes after which a change says the plan moved: its items as served, the pending items, the
// closed sessions it lists, and a reloaded profile, whose words and actions it shows.
const planInputs = port.TopicPlan | port.TopicPending | port.TopicConfig

// gateInputs are the notes after which a change says the gates moved: a gate, and what one shows of its subject,
// a draft and its items, a message (whose writes note TopicTasks) or a pending plan item.
const gateInputs = port.TopicGates | port.TopicDrafts | port.TopicItems | port.TopicTasks | port.TopicPlan | port.TopicPending

const flagInputs = planInputs | gateInputs

// bridged are the handlers whose verbs a task with units runs as its unit's action when no handler takes them.
var bridged = []string{handlerApprove, handlerDelegate, handlerDrop}

// handlerOf is the handler the profile gives verb v.
func (s *Server) handlerOf(v task.Verb) string { return s.loaded().Tasks.Handler(string(v)) }

// aboutUnits says whether e is about its units' conversation: a stored task, not a project.
func aboutUnits(e task.Task) bool {
	return !e.IsProject() && e.Origin != task.OriginDraft && e.Origin != task.OriginMessage
}

// bridges says whether verb on e runs as its first unit's action, a handler aside: every verb of a bridged
// handler on a task its unit's items made, and the delegate verb on any other task about its units.
func (s *Server) bridges(e task.Task, verb task.Verb) bool {
	h := s.handlerOf(verb)
	if len(e.Units) == 0 || !slices.Contains(bridged, h) {
		return false
	}
	return e.Origin == task.OriginDerived || h == handlerDelegate && aboutUnits(e)
}

var errTasksUnavailable = errors.New("the tasks are not served: the daemon runs without a store")

func verbKey(v task.Verb) string {
	for _, b := range keys.Core {
		if b.Context == keys.List && b.Action == string(v) {
			return b.Key
		}
	}
	return ""
}

func (s *Server) tasks(w http.ResponseWriter) (*coretasks.Service, bool) {
	if s.deps.Tasks == nil {
		writeError(w, http.StatusServiceUnavailable, errTasksUnavailable.Error())
		return nil, false
	}
	return s.deps.Tasks, true
}

// writeTasksError answers a tasks service error with its status.
func writeTasksError(w http.ResponseWriter, err error) {
	var disabled pluginOff
	switch {
	case errors.As(err, &disabled):
		writePluginDisabled(w, disabled.provider)
	case errors.Is(err, coretasks.ErrArgs):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, coretasks.ErrNoTask), errors.Is(err, messages.ErrNoMessage):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, coretasks.ErrForbidden):
		writeError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, coretasks.ErrVerb), errors.Is(err, coretasks.ErrMerged), errors.Is(err, coretasks.ErrPluginDisabled), errors.Is(err, messages.ErrClosed), errors.Is(err, turn.ErrClosed),
		errors.Is(err, messages.ErrNotApplied):
		writeError(w, http.StatusConflict, err.Error())
	default:
		writeActionError(w, err)
	}
}

func (s *Server) handleViews(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.tasks(w); !ok {
		return
	}
	writeJSON(w, s.viewsCatalog())
}

// viewsCatalog is what GET /api/views serves, with its version, built once per loaded profile.
func (s *Server) viewsCatalog() api.ViewsCatalog {
	l := s.loaded()
	b := &s.viewsBuilt
	b.Lock()
	defer b.Unlock()
	if b.loaded != l {
		b.loaded, b.cat = l, s.buildViewsCatalog(l)
	}
	return b.cat
}

func (s *Server) buildViewsCatalog(l *intentload.Loaded) api.ViewsCatalog {
	cat := api.ViewsCatalog{Areas: nonNil(slices.Clone(s.deps.Cfg.Tasks.Areas)), Contexts: nonNil(slices.Clone(s.deps.Cfg.Tasks.Contexts)), Views: s.servedViews(),
		Review: []string{}, States: []api.StateSpec{}, Props: []api.PropSpec{}}
	for _, st := range l.Views.Review {
		cat.Review = append(cat.Review, st.View)
	}
	for _, st := range l.Tasks.States {
		cat.States = append(cat.States, api.StateSpec{Name: st.Name, Label: cmp.Or(st.Label, st.Name), Category: string(st.Category)})
	}
	for _, p := range l.Tasks.Props {
		ps := api.PropSpec{Name: p.Name, Type: string(p.Type), Values: slices.Clone(p.Values), Role: string(p.Role)}
		for _, w := range p.Writers {
			ps.Writers = append(ps.Writers, string(w))
		}
		cat.Props = append(cat.Props, ps)
	}
	for _, v := range s.verbOrder() {
		t, b := l.Tasks.Verbs[v], bundledWords().tasks.Verbs[v]
		cat.Verbs = append(cat.Verbs, api.VerbSpec{Verb: v, Handler: l.Tasks.Handler(v), Label: cmp.Or(t.Label, b.Label, v), Summary: cmp.Or(t.Summary, b.Summary),
			Input: cmp.Or(t.Input, b.Input), Send: s.replySend(v), Pick: builtin.TaskPicks[l.Tasks.Handler(v)], Key: verbKey(task.Verb(v)), From: slices.Clone(t.From),
			To: t.To})
	}
	cat.Facets, cat.DefaultSort, cat.Lens = catalog.TaskFacets(l.Views.Facets, l.Tasks, cat.Contexts), l.Views.DefaultSort, catalog.LensWords(l.Views.Lens)
	cat.DetailVerbs, cat.Effects = nonNil(slices.Clone(l.Tasks.DetailVerbs)), maps.Clone(l.Gates.Effects)
	if cat.Effects == nil {
		cat.Effects = map[string]string{}
	}
	cat.DraftPurposes, cat.DraftStates = nonNilMap(l.Gates.Drafts.Purposes), nonNilMap(l.Gates.Drafts.States)
	cat.RunStates, cat.SessionStates = nonNilMap(l.Tasks.RunStates), nonNilMap(l.Tasks.SessionStates)
	cat.Version = api.VersionOf(cat)
	return cat
}

// servedViews are the profile's views, in its order.
func (s *Server) servedViews() []api.TaskViewSpec {
	def := s.loaded().Views
	out := []api.TaskViewSpec{}
	for _, v := range def.Views {
		out = append(out, api.TaskViewSpec{ID: v.ID, Label: v.Label, Summary: v.Summary, Empty: v.Empty, When: v.WhenText, Sort: v.SortText, Over: v.Over, Group: v.Group, Key: v.Key, Badge: def.Badged(v.ID), HideDisabled: v.HideDisabled,
			RemoveClean: v.RemoveClean, Rail: v.Rail, Layout: v.Layout})
	}
	return out
}

func (s *Server) handleTasks(w http.ResponseWriter, r *http.Request) {
	g, ok := s.tasks(w)
	if !ok {
		return
	}
	q := r.URL.Query()
	if q.Has(api.TasksMatch) {
		s.handleMatch(w, r, g, q.Get(api.TasksMatch), task.ID(q.Get(api.TasksExcept)))
		return
	}
	view := task.View(q.Get("view"))
	since, err := api.ParseSince(q.Get("since"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	query := task.Query{View: view, Area: q.Get("area")}
	for _, c := range strings.Split(q.Get("context"), ",") {
		if c = strings.TrimSpace(c); c != "" {
			query.Contexts = append(query.Contexts, c)
		}
	}
	units, err := api.ParseUnits(q[api.TasksUnits])
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx := r.Context()
	p, err := g.TasksSince(ctx, query, coretasks.Since{Epoch: since.Epoch, Rev: since.Rev}, units)
	if err != nil {
		writeTasksError(w, err)
		return
	}
	page := api.TasksPage{Epoch: p.Epoch, Rev: p.Rev, Full: p.Full, Tasks: []api.TaskView{}, Removed: []string{}, Counters: wireCounters(p.Counters),
		Loading: s.loading(), Specs: s.viewsCatalog().Version}
	if p.Pass != nil {
		ctx = coretasks.WithPass(ctx, p.Pass)
	}
	vs := s.taskViews(ctx, g, false)
	vs.readRows(ctx, p.Tasks)
	for _, e := range p.Tasks {
		v, err := vs.view(ctx, e)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		page.Tasks = append(page.Tasks, v)
	}
	for _, id := range p.Removed {
		page.Removed = append(page.Removed, string(id))
	}
	gs, err := s.gatesIn(ctx, string(view))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	listed := map[task.ID]bool{}
	for _, e := range p.Tasks {
		listed[e.ID] = true
	}
	for _, g := range gs {
		e, ok, err := s.gateTask(ctx, g)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if ok && listed[e.ID] {
			page.Gates = append(page.Gates, s.gateView(whereBy(ctx, vs.whereBy), g.gate, vs.unitItems()))
		}
	}
	writeJSON(w, page)
}

func (s *Server) handleTask(w http.ResponseWriter, r *http.Request) {
	g, ok := s.tasks(w)
	if !ok {
		return
	}
	ctx := r.Context()
	e, err := g.Task(ctx, task.ID(r.PathValue("id")))
	if err != nil {
		writeTasksError(w, err)
		return
	}
	v, err := s.taskViews(ctx, g, true).view(ctx, e)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, v)
}

func (s *Server) handleUnitTasks(w http.ResponseWriter, r *http.Request) {
	u, ok := s.parseUnit(r)
	if !ok {
		writeNoUnit(w, r)
		return
	}
	g := s.deps.Tasks
	out := []api.TaskView{}
	if g == nil {
		writeJSON(w, out)
		return
	}
	ctx := r.Context()
	es, err := g.OpenTasksOf(ctx, u)
	if err != nil {
		writeTasksError(w, err)
		return
	}
	vs := s.taskViews(ctx, g, false)
	for _, e := range es {
		v, err := vs.view(ctx, e)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		out = append(out, v)
	}
	writeJSON(w, out)
}

func (s *Server) handleTaskVerb(w http.ResponseWriter, r *http.Request) {
	var req api.TaskRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, tasksBodyMax)).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	s.runTaskVerb(w, r, r.PathValue("verb"), req)
}

func (s *Server) runTaskVerb(w http.ResponseWriter, r *http.Request, verb string, req api.TaskRequest) {
	g, ok := s.tasks(w)
	if !ok {
		return
	}
	if names := s.verbOrder(); !slices.Contains(names, verb) {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("unknown verb %q; one of %s", verb, strings.Join(names, ", ")))
		return
	}
	q, ok := s.forUser(w, r, verb)
	if !ok {
		return
	}
	ctx := context.WithoutCancel(r.Context())
	id := task.ID(r.PathValue("id"))
	e, err := g.Task(ctx, id)
	if err != nil {
		writeTasksError(w, err)
		return
	}
	v := task.Verb(verb)
	creq, err := g.Addressed(ctx, id, e, q.task(coretasks.Request{Verb: v, Args: req.Args, Source: req.Source}))
	if errors.Is(err, coretasks.ErrMerged) {
		writeError(w, http.StatusConflict, s.notice(builtin.NoticeTaskMerged, map[string]any{"Task": string(id), "Into": string(e.ID), "Verb": verb}))
		return
	}
	if err != nil {
		writeTasksError(w, err)
		return
	}
	req.Args = creq.Args
	if !offers(s.loaded().Tasks.Verbs, e, v) {
		writeError(w, http.StatusConflict, fmt.Sprintf("%s is not offered on this task: the profile's tasks.toml says where it is", v))
		return
	}
	for _, u := range e.Units {
		if s.handlerOf(v) != handlerDrop && s.refusePlugin(w, u) {
			return
		}
	}
	s.doTaskVerb(w, r, g, e, q.task(coretasks.Request{Verb: v, Args: req.Args, Source: req.Source}), req)
}

// gateVerbRoles are the roles of the gate intents a verb of each handler resolves a task's open gate with.
var gateVerbRoles = map[string]builtin.GateRole{"approve": builtin.GateApproves, "answer": builtin.GateTakesText}

// doTaskVerb runs creq on e: a verb whose handler approves or answers resolves the task's open gate, one a
// handler takes runs it, a reply or a bridged verb runs on the task's unit.
func (s *Server) doTaskVerb(w http.ResponseWriter, r *http.Request, g *coretasks.Service, e task.Task, creq coretasks.Request, req api.TaskRequest) {
	ctx, v := context.WithoutCancel(r.Context()), creq.Verb
	if role, ok := gateVerbRoles[s.loaded().Tasks.Handler(string(v))]; ok && creq.Approved == 0 && !g.Handles(e, v) {
		if gated, err := g.Allowed(ctx, e, creq); err != nil || gated {
			res, err := g.Do(ctx, e.ID, creq)
			s.writeTaskResult(ctx, w, g, res, err)
			return
		}
		var out strings.Builder
		resolved, err := s.resolveTaskGate(ctx, e, role, creq, &out)
		if err != nil {
			writeTasksError(w, err)
			return
		}
		if resolved {
			s.recordVerb(ctx, g, e, creq)
			now, err := g.Task(ctx, e.ID)
			if err != nil {
				now = e
			}
			s.writeTaskResult(ctx, w, g, coretasks.Result{Task: now, Lines: lines(out.String())}, nil)
			return
		}
	}
	reply := s.replies(v) && !g.Handles(e, v)
	b, bridged := s.bridge(ctx, g, e, v)
	if reply || bridged {
		gated, err := g.Allowed(ctx, e, creq)
		if err != nil || gated {
			res, err := g.Do(ctx, e.ID, creq)
			s.writeTaskResult(ctx, w, g, res, err)
			return
		}
	}
	if reply {
		s.runReply(ctx, w, g, e, v, req, requestOf(creq))
		return
	}
	if bridged {
		st, ok := b.runs()
		if !ok {
			writeError(w, http.StatusConflict, b.reason())
			return
		}
		s.runBridge(w, r, g, e, v, b.unit, st, req, requestOf(creq))
		return
	}
	res, err := g.Do(ctx, e.ID, creq)
	s.writeTaskResult(ctx, w, g, res, err)
}

// requester is who a request comes from: the person, or an agent through Session, the stable id of the session
// that holds the request's handle. Driven says the user drives that session, so the agent acts for them.
type requester struct {
	Actor   task.Actor
	Session string
	Driven  bool
}

var personRequest = requester{Actor: task.ActorPerson}

// user says whether the request carries the user's authority: the person's, or an agent's through a session they
// drive.
func (q requester) user() bool {
	return q.Actor == task.ActorPerson || q.Actor == task.ActorAgent && q.Driven
}

// task is q as a task verb's request: the session only when the user drives it.
func (q requester) task(r coretasks.Request) coretasks.Request {
	r.Actor = q.Actor
	if q.Driven {
		r.Session = q.Session
	}
	return r
}

// requestOf is who ran r, a task verb's request the daemon checked.
func requestOf(r coretasks.Request) requester {
	q := requester{Actor: cmp.Or(r.Actor, task.ActorPerson), Session: r.Session}
	q.Driven = q.Actor == task.ActorAgent && q.Session != ""
	return q
}

// requester is who r comes from: the person with no session header, else an agent through the session that
// holds the header's handle, and whether the user drives it; an agent through no session when no session holds
// the handle and the request names no manager to make it one. A header that is not a handle is refused on w.
func (s *Server) requester(w http.ResponseWriter, r *http.Request) (requester, bool) {
	h, ok := callerHandle(w, r, false)
	switch {
	case !ok:
		return requester{}, false
	case h == "":
		return personRequest, true
	case s.deps.Turns == nil:
		return requester{Actor: task.ActorAgent}, true
	}
	id, known := "", true
	if r.Header.Get(api.ManagerHeader) == "" {
		var err error
		if id, known, err = s.deps.Turns.SessionOfHandle(r.Context(), h); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return requester{}, false
		}
	} else if id, ok = s.caller(w, r, false); !ok {
		return requester{}, false
	}
	if !known {
		return requester{Actor: task.ActorAgent}, true
	}
	driven, err := s.deps.Turns.UserDriven(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return requester{}, false
	}
	return requester{Actor: task.ActorAgent, Session: id, Driven: driven}, true
}

// forUser is requester refusing, with 403, an agent whose session the user does not drive: it runs a turn the
// loop sent, or the loop typed into it last.
func (s *Server) forUser(w http.ResponseWriter, r *http.Request, what string) (requester, bool) {
	q, ok := s.requester(w, r)
	if ok && !q.user() {
		writeError(w, http.StatusForbidden, unattended(what))
		return requester{}, false
	}
	return q, ok
}

// unattended is the refusal of what to a session the user does not drive.
func unattended(what string) string {
	return what + " acts for you, so only you run it, or a session you drive; this session runs a turn the loop sent or the loop typed into it last"
}

// offers says whether the profile offers verb on e; a verb with no offer is
// offered everywhere.
func offers(verbs map[string]intentload.VerbText, e task.Task, verb task.Verb) bool {
	p := verbs[string(verb)].Offer
	if p == nil {
		return true
	}
	ok, err := p.Match(workflow.Vars{Facts: catalog.OfferFacts(e)})
	return err == nil && ok
}

// recordVerb records a verb another service ran on e; the verb already
// happened, so a failure is logged and the answer stands.
func (s *Server) recordVerb(ctx context.Context, g *coretasks.Service, e task.Task, req coretasks.Request) {
	if err := g.RecordVerb(ctx, e, req); err != nil {
		s.logf("%s on %s: recording the verb: %v", req.Verb, e.ID, err)
	}
}

func (s *Server) writeTaskResult(ctx context.Context, w http.ResponseWriter, g *coretasks.Service, res coretasks.Result, err error) {
	if err != nil {
		writeTasksError(w, err)
		return
	}
	view, err := s.taskViews(ctx, g, false).view(ctx, res.Task)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, api.TaskResult{Task: view, Lines: res.Lines})
}

// bridgeAction are the row's actions a verb may run on a unit: those of the highest rank the row offers whose task
// verb matches, enabled or not (see rank). Empty when the row offers none.
type bridgeAction struct {
	unit unit.Unit
	verb task.Verb
	top  []api.ActionState
}

// runs is the one enabled action at the top rank; ok false when none or several are.
func (b bridgeAction) runs() (api.ActionState, bool) {
	var on []api.ActionState
	for _, st := range b.top {
		if st.Enabled {
			on = append(on, st)
		}
	}
	if len(on) != 1 {
		return api.ActionState{}, false
	}
	return on[0], true
}

func (b bridgeAction) reason() string {
	var on []string
	for _, st := range b.top {
		if st.Enabled {
			on = append(on, st.ID)
		}
	}
	switch {
	case len(b.top) == 0:
		return fmt.Sprintf("%s offers no action to %s", b.unit.Label(), b.verb)
	case len(on) > 1:
		return fmt.Sprintf("%s offers %s to %s; run one of them from the palette", b.unit.Label(), strings.Join(on, " and "), b.verb)
	}
	return cmp.Or(b.top[0].Reason, fmt.Sprintf("%s is not enabled on %s", b.top[0].ID, b.unit.Label()))
}

// bridge is what verb runs on e as its unit's action: ok false when a handler takes the verb or bridges says
// it does not run as one. A dismiss the row offers no action for is the state change.
func (s *Server) bridge(ctx context.Context, g *coretasks.Service, e task.Task, verb task.Verb) (bridgeAction, bool) {
	if !s.bridges(e, verb) || g.Handles(e, verb) || !s.pluginEnabled(e.Units[0].Provider) {
		return bridgeAction{}, false
	}
	u := e.Units[0]
	b := bridgeAction{unit: u, verb: verb}
	rd, found, err := s.readRow(ctx, u)
	if err != nil {
		s.logf("tasks: the row of %s: %v", u.Label(), err)
	}
	if found {
		b = s.offeredFor(rd.Row, verb)
	}
	if s.handlerOf(verb) == handlerDrop && len(b.top) == 0 {
		return bridgeAction{}, false
	}
	return b, true
}

// offeredFor are row's actions that verb may run at the highest rank the row offers (catalog.Rank); a lower
// rank never runs while a higher one is offered.
func (s *Server) offeredFor(row api.Row, verb task.Verb) bridgeAction {
	return rankOffered(s.servedSpecs(), row, verb)
}

func rankOffered(served []api.ActionSpec, row api.Row, verb task.Verb) bridgeAction {
	specs := map[string]api.ActionSpec{}
	for _, sp := range served {
		specs[sp.ID] = sp
	}
	rank := func(sp api.ActionSpec) int { return catalog.Rank(sp, row.Unit.Provider) }
	b := bridgeAction{unit: row.Unit, verb: verb}
	best := -1
	for _, st := range row.Actions {
		sp, ok := specs[st.ID]
		if !ok || sp.Verb != string(verb) {
			continue
		}
		switch r := rank(sp); {
		case best < 0 || r < best:
			best, b.top = r, []api.ActionState{st}
		case r == best:
			b.top = append(b.top, st)
		}
	}
	return b
}

// runBridge runs st on u as POST /api/units/<provider>/<key>/<action> would, with the row's basis, the
// task's items on u, and the guidance and mode of the task's args.
func (s *Server) runBridge(w http.ResponseWriter, r *http.Request, g *coretasks.Service, e task.Task, verb task.Verb, u unit.Unit, st api.ActionState, req api.TaskRequest, q requester) {
	mode := req.Args["mode"]
	if err := s.bridgeMode(st.ID, mode); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if mode == string(turn.RunnerSession) && !s.can(u.Provider, api.CapabilityCheckouts) && s.deps.Placer != nil {
		if _, err := s.deps.Placer.SessionPlace(r.Context(), u); err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
	}
	var its []string
	for _, ref := range e.Items {
		if ref.Unit == u {
			its = append(its, ref.Item)
		}
	}
	body, err := json.Marshal(struct {
		api.ActionRequest
		Mode string `json:"mode,omitempty"`
	}{api.ActionRequest{Basis: st.Basis, Source: req.Source, Guidance: req.Args["guidance"], Items: its, Task: string(e.ID)}, mode})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	ar, err := http.NewRequestWithContext(r.Context(), http.MethodPost, api.UnitPath(u, st.ID), bytes.NewReader(body))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, h := range []string{api.SessionHeader, api.ManagerHeader} {
		if v := r.Header.Get(h); v != "" {
			ar.Header.Set(h, v)
		}
	}
	ar.SetPathValue("provider", string(u.Provider))
	ar.SetPathValue("key", u.Key)
	ar.SetPathValue("action", st.ID)
	rec := &bufferedResponse{header: http.Header{}, code: http.StatusOK}
	s.handleAction(rec, ar)
	var res api.ActionResult
	switch {
	case rec.code == http.StatusAccepted:
		var started struct{ Seq int64 }
		if err := json.Unmarshal(rec.body.Bytes(), &started); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		res.Output = []string{fmt.Sprintf("%s started headless as turn %d", st.ID, started.Seq)}
	case rec.code != http.StatusOK:
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(rec.code)
		_, _ = w.Write(rec.body.Bytes())
		return
	default:
		if err := json.Unmarshal(rec.body.Bytes(), &res); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	ctx := context.WithoutCancel(r.Context())
	s.recordVerb(ctx, g, e, q.task(coretasks.Request{Verb: verb, Source: req.Source}))
	now, err := g.Task(ctx, e.ID)
	if err != nil {
		now = e
	}
	view, err := s.taskViews(ctx, g, false).view(ctx, now)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	lines := res.Output
	if res.Intake != nil {
		lines = append(lines, fmt.Sprintf("%s accepted as intake %d", st.ID, res.Intake.Seq))
	}
	writeJSON(w, api.TaskResult{Task: view, Lines: lines})
}

// bridgeMode refuses a mode the action does not run in; "" leaves the choice to the action.
func (s *Server) bridgeMode(action, mode string) error {
	switch mode {
	case "":
		return nil
	case string(turn.RunnerHeadless), string(turn.RunnerSession):
	default:
		return fmt.Errorf("mode is %s or %s, not %q", turn.RunnerHeadless, turn.RunnerSession, mode)
	}
	d, ok := s.loaded().Registry.Get(dintent.Name(action))
	if !ok || !d.Runs(mode) {
		return fmt.Errorf("%s does not run in %s mode; %s", action, mode, otherMode(mode))
	}
	return nil
}

// delegateModes are the modes action runs in on u; a session is disabled with the placer's reason when u
// has nowhere to run one.
func (s *Server) delegateModes(ctx context.Context, u unit.Unit, action string) []api.RunnerState {
	d, ok := s.loaded().Registry.Get(dintent.Name(action))
	if !ok {
		return nil
	}
	var out []api.RunnerState
	for _, m := range []turn.Runner{turn.RunnerHeadless, turn.RunnerSession} {
		if !d.Runs(string(m)) {
			continue
		}
		ms := api.RunnerState{Runner: string(m), Enabled: true}
		if m == turn.RunnerSession && !s.can(u.Provider, api.CapabilityCheckouts) && s.deps.Placer != nil {
			if _, err := s.deps.Placer.SessionPlace(ctx, u); err != nil {
				ms.Enabled, ms.Reason = false, err.Error()
			}
		}
		out = append(out, ms)
	}
	return out
}

func otherMode(mode string) string {
	if mode == string(turn.RunnerHeadless) {
		return "hand it to an agent instead"
	}
	return "draft it headless instead"
}

// bufferedResponse holds a handler's answer for the bridge to pass on.
type bufferedResponse struct {
	header http.Header
	code   int
	body   bytes.Buffer
	wrote  bool
}

func (b *bufferedResponse) Header() http.Header { return b.header }

func (b *bufferedResponse) Write(p []byte) (int, error) {
	b.wrote = true
	return b.body.Write(p)
}

func (b *bufferedResponse) WriteHeader(code int) {
	if !b.wrote {
		b.code, b.wrote = code, true
	}
}

func (s *Server) handleCounters(w http.ResponseWriter, r *http.Request) {
	g, ok := s.tasks(w)
	if !ok {
		return
	}
	ctx, q := r.Context(), r.URL.Query()
	var c task.Counters
	var err error
	if text := strings.TrimSpace(q.Get(api.CountersQuery)); text == "" {
		c, err = g.Counts(ctx, q.Get("area"))
	} else {
		c, err = g.CountsWhere(ctx, q.Get("area"), s.lensKeep(ctx, g, text))
	}
	if err != nil {
		writeTasksError(w, err)
		return
	}
	writeJSON(w, wireCounters(c))
}

// lensKeep keeps the tasks that lens query text matches, over the facets and texts of their views; it reads
// the rows of every task of the pass at its first call.
func (s *Server) lensKeep(ctx context.Context, g *coretasks.Service, text string) coretasks.Keep {
	return s.lensKeepIn(s.taskViews(ctx, g, false), text, nil)
}

// lensKeepIn is lensKeep building each task's view with vs, and keeping in kept the view of each task it keeps
// when kept is not nil.
func (s *Server) lensKeepIn(vs *taskViewer, text string, kept map[task.ID]api.TaskView) coretasks.Keep {
	specs := s.taskFacetSpecs()
	query, now := lens.Parse(text, specs), s.deps.Now()
	preloaded := false
	return func(ctx context.Context, e task.Task) (bool, error) {
		if pass, ok := coretasks.PassOf(ctx); ok && !preloaded {
			preloaded = true
			vs.readRows(ctx, pass.Tasks)
		}
		v, err := vs.view(ctx, e)
		if err != nil {
			return false, err
		}
		ok := query.Match(lens.Item{Facets: v.Facets, Texts: v.Texts()}, specs, now)
		if ok && kept != nil {
			kept[e.ID] = v
		}
		return ok, nil
	}
}

// taskFacetSpecs are the facets a query over tasks can name: the plugins' and the profile's, one spec by id.
func (s *Server) taskFacetSpecs() map[string]api.FacetSpec {
	specs := map[string]api.FacetSpec{}
	for _, f := range s.rowsCatalog().Facets {
		specs[f.ID] = f
	}
	for _, f := range s.viewsCatalog().Facets {
		specs[f.ID] = f
	}
	return specs
}

func (s *Server) writeTask(w http.ResponseWriter, ctx context.Context, g *coretasks.Service, e task.Task) {
	v, err := s.taskViews(ctx, g, false).view(ctx, e)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, v)
}

// counters are the Area "" counters a change or a rows page carries; nil without the tasks.
func (s *Server) counters(ctx context.Context) *api.Counters {
	if s.deps.Tasks == nil {
		return nil
	}
	return s.countersOf(s.deps.Tasks.Counts(ctx, ""))
}

func (s *Server) countersOf(c task.Counters, err error) *api.Counters {
	if err != nil {
		s.logf("tasks: counters: %v", err)
		return nil
	}
	out := wireCounters(c)
	return &out
}

func wireCounters(c task.Counters) api.Counters {
	out := api.Counters{Area: c.Area, Views: map[string]int{}, Areas: map[string]int{}, Contexts: map[string]int{}, Badge: c.Badge}
	for v, n := range c.Views {
		out.Views[string(v)] = n
	}
	for a, n := range c.Areas {
		out.Areas[a] = n
	}
	for cx, n := range c.Contexts {
		out.Contexts[cx] = n
	}
	return out
}

// taskViewer maps tasks to their wire views; detail expands the cards and the items.
type taskViewer struct {
	s        *Server
	g        *coretasks.Service
	detail   bool
	children map[task.ID][]task.ID
	drafts   []draft.Draft
	// pending are the ids of openDrafts by the task they serve, read once per page.
	pending map[string][]int64
	status  map[task.ID]coretasks.ProjectState
	// briefs are the brief records by unit, read once per page.
	briefs map[unit.Unit]port.Event
	// rows are the read model's rows of the units read, each read once: a page reads its tasks' in one
	// snapshot.
	rows map[unit.Unit]readmodel.Read
	read map[unit.Unit]bool
	// refs are the tasks a listed id reads as, the page's own and each one read once; ok false for none.
	refs map[task.ID]taskRef
	// items are the items of the units the page's drafts are on, each unit's read once.
	items map[unit.Unit][]items.Item
	// whereBy bounds every draft destination lookup of this page.
	whereBy time.Time
	// as is who the page is for, whose verbs it offers.
	as requester
}

func (s *Server) taskViews(ctx context.Context, g *coretasks.Service, detail bool) *taskViewer {
	return &taskViewer{s: s, g: g, detail: detail, whereBy: time.Now().Add(s.whereBudget()), as: s.pageFor(ctx)}
}

type callerKey struct{}

// withCaller keeps the session header of each request on its context, for the pages it reads to offer what its
// caller may run.
func withCaller(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if v := r.Header.Get(api.SessionHeader); v != "" {
			r = r.WithContext(context.WithValue(r.Context(), callerKey{}, v))
		}
		h.ServeHTTP(w, r)
	})
}

// pageFor is who a page read with ctx is for: the person when the request names no session, else an agent through
// the session that holds its handle, driven by the user or not.
func (s *Server) pageFor(ctx context.Context) requester {
	v, _ := ctx.Value(callerKey{}).(string)
	if v == "" {
		return personRequest
	}
	q := requester{Actor: task.ActorAgent}
	h, err := api.SessionHandle(v)
	if err != nil || s.deps.Turns == nil {
		return q
	}
	if id, ok, err := s.deps.Turns.SessionOfHandle(ctx, h); err == nil && ok {
		q.Session = id
		q.Driven, _ = s.deps.Turns.UserDriven(ctx, id)
	}
	return q
}

// accepts refuses verb on e unless the viewer may run it.
func (ev *taskViewer) accepts(e task.Task, verb task.Verb) error {
	if !ev.as.user() {
		return errors.New(ev.s.notice(builtin.NoticeVerbForYou, map[string]any{"Verb": string(verb)}))
	}
	return ev.g.Accepts(e, verb, ev.as.Actor)
}

func (ev *taskViewer) view(ctx context.Context, e task.Task) (api.TaskView, error) {
	ctx = whereBy(ctx, ev.whereBy)
	v := api.TaskView{ID: string(e.ID), State: string(e.State), Category: string(e.Category), Title: e.Title,
		Notes: e.Notes, Parent: string(e.Parent), Props: propsView(ev.s.loaded().Tasks, e.Props), Changed: e.Changed,
		Units: slices.Clone(e.Units), Created: e.Created, Updated: e.Updated}
	if v.Units == nil {
		v.Units = []unit.Unit{}
	}
	v.UnitLabels = ev.unitLabels(ctx, v.Units)
	v.Drafts = ev.pendingDrafts(ctx, e.ID)
	if w, ok, err := ev.g.WakeOf(ctx, e.ID); err != nil {
		return api.TaskView{}, err
	} else if ok {
		v.Wake = &api.WakeView{Until: w.Until, When: w.When, Return: w.Return}
	}
	if err := ev.childrenOf(ctx, e, &v); err != nil {
		return api.TaskView{}, err
	}
	if err := ev.relatedOf(ctx, e, &v); err != nil {
		return api.TaskView{}, err
	}
	if err := ev.mergedFromOf(ctx, e, &v); err != nil {
		return api.TaskView{}, err
	}
	if err := ev.row(ctx, e, &v); err != nil {
		return api.TaskView{}, err
	}
	if ev.detail {
		if err := ev.asks(ctx, e, &v); err != nil {
			return api.TaskView{}, err
		}
		if err := ev.cards(ctx, e, &v); err != nil {
			return api.TaskView{}, err
		}
		if err := ev.unitActions(ctx, e, &v); err != nil {
			return api.TaskView{}, err
		}
		runs, err := ev.s.runViews(ctx, task.UnitOf(e.ID))
		if err != nil {
			return api.TaskView{}, err
		}
		v.Runs = runs
		v.ReplyBroadcast = ev.s.replyBroadcast(ctx, e)
	}
	v.Attention = ev.attention(ctx, e)
	v.Verbs = ev.verbs(ctx, e)
	v.Actions = taskIntentActions(ev.s.loaded(), e)
	if err := ev.project(ctx, e, &v); err != nil {
		return api.TaskView{}, err
	}
	if err := ev.brief(ctx, e, &v); err != nil {
		return api.TaskView{}, err
	}
	ev.facets(ctx, e, &v)
	if err := ev.openGates(ctx, e, &v); err != nil {
		return api.TaskView{}, err
	}
	if err := ev.withDraft(ctx, e, &v); err != nil {
		return api.TaskView{}, err
	}
	return v, nil
}

// brief sets the task's brief record: its own, or the first of its units', as a pull request task's is
// its pull request's. A detail reads its task's, then those of the tasks merged into it; a page reads every
// brief once, and its units' only.
func (ev *taskViewer) brief(ctx context.Context, e task.Task, v *api.TaskView) error {
	if ev.s.deps.TaskRecords == nil {
		return nil
	}
	var rec port.Event
	var ok bool
	if ev.detail {
		held, err := timeline.HeldOf(ctx, ev.s.deps.TaskRecords, e)
		if err != nil {
			return err
		}
		if rec, ok, err = timeline.RecordOf(ctx, ev.s.deps.TaskRecords, builtin.BuildsBrief, e.ID, held); err != nil {
			return err
		}
	} else {
		if ev.briefs == nil {
			got, err := ev.s.deps.TaskRecords.Records(ctx, builtin.BuildsBrief)
			if err != nil {
				return err
			}
			ev.briefs = got
		}
		rec, ok = timeline.Pick(ev.briefs, e.ID, e.Units)
	}
	if !ok {
		return nil
	}
	v.Brief = &api.RecordView{Kind: rec.Key, At: rec.At, Data: jsonData(rec.Data)}
	return nil
}

// facets sets the task's facets: those of its facts row set, its brief's, and its units' rows', joined as
// the profile's facets say, and its reason, its primary unit's row's in the words of that row's plugin.
func (ev *taskViewer) facets(ctx context.Context, e task.Task, v *api.TaskView) {
	defs := ev.s.loaded().Views.Facets
	own := map[string][]string{}
	maps.Copy(own, v.Facets)
	if v.Brief != nil {
		maps.Copy(own, catalog.RecordFacets(defs, v.Brief.Kind, v.Brief.Data))
	}
	rows := ev.rowsOf(ctx, e.Units)
	units := make([]map[string][]string, len(e.Units))
	for i, u := range e.Units {
		units[i] = rows[u].Row.Facets
	}
	v.Facets = catalog.MergeFacets(defs, own, units)
	if len(v.Facets) == 0 {
		v.Facets = nil
	}
	if len(e.Units) > 0 {
		v.Reason = ev.s.rowsCatalog().ReasonOf(rows[e.Units[0]].Row)
	}
}

// readRows reads the rows of the units of es in one snapshot, for their views, and keeps es for their related
// lists (knowTasks).
func (ev *taskViewer) readRows(ctx context.Context, es []task.Task) {
	ev.knowTasks(es)
	var units []unit.Unit
	for _, e := range es {
		units = append(units, e.Units...)
	}
	ev.rowsOf(ctx, units)
}

// rowsOf are the rows of the units read so far, units among them: those not read yet are read in one
// snapshot. A unit no provider lists has none, and a failed read leaves its units without one.
func (ev *taskViewer) rowsOf(ctx context.Context, units []unit.Unit) map[unit.Unit]readmodel.Read {
	if ev.rows == nil {
		ev.rows, ev.read = map[unit.Unit]readmodel.Read{}, map[unit.Unit]bool{}
	}
	var unread []unit.Unit
	for _, u := range units {
		if !ev.read[u] {
			ev.read[u] = true
			unread = append(unread, u)
		}
	}
	if len(unread) == 0 {
		return ev.rows
	}
	got, err := ev.s.rowsRead(ctx, unread)
	if err != nil {
		ev.s.logf("tasks: reading the rows of %d units: %v", len(unread), err)
	}
	for _, u := range unread {
		if r, ok := got[u]; ok {
			ev.rows[u] = r
		}
	}
	return ev.rows
}

// unitLabels are units as a person reads them, in order: each row's label, else the unit's own label.
func (ev *taskViewer) unitLabels(ctx context.Context, units []unit.Unit) []string {
	if len(units) == 0 {
		return nil
	}
	rows := ev.rowsOf(ctx, units)
	out := make([]string, len(units))
	for i, u := range units {
		out[i] = cmp.Or(rows[u].Row.Label, u.Label())
	}
	return out
}

// childrenOf sets the open children of a project, or of any task in a detail, from one read per page.
func (ev *taskViewer) childrenOf(ctx context.Context, e task.Task, v *api.TaskView) error {
	if !e.IsProject() && !ev.detail {
		return nil
	}
	if ev.children == nil {
		got, err := ev.g.Children(ctx)
		if err != nil {
			return err
		}
		ev.children = got
	}
	for _, id := range ev.children[e.ID] {
		v.Children = append(v.Children, string(id))
	}
	return nil
}

// propsView is each property in its type's JSON, as tasks.toml declares it; a property it does not declare in
// the JSON of the value it holds.
func propsView(def intentload.TasksDef, props task.Props) map[string]json.RawMessage {
	if len(props) == 0 {
		return nil
	}
	out := make(map[string]json.RawMessage, len(props))
	for name, v := range props {
		var x any
		p, _ := def.Prop(name)
		switch {
		case p.Type == task.PropInt || p.Type == "" && v.Int != 0:
			x = v.Int
		case p.Type == task.PropDate || p.Type == "" && !v.Time.IsZero():
			x = v.Time
		case p.Type == task.PropDuration || p.Type == "" && v.Dur != 0:
			x = v.Dur.String()
		case p.Type == task.PropStrings || p.Type == "" && v.List != nil:
			x = nonNil(v.List)
		case p.Type == task.PropActor || p.Type == task.PropActors || p.Type == "" && v.Actors != nil:
			x = v.Actors
		default:
			x = v.Text
		}
		if b, err := json.Marshal(x); err == nil {
			out[name] = b
		}
	}
	return out
}

// row sets what e's row shows of its facts, for a stored task: who owes its next step, who waits on you through
// it, and its extraction lag.
func (ev *taskViewer) row(ctx context.Context, e task.Task, v *api.TaskView) error {
	if ev.s.deps.Row == nil || e.Origin == task.OriginDraft {
		return nil
	}
	r, err := ev.s.deps.Row(ctx, e)
	if err != nil {
		return err
	}
	v.OwedBy, v.Askers, v.Facets = r.OwedBy, r.Askers, r.Facets
	if r.ExtractionLag > 0 {
		v.ExtractionLag = r.ExtractionLag.String()
	}
	return nil
}

// asks are the items e holds, as their askers put them, each with the quotes its brief record keeps of it.
func (ev *taskViewer) asks(ctx context.Context, e task.Task, v *api.TaskView) error {
	if ev.s.deps.Items == nil {
		return nil
	}
	quotes := briefQuotes(v.Brief)
	byUnit := ev.unitItems()
	for _, ref := range e.Items {
		its, ok := byUnit[ref.Unit]
		if !ok {
			var err error
			if its, err = ev.s.deps.Items.Items(ctx, ref.Unit); err != nil {
				return err
			}
			byUnit[ref.Unit] = its
		}
		i := slices.IndexFunc(its, func(it items.Item) bool { return it.ID == ref.Item })
		if i < 0 {
			continue
		}
		it := its[i]
		v.Asks = append(v.Asks, api.AskView{Item: it.ID, Summary: it.Summary, Asker: cmp.Or(it.Requester.Name, it.Requester.Key),
			Permalink: it.Origin.Permalink, Quotes: quotes[it.ID]})
	}
	return nil
}

// briefQuotes are the quotes a brief record's asks keep, by the item each ask is about.
func briefQuotes(rec *api.RecordView) map[string][]string {
	if rec == nil {
		return nil
	}
	var body struct {
		Asks []struct {
			Item   string `json:"item"`
			Quotes []struct {
				Text string `json:"text"`
			} `json:"quotes"`
		} `json:"asks"`
	}
	if json.Unmarshal(rec.Data, &body) != nil {
		return nil
	}
	out := map[string][]string{}
	for _, a := range body.Asks {
		for _, q := range a.Quotes {
			out[a.Item] = append(out[a.Item], q.Text)
		}
	}
	return out
}

func (ev *taskViewer) cards(ctx context.Context, e task.Task, v *api.TaskView) error {
	if ev.s.deps.Cards == nil || len(e.Units) == 0 {
		return nil
	}
	got, err := ev.s.deps.Cards.Get(ctx, e.Units)
	if err != nil {
		return err
	}
	for _, u := range e.Units {
		if snap, ok := got[u]; ok {
			v.Cards = append(v.Cards, snap)
		}
	}
	return nil
}

// unitActions are the actions the rows of e's units offer on a task's page.
func (ev *taskViewer) unitActions(ctx context.Context, e task.Task, v *api.TaskView) error {
	specs := ev.s.servedSpecs()
	onTask := func(st api.ActionState) bool {
		i := slices.IndexFunc(specs, func(sp api.ActionSpec) bool { return sp.ID == st.ID })
		return i >= 0 && slices.Contains(specs[i].Surfaces, catalog.SurfaceTask)
	}
	for _, u := range e.Units {
		rd, ok, err := ev.s.readRow(ctx, u)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		var acts []api.ActionState
		for _, st := range rd.Row.Actions {
			if onTask(st) {
				acts = append(acts, st)
			}
		}
		if len(acts) > 0 {
			v.UnitActions = append(v.UnitActions, api.UnitActions{Unit: u, Label: rd.Row.Label, Actions: acts})
		}
	}
	return nil
}

// verbs are the verbs of the catalog as they stand on e: refused all over on a disabled plugin's unit, taken by
// a handler, run as the unit's action, or the state change.
func (ev *taskViewer) verbs(ctx context.Context, e task.Task) []api.VerbState {
	order := ev.s.verbOrder()
	out := make([]api.VerbState, 0, len(order))
	words := ev.s.loaded().Tasks.Verbs
	off := ""
	for _, u := range e.Units {
		if !ev.s.pluginEnabled(u.Provider) {
			off = string(u.Provider) + " is disabled"
			break
		}
	}
	var row *api.Row
	if len(e.Units) > 0 {
		if rd, ok := ev.rowsOf(ctx, e.Units[:1])[e.Units[0]]; ok {
			row = &rd.Row
		}
	}
	offered := func(row *api.Row, verb task.Verb) bridgeAction {
		if row == nil {
			return bridgeAction{verb: verb}
		}
		return ev.s.offeredFor(*row, verb)
	}
	for _, name := range order {
		verb := task.Verb(name)
		if !offers(words, e, verb) {
			continue
		}
		st := api.VerbState{Verb: name}
		none := ev.nothingToPick(e, verb)
		switch {
		case off != "" && ev.s.handlerOf(verb) != handlerDrop:
			st.Reason = off
		case ev.resolvesGate(ctx, e, verb):
			st.Enabled = true
		case none != "":
			st.Reason = none
		case ev.g.Handles(e, verb):
			st.Enabled = true
		case ev.s.replies(verb):
			st.Reason = ev.s.replyRefusal(e)
			st.Enabled = st.Reason == ""
		case ev.s.bridges(e, verb) && ev.s.pluginEnabled(e.Units[0].Provider) && (ev.s.handlerOf(verb) != handlerDrop || len(offered(row, verb).top) > 0):
			b := offered(row, verb)
			b.unit = e.Units[0]
			var runs api.ActionState
			runs, st.Enabled = b.runs()
			if !st.Enabled {
				st.Reason = b.reason()
			} else if ev.s.handlerOf(verb) == handlerDelegate {
				st.Runners = ev.s.delegateModes(ctx, b.unit, runs.ID)
			}
		default:
			if err := ev.accepts(e, verb); err != nil {
				st.Reason = err.Error()
			} else {
				st.Enabled = true
			}
		}
		out = append(out, st)
	}
	return out
}

// nothingToPick is why verb, picking from a list of e's (builtin.PickRoles), has no task to pick on e; "" when
// it has one or picks from elsewhere.
func (ev *taskViewer) nothingToPick(e task.Task, verb task.Verb) string {
	tasks := ev.s.loaded().Tasks
	pick := builtin.TaskPicks[tasks.Handler(string(verb))]
	role, ok := builtin.PickRoles[pick]
	if !ok || len(e.Role(tasks, role).List) > 0 {
		return ""
	}
	return ev.s.notice(builtin.NoticeNothingToPick, map[string]any{"Verb": string(verb), "Pick": pick})
}

// syncTasks runs Sync once per burst of item, source-event, card and gate notes, on its own goroutine, until ctx
// ends: everything at start, after an All note and when the user's identity grows, and each minute for the
// snoozes and stars that come due. At start and after each profile reload it first recategorizes the stored
// tasks.
func (s *Server) syncTasks(ctx context.Context) {
	g := s.deps.Tasks
	var poke <-chan struct{}
	var from, cfgFrom port.Generation
	if s.deps.Changes != nil {
		p, cancel := change.Poke(s.deps.Changes, syncInputs|port.TopicConfig)
		defer cancel()
		poke = p
		cfgFrom = s.deps.Changes.Gen()
	}
	recategorize := true
	tick := time.NewTicker(syncTick)
	defer tick.Stop()
	all, settled := true, s.deps.TasksMe == nil
	var retry []unit.Unit
	var quiet, late <-chan time.Time
	idErr := ""
	for {
		var units []unit.Unit
		if s.deps.Changes != nil {
			c := s.deps.Changes.Since(from, syncInputs)
			from, all, units = c.Gen, all || c.All, c.Units
		}
		if !settled {
			grew, err := s.deps.TasksMe(ctx)
			switch {
			case err == nil:
				idErr = ""
			case err.Error() != idErr:
				idErr = err.Error()
				s.logf("tasks: identity: %v", err)
			}
			settled, all = err == nil, all || grew
		}
		if all && s.deps.TaskUnits != nil {
			us, err := s.deps.TaskUnits(ctx)
			if err != nil {
				s.logf("tasks: units: %v", err)
			}
			units = append(units, us...)
		}
		units = append(units, retry...)
		all, retry = false, nil
		if s.deps.Changes != nil {
			c := s.deps.Changes.Since(cfgFrom, port.TopicConfig)
			cfgFrom, recategorize = c.Gen, recategorize || c.Has(port.TopicConfig)
		}
		if recategorize {
			recategorize = !s.recategorize(ctx, g)
		}
		sctx, scope := meter.Start(ctx, meter.Loop("tasks"))
		if err := g.Sync(sctx, units); err != nil && ctx.Err() == nil {
			s.logf("tasks: sync: %v", err)
			retry = units
		}
		if w := s.deps.Wakes; w != nil {
			if err := w.Sweep(sctx); err != nil && ctx.Err() == nil {
				s.logf("tasks: wakes: %v", err)
			}
		}
		scope.End()
	wait:
		for {
			select {
			case <-ctx.Done():
				return
			case <-poke:
				quiet = time.After(syncQuiet)
				if late == nil {
					late = time.After(syncMaxDelay)
				}
			case <-quiet:
				break wait
			case <-late:
				break wait
			case <-tick.C:
				break wait
			}
		}
		quiet, late = nil, nil
	}
}

// recategorize runs the recategorize pass and keeps the states stored tasks hold that the profile does not
// declare, for the health; false when it failed, so the next pass tries again.
func (s *Server) recategorize(ctx context.Context, g *coretasks.Service) bool {
	unknown, err := g.Recategorize(ctx)
	if err != nil {
		if ctx.Err() == nil {
			s.logf("tasks: recategorize: %v", err)
		}
		return false
	}
	s.heldStates.Store(&unknown)
	return true
}

// heldProblems are the problems of the states stored tasks hold that the profile no longer declares.
func (s *Server) heldProblems(l *intentload.Loaded) intentload.Problems {
	held := s.heldStates.Load()
	if held == nil {
		return nil
	}
	return l.Tasks.HeldProblems(*held)
}

// facetProblems are the facets of the profile and of the plugins that disagree on how to group, sort, rank, match or bucket.
func (s *Server) facetProblems(l *intentload.Loaded) intentload.Problems {
	c := s.rowsCatalog()
	k := &s.facetCheck
	k.Lock()
	defer k.Unlock()
	if k.loaded != l || k.version != c.Version {
		k.loaded, k.version, k.out = l, c.Version, catalog.CheckFacets(l.Views.Facets, c.Facets)
	}
	return k.out
}

// feedTasks publishes a change after each burst of notes of what a view, the plan or the gates read and of row
// changes whose units a task holds, until ctx ends.
func (s *Server) feedTasks(ctx context.Context) {
	if s.deps.Changes == nil {
		return
	}
	poke, cancel := change.Poke(s.deps.Changes, taskInputs|flagInputs)
	defer cancel()
	s.moved.attach()
	from := s.deps.Changes.Gen()
	var quiet <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-poke:
		case <-s.moved.wake:
		case <-quiet:
			quiet = nil
			c := s.deps.Changes.Since(from, taskInputs)
			flags := s.deps.Changes.Since(from, flagInputs)
			from = min(c.Gen, flags.Gen)
			pctx, scope := meter.Start(ctx, meter.Loop("tasks-feed"))
			s.publishTasks(pctx, c, flags)
			scope.End()
			continue
		}
		if quiet == nil {
			quiet = time.After(syncQuiet)
		}
	}
}

// publishTasks publishes the counters with the units c and the row changes since the last call moved, unless
// nothing moved but rows no task holds, and says whether the plan and the gates moved by the topics of flags. A
// reloaded profile moves every task, since it can change their facets.
func (s *Server) publishTasks(ctx context.Context, c, flags port.Change) {
	rows, all := s.moved.take()
	var held []unit.Unit
	var snap coretasks.Snapshot
	err := errTasksUnavailable
	if s.deps.Tasks == nil {
		all = false
	} else if snap, err = s.deps.Tasks.Snapshot(ctx); err != nil {
		s.logf("tasks feed: %v", err)
		held = rows
	} else {
		held = snap.Held(rows)
	}
	pos := s.hub.position()
	ch := api.Change{Type: "changed", Epoch: pos.Epoch, Rev: pos.Rev,
		Plan: flags.All || flags.Has(planInputs), Gates: flags.All || flags.Has(gateInputs)}
	if !c.Empty() || all || len(held) > 0 {
		ch.All = c.All || all || c.Has(port.TopicConfig)
		if err == nil {
			ch.Counters = s.countersOf(snap.Counts(ctx, ""))
		}
		if !ch.All {
			units := slices.Concat(c.Units, held)
			slices.SortFunc(units, api.CompareUnits)
			ch.Units = slices.Compact(units)
		}
	} else if !ch.Plan && !ch.Gates {
		return
	}
	s.hub.publish(ch)
}

// rowsRead are the read model's rows of units, from one snapshot.
func (s *Server) rowsRead(ctx context.Context, units []unit.Unit) (map[unit.Unit]readmodel.Read, error) {
	if s.readRows != nil {
		return s.readRows(ctx, units)
	}
	got, _, _, err := s.readModel().ReadMany(ctx, units)
	return got, err
}

// presentations are units as their rows present them, from one snapshot;
// a unit without a row has its own label alone.
func (s *Server) presentations(units []unit.Unit) map[unit.Unit]readmodel.Presentation {
	known := s.readModel().Presentations(units)
	out := make(map[unit.Unit]readmodel.Presentation, len(units))
	for _, u := range units {
		p := known[u]
		p.Label = cmp.Or(p.Label, u.Label())
		out[u] = p
	}
	return out
}

// unitLabel is u as a person reads it outside a page: its row's label, else its own.
func (s *Server) unitLabel(u unit.Unit) string { return s.presentations([]unit.Unit{u})[u].Label }

// resolvesGate says whether verb resolves an open gate of e: its handler approves or answers, no handler of
// e's kind takes it, and e has an open gate that takes its role.
func (ev *taskViewer) resolvesGate(ctx context.Context, e task.Task, verb task.Verb) bool {
	role, ok := gateVerbRoles[ev.s.loaded().Tasks.Handler(string(verb))]
	if !ok || ev.g.Handles(e, verb) {
		return false
	}
	_, _, ok, err := ev.s.taskGate(ctx, e, role, api.ActionRequest{})
	if err != nil {
		ev.s.logf("the gates of %s: %v", e.ID, err)
	}
	return ok
}
