package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/wloophq/wloop/internal/core/automation"
	coregtd "github.com/wloophq/wloop/internal/core/gtd"
	"github.com/wloophq/wloop/internal/domain/gtd"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

func (s *stepSet) entries() (*coregtd.Service, error) {
	if s.e.gtd == nil {
		return nil, errors.New("no entries service: the daemon runs the steps")
	}
	return s.e.gtd, nil
}

// entryRefusal is err as the reason an entry refuses a verb, "" when err
// is not one.
func entryRefusal(err error) string {
	if errors.Is(err, coregtd.ErrVerb) || errors.Is(err, coregtd.ErrNoEntry) || errors.Is(err, coregtd.ErrPluginDisabled) {
		return err.Error()
	}
	return ""
}

func (s *stepSet) entryVerbCheck(ctx context.Context, c stepCall, _ automation.CheckOpts) (string, error) {
	g, err := s.entries()
	if err != nil {
		return "", err
	}
	id, ok := gtd.IDOf(c.u)
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
	v := gtd.Verb(c.arg("verb"))
	if g.Handles(e.Kind, v) {
		return "", nil
	}
	if err := coregtd.Accepts(e, v); err != nil {
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
	id, ok := gtd.IDOf(c.u)
	if !ok {
		return "", fmt.Errorf("%s: %s is not an entry", c.st.Use, c.u)
	}
	v := gtd.Verb(c.arg("verb"))
	args := map[string]string{}
	for _, k := range builtin.StepArgs(builtin.StepEntryVerb) {
		if a := c.arg(k); a != "" && k != "verb" {
			args[k] = a
		}
	}
	req := coregtd.Request{Verb: v, Args: args, Source: "workflow " + c.why.Workflow, Automated: true}
	if _, err := g.Do(ctx, id, req); err != nil {
		if r := entryRefusal(err); r != "" {
			return "", automation.Refused{Reason: r}
		}
		return "", err
	}
	return fmt.Sprintf("ran %s on %s", v, id), nil
}
