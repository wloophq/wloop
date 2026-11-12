package cli

import (
	"encoding/json"
	"fmt"

	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

// outboxKinds are the outbox's draft kinds: the catalog the installed
// plugins' manifests make, read when asked, so a vocabulary installed later
// is the one read.
type outboxKinds struct {
	catalog func() *draft.Catalog
}

var _ outbox.Kinds = outboxKinds{}

func (k outboxKinds) Lookup(provider, kind string) (draft.KindSpec, bool) {
	return k.catalog().Lookup(provider, kind)
}

func (k outboxKinds) Validate(spec draft.KindSpec, payload json.RawMessage) error {
	if err := k.catalog().Validate(spec, payload); err != nil {
		return fmt.Errorf("%w: %w", outbox.ErrInvalid, err)
	}
	return nil
}

// Shape gives d its kind, purpose and delivery from provider's manifest.
func (k outboxKinds) Shape(provider string, d draft.Draft) (draft.Draft, error) {
	d, err := k.catalog().Shape(provider, d)
	if err != nil {
		return d, fmt.Errorf("%w: %w", outbox.ErrInvalid, err)
	}
	return d, nil
}

// draftKinds are the outbox's draft kinds over the installed vocabulary.
var draftKinds = outboxKinds{catalog: func() *draft.Catalog { return builtin.DraftKinds }}
