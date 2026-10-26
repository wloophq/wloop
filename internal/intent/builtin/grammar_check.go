package builtin

import (
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/wloophq/wloop/internal/domain/duration"
)

func rowsOf(rows []Key, surface Surface) []Key {
	var out []Key
	for _, k := range rows {
		if k.In == surface {
			out = append(out, k)
		}
	}
	return out
}

func segments(path string) []string { return strings.Split(path, ".") }

// bare is a path or a segment without its array marks.
func bare(s string) string { return strings.ReplaceAll(s, "[]", "") }

func check(all []Key, surface Surface, doc map[string]any) []Problem {
	rows := rowsOf(all, surface)
	if len(rows) == 0 {
		return nil
	}
	w := walker{rows: rows}
	w.table(nil, "", doc)
	return w.problems
}

func checkUser(all []Key, surface Surface, doc map[string]any) []Problem {
	out := check(all, surface, doc)
	for _, k := range rowsOf(all, surface) {
		if k.Bundled && present(doc, segments(k.Path)) {
			out = append(out, Problem{Path: bare(k.Path), Msg: "only the bundled profile sets " + bare(k.Path)})
		}
	}
	return out
}

func present(doc map[string]any, path []string) bool {
	v, ok := doc[bare(path[0])]
	if !ok || len(path) == 1 {
		return ok
	}
	m, ok := v.(map[string]any)
	return ok && present(m, path[1:])
}

type walker struct {
	rows     []Key
	problems []Problem
}

func (w *walker) add(path, format string, args ...any) {
	w.problems = append(w.problems, Problem{Path: path, Msg: fmt.Sprintf(format, args...)})
}

// table checks the keys of m, at the pattern pat and the document path at.
func (w *walker) table(pat []string, at string, m map[string]any) {
	for _, k := range slices.Sorted(maps.Keys(m)) {
		path := k
		if at != "" {
			path = at + "." + k
		}
		seg, ok := w.match(pat, k)
		if !ok {
			w.unknown(pat, k, path)
			continue
		}
		w.value(append(slices.Clone(pat), seg), path, m[k])
	}
}

// match is the row segment after pat that names k: k itself, k as an array
// of tables, or an author's name.
func (w *walker) match(pat []string, k string) (string, bool) {
	var wild string
	for _, r := range w.rows {
		s := segments(r.Path)
		if len(s) <= len(pat) || !slices.Equal(s[:len(pat)], pat) {
			continue
		}
		switch seg := s[len(pat)]; {
		case bare(seg) == k:
			return seg, true
		case bare(seg) == "*":
			wild = seg
		}
	}
	return wild, wild != ""
}

func (w *walker) row(pat []string) (Key, bool) {
	for _, r := range w.rows {
		if slices.Equal(segments(r.Path), pat) {
			return r, true
		}
	}
	return Key{}, false
}

func (w *walker) value(pat []string, path string, v any) {
	r, isRow := w.row(pat)
	array := strings.HasSuffix(pat[len(pat)-1], "[]")
	switch x := v.(type) {
	case map[string]any:
		if isRow && !slices.Contains([]Kind{KindTable, KindMap, KindAny}, r.Kind) {
			w.add(path, "must be %s, not a table", kindName(r.Kind))
			return
		}
		w.table(pat, path, x)
		return
	case []any:
		if array {
			for i, e := range x {
				at := fmt.Sprintf("%s[%d]", path, i)
				if m, ok := e.(map[string]any); ok {
					w.table(pat, at, m)
				} else {
					w.add(at, "must be a table")
				}
			}
			return
		}
	}
	if !isRow {
		w.add(path, "must be a table")
		return
	}
	w.leaf(r, path, v)
}

func (w *walker) leaf(r Key, path string, v any) {
	if v == nil {
		return // JSON's null, which leaves the setting unset
	}
	switch r.Kind {
	case KindAny:
	case KindTable, KindMap:
		w.add(path, "must be a table")
	case KindBool:
		if _, ok := v.(bool); !ok {
			w.add(path, "must be true or false")
		}
	case KindInt:
		if !integer(v) {
			w.add(path, "must be an integer")
		}
	case KindNumber:
		if !number(v) {
			w.add(path, "must be a number")
		}
	case KindList:
		list, ok := v.([]any)
		if !ok {
			w.add(path, "must be a list")
			return
		}
		el := Key{Path: r.Path, Kind: r.Elem, Values: r.Values}
		for i, e := range list {
			w.leaf(el, fmt.Sprintf("%s[%d]", path, i), e)
		}
	default:
		s, ok := v.(string)
		if !ok {
			w.add(path, "must be a string")
			return
		}
		switch r.Kind {
		case KindDur:
			if _, err := duration.Parse(s); err != nil {
				w.add(path, "%s", err.Error())
			}
		case KindEnum:
			if msg := enumProblem(s, r.Values()); msg != "" {
				w.add(path, "%s", msg)
			}
		}
	}
}

func number(v any) bool {
	switch v.(type) {
	case int, int64, float64:
		return true
	}
	return false
}

func integer(v any) bool {
	switch x := v.(type) {
	case int, int64:
		return true
	case float64:
		return x == math.Trunc(x)
	}
	return false
}

// enumProblem says why v is none of values: the values and the closest.
func enumProblem(v string, values []Value) string {
	names := make([]string, len(values))
	for i, x := range values {
		if x.Name == v {
			return ""
		}
		names[i] = x.Name
	}
	msg := fmt.Sprintf("%q is not one of %s", v, orList(names))
	if c := Closest(v, names); c != "" {
		msg += fmt.Sprintf("; did you mean %q?", c)
	}
	return msg
}

func orList(names []string) string {
	q := make([]string, len(names))
	for i, n := range names {
		q[i] = strconv.Quote(n)
	}
	if len(q) < 2 {
		return strings.Join(q, "")
	}
	return strings.Join(q[:len(q)-1], ", ") + " or " + q[len(q)-1]
}

func (w *walker) unknown(pat []string, k, path string) {
	var siblings []string
	for _, r := range w.rows {
		s := segments(r.Path)
		if len(s) > len(pat) && slices.Equal(s[:len(pat)], pat) && bare(s[len(pat)]) != "*" {
			siblings = append(siblings, bare(s[len(pat)]))
		}
	}
	if c := Closest(k, siblings); c != "" {
		w.add(path, "unknown key; did you mean %s?", c)
		return
	}
	w.add(path, "unknown key")
}

// Closest is the option nearest s by edit distance, "" when none is near.
func Closest(s string, options []string) string {
	best, bestD := "", len(s)/2+2
	for _, o := range options {
		if d := levenshtein(s, o); d < bestD {
			best, bestD = o, d
		}
	}
	return best
}

func levenshtein(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

var kindNames = map[Kind]string{KindPred: "a predicate", KindFact: "a fact", KindTemplate: "a template", KindFile: "a file",
	KindEnum: "a value of a set", KindDur: "a duration", KindBool: "true or false", KindInt: "an integer", KindText: "a string",
	KindList: "a list", KindTable: "a table", KindMap: "a table", KindAny: "any value", KindNumber: "a number"}

func kindName(k Kind) string { return kindNames[k] }
