package config

import (
	"bytes"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/pelletier/go-toml/v2/unstable"

	"github.com/wloophq/wloop/internal/frontmatter"
)

// Doc is a config file edited in place: what Set does not touch, comments
// and blank lines included, stays as written.
type Doc struct {
	src    []byte
	idx    *tomlIndex // of src, nil after an edit
	parses int
}

// ParseDoc is raw as a Doc; raw that is not TOML is an error.
func ParseDoc(raw []byte) (*Doc, error) {
	d := &Doc{src: slices.Clone(raw)}
	if _, err := d.index(); err != nil {
		return nil, err
	}
	return d, nil
}

// Bytes is the document as edited.
func (d *Doc) Bytes() []byte { return d.src }

// Get is the value at path, nil when the document has none.
func (d *Doc) Get(path ...string) any {
	x, err := d.index()
	if err != nil {
		return nil
	}
	return x.get(path)
}

// tomlTable is a table of the document: the root (no path) or a header.
type tomlTable struct {
	path  []string
	array bool
	start int // where its header's line, or the comments right above it, start
	end   int // where a key added to it goes: the end of the line of its last key, or of its header
	keys  int
}

// tomlKey is a key/value expression: its full path, the end of its key and
// the end of its value.
type tomlKey struct {
	path        []string
	keyEnd, end int
	table       int
}

type tomlIndex struct {
	tables []tomlTable
	keys   []tomlKey
	tree   map[string]any
}

func (d *Doc) index() (*tomlIndex, error) {
	if d.idx != nil {
		return d.idx, nil
	}
	d.parses++
	x := &tomlIndex{tables: []tomlTable{{}}}
	if err := toml.Unmarshal(d.src, &x.tree); err != nil {
		return nil, err
	}
	var p unstable.Parser
	p.Reset(d.src)
	cur := 0
	for p.NextExpression() {
		n := p.Expression()
		switch n.Kind {
		case unstable.Table, unstable.ArrayTable:
			path, end := keyPath(n)
			first := n.Key()
			first.Next()
			x.tables = append(x.tables, tomlTable{path: path, array: n.Kind == unstable.ArrayTable,
				start: d.commentsAbove(d.lineStart(int(first.Node().Raw.Offset))), end: d.lineEnd(end)})
			cur = len(x.tables) - 1
		case unstable.KeyValue:
			path, keyEnd := keyPath(n)
			end := int(n.Raw.Offset + n.Raw.Length)
			x.keys = append(x.keys, tomlKey{path: slices.Concat(x.tables[cur].path, path), keyEnd: keyEnd, end: end, table: cur})
			x.tables[cur].end = d.lineEnd(end)
			x.tables[cur].keys++
		}
	}
	if err := p.Error(); err != nil {
		return nil, err
	}
	d.idx = x
	return x, nil
}

// keyPath is n's key, as parts, and where its last part ends.
func keyPath(n *unstable.Node) ([]string, int) {
	var path []string
	end := 0
	for it := n.Key(); it.Next(); {
		k := it.Node()
		path = append(path, string(k.Data))
		end = int(k.Raw.Offset + k.Raw.Length)
	}
	return path, end
}

func (d *Doc) lineEnd(at int) int {
	if i := bytes.IndexByte(d.src[at:], '\n'); i >= 0 {
		return at + i
	}
	return len(d.src)
}

func (d *Doc) lineStart(at int) int {
	return bytes.LastIndexByte(d.src[:at], '\n') + 1
}

// commentsAbove is where the comment lines right above the line at start
// begin, with no blank line between them.
func (d *Doc) commentsAbove(start int) int {
	for start > 0 {
		prev := d.lineStart(start - 1)
		if !bytes.HasPrefix(bytes.TrimLeft(d.src[prev:start], " \t"), []byte("#")) {
			break
		}
		start = prev
	}
	return start
}

func (d *Doc) splice(from, to int, text string) {
	d.src = slices.Concat(d.src[:from], []byte(text), d.src[to:])
	d.idx = nil
}

// get is the value at path, nil when the document has none.
func (x *tomlIndex) get(path []string) any {
	var v any = x.tree
	for _, k := range path {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[k]
	}
	return v
}

