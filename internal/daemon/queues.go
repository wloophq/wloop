package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/catalog"
	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/home"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/decision"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/items"
	dmessage "github.com/wloophq/wloop/internal/domain/message"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

func (s *Server) handleQueue(w http.ResponseWriter, r *http.Request) {
	k := decision.Kind(r.PathValue("kind"))
	if !decision.Valid(k) {
		kinds := make([]string, len(decision.Kinds))
		for i, x := range decision.Kinds {
			kinds[i] = string(x)
		}
		writeError(w, http.StatusNotFound, fmt.Sprintf("unknown decision kind %q; the kinds are %s", k, strings.Join(kinds, ", ")))
		return
	}
	g, ok := s.tasks(w)
	if !ok {
		return
	}
	ctx, ok := s.queuePass(withPlanReads(r.Context()), w, g)
	if !ok {
		return
	}
	q, err := s.home(g).Queue(ctx, k, s.queueKeep(ctx, g, r))
	if err != nil {
		writeTasksError(w, err)
		return
	}
	items, err := s.queueItems(ctx, g, q.Items)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, api.Queue{Kind: string(q.Kind), View: string(q.View), Title: q.Title, Empty: q.Empty, Total: len(items), Items: items})
}

func (s *Server) handleQueueAll(w http.ResponseWriter, r *http.Request) {
	g, ok := s.tasks(w)
	if !ok {
		return
	}
	ctx, ok := s.queuePass(withPlanReads(r.Context()), w, g)
	if !ok {
		return
	}
	d := s.loaded().Decisions
	kinds := r.URL.Query()["kind"]
	for _, k := range kinds {
		if !decision.Valid(decision.Kind(k)) {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("unknown decision kind %q", k))
			return
		}
	}
	all, err := s.home(g).QueueAll(ctx, d.Order, d.Sort, s.queueKeep(ctx, g, r))
	if err != nil {
		writeTasksError(w, err)
		return
	}
	if len(kinds) > 0 {
		all = slices.DeleteFunc(all, func(it home.Item) bool { return !slices.Contains(kinds, string(it.Found.Kind)) })
	}
	items, err := s.queueItems(ctx, g, all)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	served := make([]api.QueueKind, len(d.Order))
	for i, k := range d.Order {
		served[i] = api.QueueKind{ID: string(k), Label: d.Kinds[k].Queue}
	}
	writeJSON(w, api.Queue{View: s.loaded().Views.QueueView(), Title: d.QueueAll, Empty: d.EmptyAll, Total: len(items), Items: items, Kinds: served})
}

// queuePass is ctx carrying one pass of the tasks, which every kind's view and every item's task view read, so
// a task's facts, and what they load for every task, such as the drafts the open questions pin, are read once.
func (s *Server) queuePass(ctx context.Context, w http.ResponseWriter, g *coretasks.Service) (context.Context, bool) {
	n, err := g.Snapshot(ctx)
	if err != nil {
		writeTasksError(w, err)
		return nil, false
	}
	return coretasks.WithPage(n.Within(ctx)), true
}

// queueKeep is the lens r's query parameter q narrows a queue by; nil when it names none.
func (s *Server) queueKeep(ctx context.Context, g *coretasks.Service, r *http.Request) coretasks.Keep {
	if text := strings.TrimSpace(r.URL.Query().Get("q")); text != "" {
		return s.lensKeep(ctx, g, text)
	}
	return nil
}

