// Package jsonschema validates structured agent answers against the subset of
// JSON Schema the profile's schemas may use: type, properties, required,
// additionalProperties, enum, minLength, maxLength, minItems, maxItems,
// items, pattern (RE2) and description. Compile refuses every other keyword,
// naming the field.
package jsonschema

import "errors"

// ErrNotImplemented is what the stubs return until the validator lands.
var ErrNotImplemented = errors.New("jsonschema: not implemented")

// Keywords are the JSON Schema keywords Compile accepts.
var Keywords = []string{"type", "properties", "required", "additionalProperties", "enum", "minLength", "maxLength",
	"minItems", "maxItems", "items", "pattern", "description"}

// Schema is a compiled schema.
type Schema struct {
	src []byte
}

// Options narrow what Compile accepts.
type Options struct {
	// Flat refuses an object nested below the top level, as the brief
	// intent's extra schema must be.
	Flat bool
}

// Compile parses src, refusing a keyword outside Keywords or, with Flat, a
// nested object; the error names the field.
func Compile(src []byte, o Options) (*Schema, error) {
	_ = o
	return nil, ErrNotImplemented
}

// Source is the schema as written.
func (s *Schema) Source() []byte { return s.src }

// Validate checks a decoded JSON value against s; the error names the first
// field that does not fit.
func (s *Schema) Validate(v any) error {
	_ = v
	return ErrNotImplemented
}

// ValidateJSON decodes raw and validates it.
func (s *Schema) ValidateJSON(raw []byte) error {
	_ = raw
	return ErrNotImplemented
}

// Property is one top-level property of a flat object schema, as a fact
// reads it.
type Property struct {
	Name string
	// Type is string, integer, boolean or array (of strings).
	Type string
	Enum []string
	// Source is the property's own schema bytes, which its field version
	// hashes.
	Source []byte
}

// Properties are the top-level properties of an object schema, by name.
func (s *Schema) Properties() []Property { return nil }
