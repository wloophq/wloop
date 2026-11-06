package config

import (
	"bytes"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

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
	line  int // where its header's line starts
	key   int // where its header's key ends
	end   int // where a key added to it goes: the end of the line of its last key, or of its header
	keys  int
}

// tomlKey is a key/value expression: its full path, where its key starts
// and ends, and the end of its value.
type tomlKey struct {
	path               []string
	start, keyEnd, end int
	table              int
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
			line := d.lineStart(int(first.Node().Raw.Offset))
			x.tables = append(x.tables, tomlTable{path: path, array: n.Kind == unstable.ArrayTable,
				start: d.commentsAbove(line), line: line, key: end, end: d.lineEnd(end)})
			cur = len(x.tables) - 1
		case unstable.KeyValue:
			path, keyEnd := keyPath(n)
			end := int(n.Raw.Offset + n.Raw.Length)
			first := n.Key()
			first.Next()
			x.keys = append(x.keys, tomlKey{path: slices.Concat(x.tables[cur].path, path),
				start: int(first.Node().Raw.Offset), keyEnd: keyEnd, end: end, table: cur})
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
func (d *Doc) Set(path []string, v any) error { return d.set(path, v, false) }

// Put is Set that also adds an empty string or list.
func (d *Doc) Put(path []string, v any) error { return d.set(path, v, true) }

func (d *Doc) set(path []string, v any, keepEmpty bool) error {
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
	} else if !keepEmpty && empty(v) {
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
	if err := x.outsideArrays(path); err != nil {
		return err
	}
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

// outsideArrays refuses path when it names an array of tables or a key in
// one, which a dotted key cannot pick.
func (x *tomlIndex) outsideArrays(path []string) error {
	for _, h := range x.tables[1:] {
		if h.array && (isPrefix(h.path, path) || slices.Equal(h.path, path)) {
			return fmt.Errorf("%s is in the array of tables %s; edit it by hand", Key(path...), Key(h.path...))
		}
	}
	return nil
}

// Unset removes the key at path with the rest of its line, or the table at
// path with its keys, the comments right above it and its sub-tables; a
// document without it is left as written. A table the removal leaves with no
// key, comment or sub-table goes too. A key written in an inline table or in
// an array of tables is refused and changes nothing.
func (d *Doc) Unset(path []string) error {
	x, err := d.index()
	if err != nil {
		return err
	}
	for _, h := range x.tables[1:] {
		if h.array && isPrefix(h.path, path) {
			return fmt.Errorf("%s is in the array of tables %s; edit it by hand", Key(path...), Key(h.path...))
		}
	}
	under := func(p []string) bool { return slices.Equal(p, path) || isPrefix(path, p) }
	var cuts []cut
	gone := make([]bool, len(x.tables))
	for i, h := range x.tables[1:] {
		if under(h.path) {
			gone[i+1] = true
			cuts = append(cuts, cut{x.tables[i+1].start, x.next(i+1, len(d.src)), true})
		}
	}
	lost := make([]int, len(x.tables))
	var keyCuts []cut
	for _, k := range x.keys {
		switch {
		case gone[k.table]:
		case under(k.path):
			keyCuts = append(keyCuts, d.keyLines(k))
			lost[k.table]++
		case isPrefix(k.path, path):
			return fmt.Errorf("%s is written inline; unset %s in it by hand", Key(k.path...), Key(path...))
		}
	}
	cuts = append(cuts, keyCuts...)
	for i := 1; i < len(x.tables); i++ {
		if !gone[i] && lost[i] > 0 && lost[i] == x.tables[i].keys && d.bare(x, i, gone, keyCuts) {
			cuts = append(cuts, cut{x.tables[i].start, x.next(i, len(d.src)), true})
		}
	}
	if len(cuts) == 0 {
		return nil
	}
	before := d.src
	d.remove(cuts)
	if _, err := d.index(); err != nil {
		d.src, d.idx = before, nil
		return fmt.Errorf("unsetting %s would leave the file invalid: %w", Key(path...), err)
	}
	return nil
}

// cut is a span of the document Unset removes; a table's also takes the
// blank lines above it when nothing follows it.
type cut struct {
	from, to int
	table    bool
}

// next is where the table after table i starts, or n after the last.
func (x *tomlIndex) next(i, n int) int {
	if i+1 < len(x.tables) {
		return x.tables[i+1].start
	}
	return n
}

// keyLines is the span of k's lines, the last newline included.
func (d *Doc) keyLines(k tomlKey) cut {
	to := d.lineEnd(k.end)
	if to < len(d.src) {
		to++
	}
	return cut{from: d.lineStart(k.start), to: to}
}

// bare says whether table i is only its header once keyCuts are removed: no
// comment above, beside or below it, and no sub-table left.
func (d *Doc) bare(x *tomlIndex, i int, gone []bool, keyCuts []cut) bool {
	t := x.tables[i]
	if t.start != t.line || len(bytes.Trim(d.src[t.key:d.lineEnd(t.key)], " \t]")) > 0 {
		return false
	}
	for j, h := range x.tables {
		if !gone[j] && isPrefix(t.path, h.path) {
			return false
		}
	}
	at := d.lineEnd(t.key)
	for _, c := range keyCuts {
		if c.from >= at && c.to <= x.next(i, len(d.src)) {
			if len(bytes.TrimSpace(d.src[at:c.from])) > 0 {
				return false
			}
			at = c.to
		}
	}
	return len(bytes.TrimSpace(d.src[at:x.next(i, len(d.src))])) == 0
}

func (d *Doc) remove(cuts []cut) {
	slices.SortFunc(cuts, func(a, b cut) int { return a.from - b.from })
	var merged []cut
	for _, c := range cuts {
		if n := len(merged); n > 0 && c.from <= merged[n-1].to {
			merged[n-1].to = max(merged[n-1].to, c.to)
			merged[n-1].table = merged[n-1].table || c.table
			continue
		}
		merged = append(merged, c)
	}
	if last := &merged[len(merged)-1]; last.table && last.to == len(d.src) {
		floor := 0
		if len(merged) > 1 {
			floor = merged[len(merged)-2].to
		}
		for last.from > floor && len(bytes.TrimSpace(d.src[d.lineStart(last.from-1):last.from])) == 0 {
			last.from = d.lineStart(last.from - 1)
		}
	}
	var out []byte
	at := 0
	for _, c := range merged {
		out = append(out, d.src[at:c.from]...)
		at = c.to
	}
	d.src, d.idx = append(out, d.src[at:]...), nil
}

// ParseKey is s, a dotted TOML key such as workflows."a.b".enabled, as
// its parts.
func ParseKey(s string) ([]string, error) {
	if strings.ContainsAny(s, "\n\r") {
		return nil, fmt.Errorf("invalid key %q", s)
	}
	src := []byte(s + " = 0")
	var p unstable.Parser
	p.Reset(src)
	var path []string
	for n := 0; p.NextExpression(); n++ {
		e := p.Expression()
		if n > 0 || e.Kind != unstable.KeyValue {
			return nil, fmt.Errorf("invalid key %q", s)
		}
		path, _ = keyPath(e)
	}
	if err := p.Error(); err != nil || len(path) == 0 {
		return nil, fmt.Errorf("invalid key %q", s)
	}
	return path, nil
}

// ParseValue is s as a TOML value, or s itself when it is not one or is a
// date or a time, which the config only reads as strings.
func ParseValue(s string) any {
	var doc map[string]any
	if err := toml.Unmarshal([]byte("v = "+s), &doc); err != nil || len(doc) != 1 {
		return s
	}
	switch v := doc["v"].(type) {
	case toml.LocalDate, toml.LocalDateTime, toml.LocalTime, time.Time:
		return s
	default:
		return v
	}
}
