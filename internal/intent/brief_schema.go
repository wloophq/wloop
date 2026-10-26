package intent

import (
	"errors"
	"io/fs"

	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/jsonschema"
)

// loadExtra compiles a builder intent's schema, its extra fields, beside
// its manifest: a flat object schema in the subset the brief validates
// with, whose properties become d.Extra.
func (u *unit) loadExtra(s *sheet, d *dintent.Def) {
	if d.Builds == "" || d.Schema == "" {
		return
	}
	src, err := u.files.read(d.Schema)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			s.add("schema", "%s is not in the intent's directory", d.Schema)
		} else {
			s.add("schema", "%v", err)
		}
		return
	}
	extra, err := jsonschema.Compile(src, jsonschema.Options{Flat: true})
	if err != nil {
		s.add("schema", "%s: %v", d.Schema, err)
		return
	}
	props := extra.Properties()
	for _, p := range props {
		if !factHolds(p) {
			s.add("schema", "%s: $.%s: a %s, which no brief.extra fact holds; use a string, an integer, a boolean or an array of strings", d.Schema, p.Name, describe(p))
			return
		}
	}
	d.Extra = props
}

func factHolds(p jsonschema.Property) bool {
	switch p.Type {
	case "string", "integer", "boolean":
		return true
	case "array":
		return p.Items == "string"
	}
	return false
}

func describe(p jsonschema.Property) string {
	if p.Type == "array" {
		return "array of " + p.Items
	}
	return p.Type
}
