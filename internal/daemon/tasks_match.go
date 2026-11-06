package daemon

import (
	"net/http"

	"github.com/wloophq/wloop/internal/api"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// handleMatch serves the open tasks whose title holds text, each with only what a picker reads: its id,
// title, state, category and units.
func (s *Server) handleMatch(w http.ResponseWriter, r *http.Request, g *coretasks.Service, text string) {
	es, err := g.Match(r.Context(), text)
	if err != nil {
		writeTasksError(w, err)
		return
	}
	page := api.TasksPage{Full: true, Tasks: make([]api.TaskView, 0, len(es)), Removed: []string{}, Loading: s.loading(), Specs: s.viewsCatalog().Version}
	for _, e := range es {
		page.Tasks = append(page.Tasks, api.TaskView{ID: string(e.ID), Title: e.Title, State: string(e.State), Category: string(e.Category),
			Units: append([]unit.Unit{}, e.Units...)})
	}
	writeJSON(w, page)
}
