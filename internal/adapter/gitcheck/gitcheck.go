// Package gitcheck reads what removing a checkout would lose, through git
// and the file system.
package gitcheck

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/core/workspaces"
	"github.com/wloophq/wloop/internal/gitx"
)

// Git is the workspaces' Remote and Tree over git.
type Git struct{ G gitx.Git }

var (
	_ workspaces.Remote = Git{}
	_ workspaces.Tree   = Git{}
	_ workspaces.Files  = Files{}
)

func (g Git) run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	return g.G.R.Run(ctx, dir, nil, args...)
}

func (g Git) Tracking(ctx context.Context, dir string) (remote, branch string, err error) {
	if out, err := g.run(ctx, dir, "rev-parse", "--symbolic-full-name", "@{upstream}"); err == nil {
		if parts := strings.SplitN(strings.TrimSpace(string(out)), "/", 4); len(parts) == 4 && parts[1] == "remotes" {
			return parts[2], parts[3], nil
		}
	}
	// @{upstream} no longer resolves once FetchTracking dropped the tracking
	// ref of a branch its remote deleted; the branch's config still names it.
	head, err := g.run(ctx, dir, "symbolic-ref", "-q", "HEAD")
	ref := strings.TrimSpace(string(head))
	if err != nil || ref == "" {
		return "origin", "", nil
	}
	out, err := g.run(ctx, dir, "for-each-ref", "--format=%(upstream:remotename)%00%(upstream)", ref)
	if err != nil {
		return "", "", err
	}
	name, upstream, _ := strings.Cut(strings.TrimSpace(string(out)), "\x00")
	if b, ok := strings.CutPrefix(upstream, "refs/remotes/"+name+"/"); ok && name != "" {
		return name, b, nil
	}
	return "origin", "", nil
}

func (g Git) FetchTracking(ctx context.Context, dir string, number int) error {
	remote, branch, err := g.Tracking(ctx, dir)
	if err != nil {
		return err
	}
	if _, err := g.run(ctx, dir, "fetch", "-q", remote); err != nil {
		return err
	}
	if number > 0 {
		// A pull request head that cannot be fetched holds nothing, which
		// PRHolds then reports.
		_ = g.G.FetchPR(ctx, dir, number)
	}
	if branch == "" {
		return nil
	}
	ref := "refs/heads/" + branch
	out, err := g.run(ctx, dir, "ls-remote", "--heads", remote, ref)
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(out), "\n") {
		if _, name, ok := strings.Cut(line, "\t"); ok && name == ref {
			return nil
		}
	}
	_, err = g.run(ctx, dir, "update-ref", "-d", "refs/remotes/"+remote+"/"+branch)
	return err
}

// RemoteHasBranch reads the remote-tracking ref, which FetchTracking drops
// when the remote deleted the branch.
func (g Git) RemoteHasBranch(ctx context.Context, dir, remote, branch string) (bool, error) {
	_, err := g.run(ctx, dir, "show-ref", "--verify", "--quiet", "refs/remotes/"+remote+"/"+branch)
	var gitErr *gitx.Error
	if errors.As(err, &gitErr) && gitErr.Code == 1 {
		return false, nil
	}
	return err == nil, err
}

func (g Git) Unpushed(ctx context.Context, dir string) (int, error) { return g.G.Unpushed(ctx, dir) }

func (g Git) PRHolds(ctx context.Context, dir, prHead string) (bool, error) {
	if prHead == "" {
		return false, nil
	}
	head, err := g.G.Head(ctx, dir)
	if err != nil || head == prHead {
		return err == nil, err
	}
	return g.G.IsAncestor(ctx, dir, head, prHead)
}

func (g Git) Changed(ctx context.Context, dir string) (int, error) { return g.G.Changed(ctx, dir) }

func (g Git) DirtyTracked(ctx context.Context, dir string) (int, error) {
	out, err := g.run(ctx, dir, "status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return 0, err
	}
	lines := strings.TrimRight(string(out), "\n")
	if lines == "" {
		return 0, nil
	}
	return strings.Count(lines, "\n") + 1, nil
}

