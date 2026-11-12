package outboxtest

import (
	"cmp"
	"encoding/json"
	"fmt"

	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/domain/draft"
)

// shapes are, by kind, the deliveries of the kinds a conversation plugin declares in Shapes.
var shapes = map[string]string{
	"reply": "message", "nudge": "message", "close": "message", "ask_owner": "message",
	"react": "reaction", "comment": "comment", "edit": "edit", "delete": "delete",
}

// Shapes are the kinds a conversation plugin declares, for every provider: a reply, a nudge, a close and an
// ask_owner post a message, react a reaction, comment a comment, edit and delete change a message; a draft of
// no kind is the kind its purpose names for its delivery, else a reply of a message, else its delivery's kind.
type Shapes struct{}

var _ outbox.Kinds = Shapes{}

func (Shapes) Lookup(_, kind string) (draft.KindSpec, bool) {
	d, ok := shapes[kind]
	if !ok {
		return draft.KindSpec{}, false
	}
	return draft.KindSpec{Name: kind, Delivery: d, TextRequired: d != "reaction" && d != "delete"}, true
}

func (s Shapes) Shape(provider string, d draft.Draft) (draft.Draft, error) {
	name := string(d.Kind)
	if name == "" {
		name = shapeOf(d.Purpose, cmp.Or(string(d.Delivery), "message"))
	}
	spec, ok := s.Lookup(provider, name)
	if !ok {
		return d, fmt.Errorf("%w: %w: %q", outbox.ErrInvalid, draft.ErrUnknownKind, name)
	}
	d.Kind, d.Purpose, d.Delivery = draft.Kind(spec.Name), cmp.Or(d.Purpose, spec.Name), draft.Delivery(spec.Delivery)
	return d, nil
}

func shapeOf(purpose, delivery string) string {
	if shapes[purpose] == delivery {
		return purpose
	}
	if delivery == "message" {
		return "reply"
	}
	for _, k := range []string{"react", "comment", "edit", "delete"} {
		if shapes[k] == delivery {
			return k
		}
	}
	return ""
}

func (Shapes) Validate(draft.KindSpec, json.RawMessage) error { return nil }
