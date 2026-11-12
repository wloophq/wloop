package outbox

import (
	"encoding/json"
	"fmt"

	"github.com/wloophq/wloop/internal/domain/draft"
)

// testProviders are the providers this package's tests post drafts on.
var testProviders = []string{"github", "slack", "chat"}

// testKinds are, by kind, the delivery of the kinds every test provider declares, as a conversation plugin's
// manifest would; a draft of no kind takes message, every provider's default delivery.
var testKinds = map[string]string{
	"reply": "message", "nudge": "message", "close": "message", "ask_owner": "message",
	"react": "reaction", "comment": "comment", "edit": "edit", "delete": "delete",
}

// shapes are the test providers' kinds in a draft.Catalog, built as the manifests' merge builds one.
type shapes struct{ c *draft.Catalog }

func newShapes() shapes {
	var kinds []draft.KindSpec
	var deliveries []draft.DeliverySpec
	defaults := map[string]string{}
	for _, p := range testProviders {
		for _, d := range []string{"message", "reaction", "comment", "edit", "delete"} {
			deliveries = append(deliveries, draft.DeliverySpec{Provider: p, Name: d, Posts: d == "message" || d == "comment"})
		}
		for _, k := range []string{"reply", "nudge", "close", "ask_owner", "react", "comment", "edit", "delete"} {
			d := testKinds[k]
			kinds = append(kinds, draft.KindSpec{Provider: p, Name: k, Delivery: d, TextRequired: d != "reaction" && d != "delete",
				SchemaJSON: json.RawMessage(`{"type":"object","properties":{}}`)})
		}
		defaults[p] = "message"
	}
	c, err := draft.NewCatalog(kinds, deliveries, defaults)
	if err != nil {
		panic(err)
	}
	return shapes{c}
}

func (s shapes) Lookup(provider, kind string) (draft.KindSpec, bool) {
	return s.c.Lookup(provider, kind)
}

func (s shapes) Shape(provider string, d draft.Draft) (draft.Draft, error) {
	got, err := s.c.Shape(provider, d)
	if err != nil {
		return d, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return got, nil
}

func (s shapes) Validate(spec draft.KindSpec, payload json.RawMessage) error {
	if err := s.c.Validate(spec, payload); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return nil
}
