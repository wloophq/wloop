package outbox

import (
	"cmp"
	"encoding/json"
	"fmt"

	"github.com/wloophq/wloop/internal/domain/draft"
)

// testShapes are the kinds of every provider in this package's tests: what a conversation plugin declares,
// each kind's delivery, and message for a draft that names no kind.
var testShapes = map[string]string{
	"reply": "message", "nudge": "message", "close": "message", "ask_owner": "message",
	"react": "reaction", "comment": "comment", "edit": "edit", "delete": "delete",
}

type shapes struct{}

func (shapes) Lookup(_, kind string) (draft.KindSpec, bool) {
	d, ok := testShapes[kind]
	if !ok {
		return draft.KindSpec{}, false
	}
	return draft.KindSpec{Name: kind, Delivery: d, TextRequired: d != "reaction" && d != "delete"}, true
}

func (s shapes) Shape(provider string, d draft.Draft) (draft.Draft, error) {
	name := string(d.Kind)
	if name == "" {
		name = kindOfShape(d.Purpose, string(cmp.Or(d.Delivery, "message")))
	}
	spec, ok := s.Lookup(provider, name)
	if !ok {
		return d, fmt.Errorf("%w: unknown kind %q", ErrInvalid, name)
	}
	d.Kind, d.Purpose, d.Delivery = draft.Kind(spec.Name), cmp.Or(d.Purpose, spec.Name), draft.Delivery(spec.Delivery)
	return d, nil
}

// kindOfShape is the kind of a purpose and a delivery: the purpose when it is a kind of that delivery, else
// the first message kind for a message, else the kind of the delivery.
func kindOfShape(purpose, delivery string) string {
	if testShapes[purpose] == delivery {
		return purpose
	}
	if delivery == "message" {
		return "reply"
	}
	for _, k := range []string{"react", "comment", "edit", "delete"} {
		if testShapes[k] == delivery {
			return k
		}
	}
	return ""
}

func (shapes) Validate(draft.KindSpec, json.RawMessage) error { return nil }
