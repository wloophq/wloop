package daemon

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/catalog"
	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/core/port"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/meter"
)

const (
	draftsBodyMax = 64 << 10
	// With no draft note, the sender still wakes each draftWatchdog.
	draftWatchdog = 5 * time.Minute
	// draftWhereTimeout bounds one destination lookup and draftWhereBudget a page's; a lookup that failed or ran
	// out of time is not tried again for draftWhereRetry, so a held Slack never slows each refresh.
	draftWhereTimeout = 3 * time.Second
	draftWhereBudget  = 5 * time.Second
	draftWhereRetry   = time.Minute
)

var errOutboxUnavailable = unavailable{"the Outbox is not served: the daemon runs without a store"}

// mayHaveBeenPosted starts a publisher's error when the post may have reached its provider (#publisher).
const mayHaveBeenPosted = "may have been posted"

// draftHandlers are the handlers a draft task's verbs run, names of builtin.VerbHandlers, by the draft action
// each runs.
var draftHandlers = map[string]string{
	"approve":             "approve",
	"edit":                "edit",
	coretasks.HandlerDrop: "discard",
	"retry":               "retry",
}

// registerOutbox serves the views over gates, a draft gate as its draft's task, and makes a draft task's verbs
// act on its draft.
func (s *Server) registerOutbox() {
	if s.deps.Tasks == nil || s.deps.Outbox == nil {
		return
	}
	for handler := range draftHandlers {
		s.deps.Tasks.Register(task.OriginDraft, handler, s.draftVerb(handler))
	}
	s.deps.Tasks.RegisterView(task.OverGates, draftPrefix, gatesView{s})
}

// handleDrafts lists the drafts the query names, or the Outbox's: those whose gate is open, and those whose gate
// approved them and whose delivery failed or a restart has yet to settle.
func (s *Server) handleDrafts(w http.ResponseWriter, r *http.Request) {
	ctx := whereBy(r.Context(), time.Now().Add(s.whereBudget()))
	o := s.deps.Outbox
	if o == nil {
		writeActionError(w, errOutboxUnavailable)
		return
	}
	q := r.URL.Query()
	var f outbox.Filter
	for _, st := range splitList(q.Get("state")) {
		if !slices.Contains(draft.States, draft.State(st)) {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("unknown draft state %q", st))
			return
		}
		f.States = append(f.States, draft.State(st))
	}
	var ids []draft.ID
	for _, raw := range splitList(q.Get("id")) {
		id, ok := parseDraftID(raw)
		if !ok {
			writeError(w, http.StatusBadRequest, "no draft "+strconv.Quote(raw))
			return
		}
		ids = append(ids, id)
	}
	f.Task = q.Get("task")
	var ps []outbox.Pending
	var err error
	switch {
	case len(ids) > 0:
		ps, err = s.draftsByID(r.Context(), ids)
	case len(f.States) == 0 && f.Task == "":
		ps, err = o.Outbox(r.Context())
	default:
		ps, err = s.draftsWithGates(r.Context(), f)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, api.DraftsResponse{Drafts: s.draftDetails(ctx, ps)})
}

