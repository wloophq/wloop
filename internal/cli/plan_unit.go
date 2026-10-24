package cli

import (
	"context"

	"github.com/wloophq/wloop/internal/daemon"
	"github.com/wloophq/wloop/internal/domain/unit"
)

var _ daemon.Explainer = servedPlan{}

func (p servedPlan) Explain(ctx context.Context, u unit.Unit) (daemon.Explained, error) {
	return daemon.Explain(ctx, p.plan, u)
}
