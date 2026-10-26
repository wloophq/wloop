package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/domain/duration"
)

// UnknownKeysError names the keys of a config file that no setting reads.
type UnknownKeysError struct {
	File string
	Keys []string // paths such as "sweep.fsat" or "projects[0].agent_hom"
}

func (e *UnknownKeysError) Error() string {
	noun := "key"
	if len(e.Keys) > 1 {
		noun = "keys"
	}
	return fmt.Sprintf("%s: unknown %s %s", e.File, noun, strings.Join(e.Keys, ", "))
}

// decodeStrict decodes raw into c, refusing a key no field of Config reads
// and naming the key of a duration it cannot read.
func decodeStrict(file string, raw []byte, c *Config) error {
	var tree any
	if err := json.Unmarshal(raw, &tree); err != nil {
		return fmt.Errorf("parsing %s: %w", file, err)
	}
	var w strictWalk
	w.walk(tree, reflect.TypeFor[Config](), "")
	if len(w.unknown) > 0 {
		return &UnknownKeysError{File: file, Keys: w.unknown}
	}
	if w.bad != nil {
		return fmt.Errorf("%s: %w", file, w.bad)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(c); err != nil {
		return fmt.Errorf("parsing %s: %w", file, err)
	}
	return nil
}

var (
	unmarshalerType = reflect.TypeFor[json.Unmarshaler]()
	daysType        = reflect.TypeFor[Days]()
)

// strictWalk collects the paths that Config has no field for, sorted, and
// the first duration it cannot read.
type strictWalk struct {
	unknown []string // paths such as "sweep.fsat"
	bad     error
}

func (w *strictWalk) walk(v any, t reflect.Type, at string) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == daysType {
		if s, ok := v.(string); ok && w.bad == nil {
			if _, err := duration.Parse(s); err != nil {
				w.bad = fmt.Errorf("%s: %w", at, err)
			}
		}
		return
	}
	if reflect.PointerTo(t).Implements(unmarshalerType) {
		return
	}
	switch t.Kind() {
	case reflect.Struct:
		obj, ok := v.(map[string]any)
		if !ok {
			return
		}
		fields := jsonFields(t)
		for _, k := range slices.Sorted(maps.Keys(obj)) {
			f, ok := fields[k]
			if !ok {
				w.unknown = append(w.unknown, join(at, k))
				continue
			}
			w.walk(obj[k], f, join(at, k))
		}
	case reflect.Map:
		obj, ok := v.(map[string]any)
		if !ok {
			return
		}
		for _, k := range slices.Sorted(maps.Keys(obj)) {
			w.walk(obj[k], t.Elem(), join(at, k))
		}
	case reflect.Slice:
		list, ok := v.([]any)
		if !ok {
			return
		}
		for i, e := range list {
			w.walk(e, t.Elem(), fmt.Sprintf("%s[%d]", at, i))
		}
	}
}

func jsonFields(t reflect.Type) map[string]reflect.Type {
	out := map[string]reflect.Type{}
	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name := jsonName(f)
		switch name {
		case "-":
			continue
		case "":
			name = f.Name
		}
		out[name] = f.Type
	}
	return out
}

func join(at, k string) string {
	if at == "" {
		return k
	}
	return at + "." + k
}

// jsonName is f's key in the file, "" when its tag names none.
func jsonName(f reflect.StructField) string {
	name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
	return name
}
