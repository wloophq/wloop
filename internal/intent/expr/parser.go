package expr

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"cel.dev/cel-go/cel"
	celast "cel.dev/cel-go/common/ast"
	"cel.dev/cel-go/common/types"
)

// Compile type-checks src against env; it returns an *Error on failure, and an empty or blank src is an error.
func Compile(src string, env Env) (*Program, error) { return compileWith(src, env, false) }

func compileWith(src string, env Env, marks bool) (*Program, error) {
	if strings.TrimSpace(src) == "" {
		return nil, &Error{Col: 1, Msg: "empty predicate"}
	}
	ce, err := celEnv(env, marks)
	if err != nil {
		return nil, &Error{Col: 1, Msg: err.Error()}
	}
	cs, shifts := withHours(src)
	fail := func(pos int, msg string) error { return newError(src, back(pos, shifts), "%s", msg) }
	ast, iss := ce.Compile(cs)
	if iss.Err() != nil {
		parsed, _ := ce.Parse(cs)
		return nil, fail(issueError(cs, env, parsed, iss.Errors()))
	}
	if pos, msg, bad := checkLiterals(cs, ast.NativeRep()); bad {
		return nil, fail(pos, msg)
	}
	if id, msg, bad := checkEnums(ast.NativeRep(), env); bad {
		loc := ast.NativeRep().SourceInfo().GetStartLocation(id)
		return nil, fail(offset(cs, loc.Line(), loc.Column()), msg)
	}
	if out := ast.OutputType(); !out.IsExactType(cel.BoolType) {
		start := len(src) - len(strings.TrimLeftFunc(src, unicode.IsSpace))
		return nil, newError(src, start, "predicate must be bool, got %s", typeName(out.String()))
	}
	prg, err := ce.Program(ast, cel.EvalOptions(cel.OptOptimize))
	if err != nil {
		return nil, &Error{Col: 1, Msg: err.Error()}
	}
	p := &Program{src: src, prg: prg, env: env, reads: reads(ast.NativeRep(), env), pins: pins(ast.NativeRep())}
	if marks {
		if p.marks, err = handledNames(ast.NativeRep()); err != nil {
			return nil, &Error{Col: 1, Msg: err.Error()}
		}
	}
	return p, nil
}

var envs sync.Map

func celEnv(env Env, marks bool) (*cel.Env, error) {
	names := env.Names()
	var key strings.Builder
	opts := make([]cel.EnvOption, 0, len(names))
	for _, n := range names {
		fmt.Fprintf(&key, "%s:%d;", n, env[n])
		t, ok := celTypes[env[n]]
		if !ok {
			return nil, fmt.Errorf("field %s has an unsupported type", n)
		}
		opts = append(opts, cel.Variable(n, t))
	}
	if marks {
		key.WriteString(marksVar)
		opts = append(opts, handledOptions()...)
	}
	if ce, ok := envs.Load(key.String()); ok {
		return ce.(*cel.Env), nil
	}
	ce, err := cel.NewEnv(opts...)
	if err != nil {
		return nil, err
	}
	envs.Store(key.String(), ce)
	return ce, nil
}

var celTypes = map[Type]*cel.Type{
	Bool:     cel.BoolType,
	Int:      cel.IntType,
	String:   cel.StringType,
	List:     cel.ListType(cel.StringType),
	Duration: cel.DurationType,
	Time:     cel.TimestampType,
}

func reads(a *celast.AST, env Env) []string {
	var out []string
	for _, r := range a.ReferenceMap() {
		if _, ok := env[r.Name]; ok && !slices.Contains(out, r.Name) {
			out = append(out, r.Name)
		}
	}
	sort.Strings(out)
	return out
}

// checkLiterals refuses at load a duration or timestamp literal that could
// only fail at evaluation: where, and why.
func checkLiterals(src string, a *celast.AST) (pos int, msg string, bad bool) {
	celast.PostOrderVisit(a.Expr(), celast.NewExprVisitor(func(e celast.Expr) {
		if bad || e.Kind() != celast.CallKind {
			return
		}
		call := e.AsCall()
		if call.IsMemberFunction() || len(call.Args()) != 1 || call.Args()[0].Kind() != celast.LiteralKind {
			return
		}
		arg := call.Args()[0]
		s, ok := arg.AsLiteral().(types.String)
		if !ok {
			return
		}
		loc := a.SourceInfo().GetStartLocation(arg.ID())
		at := offset(src, loc.Line(), loc.Column())
		switch call.FunctionName() {
		case "duration":
			if _, err := time.ParseDuration(string(s)); err != nil {
				pos, msg, bad = at, fmt.Sprintf("invalid duration %q", string(s)), true
			}
		case "timestamp":
			if _, err := time.Parse(time.RFC3339, string(s)); err != nil {
				pos, msg, bad = at, fmt.Sprintf("invalid timestamp %q; use RFC 3339, as in \"2026-10-21T09:00:00-03:00\"", string(s)), true
			}
		}
	}))
	return pos, msg, bad
}

