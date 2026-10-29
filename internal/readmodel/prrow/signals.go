package prrow

import (
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/readmodel"
)

// The link signals the GitHub plugin's manifest declares.
const (
	signalStalled = "stalled"
	signalInLoop  = "in_loop"
	signalReady   = "ready"
)

// Signals are the link signals of r, a GitHub row derived under the host's
// overlays: stalled, an open pull request in scope whose situation lasted
// past its threshold; in_loop, an open one a review workspace holds; ready,
// an open one whose work is approved and no host state overlays. None of
// another provider's row.
func Signals(r api.Row) map[string]bool {
	p, ok := PayloadOf(r)
	if !ok {
		return nil
	}
	open := !r.Summary.Closed
	return map[string]bool{
		signalStalled: open && r.Summary.Stalled && len(p.Reasons) > 0,
		signalInLoop:  open && p.Workspace.IsReview,
		signalReady:   open && p.Approved,
	}
}

// Signals are the link signals of row.
func (p *Provider) Signals(_ readmodel.Base, row api.Row, _ any, _ time.Time) map[string]bool {
	return Signals(row)
}