func splitList(raw string) []string {
	var out []string
	for _, v := range strings.Split(raw, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// draftsWithGates are the drafts f selects, each with its gate.
func (s *Server) draftsWithGates(ctx context.Context, f outbox.Filter) ([]outbox.Pending, error) {
	ds, err := s.deps.Outbox.List(ctx, f)
	if err != nil {
		return nil, err
	}
	return s.withGates(ctx, ds)
}

// draftsByID are drafts ids that exist, each with its gate.
func (s *Server) draftsByID(ctx context.Context, ids []draft.ID) ([]outbox.Pending, error) {
	var ds []draft.Draft
	for _, id := range ids {
		d, err := s.deps.Outbox.Store.Get(ctx, id)
		if errors.Is(err, outbox.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		ds = append(ds, d)
	}
	return s.withGates(ctx, ds)
}

func (s *Server) withGates(ctx context.Context, ds []draft.Draft) ([]outbox.Pending, error) {
	out := make([]outbox.Pending, 0, len(ds))
	for _, d := range ds {
		g, _, err := s.deps.Outbox.GateOf(ctx, d)
		if err != nil {
			return nil, err
		}
		out = append(out, outbox.Pending{Draft: d, Gate: g})
	}
	return out, nil
}

func parseDraftID(raw string) (draft.ID, bool) {
	n, err := strconv.ParseInt(raw, 10, 64)
	return draft.ID(n), err == nil && n > 0
}

// noPublisher refuses a draft whose provider's plugin is enabled but publishes nothing.
type noPublisher struct {
	provider unit.ProviderID
	err      error
}

func (n noPublisher) Error() string { return n.err.Error() }
func (n noPublisher) Unwrap() error { return n.err }

// draftAction is what a draft verb takes: Text and Payload an approval posts or an edit sets, Broadcast an
// edit's turn of a thread reply's broadcast, Reason a discard's, Confirm a retry's of a draft that may have been
// posted, Rev the draft's version the caller showed.
type draftAction struct {
	Text      *string
	Payload   *json.RawMessage
	Broadcast *bool
	Reason    string
	Confirm   bool
	Rev       *int64
}

// draftDo runs action on draft id as actor: refused with pluginOff when the draft's plugin is disabled, but for
// discard. Approving and discarding a held draft resolve its gate.
func (s *Server) draftDo(ctx context.Context, id draft.ID, action string, a draftAction, actor requester) (draft.Draft, error) {
	s.draftMu.Lock()
	defer s.draftMu.Unlock()
	o := s.deps.Outbox
	d, err := o.Store.Get(ctx, id)
	if err != nil {
		return draft.Draft{}, err
	}
	publisher := o.PublisherOf(d)
	if action != "discard" && !s.pluginEnabled(publisher) {
		return draft.Draft{}, pluginOff{publisher}
	}
	if a.Rev != nil && *a.Rev != d.Rev {
		return draft.Draft{}, fmt.Errorf("%w: draft %d changed since you saw it (rev %d, now %d)", outbox.ErrConflict, id, *a.Rev, d.Rev)
	}
	var got draft.Draft
	switch action {
	case "approve":
		if a.Rev == nil {
			return draft.Draft{}, badRequest{"approve takes the rev you saw"}
		}
		got, err = s.decideDraft(ctx, d, gate.Approved, draftEdit{a.Text, a.Payload, a.Rev}, actor)
	case "edit":
		if a.Text == nil && a.Payload == nil && a.Broadcast == nil {
			return draft.Draft{}, badRequest{"edit takes the draft's new text, payload or broadcast"}
		}
		if a.Rev == nil {
			return draft.Draft{}, badRequest{"edit takes the rev you saw"}
		}
		if err := agentRevises(id, actor); err != nil {
			return draft.Draft{}, err
		}
		if err := s.checkEditable(d, a.Text, a.Payload); err != nil {
			return draft.Draft{}, err
		}
		got, err = s.amend(ctx, id, personAmendment(a.Text, a.Payload, a.Broadcast, a.Rev))
	case "discard":
		if d.State == draft.Held {
			got, err = s.decideDraft(ctx, d, gate.Rejected, draftEdit{}, actor)
		} else {
			got, err = o.Discard(ctx, id, a.Reason)
		}
	case "retry":
		if a.Rev == nil {
			return draft.Draft{}, badRequest{"retry takes the rev you saw"}
		}
		got, err = o.Retry(ctx, id, a.Confirm)
		s.noteStale(id, d.Rev, err)
	default:
		return draft.Draft{}, badRequest{fmt.Sprintf("unknown draft action %q", action)}
	}
	if errors.Is(err, outbox.ErrDisabled) {
		err = noPublisher{publisher, err}
	}
	return got, err
}

// draftEdit is what a person changes on a draft as they approve it; nil leaves it.
type draftEdit struct {
	Text    *string
	Payload *json.RawMessage
	// Rev is the rev the person saw; an edit that changes the draft needs it.
	Rev *int64
}

// changes says whether e changes d.
func (e draftEdit) changes(d draft.Draft) bool {
	edited := d
	if e.Text != nil {
		edited.Text = *e.Text
	}
	if e.Payload != nil {
		edited.Payload = *e.Payload
	}
	return !draft.SameContent(d, edited)
}

// amend is a person's edit of draft id: the Outbox's, unless a test replaces it.
func (s *Server) amend(ctx context.Context, id draft.ID, a outbox.Amendment) (draft.Draft, error) {
	if s.amender != nil {
		return s.amender(ctx, id, a)
	}
	return s.deps.Outbox.Amend(ctx, id, a)
}

// decideDraft resolves the open gate of held draft d, as actor, with its first intent gates.toml gives the status
// want; an approval posts edit's text and payload when they are set. It runs under draftMu.
func (s *Server) decideDraft(ctx context.Context, d draft.Draft, want gate.Status, edit draftEdit, actor requester) (draft.Draft, error) {
	o := s.deps.Outbox
	if s.deps.Gates == nil {
		return draft.Draft{}, errGatesUnavailable
	}
	g, ok, err := o.GateOf(ctx, d)
	switch {
	case err != nil:
		return draft.Draft{}, err
	case !ok:
		return draft.Draft{}, fmt.Errorf("%w: draft %d has no gate", gates.ErrNoGate, d.ID)
	case g.Status != gate.Open:
		return draft.Draft{}, fmt.Errorf("%w: the gate of draft %d is %s", gates.ErrResolved, d.ID, g.Status)
	}
	intent, ok := s.gateIntentOf(g, want)
	if !ok {
		return draft.Draft{}, fmt.Errorf("%w: gate %s offers no %s intent", gates.ErrIntent, g.Name, want)
	}
	if err := s.resolveDraftGate(ctx, g, intent, d.Rev, edit, actor); err != nil {
		return draft.Draft{}, err
	}
	return o.Store.Get(ctx, d.ID)
}

// resolveDraftGate resolves draft gate g with intent as actor, under draftMu. Only the user, a person or an agent
// through a session they drive that did not propose the draft, resolves a draft's gate here, refused before
// anything is written. An approval readies its draft at rev; with text or a payload other than the draft's, it
// edits the draft at rev first, which supersedes g, and approves the gate that edit opened.
func (s *Server) resolveDraftGate(ctx context.Context, g gate.Gate, intent string, rev int64, edit draftEdit, actor requester) error {
	switch {
	case !actor.user():
		return fmt.Errorf("%w: draft gate %d %s", gates.ErrRefused, g.ID, unattended("is resolved: it"))
	case actor.Actor == task.ActorAgent && g.Session == actor.Session:
		return fmt.Errorf("%w: draft gate %d holds a draft session %s proposed, which it does not resolve", gates.ErrRefused, g.ID, actor.Session)
	}
	if (edit.Text != nil || edit.Payload != nil) && edit.Rev == nil {
		return badRequest{"an approval that changes the draft takes the rev you saw"}
	}
	if g.Status != gate.Open {
		return fmt.Errorf("%w: draft gate %d is %s", gates.ErrResolved, g.ID, g.Status)
	}
	if st, _ := s.intentStatus(g, intent); st == gate.Approved {
		o := s.deps.Outbox
		n, err := strconv.ParseInt(g.Subject, 10, 64)
		if err != nil {
			return fmt.Errorf("draft gate %d names no draft: %q", g.ID, g.Subject)
		}
		id := draft.ID(n)
		d, err := o.Prepare(ctx, id, rev)
		if err != nil {
			s.noteStale(id, rev, err)
			return err
		}
		if edit.changes(d) {
			if err := agentRevises(id, actor); err != nil {
				return err
			}
			if err := s.checkEditable(d, edit.Text, edit.Payload); err != nil {
				return err
			}
			if d, err = s.amend(ctx, id, personAmendment(edit.Text, edit.Payload, nil, &rev)); err != nil {
				return err
			}
			ng, ok, err := o.GateOf(ctx, d)
			switch {
			case err != nil:
				return err
			case !ok || ng.Status != gate.Open:
				return fmt.Errorf("%w: draft %d has no open gate after its edit", outbox.ErrConflict, id)
			}
			if _, err := o.Prepare(ctx, id, d.Rev); err != nil {
				return err
			}
			g = ng
		}
	}
	return s.deps.Gates.Resolve(ctx, g.ID, gates.Resolution{Intent: intent, By: actor.Actor, Session: actor.Session})
}

// gateIntentOf is the first intent g offers that gates.toml resolves to want.
func (s *Server) gateIntentOf(g gate.Gate, want gate.Status) (string, bool) {
	for _, name := range g.Intents {
		if st, ok := s.intentStatus(g, name); ok && st == want {
			return name, true
		}
	}
	return "", false
}

// intentStatus is the status gates.toml gives g's intent name.
func (s *Server) intentStatus(g gate.Gate, name string) (gate.Status, bool) {
	def, ok := s.loaded().Gates.Gate(g.Name)
	if !ok {
		return "", false
	}
	for _, in := range def.Intents {
		if in.Name == name {
			return in.Status, true
		}
	}
	return "", false
}

var errGatesUnavailable = unavailable{"gates are not served: the daemon runs without a store"}

func writeDraftError(w http.ResponseWriter, err error) {
	var off pluginOff
	var none noPublisher
	switch {
	case errors.As(err, &off):
		writePluginDisabled(w, off.provider)
	case errors.As(err, &none):
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": none.Error(), "plugin": string(none.provider)})
	case errors.Is(err, outbox.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, outbox.ErrNeedsConfirm):
		writeError(w, http.StatusConflict, err.Error()+": pass confirm once you checked it was not posted")
	case errors.Is(err, outbox.ErrConflict), errors.Is(err, outbox.ErrStale), errors.Is(err, gates.ErrResolved):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, gates.ErrRefused):
		writeError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, gates.ErrNoGate):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, outbox.ErrInvalid):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeActionError(w, err)
	}
}

