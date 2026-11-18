package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/config"
)

// diskChecks is doctor's disk group: the Claude transcripts of the worktrees the loop's sessions ran in,
// with their count and bytes, and the dossiers directory an earlier release left. It reads the store
// without writing it.
func (e *Env) diskChecks(ctx context.Context, _ config.Config) []check {
	var out []check
	var worktrees []sqlite.AgentWorktree
	db, err := sqlite.OpenReadOnly(filepath.Join(e.StateDir, storeFile))
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return append(out, check{markInfo, "disk use", "the store is unreadable: " + err.Error(), ""})
	default:
		defer db.Close()
		if worktrees, err = db.AgentWorktrees(ctx); err != nil {
			return append(out, check{markInfo, "disk use", err.Error(), ""})
		}
	}
	files, bytes := e.transcripts(worktrees)
	out = append(out, check{markInfo, "agent transcripts of " + brand.Name + " worktrees", fmt.Sprintf("%d, %s", files, diskSize(bytes)), ""})
	if dir := filepath.Join(e.StateDir, legacyDossiersDir); pathExists(dir) {
		out = append(out, check{markInfo, "dossiers", dir + " is no longer used; remove it by hand once you no longer need its notes", ""})
	}
	return out
}

// legacyDossiersDir is where releases before the turn records' learned entries kept the task dossiers.
const legacyDossiersDir = "dossiers"

// transcripts sums the agent's transcript directories of worktrees: their transcripts and their bytes.
func (e *Env) transcripts(worktrees []sqlite.AgentWorktree) (files int, bytes int64) {
	var dirs []string
	for _, w := range worktrees {
		dirs = append(dirs, e.transcriptDir(w.Home, w.Worktree))
	}
	slices.Sort(dirs)
	for _, d := range slices.Compact(dirs) {
		_ = filepath.WalkDir(d, func(p string, ent fs.DirEntry, err error) error {
			if err != nil || ent.IsDir() {
				return nil
			}
			if info, err := ent.Info(); err == nil && info.Mode().IsRegular() {
				bytes += info.Size()
				if strings.HasSuffix(p, ".jsonl") {
					files++
				}
			}
			return nil
		})
	}
	return files, bytes
}

func diskSize(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}
