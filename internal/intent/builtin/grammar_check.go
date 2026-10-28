package builtin

import (
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/wloophq/wloop/internal/domain/duration"
	"github.com/wloophq/wloop/internal/intent/expr"
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
	w := walker{rows: rowsOf(all, surface)}
	w.table(nil, "", doc)
	return w.problems
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

// confused are the rows whose keys a slip swaps, each with the other's path:
// a value of the wrong shape at one names the other.
var confused = map[string]string{
	"record.require[]": "record.requires",
	"record.requires":  "record.require",
}

func (w *walker) value(pat []string, path string, v any) {
	if other, ok := confused[strings.Join(pat, ".")]; ok {
		n := len(w.problems)
		defer func() {
			for i := n; i < len(w.problems); i++ {
				if strings.HasPrefix(w.problems[i].Msg, "must be") {
					w.problems[i].Msg += fmt.Sprintf("; did you mean %s?", other)
				}
			}
		}()
	}
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
		w.anyOf(r, path, v)
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
			if msg, near := EnumProblem(s, r.Values()); msg != "" {
				w.problems = append(w.problems, Problem{Path: path, Msg: msg, Near: near})
			}
		}
	}
}

// anyOf checks a value of more than one form against the row's values: a
// string, or each string of a list.
func (w *walker) anyOf(r Key, path string, v any) {
	if r.Values == nil {
		return
	}
	el := Key{Path: r.Path, Kind: KindEnum, Values: r.Values}
	switch x := v.(type) {
	case string:
		w.leaf(el, path, x)
	case []any:
		for i, e := range x {
			if s, ok := e.(string); ok {
				w.leaf(el, fmt.Sprintf("%s[%d]", path, i), s)
			}
		}
	}
}

// number refuses TOML's inf and nan, which no setting means.
func number(v any) bool {
	switch x := v.(type) {
	case int, int64:
		return true
	case float64:
		return !math.IsInf(x, 0) && !math.IsNaN(x)
	}
	return false
}

func integer(v any) bool {
	switch x := v.(type) {
	case int, int64:
		return true
	case float64:
		return number(x) && x == math.Trunc(x)
	}
	return false
}

// EnumProblem says why v is none of values, "" when it is one: the values
// and the near one, never a value that releases, which a guess must not
// turn a typo into.
func EnumProblem(v string, values []Value) (msg, near string) {
	names := make([]string, len(values))
	for i, x := range values {
		if x.Name == v {
			return "", ""
		}
		names[i] = x.Name
	}
	msg = fmt.Sprintf("%q is not one of %s", v, OrList(names))
	if c := expr.Suggest(v, names); c != "" && !values[slices.Index(names, c)].Releases {
		near = c
		msg += fmt.Sprintf("; did you mean %q?", c)
	}
	return msg, near
}

// OrList is names quoted, as "a", "b" or "c".
func OrList(names []string) string {
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
	near := expr.Suggest(k, siblings)
	w.problems = append(w.problems, Problem{Path: path, Msg: "unknown key" + expr.DidYouMean(k, siblings), Near: near, Unknown: true})
}

var kindNames = map[Kind]string{KindPred: "a predicate", KindFact: "a fact", KindTemplate: "a template", KindFile: "a file",
	KindEnum: "a value of a set", KindDur: "a duration", KindBool: "true or false", KindInt: "an integer", KindText: "a string",
	KindList: "a list", KindTable: "a table", KindMap: "a table", KindAny: "any value", KindNumber: "a number"}

func kindName(k Kind) string { return kindNames[k] }
