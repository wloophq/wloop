package daemon

import (
	"cmp"
	"slices"

	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/workflow"
	intentload "github.com/wloophq/wloop/internal/intent"
)

// draftViewFacts are the facts of d a draft facet reads; where and people come
// from the draft's unit and its plugin.
func draftViewFacts(d draft.Draft, where string, people []string) workflow.Facts {
	return workflow.Facts{
		"draft.proposer":   d.Origin.Proposer(),
		"draft.workflow":   d.Origin.Workflow,
		"draft.kind":       string(d.Kind),
		"draft.delivery":   string(d.Delivery),
		"draft.provider":   string(d.Unit.Provider),
		"draft.state":      string(d.State),
		"draft.where":      where,
		"draft.people":     append([]string{}, people...),
		"draft.created_at": d.CreatedAt,
	}
}

// rowWhere is where a draft's unit is as its row says it: the first value the row gives the facet whose id
// is that of the draft facet reading draft.where, "" for none.
func rowWhere(defs []intentload.FacetDef, row map[string][]string) string {
	for _, f := range defs {
		if f.Fact == "draft.where" && len(row[f.ID]) > 0 {
			return row[f.ID][0]
		}
	}
	return ""
}

// mentioned are the people text mentions, each once: the name names gives a mention key, else the key.
func mentioned(text string, names map[string]string) []string {
	var out []string
	for _, m := range mentionKey.FindAllStringSubmatch(text, -1) {
		if p := cmp.Or(names[m[1]], m[1]); !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out
}
