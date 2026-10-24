package claude

import "path/filepath"

// ProjectDir is where Claude Code keeps the transcripts of the sessions run in worktree.
func ProjectDir(home, worktree string) string {
	return filepath.Join(home, "projects", projectSlug(worktree))
}
