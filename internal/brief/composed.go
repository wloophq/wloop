package brief

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sort"

	"github.com/wloophq/wloop/internal/domain/jsonschema"
)

// ErrExtra is a model answer whose core brief fits and whose extra fields
// do not: the core brief is kept and the fields stay missing.
var ErrExtra = errors.New("the brief's extra fields do not fit their schema")

// Composed is the schema a brief call sends and Go validates the answer
// against: the core schema Go owns (goal, context, kind, size, risk,
// risk_why, open_questions, tags and the provider's facets) plus
// {"extra": <the brief intent's schema.json>}. The zero Composed is the
// core schema alone.
type Composed struct {
	// Core is the core schema's JSON.
	Core string
	// Extra is the brief intent's flat extra schema; nil when it declares
	// none, and then JSON is Core byte for byte.
	Extra *jsonschema.Schema

	json     string
	core     *jsonschema.Schema
	versions map[string]string
}

// Compose is the composed schema of the core schema and extra, the brief
// intent's schema.json; a nil extra adds nothing. extra must be a flat
// object schema: a nested object or a keyword outside the subset fails,
// naming the field.
func Compose(core string, extra []byte) (Composed, error) {
	lenient, err := tagsOpen(core)
	if err != nil {
		return Composed{}, fmt.Errorf("brief: the core schema: %w", err)
	}
	cs, err := jsonschema.Compile(lenient, jsonschema.Options{})
	if err != nil {
		return Composed{}, fmt.Errorf("brief: the core schema: %w", err)
	}
	c := Composed{Core: core, json: core, core: cs}
	if extra == nil {
		return c, nil
	}
	if c.Extra, err = jsonschema.Compile(extra, jsonschema.Options{Flat: true}); err != nil {
		return Composed{}, fmt.Errorf("brief: the brief intent's schema.json: %w", err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(core), &doc); err != nil {
		return Composed{}, err
	}
	var ex any
	if err := json.Unmarshal(extra, &ex); err != nil {
		return Composed{}, err
	}
	doc["properties"].(map[string]any)["extra"] = ex
	doc["required"] = append(doc["required"].([]any), "extra")
	b, err := json.Marshal(doc)
	if err != nil {
		return Composed{}, err
	}
	c.json = string(b)
	c.versions = map[string]string{}
	for _, p := range c.Extra.Properties() {
		c.versions[p.Name] = fieldVersion(p.Source)
	}
	return c, nil
}

// tagsOpen is the core schema with the tag values' enums left out: a tag
// value outside its key's list is dropped by normalization, alone, and
// never fails the brief.
func tagsOpen(core string) ([]byte, error) {
	var doc map[string]any
	if err := json.Unmarshal([]byte(core), &doc); err != nil {
		return nil, err
	}
	props, _ := doc["properties"].(map[string]any)
	tags, _ := props["tags"].(map[string]any)
	keys, _ := tags["properties"].(map[string]any)
	for _, k := range keys {
		if items, ok := k.(map[string]any)["items"].(map[string]any); ok {
			delete(items, "enum")
		}
	}
	return json.Marshal(doc)
}

func fieldVersion(src []byte) string {
	var compact bytes.Buffer
	if err := json.Compact(&compact, src); err != nil {
		compact.Reset()
		compact.Write(src)
	}
	h := sha256.Sum256(compact.Bytes())
	return "f1-" + hex.EncodeToString(h[:6])
}

func (c Composed) orCore() Composed {
	if c.core != nil {
		return c
	}
	d, err := Compose(Schema, nil)
	if err != nil {
		panic(err)
	}
	return d
}

// JSON is the schema sent to the model.
func (c Composed) JSON() string { return c.orCore().json }

// Validate checks a model's answer against c, then decodes it; a bad extra
// fails only the extras, keeping the core brief, with ErrExtra.
func (c Composed) Validate(answer []byte) (Brief, error) {
	b, _, err := c.decode(answer)
	return b, err
}

// decode is Validate with the tags normalization dropped.
func (c Composed) decode(answer []byte) (Brief, []string, error) {
	c = c.orCore()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(answer, &fields); err != nil {
		return Brief{}, nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	extra, hasExtra := fields["extra"]
	core := answer
	if hasExtra && c.Extra != nil {
		delete(fields, "extra")
		var err error
		if core, err = json.Marshal(fields); err != nil {
			return Brief{}, nil, err
		}
	}
	if err := c.core.ValidateJSON(core); err != nil {
		return Brief{}, nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	b, dropped, err := decode(core)
	if err != nil || c.Extra == nil {
		return b, dropped, err
	}
	if !hasExtra {
		return b, dropped, fmt.Errorf("%w: no extra", ErrExtra)
	}
	values, err := c.decodeExtra(extra, nil)
	if err != nil {
		return b, dropped, err
	}
	b.Extra, b.ExtraVersions = values, c.FieldVersions()
	return b, dropped, nil
}

// decodeExtra validates an answer's extra object: the whole extra schema,
// or only names, every one required, when names is not nil.
func (c Composed) decodeExtra(raw json.RawMessage, names []string) (map[string]any, error) {
	var values map[string]any
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrExtra, err)
	}
	s := c.Extra
	if names != nil {
		s = c.fieldsSchema(names)
	}
	if err := s.Validate(values); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrExtra, err)
	}
	return values, nil
}

