package expr

import (
	"errors"
	"slices"
	"time"

	"cel.dev/cel-go/cel"
	celast "cel.dev/cel-go/common/ast"
	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
	"cel.dev/cel-go/common/types/traits"

	"github.com/wloophq/wloop/internal/domain/mark"
)

// marksVar is the unit's marks a workflow's activation holds; handled(name,
// value) expands to handledFn over it, so a predicate never names it.
const (
	marksVar  = "_marks"
	handledFn = "_handled"
)

func handledOptions() []cel.EnvOption {
	marks := cel.MapType(cel.StringType, cel.StringType)
	overload := func(id string, t *cel.Type) cel.FunctionOpt {
		return cel.Overload(id, []*cel.Type{marks, cel.StringType, t}, cel.BoolType, cel.FunctionBinding(handled))
	}
	return []cel.EnvOption{
		cel.Variable(marksVar, marks),
		cel.Macros(cel.GlobalMacro("handled", 2, func(eh cel.MacroExprFactory, _ celast.Expr, args []celast.Expr) (celast.Expr, *cel.Error) {
			return eh.NewCall(handledFn, eh.NewIdent(marksVar), args[0], args[1]), nil
		})),
		cel.Function(handledFn,
			overload("handled_string", cel.StringType),
			overload("handled_timestamp", cel.TimestampType),
			overload("handled_int", cel.IntType)),
	}
}

// handledNames are the mark names a's handled() calls read, sorted; a name
// must be a string literal, so a workflow's marks can be checked at load.
func handledNames(a *celast.AST) ([]string, error) {
	var names []string
	var bad bool
	celast.PostOrderVisit(a.Expr(), celast.NewExprVisitor(func(e celast.Expr) {
		if e.Kind() != celast.CallKind || e.AsCall().FunctionName() != handledFn {
			return
		}
		arg := e.AsCall().Args()[1]
		if arg.Kind() != celast.LiteralKind {
			bad = true
			return
		}
		if s, ok := arg.AsLiteral().Value().(string); ok {
			names = append(names, s)
		}
	}))
	if bad {
		return nil, errors.New("handled names its mark with a string literal")
	}
	slices.Sort(names)
	return slices.Compact(names), nil
}

func handled(args ...ref.Val) ref.Val {
	m, ok := args[0].(traits.Mapper)
	if !ok {
		return types.NewErr("handled: marks of type %s", args[0].Type())
	}
	stored := ""
	if v, found := m.Find(args[1]); found {
		stored, _ = v.Value().(string)
	}
	var current any
	switch x := args[2].Value().(type) {
	case time.Time:
		current = x
	case int64:
		current = x
	case string:
		current = x
	}
	return types.Bool(mark.Handled(stored, current))
}
