//go:build darwin

package locale

import (
	"errors"
	"testing"
)

func TestAMissingSymbolBindsNothing(t *testing.T) {
	var fn func() uintptr
	lookup := func(uintptr, string) (uintptr, error) { return 0, errors.New("symbol not found") }
	if bind(0, map[string]any{"CFNope": &fn}, lookup) || fn != nil {
		t.Fatal("a missing symbol bound")
	}
}
