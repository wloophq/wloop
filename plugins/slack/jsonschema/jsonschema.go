// Package jsonschema validates structured agent answers against the subset of
// JSON Schema the profile's schemas may use: type, properties, required,
// additionalProperties, enum, minLength, maxLength, minItems, maxItems,
// items, pattern (RE2) and description. Compile refuses every other keyword,
// naming the field.
package jsonschema

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"unicode/utf8"
)

// ErrSchema is wrapped by every error Compile returns.
var ErrSchema = errors.New("jsonschema: unsupported schema")

// ErrInvalid is wrapped by every error Validate returns.
var ErrInvalid = errors.New("does not fit the schema")

// Keywords are the JSON Schema keywords Compile accepts.
var Keywords = []string{"type", "properties", "required", "additionalProperties", "enum", "minLength", "maxLength",
	"minItems", "maxItems", "items", "pattern", "description"}

// Types are the values of type Compile accepts.
var Types = []string{"object", "array", "string", "integer", "number", "boolean", "null"}

// Schema is a compiled schema.
type Schema struct {
	src  []byte
	root *node
}

type node struct {
	src      []byte
	typ      string
	props    map[string]*node
	required []string
	closed   bool
	enum     []any
	minLen   *int
	maxLen   *int
	minItems *int
	maxItems *int
	items    *node
	pattern  *regexp.Regexp
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
	root, err := compile(src, "$", 0, o)
	if err != nil {
		return nil, err
	}
	if o.Flat && root.typ != "object" {
		return nil, fmt.Errorf("%w: $: a flat schema is an object, not %q", ErrSchema, root.typ)
	}
	return &Schema{src: bytes.Clone(src), root: root}, nil
}

func compile(src []byte, path string, depth int, o Options) (*node, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(src, &raw); err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrSchema, path, err)
	}
	keys := make([]string, 0, len(raw))
	for k := range raw {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !slices.Contains(Keywords, k) {
			return nil, fmt.Errorf("%w: %s: keyword %q is not supported", ErrSchema, path, k)
		}
	}
	bad := func(k string, err error) error { return fmt.Errorf("%w: %s: %s: %v", ErrSchema, path, k, err) }
	n := &node{src: bytes.Clone(src)}
	t, ok := raw["type"]
	if !ok {
		return nil, fmt.Errorf("%w: %s: no type", ErrSchema, path)
	}
	if err := json.Unmarshal(t, &n.typ); err != nil || !slices.Contains(Types, n.typ) {
		return nil, fmt.Errorf("%w: %s: type %s is not supported", ErrSchema, path, t)
	}
	if o.Flat && depth > 0 && n.typ == "object" {
		return nil, fmt.Errorf("%w: %s: a nested object", ErrSchema, path)
	}
	for _, l := range []struct {
		key string
		dst **int
	}{{"minLength", &n.minLen}, {"maxLength", &n.maxLen}, {"minItems", &n.minItems}, {"maxItems", &n.maxItems}} {
		v, ok := raw[l.key]
		if !ok {
			continue
		}
		var i int
		if err := json.Unmarshal(v, &i); err != nil || i < 0 {
			return nil, bad(l.key, fmt.Errorf("not a count: %s", v))
		}
		*l.dst = &i
	}
	if v, ok := raw["enum"]; ok {
		var vals []json.RawMessage
		if err := json.Unmarshal(v, &vals); err != nil || len(vals) == 0 {
			return nil, bad("enum", fmt.Errorf("not a list of values: %s", v))
		}
		for _, raw := range vals {
			e, err := enumValue(raw, n.typ)
			if err != nil {
				return nil, bad("enum", err)
			}
			n.enum = append(n.enum, e)
		}
	}
	if v, ok := raw["pattern"]; ok {
		var p string
		if err := json.Unmarshal(v, &p); err != nil {
			return nil, bad("pattern", err)
		}
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, bad("pattern", err)
		}
		n.pattern = re
	}
	if v, ok := raw["required"]; ok {
		if err := json.Unmarshal(v, &n.required); err != nil {
			return nil, bad("required", err)
		}
	}
	if v, ok := raw["additionalProperties"]; ok {
		var open bool
		if err := json.Unmarshal(v, &open); err != nil {
			return nil, bad("additionalProperties", fmt.Errorf("only true or false: %s", v))
		}
		n.closed = !open
	}
	if v, ok := raw["description"]; ok {
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			return nil, bad("description", err)
		}
	}
	if v, ok := raw["properties"]; ok {
		var props map[string]json.RawMessage
		if err := json.Unmarshal(v, &props); err != nil {
			return nil, bad("properties", err)
		}
		n.props = map[string]*node{}
		for name, p := range props {
			sub, err := compile(p, path+"."+name, depth+1, o)
			if err != nil {
				return nil, err
			}
			n.props[name] = sub
		}
	}
	for _, r := range n.required {
		if _, ok := n.props[r]; !ok {
			return nil, fmt.Errorf("%w: %s: required %q has no property", ErrSchema, path, r)
		}
	}
	if v, ok := raw["items"]; ok {
		sub, err := compile(v, path+"[]", depth+1, o)
		if err != nil {
			return nil, err
		}
		n.items = sub
	}
	if n.typ == "array" && n.items == nil {
		return nil, fmt.Errorf("%w: %s: an array without items", ErrSchema, path)
	}
	return n, nil
}