// draftVerb runs the verb of handler on a draft task's draft, with the statuses the draft routes answer.
func (s *Server) draftVerb(handler string) coretasks.VerbHandler {
	return func(ctx context.Context, e task.Task, req coretasks.Request) (coretasks.Result, error) {
		id, ok := draftIDOf(e.ID)
		if !ok {
			return coretasks.Result{}, fmt.Errorf("%w: %s", coretasks.ErrNoTask, e.ID)
		}
		var a draftAction
		if text, ok := req.Args["text"]; ok {
			a.Text = &text
		}
		if raw, ok := req.Args["payload"]; ok {
			if !json.Valid([]byte(raw)) {
				return coretasks.Result{}, fmt.Errorf("%w: payload is the draft's payload as JSON, not %q", coretasks.ErrArgs, raw)
			}
			p := json.RawMessage(raw)
			a.Payload = &p
		}
		a.Reason = req.Args["reason"]
		if raw, ok := req.Args["broadcast"]; ok {
			b, err := strconv.ParseBool(raw)
			if err != nil {
				return coretasks.Result{}, fmt.Errorf("%w: broadcast is true or false, not %q", coretasks.ErrArgs, raw)
			}
			a.Broadcast = &b
		}
		if raw, ok := req.Args["confirm"]; ok {
			b, err := strconv.ParseBool(raw)
			if err != nil {
				return coretasks.Result{}, fmt.Errorf("%w: confirm is true or false, not %q", coretasks.ErrArgs, raw)
			}
			a.Confirm = b
		}
		if raw, ok := req.Args["rev"]; ok {
			n, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || n < 0 {
				return coretasks.Result{}, fmt.Errorf("%w: rev is the draft's revision, not %q", coretasks.ErrArgs, raw)
			}
			a.Rev = &n
		}
		d, err := s.draftDo(ctx, id, draftHandlers[handler], a, requestOf(req))
		if err != nil {
			return coretasks.Result{}, taskDraftError(err)
		}
		got, err := s.deps.Tasks.Task(ctx, e.ID)
		if err != nil {
			return coretasks.Result{}, err
		}
		return coretasks.Result{Task: got, Lines: []string{fmt.Sprintf("draft %d is %s", d.ID, d.State)}}, nil
	}
}

