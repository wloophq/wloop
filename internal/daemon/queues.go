package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/catalog"
	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/home"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/decision"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/items"
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
	ctx := withPlanReads(r.Context())
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
	ctx := withPlanReads(r.Context())
	d := s.loaded().Decisions
	all, err := s.home(g).QueueAll(ctx, d.Order, s.queueKeep(ctx, g, r))
	if err != nil {
		writeTasksError(w, err)
		return
	}
	items, err := s.queueItems(ctx, g, all)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, api.Queue{View: s.loaded().Views.QueueView(), Title: d.QueueAll, Empty: d.EmptyAll, Total: len(items), Items: items})
}

// queueKeep is the lens r's query parameter q narrows a queue by; nil when it names none.
func (s *Server) queueKeep(ctx context.Context, g *coretasks.Service, r *http.Request) coretasks.Keep {
	if text := strings.TrimSpace(r.URL.Query().Get("q")); text != "" {
		return s.lensKeep(ctx, g, text)
	}
	return nil
}

// queueItems are its as a queue serves them.
func (s *Server) queueItems(ctx context.Context, g *coretasks.Service, its []home.Item) ([]api.QueueItem, error) {
	l := s.loaded()
	tasks := make([]task.Task, len(its))
	for i, it := range its {
		tasks[i] = it.Task
	}
	vs := s.taskViews(ctx, g, false)
	vs.readRows(ctx, tasks)
	byUnit := map[unit.Unit][]items.Item{}
	var pinned map[int64]int64
	if svc := s.deps.Gates; svc != nil {
		var err error
		if pinned, err = svc.Pinned(ctx); err != nil {
			return nil, err
		}
	}
	out := []api.QueueItem{}
	for _, it := range its {
		v, err := vs.view(ctx, it.Task)
		if err != nil {
			return nil, err
		}
		qi := api.QueueItem{Kind: string(it.Found.Kind), Word: it.Word, Tone: it.Tone, Since: it.Found.Since, Task: v, Primary: api.QueueAction{Kind: it.Primary.Kind, ID: it.Primary.ID, Label: primaryLabel(l, it)}}
		if it.Primary.Kind == home.ActionRetry {
			if qi.Primary, err = s.retryAction(ctx, *it.Found.Gate); err != nil {
				return nil, err
			}
		}
		if g := it.Found.Gate; g != nil {
			gv := s.gateView(ctx, *g, byUnit)
			qi.Gate = &gv
			if qi.Options, err = s.questionOptions(ctx, *g, pinned, byUnit); err != nil {
				return nil, err
			}
		} else {
			qi.Situation = &api.QueueSituation{Provider: string(it.Found.Unit.Provider), Kind: it.Found.Situation, Label: it.Situation, Unit: it.Found.Unit.String()}
		}
		out = append(out, qi)
	}
	return out, nil
}

// questionOptions are the choices of question gate q when its message pins drafts (pinned, by draft, the message
// that pins each), each with its draft's gate: an option's, or a confirm's yes and no. Nil when the message pins
// none. The pinned drafts' own gates make no item, so a message that cannot be read is an error, not a question
// without its drafts.
func (s *Server) questionOptions(ctx context.Context, q gate.Gate, pinned map[int64]int64, byUnit map[unit.Unit][]items.Item) ([]api.QueueOption, error) {
	id, err := strconv.ParseInt(q.Subject, 10, 64)
	if q.Kind != gate.KindQuestion || err != nil || !pinsAny(pinned, id) {
		return nil, nil
	}
	if s.deps.Messages == nil || s.deps.GateReader == nil {
		return nil, fmt.Errorf("question gate %d pins drafts, and the daemon reads no messages or gates", q.ID)
	}
	m, ok, err := s.deps.Messages.Store.Message(ctx, id)
	switch {
	case err != nil:
		return nil, fmt.Errorf("the message of question gate %d, which pins drafts: %w", q.ID, err)
	case !ok:
		return nil, fmt.Errorf("the message of question gate %d, which pins drafts, is gone", q.ID)
	}
	draftGate := func(ref *record.DraftRef) (*api.GateView, error) {
		if ref == nil || pinned[ref.ID] != m.ID {
			return nil, nil
		}
		gs, err := s.deps.GateReader.Where(ctx, gates.Query{Kind: gate.KindDraft, Subjects: []string{strconv.FormatInt(ref.ID, 10)}})
		if err != nil || len(gs) == 0 {
			return nil, err
		}
		v := s.gateView(ctx, gs[len(gs)-1], byUnit)
		return &v, nil
	}
	var out []api.QueueOption
	if m.Confirm {
		yes, err := draftGate(m.Draft)
		if err != nil {
			return nil, err
		}
		out = []api.QueueOption{
			{ID: "yes", Label: s.notice(builtin.NoticeNotifyYes, nil), Recommended: m.Recommend == "yes", Draft: yes},
			{ID: "no", Label: s.notice(builtin.NoticeNotifyNo, nil), Recommended: m.Recommend == "no"},
		}
	}
	for _, o := range m.Options {
		d, err := draftGate(o.Draft)
		if err != nil {
			return nil, err
		}
		out = append(out, api.QueueOption{ID: o.ID, Label: o.Label, Recommended: o.ID == m.Recommend, Draft: d})
	}
	for i := range out {
		out[i].Primary = s.word(api.QueueOptionPrimary, map[string]any{"option": optionWord(out[i])})
	}
	return out, nil
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

// retryAction retries the delivery of the draft of approved draft gate g: the verb whose handler retries, on the
// draft's own task, at the draft's rev, worded as the profile words that verb.
func (s *Server) retryAction(ctx context.Context, g gate.Gate) (api.QueueAction, error) {
	if s.deps.Outbox == nil {
		return api.QueueAction{}, fmt.Errorf("draft gate %d failed to deliver, and the daemon serves no Outbox", g.ID)
	}
	id, err := strconv.ParseInt(g.Subject, 10, 64)
	if err != nil {
		return api.QueueAction{}, fmt.Errorf("draft gate %d names no draft: %w", g.ID, err)
	}
	d, err := s.deps.Outbox.Store.Get(ctx, draft.ID(id))
	if err != nil {
		return api.QueueAction{}, fmt.Errorf("the draft of gate %d: %w", g.ID, err)
	}
	verb, label := s.handlerVerb(handlerRetry)
	return api.QueueAction{Kind: queueVerb, ID: string(verb), Task: draftPrefix + g.Subject, Args: map[string]string{"rev": strconv.FormatInt(d.Rev, 10)}, Label: label}, nil
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
