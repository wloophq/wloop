package daemon

import (
	"net/http"
	"slices"
	"strings"
	"unicode"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/catalog"
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
	ctx, l := r.Context(), s.loaded()
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
	sets, err := g.Sets(ctx, views, s.lensKeep(ctx, g, q))
	if err != nil {
		writeTasksError(w, err)
		return
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
	vs := s.taskViews(ctx, g, false)
	for _, grp := range groups {
		shown := grp.tasks[:min(len(grp.tasks), def.Limit)]
		vs.readRows(ctx, shown)
		sg := api.SearchGroup{ID: grp.id, Label: grp.label, Count: len(grp.tasks), Results: []api.SearchResult{}}
		for _, e := range grp.tasks {
			v, err := vs.view(ctx, e)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			res := api.SearchResult{Task: v, Views: []string{}}
			if len(v.UnitLabels) > 0 {
				res.UnitLabel = v.UnitLabels[0]
			}
			for _, rv := range rail {
				if slices.ContainsFunc(sets[rv], func(x task.Task) bool { return x.ID == e.ID }) {
					res.Views = append(res.Views, string(rv))
				}
			}
			sg.Results = append(sg.Results, res)
		}
		slices.SortStableFunc(sg.Results, func(a, b api.SearchResult) int {
			return boolRank(wholeWords(a, terms)) - boolRank(wholeWords(b, terms))
		})
		sg.Results = sg.Results[:min(len(sg.Results), def.Limit)]
		out.Groups = append(out.Groups, sg)
	}
	writeJSON(w, out)
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