// taskDraftError is a draft action's error as the tasks' routes answer it.
func taskDraftError(err error) error {
	var off pluginOff
	var bad badRequest
	switch {
	case errors.As(err, &off):
		return fmt.Errorf("%w: %s", coretasks.ErrPluginDisabled, off)
	case errors.Is(err, outbox.ErrNotFound):
		return fmt.Errorf("%w: %w", coretasks.ErrNoTask, err)
	case errors.Is(err, outbox.ErrNeedsConfirm):
		return fmt.Errorf("%w: %w: pass the confirm argument once you checked it was not posted", coretasks.ErrVerb, err)
	case errors.Is(err, outbox.ErrConflict), errors.Is(err, outbox.ErrStale), errors.Is(err, outbox.ErrDisabled), errors.Is(err, gates.ErrResolved),
		errors.Is(err, gates.ErrRefused), errors.Is(err, gates.ErrNoGate), errors.Is(err, gates.ErrIntent):
		return fmt.Errorf("%w: %w", coretasks.ErrVerb, err)
	case errors.Is(err, outbox.ErrInvalid), errors.As(err, &bad):
		return fmt.Errorf("%w: %w", coretasks.ErrArgs, err)
	}
	return err
}

// draftView is d on the wire, with g, its gate, and its actions as they stand.
// quotes says whether d, a per-person draft of a workflow posted in the direct conversation of the person it
// is to, quotes items of its unit, a conversation that person does not read.
func quotes(d draft.Draft) bool {
	return d.Target != "" && d.Origin.Kind == draft.OriginAuto && d.Origin.Key == d.Unit.String()+":"+d.Target
}

