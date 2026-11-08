package cli

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	intentload "github.com/wloophq/wloop/internal/intent"
)

// stepGates are the gates gate.open opens, and the open ones it holds on.
type stepGates interface {
	Open(ctx context.Context, g gate.Gate, p gates.Policy) (gate.Gate, error)
	Where(ctx context.Context, q gates.Query) ([]gate.Gate, error)
}

var errNoGates = errors.New("no gates service: the daemon runs the steps")

// serviceGates are the daemon's gates, once serve built them.
type serviceGates struct{ e *Env }

func (g serviceGates) Open(ctx context.Context, gt gate.Gate, p gates.Policy) (gate.Gate, error) {
	if g.e.gates == nil {
		return gate.Gate{}, errNoGates
	}
	return g.e.gates.Open(ctx, gt, p)
}

func (g serviceGates) Where(ctx context.Context, q gates.Query) ([]gate.Gate, error) {
	if g.e.gateReader == nil {
		return nil, errNoGates
	}
	return g.e.gateReader.Where(ctx, q)
}

// gateOpenCheck holds while a gate of the step's name its workflow opened
// is open on the unit.
func (s *stepSet) gateOpenCheck(ctx context.Context, c stepCall, _ automation.CheckOpts) (string, error) {
	if s.d.Gates == nil {
		return "", errNoGates
	}
	name := c.arg("name")
	if _, ok := s.e.loadedIntents().Gates.Gate(name); !ok {
		return fmt.Sprintf("gates.toml declares no gate %s", name), nil
	}
	open, err := s.d.Gates.Where(ctx, gates.Query{Name: name, Unit: c.u})
	if err != nil {
		return "", err
	}
	if slices.ContainsFunc(open, func(g gate.Gate) bool { return g.Subject == c.why.Workflow }) {
		return fmt.Sprintf("gate %s of %s is open on %s", name, c.why.Workflow, c.u), nil
	}
	return "", nil
}

// gateOpenRun opens the gate the step names on the unit, its subject the
// workflow, decided by the workflow's [gates.approval] over the unit's
// facts and the configuration's.
func (s *stepSet) gateOpenRun(ctx context.Context, c stepCall, _ automation.RunOpts) (string, error) {
	refusal, err := s.gateOpenCheck(ctx, c, automation.CheckOpts{})
	switch {
	case err != nil:
		return "", err
	case refusal != "":
		return "", automation.Refused{Reason: refusal}
	}
	w, ok := s.configuredWorkflow(c.why.Workflow)
	if !ok {
		return "", fmt.Errorf("%s: workflow %s is not loaded", c.st.Use, c.why.Workflow)
	}
	facts, err := s.facts(ctx, c.u)
	if err != nil && !errors.Is(err, automation.ErrUnitGone) {
		return "", err
	}
	g := gate.Gate{Name: c.arg("name"), Unit: c.u, Subject: w.Name, Text: s.stepText(ctx, c.arg("name"), facts)}
	if g, err = s.d.Gates.Open(ctx, g, s.e.workflowPolicy(w, facts)); err != nil {
		return "", err
	}
	return fmt.Sprintf("opened gate %s %d, %s", g.Name, g.ID, g.Status), nil
}

// stepText is gate name's text over the facts of its unit, which are facts, and of the units its task holds,
// the first unit's where two say the same fact; "" leaves the gate its label, as when the text fails.
func (s *stepSet) stepText(ctx context.Context, name string, facts workflow.Facts) string {
	def, ok := s.e.loadedIntents().Gates.Gate(name)
	if !ok || def.Text == nil {
		return ""
	}
	all := maps.Clone(facts)
	for _, ref := range factList(facts, "task.units") {
		p, key, ok := strings.Cut(ref, ":")
		if !ok {
			continue
		}
		f, err := s.facts(ctx, unit.Unit{Provider: unit.ProviderID(p), Key: key})
		if err != nil {
			s.logf("gate %s: the facts of %s: %v", name, ref, err)
			continue
		}
		for k, v := range f {
			if _, set := all[k]; !set {
				all[k] = v
			}
		}
	}
	all, _ = workflow.Resolve(all, s.e.Now())
	text, err := def.Words(intentload.StepFacts(func(n string) any { return all[n] }))
	if err != nil {
		s.logf("gate %s: its text: %v", name, err)
		return ""
	}
	return text
}

// configuredWorkflow is the loaded workflow name with the configuration
// laid over it.
func (s *stepSet) configuredWorkflow(name string) (workflow.Workflow, bool) {
	return s.e.configuredWorkflow(name)
}

// configuredWorkflow is the loaded workflow name with the configuration
// laid over it.
func (e *Env) configuredWorkflow(name string) (workflow.Workflow, bool) {
	l := e.loadedIntents()
	for _, w := range l.Workflows {
		if w.Name != name {
			continue
		}
		if out := applyConfigWith([]workflow.Workflow{w}, e.currentConfig(), briefExtrasOf(l)); len(out) == 1 {
			return out[0], true
		}
	}
	return workflow.Workflow{}, false
}
