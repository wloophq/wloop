// Package dossier keeps the dossiers' files, their git history and their archives under the
// state directory.
package dossier

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/core/dossiers"
	"github.com/wloophq/wloop/internal/domain/dossier"
	"github.com/wloophq/wloop/internal/meter"
)

const (
	dirMode  = 0o700
	fileMode = 0o600
	notes    = "notes.md"
)

// Subdirs are the directories every dossier holds besides its repository.
var Subdirs = []string{"sources", "drafts", "log"}

// Files is dossiers.Files over the state directory.
type Files struct {
	StateDir string
	// Home bounds the checkout walk: a .git in Home or above it does not count.
	Home    string
	MaxFile int64
	Now     func() time.Time
}

var _ dossiers.Files = Files{}

// ErrTooLarge refuses a dossier file over MaxFile.
var ErrTooLarge = errors.New("link it instead")

func (f Files) root() string { return dossier.Root(f.StateDir) }

func (f Files) now() time.Time {
	if f.Now != nil {
		return f.Now()
	}
	return time.Now()
}

// Checkout is the repository checkout holding stateDir, or "": the first of stateDir and its
// parents that holds a .git, stopping before home. The walk runs on the path as given and again
// once symlinks are resolved, so a state directory linked into a repository is found.
func Checkout(stateDir, home string) string {
	if co := checkoutAbove(stateDir, home); co != "" {
		return co
	}
	realState, err := resolve(stateDir)
	if err != nil {
		return ""
	}
	realHome := ""
	if home != "" {
		if realHome, err = resolve(home); err != nil {
			return ""
		}
	}
	return checkoutAbove(realState, realHome)
}

func checkoutAbove(stateDir, home string) string {
	home = filepath.Clean(home)
	for d := filepath.Clean(stateDir); ; {
		if home != "" && d == home {
			return ""
		}
		if _, err := os.Lstat(filepath.Join(d, ".git")); err == nil {
			return d
		}
		parent := filepath.Dir(d)
		if parent == d {
			return ""
		}
		d = parent
	}
}

// inside refuses a path that is not strictly under the dossiers root, lexically or once its
// existing part is resolved through symlinks.
func (f Files) inside(path string) error {
	if !filepath.IsAbs(f.StateDir) {
		return fmt.Errorf("the state directory %q is not absolute", f.StateDir)
	}
	if !filepath.IsAbs(path) {
		return fmt.Errorf("the dossier path %q is not absolute", path)
	}
	if !under(f.root(), filepath.Clean(path)) {
		return fmt.Errorf("%s is outside %s", path, f.root())
	}
	state, err := resolve(f.StateDir)
	if err != nil {
		return err
	}
	real, err := resolve(path)
	if err != nil {
		return err
	}
	if !under(filepath.Join(state, dossier.RootDir), real) {
		return fmt.Errorf("%s resolves to %s, outside the state directory %s", path, real, f.StateDir)
	}
	return nil
}

func under(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// resolve follows the symlinks of the longest existing prefix of path.
func resolve(path string) (string, error) {
	path = filepath.Clean(path)
	var rest []string
	for {
		real, err := filepath.EvalSymlinks(path)
		if err == nil {
			return filepath.Join(append([]string{real}, rest...)...), nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(path)
		if parent == path {
			return "", err
		}
		rest = append([]string{filepath.Base(path)}, rest...)
		path = parent
	}
}

func (f Files) Ensure(ctx context.Context, dir string) error {
	if err := f.inside(dir); err != nil {
		return err
	}
	rel, _ := filepath.Rel(f.root(), dir)
	owner, err := dossier.OwnerOf(rel)
	if err != nil {
		return err
	}
	if co := Checkout(f.StateDir, f.Home); co != "" {
		return fmt.Errorf("the state directory %s is inside the repository checkout %s; dossiers stay out of repositories", f.StateDir, co)
	}
	if err := mkdir(f.root()); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return err
	}
	for _, d := range append([]string{dir}, join(dir, Subdirs)...) {
		if err := mkdir(d); err != nil {
			return err
		}
	}
	if err := f.writeNotes(filepath.Join(dir, notes), owner); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); errors.Is(err, fs.ErrNotExist) {
		if _, err := f.git(ctx, dir, false, "init", "-q", "--shared=0600"); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	return f.Commit(ctx, dir, "open "+owner.String(), notes)
}

func mkdir(d string) error {
	if err := os.MkdirAll(d, dirMode); err != nil {
		return err
	}
	return os.Chmod(d, dirMode)
}

func join(dir string, names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = filepath.Join(dir, n)
	}
	return out
}

