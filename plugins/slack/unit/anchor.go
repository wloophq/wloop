package unit

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

const maxAnchorRef = 300

// Anchor is a plugin's opaque reference to a message or event inside a unit.
type Anchor struct {
	Unit      Unit      `json:"unit"`
	Ref       string    `json:"ref"` // slack "<conversation>/<ts>"; github "review:<id>", "comment:<id>", "commit:<oid>", "situation:<kind>"; "turn:<round>"; "row:<rev>"; "draft:<id>", "draft:<id>:<kind>" before v4
	Permalink string    `json:"permalink,omitempty"`
	At        time.Time `json:"at,omitzero"`
}

// Valid says why a is not an anchor: Ref non-blank, one line, at most 300 bytes; Unit non-zero.
func (a Anchor) Valid() error {
	switch {
	case strings.TrimSpace(a.Ref) == "":
		return errors.New("required")
	case strings.ContainsAny(a.Ref, "\r\n"):
		return errors.New("ref spans lines")
	case len(a.Ref) > maxAnchorRef:
		return fmt.Errorf("ref is %d bytes; at most %d", len(a.Ref), maxAnchorRef)
	case a.Unit.IsZero():
		return errors.New("unit is zero")
	}
	return nil
}
