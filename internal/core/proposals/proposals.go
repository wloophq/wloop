// Package proposals is the job that proposes what each enabled plugin
// proposes on its own units, such as GitHub's nudge. Every draft waits for
// its rule's approval policy. The drafts on tasks and Slack units come
// from the profile's workflows through draft.propose.
package proposals

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/core/port"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// ReasonMoot is the discard reason of a plugin rule's draft on a unit whose
// closure is posted.
const ReasonMoot = draft.WithdrawMoot

// Outbox is what the job needs of the outbox service (*outbox.Service).
type Outbox interface {
	Propose(ctx context.Context, p outbox.Proposal) (draft.Draft, error)
	List(ctx context.Context, f outbox.Filter) ([]draft.Draft, error)
	Withdraw(ctx context.Context, id draft.ID, reason string) (draft.Draft, error)
}

// Source is an enabled plugin's Proposals.
type Source struct {
	Provider  unit.ProviderID
	Proposals func(ctx context.Context, now time.Time) (Pass, error)
}

// Pass is what a plugin's rules call for now: the drafts to propose, and the
// rules that no longer call for a draft on a unit.
type Pass struct {
	Proposals   []outbox.Proposal
	Withdrawals []Withdrawal
}

// Withdrawal says the plugin's rule Rule no longer calls for a draft on Unit.
type Withdrawal struct {
	Unit unit.Unit
	Rule string
}

type Job struct {
	Outbox Outbox
	// Facts reads a unit's card facts; a plugin rule's draft on a unit whose closure is posted is withdrawn. nil: none.
	Facts   coretasks.Facts
	Sources []Source
	// Enabled says whether a provider's plugin is enabled; nil: every one is.
	Enabled func(unit.ProviderID) bool
	// Approval is the configured approval of a plugin's rules' drafts; nil: wait.
	Approval func(unit.ProviderID) draft.Approval
	// RepeatAfter is how long a sent, discarded or dry-run draft of a key keeps the key quiet.
	RepeatAfter time.Duration
	Clock       port.Clock
	Log         func(format string, args ...any) // ids, rules, kinds and units; never text

	// mu makes each pass, its dedupe checks and its proposals, one step.
	mu sync.Mutex
	// refused is the last refusal logged per rule key, so a refusal repeating every pass is logged once.
	refused map[string]string
}

// Summary counts what one pass did.
type Summary struct {
	Proposed, Held, Discarded, Refused int
}

func (s Summary) String() string {
	return fmt.Sprintf("%d proposed, %d held, %d discarded, %d refused", s.Proposed, s.Held, s.Discarded, s.Refused)
}

func (j *Job) enabled(p unit.ProviderID) bool { return j.Enabled == nil || j.Enabled(p) }

func (j *Job) approval(p unit.ProviderID) draft.Approval {
	if j.Approval == nil {
		return draft.Approval{Mode: draft.Wait}
	}
	return j.Approval(p)
}

func (j *Job) logf(format string, args ...any) {
	if j.Log != nil {
		j.Log(format, args...)
	}
}

// Run is one pass: it withdraws the plugin rules' drafts on units whose
// closure is posted or whose rule its plugin says no longer calls for them,
// then proposes what every plugin calls for now and no draft of the same rule
// and key holds back.
func (j *Job) Run(ctx context.Context) (Summary, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	now := j.Clock.Now()
	var sum Summary
	discarded, err := j.withdraw(ctx)
	sum.Discarded = discarded
	if err != nil {
		return sum, err
	}
	props, withdrawals, err := j.fromSources(ctx)
	withdrawn, werr := j.withdrawNamed(ctx, withdrawals)
	sum.Discarded += withdrawn
	err = errors.Join(err, werr)
	for _, p := range props {
		p.Policy = draft.Fixed(j.approval(p.Unit.Provider))
		switch perr := j.propose(ctx, p, now); {
		case perr == nil:
			sum.Proposed++
		case errors.Is(perr, errHeld):
			sum.Held++
		default:
			sum.Refused++
			j.refusal(p, perr)
		}
	}
	return sum, err
}

var errHeld = errors.New("held by a draft of the same rule and key")

// propose proposes p unless an auto draft of its unit, rule and key is open
// or failed, or was sent, discarded or stored as a dry run less than
// RepeatAfter ago;
// a draft the job withdrew holds nothing back.
func (j *Job) propose(ctx context.Context, p outbox.Proposal, now time.Time) error {
	prior, err := j.Outbox.List(ctx, outbox.Filter{Unit: &p.Unit, Rule: p.Origin.Rule, Key: p.Origin.Key})
	if err != nil {
		return err
	}
	for _, d := range prior {
		if d.Origin.Kind != draft.OriginAuto {
			continue
		}
		switch {
		case slices.Contains(draft.Open, d.State), d.State == draft.Failed:
			return errHeld
		case d.State == draft.Dropped && d.Reason == ReasonMoot:
		case now.Sub(d.UpdatedAt) < j.RepeatAfter:
			return errHeld
		}
	}
	_, err = j.Outbox.Propose(ctx, p)
	if errors.Is(err, outbox.ErrDuplicate) {
		return errHeld
	}
	return err
}

