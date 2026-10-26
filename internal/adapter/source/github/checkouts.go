package ghsource

import (
	"context"

	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/github"
	sourcereg "github.com/wloophq/wloop/internal/source"
)

type prReader interface {
	PR(ctx context.Context, ref github.Ref) (dinv.PR, bool, error)
}

// checkouts are a pull request's repository, head branch and head commit as
// the inventory last read them.
type checkouts struct{ store prReader }

func (c checkouts) Checkout(ctx context.Context, u unit.Unit) (sourcereg.Checkout, bool, error) {
	ref, err := pr.ParseKey(u.Key)
	if err != nil || u.Provider != unit.GitHub {
		return sourcereg.Checkout{}, false, nil
	}
	out := sourcereg.Checkout{Repo: ref.Owner + "/" + ref.Repo}
	if c.store == nil {
		return out, true, nil
	}
	stored, ok, err := c.store.PR(ctx, ref)
	if err != nil {
		return sourcereg.Checkout{}, false, err
	}
	if ok {
		out.Branch, out.Head = stored.HeadBranch, stored.HeadOid
	}
	return out, true, nil
}
