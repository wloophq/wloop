package claudedesktop

import (
	"path/filepath"
	"strings"
)

const (
	// HandleVar holds a Desktop process's session handle, local_<uuid>.
	HandleVar     = "CLAUDE_CODE_HOST_SESSION_ID"
	EntrypointVar = "CLAUDE_CODE_ENTRYPOINT"
)

// SessionEnv reads the identity Desktop gives a session's process. Desktop
// also runs sessions the loop does not drive, so it claims only the
// worktrees under Root.
type SessionEnv struct{ Root string }

func (SessionEnv) Handle(getenv func(string) string) string {
	if getenv(EntrypointVar) != entrypoint {
		return ""
	}
	return getenv(HandleVar)
}

func (s SessionEnv) Pane(getenv func(string) string) string { return s.Handle(getenv) }

func (SessionEnv) WorktreeID(func(string) string) string { return "" }

func (s SessionEnv) Claims(cwd string) bool {
	if s.Root == "" || cwd == "" {
		return false
	}
	for _, root := range roots(s.Root) {
		for _, dir := range roots(cwd) {
			if rel, err := filepath.Rel(root, dir); err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return true
			}
		}
	}
	return false
}

// roots is path as given and with its symlinks resolved.
func roots(path string) []string {
	out := []string{filepath.Clean(path)}
	if real, err := canonical(path); err == nil && real != out[0] {
		out = append(out, real)
	}
	return out
}
