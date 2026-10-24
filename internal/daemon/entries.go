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
	coregtd "github.com/wloophq/wloop/internal/core/gtd"
	"github.com/wloophq/wloop/internal/core/messages"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gtd"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/keys"
	"github.com/wloophq/wloop/internal/meter"
)

const (
	entriesBodyMax = 64 << 10
	// The Sync worker folds a burst of notes as the row feed does, and wakes each minute for the snoozes and
	// stars that come due with no note.
	syncQuiet    = 250 * time.Millisecond
	syncMaxDelay = time.Second
	syncTick     = time.Minute
)

// syncInputs are the notes whose units Sync reconciles.
const syncInputs = port.TopicItems | port.TopicSourceEvents | port.TopicCards

// entryInputs are the notes after which a change carries the counters.
const entryInputs = port.TopicEntries | port.TopicDrafts

var gtdViews = []api.GTDView{
	{ID: string(gtd.ViewFocus), Label: "Focus"},
	{ID: string(gtd.ViewInbox), Label: "Inbox"},
	{ID: string(gtd.ViewOutbox), Label: "Outbox"},
	{ID: string(gtd.ViewNext), Label: "Next"},
	{ID: string(gtd.ViewWaiting), Label: "Waiting"},
	{ID: string(gtd.ViewProjects), Label: "Projects"},
	{ID: string(gtd.ViewSomeday), Label: "Someday"},
	{ID: string(gtd.ViewLogbook), Label: "Logbook"},
}

var reviewView = api.GTDView{ID: string(gtd.ViewReview), Label: "Review"}

var gtdVerbs = []struct {
	verb  gtd.Verb
	label string
}{
	{gtd.VerbNext, "Next"}, {gtd.VerbWaiting, "Waiting"}, {gtd.VerbSchedule, "Schedule"}, {gtd.VerbSomeday, "Someday"},
	{gtd.VerbReference, "Reference"}, {gtd.VerbFocus, "Focus"}, {gtd.VerbProject, "Project"}, {gtd.VerbApprove, "Approve"},
	{gtd.VerbDelegate, "Delegate"}, {gtd.VerbDone, "Done"}, {gtd.VerbDismiss, "Dismiss"}, {gtd.VerbSeen, "Seen"},
	{gtd.VerbContext, "Context"}, {gtd.VerbArea, "Area"}, {gtd.VerbAnswer, "Answer"}, {gtd.VerbEdit, "Edit"},
	{gtd.VerbRetry, "Retry"}, {gtd.VerbReviewed, "Reviewed"}, {gtd.VerbSequential, "Sequential"}, {gtd.VerbReply, "Reply"},
}

// bridged are the verbs an entry with units runs as its unit's action when no handler takes them.
var bridged = []gtd.Verb{gtd.VerbApprove, gtd.VerbDelegate, gtd.VerbDismiss}

// delegableKinds are the kinds of entry with units whose delegate runs as their unit's action; an action entry
// bridges every verb of bridged.
var delegableKinds = []gtd.Kind{gtd.KindAction, gtd.KindWaiting, gtd.KindClose, gtd.KindCapture}

// bridges says whether verb on e runs as its first unit's action, a handler aside.
func bridges(e gtd.Entry, verb gtd.Verb) bool {
	if len(e.Units) == 0 || !slices.Contains(bridged, verb) {
		return false
	}
	return e.Kind == gtd.KindAction || verb == gtd.VerbDelegate && slices.Contains(delegableKinds, e.Kind)
}

var errGTDUnavailable = errors.New("the entries are not served: the daemon runs without a store")

func verbKey(v gtd.Verb) string {
	for _, b := range keys.Core {
		if b.Context == keys.List && b.Action == string(v) {
			return b.Key
		}
	}
	return ""
}

func knownVerb(v string) bool {
	return slices.ContainsFunc(gtdVerbs, func(x struct {
		verb  gtd.Verb
		label string
	}) bool {
		return string(x.verb) == v
	})
}

