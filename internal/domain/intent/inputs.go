package intent

import (
	"cmp"
	"fmt"
	"reflect"
	"slices"
)

// Label is how a message names an input: its flag, else its field.
func (in Input) Label() string {
	return cmp.Or(in.Flag, "inputs."+in.Name)
}

// Normalize is v as the input's type: a flag a bool, a text a string,
// a list a []string, whatever JSON decoded them as.
func Normalize(spec Input, v any) (any, error) {
	wrong := func(want string) error {
		return fmt.Errorf("%s takes %s", spec.Label(), want)
	}
	switch spec.Type {
	case "flag":
		b, ok := v.(bool)
		if !ok {
			return nil, wrong("true or false")
		}
		return b, nil
	case "text":
		s, ok := v.(string)
		if !ok {
			return nil, wrong("a text")
		}
		return s, nil
	case "list":
		switch l := v.(type) {
		case []string:
			return slices.Clone(l), nil
		case []any:
			out := make([]string, 0, len(l))
			for _, x := range l {
				s, ok := x.(string)
				if !ok {
					return nil, wrong("a list of texts")
				}
				out = append(out, s)
			}
			return out, nil
		}
		return nil, wrong("a list of texts")
	}
	return nil, wrong("a known type")
}

// NormalizeAll normalizes inputs read back from a stored request,
// keeping only the declared ones.
func (d Def) NormalizeAll(in map[string]any) (map[string]any, error) {
	out := map[string]any{}
	for _, spec := range d.Inputs {
		v, ok := in[spec.Name]
		if !ok {
			continue
		}
		n, err := Normalize(spec, v)
		if err != nil {
			return nil, err
		}
		out[spec.Name] = n
	}
	return out, nil
}

// Zero tells an input value that is unset: nil, its type's zero, or an
// empty list or map.
func Zero(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	return rv.IsZero() || ((rv.Kind() == reflect.Slice || rv.Kind() == reflect.Map) && rv.Len() == 0)
}
