package profile

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"
)

// The profile directories whose files resolve through the layers, a file
// of a higher layer replacing the bundled file of the same path: the text
// agents read around an intent's instructions, and the prompts of the
// loop's own calls.
const (
	PartialsDir = "partials"
	PromptsDir  = "prompts"
)

// Resolved is a profile file as the layers resolve it.
type Resolved struct {
	// Layer is the layer the file came from; Path is where it is, as
	// Layer.FileName names it.
	Layer Layer
	Path  string
	Body  []byte
}

// Resolve reads rel, a slash path such as partials/record.md.tmpl, from the
// last of layers that holds it, the bundled layer reading the embedded
// profile. An error wraps fs.ErrNotExist when no layer holds it.
func Resolve(layers []Layer, rel string) (Resolved, error) {
	if !fs.ValidPath(rel) || strings.HasPrefix(path.Base(rel), ".") {
		return Resolved{}, fmt.Errorf("%q is not a profile file: %w", rel, fs.ErrInvalid)
	}
	for _, l := range slices.Backward(layers) {
		r, err := ReadFile(l, rel)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		return r, err
	}
	return Resolved{}, fmt.Errorf("no profile layer holds %s: %w", rel, fs.ErrNotExist)
}

// Files are the files under dir, a slash path such as partials, resolved
// through layers, by their path relative to the profile.
func Files(layers []Layer, dir string) (map[string]Resolved, error) {
	out := map[string]Resolved{}
	for _, l := range layers {
		names, err := listLayer(l, dir)
		if err != nil {
			return nil, err
		}
		for _, rel := range names {
			r, err := ReadFile(l, rel)
			if err != nil {
				return nil, err
			}
			out[rel] = r
		}
	}
	return out, nil
}

// ReadFile reads rel, a slash path, from l. An error wraps fs.ErrNotExist
// when l does not hold it.
func ReadFile(l Layer, rel string) (Resolved, error) {
	r := Resolved{Layer: l, Path: l.FileName(rel)}
	fsys, err := l.FSys()
	if err != nil {
		return r, err
	}
	r.Body, err = fs.ReadFile(fsys, rel)
	return r, err
}

// ReadDir lists dir, a slash path, of l; a layer without dir has no entries.
func ReadDir(l Layer, dir string) ([]fs.DirEntry, error) {
	fsys, err := l.FSys()
	if err != nil {
		return nil, err
	}
	entries, err := fs.ReadDir(fsys, dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return entries, err
}

// listLayer lists the regular files under dir in l, dotfiles left out; a
// layer without dir has none.
func listLayer(l Layer, dir string) ([]string, error) {
	fsys, err := l.FSys()
	if err != nil {
		return nil, err
	}
	root := dir
	var out []string
	err = fs.WalkDir(fsys, root, func(p string, d fs.DirEntry, err error) error {
		switch {
		case errors.Is(err, fs.ErrNotExist) && p == root:
			return fs.SkipAll
		case err != nil:
			return err
		case strings.HasPrefix(d.Name(), ".") && p != root:
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		case d.Type().IsRegular():
			rel, _ := strings.CutPrefix(p, root)
			out = append(out, path.Join(dir, strings.TrimPrefix(rel, "/")))
		}
		return nil
	})
	return out, err
}
