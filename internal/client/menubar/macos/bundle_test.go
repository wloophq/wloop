package macos

import (
	"testing"

	"github.com/wloophq/wloop/internal/brand"
)

func TestInBundle(t *testing.T) {
	cases := map[string]bool{
		"/Users/me/Applications/" + brand.App + ".app/Contents/MacOS/" + brand.Name: true,
		"/opt/homebrew/bin/" + brand.Name:                                           false,
		"/Users/me/" + brand.App + ".app/" + brand.Name:                             false,
		"/x/" + brand.App + ".app/Contents/MacOS/sub/" + brand.Name:                 false,
	}
	for exe, want := range cases {
		if got := InBundle(exe); got != want {
			t.Errorf("InBundle(%q) = %v, want %v", exe, got, want)
		}
	}
}
