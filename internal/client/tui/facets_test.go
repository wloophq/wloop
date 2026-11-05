package tui

import (
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// servedFacets are the facets the GitHub provider serves for a test row: its repository, its author ("me"
// first for the viewer's), whom it waits on, its situation, its brief's size and risk, its workspace column,
// its flags and its age.
func servedFacets(viewer string, r api.Row) map[string][]string {
	p := prOf(r)
	repo, _, _ := strings.Cut(r.Label, "#")
	f := map[string][]string{"where": {repo}, "kind": {string(r.Kind)}, "age": {r.Summary.Since.UTC().Format(time.RFC3339)}}
	mine := viewer != "" && strings.EqualFold(p.Author, viewer)
	if p.Author != "" {
		f["author"] = []string{p.Author}
		if mine {
			f["author"] = []string{"me", p.Author}
		}
	}
	for w := range strings.SplitSeq(r.Summary.WaitsOn, ", ") {
		switch {
		case w == "" || w == "nobody":
		case w == "author" && mine:
			f["waits-on"] = append(f["waits-on"], "me")
		case w == "author":
			f["waits-on"] = append(f["waits-on"], p.Author)
		default:
			f["waits-on"] = append(f["waits-on"], w)
		}
	}
	if b := p.Brief; b != nil {
		f["size"], f["risk"] = []string{cmpOr(b.Size, "none")}, []string{cmpOr(b.Risk, "none")}
	}
	f["status"] = []string{cmpOr(p.Workspace.Status, "none")}
	if r.Summary.Stalled && slices.Contains(r.Views, "stalled") {
		f["is"] = []string{"stalled"}
	}
	return f
}

// withServedFacets gives each GitHub row that carries no situation facet the facets its provider serves.
func withServedFacets(viewer string, rows []api.Row) []api.Row {
	out := slices.Clone(rows)
	for i, r := range out {
		if r.Unit.Provider == unit.GitHub && r.Facets["kind"] == nil {
			out[i].Facets = servedFacets(viewer, r)
		}
	}
	return out
}