func (f Files) writeNotes(path string, o dossier.Owner) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fileMode)
	if errors.Is(err, fs.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(file, "---\nowner: %q\nupdated: %s\n---\n", o.String(), f.now().UTC().Format(time.RFC3339))
	return errors.Join(err, file.Close())
}

func (f Files) Usage(ctx context.Context, dir string) (int64, int, error) {
	if err := f.inside(dir); err != nil {
		return 0, 0, err
	}
	var bytes int64
	var files int
	gitDir := filepath.Join(dir, ".git")
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return ctx.Err()
		}
		info, err := d.Info()
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		bytes += info.Size()
		if p != gitDir && !strings.HasPrefix(p, gitDir+string(filepath.Separator)) {
			files++
		}
		return nil
	})
	return bytes, files, err
}

// Commit stages paths, or every change when none is named, and commits them with message.
func (f Files) Commit(ctx context.Context, dir, message string, paths ...string) error {
	changes, err := f.Changed(ctx, dir)
	if err != nil {
		return err
	}
	if len(paths) > 0 {
		changes = slices.DeleteFunc(changes, func(c dossiers.Change) bool { return !slices.Contains(paths, c.Rel) })
	}
	if len(changes) == 0 {
		return nil
	}
	var large []string
	for _, c := range changes {
		if c.Kind == "deleted" {
			continue
		}
		info, err := os.Lstat(filepath.Join(dir, c.Rel))
		if err != nil {
			return err
		}
		if f.MaxFile > 0 && info.Mode().IsRegular() && info.Size() > f.MaxFile {
			large = append(large, fmt.Sprintf("%s (%s)", c.Rel, size(info.Size())))
		}
	}
	if len(large) > 0 {
		return fmt.Errorf("%s over the %s file limit: %w", strings.Join(large, ", "), size(f.MaxFile), ErrTooLarge)
	}
	add := []string{"add", "-A", "-f", "--"}
	for _, c := range changes {
		add = append(add, c.Rel)
	}
	err = f.retryLocked(func() error {
		if _, err := f.git(ctx, dir, true, add...); err != nil {
			return err
		}
		_, err := f.git(ctx, dir, true, "commit", "-q", "--no-verify", "-m", message)
		return err
	})
	if err != nil {
		return err
	}
	return tighten(dir)
}

// retryLocked runs fn again once when another git process held the dossier's index lock.
func (f Files) retryLocked(fn func() error) error {
	err := fn()
	if err != nil && strings.Contains(err.Error(), "index.lock") {
		time.Sleep(200 * time.Millisecond)
		err = fn()
	}
	return err
}

// tighten takes the group and world bits off what dir holds: an agent writes with the default
// umask, and git honors core.sharedRepository for its objects but not for every file it writes.
func tighten(dir string) error {
	objects := filepath.Join(dir, ".git", "objects")
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == objects {
			return filepath.SkipDir
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir() && info.Mode().Perm() != dirMode:
			return os.Chmod(p, dirMode)
		case info.Mode().IsRegular() && links(info) > 1:
			return nil
		case info.Mode().IsRegular() && info.Mode().Perm()&0o077 != 0:
			return os.Chmod(p, info.Mode().Perm()&^0o077)
		}
		return nil
	})
}

func size(n int64) string {
	if n >= dossier.MB {
		return fmt.Sprintf("%.1f MB", float64(n)/dossier.MB)
	}
	return fmt.Sprintf("%.1f KB", float64(n)/dossier.KB)
}