// offset turns CEL's location (1-based line, 0-based code point column) into a byte offset of src.
func offset(src string, line, col int) int {
	if line < 1 || col < 0 {
		return 0
	}
	pos := 0
	for l := 1; l < line; l++ {
		i := strings.IndexByte(src[pos:], '\n')
		if i < 0 {
			return len(src)
		}
		pos += i + 1
	}
	for ; col > 0 && pos < len(src); col-- {
		_, n := utf8.DecodeRuneInString(src[pos:])
		pos += n
	}
	return pos
}

var (
	reUndeclared = regexp.MustCompile(`^undeclared reference to '[^']*'`)
	reOverload   = regexp.MustCompile(`^found no matching overload for '([^']+)' applied to '\((.*)\)'$`)
	reInput      = regexp.MustCompile(`^(?:mismatched|extraneous) input '(.*)' expecting`)
	reToken      = regexp.MustCompile(`(?s)^token recognition error at: '(.*)'$`)
	reMissing    = regexp.MustCompile(`^(missing .*) at '.*'$`)
	reExpected   = regexp.MustCompile(`^expected type '(.*)' but found '(.*)'$`)
	cmpOps       = map[string]string{"_==_": "==", "_!=_": "!=", "_<_": "<", "_<=_": "<=", "_>_": ">", "_>=_": ">="}
)

// issueError is where CEL's first error is and what it says, worded for an author.
func issueError(src string, env Env, parsed *cel.Ast, errs []*cel.Error) (int, string) {
	sort.SliceStable(errs, func(i, j int) bool {
		a, b := errs[i].Location, errs[j].Location
		return a.Line() < b.Line() || a.Line() == b.Line() && a.Column() < b.Column()
	})
	e := errs[0]
	pos := offset(src, e.Location.Line(), e.Location.Column())
	msg, syntax := strings.CutPrefix(e.Message, "Syntax error: ")
	if !syntax {
		pos = fieldStart(src, pos)
	}
	switch {
	case reUndeclared.MatchString(msg):
		name := fieldAt(src, pos)
		if renamed := env.Was(name); renamed != "" {
			msg = "unknown field " + name + "; it was renamed " + renamed
			break
		}
		msg = "unknown field " + name + suggest(name, env)
	case reOverload.MatchString(msg):
		m := reOverload.FindStringSubmatch(msg)
		args := splitArgs(m[2])
		switch op, isCmp := cmpOps[m[1]]; {
		case isCmp && len(args) == 2:
			msg = fmt.Sprintf("cannot compare %s %s %s", args[0], op, args[1])
		case m[1] == "@in" && len(args) == 2:
			msg = fmt.Sprintf("in needs a value and a list of its type, got %s in %s", args[0], args[1])
		case m[1] == "!_" && len(args) == 1:
			msg = fmt.Sprintf("! needs bool, got %s", args[0])
			if hint := negatedOperand(src, parsed, e.ExprID); hint != "" {
				msg += hint
			}
		}
	case reExpected.MatchString(msg):
		m := reExpected.FindStringSubmatch(msg)
		msg = fmt.Sprintf("expected %s, got %s", typeName(m[1]), typeName(m[2]))
	case strings.HasPrefix(msg, "expression recursion limit exceeded"):
		msg = "expression nested too deeply"
	case reToken.MatchString(msg):
		text := reToken.FindStringSubmatch(msg)[1]
		if strings.HasPrefix(text, `"`) || strings.HasPrefix(text, "'") {
			msg = "unterminated string"
		} else {
			r, _ := utf8.DecodeRuneInString(text)
			msg = "unexpected " + strconv.QuoteRune(r)
		}
	case reInput.MatchString(msg):
		if in := reInput.FindStringSubmatch(msg)[1]; in == "<EOF>" {
			msg = "unexpected end of expression"
		} else {
			msg = fmt.Sprintf("unexpected %q", in)
		}
	case strings.HasPrefix(msg, "no viable alternative"):
		if pos >= len(src) {
			msg = "unexpected end of expression"
		} else {
			r, _ := utf8.DecodeRuneInString(src[pos:])
			msg = fmt.Sprintf("unexpected %q", string(r))
		}
	case reMissing.MatchString(msg):
		msg = reMissing.FindStringSubmatch(msg)[1]
	}
	return pos, msg
}

