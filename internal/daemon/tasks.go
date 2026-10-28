package daemon

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/api"
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
const syncInputs = port.TopicItems | port.TopicSourceEvents | port.TopicCards

// taskInputs are the notes after which a change carries the counters.
const taskInputs = port.TopicTasks | port.TopicDrafts

// bridged are the verbs a task with units runs as its unit's action when no handler takes them.
var bridged = []task.Verb{task.VerbApprove, task.VerbDelegate, task.VerbDismiss}

// delegableKinds are the kinds of task with units whose delegate runs as their unit's action; an action task
// bridges every verb of bridged.
var delegableKinds = []task.Kind{task.KindAction, task.KindWaiting, task.KindClose, task.KindCapture}

// bridges says whether verb on e runs as its first unit's action, a handler aside.
func bridges(e task.Task, verb task.Verb) bool {
	if len(e.Units) == 0 || !slices.Contains(bridged, verb) {
		return false
	}
	return e.Kind == task.KindAction || verb == task.VerbDelegate && slices.Contains(delegableKinds, e.Kind)
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

func knownVerb(v string) bool { return slices.Contains(builtin.TaskVerbs, v) }

func verbNames() string { return strings.Join(builtin.TaskVerbs, ", ") }

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
	case errors.Is(err, coretasks.ErrVerb), errors.Is(err, coretasks.ErrPluginDisabled), errors.Is(err, messages.ErrClosed), errors.Is(err, turn.ErrClosed),
		errors.Is(err, messages.ErrNotApplied):
		writeError(w, http.StatusConflict, err.Error())
	default:
		writeActionError(w, err)
	}
}

func (s *Server) handleViews(w http.ResponseWriter, r *http.Request) {
	g, ok := s.tasks(w)
	if !ok {
		return
	}
	cat := api.ViewsCatalog{Areas: nonNil(slices.Clone(s.deps.Cfg.Tasks.Areas)), Contexts: nonNil(slices.Clone(s.deps.Cfg.Tasks.Contexts)), Views: s.servedViews(g)}
	for _, v := range builtin.TaskVerbs {
		t, b := s.loaded().Tasks.Verbs[v], bundledWords().tasks.Verbs[v]
		cat.Verbs = append(cat.Verbs, api.VerbSpec{Verb: v, Handler: s.loaded().Tasks.Handler(v), Label: cmp.Or(t.Label, b.Label, v), Summary: cmp.Or(t.Summary, b.Summary),
			Input: cmp.Or(t.Input, b.Input), Key: verbKey(task.Verb(v))})
	}
	writeJSON(w, cat)
}

// servedViews are the views g serves, worded by the profile.
func (s *Server) servedViews(g *coretasks.Service) []api.TaskViewSpec {
	texts, bundled := s.loaded().Lists, bundledWords().lists
	var out []api.TaskViewSpec
	for _, v := range builtin.Views {
		if v != string(task.ViewReview) || g.Serves(task.ViewReview) {
			out = append(out, api.TaskViewSpec{ID: v, Label: cmp.Or(texts.Views[v], bundled.Views[v], v), Key: texts.Keys[v]})
		}
	}
	return out
}

func (s *Server) handleTasks(w http.ResponseWriter, r *http.Request) {
	g, ok := s.tasks(w)
	if !ok {
		return
	}
	q := r.URL.Query()
	view := task.View(q.Get("view"))
	if !slices.Contains(builtin.Views, string(view)) {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("unknown view %q", view))
		return
	}
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
	ctx := r.Context()
	p, err := g.TasksSince(ctx, query, coretasks.Since{Epoch: since.Epoch, Rev: since.Rev})
	if err != nil {
		writeTasksError(w, err)
		return
	}
	page := api.TasksPage{Epoch: p.Epoch, Rev: p.Rev, Full: p.Full, Tasks: []api.TaskView{}, Removed: []string{}, Counters: wireCounters(p.Counters),
		Loading: s.loading()}
	vs := s.taskViews(ctx, g, false)
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
	if !knownVerb(verb) {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("unknown verb %q; one of %s", verb, verbNames()))
		return
	}
	actor, ok := requestActor(w, r)
	if !ok {
		return
	}
	ctx := context.WithoutCancel(r.Context())
	e, err := g.Task(ctx, task.ID(r.PathValue("id")))
	if err != nil {
		writeTasksError(w, err)
		return
	}
	v := task.Verb(verb)
	if !offers(s.loaded().Tasks.Verbs, e, v) {
		writeError(w, http.StatusConflict, fmt.Sprintf("%s is not offered on this task: the profile's tasks.toml says where it is", v))
		return
	}
	for _, u := range e.Units {
		if v != task.VerbDismiss && s.refusePlugin(w, u) {
			return
		}
	}
	s.doTaskVerb(w, r, g, e, coretasks.Request{Verb: v, Args: req.Args, Source: req.Source, Actor: actor}, req)
}