// FieldVersions are the versions of c's extra fields, by name: a hash of
// each property's schema.
func (c Composed) FieldVersions() map[string]string {
	if len(c.versions) == 0 {
		return nil
	}
	return maps.Clone(c.versions)
}

// Missing are the extra fields b lacks at their current version, by name.
func (c Composed) Missing(b Brief) []string {
	var out []string
	for name, v := range c.versions {
		if b.ExtraVersions[name] != v {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func (c Composed) fieldsSchema(names []string) *jsonschema.Schema {
	props := map[string]json.RawMessage{}
	for _, p := range c.Extra.Properties() {
		if slices.Contains(names, p.Name) {
			props[p.Name] = p.Source
		}
	}
	b, _ := json.Marshal(map[string]any{"type": "object", "additionalProperties": false, "required": names, "properties": props})
	s, err := jsonschema.Compile(b, jsonschema.Options{})
	if err != nil {
		panic(fmt.Sprintf("brief: the fields schema of %v: %v", names, err))
	}
	return s
}

// FieldsJSON is the schema of a missing-fields answer, {"extra": {...}}
// holding names alone, every one required.
func (c Composed) FieldsJSON(names []string) string {
	b, _ := json.Marshal(map[string]any{"type": "object", "additionalProperties": false, "required": []string{"extra"},
		"properties": map[string]json.RawMessage{"extra": c.fieldsSchema(names).Source()}})
	return string(b)
}

// DecodeFields validates a missing-fields answer asking for names.
func (c Composed) DecodeFields(answer []byte, names []string) (map[string]any, error) {
	var a struct {
		Extra json.RawMessage `json:"extra"`
	}
	dec := json.NewDecoder(bytes.NewReader(answer))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&a); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrExtra, err)
	}
	if a.Extra == nil {
		return nil, fmt.Errorf("%w: no extra", ErrExtra)
	}
	return c.decodeExtra(a.Extra, names)
}

// WithFields is b with values filled at their current versions.
func (c Composed) WithFields(b Brief, values map[string]any) Brief {
	extra, versions := maps.Clone(b.Extra), maps.Clone(b.ExtraVersions)
	if extra == nil {
		extra, versions = map[string]any{}, map[string]string{}
	}
	if versions == nil {
		versions = map[string]string{}
	}
	for k, v := range values {
		extra[k], versions[k] = v, c.versions[k]
	}
	b.Extra, b.ExtraVersions = extra, versions
	return b
}

// ComposeExtra is Compose with the extra schema made of a brief intent's
// properties, every one required: the brief intent's Def.Extra.
func ComposeExtra(core string, props []jsonschema.Property) (Composed, error) {
	if len(props) == 0 {
		return Compose(core, nil)
	}
	fields := map[string]json.RawMessage{}
	names := make([]string, 0, len(props))
	for _, p := range props {
		fields[p.Name] = p.Source
		names = append(names, p.Name)
	}
	extra, err := json.Marshal(map[string]any{"type": "object", "additionalProperties": false, "required": names, "properties": fields})
	if err != nil {
		return Composed{}, err
	}
	return Compose(core, extra)
}
