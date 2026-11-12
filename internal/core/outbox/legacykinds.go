package outbox

import (
	"cmp"
	"encoding/json"
	"slices"

	"github.com/wloophq/wloop/internal/domain/draft"
)

// legacyKinds are the kinds the draft package still enumerates, which a Service built with no Kinds reads.
// Only tests build one: serve sets Kinds from the manifests. This file goes with those enumerations.
type legacyKinds struct{}

var legacyDeliveries = []draft.Delivery{draft.Message, draft.Reaction, draft.CommentDelivery, draft.EditDelivery, draft.DeleteDelivery}

func (legacyKinds) Lookup(_, kind string) (draft.KindSpec, bool) {
	k := draft.Kind(kind)
	if !slices.Contains(draft.Kinds, k) {
		return draft.KindSpec{}, false
	}
	_, d := draft.ShapeOf(k)
	return legacySpec(k, d), true
}

func (legacyKinds) ForDelivery(_, delivery string) (draft.KindSpec, bool) {
	d := cmp.Or(draft.Delivery(delivery), draft.Message)
	if !slices.Contains(legacyDeliveries, d) {
		return draft.KindSpec{}, false
	}
	return legacySpec(draft.KindOf("", d), d), true
}

func (legacyKinds) Validate(draft.KindSpec, json.RawMessage) error { return nil }

func legacySpec(k draft.Kind, d draft.Delivery) draft.KindSpec {
	return draft.KindSpec{Name: string(k), Delivery: string(d), TextRequired: d != draft.Reaction && d != draft.DeleteDelivery}
}
