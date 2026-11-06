package workflow

import (
	"context"
	"encoding/json"
	"slices"
)

type readsKey struct{}

// Unrequested is the value of a costly fact whose reader did not name it (WithReads): evaluating it is an error,
// so a reader that forgot to name it fails instead of reading an empty value, and it prints as not requested.
type Unrequested struct{ Fact string }

func (u Unrequested) String() string { return "not requested" }

func (u Unrequested) MarshalJSON() ([]byte, error) { return json.Marshal(u.String()) }

// WithReads is ctx carrying names besides the reads it already carries: the facts, or texts that may name
// facts, its reader of facts reads. A costly fact is computed only for a reader that names it.
func WithReads(ctx context.Context, names ...string) context.Context {
	if len(names) == 0 {
		return ctx
	}
	return context.WithValue(ctx, readsKey{}, slices.Concat(ReadsOf(ctx), names))
}

// OnlyReads is ctx carrying names in place of the reads it carries.
func OnlyReads(ctx context.Context, names ...string) context.Context {
	return context.WithValue(ctx, readsKey{}, slices.Clone(names))
}

// ReadsOf are the reads ctx carries.
func ReadsOf(ctx context.Context) []string {
	names, _ := ctx.Value(readsKey{}).([]string)
	return names
}

// Reads are the facts w reads: those of its when, withdraw and approval predicates, its key and marks, and
// every text of its steps' args and of its ask, any of which may name a fact.
func Reads(w Workflow) []string {
	var out []string
	for _, p := range append(w.Approval.Predicates(), w.When, w.Withdraw) {
		if p != nil {
			out = append(out, p.Reads()...)
		}
	}
	out = append(out, w.Key...)
	for _, f := range w.Marks {
		out = append(out, f)
	}
	if w.Ask != nil && w.Ask.Tree != nil {
		out = append(out, w.Ask.Tree.Root.String())
	}
	return append(out, StepReads(w.Steps)...)
}

// StepReads are the texts of steps' args, which may name facts.
func StepReads(steps []Step) []string {
	var out []string
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case string:
			out = append(out, x)
		case []any:
			for _, e := range x {
				walk(e)
			}
		case []string:
			out = append(out, x...)
		case map[string]any:
			for _, e := range x {
				walk(e)
			}
		}
	}
	for _, st := range steps {
		walk(st.Args)
	}
	return out
}