// queueItems are its as a queue serves them, but the items whose gate's subject cannot be read (servable).
func (s *Server) queueItems(ctx context.Context, g *coretasks.Service, its []home.Item) ([]api.QueueItem, error) {
	l := s.loaded()
	tasks := make([]task.Task, len(its))
	for i, it := range its {
		tasks[i] = it.Task
	}
	vs := s.taskViews(ctx, g, false)
	vs.readRows(ctx, tasks)
	byUnit := map[unit.Unit][]items.Item{}
	pinned, err := s.queuePins(ctx)
	if err != nil {
		return nil, err
	}
	out := []api.QueueItem{}
	for _, it := range its {
		r, err := s.readQueueItem(ctx, it, pinned)
		if !s.servedOrDropped(it, err) {
			continue
		}
		qi, err := s.queueItem(ctx, vs, l, it, r, byUnit)
		if err != nil {
			return nil, err
		}
		out = append(out, qi)
	}
	return out, nil
}

// queuePins are the drafts the open questions pin, by draft, with the message that pins each, as the queue's
// decisions read them.
func (s *Server) queuePins(ctx context.Context) (map[int64]int64, error) {
	w, err := s.waiting(ctx)
	return w.Pinned, err
}

// queueItem is it as the queue serves it, with r, what its gate names.
func (s *Server) queueItem(ctx context.Context, vs *taskViewer, l *intentload.Loaded, it home.Item, r queueReads, byUnit map[unit.Unit][]items.Item) (api.QueueItem, error) {
	v, err := vs.view(ctx, it.Task)
	if err != nil {
		return api.QueueItem{}, err
	}
	qi := api.QueueItem{Kind: string(it.Found.Kind), Word: it.Word, Tone: it.Tone, Since: it.Found.Since, Task: v, Primary: api.QueueAction{Kind: it.Primary.Kind, ID: it.Primary.ID, Label: primaryLabel(l, it)}}
	if r.retry != nil {
		qi.Primary = s.retryAction(*it.Found.Gate, *r.retry)
	}
	if g := it.Found.Gate; g != nil {
		gv := s.gateView(ctx, *g, byUnit)
		qi.Gate = &gv
		if r.message != nil {
			qi.Options = s.questionOptions(ctx, *r.message, r.drafts, byUnit)
		}
	} else {
		qi.Situation = &api.QueueSituation{Provider: string(it.Found.Unit.Provider), Kind: it.Found.Situation, Label: it.Situation, Unit: it.Found.Unit.String()}
	}
	return qi, nil
}

// queueReads are what an item's gate names that its card shows: the message of a question that pins drafts with
// the newest gate of each draft it pins, by draft, and the draft whose failed delivery it retries.
type queueReads struct {
	message *dmessage.Message
	drafts  map[int64]gate.Gate
	retry   *draft.Draft
}

// readQueueItem reads what it's gate names, once in the pass ctx carries; an error says the item cannot be
// served.
func (s *Server) readQueueItem(ctx context.Context, it home.Item, pinned map[int64]int64) (queueReads, error) {
	g := it.Found.Gate
	if g == nil {
		return queueReads{}, nil
	}
	return coretasks.Once(ctx, passQueueItem+strconv.FormatInt(g.ID, 10)+"."+it.Primary.Kind, func() (queueReads, error) {
		return s.readGateNames(ctx, it, pinned)
	})
}

const passQueueItem = "daemon.queue_item."

func (s *Server) readGateNames(ctx context.Context, it home.Item, pinned map[int64]int64) (queueReads, error) {
	var r queueReads
	g := it.Found.Gate
	if it.Primary.Kind == home.ActionRetry {
		d, err := s.retryDraft(ctx, *g)
		if err != nil {
			return r, err
		}
		r.retry = &d
	}
	m, err := s.pinningMessage(ctx, *g, pinned)
	if err != nil || m == nil {
		return r, err
	}
	r.message = m
	r.drafts, err = s.pinnedDraftGates(ctx, *m, pinned)
	return r, err
}

