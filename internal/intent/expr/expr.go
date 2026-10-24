// Package expr compiles and evaluates the predicates of intent and workflow
// manifests: CEL expressions (cel-go) over typed facts, checked at load.
package expr

import (
	"fmt"

	"cel.dev/cel-go/cel"
)

type Type int

const (
	Bool Type = iota + 1
	Int
	String
	List
	Duration
	Time
)

type Env map[string]Type // dotted field -> type, one slot

// Facts resolves a dotted field; the value's Go type matches the Env (bool,
// int, string, []string, time.Duration, time.Time).
type Facts interface{ Get(field string) any }

type Program struct {
	prg   cel.Program
	env   Env
	reads []string
}

type Error struct {
	Col     int // 1-based within the expression
	Msg     string
	Snippet string // the expression, then a caret line
}

func (e *Error) Error() string {
	if e.Snippet == "" {
		return fmt.Sprintf("%d: %s", e.Col, e.Msg)
	}
	return fmt.Sprintf("%d: %s\n%s", e.Col, e.Msg, e.Snippet)
}
