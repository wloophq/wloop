package cli

import (
	"context"
	"slices"

	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// keptPR is u's pull request as the GitHub plugin last reported it, which
// a round its plan names is sent with; zero when it reported none.
func (e *Env) keptPR(ctx context.Context, u unit.Unit) (dinv.PlanFacts, error) {
	ref, err := githubRef(u)
	in := e.planInputs()
	if err != nil || in == nil {
		return dinv.PlanFacts{}, nil
	}
	var row dinv.PR
	found := false
	if r, ok := in.(reportedPRs); ok {
		if row, found, err = r.PR(ctx, ref); err != nil {
			return dinv.PlanFacts{}, err
		}
	} else {
		rows, err := in.Rows(ctx)
		if err != nil {
			return dinv.PlanFacts{}, err
		}
		if i := slices.IndexFunc(rows, func(r dinv.PR) bool { return r.Ref == ref }); i >= 0 {
			row, found = rows[i], true
		}
	}
	if !found {
		return dinv.PlanFacts{}, nil
	}
	viewer, err := in.Viewer(ctx)
	if err != nil {
		return dinv.PlanFacts{}, err
	}
	got, err := in.Facts(ctx, []dinv.PR{row}, viewer)
	return got[u], err
}