func (s *Server) draftView(ctx context.Context, d draft.Draft, g gate.Gate) api.Draft {
	v := api.Draft{ID: int64(d.ID), Unit: string(d.Unit.Provider) + "/" + d.Unit.Key, Items: slices.Clone(d.Items), Task: d.Task, Kind: string(d.Kind),
		Target: d.Target, Broadcast: string(d.Broadcast), Text: d.Text, Why: d.Why, Language: d.Language, Reaction: d.Reaction, Original: d.Original, Replaced: d.Replaced, Edited: d.Edited,
		Origin: api.DraftOrigin{Kind: string(d.Origin.Kind), TurnSeq: d.Origin.TurnSeq, Intent: d.Origin.Intent,
			Workflow: d.Origin.Workflow, Rule: d.Origin.Rule, Unit: originUnit(d.Origin)},
		State: string(d.State), Error: d.Error, Reason: d.Reason, Permalink: d.Evidence.Permalink, Gate: g.ID,
		Rev: d.Rev, CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt}
	v.Purpose, v.Delivery, v.Effects = d.Purpose, string(d.Delivery), slices.Clone(d.Effects)
	if v.Items == nil {
		v.Items = []string{}
	}
	if v.Effects == nil {
		v.Effects = []string{}
	}
	if g.Status == gate.Open && !g.Deadline.IsZero() {
		dl := g.Deadline
		v.Deadline = &dl
	}
	if g.Status == gate.Open {
		for _, name := range g.Intents {
			if st, ok := s.intentStatus(g, name); ok && v.Intents[string(st)] == "" {
				if v.Intents == nil {
					v.Intents = map[string]string{}
				}
				v.Intents[string(st)] = name
			}
		}
	}
	if d.State == draft.Dropped {
		v.ReasonText = s.loaded().Gates.Drafts.Reasons[d.Reason]
	}
	v.Actions = catalog.DraftStates(s.loaded(), s.draftFacts(ctx, d))
	v.Where = s.draftWhere(ctx, d)
	s.cardFields(ctx, d, g, &v)
	p := s.presentations([]unit.Unit{d.Unit})[d.Unit]
	v.Label, v.Title, v.Link = p.Label, p.Title, p.Link
	if quotes(d) {
		v.Quoted = strings.ReplaceAll(s.loaded().Gates.Drafts.Quoted, "{conversation}", cmp.Or(p.Title, p.Label, d.Unit.String()))
	}
	return v
}

// draftFacts are what d's actions depend on.
func (s *Server) draftFacts(ctx context.Context, d draft.Draft) catalog.DraftFacts {
	publisher := s.publisherOf(d)
	f := catalog.DraftFacts{State: string(d.State), Provider: publisher, Disabled: !s.pluginEnabled(publisher),
		MayHaveBeenPosted: d.Error == outbox.NotConfirmed || strings.HasPrefix(d.Error, mayHaveBeenPosted),
		Effects:           slices.Clone(d.Effects)}
	if d.Origin.Kind == draft.OriginAuto {
		f.Workflow = d.Origin.Workflow
	}
	if f.Workflow != "" {
		f.Skipped = slices.Contains(s.skippedOn(ctx, d.Unit), f.Workflow)
	}
	if o := s.deps.Outbox; o != nil && o.Effects != nil {
		_, err := o.Effects(publisher)
		f.NoPublisher = err != nil
	}
	return f
}