// pinnedDraftGates are the newest gates of the drafts message m pins (pinned, by draft, the message that pins
// each), by draft.
func (s *Server) pinnedDraftGates(ctx context.Context, m dmessage.Message, pinned map[int64]int64) (map[int64]gate.Gate, error) {
	refs := []*record.DraftRef{m.Draft}
	for _, o := range m.Options {
		refs = append(refs, o.Draft)
	}
	out := map[int64]gate.Gate{}
	for _, ref := range refs {
		if ref == nil || pinned[ref.ID] != m.ID {
			continue
		}
		gs, err := s.deps.GateReader.Where(ctx, gates.Query{Kind: gate.KindDraft, Subjects: []string{strconv.FormatInt(ref.ID, 10)}})
		if err != nil {
			return nil, fmt.Errorf("the gate of draft %d, which message %d pins: %w", ref.ID, m.ID, err)
		}
		if len(gs) > 0 {
			out[ref.ID] = gs[len(gs)-1]
		}
	}
	return out, nil
}

// servable are its but the items queueItems drops, so a count of the queue counts what it serves.
func (s *Server) servable(ctx context.Context, its []home.Item) ([]home.Item, error) {
	pinned, err := s.queuePins(ctx)
	if err != nil {
		return nil, err
	}
	out := its[:0:0]
	for _, it := range its {
		if _, err := s.readQueueItem(ctx, it, pinned); s.servedOrDropped(it, err) {
			out = append(out, it)
		}
	}
	return out, nil
}

// servedOrDropped says whether it is served, err nil; else it logs why it is dropped, once while the reason
// stays the same.
func (s *Server) servedOrDropped(it home.Item, err error) bool {
	key := "task " + string(it.Task.ID) + " " + string(it.Found.Kind)
	if g := it.Found.Gate; g != nil {
		key = "gate " + strconv.FormatInt(g.ID, 10)
	}
	if err == nil {
		s.queueDrops.Delete(key)
		return true
	}
	now := time.Now()
	if was, ok := s.queueDrops.Swap(key, queueDrop{err.Error(), now}); !ok || was.(queueDrop).why != err.Error() {
		s.logf("queue: dropping the item of %s on task %s: %v", key, it.Task.ID, err)
	}
	s.queueDrops.Range(func(k, v any) bool {
		if now.Sub(v.(queueDrop).at) >= queueDropKept {
			s.queueDrops.Delete(k)
		}
		return true
	})
	return false
}

// queueDrop is why an item was last dropped, and when.
type queueDrop struct {
	why string
	at  time.Time
}

// queueDropKept is how long a dropped item's reason is kept unlogged; one dropped again after it is logged again,
// so items that left the queue while dropped are let go.
const queueDropKept = time.Hour

// pinningMessage is the message of question gate q when it pins drafts (pinned, by draft, the message that pins
// each); nil when it pins none. The pinned drafts' own gates make no item, so a message that cannot be read is an
// error, not a question without its drafts.
func (s *Server) pinningMessage(ctx context.Context, q gate.Gate, pinned map[int64]int64) (*dmessage.Message, error) {
	id, err := strconv.ParseInt(q.Subject, 10, 64)
	if q.Kind != gate.KindQuestion || err != nil || !pinsAny(pinned, id) {
		return nil, nil
	}
	if s.deps.Messages == nil || s.deps.GateReader == nil {
		return nil, fmt.Errorf("question gate %d pins drafts, and the daemon reads no messages or gates", q.ID)
	}
	m, ok, err := s.message(ctx, id)
	switch {
	case err != nil:
		return nil, fmt.Errorf("the message of question gate %d, which pins drafts: %w", q.ID, err)
	case !ok:
		return nil, fmt.Errorf("the message of question gate %d, which pins drafts, is gone", q.ID)
	}
	return &m, nil
}

