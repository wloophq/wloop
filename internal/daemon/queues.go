package daemon

import (
	"context"
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
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	intentload "github.com/wloophq/wloop/internal/intent"
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
	writeJSON(w, api.Queue{Title: d.QueueAll, Empty: d.EmptyAll, Total: len(items), Items: items})
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
	out := []api.QueueItem{}
	for _, it := range its {
		v, err := vs.view(ctx, it.Task)
		if err != nil {
			return nil, err
		}
		qi := api.QueueItem{Kind: string(it.Found.Kind), Task: v, Primary: api.QueueAction{Kind: it.Primary.Kind, ID: it.Primary.ID, Label: primaryLabel(l, it)}}
		if g := it.Found.Gate; g != nil {
			gv := s.gateView(ctx, *g, byUnit)
			qi.Gate = &gv
			qi.Options = s.questionOptions(ctx, *g, byUnit)
		} else {
			qi.Situation = &api.QueueSituation{Provider: string(it.Found.Unit.Provider), Kind: it.Found.Situation, Label: it.Situation}
		}
		out = append(out, qi)
	}
	return out, nil
}

// questionOptions are the options of question gate q when one pins a held draft q decides (gates.Service.PinnedBy),
// each with its draft's gate; nil when none does.
func (s *Server) questionOptions(ctx context.Context, q gate.Gate, byUnit map[unit.Unit][]items.Item) []api.QueueOption {
	if q.Kind != gate.KindQuestion || s.deps.Gates == nil || s.deps.GateReader == nil {
		return nil
	}
	m, ok := s.messageOf(ctx, q)
	if !ok {
		return nil
	}
	out := make([]api.QueueOption, len(m.Options))
	pins := false
	for i, o := range m.Options {
		out[i] = api.QueueOption{ID: o.ID, Label: o.Label, Recommended: o.ID == m.Recommend}
		if o.Draft == nil {
			continue
		}
		drafted, err := s.deps.GateReader.Where(ctx, gates.Query{Kind: gate.KindDraft, Subjects: []string{strconv.FormatInt(o.Draft.ID, 10)}})
		if err != nil {
			s.logf("the gate of the draft option %s of question gate %d pins: %v", o.ID, q.ID, err)
			continue
		}
		for _, dg := range drafted {
			by, ok, err := s.deps.Gates.PinnedBy(ctx, dg)
			if err != nil {
				s.logf("whether question gate %d pins draft gate %d: %v", q.ID, dg.ID, err)
			}
			if ok && by.ID == q.ID {
				v := s.gateView(ctx, dg, byUnit)
				out[i].Draft, pins = &v, true
				break
			}
		}
	}
	if !pins {
		return nil
	}
	return out
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
