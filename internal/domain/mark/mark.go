// Package mark holds what a unit's triggers already handled: per unit and
// mark name, the value of the signal a round or a workflow covered. The core
// owns the encoding and the comparison; what a signal is belongs to whoever
// declares the mark.
package mark

import (
	"errors"
	"fmt"
	"strconv"
	"time"
	"unicode"

	"github.com/wloophq/wloop/internal/domain/event"
)

// Value is a mark's canonical value and its kind: a version is replaced by
// any new value, a watermark only by a later one.
type Value struct {
	Value     string
	Watermark bool
}

// New is v's mark: a string as is (a version), a time in event.TimeLayout,
// which sorts as text, or an int in decimal (a watermark). False for a
// value that cannot be a mark: empty, zero time, or another type.
func New(v any) (Value, bool) {
	switch x := v.(type) {
	case string:
		return Value{Value: x}, x != ""
	case time.Time:
		if x.IsZero() {
			return Value{}, false
		}
		return Value{Value: x.UTC().Format(event.TimeLayout), Watermark: true}, true
	case int:
		return Value{Value: strconv.Itoa(x), Watermark: true}, true
	case int64:
		return Value{Value: strconv.FormatInt(x, 10), Watermark: true}, true
	}
	return Value{}, false
}

// Merge is what a unit keeps when v is recorded over stored: v, unless v is
// a watermark stored already covers.
func Merge(stored string, v Value) string {
	if !v.Watermark || stored == "" {
		return v.Value
	}
	if t, err := time.Parse(time.RFC3339Nano, v.Value); err == nil && Handled(stored, t) {
		return stored
	}
	if n, err := strconv.ParseInt(v.Value, 10, 64); err == nil && Handled(stored, n) {
		return stored
	}
	return v.Value
}

// Handled says whether stored covers current: a string is a version, handled
// by the same value; a time or an int is a watermark, handled up to stored.
// An empty signal is handled: there is nothing to handle.
func Handled(stored string, current any) bool {
	switch x := current.(type) {
	case string:
		return x == "" || x == stored
	case time.Time:
		if x.IsZero() {
			return true
		}
		t, err := time.Parse(time.RFC3339Nano, stored)
		return err == nil && !x.After(t)
	case int:
		return handledInt(stored, int64(x))
	case int64:
		return handledInt(stored, x)
	}
	return false
}

func handledInt(stored string, x int64) bool {
	n, err := strconv.ParseInt(stored, 10, 64)
	return err == nil && x <= n
}

// Of is the name a workflow's mark is stored under: a workflow writes only
// its own marks, and no plugin mark has a slash.
func Of(workflow, name string) string { return workflow + "/" + name }

// ValidName checks the name a plugin gives a mark, or a workflow one of its
// own before Of.
func ValidName(name string) error {
	if name == "" {
		return errors.New("a mark needs a name")
	}
	for _, r := range name {
		if r == '/' || unicode.IsSpace(r) || !unicode.IsPrint(r) {
			return fmt.Errorf("mark %q: no slash, space or control character", name)
		}
	}
	return nil
}
