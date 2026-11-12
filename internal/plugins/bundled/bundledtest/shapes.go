// Package bundledtest holds what tests read of the bundled plugins' vocabulary without loading them.
package bundledtest

import (
	"encoding/json"
	"fmt"
	"slices"
	"sync"

	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/domain/draft"
)

// shapes are, by kind, the deliveries of the kinds a conversation plugin declares in Shapes.
var shapes = map[string]string{
	"reply": "message", "nudge": "message", "close": "message", "ask_owner": "message",
	"react": "reaction", "comment": "comment", "edit": "edit", "delete": "delete",
}

// order is the order the kinds are declared in, which picks the kind of a delivery a draft names no kind of.
var order = []string{"reply", "nudge", "close", "ask_owner", "react", "comment", "edit", "delete"}

// Shapes are the kinds the bundled conversation plugins declare, for every provider of a test outbox: a reply, a nudge, a close and an
// ask_owner post a message, react a reaction, comment a comment, edit and delete change a message. A draft of no kind is shaped as
// draft.Catalog shapes it, message being every provider's default delivery.
type Shapes struct{}

var _ outbox.Kinds = Shapes{}

var catalogs sync.Map

func (Shapes) Lookup(provider, kind string) (draft.KindSpec, bool) {
	d, ok := shapes[kind]
	if !ok {
		return draft.KindSpec{}, false
	}
	text := d != "reaction" && d != "delete"
	spec := draft.KindSpec{Provider: provider, Name: kind, Delivery: d, TextRequired: text}
	if text {
		spec.Editable = []string{draft.EditableText}
	}
	return spec, true
}

func (s Shapes) Shape(provider string, d draft.Draft) (draft.Draft, error) {
	got, err := s.catalog(provider).Shape(provider, d)
	if err != nil {
		return d, fmt.Errorf("%w: %w", outbox.ErrInvalid, err)
	}
	return got, nil
}

// catalog is provider's kinds in a draft.Catalog, built once.
func (s Shapes) catalog(provider string) *draft.Catalog {
	if c, ok := catalogs.Load(provider); ok {
		return c.(*draft.Catalog)
	}
	var kinds []draft.KindSpec
	var deliveries []draft.DeliverySpec
	for _, k := range order {
		spec, _ := s.Lookup(provider, k)
		spec.SchemaJSON = json.RawMessage(`{"type":"object"}`)
		kinds = append(kinds, spec)
		if dl := (draft.DeliverySpec{Provider: provider, Name: spec.Delivery}); !slices.Contains(deliveries, dl) {
			deliveries = append(deliveries, dl)
		}
	}
	c, err := draft.NewCatalog(kinds, deliveries, map[string]string{provider: "message"})
	if err != nil {
		panic(err)
	}
	got, _ := catalogs.LoadOrStore(provider, c)
	return got.(*draft.Catalog)
}

func (Shapes) Validate(draft.KindSpec, json.RawMessage) error { return nil }
