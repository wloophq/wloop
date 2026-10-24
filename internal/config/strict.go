package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
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

// decodeStrict decodes raw into c, refusing a key no field of Config reads.
func decodeStrict(file string, raw []byte, c *Config) error {
	var tree any
	if err := json.Unmarshal(raw, &tree); err != nil {
		return fmt.Errorf("parsing %s: %w", file, err)
	}
	if keys := unknownKeys(tree, reflect.TypeFor[Config](), ""); len(keys) > 0 {
		return &UnknownKeysError{File: file, Keys: keys}
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
	proposalsType   = reflect.TypeFor[Proposals]()
)

// unknownKeys lists the paths under v that t has no field for, sorted.
func unknownKeys(v any, t reflect.Type, at string) []string {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch {
	case t == proposalsType:
		return proposalKeys(v, at)
	case reflect.PointerTo(t).Implements(unmarshalerType):
		return nil
	}
	switch t.Kind() {
	case reflect.Struct:
		obj, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		fields := jsonFields(t)
		var out []string
		for _, k := range slices.Sorted(maps.Keys(obj)) {
			f, ok := fields[k]
			if !ok {
				out = append(out, join(at, k))
				continue
			}
			out = append(out, unknownKeys(obj[k], f, join(at, k))...)
		}
		return out
	case reflect.Map:
		obj, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		var out []string
		for _, k := range slices.Sorted(maps.Keys(obj)) {
			out = append(out, unknownKeys(obj[k], t.Elem(), join(at, k))...)
		}
		return out
	case reflect.Slice:
		list, ok := v.([]any)
		if !ok {
			return nil
		}
		var out []string
		for i, e := range list {
			out = append(out, unknownKeys(e, t.Elem(), fmt.Sprintf("%s[%d]", at, i))...)
		}
		return out
	}
	return nil
}

func proposalKeys(v any, at string) []string {
	obj, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	var out []string
	for _, k := range slices.Sorted(maps.Keys(obj)) {
		if k != renudgeAfterKey {
			out = append(out, unknownKeys(obj[k], reflect.TypeFor[ProposalRule](), join(at, k))...)
		}
	}
	return out
}

func jsonFields(t reflect.Type) map[string]reflect.Type {
	out := map[string]reflect.Type{}
	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
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
