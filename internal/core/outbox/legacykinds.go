package outbox

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/wloophq/wloop/internal/domain/draft"
)

// legacyKinds are the kinds the draft package still enumerates, which a Service built with no Kinds reads:
// serve builds one until it wires the manifests' Kinds. This file goes with those enumerations.
type legacyKinds struct{}

var legacyDeliveries = []draft.Delivery{draft.Message, draft.Reaction, draft.CommentDelivery, draft.EditDelivery, draft.DeleteDelivery}

func (legacyKinds) Lookup(_, kind string) (draft.KindSpec, bool) {
	k := draft.Kind(kind)
	if !slices.Contains(draft.Kinds, k) {
		return draft.KindSpec{}, false
	}
	_, d := draft.ShapeOf(k)
	return draft.KindSpec{Name: kind, Delivery: string(d), TextRequired: d != draft.Reaction && d != draft.DeleteDelivery}, true
}

// Shape is the shaping the draft package enumerates: a kind gives its purpose and delivery, an unnamed
// delivery is a message, and the kind is the one of the purpose and the delivery.
func (legacyKinds) Shape(_ string, d draft.Draft) (draft.Draft, error) {
	if d.Kind != "" && !slices.Contains(draft.Kinds, d.Kind) {
		return d, fmt.Errorf("%w: unknown kind %q", ErrInvalid, d.Kind)
	}
	if d.Purpose == "" && d.Kind != "" {
		d.Purpose, d.Delivery = draft.ShapeOf(d.Kind)
	}
	if d.Delivery == "" {
		d.Delivery = draft.Message
	}
	if !slices.Contains(legacyDeliveries, d.Delivery) {
		return d, fmt.Errorf("%w: unknown delivery %q", ErrInvalid, d.Delivery)
	}
	d.Kind = draft.KindOf(d.Purpose, d.Delivery)
	return d, nil
}

func (legacyKinds) Validate(draft.KindSpec, json.RawMessage) error { return nil }