// fieldStart moves a position CEL gives at a field's dot to the field's start.
func fieldStart(src string, pos int) int {
	if pos >= len(src) || src[pos] != '.' {
		return pos
	}
	for pos > 0 && isFieldByte(src[pos-1]) {
		pos--
	}
	return pos
}

func isFieldByte(c byte) bool {
	return c == '_' || c == '.' || 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9'
}

func fieldAt(src string, pos int) string {
	end := pos
	for end < len(src) && isFieldByte(src[end]) {
		end++
	}
	name := strings.TrimRight(src[pos:end], ".")
	if rest := strings.TrimLeft(src[end:], " \t\r\n"); strings.HasPrefix(rest, "(") {
		if i := strings.LastIndexByte(name, '.'); i > 0 {
			name = name[:i]
		}
	}
	return name
}

func splitArgs(s string) []string {
	var out []string
	depth, start := 0, 0
	for i, c := range s {
		switch c {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, typeName(strings.TrimSpace(s[start:i])))
				start = i + 1
			}
		}
	}
	return append(out, typeName(strings.TrimSpace(s[start:])))
}

func typeName(cel string) string {
	switch {
	case strings.HasPrefix(cel, "list("):
		return List.String()
	case strings.HasPrefix(cel, "map("):
		return "map"
	case cel == "google.protobuf.Duration":
		return Duration.String()
	case cel == "google.protobuf.Timestamp":
		return Time.String()
	}
	return cel
}

// negatedOperand explains `!a == b` and `!a in l`, which the earlier grammar
// read as `!(a == b)` and CEL reads as `(!a) == b`.
func negatedOperand(src string, parsed *cel.Ast, notID int64) string {
	if parsed == nil {
		return ""
	}
	a := parsed.NativeRep()
	var op string
	var right celast.Expr
	celast.PostOrderVisit(a.Expr(), celast.NewExprVisitor(func(e celast.Expr) {
		if e.Kind() != celast.CallKind {
			return
		}
		call := e.AsCall()
		name, isCmp := cmpOps[call.FunctionName()]
		if call.FunctionName() == "@in" {
			name, isCmp = "in", true
		}
		if isCmp && len(call.Args()) == 2 && call.Args()[0].ID() == notID {
			op, right = name, call.Args()[1]
		}
	}))
	notAt, ok := a.SourceInfo().GetOffsetRange(notID)
	if right == nil || !ok {
		return ""
	}
	end := int32(0)
	celast.PostOrderVisit(right, celast.NewExprVisitor(func(e celast.Expr) {
		if r, ok := a.SourceInfo().GetOffsetRange(e.ID()); ok {
			end = max(end, r.Stop)
		}
	}))
	start, stop := runeOffset(src, int(notAt.Start)), runeOffset(src, int(end))
	if src[start] != '!' || stop <= start {
		return fmt.Sprintf("; ! applies before %s: wrap the comparison, as in !(a %s b)", op, op)
	}
	for stop < len(src) && (isFieldByte(src[stop]) || depth(src[start:stop]) > 0 && strings.ContainsRune(")] \t", rune(src[stop]))) {
		stop++
	}
	return fmt.Sprintf("; ! applies before %s: write !(%s)", op, strings.TrimSpace(src[start+1:stop]))
}

func runeOffset(src string, n int) int {
	pos := 0
	for ; n > 0 && pos < len(src); n-- {
		_, size := utf8.DecodeRuneInString(src[pos:])
		pos += size
	}
	return pos
}

// depth counts the brackets s leaves open outside string literals.
func depth(s string) int {
	d := 0
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0 && c == '\\':
			i++
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '(' || c == '[':
			d++
		case c == ')' || c == ']':
			d--
		}
	}
	return d
}

// pins are the field == "literal" terms of a's top-level conjunction, as
// field=literal: what every match of the predicate requires.
func pins(a *celast.AST) []string {
	var out []string
	var walk func(e celast.Expr)
	walk = func(e celast.Expr) {
		if e.Kind() != celast.CallKind {
			return
		}
		call := e.AsCall()
		switch call.FunctionName() {
		case "_&&_":
			for _, arg := range call.Args() {
				walk(arg)
			}
		case "_==_":
			args := call.Args()
			for i, arg := range args {
				lit := args[1-i]
				ref, ok := a.ReferenceMap()[arg.ID()]
				if !ok || ref.Name == "" || lit.Kind() != celast.LiteralKind {
					continue
				}
				if s, ok := lit.AsLiteral().(types.String); ok {
					out = append(out, ref.Name+"="+string(s))
				}
			}
		}
	}
	walk(a.Expr())
	return out
}
