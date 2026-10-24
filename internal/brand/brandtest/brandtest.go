// Package brandtest keeps golden files valid under any brand.
package brandtest

import (
	"regexp"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/brand"
)

// DefaultName is the brand a build without -ldflags has. Outside
// internal/brand, it is neither spelled in the source nor written by a build
// with a test brand.
const DefaultName = "wloop"

// IsDefault tells whether the suite runs with the default brand.
func IsDefault() bool { return brand.Name == DefaultName }

var spells = regexp.MustCompile(`(?i)(^|[^a-z])` + DefaultName)

// Spells tells whether s spells the default brand in any case where no letter
// precedes it, as in ViewLoop.
func Spells(s string) bool { return spells.MatchString(s) }

// CanUpdate stops a golden update while Command equals Name, where Collapse
// cannot tell them apart.
func CanUpdate(t testing.TB) {
	t.Helper()
	if brand.Command == brand.Name {
		t.Fatalf(`golden updates need the test brand: go test -ldflags "$(make -s print-brand-ldflags)" -run %s -update`, t.Name())
	}
}

// Expand turns the {{command}} and {{name}} tokens of a golden file into the active brand.
func Expand(golden string) string {
	return strings.NewReplacer("{{command}}", brand.Command, "{{name}}", brand.Name).Replace(golden)
}

// Collapse turns the active brand into the tokens Expand reads, to write or compare a golden file.
func Collapse(got string) string {
	return strings.NewReplacer(brand.Command, "{{command}}", brand.Name, "{{name}}").Replace(got)
}