var mentionKey = regexp.MustCompile(`@\{(group:[^{}\n]+|[^{}\s]+)\}`)

// draftDetail is draftView with the names of the people the text mentions and the items it is about, read
// from the unit's items, which only a draft with items or a mention reads, once per unit into byUnit (nil
// keeps none).
func (s *Server) draftDetail(ctx context.Context, d draft.Draft, g gate.Gate, byUnit map[unit.Unit][]items.Item) api.Draft {
	v := s.draftView(ctx, d, g)
	if s.deps.Items == nil || (len(d.Items) == 0 && !mentionKey.MatchString(d.Text)) {
		return v
	}
	its := s.itemsOnce(ctx, byUnit, d.Unit)
	v.Names = mentionNames(d.Text, its)
	for _, id := range d.Items {
		iv := api.ItemView{Unit: d.Unit, ID: id}
		if i := slices.IndexFunc(its, func(it items.Item) bool { return it.ID == id }); i >= 0 {
			iv.Summary, iv.State, iv.Kind = its[i].Summary, string(its[i].State), string(its[i].Kind)
		}
		v.ItemDetails = append(v.ItemDetails, iv)
	}
	return v
}

// mentionNames are the names of the people text mentions, by the key it mentions them with, read from the
// requesters, assignees and people waited on of its; nil when it names nobody.
func mentionNames(text string, its []items.Item) map[string]string {
	mentions := mentionKey.FindAllStringSubmatch(text, -1)
	if len(mentions) == 0 {
		return nil
	}
	names := map[string]string{}
	for _, it := range its {
		for _, p := range append(append([]items.Person{it.Requester}, it.Assignees...), it.WaitingOn...) {
			for _, k := range append([]string{p.Key}, p.Aliases...) {
				if _, ok := names[k]; !ok && p.Name != "" && k != "" {
					names[k] = p.Name
				}
			}
		}
	}
	var out map[string]string
	for _, m := range mentions {
		if n, ok := names[m[1]]; ok {
			if out == nil {
				out = map[string]string{}
			}
			out[m[1]] = n
		}
	}
	return out
}

// draftDetails are the details of ps, each unit's items read once.
func (s *Server) draftDetails(ctx context.Context, ps []outbox.Pending) []api.Draft {
	byUnit := map[unit.Unit][]items.Item{}
	out := make([]api.Draft, 0, len(ps))
	for _, p := range ps {
		out = append(out, s.draftDetail(ctx, p.Draft, p.Gate, byUnit))
	}
	return out
}

// itemsOnce are u's items, read once into byUnit (nil keeps none); a failed read is logged and reads as none.
func (s *Server) itemsOnce(ctx context.Context, byUnit map[unit.Unit][]items.Item, u unit.Unit) []items.Item {
	if its, ok := byUnit[u]; ok {
		return its
	}
	its, err := s.deps.Items.Items(ctx, u)
	if err != nil {
		s.logf("drafts: the items of %s: %v", u.Label(), err)
	}
	if its == nil {
		its = []items.Item{}
	}
	if byUnit != nil {
		byUnit[u] = its
	}
	return its
}

// draftWhere is where a draft still to be posted goes, best effort: "" when its publisher cannot tell.
// A unit and target never change, so a resolved destination is kept while pages keep reading it; a failed lookup,
// or one the page had no time left for, is kept for draftWhereRetry. keepLookup forgets what went unread that long.
func (s *Server) draftWhere(ctx context.Context, d draft.Draft) string {
	if !slices.Contains(draft.Open, d.State) && d.State != draft.Failed {
		return ""
	}
	now := time.Now()
	if c, ok := s.draftWheres.Load(d.ID); ok {
		w := c.(whereSeen)
		if w.where != "" {
			s.keepLookup(&s.draftWheres, d.ID, whereSeen{where: w.where, at: now}, now)
			return w.where
		}
		if now.Sub(w.at) < draftWhereRetry {
			return ""
		}
	}
	loc := s.locator(s.publisherOf(d))
	if loc == nil {
		return ""
	}
	by := now.Add(s.whereTimeout())
	if page, ok := ctx.Value(whereByKey{}).(time.Time); ok && page.Before(by) {
		by = page
	}
	if !now.Before(by) {
		s.keepLookup(&s.draftWheres, d.ID, whereSeen{at: now}, now)
		return ""
	}
	ctx, cancel := context.WithDeadline(ctx, by)
	defer cancel()
	w, err := loc.Where(ctx, d)
	if err != nil || w == "" {
		s.keepLookup(&s.draftWheres, d.ID, whereSeen{at: time.Now()}, now)
		return ""
	}
	s.keepLookup(&s.draftWheres, d.ID, whereSeen{where: w, at: now}, now)
	return w
}