// Set writes v, any TOML value but a table, at path. A value equal to the
// document's is left as written; an empty string or list is not added where
// the document has no key. An edit that would leave the document invalid
// is refused and changes nothing.
func (d *Doc) Set(path []string, v any) error {
	enc, err := frontmatter.Value(v)
	if err != nil {
		return fmt.Errorf("%s: %w", Key(path...), err)
	}
	x, err := d.index()
	if err != nil {
		return err
	}
	old := x.get(path)
	if old != nil {
		if was, err := frontmatter.Value(old); err == nil && was == enc {
			return nil
		}
	} else if empty(v) {
		return nil
	}
	before := d.src
	if err := d.edit(x, path, enc); err != nil {
		return err
	}
	if _, err := d.index(); err != nil {
		d.src, d.idx = before, nil
		return fmt.Errorf("setting %s would leave the file invalid: %w", Key(path...), err)
	}
	return nil
}

func empty(v any) bool {
	r := reflect.ValueOf(v)
	return (r.Kind() == reflect.String || r.Kind() == reflect.Slice) && r.Len() == 0
}

func (d *Doc) edit(x *tomlIndex, path []string, enc string) error {
	for _, k := range x.keys {
		switch {
		case slices.Equal(k.path, path):
			d.splice(k.keyEnd, k.end, " = "+enc)
			return nil
		case isPrefix(k.path, path):
			return fmt.Errorf("%s is written inline; set %s in it by hand", Key(k.path...), Key(path...))
		}
	}
	t, ok := x.tableFor(path)
	if !ok {
		parent := path[:len(path)-1]
		if len(d.src) > 0 && !bytes.HasSuffix(d.src, []byte("\n")) {
			d.src = append(d.src, '\n')
		}
		if len(d.src) > 0 {
			d.src = append(d.src, '\n')
		}
		d.splice(len(d.src), len(d.src), fmt.Sprintf("[%s]\n%s = %s\n", Key(parent...), Key(path[len(parent):]...), enc))
		return nil
	}
	line := Key(path[len(t.path):]...) + " = " + enc
	switch {
	case len(t.path) > 0 || t.keys > 0:
		d.splice(t.end, t.end, "\n"+line)
	case len(x.tables) > 1:
		at := x.tables[1].start
		d.splice(at, at, line+"\n\n")
	case len(d.src) == 0 || bytes.HasSuffix(d.src, []byte("\n")):
		d.splice(len(d.src), len(d.src), line+"\n")
	default:
		d.splice(len(d.src), len(d.src), "\n"+line+"\n")
	}
	return nil
}

// tableFor is the table a new key at path goes in: the longest header over
// it, or the root when path has one part or the root already spells its
// first part as a dotted key. ok is false when a new header has to hold it,
// which is also when a dotted key would extend a table a header defines.
func (x *tomlIndex) tableFor(path []string) (tomlTable, bool) {
	t, found := x.tables[0], false
	for _, h := range x.tables[1:] {
		if !h.array && isPrefix(h.path, path) && (!found || len(h.path) > len(t.path)) {
			t, found = h, true
		}
	}
	if !found && len(path) > 1 && !slices.ContainsFunc(x.keys, func(k tomlKey) bool {
		return k.table == 0 && len(k.path) > 1 && k.path[0] == path[0]
	}) {
		return tomlTable{}, false
	}
	if route := path[:len(t.path)+1]; len(route) < len(path) && slices.ContainsFunc(x.tables, func(h tomlTable) bool {
		return len(h.path) >= len(route) && slices.Equal(h.path[:len(route)], route)
	}) {
		return tomlTable{}, false
	}
	return t, true
}

// isPrefix says whether p is a proper prefix of path.
func isPrefix(p, path []string) bool {
	return len(p) < len(path) && slices.Equal(p, path[:len(p)])
}

// Key is path as a dotted TOML key, each part quoted where it needs it.
func Key(path ...string) string {
	parts := make([]string, len(path))
	for i, k := range path {
		parts[i] = frontmatter.Key(k)
	}
	return strings.Join(parts, ".")
}
