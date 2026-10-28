package cli

import (
	"context"
	"errors"
	"fmt"

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
	return "", nil
}

func (s *stepSet) taskSetRun(ctx context.Context, c stepCall, _ automation.RunOpts) (string, error) {
	if _, err := s.taskSetCheck(ctx, c, automation.CheckOpts{}); err != nil {
		return "", err
	}
	id, _ := task.IDOf(c.u)
	prop := c.arg("prop")
	if err := s.d.Props.SetProp(ctx, id, prop, task.Value{Text: c.arg("value")}, task.WriterWorkflow); err != nil {
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
