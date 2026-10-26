package cli

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/headless"
	"github.com/wloophq/wloop/internal/core/turns"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

// headlessStarter starts a headless turn; Start returns once it is stored.
type headlessStarter interface {
	Start(ctx context.Context, r headless.Request) (int64, error)
}

// automatedTurns counts the automated turns sent since from on units of
// providers other than except's.
type automatedTurns interface {
	AutomatedTurns(ctx context.Context, from time.Time, except []unit.ProviderID) (int, error)
}

// unitGuards are a turn.start's checks on a unit outside the round
// providers: the intent is a turn that serves the unit in the step's mode,
// its refuse_when is empty, no turn of it is open on the unit, a repository
// it names by input is never taken from an automatic run, and the day's
// automated turns stay under turns.per_day. The workflow's own
// limits are the engine's.
func (s *stepSet) unitGuards(ctx context.Context, c stepCall, p startPlan) (string, startPlan, error) {
	d, mode := p.args.def, p.args.runMode()
	switch {
	case d.Name == "":
		return "", p, fmt.Errorf("the intent %q does not load", p.args.intent)
	case d.Builds != "":
		return "", p, fmt.Errorf("the intent %s builds a %s and starts no turn", d.Name, d.Builds)
	case !d.Serves(c.u.Provider):
		return "", p, fmt.Errorf("the intent %s does not act on %s units", d.Name, c.u.Provider)
	case !d.Runs(mode):
		return "", p, fmt.Errorf("the intent %s does not run as %s (its manifest's modes)", d.Name, mode)
	}
	var err error
	if p.facts, err = s.facts(ctx, c.u); err != nil {
		return "", p, err
	}
	if d.RefuseWhen != "" {
		if why := factString(p.facts, d.RefuseWhen); why != "" {
			return why, p, nil
		}
	}
	if w := d.WorkspaceRepo; w != nil && w.RepoInput != "" {
		return "its repository comes from the input " + w.RepoInput + ", which only a person sets", p, nil
	}
	if why, err := s.openTurnOf(ctx, c.u, dturn.Intent(d.Name)); err != nil || why != "" {
		return why, p, err
	}
	why, err := s.automatedBudget(ctx)
	return why, p, err
}

func (s *stepSet) openTurnOf(ctx context.Context, u unit.Unit, intent dturn.Intent) (string, error) {
	if s.e.Turns == nil {
		return "", nil
	}
	books, err := s.e.Turns.Store.Books(ctx, u)
	if err != nil {
		return "", err
	}
	b := books[u]
	if t, ok := b.Newest(intent); ok && t.State.Open() {
		return "its " + string(intent) + " turn is " + string(t.State), nil
	}
	return "", nil
}

// automatedBudget refuses once the local day's automated turns on units
// outside the round providers reach turns.per_day.
func (s *stepSet) automatedBudget(ctx context.Context) (string, error) {
	cfg, err := s.e.loadConfig()
	if err != nil {
		return "", err
	}
	if s.d.Turns == nil {
		return "", errors.New("no turn store counts the automated turns")
	}
	now := s.e.Now()
	n, err := s.d.Turns.AutomatedTurns(ctx, time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()), builtin.RoundProviders)
	if err != nil {
		return "", err
	}
	if limit := cfg.Turns.PerDay; n >= limit {
		return fmt.Sprintf("turns.per_day reached (%d)", limit), nil
	}
	return "", nil
}

// unitStart checks again on current facts, then starts the turn in its
// mode: a headless call, or a session where the intent runs.
func (s *stepSet) unitStart(ctx context.Context, c stepCall, o automation.RunOpts) (string, error) {
	refusal, p, err := s.unitGuards(ctx, c, startPlan{args: s.e.startOf(c.st)})
	if err != nil {
		return "", err
	}
	if refusal != "" {
		return "", automation.Refused{Reason: refusal}
	}
	d, why := p.args.def, c.why
	if p.args.runMode() == string(dturn.RunnerHeadless) {
		if s.d.Headless == nil {
			return "", errors.New("no headless runner is configured")
		}
		if _, err := s.d.Headless.Start(ctx, headless.Request{Unit: c.u, Intent: dturn.Intent(d.Name), Guidance: o.Guidance,
			Trigger: dturn.Automated, Workflow: why.Workflow}); err != nil {
			return "", startError(err)
		}
		return "started a headless " + string(d.Name) + " turn", nil
	}
	err = cliLauncher{s.e}.Launch(ctx, turns.Launch{Unit: c.u, Intent: dturn.Intent(d.Name), Guidance: o.Guidance, Trigger: dturn.Automated, Why: &why})
	if err != nil {
		return "", startError(err)
	}
	return "started a " + string(d.Name) + " turn", nil
}
