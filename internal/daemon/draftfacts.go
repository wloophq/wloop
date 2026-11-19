package daemon

import (
	"strings"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

// draftViewFacts are the facts of d a draft facet reads; where and people come
// from the draft's unit and its plugin.
func draftViewFacts(d api.Draft, where string, people []string) workflow.Facts {
	provider, _, _ := strings.Cut(d.Unit, "/")
	origin := draft.Origin{Kind: draft.OriginKind(d.Origin.Kind), Intent: d.Origin.Intent, Workflow: d.Origin.Workflow, Rule: d.Origin.Rule}
	return workflow.Facts{
		"draft.proposer":   origin.Proposer(),
		"draft.workflow":   d.Origin.Workflow,
		"draft.kind":       d.Kind,
		"draft.delivery":   d.Delivery,
		"draft.provider":   provider,
		"draft.state":      d.State,
		"draft.where":      where,
		"draft.people":     append([]string{}, people...),
		"draft.created_at": d.CreatedAt,
	}
}
