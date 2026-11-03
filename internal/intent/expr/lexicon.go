package expr

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	celast "cel.dev/cel-go/common/ast"
	"cel.dev/cel-go/common/types"
)

// An Env also carries the closed values of its string facts, as keys under
// metaPrefix, so a clone or a merge of the Env keeps them.
const (
	metaPrefix    = "\x00"
	enumMeta      = metaPrefix + "enum\x00"
	untrustedMeta = metaPrefix + "untrusted\x00"
	meta          = Type(-1)
)

// SetUntrusted marks the fact name as one an agent or a third party may
// set, which no releasing approval rule reads.
func (e Env) SetUntrusted(name string) { e[untrustedMeta+name] = meta }

// Untrusted says whether SetUntrusted marked the fact name.
func (e Env) Untrusted(name string) bool {
	_, ok := e[untrustedMeta+name]
	return ok
}

// SetEnum declares the closed values of the string fact name: a predicate
// that compares it with a literal outside them does not compile.
func (e Env) SetEnum(name string, values []string) {
	for _, v := range values {
		e[enumMeta+name+"\x00"+v] = meta
	}
}

// ClearEnum lets the string fact name take any value again.
func (e Env) ClearEnum(name string) {
	prefix := enumMeta + name + "\x00"
	for k := range e {
		if strings.HasPrefix(k, prefix) {
			delete(e, k)
		}
	}
}

// Enum is the closed values of name, sorted; nil when it takes any value.
func (e Env) Enum(name string) []string {
	var out []string
	prefix := enumMeta + name + "\x00"
	for k := range e {
		if v, ok := strings.CutPrefix(k, prefix); ok {
			out = append(out, v)
		}
	}
	slices.Sort(out)
	return out
}

// Names are the env's facts, sorted.
func (e Env) Names() []string {
	out := make([]string, 0, len(e))
	for k := range e {
		if !strings.HasPrefix(k, metaPrefix) {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out
}

// checkEnums refuses ==, != or in between a fact with closed values and a
// string literal outside them, at the literal.
func checkEnums(a *celast.AST, env Env) (pos int64, msg string, bad bool) {
	refs := a.ReferenceMap()
	fact := func(e celast.Expr) string {
		if r, ok := refs[e.ID()]; ok && r.Name != "" {
			return r.Name
		}
		return ""
	}
	literal := func(e celast.Expr) (string, bool) {
		if e.Kind() != celast.LiteralKind {
			return "", false
		}
		s, ok := e.AsLiteral().(types.String)
		return string(s), ok
	}
	try := func(name string, lit celast.Expr) bool {
		v, ok := literal(lit)
		values := env.Enum(name)
		if !ok || values == nil || slices.Contains(values, v) {
			return false
		}
		pos, msg, bad = lit.ID(), enumMessage(name, v, values), true
		return true
	}
	celast.PostOrderVisit(a.Expr(), celast.NewExprVisitor(func(e celast.Expr) {
		if bad || e.Kind() != celast.CallKind {
			return
		}
		call := e.AsCall()
		args := call.Args()
		if len(args) != 2 {
			return
		}
		switch call.FunctionName() {
		case "_==_", "_!=_":
			if n := fact(args[0]); n != "" {
				try(n, args[1])
			} else if n := fact(args[1]); n != "" {
				try(n, args[0])
			}
		case "@in":
			if n := fact(args[0]); n != "" && args[1].Kind() == celast.ListKind {
				for _, el := range args[1].AsList().Elements() {
					if try(n, el) {
						return
					}
				}
			}
		}
	}))
	return pos, msg, bad
}

func enumMessage(name, v string, values []string) string {
	quoted := make([]string, len(values))
	for i, s := range values {
		quoted[i] = strconv.Quote(s)
	}
	list := strings.Join(quoted, ", ")
	if n := len(quoted); n > 1 {
		list = strings.Join(quoted[:n-1], ", ") + " or " + quoted[n-1]
	}
	msg := fmt.Sprintf("%s is %s, never %q", name, list, v)
	return msg + DidYouMean(v, values)
}

// dayLiteral is a duration literal written in days, which CEL does not read.
var dayLiteral = regexp.MustCompile(`(\bduration\(\s*)("|')(\d+)d("|')`)

// shift is a rewrite of src: from offset at on, the rewritten text runs
// delta bytes longer.
type shift struct{ at, delta int }

// withHours rewrites each duration("<n>d") of src as hours, and the shifts
// that map an offset of the result back to src.
func withHours(src string) (string, []shift) {
	var b strings.Builder
	var shifts []shift
	last := 0
	for _, m := range dayLiteral.FindAllStringSubmatchIndex(src, -1) {
		days, err := strconv.Atoi(src[m[6]:m[7]])
		if err != nil || src[m[4]:m[5]] != src[m[8]:m[9]] {
			continue
		}
		b.WriteString(src[last:m[6]])
		hours := strconv.Itoa(days*24) + "h"
		b.WriteString(hours)
		shifts = append(shifts, shift{at: b.Len(), delta: len(hours) - (m[9] - m[6] - 1)})
		last = m[9] - 1
	}
	if shifts == nil {
		return src, nil
	}
	b.WriteString(src[last:])
	return b.String(), shifts
}

// back maps an offset of withHours' result to src.
func back(pos int, shifts []shift) int {
	total := 0
	for _, s := range shifts {
		if pos >= s.at {
			total += s.delta
		}
	}
	return pos - total
}
