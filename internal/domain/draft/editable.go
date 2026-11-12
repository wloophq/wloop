package draft

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

// EditableText is the entry of a KindSpec's Editable that lets a person change a draft's text.
const EditableText = "text"

// ErrNotEditable is wrapped when a person's edit changes what a draft's kind does not let them.
var ErrNotEditable = errors.New("not a person's to change")

// CheckEdit refuses after, a person's edit of before, when it changes the text and k's Editable lacks
// EditableText, or a payload value at a JSON pointer none of k's Editable pointers covers.
func (k KindSpec) CheckEdit(before, after Draft) error {
	if after.Text != before.Text && !slices.Contains(k.Editable, EditableText) {
		return fmt.Errorf("%w: a %s draft's text", ErrNotEditable, before.Kind)
	}
	if string(after.Payload) == string(before.Payload) {
		return nil
	}
	var was, is any
	if len(before.Payload) > 0 {
		if err := json.Unmarshal(before.Payload, &was); err != nil {
			return fmt.Errorf("%w: draft %d's payload does not decode: %w", ErrNotEditable, before.ID, err)
		}
	}
	if err := json.Unmarshal(after.Payload, &is); err != nil {
		return fmt.Errorf("%w: the payload is no JSON: %w", ErrNotEditable, err)
	}
	if _, object := is.(map[string]any); object && was == nil {
		was = map[string]any{}
	}
	for _, p := range changedPointers("", was, is) {
		if !slices.ContainsFunc(k.Editable, func(e string) bool { return e != EditableText && (p == e || strings.HasPrefix(p, e+"/")) }) {
			return fmt.Errorf("%w: %q of a %s draft", ErrNotEditable, p, before.Kind)
		}
	}
	return nil
}

func changedPointers(at string, a, b any) []string {
	switch x := a.(type) {
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok {
			break
		}
		var out []string
		for k := range x {
			out = append(out, changedPointers(at+"/"+escapePointer(k), x[k], y[k])...)
		}
		for k := range y {
			if _, seen := x[k]; !seen {
				out = append(out, at+"/"+escapePointer(k))
			}
		}
		return out
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			break
		}
		var out []string
		for i := range x {
			out = append(out, changedPointers(at+"/"+strconv.Itoa(i), x[i], y[i])...)
		}
		return out
	}
	if reflect.DeepEqual(a, b) {
		return nil
	}
	return []string{at}
}

func escapePointer(k string) string {
	return strings.ReplaceAll(strings.ReplaceAll(k, "~", "~0"), "/", "~1")
}
