package catalog

import (
	"context"
	"fmt"
	"maps"
	"strconv"
	"strings"
	"sync"

	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/wake"
	"github.com/wloophq/wloop/internal/domain/workflow"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/intent/expr"
)

// Holds is the core's wakes.Holds: a wake's When, a predicate over the
// task's workflow facts and the properties of tasks.toml, matched over the
// facts facts reads of the task. A condition that does not compile fails.
func Holds(loaded func() *intentload.Loaded, facts func(ctx context.Context, t task.Task) (workflow.Facts, error)) func(ctx context.Context, w wake.Wake, t task.Task) (bool, error) {
	var mu sync.Mutex
	compiled := map[string]workflow.Predicate{}
	return func(ctx context.Context, w wake.Wake, t task.Task) (bool, error) {
		env := builtin.WorkflowEnv(builtin.TaskSlot)
		maps.Copy(env, loaded().Tasks.Facts())
		mu.Lock()
		pred, ok := compiled[w.When]
		if !ok {
			var err error
			if pred, err = expr.CompileWorkflow(w.When, env); err != nil {
				mu.Unlock()
				return false, fmt.Errorf("the wake's when %q: %w", w.When, err)
			}
			compiled[w.When] = pred
		}
		mu.Unlock()
		f, err := facts(ctx, t)
		if err != nil {
			return false, err
		}
		return pred.Match(workflow.Vars{Facts: f})
	}
}

// Baseline is the core's tasks.Baseline: a wake's When that names facts of
// the task alone, list or bool ones separated by commas, such as
// "task.link_owes, task.link_signal.in_loop", asks to wake once the task's
// own value of any of them changes. Baseline turns it into the condition over
// the values facts reads of the task now, a list's in any order; any other
// When stays as it is.
func Baseline(loaded func() *intentload.Loaded, facts func(ctx context.Context, t task.Task) (workflow.Facts, error)) func(ctx context.Context, t task.Task, when string) (string, error) {
	return func(ctx context.Context, t task.Task, when string) (string, error) {
		env := builtin.WorkflowEnv(builtin.TaskSlot)
		maps.Copy(env, loaded().Tasks.Facts())
		names := strings.Split(when, ",")
		for i, n := range names {
			names[i] = strings.TrimSpace(n)
			if typ, ok := env[names[i]]; !ok || typ != expr.List && typ != expr.Bool {
				return when, nil
			}
		}
		f, err := facts(ctx, t)
		if err != nil {
			return "", err
		}
		var parts []string
		for _, name := range names {
			if env[name] == expr.Bool {
				on, _ := f[name].(bool)
				parts = append(parts, name+" != "+strconv.FormatBool(on))
				continue
			}
			now, _ := f[name].([]string)
			parts = append(parts, "size("+name+") != "+strconv.Itoa(len(now)))
			for _, v := range now {
				parts = append(parts, "!("+strconv.Quote(v)+" in "+name+")")
			}
		}
		return strings.Join(parts, " || "), nil
	}
}
