package intent

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// files are the files of one intents/<name>/ or workflows/<name>/ directory:
// the bundled one, or a user one, with the user directories that override a
// bundled one file by file, later layers first.
type files struct {
	bundled fs.FS
	dir     string // slash path in bundled, else a user directory
	overlay []string
}

// read falls through to the next layer only when name is absent; an
// override that cannot be read is an error.
func (f files) read(name string) ([]byte, error) {
	for _, d := range slices.Backward(f.overlay) {
		if b, err := os.ReadFile(filepath.Join(d, name)); !errors.Is(err, fs.ErrNotExist) {
			return b, err
		}
	}
	if f.bundled != nil {
		return fs.ReadFile(f.bundled, path.Join(f.dir, name))
	}
	return os.ReadFile(filepath.Join(f.dir, name))
}

// path is where name is read from, as problems show it.
func (f files) path(name string) string {
	for _, d := range slices.Backward(f.overlay) {
		if _, err := os.Lstat(filepath.Join(d, name)); !errors.Is(err, fs.ErrNotExist) {
			return filepath.Join(d, name)
		}
	}
	if f.bundled != nil {
		return path.Join(f.dir, name)
	}
	return filepath.Join(f.dir, name)
}

// readProblem is why file was not read: missing when it does not exist.
func readProblem(file string, err error, missing string) Problem {
	if errors.Is(err, fs.ErrNotExist) {
		return Problem{File: file, Msg: missing}
	}
	if pe := (*fs.PathError)(nil); errors.As(err, &pe) {
		err = pe.Err
	}
	return Problem{File: file, Msg: "unreadable: " + err.Error()}
}

// own moves the problems an override causes in a bundled file onto the
// user's files: the manifest when the user wrote one, else the override's
// directory.
func (f files) own(problems Problems, manifest string) Problems {
	if len(f.overlay) == 0 {
		return problems
	}
	user := f.overlay[len(f.overlay)-1]
	if p := f.path(manifest); p != path.Join(f.dir, manifest) {
		user = p
	}
	inOverlay := func(file string) bool {
		return slices.ContainsFunc(f.overlay, func(d string) bool {
			return file == d || strings.HasPrefix(file, d+string(filepath.Separator))
		})
	}
	out := slices.Clone(problems)
	for i, p := range out {
		if inOverlay(p.File) {
			continue
		}
		out[i] = Problem{File: user, Msg: fmt.Sprintf("with this override, the bundled %s", Problems{p}.Error()), Snippet: p.Snippet}
	}
	return out
}

// overrides is the files the overlay holds, relative and sorted; dotfiles
// are not part of a profile.
func overrides(overlay []string) []string {
	var out []string
	for _, d := range overlay {
		_ = filepath.WalkDir(d, func(p string, e fs.DirEntry, err error) error {
			switch {
			case err != nil:
				return nil
			case p != d && strings.HasPrefix(e.Name(), ".") && e.IsDir():
				return filepath.SkipDir
			case strings.HasPrefix(e.Name(), ".") || e.IsDir():
				return nil
			}
			if rel, err := filepath.Rel(d, p); err == nil && !slices.Contains(out, filepath.ToSlash(rel)) {
				out = append(out, filepath.ToSlash(rel))
			}
			return nil
		})
	}
	slices.Sort(out)
	return out
}