// gateVerbRoles are the roles of the gate intents a verb of each handler resolves a task's open gate with.
var gateVerbRoles = map[string]builtin.GateRole{"approve": builtin.GateApproves, "answer": builtin.GateTakesText}

// doTaskVerb runs creq on e: a verb whose handler approves or answers resolves the task's open gate, one a
// handler takes runs it, a reply or a bridged verb runs on the task's unit.
func (s *Server) doTaskVerb(w http.ResponseWriter, r *http.Request, g *coretasks.Service, e task.Task, creq coretasks.Request, req api.TaskRequest) {
	ctx, v := context.WithoutCancel(r.Context()), creq.Verb
	if role, ok := gateVerbRoles[s.loaded().Tasks.Handler(string(v))]; ok && creq.Approved == 0 && !g.Handles(e.Kind, v) {
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
	reply := s.replies(v) && !g.Handles(e.Kind, v)
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
		s.runReply(ctx, w, g, e, v, req, creq.Actor)
		return
	}
	if bridged {
		st, ok := b.runs()
		if !ok {
			writeError(w, http.StatusConflict, b.reason())
			return
		}
		s.runBridge(w, r, g, e, v, b.unit, st, req, creq.Actor)
		return
	}
	res, err := g.Do(ctx, e.ID, creq)
	s.writeTaskResult(ctx, w, g, res, err)
}

