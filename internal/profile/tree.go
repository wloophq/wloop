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

	dintent "github.com/wloophq/wloop/internal/domain/intent"
)

const (
	BlockMax    = 16 << 10
	JudgmentMax = 48 << 10
	IncludeMax  = 8
)

type Include struct{ Name, Text string }

type Block struct {
	Layer Layer
	// File is the block's full path, or "embedded <rel>" in the bundled layer.
	File     string
	Path     BlockPath
	FM       FrontMatter
	Text     string
	Includes []Include
}

// Rel is the block's path relative to its layer.
func (b Block) Rel() string { return write(b.Path) }

type Tree struct {
	Chain  []Layer
	Blocks []Block
	// Labels are the headings of the blocks' conditions; Dims what their
	// conditions may name. Load fills both; a tree built without them
	// reads the bundled labels and the dimensions of no brief intent.
	Labels *Labels
	Dims   []DimDef
}

func (t Tree) labels() Labels {
	if t.Labels != nil {
		return *t.Labels
	}
	return bundledLabels()
}

func (t Tree) vocab() []DimDef {
	if t.Dims != nil {
		return t.Dims
	}
	return Vocabulary(nil)
}

type SizeError struct {
	File string
	Size int
}

func (e SizeError) Error() string {
	return fmt.Sprintf("%s: %d bytes with its includes; a block is at most %d, so split it into blocks with narrower conditions", e.File, e.Size, BlockMax)
}

// layerFS confines every read to the layer's directory: a symlink that
// leads out of it fails to open.
func layerFS(l Layer) (fs.FS, func(), error) {
	if l.Embedded() {
		fsys, err := l.FSys()
		return fsys, func() {}, err
	}
	if fi, err := os.Stat(l.Dir); err != nil || !fi.IsDir() {
		return nil, nil, MissingError{Layer: l.Name, Dir: l.Dir}
	}
	root, err := os.OpenRoot(l.Dir)
	if err != nil {
		return nil, nil, err
	}
	return root.FS(), func() { root.Close() }, nil
}

func fileName(l Layer, rel string) string {
	switch {
	case l.Bundled():
		return "embedded " + rel
	case l.Embedded():
		return "embedded " + l.FileName(rel)
	}
	return l.FileName(rel)
}

// Load reads and checks every block of the chain's layers, lowest layer
// first; intents are the registry blocks are written for, loaded first.
// Blocks of a broken intent are skipped, so its manifest's problems stay its own.
func Load(chain []Layer, intents *dintent.Registry, broken []dintent.Name) (Tree, error) {
	vocab := Vocabulary(intents)
	labels, err := LoadLabels(chain, vocab)
	if err != nil {
		return Tree{}, err
	}
	t := Tree{Chain: chain, Labels: &labels, Dims: vocab}
	for _, l := range chain {
		fsys, done, err := layerFS(l)
		if err != nil {
			return Tree{}, err
		}
		err = fs.WalkDir(fsys, ".", func(rel string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if rel != "." && strings.HasPrefix(d.Name(), ".") {
					return fs.SkipDir
				}
				return nil
			}
			if ofBroken(rel, broken) {
				return nil
			}
			p, ok, err := ParsePath(rel, intents)
			if err != nil {
				var e PathError
				if !errors.As(err, &e) {
					return err
				}
				e.File = fileName(l, rel)
				return e
			}
			if !ok {
				return nil
			}
			b, err := readBlock(fsys, l, rel, p)
			if err != nil {
				return err
			}
			t.Blocks = append(t.Blocks, b)
			return nil
		})
		done()
		if err != nil {
			return Tree{}, err
		}
	}
	return t, nil
}

func ofBroken(rel string, broken []dintent.Name) bool {
	name, ok := strings.CutPrefix(path.Base(rel), "intent=")
	return ok && slices.Contains(broken, dintent.Name(strings.TrimSuffix(name, ".md")))
}

func readBlock(fsys fs.FS, l Layer, rel string, p BlockPath) (Block, error) {
	file := fileName(l, rel)
	raw, err := fs.ReadFile(fsys, rel)
	if err != nil {
		return Block{}, err
	}
	fm, text, err := splitFrontMatter(file, string(raw))
	if err != nil {
		return Block{}, err
	}
	b := Block{Layer: l, File: file, Path: p, FM: fm, Text: text}
	if len(fm.Include) > IncludeMax {
		return Block{}, FrontMatterError{File: file, Key: "include", Problem: fmt.Sprintf("lists %d files; at most %d", len(fm.Include), IncludeMax)}
	}
	size := len(text)
	bases := map[string]int{}
	for _, name := range fm.Include {
		bases[path.Base(name)]++
	}
	for _, name := range fm.Include {
		if path.IsAbs(name) || filepath.IsAbs(name) {
			return Block{}, FrontMatterError{File: file, Key: "include", Problem: name + ": must be relative to the block's file"}
		}
		at := path.Join(path.Dir(rel), name)
		inc, err := readInclude(fsys, at)
		if err != nil {
			return Block{}, FrontMatterError{File: file, Key: "include", Problem: name + ": " + err.Error()}
		}
		size += len(inc)
		heading := path.Base(name)
		if bases[heading] > 1 {
			heading = at
		}
		b.Includes = append(b.Includes, Include{Name: heading, Text: inc})
	}
	if size > BlockMax {
		return Block{}, SizeError{File: file, Size: size}
	}
	return b, nil
}

func readInclude(fsys fs.FS, rel string) (string, error) {
	if ext := path.Ext(rel); ext != ".md" && ext != ".txt" {
		return "", fmt.Errorf("only .md and .txt files can be included")
	}
	if !fs.ValidPath(rel) {
		return "", fmt.Errorf("outside the profile directory")
	}
	fi, err := fs.Lstat(fsys, rel)
	if err != nil {
		return "", fmt.Errorf("not found inside the profile directory")
	}
	if !fi.Mode().IsRegular() {
		return "", fmt.Errorf("not a regular file")
	}
	raw, err := fs.ReadFile(fsys, rel)
	if err != nil {
		return "", err
	}
	return strings.ReplaceAll(strings.TrimPrefix(string(raw), "\ufeff"), "\r\n", "\n"), nil
}
