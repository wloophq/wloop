package daemon

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/catalog"
	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

// runHeld runs the round the daily cap held, by hand: the plan gate's
// approving intent on a row with no round pending.
var runHeld = delegated{loop: true, full: true, event: builtin.EventRoundSent, data: "run now", call: Actions.Run}

// unblocks are a trust gate's consequences, by whether it was approved:
// trust the folder and run the round, or lift the block until the round's
// next trigger.
var unblocks = map[bool]delegated{
	true:  {loop: true, full: true, event: builtin.EventRoundSent, data: "trusted its folder and ran", call: unblocking(Unblocker.TrustAndRun)},
	false: {loop: true, event: builtin.EventRoundUnblocked, data: "dismissed by you", call: unblocking(Unblocker.Unblock)},
}

// Release is a resolved plan gate's consequence: the plan carries it onto
// the item the gate names, under the loop lock.
func (s *Server) Release(ctx context.Context, g gate.Gate) error {
	if s.deps.Plan == nil {
		return errPlanUnavailable
	}
	if !LoopHeld(ctx) {
		if s.forge(g.Unit) {
			ctx = s.readAhead(ctx, g.Unit, true)
		}
		s.deps.LoopMu.Lock()
		defer s.deps.LoopMu.Unlock()
		ctx = withLoopHeld(ctx)
	}
	return s.deps.Plan.Release(ctx, g)
}

// Unblock is a resolved trust gate's consequence on the start block of its
// unit's round: an approval trusts the folder and runs the round, any other
// resolution lifts the block until a new trigger.
func (s *Server) Unblock(ctx context.Context, g gate.Gate) error {
	if s.deps.Actions == nil {
		return errActionsUnavailable
	}
	return s.delegate(ctx, unblocks[g.Status == gate.Approved], g.Unit, Call{Out: io.Discard, Block: g.Subject})
}

// openRowGate is u's open gate of kind k: for a plan gate, the one its
// pending round waits on, else its oldest; ok is false when none is open.
func (s *Server) openRowGate(ctx context.Context, k gate.Kind, u unit.Unit) (gate.Gate, workflow.Item, bool, error) {
	if s.deps.GateReader == nil {
		return gate.Gate{}, workflow.Item{}, false, nil
	}
	open, err := s.deps.GateReader.Where(ctx, gates.Query{Kind: k, Unit: u})
	if err != nil || len(open) == 0 {
		return gate.Gate{}, workflow.Item{}, false, err
	}
	if k != gate.KindPlan || s.deps.Plan == nil {
		return open[0], workflow.Item{}, true, nil
	}
	var first *gate.Gate
	var firstItem workflow.Item
	for i, g := range open {
		items, err := s.deps.Plan.Items(ctx, automation.Filter{ID: g.Subject})
		if err != nil {
			return gate.Gate{}, workflow.Item{}, false, err
		}
		if len(items) == 0 {
			continue
		}
		if slices.ContainsFunc(items[0].Steps, func(st workflow.Step) bool { return st.Use == builtin.StepTurnStart }) {
			return g, items[0], true, nil
		}
		if first == nil {
			first, firstItem = &open[i], items[0]
		}
	}
	if first == nil {
		return gate.Gate{}, workflow.Item{}, false, nil
	}
	return *first, firstItem, true, nil
}

// actOnGate resolves u's open gate of g's kind with g's intent, as by. With
// no such gate open, the plan gate's first approving intent runs a round
// the cap held, and its first taking text keeps the guidance for the next
// round; any other intent is refused.
func (s *Server) actOnGate(ctx context.Context, g catalog.GateIntent, u unit.Unit, req api.ActionRequest, by task.Actor, out io.Writer) error {
	open, it, ok, err := s.openRowGate(ctx, g.Kind, u)
	if err != nil {
		return err
	}
	guidance := strings.TrimSpace(req.Guidance)
	switch {
	case !ok && g.Kind == gate.KindTrust && s.deps.Gates != nil:
		key, blocked, err := s.blockKey(ctx, u)
		if err != nil {
			return err
		}
		if !blocked {
			return conflict{msg: "no start block holds a round of " + u.Label()}
		}
		if open, err = s.deps.Gates.Open(ctx, gate.Gate{Kind: gate.KindTrust, Unit: u, Subject: key}, holdForYou{}); err != nil {
			return err
		}
	case !ok || s.deps.Gates == nil:
		return s.actWithoutGate(ctx, g, u, req, guidance, out)
	}
	r := gates.Resolution{Intent: g.Intent.Name, By: by}
	if g.Intent.Answer {
		if guidance == "" {
			return badRequest{g.Intent.Name + " needs your text"}
		}
		r.Answer = guidance
	}
	if err := s.deps.Gates.Resolve(ctx, open.ID, r); err != nil {
		return err
	}
	fmt.Fprintln(out, resolvedLine(g, it, u))
	return nil
}