// requestActor is who runs the verb a request asks for: an agent when the request comes from a session, the
// person otherwise; false when its session header is not a handle, refused on w.
func requestActor(w http.ResponseWriter, r *http.Request) (task.Actor, bool) {
	h, ok := callerHandle(w, r, false)
	switch {
	case !ok:
		return "", false
	case h != "":
		return task.ActorAgent, true
	}
	return task.ActorPerson, true
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
	if !bridges(e, verb) || g.Handles(e.Kind, verb) || !s.pluginEnabled(e.Units[0].Provider) {
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
	if verb == task.VerbDismiss && len(b.top) == 0 {
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
func (s *Server) runBridge(w http.ResponseWriter, r *http.Request, g *coretasks.Service, e task.Task, verb task.Verb, u unit.Unit, st api.ActionState, req api.TaskRequest, actor task.Actor) {
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
	s.recordVerb(ctx, g, e, coretasks.Request{Verb: verb, Source: req.Source, Actor: actor})
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
	var terms []string
	for _, t := range q[api.CountersTerm] {
		if t = strings.TrimSpace(t); t != "" {
			terms = append(terms, t)
		}
	}
	var c task.Counters
	var err error
	if len(terms) == 0 {
		c, err = g.Counts(ctx, q.Get("area"))
	} else {
		vs := s.taskViews(ctx, g, false)
		c, err = g.CountsWhere(ctx, q.Get("area"), func(e task.Task) (bool, error) {
			v, err := vs.view(ctx, e)
			return err == nil && api.TaskMatches(v, terms), err
		})
	}
	if err != nil {
		writeTasksError(w, err)
		return
	}
	writeJSON(w, wireCounters(c))
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
	c, err := s.deps.Tasks.Counts(ctx, "")
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
		if v != task.ViewReview && v != task.ViewOpen {
			out.Views[string(v)] = n
		}
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
	status   map[task.ID]coretasks.ProjectState
	// briefs are the brief records by unit, read once per page.
	briefs map[unit.Unit]port.Event
	// whereBy bounds every draft destination lookup of this page.
	whereBy time.Time
}

func (s *Server) taskViews(_ context.Context, g *coretasks.Service, detail bool) *taskViewer {
	return &taskViewer{s: s, g: g, detail: detail, whereBy: time.Now().Add(s.whereBudget())}
}

func (ev *taskViewer) view(ctx context.Context, e task.Task) (api.TaskView, error) {
	v := api.TaskView{ID: string(e.ID), Kind: string(e.Kind), State: string(e.State), Category: string(e.Category), Title: e.Title,
		Notes: e.Notes, Parent: string(e.Parent), Props: propsView(ev.s.loaded().Tasks, e.Props), Changed: e.Changed,
		Units: slices.Clone(e.Units), Items: []api.ItemView{}, Created: e.Created, Updated: e.Updated}
	if v.Units == nil {
		v.Units = []unit.Unit{}
	}
	v.UnitLabels = ev.s.unitLabels(v.Units)
	if e.Snoozed() {
		v.SnoozeUntil, v.SnoozeOnActivity, v.Muted = e.Snooze.Until, e.Snooze.OnActivity, e.Snooze.Mute
	}
	if e.Waiting != nil {
		w := &api.WaitingView{On: []api.WaitingPerson{}, Since: e.Waiting.Since, LastNudge: e.Waiting.LastNudge, Resurface: e.Waiting.Resurface}
		for _, p := range e.Waiting.On {
			w.On = append(w.On, api.WaitingPerson{Name: cmp.Or(p.Name, p.Key)})
		}
		v.Waiting = w
	}
	if err := ev.childrenOf(ctx, e, &v); err != nil {
		return api.TaskView{}, err
	}
	if err := ev.items(ctx, e, &v); err != nil {
		return api.TaskView{}, err
	}
	if ev.detail {
		if err := ev.cards(ctx, e, &v); err != nil {
			return api.TaskView{}, err
		}
		runs, err := ev.s.runViews(ctx, task.UnitOf(e.ID))
		if err != nil {
			return api.TaskView{}, err
		}
		v.Runs = runs
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
	if err := ev.openGates(ctx, e, &v); err != nil {
		return api.TaskView{}, err
	}
	if err := ev.s.withDraft(whereBy(ctx, ev.whereBy), e, &v); err != nil {
		return api.TaskView{}, err
	}
	return v, nil
}

// brief sets the task's brief record: its own, or the first of its units', as a pull request task's is
// its pull request's. A detail reads its task's; a page reads every brief once.
func (ev *taskViewer) brief(ctx context.Context, e task.Task, v *api.TaskView) error {
	if ev.s.deps.TaskRecords == nil {
		return nil
	}
	var rec port.Event
	var ok bool
	if ev.detail {
		var err error
		if rec, ok, err = timeline.RecordOf(ctx, ev.s.deps.TaskRecords, builtin.BuildsBrief, e.ID, e.Units); err != nil {
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

// childrenOf sets the open children of a project, or of any task in a detail, from one read per page.
func (ev *taskViewer) childrenOf(ctx context.Context, e task.Task, v *api.TaskView) error {
	if e.Kind != task.KindProject && !ev.detail {
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

func (ev *taskViewer) items(ctx context.Context, e task.Task, v *api.TaskView) error {
	byUnit := map[unit.Unit][]items.Item{}
	for _, ref := range e.Items {
		iv := api.ItemView{Unit: ref.Unit, ID: ref.Item}
		if ev.detail && ev.s.deps.Items != nil {
			its, ok := byUnit[ref.Unit]
			if !ok {
				var err error
				if its, err = ev.s.deps.Items.Items(ctx, ref.Unit); err != nil {
					return err
				}
				byUnit[ref.Unit] = its
			}
			if i := slices.IndexFunc(its, func(it items.Item) bool { return it.ID == ref.Item }); i >= 0 {
				iv.Summary, iv.State, iv.Kind = its[i].Summary, string(its[i].State), string(its[i].Kind)
			}
		}
		v.Items = append(v.Items, iv)
	}
	return nil
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

// verbs are the verbs of the catalog as they stand on e: refused all over on a disabled plugin's unit, taken by
// a handler, run as the unit's action, or the state change.
func (ev *taskViewer) verbs(ctx context.Context, e task.Task) []api.VerbState {
	out := make([]api.VerbState, 0, len(builtin.TaskVerbs))
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
		if rd, ok, err := ev.s.readModel().Read(ctx, e.Units[0]); err == nil && ok {
			row = &rd.Row
		}
	}
	offered := func(row *api.Row, verb task.Verb) bridgeAction {
		if row == nil {
			return bridgeAction{verb: verb}
		}
		return ev.s.offeredFor(*row, verb)
	}
	for _, name := range builtin.TaskVerbs {
		verb := task.Verb(name)
		if !offers(words, e, verb) {
			continue
		}
		st := api.VerbState{Verb: name}
		switch {
		case off != "" && verb != task.VerbDismiss:
			st.Reason = off
		case ev.resolvesGate(ctx, e, verb):
			st.Enabled = true
		case ev.g.Handles(e.Kind, verb):
			st.Enabled = true
		case ev.s.replies(verb):
			st.Reason = ev.s.replyRefusal(e)
			st.Enabled = st.Reason == ""
		case bridges(e, verb) && ev.s.pluginEnabled(e.Units[0].Provider) && (verb != task.VerbDismiss || len(offered(row, verb).top) > 0):
			b := offered(row, verb)
			b.unit = e.Units[0]
			var runs api.ActionState
			runs, st.Enabled = b.runs()
			if !st.Enabled {
				st.Reason = b.reason()
			} else if verb == task.VerbDelegate {
				st.Runners = ev.s.delegateModes(ctx, b.unit, runs.ID)
			}
		default:
			if err := ev.g.Accepts(e, verb, task.ActorPerson); err != nil {
				st.Reason = err.Error()
			} else {
				st.Enabled = true
			}
		}
		out = append(out, st)
	}
	return out
}

// syncTasks runs Sync once per burst of item, source-event and card notes, on its own goroutine, until ctx
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

// feedTasks publishes a change with the counters after each burst of task and draft notes, naming the units
// of the tasks that moved, until ctx ends.
func (s *Server) feedTasks(ctx context.Context) {
	if s.deps.Changes == nil {
		return
	}
	poke, cancel := change.Poke(s.deps.Changes, taskInputs)
	defer cancel()
	from := s.deps.Changes.Gen()
	var quiet <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-poke:
			if quiet == nil {
				quiet = time.After(syncQuiet)
			}
			continue
		case <-quiet:
			quiet = nil
		}
		c := s.deps.Changes.Since(from, taskInputs)
		from = c.Gen
		pctx, scope := meter.Start(ctx, meter.Loop("tasks-feed"))
		counters := s.counters(pctx)
		scope.End()
		pos := s.hub.position()
		ch := api.Change{Type: "changed", Epoch: pos.Epoch, Rev: pos.Rev, Units: c.Units, Counters: counters}
		if c.All {
			ch.Units = nil
		}
		s.hub.publish(ch)
	}
}

// unitLabels are units as a person reads them, in order: each row's label,
// else the unit's own label.
func (s *Server) unitLabels(units []unit.Unit) []string {
	if len(units) == 0 {
		return nil
	}
	known := s.presentations(units)
	out := make([]string, len(units))
	for i, u := range units {
		out[i] = known[u].Label
	}
	return out
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

func (s *Server) unitLabel(u unit.Unit) string { return s.unitLabels([]unit.Unit{u})[0] }

// resolvesGate says whether verb resolves an open gate of e: its handler approves or answers, no handler of
// e's kind takes it, and e has an open gate that takes its role.
func (ev *taskViewer) resolvesGate(ctx context.Context, e task.Task, verb task.Verb) bool {
	role, ok := gateVerbRoles[ev.s.loaded().Tasks.Handler(string(verb))]
	if !ok || ev.g.Handles(e.Kind, verb) {
		return false
	}
	_, _, ok, err := ev.s.taskGate(ctx, e, role, api.ActionRequest{})
	if err != nil {
		ev.s.logf("the gates of %s: %v", e.ID, err)
	}
	return ok
}
