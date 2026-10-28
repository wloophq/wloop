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
	"github.com/wloophq/wloop/internal/core/dossiers"
)

// diskChecks is doctor's disk group: the dossiers, their archives and the Claude transcripts
// of the worktrees the loop's sessions ran in, each with its count and bytes. It reads the
// store without writing it.
func (e *Env) diskChecks(ctx context.Context, cfg config.Config) []check {
	var out []check
	if co := e.stateCheckout(); co != "" {
		out = append(out, check{markMissing, "dossiers", "the state directory " + e.StateDir + " is inside the repository checkout " + co + "; dossiers are refused there",
			"set XDG_STATE_HOME to a directory outside any repository"})
	}
	if st, err := sqlite.Inspect(filepath.Join(e.StateDir, storeFile)); err == nil && (st.Version == 0 || st.Version >= taskRenamedAt) {
		out = append(out, leftoverEntryDossiers(e.StateDir)...)
	}
	p := dossierPolicy(cfg.Dossiers)
	var r dossiers.Report
	var worktrees []sqlite.AgentWorktree
	db, err := sqlite.OpenReadOnly(filepath.Join(e.StateDir, storeFile))
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return append(out, check{markInfo, "disk use", "the store is unreadable: " + err.Error(), ""})
	default:
		defer db.Close()
		s := dossiers.Service{Store: db.Dossiers(), Files: e.dossierFiles(p), Policy: p, StateDir: e.StateDir, Clock: clockFunc(e.Now)}
		if r, err = s.Report(ctx); err != nil {
			return append(out, check{markInfo, "disk use", err.Error(), ""})
		}
		if worktrees, err = db.AgentWorktrees(ctx); err != nil {
			return append(out, check{markInfo, "disk use", err.Error(), ""})
		}
	}
	r.Transcripts = e.transcripts(worktrees)
	line := func(name string, u dossiers.Usage) check {
		return check{markInfo, name, fmt.Sprintf("%d, %s", u.Count, diskSize(u.Bytes)), ""}
	}
	out = append(out, line("dossiers", r.Dossiers), line("dossier archives", r.Archives),
		line("agent transcripts of "+brand.Name+" worktrees", r.Transcripts))
	for _, o := range r.OverCap {
		out = append(out, check{markInfo, "dossier " + o.String(), "over the " + diskSize(p.SoftCap) + " soft cap", ""})
	}
	if r.OverTotal(p) {
		out = append(out, check{markInfo, "dossiers and archives", diskSize(r.Dossiers.Bytes+r.Archives.Bytes) + ", over the " + diskSize(p.TotalCap) + " total cap", ""})
	}
	return out
}

// transcripts sums the agent's transcript directories of worktrees; Count is their transcripts.
func (e *Env) transcripts(worktrees []sqlite.AgentWorktree) dossiers.Usage {
	var dirs []string
	for _, w := range worktrees {
		dirs = append(dirs, e.transcriptDir(w.Home, w.Worktree))
	}
	slices.Sort(dirs)
	var u dossiers.Usage
	for _, d := range slices.Compact(dirs) {
		_ = filepath.WalkDir(d, func(p string, ent fs.DirEntry, err error) error {
			if err != nil || ent.IsDir() {
				return nil
			}
			if info, err := ent.Info(); err == nil && info.Mode().IsRegular() {
				u.Bytes += info.Size()
				if strings.HasSuffix(p, ".jsonl") {
					u.Count++
				}
			}
			return nil
		})
	}
	return u
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