// questionOptions are the choices of message m, each with the gate of the draft picking it approves (drafts, by
// draft, the newest gate of each draft m pins): an option's, or a confirm's yes and no.
func (s *Server) questionOptions(ctx context.Context, m dmessage.Message, drafts map[int64]gate.Gate, byUnit map[unit.Unit][]items.Item) []api.QueueOption {
	draftGate := func(ref *record.DraftRef) *api.GateView {
		if ref == nil {
			return nil
		}
		g, ok := drafts[ref.ID]
		if !ok {
			return nil
		}
		v := s.gateView(ctx, g, byUnit)
		return &v
	}
	var out []api.QueueOption
	if m.Confirm {
		out = []api.QueueOption{
			{ID: "yes", Label: s.notice(builtin.NoticeNotifyYes, nil), Recommended: m.Recommend == "yes", Draft: draftGate(m.Draft)},
			{ID: "no", Label: s.notice(builtin.NoticeNotifyNo, nil), Recommended: m.Recommend == "no"},
		}
	}
	for _, o := range m.Options {
		out = append(out, api.QueueOption{ID: o.ID, Label: o.Label, Recommended: o.ID == m.Recommend, Draft: draftGate(o.Draft)})
	}
	for i := range out {
		out[i].Primary = s.word(api.QueueOptionPrimary, map[string]any{"option": optionWord(out[i])})
	}
	return out
}

// optionWord is what o's button names: its draft's verdict, else its label.
func optionWord(o api.QueueOption) string {
	if o.Draft != nil && len(o.Draft.Subject) > 0 {
		var d api.Draft
		if json.Unmarshal(o.Draft.Subject, &d) == nil && d.Verdict != "" {
			return d.Verdict
		}
	}
	return o.Label
}

// retryDraft is the draft of approved draft gate g, whose delivery failed.
func (s *Server) retryDraft(ctx context.Context, g gate.Gate) (draft.Draft, error) {
	if s.deps.Outbox == nil {
		return draft.Draft{}, fmt.Errorf("draft gate %d failed to deliver, and the daemon serves no Outbox", g.ID)
	}
	id, err := strconv.ParseInt(g.Subject, 10, 64)
	if err != nil {
		return draft.Draft{}, fmt.Errorf("draft gate %d names no draft: %w", g.ID, err)
	}
	d, err := s.deps.Outbox.Store.Get(ctx, draft.ID(id))
	if err != nil {
		return draft.Draft{}, fmt.Errorf("the draft of gate %d: %w", g.ID, err)
	}
	return d, nil
}

// retryAction retries the delivery of draft d of approved draft gate g: the verb whose handler retries, on the
// draft's own task, at the draft's rev, worded as the profile words that verb.
func (s *Server) retryAction(g gate.Gate, d draft.Draft) api.QueueAction {
	verb, label := s.handlerVerb(handlerRetry)
	return api.QueueAction{Kind: queueVerb, ID: string(verb), Task: draftPrefix + g.Subject, Args: map[string]string{"rev": strconv.FormatInt(d.Rev, 10)}, Label: label}
}

// queueVerb is the kind of a QueueAction that runs a task verb.
const queueVerb = "verb"

// pinsAny says whether message pins a draft of pinned.
func pinsAny(pinned map[int64]int64, message int64) bool {
	for _, m := range pinned {
		if m == message {
			return true
		}
	}
	return false
}

// primaryLabel is what the button of item's primary action says: its gate intent's, its catalog action's, or
// opening the unit at its source.
func primaryLabel(l *intentload.Loaded, it home.Item) string {
	switch it.Primary.Kind {
	case home.ActionGate:
		if def, ok := l.Gates.Gate(it.Found.Gate.Name); ok {
			if i := slices.IndexFunc(def.Intents, func(in intentload.GateIntent) bool { return in.Name == it.Primary.ID }); i >= 0 {
				return def.Intents[i].Label
			}
		}
	case home.ActionAction:
		specs := catalog.Specs(l)
		if i := slices.IndexFunc(specs, func(a api.ActionSpec) bool { return a.ID == it.Primary.ID }); i >= 0 {
			return specs[i].Label
		}
	case home.ActionOpen:
		return l.Decisions.Open
	}
	return it.Primary.ID
}
