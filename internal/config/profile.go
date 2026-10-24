package config

import (
	"path/filepath"
	"strings"
)

// ProfilesDir holds the review profiles named by a bare name.
func ProfilesDir() string { return filepath.Join(Dir(), "profiles") }

// ResolveProfile turns a profile setting into a directory: a bare name lives
// under ProfilesDir, anything else is a path.
func ResolveProfile(v string) string {
	if v == "" {
		return ""
	}
	if !strings.ContainsRune(v, '/') && !strings.HasPrefix(v, "~") && !strings.HasPrefix(v, ".") {
		return filepath.Join(ProfilesDir(), v)
	}
	return expandConfigPath(v)
}
