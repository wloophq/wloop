package cli

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/wloophq/wloop/internal/core/automation"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/task"
)

func (s *stepSet) taskSetCheck(_ context.Context, c stepCall, _ automation.CheckOpts) (string, error) {
	if s.d.Props == nil {
		return "", errors.New("no property store: the daemon runs the steps")
	}
	if _, ok := task.IDOf(c.u); !ok {
		return "", fmt.Errorf("%s: %s is not a task", c.st.Use, c.u)
	}
	_, refusal := s.taskSetValue(c)
	return refusal, nil
}

// taskSetValue is the value c sets, parsed by its property's type, or why
// a workflow may not set it.
func (s *stepSet) taskSetValue(c stepCall) (task.Value, string) {
	tasks := s.e.loadedIntents().Tasks
	name := c.arg("prop")
	p, ok := tasks.Prop(name)
	if !ok {
		_, err := tasks.Parse(name, c.arg("value"))
		return task.Value{}, err.Error()
	}
	if !slices.Contains(p.Writers, task.WriterWorkflow) {
		return task.Value{}, fmt.Sprintf("a workflow does not write the property %s: its writers are %v", name, p.Writers)
	}
	v, err := tasks.Parse(name, c.arg("value"))
	if err != nil {
		return task.Value{}, err.Error()
	}
	return v, ""
}

func (s *stepSet) taskSetRun(ctx context.Context, c stepCall, _ automation.RunOpts) (string, error) {
	if _, err := s.taskSetCheck(ctx, c, automation.CheckOpts{}); err != nil {
		return "", err
	}
	v, refusal := s.taskSetValue(c)
	if refusal != "" {
		return "", automation.Refused{Reason: refusal}
	}
	id, _ := task.IDOf(c.u)
	prop := c.arg("prop")
	if err := s.d.Props.SetProp(ctx, id, prop, v, task.WriterWorkflow); err != nil {
		if errors.Is(err, coretasks.ErrNoTask) {
			return "", automation.Refused{Reason: err.Error()}
		}
		return "", err
	}
	return fmt.Sprintf("set %s of %s", prop, id), nil
}

var errGateOpenG2 = fmt.Errorf("not implemented: G2: %w", errors.ErrUnsupported)

func (s *stepSet) gateOpenCheck(context.Context, stepCall, automation.CheckOpts) (string, error) {
	return "", errGateOpenG2
}

func (s *stepSet) gateOpenRun(context.Context, stepCall, automation.RunOpts) (string, error) {
	return "", errGateOpenG2
}
