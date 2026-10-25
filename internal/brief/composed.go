package brief

import (
	"errors"

	"github.com/wloophq/wloop/internal/domain/jsonschema"
)

// ErrComposeNotImplemented is what Compose answers until the composed
// schema lands.
var ErrComposeNotImplemented = errors.New("brief: the composed schema is not implemented")

// Composed is the schema a brief call sends and Go validates the answer
// against: the core schema Go owns (goal, context, kind, size, risk,
// risk_why, open_questions, tags and the provider's facets) plus
// {"extra": <the brief intent's schema.json>}.
type Composed struct {
	// Core is the core schema's JSON.
	Core string
	// Extra is the brief intent's flat extra schema; nil when it declares
	// none, and then JSON is Core byte for byte.
	Extra *jsonschema.Schema
}

// Compose is the composed schema of the core schema and extra, the brief
// intent's schema.json; a nil extra adds nothing.
func Compose(core string, extra []byte) (Composed, error) {
	if extra == nil {
		return Composed{Core: core}, nil
	}
	return Composed{}, ErrComposeNotImplemented
}

// JSON is the schema sent to the model.
func (c Composed) JSON() string { return c.Core }

// Validate checks a model's answer against c, then decodes it; a bad extra
// fails only the extras, keeping the core brief.
func (c Composed) Validate(answer []byte) (Brief, error) {
	_ = answer
	return Brief{}, ErrComposeNotImplemented
}

// FieldVersions are the versions of c's extra fields, by name: a hash of
// each property's schema.
func (c Composed) FieldVersions() map[string]string { return nil }
