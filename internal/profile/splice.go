package profile

import (
	"bytes"
	"fmt"
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2/unstable"
)

// pathKey is path's segments joined by "\x00", the key Splice and Texts index values by.
func pathKey(path []string) string { return strings.Join(path, "\x00") }

// keyString is path as a person reads it: dotted, an array index as [3].
func keyString(path []string) string {
	var b strings.Builder
	for i, s := range path {
		if i > 0 && !strings.HasPrefix(s, "[") {
			b.WriteByte('.')
		}
		b.WriteString(s)
	}
	return b.String()
}

// tomlString is a string value of a TOML document: its path, decoded text and byte range.
type tomlString struct {
	path []string
	text string
	raw  unstable.Range
}

// tomlStrings are the string values of the TOML document src, each at its concrete path.
func tomlStrings(src []byte) ([]tomlString, error) {
	var p unstable.Parser
	p.Reset(src)
	var out []tomlString
	var table []string
	arrays := map[string]int{}
	resolve := func(keys []string) []string {
		var at []string
		for _, k := range keys {
			at = append(at, k)
			if i, ok := arrays[pathKey(at)]; ok {
				at = append(at, fmt.Sprintf("[%d]", i))
			}
		}
		return at
	}
	var value func(path []string, n *unstable.Node)
	value = func(path []string, n *unstable.Node) {
		switch n.Kind {
		case unstable.String:
			out = append(out, tomlString{path: path, text: string(n.Data), raw: n.Raw})
		case unstable.Array:
			i := 0
			for it := n.Children(); it.Next(); {
				if c := it.Node(); c.Kind != unstable.Comment {
					value(append(slices.Clip(path), fmt.Sprintf("[%d]", i)), c)
					i++
				}
			}
		case unstable.InlineTable:
			for it := n.Children(); it.Next(); {
				if c := it.Node(); c.Kind == unstable.KeyValue {
					value(append(slices.Clip(path), keysOf(c)...), c.Value())
				}
			}
		}
	}
	for p.NextExpression() {
		e := p.Expression()
		switch e.Kind {
		case unstable.Table:
			table = resolve(keysOf(e))
		case unstable.ArrayTable:
			keys := keysOf(e)
			at := append(resolve(keys[:len(keys)-1]), keys[len(keys)-1])
			i, seen := arrays[pathKey(at)]
			if seen {
				i++
			}
			arrays[pathKey(at)] = i
			table = append(at, fmt.Sprintf("[%d]", i))
		case unstable.KeyValue:
			value(append(slices.Clip(table), keysOf(e)...), e.Value())
		}
	}
	return out, p.Error()
}

func keysOf(n *unstable.Node) []string {
	var out []string
	for it := n.Key(); it.Next(); {
		out = append(out, string(it.Node().Data))
	}
	return out
}

// Splice is the TOML document src with each string value at a key of values (its path's segments joined by
// "\x00") replaced, every other byte and every line number kept.
func Splice(src []byte, values map[string]string) ([]byte, error) {
	strs, err := tomlStrings(src)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	at := 0
	for _, s := range strs {
		v, ok := values[pathKey(s.path)]
		if !ok {
			continue
		}
		start, end := int(s.raw.Offset), int(s.raw.Offset+s.raw.Length)
		out.Write(src[at:start])
		out.WriteString(basicString(v, bytes.Count(src[start:end], []byte("\n"))))
		at = end
	}
	out.Write(src[at:])
	return out.Bytes(), nil
}

// basicString is text as a TOML basic string spanning lines newlines: a value that spanned lines keeps them
// as a multi-line string whose line-ending backslash trims them from the value.
func basicString(text string, lines int) string {
	var b strings.Builder
	q := `"`
	if lines > 0 {
		q = `"""`
	}
	b.WriteString(q)
	for _, r := range text {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u%04X`, r)
				continue
			}
			b.WriteRune(r)
		}
	}
	if lines > 0 {
		b.WriteString(`\` + strings.Repeat("\n", lines))
	}
	b.WriteString(q)
	return b.String()
}
