package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/wloophq/wloop/internal/core/automation"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

func (s *stepSet) entries() (*coretasks.Service, error) {
	if s.e.gtd == nil {
		return nil, errors.New("no entries service: the daemon runs the steps")
	}
	return s.e.gtd, nil
}

// entryRefusal is err as the reason an entry refuses a verb, "" when err
// is not one.
func entryRefusal(err error) string {
	if errors.Is(err, coretasks.ErrVerb) || errors.Is(err, coretasks.ErrNoEntry) || errors.Is(err, coretasks.ErrPluginDisabled) {
		return err.Error()
	}
	return ""
}

func (s *stepSet) entryVerbCheck(ctx context.Context, c stepCall, _ automation.CheckOpts) (string, error) {
	g, err := s.entries()
	if err != nil {
		return "", err
	}
	id, ok := task.IDOf(c.u)
	if !ok {
		return "", fmt.Errorf("%s: %s is not an entry", c.st.Use, c.u)
	}
	e, err := g.Entry(ctx, id)
	if err != nil {
		if r := entryRefusal(err); r != "" {
			return r, nil
		}
		return "", err
	}
	v := task.Verb(c.arg("verb"))
	if e.Origin == task.OriginDerived && (v == task.VerbDone || v == task.VerbDismiss) {
		return "a derived entry closes when its items resolve", nil
	}
	if g.Handles(e.Kind, v) {
		return "", nil
	}
	if err := coretasks.Accepts(e, v); err != nil {
		if r := entryRefusal(err); r != "" {
			return r, nil
		}
		return "", err
	}
	return "", nil
}

func (s *stepSet) entryVerbRun(ctx context.Context, c stepCall, _ automation.RunOpts) (string, error) {
	g, err := s.entries()
	if err != nil {
		return "", err
	}
	id, ok := task.IDOf(c.u)
	if !ok {
		return "", fmt.Errorf("%s: %s is not an entry", c.st.Use, c.u)
	}
	v := task.Verb(c.arg("verb"))
	args := map[string]string{}
	for _, k := range builtin.StepArgs(builtin.StepEntryVerb) {
		if a := c.arg(k); a != "" && k != "verb" {
			args[k] = a
		}
	}
	req := coretasks.Request{Verb: v, Args: args, Source: "workflow " + c.why.Workflow, Automated: true}
	if _, err := g.Do(ctx, id, req); err != nil {
		if r := entryRefusal(err); r != "" {
			return "", automation.Refused{Reason: r}
		}
		return "", err
	}
	return fmt.Sprintf("ran %s on %s", v, id), nil
}
