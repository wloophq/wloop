package config

import (
	"bytes"
	"fmt"
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/pelletier/go-toml/v2/unstable"
)

// Doc is a config file edited in place: what Set does not touch, comments
// and blank lines included, stays as written.
type Doc struct {
	src []byte
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
			x.tables = append(x.tables, tomlTable{path: path, array: n.Kind == unstable.ArrayTable, end: d.lineEnd(end)})
			cur = len(x.tables) - 1
		case unstable.KeyValue:
			path, keyEnd := keyPath(n)
			end := int(n.Raw.Offset + n.Raw.Length)
			x.keys = append(x.keys, tomlKey{path: slices.Concat(x.tables[cur].path, path), keyEnd: keyEnd, end: end, table: cur})
			x.tables[cur].end = d.lineEnd(end)
			x.tables[cur].keys++
		}
	}
	return x, p.Error()
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

func (d *Doc) splice(from, to int, text string) {
	d.src = slices.Concat(d.src[:from], []byte(text), d.src[to:])
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

// Set writes v, a string or a list of strings, at path. A value equal to
// the document's is left as written; an empty one is not added where the
// document has no key.
func (d *Doc) Set(path []string, v any) error {
	x, err := d.index()
	if err != nil {
		return err
	}
	enc, empty := tomlValue(v)
	for _, k := range x.keys {
		switch {
		case slices.Equal(k.path, path):
			if sameValue(x.get(path), v) {
				return nil
			}
			d.splice(k.keyEnd, k.end, " = "+enc)
			return nil
		case isPrefix(k.path, path) && !empty:
			return fmt.Errorf("%s is written inline; set %s in it by hand", strings.Join(k.path, "."), strings.Join(path, "."))
		}
	}
	if empty {
		return nil
	}
	t, ok := x.tableFor(path)
	if !ok {
		parent := path[:len(path)-1]
		if !bytes.HasSuffix(d.src, []byte("\n")) && len(d.src) > 0 {
			d.src = append(d.src, '\n')
		}
		if len(d.src) > 0 {
			d.src = append(d.src, '\n')
		}
		d.src = fmt.Appendf(d.src, "[%s]\n%s = %s\n", Key(parent...), Key(path[len(parent):]...), enc)
		return nil
	}
	line := Key(path[len(t.path):]...) + " = " + enc
	switch {
	case len(t.path) == 0 && t.keys == 0 && len(d.src) > 0:
		d.splice(0, 0, line+"\n\n")
	case len(t.path) == 0 && t.keys == 0:
		d.src = []byte(line + "\n")
	default:
		d.splice(t.end, t.end, "\n"+line)
	}
	return nil
}

// tableFor is the table a new key at path goes in: the longest header over
// it, or the root when path has one part or the root already spells its
// first part as a dotted key. ok is false when a new header has to hold it.
func (x *tomlIndex) tableFor(path []string) (tomlTable, bool) {
	best := -1
	for i, t := range x.tables[1:] {
		if !t.array && isPrefix(t.path, path) && (best < 0 || len(t.path) > len(x.tables[best+1].path)) {
			best = i
		}
	}
	if best >= 0 {
		return x.tables[best+1], true
	}
	if len(path) == 1 {
		return x.tables[0], true
	}
	for _, k := range x.keys {
		if k.table == 0 && len(k.path) > 1 && k.path[0] == path[0] {
			return x.tables[0], true
		}
	}
	return tomlTable{}, false
}

// isPrefix says whether p is a proper prefix of path.
func isPrefix(p, path []string) bool {
	return len(p) < len(path) && slices.Equal(p, path[:len(p)])
}

// Key is path as a dotted TOML key, each part quoted where it needs it.
func Key(path ...string) string {
	parts := make([]string, len(path))
	for i, k := range path {
		parts[i] = tomlKeyName(k)
	}
	return strings.Join(parts, ".")
}

func tomlKeyName(k string) string {
	for _, r := range k {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return tomlString(k)
		}
	}
	if k == "" {
		return `""`
	}
	return k
}

// tomlValue is v written as TOML, and whether it is empty.
func tomlValue(v any) (string, bool) {
	switch x := v.(type) {
	case string:
		return tomlString(x), x == ""
	case []string:
		parts := make([]string, len(x))
		for i, s := range x {
			parts[i] = tomlString(s)
		}
		return "[" + strings.Join(parts, ", ") + "]", len(x) == 0
	}
	panic(fmt.Sprintf("tomlValue: %T", v))
}

func sameValue(old any, v any) bool {
	switch x := v.(type) {
	case string:
		s, ok := old.(string)
		return ok && s == x
	case []string:
		list, ok := old.([]any)
		if !ok || len(list) != len(x) {
			return false
		}
		for i, e := range list {
			if s, ok := e.(string); !ok || s != x[i] {
				return false
			}
		}
		return true
	}
	return false
}

// tomlString is s as a TOML basic string.
func tomlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u%04X`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}