func verbNames() string {
	names := make([]string, len(gtdVerbs))
	for i, v := range gtdVerbs {
		names[i] = string(v.verb)
	}
	return strings.Join(names, ", ")
}

func (s *Server) gtd(w http.ResponseWriter) (*coregtd.Service, bool) {
	if s.deps.GTD == nil {
		writeError(w, http.StatusServiceUnavailable, errGTDUnavailable.Error())
		return nil, false
	}
	return s.deps.GTD, true
}

// writeGTDError answers a gtd service error with its status.
func writeGTDError(w http.ResponseWriter, err error) {
	var disabled pluginOff
	switch {
	case errors.As(err, &disabled):
		writePluginDisabled(w, disabled.provider)
	case errors.Is(err, coregtd.ErrArgs):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, coregtd.ErrNoEntry), errors.Is(err, messages.ErrNoMessage):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, coregtd.ErrVerb), errors.Is(err, coregtd.ErrPluginDisabled), errors.Is(err, messages.ErrClosed), errors.Is(err, turn.ErrClosed):
		writeError(w, http.StatusConflict, err.Error())
	default:
		writeActionError(w, err)
	}
}

func (s *Server) handleGTD(w http.ResponseWriter, r *http.Request) {
	g, ok := s.gtd(w)
	if !ok {
		return
	}
	cat := api.GTDCatalog{Views: slices.Clone(gtdViews), Areas: slices.Clone(s.deps.Cfg.GTD.Areas), Contexts: slices.Clone(s.deps.Cfg.GTD.Contexts)}
	if g.Serves(gtd.ViewReview) {
		cat.Views = append(cat.Views, reviewView)
	}
	cat.Areas, cat.Contexts = nonNil(cat.Areas), nonNil(cat.Contexts)
	for _, v := range gtdVerbs {
		cat.Verbs = append(cat.Verbs, api.GTDVerb{Verb: string(v.verb), Label: v.label, Key: verbKey(v.verb)})
	}
	writeJSON(w, cat)
}

func (s *Server) handleEntries(w http.ResponseWriter, r *http.Request) {
	g, ok := s.gtd(w)
	if !ok {
		return
	}
	q := r.URL.Query()
	view := gtd.View(q.Get("view"))
	if !slices.ContainsFunc(append(slices.Clone(gtdViews), reviewView), func(v api.GTDView) bool { return v.ID == string(view) }) {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("unknown view %q", view))
		return
	}
	since, err := api.ParseSince(q.Get("since"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	query := gtd.Query{View: view, Area: q.Get("area")}
	for _, c := range strings.Split(q.Get("context"), ",") {
		if c = strings.TrimSpace(c); c != "" {
			query.Contexts = append(query.Contexts, c)
		}
	}
	ctx := r.Context()
	p, err := g.EntriesSince(ctx, query, coregtd.Since{Epoch: since.Epoch, Rev: since.Rev})
	if err != nil {
		writeGTDError(w, err)
		return
	}
	page := api.EntriesPage{Epoch: p.Epoch, Rev: p.Rev, Full: p.Full, Entries: []api.EntryView{}, Removed: []string{}, Counters: wireCounters(p.Counters)}
	vs := s.entryViews(ctx, g, false)
	for _, e := range p.Entries {
		v, err := vs.view(ctx, e)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		page.Entries = append(page.Entries, v)
	}
	for _, id := range p.Removed {
		page.Removed = append(page.Removed, string(id))
	}
	writeJSON(w, page)
}

func (s *Server) handleEntry(w http.ResponseWriter, r *http.Request) {
	g, ok := s.gtd(w)
	if !ok {
		return
	}
	ctx := r.Context()
	e, err := g.Entry(ctx, gtd.ID(r.PathValue("id")))
	if err != nil {
		writeGTDError(w, err)
		return
	}
	v, err := s.entryViews(ctx, g, true).view(ctx, e)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, v)
}

