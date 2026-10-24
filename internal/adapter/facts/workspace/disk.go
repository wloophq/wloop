package factworkspace

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// OS is Disk over the file system.
type OS struct{}

var _ Disk = OS{}

func (OS) Exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// Touched is the time of the checkout's HEAD reflog, which every commit,
// checkout and reset appends to, else of its HEAD file.
func (OS) Touched(dir string) time.Time {
	gd := gitDir(dir)
	for _, p := range []string{filepath.Join(gd, "logs", "HEAD"), filepath.Join(gd, "HEAD")} {
		if fi, err := os.Stat(p); err == nil {
			return fi.ModTime()
		}
	}
	return time.Time{}
}

func (OS) Index(dir string) time.Time {
	if fi, err := os.Stat(filepath.Join(gitDir(dir), "index")); err == nil {
		return fi.ModTime()
	}
	return time.Time{}
}

// gitDir follows a worktree's .git file to its git directory.
func gitDir(dir string) string {
	p := filepath.Join(dir, ".git")
	b, err := os.ReadFile(p)
	if err != nil {
		return p
	}
	gd, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir:")
	if !ok {
		return p
	}
	gd = strings.TrimSpace(gd)
	if !filepath.IsAbs(gd) {
		gd = filepath.Join(dir, gd)
	}
	return gd
}

// Size adds the sizes of the regular files under root, without following
// links, until ctx ends.
func (OS) Size(ctx context.Context, root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil || !d.Type().IsRegular() {
			return nil
		}
		if fi, err := d.Info(); err == nil {
			total += fi.Size()
		}
		return nil
	})
	return total, err
}