func (j *Job) refusal(p outbox.Proposal, err error) {
	key := p.Origin.Rule + "|" + p.Unit.String() + "|" + p.Origin.Key
	if j.refused == nil {
		j.refused = map[string]string{}
	}
	if j.refused[key] == err.Error() {
		return
	}
	j.refused[key] = err.Error()
	if errors.Is(err, outbox.ErrDisabled) {
		j.logf("proposals: %s on %s not proposed: no enabled publisher", p.Origin.Rule, p.Unit)
		return
	}
	j.logf("proposals: %s on %s not proposed: %v", p.Origin.Rule, p.Unit, err)
}

func (j *Job) plugged(p unit.ProviderID) bool {
	return slices.ContainsFunc(j.Sources, func(s Source) bool { return s.Provider == p })
}

// withdraw discards each proposed auto draft of a plugin rule whose unit's
// card reports its closure posted (moot).
func (j *Job) withdraw(ctx context.Context) (int, error) {
	proposed, err := j.Outbox.List(ctx, outbox.Filter{States: []draft.State{draft.Held}})
	if err != nil {
		return 0, err
	}
	n := 0
	var errs []error
	for _, d := range proposed {
		if d.Origin.Kind != draft.OriginAuto || d.Origin.Workflow != "" || !j.plugged(d.Unit.Provider) || !j.enabled(d.Unit.Provider) {
			continue
		}
		reason, err := j.closed(ctx, d)
		if err == nil && reason != "" {
			_, err = j.Outbox.Withdraw(ctx, d.ID, reason)
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if reason != "" {
			n++
			j.logf("proposals: draft %d (%s on %s) discarded: %s", d.ID, d.Origin.Rule, d.Unit, reason)
		}
	}
	return n, errors.Join(errs...)
}

// closed is moot when d's unit's card reports its closure posted; a plugin's empty result is never read as moot,
// since it is empty until the plugin's read model is first built.
func (j *Job) closed(ctx context.Context, d draft.Draft) (string, error) {
	if j.Facts == nil {
		return "", nil
	}
	f, ok, err := j.Facts.UnitFacts(ctx, d.Unit)
	if err != nil || !ok || !f.ClosurePosted {
		return "", err
	}
	return ReasonMoot, nil
}

// fromSources is what each enabled plugin proposes on its own units, keeping
// of its Origin only the rule id: the job sets the key and, in Run, the
// plugin's configured approval.
func (j *Job) fromSources(ctx context.Context) ([]outbox.Proposal, []Withdrawal, error) {
	now := j.Clock.Now()
	var out []outbox.Proposal
	var withdrawals []Withdrawal
	var errs []error
	for _, src := range j.Sources {
		if src.Proposals == nil || !j.enabled(src.Provider) {
			continue
		}
		pass, err := src.Proposals(ctx, now)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s proposals: %w", src.Provider, err))
			continue
		}
		for _, w := range pass.Withdrawals {
			if w.Unit.Provider != src.Provider || w.Rule == "" {
				j.logf("proposals: %s withdrew %q on %s, outside its lane; ignored", src.Provider, w.Rule, w.Unit)
				continue
			}
			withdrawals = append(withdrawals, w)
		}
		for _, p := range pass.Proposals {
			rule := p.Origin.Rule
			switch {
			case p.Unit.Provider != src.Provider:
				j.logf("proposals: %s proposed on another provider's unit %s; dropped", src.Provider, p.Unit)
				continue
			case rule == "":
				j.logf("proposals: %s proposed under no rule on %s; dropped", src.Provider, p.Unit)
				continue
			}
			out = append(out, outbox.Proposal{
				Unit: p.Unit, Items: slices.Clone(p.Items), Kind: p.Kind, Target: p.Target, Text: p.Text, Reaction: p.Reaction,
				Purpose: p.Purpose, Delivery: p.Delivery, Effects: slices.Clone(p.Effects),
				Origin: draft.Origin{Kind: draft.OriginAuto, Rule: rule, Key: p.Unit.String()},
			})
		}
	}
	return out, withdrawals, errors.Join(errs...)
}

// withdrawNamed discards, as moot, each held auto draft of a plugin rule on
// a unit where the rule's plugin said it no longer calls for one.
func (j *Job) withdrawNamed(ctx context.Context, ws []Withdrawal) (int, error) {
	n := 0
	var errs []error
	for _, w := range ws {
		held, err := j.Outbox.List(ctx, outbox.Filter{Unit: &w.Unit, Rule: w.Rule, States: []draft.State{draft.Held}})
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, d := range held {
			if d.Origin.Kind != draft.OriginAuto || d.Origin.Workflow != "" {
				continue
			}
			if _, err := j.Outbox.Withdraw(ctx, d.ID, ReasonMoot); err != nil {
				errs = append(errs, err)
				continue
			}
			n++
			j.logf("proposals: draft %d (%s on %s) discarded: its rule no longer calls for it", d.ID, d.Origin.Rule, d.Unit)
		}
	}
	return n, errors.Join(errs...)
}