func (s *Server) handleCapture(w http.ResponseWriter, r *http.Request) {
	g, ok := s.gtd(w)
	if !ok {
		return
	}
	var req api.CaptureRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, entriesBodyMax)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	if req.Unit != nil && s.refusePlugin(w, *req.Unit) {
		return
	}
	ctx := r.Context()
	e, err := g.Capture(ctx, req.Title, req.Notes, req.Unit)
	if err != nil {
		writeGTDError(w, err)
		return
	}
	s.writeEntry(w, ctx, g, e)
}

func (s *Server) handleEntryVerb(w http.ResponseWriter, r *http.Request) {
	var req api.EntryRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, entriesBodyMax)).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	s.runEntryVerb(w, r, r.PathValue("verb"), req)
}

func (s *Server) runEntryVerb(w http.ResponseWriter, r *http.Request, verb string, req api.EntryRequest) {
	g, ok := s.gtd(w)
	if !ok {
		return
	}
	if !knownVerb(verb) {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("unknown verb %q; one of %s", verb, verbNames()))
		return
	}
	ctx := context.WithoutCancel(r.Context())
	e, err := g.Entry(ctx, gtd.ID(r.PathValue("id")))
	if err != nil {
		writeGTDError(w, err)
		return
	}
	v := gtd.Verb(verb)
	if v == gtd.VerbDismiss && s.pluginsOff(e) {
		res, err := g.Do(ctx, e.ID, coregtd.Request{Verb: v, Args: req.Args, Source: req.Source})
		s.writeEntryResult(ctx, w, g, res, err)
		return
	}
	for _, u := range e.Units {
		if s.refusePlugin(w, u) {
			return
		}
	}
	if v == gtd.VerbReply && !g.Handles(e.Kind, v) {
		s.runReply(ctx, w, g, e, req)
		return
	}
	if b, ok := s.bridge(ctx, g, e, v); ok {
		st, ok := b.runs()
		if !ok {
			writeError(w, http.StatusConflict, b.reason())
			return
		}
		s.runBridge(w, r, g, e, b.unit, st, req)
		return
	}
	res, err := g.Do(ctx, e.ID, coregtd.Request{Verb: v, Args: req.Args, Source: req.Source})
	s.writeEntryResult(ctx, w, g, res, err)
}

func (s *Server) writeEntryResult(ctx context.Context, w http.ResponseWriter, g *coregtd.Service, res coregtd.Result, err error) {
	if err != nil {
		writeGTDError(w, err)
		return
	}
	view, err := s.entryViews(ctx, g, false).view(ctx, res.Entry)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, api.EntryResult{Entry: view, Lines: res.Lines})
}

