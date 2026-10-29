package cli

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/core/automation"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/duration"
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
		_, err := tasks.Parse(name, c.arg("value"), s.e.Now().Location())
		return task.Value{}, err.Error()
	}
	if !slices.Contains(p.Writers, task.WriterWorkflow) {
		return task.Value{}, fmt.Sprintf("a workflow does not write the property %s: its writers are %v", name, p.Writers)
	}
	value := c.arg("value")
	if after, relative := strings.CutPrefix(value, "+"); relative && p.Type == task.PropDate {
		d, err := duration.Parse(after)
		if err != nil || d <= 0 {
			return task.Value{}, fmt.Sprintf("%s: %q is not a duration after now, such as +2d", name, value)
		}
		return task.Value{Time: s.e.Now().Add(d)}, ""
	}
	v, err := tasks.Parse(name, value, s.e.Now().Location())
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

// serviceProps writes the properties through the daemon's tasks service, under the lock its other writes take.
type serviceProps struct{ e *Env }

func (p serviceProps) service() (*coretasks.Service, error) {
	if p.e.tasks == nil {
		return nil, errors.New("no tasks service: the daemon runs the steps")
	}
	return p.e.tasks, nil
}

func (p serviceProps) SetProp(ctx context.Context, id task.ID, name string, v task.Value, w task.Writer) error {
	g, err := p.service()
	if err != nil {
		return err
	}
	return g.SetProp(ctx, id, name, v, w)
}

func (p serviceProps) PropsOf(ctx context.Context, id task.ID) (task.Props, error) {
	g, err := p.service()
	if err != nil {
		return nil, err
	}
	return g.PropsOf(ctx, id)
}
