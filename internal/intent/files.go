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

	"github.com/wloophq/wloop/internal/profile"
)

// files are the files of one intents/<name>/ or workflows/<name>/ directory:
// the bundled one, or a user one, with the user directories that override a
// bundled one file by file, later layers first.
type files struct {
	bundled fs.FS
	dir     string // slash path in bundled, else a user directory
	// shown is where dir is as problems name it, when bundled is a
	// plugin's layer.
	shown   string
	overlay []string
	// rel is dir relative to the profile, such as workflows/answer-unread, which r localizes by.
	rel string
	r   layerReader
	// layer is the layer of dir; layers are those of overlay, one each.
	layer  profile.Layer
	layers []profile.Layer
}

// top is the highest layer whose files f reads: its last overlay's, else its own.
func (f files) top() profile.Layer {
	if n := len(f.overlay); n > 0 && n <= len(f.layers) {
		return f.layers[n-1]
	}
	return f.layer
}

// overlaid is f with dir, a directory of layer l, over its other files.
func (f *files) overlaid(l profile.Layer, dir string) {
	f.overlay = append(f.overlay, dir)
	f.layers = append(f.layers, l)
}

// layerFiles are the files of dir, a slash path in l, a layer other than
// the bundled one, and where they are as problems name them.
func layerFiles(l profile.Layer, dir string, r layerReader) (files, string) {
	shown := l.FileName(dir)
	if l.FS == nil {
		return files{dir: shown, rel: dir, r: r, layer: l}, shown
	}
	return files{bundled: l.FS, dir: dir, shown: shown, rel: dir, r: r, layer: l}, shown
}

// read is name with its texts in the reader's locale: a whole-file text resolved across the layers, any
// other file as raw reads it.
func (f files) read(name string) ([]byte, error) {
	rel := path.Join(f.rel, name)
	if res, ok, err := f.r.file(f.top(), rel); ok {
		return res.Body, err
	}
	b, err := f.raw(name)
	if err != nil {
		return b, err
	}
	return f.r.localize(f.top(), rel, b), nil
}

// raw falls through to the next layer only when name is absent; an
// override that cannot be read is an error.
func (f files) raw(name string) ([]byte, error) {
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
	return f.base(name)
}

// base is where name is read from without the overlay.
func (f files) base(name string) string {
	switch {
	case f.shown != "":
		return path.Join(f.shown, name)
	case f.bundled != nil:
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
	if p := f.path(manifest); p != f.base(manifest) {
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