// bridgeAction are the row's actions a verb may run on a unit: those of the highest rank the row offers whose gtd
// verb matches, enabled or not (see rank). Empty when the row offers none.
type bridgeAction struct {
	unit unit.Unit
	verb gtd.Verb
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
func (s *Server) bridge(ctx context.Context, g *coregtd.Service, e gtd.Entry, verb gtd.Verb) (bridgeAction, bool) {
	if !bridges(e, verb) || g.Handles(e.Kind, verb) {
		return bridgeAction{}, false
	}
	u := e.Units[0]
	b := bridgeAction{unit: u, verb: verb}
	rd, found, err := s.readRow(ctx, u)
	if err != nil {
		s.logf("entries: the row of %s: %v", u.Label(), err)
	}
	if found {
		b = s.offeredFor(rd.Row, verb)
	}
	if verb == gtd.VerbDismiss && len(b.top) == 0 {
		return bridgeAction{}, false
	}
	return b, true
}

// offeredFor are row's actions that verb may run at the highest rank the row offers (catalog.Rank); a lower
// rank never runs while a higher one is offered.
func (s *Server) offeredFor(row api.Row, verb gtd.Verb) bridgeAction {
	return rankOffered(s.servedSpecs(), row, verb)
}

func rankOffered(served []api.ActionSpec, row api.Row, verb gtd.Verb) bridgeAction {
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
// entry's items on u, and the guidance and mode of the entry's args.
func (s *Server) runBridge(w http.ResponseWriter, r *http.Request, g *coregtd.Service, e gtd.Entry, u unit.Unit, st api.ActionState, req api.EntryRequest) {
	mode := req.Args["mode"]
	if err := s.bridgeMode(st.ID, mode); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if mode == string(turn.ModeSession) && u.Provider != unit.GitHub && s.deps.Placer != nil {
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
	}{api.ActionRequest{Basis: st.Basis, Source: req.Source, Guidance: req.Args["guidance"], Items: its, Entry: string(e.ID)}, mode})
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
		for k, vs := range rec.header {
			w.Header()[k] = vs
		}
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
	now, err := g.Entry(ctx, e.ID)
	if err != nil {
		now = e
	}
	view, err := s.entryViews(ctx, g, false).view(ctx, now)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	lines := res.Output
	if res.Intake != nil {
		lines = append(lines, fmt.Sprintf("%s accepted as intake %d", st.ID, res.Intake.Seq))
	}
	writeJSON(w, api.EntryResult{Entry: view, Lines: lines})
}

// bridgeMode refuses a mode the action does not run in; "" leaves the choice to the action.
func (s *Server) bridgeMode(action, mode string) error {
	switch mode {
	case "":
		return nil
	case string(turn.ModeHeadless), string(turn.ModeSession):
	default:
		return fmt.Errorf("mode is %s or %s, not %q", turn.ModeHeadless, turn.ModeSession, mode)
	}
	d, ok := s.loaded().Registry.Get(dintent.Name(action))
	if !ok || !d.Runs(mode) {
		return fmt.Errorf("%s does not run in %s mode; %s", action, mode, otherMode(mode))
	}
	return nil
}

// delegateModes are the modes action runs in on u; a session is disabled with the placer's reason when u
// has nowhere to run one.
func (s *Server) delegateModes(ctx context.Context, u unit.Unit, action string) []api.ModeState {
	d, ok := s.loaded().Registry.Get(dintent.Name(action))
	if !ok {
		return nil
	}
	var out []api.ModeState
	for _, m := range []turn.Mode{turn.ModeHeadless, turn.ModeSession} {
		if !d.Runs(string(m)) {
			continue
		}
		ms := api.ModeState{Mode: string(m), Enabled: true}
		if m == turn.ModeSession && u.Provider != unit.GitHub && s.deps.Placer != nil {
			if _, err := s.deps.Placer.SessionPlace(ctx, u); err != nil {
				ms.Enabled, ms.Reason = false, err.Error()
			}
		}
		out = append(out, ms)
	}
	return out
}

func otherMode(mode string) string {
	if mode == string(turn.ModeHeadless) {
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
	g, ok := s.gtd(w)
	if !ok {
		return
	}
	c, err := g.Counts(r.Context(), r.URL.Query().Get("area"))
	if err != nil {
		writeGTDError(w, err)
		return
	}
	writeJSON(w, wireCounters(c))
}

func (s *Server) writeEntry(w http.ResponseWriter, ctx context.Context, g *coregtd.Service, e gtd.Entry) {
	v, err := s.entryViews(ctx, g, false).view(ctx, e)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, v)
}

// counters are the Area "" counters a change or a rows page carries; nil without the entries.
func (s *Server) counters(ctx context.Context) *api.Counters {
	if s.deps.GTD == nil {
		return nil
	}
	c, err := s.deps.GTD.Counts(ctx, "")
	if err != nil {
		s.logf("entries: counters: %v", err)
		return nil
	}
	out := wireCounters(c)
	return &out
}

func wireCounters(c gtd.Counters) api.Counters {
	out := api.Counters{Area: c.Area, Views: map[string]int{}, Areas: map[string]int{}, Contexts: map[string]int{}, Badge: c.Badge}
	for v, n := range c.Views {
		if v != gtd.ViewReview && v != gtd.ViewOpen {
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

// entryViewer maps entries to their wire views; detail expands the cards and the items.
type entryViewer struct {
	s        *Server
	g        *coregtd.Service
	detail   bool
	projects map[gtd.ID]string
	drafts   []draft.Draft
	status   map[gtd.ID]coregtd.ProjectState
	// whereBy bounds every draft destination lookup of this page.
	whereBy time.Time
}

func (s *Server) entryViews(_ context.Context, g *coregtd.Service, detail bool) *entryViewer {
	return &entryViewer{s: s, g: g, detail: detail, projects: map[gtd.ID]string{}, whereBy: time.Now().Add(s.whereBudget())}
}

func (ev *entryViewer) view(ctx context.Context, e gtd.Entry) (api.EntryView, error) {
	v := api.EntryView{ID: string(e.ID), Kind: string(e.Kind), State: string(e.State), Title: e.Title, Notes: e.Notes,
		Focus: e.Focus.On, FocusAuto: e.Focus.Auto, Contexts: nonNil(slices.Clone(e.Contexts)), Area: e.Area, Project: string(e.Project),
		Deadline: e.Deadline, Changed: e.Changed, Units: slices.Clone(e.Units), Items: []api.ItemView{}, Created: e.Created, Updated: e.Updated}
	if v.Units == nil {
		v.Units = []unit.Unit{}
	}
	v.UnitLabels = ev.s.unitLabels(v.Units)
	if e.State == gtd.Scheduled {
		v.SnoozeUntil, v.SnoozeOnActivity, v.Muted = e.Snooze.Until, e.Snooze.OnActivity, e.Snooze.Mute
	}
	if e.Waiting != nil {
		w := &api.WaitingView{On: []api.WaitingPerson{}, Since: e.Waiting.Since, LastNudge: e.Waiting.LastNudge, Resurface: e.Waiting.Resurface}
		for _, p := range e.Waiting.On {
			w.On = append(w.On, api.WaitingPerson{Name: cmp.Or(p.Name, p.Key)})
		}
		v.Waiting = w
	}
	if e.Project != "" {
		title, ok := ev.projects[e.Project]
		if !ok {
			if p, err := ev.g.Entry(ctx, e.Project); err == nil {
				title = p.Title
			}
			ev.projects[e.Project] = title
		}
		v.ProjectTitle = title
	}
	if err := ev.items(ctx, e, &v); err != nil {
		return api.EntryView{}, err
	}
	if ev.detail {
		if err := ev.cards(ctx, e, &v); err != nil {
			return api.EntryView{}, err
		}
	}
	msg, err := ev.s.entryMessage(ctx, string(e.ID))
	if err != nil {
		return api.EntryView{}, err
	}
	v.Message = msg
	v.Attention = ev.attention(ctx, e)
	v.Verbs = ev.verbs(ctx, e)
	if err := ev.project(ctx, e, &v); err != nil {
		return api.EntryView{}, err
	}
	if err := ev.s.withDraft(whereBy(ctx, ev.whereBy), e, &v); err != nil {
		return api.EntryView{}, err
	}
	return v, nil
}

func (ev *entryViewer) items(ctx context.Context, e gtd.Entry, v *api.EntryView) error {
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

func (ev *entryViewer) cards(ctx context.Context, e gtd.Entry, v *api.EntryView) error {
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
func (ev *entryViewer) verbs(ctx context.Context, e gtd.Entry) []api.VerbState {
	out := make([]api.VerbState, 0, len(gtdVerbs))
	off := ""
	for _, u := range e.Units {
		if !ev.s.pluginEnabled(u.Provider) {
			off = string(u.Provider) + " is disabled"
			break
		}
	}
	dismissable := ev.s.pluginsOff(e)
	var row *api.Row
	if len(e.Units) > 0 {
		if rd, ok, err := ev.s.readModel().Read(ctx, e.Units[0]); err == nil && ok {
			row = &rd.Row
		}
	}
	offered := func(row *api.Row, verb gtd.Verb) bridgeAction {
		if row == nil {
			return bridgeAction{verb: verb}
		}
		return ev.s.offeredFor(*row, verb)
	}
	for _, x := range gtdVerbs {
		st := api.VerbState{Verb: string(x.verb)}
		switch {
		case dismissable && x.verb == gtd.VerbDismiss:
			st.Enabled = coregtd.Accepts(e, x.verb) == nil
		case off != "":
			st.Reason = off
		case ev.g.Handles(e.Kind, x.verb):
			st.Enabled = true
		case x.verb == gtd.VerbReply:
			st.Reason = ev.s.replyRefusal(e)
			st.Enabled = st.Reason == ""
		case bridges(e, x.verb) && (x.verb != gtd.VerbDismiss || len(offered(row, x.verb).top) > 0):
			b := offered(row, x.verb)
			b.unit = e.Units[0]
			var runs api.ActionState
			runs, st.Enabled = b.runs()
			if !st.Enabled {
				st.Reason = b.reason()
			} else if x.verb == gtd.VerbDelegate {
				st.Modes = ev.s.delegateModes(ctx, b.unit, runs.ID)
			}
		default:
			if err := coregtd.Accepts(e, x.verb); err != nil {
				st.Reason = err.Error()
			} else {
				st.Enabled = true
			}
		}
		out = append(out, st)
	}
	return out
}

// syncEntries runs Sync once per burst of item, source-event and card notes, on its own goroutine, until ctx
// ends: everything at start, after an All note and when the user's identity grows, and each minute for the
// snoozes and stars that come due.
func (s *Server) syncEntries(ctx context.Context) {
	g := s.deps.GTD
	var poke <-chan struct{}
	var from port.Generation
	if s.deps.Changes != nil {
		p, cancel := change.Poke(s.deps.Changes, syncInputs)
		defer cancel()
		poke = p
	}
	tick := time.NewTicker(syncTick)
	defer tick.Stop()
	all, settled := true, s.deps.GTDMe == nil
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
			grew, err := s.deps.GTDMe(ctx)
			switch {
			case err == nil:
				idErr = ""
			case err.Error() != idErr:
				idErr = err.Error()
				s.logf("entries: identity: %v", err)
			}
			settled, all = err == nil, all || grew
		}
		if all && s.deps.GTDUnits != nil {
			us, err := s.deps.GTDUnits(ctx)
			if err != nil {
				s.logf("entries: units: %v", err)
			}
			units = append(units, us...)
		}
		units = append(units, retry...)
		all, retry = false, nil
		sctx, scope := meter.Start(ctx, meter.Loop("entries"))
		if err := g.Sync(sctx, units); err != nil && ctx.Err() == nil {
			s.logf("entries: sync: %v", err)
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

// feedEntries publishes a change with the counters after each burst of entry and draft notes, naming the units
// of the entries that moved, until ctx ends.
func (s *Server) feedEntries(ctx context.Context) {
	if s.deps.Changes == nil {
		return
	}
	poke, cancel := change.Poke(s.deps.Changes, entryInputs)
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
		c := s.deps.Changes.Since(from, entryInputs)
		from = c.Gen
		pctx, scope := meter.Start(ctx, meter.Loop("entries-feed"))
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
	known := s.readModel().Labels(units)
	out := make([]string, len(units))
	for i, u := range units {
		out[i] = cmp.Or(known[u], u.Label())
	}
	return out
}

func (s *Server) unitLabel(u unit.Unit) string { return s.unitLabels([]unit.Unit{u})[0] }
