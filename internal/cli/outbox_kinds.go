package cli

import (
	"encoding/json"
	"fmt"

	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

// newManifestKinds are the outbox's draft kinds over the catalog catalog returns.
func newManifestKinds(catalog func() *draft.Catalog) manifestKinds {
	return manifestKinds{catalog: catalog}
}

// manifestKinds are the outbox's draft kinds: the catalog the installed
// plugins' manifests make, read when asked, so a vocabulary installed later
// is the one read.
type manifestKinds struct {
	catalog func() *draft.Catalog
}

var _ outbox.Kinds = manifestKinds{}

func (k manifestKinds) Lookup(provider, kind string) (draft.KindSpec, bool) {
	return k.catalog().Lookup(provider, kind)
}

// ForDelivery is provider's first declared kind with delivery, or its first kind for "".
func (k manifestKinds) ForDelivery(provider, delivery string) (draft.KindSpec, bool) {
	for _, s := range k.catalog().Kinds() {
		if s.Provider == provider && (delivery == "" || s.Delivery == delivery) {
			return s, true
		}
	}
	return draft.KindSpec{}, false
}

func (k manifestKinds) Validate(spec draft.KindSpec, payload json.RawMessage) error {
	if err := k.catalog().Validate(spec, payload); err != nil {
		return fmt.Errorf("%w: %w", outbox.ErrInvalid, err)
	}
	return nil
}

// Shape gives d its kind, purpose and delivery from provider's manifest.
func (k manifestKinds) Shape(provider string, d draft.Draft) (draft.Draft, error) {
	d, err := k.catalog().Shape(provider, d)
	if err != nil {
		return d, fmt.Errorf("%w: %w", outbox.ErrInvalid, err)
	}
	return d, nil
}

// draftKinds are the outbox's draft kinds over the installed vocabulary.
var draftKinds = newManifestKinds(func() *draft.Catalog { return builtin.DraftKinds })