func (g Git) Ignored(ctx context.Context, dir string) (untracked, ignored []string, err error) {
	out, err := g.run(ctx, dir, "status", "--porcelain", "-z", "--ignored=matching", "--untracked-files=all")
	if err != nil {
		return nil, nil, err
	}
	for _, entry := range strings.Split(string(out), "\x00") {
		if p, ok := strings.CutPrefix(entry, "?? "); ok {
			untracked = append(untracked, p)
		} else if p, ok := strings.CutPrefix(entry, "!! "); ok {
			ignored = append(ignored, p)
		}
	}
	return untracked, ignored, nil
}

func (g Git) Head(ctx context.Context, dir string) (string, error) { return g.G.Head(ctx, dir) }

func (g Git) Kept(ctx context.Context, dir string) (string, error) {
	remotes, err := g.run(ctx, dir, "remote")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(string(remotes)) == "" {
		return "no remote", nil
	}
	out, err := g.run(ctx, dir, "rev-list", "--count", "--all", "--not", "--remotes")
	if err != nil {
		return "", err
	}
	if n := strings.TrimSpace(string(out)); n != "0" {
		return n + " commits on no remote", nil
	}
	if _, err := g.run(ctx, dir, "rev-parse", "--verify", "--quiet", "refs/stash"); err == nil {
		return "a stash", nil
	}
	return "", nil
}

// Files reads the file system.
type Files struct{}

func (Files) Exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func (Files) Link(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode()&fs.ModeSymlink != 0
}

func (Files) Repos(dir string) ([]string, error) {
	var out []string
	err := repos(dir, ".", &out)
	return out, err
}

// repos adds to out the repositories at or below dir, rel to the root it
// started from, reading each directory once.
func repos(dir, rel string, out *[]string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	entries, err := f.ReadDir(-1)
	f.Close()
	if err != nil {
		return err
	}
	if slices.ContainsFunc(entries, func(e fs.DirEntry) bool { return e.Name() == ".git" }) {
		*out = append(*out, rel)
		return nil
	}
	for _, e := range entries {
		if e.IsDir() {
			if err := repos(filepath.Join(dir, e.Name()), path.Join(rel, e.Name()), out); err != nil {
				return err
			}
		}
	}
	return nil
}

// Same compares regular files by content, links by target and directories
// entry by entry; any other kind, or a missing side, is not the same.
func (f Files) Same(a, b string) (bool, error) {
	a, b = filepath.Clean(a), filepath.Clean(b)
	ia, err := os.Lstat(a)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	ib, err := os.Lstat(b)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if ia.Mode().Type() != ib.Mode().Type() {
		return false, nil
	}
	switch {
	case ia.Mode().IsRegular():
		if ia.Size() != ib.Size() {
			return false, nil
		}
		return sameContent(a, b)
	case ia.Mode()&fs.ModeSymlink != 0:
		ta, err := os.Readlink(a)
		if err != nil {
			return false, err
		}
		tb, err := os.Readlink(b)
		return ta == tb, err
	case ia.IsDir():
		return f.sameDir(a, b)
	}
	return false, nil
}

func (f Files) sameDir(a, b string) (bool, error) {
	ea, err := os.ReadDir(a)
	if err != nil {
		return false, err
	}
	eb, err := os.ReadDir(b)
	if err != nil {
		return false, err
	}
	if len(ea) != len(eb) {
		return false, nil
	}
	for i, e := range ea {
		if e.Name() != eb[i].Name() {
			return false, nil
		}
		same, err := f.Same(filepath.Join(a, e.Name()), filepath.Join(b, e.Name()))
		if err != nil || !same {
			return false, err
		}
	}
	return true, nil
}

func sameContent(a, b string) (bool, error) {
	fa, err := os.Open(a)
	if err != nil {
		return false, err
	}
	defer fa.Close()
	fb, err := os.Open(b)
	if err != nil {
		return false, err
	}
	defer fb.Close()
	bufA, bufB := make([]byte, 64<<10), make([]byte, 64<<10)
	for {
		na, errA := io.ReadFull(fa, bufA)
		nb, errB := io.ReadFull(fb, bufB)
		if na != nb || !bytes.Equal(bufA[:na], bufB[:nb]) {
			return false, nil
		}
		if done(errA) && done(errB) {
			return true, nil
		}
		if errA != nil && !done(errA) {
			return false, errA
		}
		if errB != nil && !done(errB) {
			return false, errB
		}
	}
}

func done(err error) bool { return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) }
