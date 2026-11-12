package manifest

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"
)

// The bases a draft kind names.
var draftBases = []string{"", "agent"}

// editableText is the editable entry that names a draft's text.
const editableText = "text"

// readDraftKinds reads each draft kind's schema from files and checks what
// the kind declares against it: every fact is named draft.<provider>.<kind>.<field>
// and points to a required string enum, so a rule that reads it never reads
// an absent value; every editable pointer names a property. A preview a kind
// names may be missing.
func (s *Source) readDraftKinds(files fs.FS) error {
	var errs []error
	bad := func(k DraftKind, format string, args ...any) {
		errs = append(errs, fmt.Errorf("draft kind %q: %s", k.Name, fmt.Sprintf(format, args...)))
	}
	var facts []string
	for i := range s.DraftKinds {
		k := &s.DraftKinds[i]
		if !slices.Contains(draftBases, k.Basis) {
			bad(*k, "basis %q is not agent or empty", k.Basis)
		}
		for _, p := range []string{k.Schema, k.Preview} {
			if p != "" && !fs.ValidPath(p) {
				bad(*k, "%q is not a path inside the plugin's directory", p)
			}
		}
		schema, err := readSchema(files, k.Schema)
		if err != nil {
			bad(*k, "schema: %v", err)
			continue
		}
		k.SchemaJSON = schema.raw
		for _, f := range k.Facts {
			if slices.Contains(facts, f.Name) {
				bad(*k, "fact %q is declared twice", f.Name)
			}
			facts = append(facts, f.Name)
			prefix := "draft." + s.Provider + "." + k.Name + "."
			if field, ok := strings.CutPrefix(f.Name, prefix); !ok || !nameRe.MatchString(field) || strings.Contains(field, ".") {
				bad(*k, "fact %q is not named %s<field>", f.Name, prefix)
			}
			if err := schema.enum(f.Pointer); err != nil {
				bad(*k, "fact %q: %v", f.Name, err)
			}
		}
		for _, e := range k.Editable {
			if e == editableText {
				continue
			}
			if _, _, err := schema.at(e); err != nil {
				bad(*k, "editable %q: %v", e, err)
			}
		}
	}
	return errors.Join(errs...)
}

// draftSchema is a draft kind's JSON schema, decoded.
type draftSchema struct {
	raw  json.RawMessage
	root map[string]any
}

func readSchema(files fs.FS, name string) (draftSchema, error) {
	switch {
	case name == "":
		return draftSchema{}, errors.New("is required")
	case files == nil:
		return draftSchema{}, fmt.Errorf("%s: no plugin directory to read it from", name)
	}
	raw, err := fs.ReadFile(files, path.Clean(name))
	if err != nil {
		return draftSchema{}, err
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil || root == nil {
		return draftSchema{}, fmt.Errorf("%s is not a JSON object", name)
	}
	if root["type"] != "object" {
		return draftSchema{}, fmt.Errorf("%s: the payload is not of type object", name)
	}
	return draftSchema{raw: raw, root: root}, nil
}

// at is the property pointer names, and whether every object on its way
// requires it.
func (s draftSchema) at(pointer string) (map[string]any, bool, error) {
	if !strings.HasPrefix(pointer, "/") {
		return nil, false, fmt.Errorf("%q is not a JSON pointer", pointer)
	}
	node, required := s.root, true
	for _, tok := range strings.Split(pointer[1:], "/") {
		tok = strings.ReplaceAll(strings.ReplaceAll(tok, "~1", "/"), "~0", "~")
		if node["type"] != "object" {
			return nil, false, fmt.Errorf("%q passes through a value that is not an object", pointer)
		}
		props, _ := node["properties"].(map[string]any)
		next, ok := props[tok].(map[string]any)
		if !ok {
			return nil, false, fmt.Errorf("%q names no property of the schema", pointer)
		}
		req, _ := node["required"].([]any)
		required = required && slices.Contains(req, any(tok))
		node = next
	}
	return node, required, nil
}

// enum refuses a pointer to anything but a required string with an enum.
func (s draftSchema) enum(pointer string) error {
	prop, required, err := s.at(pointer)
	switch {
	case err != nil:
		return err
	case !required:
		return fmt.Errorf("%q is not required, and an absent value would read as none", pointer)
	case prop["type"] != "string":
		return fmt.Errorf("%q is not a string", pointer)
	}
	values, _ := prop["enum"].([]any)
	if len(values) == 0 {
		return fmt.Errorf("%q has no enum", pointer)
	}
	for _, v := range values {
		if _, ok := v.(string); !ok {
			return fmt.Errorf("%q has an enum value that is not a string", pointer)
		}
	}
	return nil
}