// Source is the schema as written.
func (s *Schema) Source() []byte { return s.src }

// Validate checks a decoded JSON value against s; the error names the first
// field that does not fit.
func (s *Schema) Validate(v any) error { return s.root.validate(v, "$") }

// ValidateJSON decodes raw and validates it.
func (s *Schema) ValidateJSON(raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if dec.More() {
		return fmt.Errorf("%w: text after the value", ErrInvalid)
	}
	return s.Validate(v)
}

func (n *node) validate(v any, path string) error {
	fail := func(f string, a ...any) error {
		return fmt.Errorf("%s: %s: %w", path, fmt.Sprintf(f, a...), ErrInvalid)
	}
	switch n.typ {
	case "object":
		obj, ok := v.(map[string]any)
		if !ok {
			return fail("not an object")
		}
		for _, r := range n.required {
			if _, ok := obj[r]; !ok {
				return fail("%s is missing", r)
			}
		}
		keys := make([]string, 0, len(obj))
		for k := range obj {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			sub, ok := n.props[k]
			if !ok {
				if n.closed {
					return fail("unknown field %s", k)
				}
				continue
			}
			if err := sub.validate(obj[k], path+"."+k); err != nil {
				return err
			}
		}
	case "array":
		arr, ok := v.([]any)
		if !ok {
			return fail("not an array")
		}
		if n.maxItems != nil && len(arr) > *n.maxItems {
			return fail("%d entries, over %d", len(arr), *n.maxItems)
		}
		if n.minItems != nil && len(arr) < *n.minItems {
			return fail("%d entries, under %d", len(arr), *n.minItems)
		}
		for i, e := range arr {
			if err := n.items.validate(e, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	case "string":
		s, ok := v.(string)
		if !ok {
			return fail("not a string")
		}
		c := utf8.RuneCountInString(s)
		if n.maxLen != nil && c > *n.maxLen {
			return fail("%d characters, over %d", c, *n.maxLen)
		}
		if n.minLen != nil && c < *n.minLen {
			return fail("%d characters, under %d", c, *n.minLen)
		}
		if n.pattern != nil && !n.pattern.MatchString(s) {
			return fail("%q does not match %s", s, n.pattern)
		}
	case "integer", "number":
		f, ok := number(v)
		if !ok {
			return fail("not a number")
		}
		if n.typ == "integer" && f != float64(int64(f)) {
			return fail("not an integer")
		}
	case "boolean":
		if _, ok := v.(bool); !ok {
			return fail("not a boolean")
		}
	case "null":
		if v != nil {
			return fail("not null")
		}
	}
	if n.enum != nil && !slices.ContainsFunc(n.enum, func(e any) bool { return same(e, v) }) {
		return fail("%v is not one of %v", v, n.enum)
	}
	return nil
}

// enumValue decodes one enum value, refusing one that is not a scalar of the
// node's type: validate compares enum values with ==, and no answer could fit
// a value of another type.
func enumValue(raw json.RawMessage, typ string) (any, error) {
	var e any
	if err := json.Unmarshal(raw, &e); err != nil {
		return nil, err
	}
	var fits bool
	switch x := e.(type) {
	case string:
		fits = typ == "string"
	case float64:
		fits = typ == "number" || typ == "integer" && x == float64(int64(x))
	case bool:
		fits = typ == "boolean"
	case nil:
		fits = typ == "null"
	default:
		return nil, fmt.Errorf("%s is not a string, number, boolean or null", raw)
	}
	if !fits {
		return nil, fmt.Errorf("%s is not %s %s", raw, article(typ), typ)
	}
	return e, nil
}

func article(typ string) string {
	if typ == "integer" || typ == "array" || typ == "object" {
		return "an"
	}
	return "a"
}

func number(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	}
	return 0, false
}

func same(a, b any) bool {
	if fa, ok := number(a); ok {
		fb, ok := number(b)
		return ok && fa == fb
	}
	return a == b
}

// Property is one top-level property of a flat object schema, as a fact
// reads it.
type Property struct {
	Name string
	// Type is the property's type.
	Type string
	// Items is the type of an array property's items.
	Items string
	// Enum are the string values of the property, or of its items.
	Enum []string
	// Source is the property's own schema bytes, which its field version
	// hashes.
	Source []byte
}

// Properties are the top-level properties of an object schema, by name.
func (s *Schema) Properties() []Property {
	if s == nil || s.root == nil {
		return nil
	}
	names := make([]string, 0, len(s.root.props))
	for k := range s.root.props {
		names = append(names, k)
	}
	sort.Strings(names)
	out := make([]Property, 0, len(names))
	for _, k := range names {
		p := s.root.props[k]
		prop := Property{Name: k, Type: p.typ, Source: p.src}
		enum := p.enum
		if p.typ == "array" {
			enum = p.items.enum
			prop.Items = p.items.typ
		}
		for _, e := range enum {
			if s, ok := e.(string); ok {
				prop.Enum = append(prop.Enum, s)
			}
		}
		out = append(out, prop)
	}
	return out
}