// whereSeen is a destination its publisher resolved and when a page last read it, or, with no where, when the
// lookup failed.
type whereSeen struct {
	where string
	at    time.Time
}

// keepLookup stores v under key in m, and at most once each draftWhereRetry forgets every draft destination and
// reply broadcast older than that, so neither cache outgrows what pages read.
func (s *Server) keepLookup(m *sync.Map, key, v any, now time.Time) {
	m.Store(key, v)
	last := s.lookupsSwept.Load()
	if now.UnixNano()-last < int64(draftWhereRetry) || !s.lookupsSwept.CompareAndSwap(last, now.UnixNano()) {
		return
	}
	for _, c := range []*sync.Map{&s.draftWheres, &s.replyBroadcasts} {
		c.Range(func(k, v any) bool {
			var at time.Time
			switch v := v.(type) {
			case whereSeen:
				at = v.at
			case broadcastSeen:
				at = v.at
			}
			if now.Sub(at) >= draftWhereRetry {
				c.Delete(k)
			}
			return true
		})
	}
}

type whereByKey struct{}

// whereBy makes by the deadline of every draft destination lookup under ctx.
func whereBy(ctx context.Context, by time.Time) context.Context {
	return context.WithValue(ctx, whereByKey{}, by)
}

func (s *Server) publisherOf(d draft.Draft) unit.ProviderID {
	if o := s.deps.Outbox; o != nil {
		return o.PublisherOf(d)
	}
	return d.Unit.Provider
}

func (s *Server) locator(p unit.ProviderID) port.Locator {
	o := s.deps.Outbox
	if o == nil || o.Effects == nil {
		return nil
	}
	fx, err := o.Effects(p)
	if err != nil {
		return nil
	}
	loc, _ := fx.(port.Locator)
	return loc
}

func (s *Server) whereTimeout() time.Duration { return cmp.Or(s.whereLookup, draftWhereTimeout) }

func (s *Server) whereBudget() time.Duration { return cmp.Or(s.wherePage, draftWhereBudget) }

// sendDrafts settles the drafts a crash left sending, then publishes the queued drafts, after each draft note and
// each draftWatchdog, until ctx ends. A draft's gate queues it; the gates' tick resolves the windows.
func (s *Server) sendDrafts(ctx context.Context) {
	o := s.deps.Outbox
	var poke <-chan struct{}
	if s.deps.Changes != nil {
		p, cancel := change.Poke(s.deps.Changes, port.TopicDrafts)
		defer cancel()
		poke = p
	}
	rctx, scope := meter.Start(ctx, meter.Loop("drafts"))
	if err := o.Recover(rctx); err != nil && ctx.Err() == nil {
		s.logf("drafts: recover: %v", err)
	}
	if err := o.PlaceBroadcasts(rctx); err != nil && ctx.Err() == nil {
		s.logf("drafts: placing broadcasts: %v", err)
	}
	s.adoptDrafts(rctx)
	scope.End()
	for {
		pctx, scope := meter.Start(ctx, meter.Loop("drafts"))
		sent, failed, err := o.Send(pctx)
		if err != nil && ctx.Err() == nil {
			s.logf("drafts: send: %v", err)
		}
		if sent+failed > 0 {
			s.logf("drafts: %d sent, %d failed", sent, failed)
		}
		scope.End()
		select {
		case <-ctx.Done():
			return
		case <-poke:
		case <-s.draftClock.After(draftWatchdog):
		}
	}
}

