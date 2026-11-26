//go:build darwin

package macos

import (
	"testing"

	"github.com/wloophq/wloop/internal/brand"
)

// Until the first count arrives, the menu bar shows the product's name.
func TestTheTrayStartsWithTheBrandsName(t *testing.T) {
	if trayStartTitle != brand.Name {
		t.Fatalf("the tray starts as %q, want %q", trayStartTitle, brand.Name)
	}
}
