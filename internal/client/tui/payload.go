package tui

import (
	"encoding/json"

	"github.com/wloophq/wloop/internal/api"
)

// prView is what the terminal shows of a pull request's payload, read by
// the payload's wire keys; a row with no payload has none of it.
type prView struct {
	Author    string            `json:"author"`
	Head      string            `json:"head"`
	Brief     *api.BriefSummary `json:"brief"`
	Facts     *api.RowFacts     `json:"facts"`
	Workspace api.Workspace     `json:"workspace"`
}

// haystack are the payload's texts the filter searches.
func (p prView) haystack() []string {
	out := []string{p.Head, p.Workspace.DisplayName, p.Workspace.Comment}
	if p.Brief != nil {
		out = append(out, p.Brief.Goal)
	}
	return out
}

func prOf(r api.Row) prView {
	var p prView
	if len(r.Payload) > 0 {
		_ = json.Unmarshal(r.Payload, &p)
	}
	return p
}
