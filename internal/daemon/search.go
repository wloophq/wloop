package daemon

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"unicode"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/catalog"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/task"
)

// otherGroup is the id of the search group of what no group's view holds.
const otherGroup = "other"

// handleSearch answers every task the lens query q matches, in the rail's views and the search groups', grouped
// by the first group whose view holds it, each group in its view's order with whole-word matches first.
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeError(w, http.StatusBadRequest, "q is required")
		return
	}
	g, ok := s.tasks(w)
	if !ok {
		return
	}
	ctx, ok := s.queuePass(r.Context(), w, g)
	if !ok {
		return
	}
	out, err := s.search(ctx, g, s.taskViews(ctx, g, false), q)
	if err != nil {
		writeTasksError(w, err)
		return
	}
	writeJSON(w, out)
}

// search is the answer to lens query q, matched on each task's match view, with vs, and with the full view of
// each result it returns.
func (s *Server) search(ctx context.Context, g *coretasks.Service, vs taskViews, q string) (api.Search, error) {
	l := s.loaded()
	def := l.Omnibox
	var rail, views []task.View
	for _, v := range l.Views.Views {
		if v.Over == task.OverTasks && catalog.Lists(l.Views, v.ID) && v.Rail {
			rail = append(rail, task.View(v.ID))
		}
	}
	views = slices.Clone(rail)
	for _, grp := range def.Groups {
		if !slices.Contains(views, task.View(grp.View)) {
			views = append(views, task.View(grp.View))
		}
	}
	kept := map[task.ID]api.TaskView{}
	sets, err := g.Sets(ctx, views, s.lensKeepIn(vs, q, kept))
	if err != nil {
		return api.Search{}, err
	}
	placed := map[task.ID]bool{}
	type group struct {
		id, label string
		tasks     []task.Task
	}
	var groups []group
	take := func(id, label string, from []task.View) {
		out := group{id: id, label: label}
		for _, v := range from {
			for _, e := range sets[v] {
				if !placed[e.ID] {
					placed[e.ID] = true
					out.tasks = append(out.tasks, e)
				}
			}
		}
		if len(out.tasks) > 0 {
			groups = append(groups, out)
		}
	}
	for _, grp := range def.Groups {
		take(grp.ID, grp.Label, []task.View{task.View(grp.View)})
	}
	take(otherGroup, def.Other, rail)
	out := api.Search{Query: q, Total: len(placed), Groups: []api.SearchGroup{}}
	terms := words(api.Fold(q))
	inViews := map[task.ID][]string{}
	for _, rv := range rail {
		for _, e := range sets[rv] {
			inViews[e.ID] = append(inViews[e.ID], string(rv))
		}
	}
	byID := map[task.ID]task.Task{}
	for _, grp := range groups {
		sg := api.SearchGroup{ID: grp.id, Label: grp.label, Count: len(grp.tasks), Results: []api.SearchResult{}}
		for _, e := range grp.tasks {
			byID[e.ID] = e
			v := kept[e.ID]
			res := api.SearchResult{Task: v, Views: append([]string{}, inViews[e.ID]...)}
			if len(v.UnitLabels) > 0 {
				res.UnitLabel = v.UnitLabels[0]
			}
			sg.Results = append(sg.Results, res)
		}
		slices.SortStableFunc(sg.Results, func(a, b api.SearchResult) int {
			return boolRank(wholeWords(a, terms)) - boolRank(wholeWords(b, terms))
		})
		sg.Results = sg.Results[:min(len(sg.Results), def.Limit)]
		for i, res := range sg.Results {
			v, err := vs.view(ctx, byID[task.ID(res.Task.ID)])
			if err != nil {
				return api.Search{}, err
			}
			sg.Results[i].Task = v
		}
		out.Groups = append(out.Groups, sg)
	}
	return out, nil
}

func boolRank(b bool) int {
	if b {
		return 0
	}
	return 1
}

// wholeWords says whether every one of terms is a whole word of the result's title or unit label.
func wholeWords(r api.SearchResult, terms []string) bool {
	have := words(api.Fold(r.Task.Title + " " + r.UnitLabel))
	for _, t := range terms {
		if !slices.Contains(have, t) {
			return false
		}
	}
	return len(terms) > 0
}

// words are s's runs of letters and digits.
func words(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}
