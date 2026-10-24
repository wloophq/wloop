package macos

import (
	"path/filepath"
	"strings"
)

// InBundle tells whether exe is a bundle's main executable; notifications
// need the bundle's identity.
func InBundle(exe string) bool {
	dir := filepath.Dir(exe)
	return filepath.Base(dir) == "MacOS" && filepath.Base(filepath.Dir(dir)) == "Contents" &&
		strings.HasSuffix(filepath.Dir(filepath.Dir(dir)), ".app")
}