// resolvedLine says what resolving g's gate on u, held by it, did.
func resolvedLine(g catalog.GateIntent, it workflow.Item, u unit.Unit) string {
	what := cmp.Or(it.Workflow, "round")
	switch {
	case g.Kind == gate.KindTrust && g.Role == builtin.GateApproves:
		return "trusted the folder of " + u.Label() + " and ran its round"
	case g.Kind == gate.KindTrust:
		return "lifted the start block of " + u.Label()
	case g.Role == builtin.GateRefuses:
		return "skipped the pending " + what
	}
	return "sent the " + what + " round"
}

// blockKey names the start block holding u's round, when Actions read it;
// with no reader, the unit's block is taken as it stands.
func (s *Server) blockKey(ctx context.Context, u unit.Unit) (string, bool, error) {
	r, ok := s.deps.Actions.(BlockReader)
	if !ok {
		return "", true, nil
	}
	return r.StartBlockKey(ctx, u)
}

// holdForYou holds a gate for a person: a start block's trust is the
// user's alone.
type holdForYou struct{}

func (holdForYou) Decide(context.Context, gate.Gate) (gates.Decision, error) {
	return gates.Decision{Mode: gate.ModeWait}, nil
}

// actWithoutGate is g's intent on u with no gate of its kind open, or no
// gates served.
func (s *Server) actWithoutGate(ctx context.Context, g catalog.GateIntent, u unit.Unit, req api.ActionRequest, guidance string, out io.Writer) error {
	switch {
	case g.Kind != gate.KindPlan || !g.First:
	case g.Role == builtin.GateApproves:
		if s.deps.Actions == nil {
			return errActionsUnavailable
		}
		return s.delegate(ctx, runHeld, u, Call{Guidance: req.Guidance, AgentHome: req.AgentHome, Out: out})
	case g.Role == builtin.GateTakesText && guidance != "":
		if s.deps.Plan == nil {
			return conflict{msg: "the daemon plans no automatic round; see doctor"}
		}
		if s.deps.Overrides == nil {
			return errStoreUnavailable
		}
		if _, err := s.deps.Overrides.SetOverride(ctx, u, map[string]string{port.OverrideNextGuidance: guidance}, nil); err != nil {
			return err
		}
		fmt.Fprintln(out, "no round pending; the guidance goes with the next automatic round")
		return nil
	}
	if g.Kind == gate.KindTrust {
		if s.deps.Actions == nil {
			return errActionsUnavailable
		}
		return s.delegate(ctx, unblocks[g.Role == builtin.GateApproves], u, Call{AgentHome: req.AgentHome, Out: out})
	}
	return badRequest{"no round pending for " + u.Label() + "; add guidance to send it with the next round"}
}

// planRoles are the roles of the plan gate's intents the plan's actions on
// an item resolve its gate with.
var planRoles = map[string]builtin.GateRole{
	"apply": builtin.GateApproves, "guide": builtin.GateTakesText, "dismiss": builtin.GateRefuses, "skip": builtin.GateRefuses,
}

// itemGate is the open plan gate it waits on; false when none is open.
func (s *Server) itemGate(ctx context.Context, it workflow.Item) (gate.Gate, bool, error) {
	if s.deps.GateReader == nil || s.deps.Gates == nil {
		return gate.Gate{}, false, nil
	}
	open, err := s.deps.GateReader.Where(ctx, gates.Query{Kind: gate.KindPlan, Unit: it.Unit})
	if err != nil {
		return gate.Gate{}, false, err
	}
	i := slices.IndexFunc(open, func(g gate.Gate) bool { return g.Subject == it.ID })
	if i < 0 {
		return gate.Gate{}, false, nil
	}
	return open[i], true, nil
}

// resolveItemGate resolves g, the gate of a plan item, with the first intent
// of the role the plan's action names, as by.
func (s *Server) resolveItemGate(ctx context.Context, g gate.Gate, action, guidance string, by task.Actor) error {
	role := planRoles[action]
	def, _ := s.loaded().Gates.Gate(g.Name)
	i := slices.IndexFunc(def.Intents, func(in intentload.GateIntent) bool {
		return builtin.GateRoleOf(in.Status, in.Answer) == role && slices.Contains(g.Intents, in.Name)
	})
	if i < 0 {
		return conflict{msg: fmt.Sprintf("gate %s offers no intent that %s", g.Name, role)}
	}
	r := gates.Resolution{Intent: def.Intents[i].Name, By: by}
	if def.Intents[i].Answer {
		r.Answer = guidance
	}
	return s.deps.Gates.Resolve(ctx, g.ID, r)
}
