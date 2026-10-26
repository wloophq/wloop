// Package proposals is the job that proposes what each enabled plugin
// proposes on its own units, such as GitHub's nudge. Every draft waits for
// its rule's approval policy. The drafts on GTD entries and Slack units come
// from the profile's workflows through draft.propose.
package proposals

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	coregtd "github.com/wloophq/wloop/internal/core/gtd"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/core/port"
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
	Discard(ctx context.Context, id draft.ID, reason string) (draft.Draft, error)
}

// Source is an enabled plugin's Proposals.
type Source struct {
	Provider  unit.ProviderID
	Proposals func(ctx context.Context, now time.Time) ([]outbox.Proposal, error)
}

type Job struct {
	Outbox Outbox
	// Facts reads a unit's card facts; a plugin rule's draft on a unit whose closure is posted is withdrawn. nil: none.
	Facts   coregtd.Facts
	Sources []Source
	// Enabled says whether a provider's plugin is enabled; nil: every one is.
	Enabled func(unit.ProviderID) bool
	// Approval is a rule's configured approval; nil: always.
	Approval func(rule string) draft.Approval
	// RenudgeAfter is how long a sent, discarded or observed draft of a key keeps the key quiet.
	RenudgeAfter time.Duration
	Clock        port.Clock
	Log          func(format string, args ...any) // ids, rules, kinds and units; never text

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

func (j *Job) approval(rule string) draft.Approval {
	if j.Approval == nil {
		return draft.Approval{Mode: draft.Wait}
	}
	return j.Approval(rule)
}

func (j *Job) logf(format string, args ...any) {
	if j.Log != nil {
		j.Log(format, args...)
	}
}

// Run is one pass: it withdraws the plugin rules' drafts on units whose
// closure is posted, then proposes what every plugin calls for now and no
// draft of the same rule and key holds back.
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
	props, err := j.fromSources(ctx)
	for _, p := range props {
		p.Policy = draft.Fixed(j.approval(p.Origin.Rule))
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
// or failed, or was sent, discarded or observed less than RenudgeAfter ago;
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
		case d.State == draft.Discarded && d.Reason == ReasonMoot:
		case now.Sub(d.UpdatedAt) < j.RenudgeAfter:
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
	proposed, err := j.Outbox.List(ctx, outbox.Filter{States: []draft.State{draft.Proposed}})
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
			_, err = j.Outbox.Discard(ctx, d.ID, reason)
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
// rule's configured approval.
func (j *Job) fromSources(ctx context.Context) ([]outbox.Proposal, error) {
	now := j.Clock.Now()
	var out []outbox.Proposal
	var errs []error
	for _, src := range j.Sources {
		if src.Proposals == nil || !j.enabled(src.Provider) {
			continue
		}
		ps, err := src.Proposals(ctx, now)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s proposals: %w", src.Provider, err))
			continue
		}
		for _, p := range ps {
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
	return out, errors.Join(errs...)
}
