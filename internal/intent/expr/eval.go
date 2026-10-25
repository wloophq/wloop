package expr

import (
	"fmt"
	"time"

	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/interpreter"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

// Eval evaluates the predicate; a nil fact reads as its type's zero value and
// a runtime error, such as a fact of another Go type, reads as false.
func (p *Program) Eval(f Facts) bool {
	ok, _ := p.run(activation{env: p.env, get: f.Get})
	return ok
}

// Reads are the fields the predicate names, sorted.
func (p *Program) Reads() []string { return p.reads }

func (p *Program) run(a activation) (bool, error) {
	out, _, err := p.prg.Eval(a)
	if err != nil {
		return false, err
	}
	b, ok := out.Value().(bool)
	if !ok {
		return false, fmt.Errorf("predicate gave %v, not a bool", out.Value())
	}
	return b, nil
}

type activation struct {
	env   Env
	marks map[string]string
	get   func(string) any
}

func (a activation) ResolveName(name string) (any, bool) {
	if name == marksVar {
		if a.marks == nil {
			return map[string]string{}, true
		}
		return a.marks, true
	}
	typ, ok := a.env[name]
	if !ok {
		return nil, false
	}
	v := a.get(name)
	if v == nil {
		return zero(typ), true
	}
	if !goTypeFits(typ, v) {
		return types.NewErr("fact %s has Go type %T, want %s", name, v, typ), true
	}
	return v, true
}

func goTypeFits(t Type, v any) bool {
	switch v.(type) {
	case bool:
		return t == Bool
	case int, int64:
		return t == Int
	case string:
		return t == String
	case []string:
		return t == List
	case time.Duration:
		return t == Duration
	case time.Time:
		return t == Time
	}
	return false
}

func (a activation) Parent() interpreter.Activation { return nil }

func zero(t Type) any {
	switch t {
	case Bool:
		return false
	case Int:
		return 0
	case String:
		return ""
	case List:
		return []string{}
	case Duration:
		return time.Duration(0)
	case Time:
		return time.Time{}
	}
	return nil
}

const nowVar = "now"

type predicate struct{ p *Program }

// CompileWorkflow compiles a workflow's `when` over env (one unit slot,
// trigger.* and config.*) plus now, the evaluation's time.
func CompileWorkflow(src string, env Env) (workflow.Predicate, error) {
	full := make(Env, len(env)+1)
	for k, v := range env {
		full[k] = v
	}
	full[nowVar] = Time
	p, err := compileWith(src, full, true)
	if err != nil {
		return nil, err
	}
	reads := make([]string, 0, len(p.reads))
	for _, r := range p.reads {
		if r != nowVar {
			reads = append(reads, r)
		}
	}
	p.reads = reads
	return predicate{p}, nil
}

func (w predicate) Reads() []string { return w.p.reads }

func (w predicate) Match(v workflow.Vars) (bool, error) {
	return w.p.run(activation{env: w.p.env, marks: v.Marks, get: func(name string) any {
		if name == nowVar {
			return v.Now
		}
		for _, f := range []workflow.Facts{v.Facts, v.Trigger, v.Config} {
			if x, ok := f[name]; ok {
				return x
			}
		}
		return nil
	}})
}
