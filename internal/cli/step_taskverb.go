package cli

import (
	"context"
	"errors"
	"fmt"

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
	if e.Origin == task.OriginDerived && s.closes(v) {
		return "a derived task closes when its items resolve", nil
	}
	if g.Handles(e.Kind, v) {
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
		if a := c.arg(k); a != "" && k != "verb" {
			args[k] = a
		}
	}
	var chain workflow.Chain
	if ev := c.why.Event; ev != nil {
		chain = ev.Chain
	}
	req := coretasks.Request{Verb: v, Args: args, Source: c.why.Workflow, Actor: task.ActorWorkflow, Chain: chain.Then(c.why.Workflow)}
	if _, err := g.Do(ctx, id, req); err != nil {
		if r := taskRefusal(err); r != "" {
			return "", automation.Refused{Reason: r}
		}
		return "", err
	}
	return fmt.Sprintf("ran %s on %s", v, id), nil
}

// closes says whether verb moves a task into the done or the dropped category, as the profile says.
func (s *stepSet) closes(v task.Verb) bool {
	l := s.e.loadedIntents()
	_, to, _ := l.Tasks.Move(string(v))
	c, ok := l.Tasks.CategoryOf(to)
	return ok && (c == task.CategoryDone || c == task.CategoryDropped)
}
