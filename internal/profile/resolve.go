package profile

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/wloophq/wloop/profiles"
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
	// Layer is the layer the file came from; Path is where it is, a path on
	// disk for a directory layer, default/<rel> for the bundled one.
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
		r, err := readLayer(l, rel)
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
			r, err := readLayer(l, rel)
			if err != nil {
				return nil, err
			}
			out[rel] = r
		}
	}
	return out, nil
}

func readLayer(l Layer, rel string) (Resolved, error) {
	if l.Dir == "" {
		body, err := fs.ReadFile(profiles.Default, path.Join("default", rel))
		return Resolved{Layer: l, Path: path.Join("default", rel), Body: body}, err
	}
	p := filepath.Join(l.Dir, filepath.FromSlash(rel))
	body, err := os.ReadFile(p)
	return Resolved{Layer: l, Path: p, Body: body}, err
}

// listLayer lists the regular files under dir in l, dotfiles left out; a
// layer without dir has none.
func listLayer(l Layer, dir string) ([]string, error) {
	var fsys fs.FS = profiles.Default
	root := path.Join("default", dir)
	if l.Dir != "" {
		fsys, root = os.DirFS(l.Dir), dir
	}
	var out []string
	err := fs.WalkDir(fsys, root, func(p string, d fs.DirEntry, err error) error {
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
