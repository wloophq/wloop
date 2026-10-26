package cli

import (
	"context"
	"errors"

	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/domain/draft"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	intentload "github.com/wloophq/wloop/internal/intent"
)

type turnBooks interface {
	Books(ctx context.Context, units ...unit.Unit) (map[unit.Unit]dturn.Book, error)
}

// outboxManifests is the outbox's view of the loaded intents and workflows and of the turns book.
type outboxManifests struct {
	loaded func() *intentload.Loaded
	books  turnBooks
	// config is the config whose workflows.<name>.draft_approval replaces a
	// workflow's block; nil reads none.
	config func() config.Config
}

// outboxPolicies resolves draft approvals from the manifests as loaded at each call.
func (e *Env) outboxPolicies() outbox.Policies {
	return outbox.Resolver{Manifests: outboxManifests{loaded: e.loadedIntents, books: envBooks{e}, config: e.currentConfig}}
}

var errNoTurns = errors.New("no turns book is wired")

type envBooks struct{ e *Env }

func (b envBooks) Books(ctx context.Context, units ...unit.Unit) (map[unit.Unit]dturn.Book, error) {
	if b.e.Turns == nil || b.e.Turns.Store == nil {
		return nil, errNoTurns
	}
	return b.e.Turns.Store.Books(ctx, units...)
}

func (m outboxManifests) IntentApproval(name string) (*draft.Policy, bool) {
	d, ok := m.loaded().Registry.Get(dintent.Name(name))
	return d.DraftApproval, ok
}

func (m outboxManifests) WorkflowApproval(name string) (*draft.Policy, bool) {
	for _, w := range m.loaded().Workflows {
		if w.Name != name {
			continue
		}
		if m.config != nil {
			if p := configPolicy(w, m.config().Workflows[name].DraftApproval); p != nil {
				return p, true
			}
		}
		return w.DraftApproval, true
	}
	return nil, false
}

func (m outboxManifests) Turn(ctx context.Context, u unit.Unit, seq int64) (string, string, bool, error) {
	books, err := m.books.Books(ctx, u)
	if err != nil {
		return "", "", false, err
	}
	b := books[u]
	for i := len(b.Turns) - 1; i >= 0; i-- {
		if t := b.Turns[i]; t.Seq == seq {
			return string(t.Intent), t.Workflow, true, nil
		}
	}
	return "", "", false, nil
}
