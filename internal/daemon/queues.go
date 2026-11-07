package daemon

import (
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/catalog"
	"github.com/wloophq/wloop/internal/core/home"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/decision"
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
	ctx := r.Context()
	var keep coretasks.Keep
	if text := strings.TrimSpace(r.URL.Query().Get("q")); text != "" {
		keep = s.lensKeep(ctx, g, text)
	}
	q, err := s.home(g).Queue(ctx, k, keep)
	if err != nil {
		writeTasksError(w, err)
		return
	}
	l := s.loaded()
	out := api.Queue{Kind: string(q.Kind), Title: q.Title, Empty: q.Empty, Total: len(q.Items), Items: []api.QueueItem{}}
	tasks := make([]task.Task, len(q.Items))
	for i, it := range q.Items {
		tasks[i] = it.Task
	}
	vs := s.taskViews(ctx, g, false)
	vs.readRows(ctx, tasks)
	byUnit := map[unit.Unit][]items.Item{}
	for _, it := range q.Items {
		v, err := vs.view(ctx, it.Task)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		qi := api.QueueItem{Task: v, Primary: api.QueueAction{Kind: it.Primary.Kind, ID: it.Primary.ID, Label: primaryLabel(l, it)}}
		if g := it.Found.Gate; g != nil {
			gv := s.gateView(ctx, *g, byUnit)
			qi.Gate = &gv
		} else {
			qi.Situation = &api.QueueSituation{Provider: string(it.Found.Unit.Provider), Kind: it.Found.Situation, Label: it.Situation}
		}
		out.Items = append(out.Items, qi)
	}
	writeJSON(w, out)
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
