package frontmatter

import (
	"strconv"

	"github.com/pelletier/go-toml/v2/unstable"
)

// Position is a 1-based line and column in a file.
type Position struct{ Line, Col int }

// Positions maps each key path of a TOML document to where its value starts,
// with lines offset by firstLine-1. Paths are dotted keys; array elements and
// array-table entries carry their index ("paths[1]", "headline.new[0].when").
// A table or an array maps to its key. It returns what it read before a syntax error.
func Positions(src []byte, firstLine int) map[string]Position {
	x := indexer{at: map[string]Position{}, entries: map[string]int{}, offset: firstLine - 1}
	x.p.Reset(src)
	prefix := ""
	for x.p.NextExpression() {
		n := x.p.Expression()
		switch n.Kind {
		case unstable.Table, unstable.ArrayTable:
			prefix = x.header(n)
		case unstable.KeyValue:
			x.keyValue(prefix, n)
		}
	}
	return x.at
}

type indexer struct {
	p       unstable.Parser
	at      map[string]Position
	entries map[string]int // array-table path -> entries seen
	offset  int
}

func (x *indexer) pos(r unstable.Range) Position {
	s := x.p.Shape(r).Start
	return Position{Line: s.Line + x.offset, Col: s.Column}
}

func (x *indexer) set(path string, at Position) {
	if _, ok := x.at[path]; !ok {
		x.at[path] = at
	}
}

func (x *indexer) resolve(path string) string {
	if n, ok := x.entries[path]; ok {
		return path + "[" + strconv.Itoa(n-1) + "]"
	}
	return path
}

func (x *indexer) header(n *unstable.Node) string {
	path := ""
	var at Position
	for it := n.Key(); it.Next(); {
		k := it.Node()
		if path == "" {
			at = x.pos(k.Raw)
		}
		if path != "" {
			x.set(path, at)
			path = x.resolve(path) + "."
		}
		path += string(k.Data)
	}
	if n.Kind == unstable.ArrayTable {
		x.set(path, at)
		x.entries[path]++
	}
	path = x.resolve(path)
	x.set(path, at)
	return path
}

func (x *indexer) keyValue(prefix string, n *unstable.Node) {
	path := prefix
	var at Position
	for it := n.Key(); it.Next(); {
		k := it.Node()
		at = x.pos(k.Raw)
		if path != "" {
			path += "."
		}
		path += string(k.Data)
		x.set(path, at)
	}
	x.value(path, at, n.Value())
}

func (x *indexer) value(path string, keyAt Position, v *unstable.Node) {
	switch v.Kind {
	case unstable.Array:
		x.set(path, keyAt)
		i := 0
		for it := v.Children(); it.Next(); i++ {
			x.value(path+"["+strconv.Itoa(i)+"]", keyAt, it.Node())
		}
	case unstable.InlineTable:
		x.at[path] = x.pos(v.Raw)
		for it := v.Children(); it.Next(); {
			if c := it.Node(); c.Kind == unstable.KeyValue {
				x.keyValue(path, c)
			}
		}
	default:
		if v.Raw.Length == 0 {
			x.set(path, keyAt)
			return
		}
		x.at[path] = x.pos(v.Raw)
	}
}
