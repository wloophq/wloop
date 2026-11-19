package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/turns"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/unit"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/turn"
)

// startHold is what a start block records of the round it holds.
type startHold struct {
	// round names the round; "" names it by its intent.
	round string
	// skip are the marks dismissing the block writes, its plan's OnSkipped.
	skip map[string]string
	// asked is the round a person asked for, nil for one the loop planned.
	asked *inventory.AskedRound
	// questionSkips records a question other than trust as a skipped round
	// rather than a block: a planned round in a workspace that already
	// exists, which the next trigger plans again.
	questionSkips bool
}

// askedHold is the hold of r's round: a new round a person asked for keeps
// its intent and what they gave it.
func askedHold(r turn.Request) startHold {
	if r.Trigger != turn.ByHand || r.Resume != nil {
		return startHold{}
	}
	return startHold{asked: &inventory.AskedRound{Intent: string(r.Intent), Guidance: r.Guidance, Inputs: r.Inputs}}
}

// startAgent gives the round of u in ws an agent session that can take its
// turn: the dormant one wake brings back, when wake is set and finds one,
// else a new one. A start the agent's question stops, or whose session
// never starts, holds the round with a start block, whoever asked for it.
func (e *Env) startAgent(ctx context.Context, cfg config.Config, ws wsdomain.Workspace, u unit.Unit, opts roundOptions, wake func() (proc, bool, error)) (p proc, woke bool, err error) {
	defer func() {
		if err != nil {
			err = e.holdRound(ctx, cfg, ws, u, opts, err)
		}
	}()
	if wake != nil {
		if p, woke, err = wake(); err != nil || woke {
			return p, woke, err
		}
	}
	s, err := e.agentSession(ctx, cfg, ws, opts)
	if err != nil {
		return proc{}, false, err
	}
	name, err := e.roundName(opts, u)
	if err != nil {
		return proc{}, false, err
	}
	p, err = e.startSession(ctx, cfg, ws, u, s, name, false, opts.trusted)
	return p, false, err
}

// holdRound records the start block err, a failed start of the round of u
// in ws, puts on the round, and returns err, saying so when it did: a round
// on an untrusted folder, one whose session never started, and one that
// does not skip on its question stay blocked until the agent home answers or
// the user decides; any other question records why the round was skipped,
// once per distinct reason.
func (e *Env) holdRound(ctx context.Context, cfg config.Config, ws wsdomain.Workspace, u unit.Unit, opts roundOptions, err error) error {
	var block inventory.StartBlock
	var untrusted *turns.UntrustedError
	var asked *turns.QuestionError
	why := err.Error()
	switch {
	case errors.As(err, &untrusted):
		block = inventory.StartBlock{Question: turns.QuestionTrust, Root: untrusted.Root, Folder: untrusted.Folder, Home: untrusted.Home,
			Homes: e.trustHomes(cfg, untrusted.Home)}
		why = untrustedWhy(untrusted)
	case errors.As(err, &asked):
		block = inventory.StartBlock{Question: asked.Question, Root: asked.Root}
	case errors.As(err, new(errNoMCP)):
		block = inventory.StartBlock{Folder: ws.Path, RetryAt: e.Now().Add(time.Duration(cfg.Turns.NoStartWait))}
	default:
		return err
	}
	if e.Rec == nil {
		return err
	}
	ctx = context.WithoutCancel(ctx)
	h := opts.hold
	round := h.round
	if round == "" {
		round = string(opts.intent)
	}
	now := e.Now()
	if asked != nil && h.questionSkips {
		sum := sha256.Sum256([]byte(round + "\n" + block.Key(time.Time{})))
		ev := port.Event{Unit: u, Kind: dinv.KindRoundSkipped, Key: "start " + hex.EncodeToString(sum[:8]), At: now, Data: round + ": " + why}
		if rerr := e.Rec.RecordUnit(ctx, ev); rerr != nil {
			fmt.Fprintf(e.Err, "%s error: %s: recording %s: %v\n", e.stamp(), u.Label(), dinv.KindRoundSkipped, rerr)
		}
		return err
	}
	block.Round, block.Skip, block.Asked = round, h.skip, h.asked
	ev := port.Event{Unit: u, Kind: dinv.KindRoundBlocked, Key: block.Key(now), At: now, Data: why}
	if rerr := e.Rec.RecordUnit(ctx, ev); rerr != nil {
		fmt.Fprintf(e.Err, "%s error: %s: recording the blocked %s: %v; the next sweep retries\n", e.stamp(), u.Label(), round, rerr)
		return err
	}
	fmt.Fprintf(e.Out, "%s held the %s of %s: %s\n", e.stamp(), round, u.Label(), why)
	return fmt.Errorf("%w; the round waits on its start block", err)
}

// untrustedWhy is the outcome a round blocked on an untrusted folder shows.
func untrustedWhy(u *turns.UntrustedError) string {
	home := "the agent home of its terminal"
	if u.Home != "" {
		home = u.Home
	}
	return "could not start: " + u.Folder + " is not trusted in " + home
}