// adoptDrafts gives each held draft of no task the task its gate waits on, settling one whose task ended, and
// ends each task opened to hold gates whose draft gates all closed.
func (s *Server) adoptDrafts(ctx context.Context) {
	if s.deps.Tasks == nil {
		return
	}
	adopted, ended, err := s.deps.Outbox.Adopt(ctx, func(ctx context.Context, id task.ID) (bool, error) {
		return coretasks.Ended(s.deps.Tasks.Task(ctx, id))
	})
	if err != nil && ctx.Err() == nil {
		s.logf("drafts: adopt: %v", err)
	}
	if adopted+ended > 0 {
		s.logf("drafts: %d of no task adopted by their gate's task, %d of an ended task withdrawn", adopted, ended)
	}
	if s.deps.Gates == nil {
		return
	}
	held, err := s.deps.Tasks.HeldOpen(ctx)
	for _, id := range held {
		err = errors.Join(err, s.deps.Gates.SettleHeld(ctx, id))
	}
	if err != nil && ctx.Err() == nil {
		s.logf("drafts: settling the tasks opened to hold gates: %v", err)
	}
}

func originUnit(o draft.Origin) string {
	if o.Unit.IsZero() {
		return ""
	}
	return o.Unit.Label()
}

// handleDraftCloseSilently closes a held draft's conversation without its message: a person discards the draft
// through its gate, then its effects, each of which applies without a post, run as if it was sent.
func (s *Server) handleDraftCloseSilently(w http.ResponseWriter, r *http.Request) {
	id, ok := parseDraftID(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "no draft "+strconv.Quote(r.PathValue("id")))
		return
	}
	var req api.DraftCloseSilentlyRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, draftsBodyMax)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "close-silently takes the rev you saw: "+err.Error())
		return
	}
	actor, ok := s.forUser(w, r, "closing a conversation without its message")
	if !ok {
		return
	}
	o := s.deps.Outbox
	if o == nil {
		writeActionError(w, errOutboxUnavailable)
		return
	}
	ctx := context.WithoutCancel(r.Context())
	got, err := s.closeSilently(ctx, id, req.Rev, actor)
	switch {
	case errors.Is(err, outbox.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
		return
	case errors.Is(err, outbox.ErrConflict):
		writeError(w, http.StatusConflict, err.Error())
		return
	case err != nil:
		writeActionError(w, err)
		return
	}
	g, _, err := o.GateOf(ctx, got)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, s.draftDetail(ctx, got, g, nil))
}

// closeSilently discards held draft id at rev as actor and applies its effects, under draftMu; refused with the
// close-silently action's refusal when that action is disabled on it.
func (s *Server) closeSilently(ctx context.Context, id draft.ID, rev int64, actor requester) (draft.Draft, error) {
	s.draftMu.Lock()
	defer s.draftMu.Unlock()
	o := s.deps.Outbox
	d, err := o.Store.Get(ctx, id)
	if err != nil {
		return draft.Draft{}, err
	}
	if rev != d.Rev {
		return draft.Draft{}, fmt.Errorf("%w: draft %d changed since you saw it (rev %d, now %d)", outbox.ErrConflict, id, rev, d.Rev)
	}
	for _, st := range catalog.DraftStates(s.loaded(), s.draftFacts(ctx, d)) {
		if st.ID == catalog.DraftCloseSilently && !st.Enabled {
			return draft.Draft{}, conflict{msg: st.Reason, refusal: catalog.DraftRefusal(catalog.DraftCloseSilently, s.draftFacts(ctx, d))}
		}
	}
	if _, err := s.decideDraft(ctx, d, gate.Rejected, draftEdit{}, actor); err != nil {
		return draft.Draft{}, err
	}
	return o.CloseSilently(ctx, id)
}

// agentRevises refuses an agent session's change of draft id on a person's route, which would stamp the
// revision as the person's and let that session approve what it wrote; an agent revises a draft through the
// agent route, which stamps it, and a session that revised a draft never resolves its gate.
func agentRevises(id draft.ID, actor requester) error {
	if actor.Actor != task.ActorAgent {
		return nil
	}
	return fmt.Errorf("%w: session %s changes draft %d through draft_update, not a person's edit", gates.ErrRefused, actor.Session, id)
}
