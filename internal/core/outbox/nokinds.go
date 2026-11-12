package outbox

import (
	"cmp"
	"encoding/json"

	"github.com/wloophq/wloop/internal/domain/draft"
)

// noKinds are the kinds of a Service built with no Kinds: none is declared, so a draft keeps the kind,
// purpose and delivery its proposer gave, and no payload is checked.
type noKinds struct{}

func (noKinds) Lookup(string, string) (draft.KindSpec, bool) { return draft.KindSpec{}, false }

func (noKinds) Shape(_ string, d draft.Draft) (draft.Draft, error) {
	d.Purpose = cmp.Or(d.Purpose, string(d.Kind))
	return d, nil
}

func (noKinds) Validate(draft.KindSpec, json.RawMessage) error { return nil }