func (f Files) Changed(ctx context.Context, dir string) ([]dossiers.Change, error) {
	if err := f.inside(dir); err != nil {
		return nil, err
	}
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	out, err := f.git(ctx, dir, true, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--ignored", "--no-renames")
	if err != nil {
		return nil, err
	}
	var changes []dossiers.Change
	for _, rec := range strings.Split(string(out), "\x00") {
		if len(rec) < 4 {
			continue
		}
		xy, rel := rec[:2], rec[3:]
		kind := "modified"
		switch {
		case xy == "??" || xy == "!!" || strings.ContainsRune(xy, 'A'):
			kind = "added"
		case strings.ContainsRune(xy, 'D'):
			kind = "deleted"
		}
		changes = append(changes, dossiers.Change{Rel: rel, Kind: kind})
	}
	return changes, nil
}

func (f Files) RestoreFile(ctx context.Context, dir, rel string) error {
	if err := f.inside(dir); err != nil {
		return err
	}
	clean := filepath.Clean(rel)
	if filepath.IsAbs(rel) || !under(dir, filepath.Join(dir, clean)) || clean == ".git" || strings.HasPrefix(clean, ".git"+string(filepath.Separator)) {
		return fmt.Errorf("%q is not a file of the dossier", rel)
	}
	if _, err := f.git(ctx, dir, true, "cat-file", "-e", "HEAD:"+filepath.ToSlash(clean)); err != nil {
		err := os.Remove(filepath.Join(dir, clean))
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	return f.retryLocked(func() error {
		_, err := f.git(ctx, dir, true, "checkout", "-q", "HEAD", "--", filepath.ToSlash(clean))
		return err
	})
}

func (f Files) Remove(ctx context.Context, path string) error {
	if err := f.inside(path); err != nil {
		return err
	}
	if path == filepath.Join(f.root(), dossier.ArchiveDir) {
		return fmt.Errorf("%s holds every archive", path)
	}
	if err := os.RemoveAll(path); err != nil {
		return err
	}
	for d := filepath.Dir(path); under(f.root(), d) && d != filepath.Join(f.root(), dossier.ArchiveDir); d = filepath.Dir(d) {
		if os.Remove(d) != nil {
			break
		}
	}
	return nil
}

// git runs git in dir with none of the user's configuration, hooks or templates, no background
// maintenance (it writes .git after git exits, while Usage walks it), and a fixed author: every
// GIT_* variable of the caller is dropped. repo pins git to dir's own repository and index, so it
// never reaches a repository above it.
func (f Files) git(ctx context.Context, dir string, repo bool, args ...string) ([]byte, error) {
	author := brand.Name
	email := brand.Name + "@localhost"
	env := slices.DeleteFunc(os.Environ(), func(kv string) bool { return strings.HasPrefix(kv, "GIT_") })
	env = append(env,
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "GIT_TEMPLATE_DIR=", "GIT_CONFIG_COUNT=0",
		"GIT_CEILING_DIRECTORIES="+filepath.Dir(dir), "GIT_TERMINAL_PROMPT=0", "GIT_LITERAL_PATHSPECS=1", "GIT_OPTIONAL_LOCKS=0",
		"GIT_AUTHOR_NAME="+author, "GIT_AUTHOR_EMAIL="+email,
		"GIT_COMMITTER_NAME="+author, "GIT_COMMITTER_EMAIL="+email,
	)
	if repo {
		if err := pinRepository(dir); err != nil {
			return nil, err
		}
		gitDir := filepath.Join(dir, ".git")
		env = append(env, "GIT_DIR="+gitDir, "GIT_WORK_TREE="+dir, "GIT_INDEX_FILE="+filepath.Join(gitDir, "index"))
	}
	full := append([]string{
		"-C", dir,
		"-c", "core.hooksPath=/dev/null", "-c", "commit.gpgsign=false",
		"-c", "user.name=" + author, "-c", "user.email=" + email,
		"-c", "init.defaultBranch=main", "-c", "core.fsmonitor=false",
		"-c", "maintenance.auto=false", "-c", "gc.auto=0",
	}, args...)
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	start := time.Now()
	err := cmd.Run()
	meter.Add(ctx, meter.Sample{Res: meter.Git, Op: args[0], Spawn: true, Err: err != nil && ctx.Err() == nil, Dur: time.Since(start)})
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return stdout.Bytes(), fmt.Errorf("git %s: %s", args[0], msg)
	}
	return stdout.Bytes(), nil
}
