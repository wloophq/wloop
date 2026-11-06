package cli

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/core/automation"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

func (s *stepSet) tasks() (*coretasks.Service, error) {
	if s.e.tasks == nil {
		return nil, errors.New("no tasks service: the daemon runs the steps")
	}
	return s.e.tasks, nil
}

// taskRefusal is err as the reason a task refuses a verb, "" when err
// is not one.
func taskRefusal(err error) string {
	if errors.Is(err, coretasks.ErrVerb) || errors.Is(err, coretasks.ErrNoTask) || errors.Is(err, coretasks.ErrPluginDisabled) ||
		errors.Is(err, coretasks.ErrForbidden) {
		return err.Error()
	}
	return ""
}

func (s *stepSet) taskVerbCheck(ctx context.Context, c stepCall, _ automation.CheckOpts) (string, error) {
	g, err := s.tasks()
	if err != nil {
		return "", err
	}
	id, ok := task.IDOf(c.u)
	if !ok {
		return "", fmt.Errorf("%s: %s is not a task", c.st.Use, c.u)
	}
	e, err := g.Task(ctx, id)
	if err != nil {
		if r := taskRefusal(err); r != "" {
			return r, nil
		}
		return "", err
	}
	v := task.Verb(c.arg("verb"))
	if e.Origin == task.OriginDerived && len(e.Items) > 0 && s.closes(v) {
		return "a derived task closes when its items resolve", nil
	}
	if _, refusal, err := s.otherTasks(ctx, c); err != nil || refusal != "" {
		return refusal, err
	}
	if g.Handles(e, v) {
		return "", nil
	}
	if err := g.Accepts(e, v, task.ActorWorkflow); err != nil {
		if r := taskRefusal(err); r != "" {
			return r, nil
		}
		return "", err
	}
	return "", nil
}

func (s *stepSet) taskVerbRun(ctx context.Context, c stepCall, _ automation.RunOpts) (string, error) {
	g, err := s.tasks()
	if err != nil {
		return "", err
	}
	id, ok := task.IDOf(c.u)
	if !ok {
		return "", fmt.Errorf("%s: %s is not a task", c.st.Use, c.u)
	}
	v := task.Verb(c.arg("verb"))
	args := map[string]string{}
	for _, k := range builtin.StepArgs(builtin.StepTaskVerb) {
		if a := c.arg(k); a != "" && k != "verb" && k != argEach {
			args[k] = a
		}
	}
	others, refusal, err := s.otherTasks(ctx, c)
	switch {
	case err != nil:
		return "", err
	case refusal != "":
		return "", automation.Refused{Reason: refusal}
	case len(others) == 0:
		others = []task.ID{""}
	}
	var chain workflow.Chain
	if ev := c.why.Event; ev != nil {
		chain = ev.Chain
	}
	var ran, refused []string
	for _, other := range others {
		a := maps.Clone(args)
		if other != "" {
			a[task.ArgTask] = string(other)
		}
		req := coretasks.Request{Verb: v, Args: a, Source: c.why.Workflow, Actor: task.ActorWorkflow, Chain: chain.Then(c.why.Workflow)}
		if _, err := g.Do(ctx, id, req); err != nil {
			if r := taskRefusal(err); r != "" {
				refused = append(refused, r)
				continue
			}
			return "", err
		}
		ran = append(ran, string(other))
	}
	if len(ran) == 0 {
		return "", automation.Refused{Reason: strings.Join(refused, "; ")}
	}
	if len(others) == 1 && others[0] == "" {
		return fmt.Sprintf("ran %s on %s", v, id), nil
	}
	return fmt.Sprintf("ran %s on %s with %s", v, id, strings.Join(ran, ", ")), nil
}

// closes says whether verb moves a task into the done or the dropped category, as the profile says.
func (s *stepSet) closes(v task.Verb) bool {
	l := s.e.loadedIntents()
	_, to, _ := l.Tasks.Move(string(v))
	c, ok := l.Tasks.CategoryOf(to)
	return ok && (c == task.CategoryDone || c == task.CategoryDropped)
}

// argEach is the task.verb argument that runs the verb with every task a list fact names.
const argEach = "each"

// otherTasks are the tasks c's task argument names, for a verb whose handler takes one: the id the fact it
// names holds, or the first of a list fact's, every one of them with each; refusal says why it names none.
func (s *stepSet) otherTasks(ctx context.Context, c stepCall) (ids []task.ID, refusal string, err error) {
	name := c.arg(task.ArgTask)
	handler := s.e.loadedIntents().Tasks.Handler(c.arg("verb"))
	if name == "" || !slices.Contains(builtin.HandlerArgs[handler], task.ArgTask) {
		return nil, "", nil
	}
	f, err := s.facts(workflow.WithReads(ctx, name), c.u)
	if err != nil {
		return nil, "", err
	}
	switch v := f[name].(type) {
	case string:
		if v != "" {
			ids = append(ids, task.ID(v))
		}
	case []string:
		for _, id := range v {
			ids = append(ids, task.ID(id))
		}
	case []any:
		for _, x := range v {
			if id, _ := x.(string); id != "" {
				ids = append(ids, task.ID(id))
			}
		}
	}
	if len(ids) == 0 {
		return nil, fmt.Sprintf("%s names no task", name), nil
	}
	if c.arg(argEach) != "true" {
		ids = ids[:1]
	}
	return ids, "", nil
}
