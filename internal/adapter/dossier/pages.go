package dossier

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/wloophq/wloop/internal/core/dossiers"
)

var _ dossiers.Pages = Files{}

// at is rel's absolute path in the dossier dir: rel is relative and stays in dir lexically, and the
// directory holding it resolves inside dir; rel's own last element is never followed.
func (f Files) at(dir, rel string) (string, error) {
	if err := f.inside(dir); err != nil {
		return "", err
	}
	clean := filepath.Clean(filepath.FromSlash(rel))
	if rel == "" || filepath.IsAbs(clean) || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%q is not a path in the dossier", rel)
	}
	full := filepath.Join(dir, clean)
	realDir, err := resolve(dir)
	if err != nil {
		return "", err
	}
	parent, err := resolve(filepath.Dir(full))
	if err != nil {
		return "", err
	}
	if parent != realDir && !under(realDir, parent) {
		return "", fmt.Errorf("%s resolves outside the dossier %s", rel, dir)
	}
	return full, nil
}

func (f Files) Stat(_ context.Context, dir, rel string) (dossiers.Entry, error) {
	p, err := f.at(dir, rel)
	if err != nil {
		return dossiers.Entry{}, err
	}
	info, err := os.Lstat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return dossiers.Entry{Rel: rel, Kind: dossiers.Missing}, nil
	}
	if err != nil {
		return dossiers.Entry{}, err
	}
	return dossiers.Entry{Rel: rel, Kind: kindOf(info.Mode()), Size: info.Size(), Links: links(info)}, nil
}

func kindOf(m fs.FileMode) dossiers.EntryKind {
	switch {
	case m.IsRegular():
		return dossiers.File
	case m.IsDir():
		return dossiers.Folder
	case m&fs.ModeSymlink != 0:
		return dossiers.Symlink
	}
	return dossiers.Other
}

func (f Files) List(ctx context.Context, dir, rel string) ([]dossiers.Entry, error) {
	root, err := f.at(dir, rel)
	if err != nil {
		return nil, err
	}
	var out []dossiers.Entry
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		r, _ := filepath.Rel(dir, p)
		out = append(out, dossiers.Entry{Rel: filepath.ToSlash(r), Kind: dossiers.File, Size: info.Size()})
		return nil
	})
	slices.SortFunc(out, func(a, b dossiers.Entry) int { return strings.Compare(a.Rel, b.Rel) })
	return out, err
}

func (f Files) Read(_ context.Context, dir, rel string) ([]byte, error) {
	p, err := f.at(dir, rel)
	if err != nil {
		return nil, err
	}
	file, err := os.OpenFile(p, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if info, err := file.Stat(); err != nil {
		return nil, err
	} else if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", rel)
	}
	return io.ReadAll(file)
}

// Write replaces rel through a temporary file in its directory, so a symlink at rel is replaced, never followed.
func (f Files) Write(_ context.Context, dir, rel string, data []byte) error {
	p, err := f.at(dir, rel)
	if err != nil {
		return err
	}
	if f.MaxFile > 0 && int64(len(data)) > f.MaxFile {
		return fmt.Errorf("%s is over the %s file limit: %w", rel, size(f.MaxFile), ErrTooLarge)
	}
	if err := mkdir(filepath.Dir(p)); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".write-*")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(data)
	if err := errors.Join(werr, tmp.Chmod(fileMode), tmp.Close()); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), p); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}

func (f Files) Discard(_ context.Context, dir, rel string) error {
	p, err := f.at(dir, rel)
	if err != nil {
		return err
	}
	if p == filepath.Join(dir, ".git") {
		return fmt.Errorf("%s is the dossier's repository", rel)
	}
	return os.RemoveAll(p)
}

// Committed is rel's content in the last commit; ok false when the commit has no such file.
func (f Files) Committed(ctx context.Context, dir, rel string) ([]byte, bool, error) {
	if _, err := f.at(dir, rel); err != nil {
		return nil, false, err
	}
	spec := "HEAD:" + filepath.ToSlash(filepath.Clean(rel))
	if _, err := f.git(ctx, dir, true, "cat-file", "-e", spec); err != nil {
		return nil, false, nil
	}
	out, err := f.git(ctx, dir, true, "cat-file", "blob", spec)
	return out, err == nil, err
}

// pointers are the .git files and directories that send git to another repository's config, objects
// or worktrees; a dossier's own repository has none.
var pointers = []string{"commondir", "gitdir", "worktrees", filepath.Join("objects", "info", "alternates"), filepath.Join("objects", "info", "http-alternates")}

// pinRepository puts back the repository settings an agent could have written into dir's .git, so the
// daemon's git never runs a filter, reads an exclude or an attribute the agent chose, nor follows a
// pointer to another repository: a fixed config, an empty info/exclude, no info/attributes, no pointers.
func pinRepository(dir string) error {
	gitDir := filepath.Join(dir, ".git")
	if fi, err := os.Lstat(gitDir); err != nil || !fi.IsDir() {
		return nil
	}
	for _, d := range []string{"objects", filepath.Join("objects", "info")} {
		if fi, err := os.Lstat(filepath.Join(gitDir, d)); err == nil && !fi.IsDir() {
			return fmt.Errorf("%s/.git/%s is not a directory: the dossier's repository was tampered with", dir, d)
		}
	}
	for _, p := range pointers {
		if err := os.RemoveAll(filepath.Join(gitDir, p)); err != nil {
			return err
		}
	}
	cfg := filepath.Join(gitDir, "config")
	raw, _ := os.ReadFile(cfg)
	if err := replace(cfg, []byte(repositoryConfig(string(raw)))); err != nil {
		return err
	}
	info := filepath.Join(gitDir, "info")
	if fi, err := os.Lstat(info); err == nil && !fi.IsDir() {
		if err := os.Remove(info); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(info, dirMode); err != nil {
		return err
	}
	if err := os.RemoveAll(filepath.Join(info, "attributes")); err != nil {
		return err
	}
	return replace(filepath.Join(info, "exclude"), nil)
}

// repositoryConfig is the config init writes, keeping only the filesystem facts git detected then.
func repositoryConfig(old string) string {
	var b strings.Builder
	b.WriteString("[core]\n\trepositoryformatversion = 0\n\tfilemode = true\n\tbare = false\n\tlogallrefupdates = true\n\tsharedrepository = 0600\n")
	for _, key := range []string{"ignorecase", "precomposeunicode"} {
		for _, l := range strings.Split(old, "\n") {
			if strings.Join(strings.Fields(l), " ") == key+" = true" {
				b.WriteString("\t" + key + " = true\n")
				break
			}
		}
	}
	return b.String()
}

// replace writes path through a temporary file and a rename, so a symlink at path is replaced, never followed.
func replace(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".pin-*")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(data)
	if err := errors.Join(werr, tmp.Chmod(fileMode), tmp.Close()); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}
