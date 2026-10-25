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

// Encode is v's canonical value: a string as is (a version), a time in
// event.TimeLayout, which sorts as text, or an int in decimal (a
// watermark). False for a value that cannot be a mark: empty, zero time, or
// another type.
func Encode(v any) (string, bool) {
	switch x := v.(type) {
	case string:
		return x, x != ""
	case time.Time:
		if x.IsZero() {
			return "", false
		}
		return x.UTC().Format(event.TimeLayout), true
	case int:
		return strconv.Itoa(x), true
	case int64:
		return strconv.FormatInt(x, 10), true
	}
	return "", false
}

// Handled says whether stored covers current: a string is a version, handled
// by the same value; a time or an int is a watermark, handled up to stored.
// No mark handles nothing.
func Handled(stored string, current any) bool {
	if stored == "" {
		return false
	}
	switch x := current.(type) {
	case string:
		return x == stored
	case time.Time:
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
