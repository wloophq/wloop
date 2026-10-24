package intent

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
)

// files are the files of one intents/<name>/ or workflows/<name>/ directory:
// the bundled one, or a user one, with the user directories that override a
// bundled one file by file, later layers first.
type files struct {
	bundled fs.FS
	dir     string // slash path in bundled, else a user directory
	overlay []string
}

func (f files) read(name string) ([]byte, error) {
	for _, d := range slices.Backward(f.overlay) {
		if b, err := os.ReadFile(filepath.Join(d, name)); err == nil {
			return b, nil
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
		if _, err := os.Stat(filepath.Join(d, name)); err == nil {
			return filepath.Join(d, name)
		}
	}
	if f.bundled != nil {
		return path.Join(f.dir, name)
	}
	return filepath.Join(f.dir, name)
}

// overrides is the files the overlay holds, relative and sorted.
func overrides(overlay []string) []string {
	var out []string
	for _, d := range overlay {
		_ = filepath.WalkDir(d, func(p string, e fs.DirEntry, err error) error {
			if err != nil || e.IsDir() {
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
