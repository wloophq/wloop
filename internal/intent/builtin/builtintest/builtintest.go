// Package builtintest swaps the builtin tables for a test.
package builtintest

import (
	"testing"

	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/pkg/plugin/manifest"
)

// Use installs v for the rest of t and restores the tables after. A test
// that calls it must not run in parallel with one that reads the tables.
func Use(t testing.TB, v *builtin.Vocabulary) {
	t.Helper()
	was := builtin.Installed()
	builtin.Install(v)
	t.Cleanup(func() { builtin.Install(was) })
}

// Merge installs the vocabulary of srcs for the rest of t, as Use does; a
// source that does not merge fails t.
func Merge(t testing.TB, srcs ...*manifest.Source) {
	t.Helper()
	v, err := builtin.Merge(srcs...)
	if err != nil {
		t.Fatal(err)
	}
	Use(t, v)
}
