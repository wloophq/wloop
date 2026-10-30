package present

import (
	"context"

	"github.com/wloophq/wloop/internal/readmodel"
	"github.com/wloophq/wloop/internal/readmodel/prrow"
)

// recordSituations writes, in one transaction, a situation event for each
// followed open pull request whose situation differs from its last one,
// logging the first failure of a streak only.
func (p *Presenter) recordSituations(ctx context.Context, k readmodel.Kept) {
	sits := prrow.Situations(k, p.h.Now())
	if len(sits) == 0 {
		return
	}
	err := p.d.Store.RecordSituations(ctx, sits)
	if failing := p.situationsFailing.Swap(err != nil); err != nil && !failing {
		p.h.Logf("situations: %v", err)
	}
}
