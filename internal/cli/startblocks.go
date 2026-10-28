package cli

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/round"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

// blindBlock is how long a start block holds when the session runs with its
// terminal's own agent home, which the loop cannot read for the answer.
const blindBlock = 24 * time.Hour

// startBlocks lifts, before a sweep plans, the start blocks of any unit
// that the agent home answered, so the round they held may be planned in
// the same sweep; a plan reads a block still holding as RoundState.Blocked.
type startBlocks struct {
	e *Env

	mu  sync.Mutex
	due time.Time
}

func (b *startBlocks) Refresh(ctx context.Context) error {
	e := b.e
	if e.Rec == nil || e.Agents == nil {
		return nil
	}
	blocks, err := e.Rec.FirstRoundBlocks(ctx)
	if err != nil || len(blocks) == 0 {
		return err
	}
	cfg, err := e.loadConfig()
	if err != nil {
		return err
	}
	st := round.New()
	if e.Rounds != nil {
		if st, err = e.Rounds.Load(ctx); err != nil {
			return err
		}
	}
	now := e.Now()
	var due time.Time
	var errs []error
	for u, ev := range blocks {
		blk, ok := inventory.StartBlockOf(ev)
		if !ok {
			continue
		}
		ucfg, err := e.unitConfig(ctx, cfg, u, st)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		cleared, until := answeredBlock(ctx, e.Agents[ucfg.Agent], blk, ucfg.AgentHome, ev.At, now)
		if cleared == "" {
			if !until.IsZero() && (due.IsZero() || until.Before(due)) {
				due = until
			}
			continue
		}
		unblock := port.Event{Unit: u, Kind: builtin.EventRoundUnblocked, At: now, Data: cleared}
		if err := e.Rec.RecordUnit(ctx, unblock); err != nil {
			errs = append(errs, err)
		}
	}
	b.mu.Lock()
	b.due = due
	b.mu.Unlock()
	return errors.Join(errs...)
}

// Due is when a blind block lifts with no answer read.
func (b *startBlocks) Due() time.Time {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.due.After(b.e.Now()) {
		return b.due
	}
	return time.Time{}
}

// answeredBlock says what answered b's question, read in home for its
// repository root or folder; "" while it still holds, with until the
// instant a blind block lifts.
func answeredBlock(ctx context.Context, agent turns.Agent, b inventory.StartBlock, home string, at, now time.Time) (cleared string, until time.Time) {
	cleared = "the agent's question is answered"
	if b.Question == turns.QuestionTrust {
		cleared = "the agent home trusts its folder"
	}
	q, isQuestions := agent.(turns.Questions)
	if isQuestions && answered(ctx, q, b, home) {
		return cleared, time.Time{}
	}
	if home == "" {
		if now.Sub(at) < blindBlock {
			return "", at.Add(blindBlock)
		}
		return "a day passed, and " + brand.Name + " cannot read the agent home of its terminal to see an answer", time.Time{}
	}
	if isQuestions {
		return "", time.Time{}
	}
	return cleared, time.Time{}
}

// answered says whether home, or the checkout for a question the agent
// records there, answers b's question for its repository root or folder.
func answered(ctx context.Context, q turns.Questions, b inventory.StartBlock, home string) bool {
	for _, d := range []string{b.Root, b.Folder} {
		if d != "" && !q.Unanswered(ctx, turns.AgentSession{Worktree: d, Home: home, Trusted: true}, b.Question) {
			return true
		}
	}
	return false
}
