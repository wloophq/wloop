// Package outbox keeps the drafts the loop proposes and publishes the ones
// the user, or their approval policy, approved.
package outbox

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/unit"
)

type Proposal struct {
	Unit                   unit.Unit
	Items                  []string
	Entry                  string
	Kind                   draft.Kind
	Target, Text, Reaction string
	Origin                 draft.Origin
	Approval               *draft.Approval // nil: resolved by Service.Policies from Origin
	// Purpose and Delivery are the draft's shape; "" derives them from Kind
	// (draft.ShapeOf), and a set Purpose with no Kind derives Kind
	// (draft.KindOf).
	Purpose  string
	Delivery draft.Delivery
	// Effects are applied once the draft is sent, names of builtin.Effects.
	Effects []string
	// ObservedFor is how long a settled draft of the same origin holds this
	// proposal from being proposed again; zero keeps Service.ObservedFor.
	ObservedFor time.Duration
	// HoldAutoSend keeps the draft for a person's approval whatever its
	// approval: its proposer's manifest sets auto_send = false.
	HoldAutoSend bool
}

type Filter struct {
	States    []draft.State
	Unit      *unit.Unit
	Entry     string
	Rule, Key string
	// Workflow keeps the drafts whose Origin.Workflow is it; "" any.
	Workflow string
}

// Effect applies one declared effect of a sent draft.
type Effect func(ctx context.Context, d draft.Draft) error

// Applier applies the Effects of a sent draft exactly once: each by its
// name, then the draft's EffectsAppliedAt, so a restart applies only those
// of drafts sent and not yet applied.
type Applier interface {
	Apply(ctx context.Context, d draft.Draft) error
	// Pending are the sent drafts whose effects are not applied yet.
	Pending(ctx context.Context) ([]draft.Draft, error)
}

type Store interface {
	Insert(ctx context.Context, d draft.Draft) (draft.Draft, error) // assigns ID
	Get(ctx context.Context, id draft.ID) (draft.Draft, error)      // ErrNotFound
	List(ctx context.Context, f Filter) ([]draft.Draft, error)      // newest first
	// Update writes d only while the stored state is from and the stored Rev is d.Rev, and returns
	// d with Rev bumped; ErrConflict otherwise.
	Update(ctx context.Context, d draft.Draft, from draft.State) (draft.Draft, error)
}

// Effects returns the publishing capability of a provider; an error wrapping
// ErrDisabled when its plugin is off or has no publisher.
type Effects func(p unit.ProviderID) (port.Effects, error)

// Evidence receives a sent draft's evidence for its items (wired to the items ledger, op "evidence").
type Evidence func(ctx context.Context, d draft.Draft) error

// Policies resolves the approval of a proposal on u whose Approval is nil.
type Policies interface {
	For(ctx context.Context, u unit.Unit, o draft.Origin) (draft.Approval, error)
}

// ItemReader reads a unit's items, for the confidence Would.Policy weighs.
type ItemReader interface {
	Items(ctx context.Context, u unit.Unit) ([]items.Item, error)
}

type Service struct {
	Store    Store
	Effects  Effects
	Policies Policies   // nil resolves every proposal to always
	Evidence Evidence   // nil none
	Items    ItemReader // nil: no item counts as low confidence
	Changes  port.Changes
	Clock    port.Clock
	Log      func(format string, args ...any) // ids, states and kinds only; never text or tokens
	// OnSent runs after a draft is stored as sent and its Evidence call (last-nudge stamps, plugin bookkeeping).
	OnSent []func(ctx context.Context, d draft.Draft) error
	// ObservedFor is how long an observed draft keeps an auto proposal of its key from being observed again;
	// zero keeps it for good.
	ObservedFor time.Duration
	// Applier applies a sent draft's Effects; nil applies none.
	Applier Applier
}

var (
	ErrNotFound     = errors.New("no such draft")
	ErrConflict     = errors.New("the draft changed state")
	ErrDisabled     = errors.New("the draft's plugin is disabled or has no publisher")
	ErrDuplicate    = errors.New("an open draft of the same rule and key exists")
	ErrNeedsConfirm = errors.New("the draft may have been posted; retry needs confirm")
	ErrInvalid      = errors.New("invalid draft")
)

// NotConfirmed is the Error of a draft Recover found in sending and Verify could not confirm.
const NotConfirmed = "not confirmed after a restart"

const mayHaveBeenPosted = "may have been posted"

func (s *Service) Propose(ctx context.Context, p Proposal) (draft.Draft, error) {
	now := s.Clock.Now()
	d := draft.Draft{
		Unit: p.Unit, Items: p.Items, Entry: p.Entry, Kind: p.Kind, Target: p.Target, Text: p.Text, Reaction: p.Reaction,
		Original: p.Text, Origin: p.Origin, State: draft.Proposed, CreatedAt: now, UpdatedAt: now,
		Purpose: p.Purpose, Delivery: p.Delivery, Effects: p.Effects,
	}
	if d.Kind == "" && d.Purpose != "" {
		d.Kind = draft.KindOf(d.Purpose, d.Delivery)
	}
	if err := checkContent(d); err != nil {
		return draft.Draft{}, err
	}
	approval, err := s.approval(ctx, p)
	if err != nil {
		return draft.Draft{}, err
	}
	if targetsOutside(d) && approval.Mode != draft.Observe {
		approval = always
	}
	d.Approval = approval
	if dup, ok, err := s.duplicate(ctx, d, now); err != nil || ok {
		return dup, err
	}
	if err := s.publishable(ctx, d); err != nil {
		return draft.Draft{}, err
	}
	d.Would = s.would(ctx, d)
	switch approval.Mode {
	case draft.Window:
		d.Deadline = now.Add(approval.Window)
	case draft.Observe:
		d.State, d.Reason, d.Would.Outcome = draft.Discarded, string(draft.Observe), draft.OutcomeObserved
	}
	d, err = s.Store.Insert(ctx, d)
	if err != nil {
		return draft.Draft{}, err
	}
	s.logf("draft %d proposed: %s on %s by %s, approval %s, would %s", d.ID, d.Kind, d.Unit, d.Origin.Kind, d.Approval.Mode, d.Would.Policy)
	if approval.Mode == draft.None {
		approved, err := s.markAuto(ctx, d)
		if err != nil {
			return d, err
		}
		return approved, nil
	}
	return d, nil
}

// duplicate is the open draft an auto proposal d repeats, with ErrDuplicate;
// under observe, an observed one younger than ObservedFor counts too.
func (s *Service) duplicate(ctx context.Context, d draft.Draft, now time.Time) (draft.Draft, bool, error) {
	if d.Origin.Kind != draft.OriginAuto {
		return draft.Draft{}, false, nil
	}
	states := draft.Open
	if d.Approval.Mode == draft.Observe {
		states = append(slices.Clone(states), draft.Discarded)
	}
	found, err := s.Store.List(ctx, Filter{States: states, Unit: &d.Unit, Rule: d.Origin.Rule, Key: d.Origin.Key})
	if err != nil {
		return draft.Draft{}, false, err
	}
	for _, o := range found {
		if o.Kind != d.Kind || o.Origin.Rule != d.Origin.Rule || o.Origin.Key != d.Origin.Key {
			continue
		}
		if o.State != draft.Discarded || o.Reason == string(draft.Observe) && s.observing(o, now) {
			return o, true, ErrDuplicate
		}
	}
	return draft.Draft{}, false, nil
}

func (s *Service) observing(o draft.Draft, now time.Time) bool {
	return s.ObservedFor <= 0 || now.Sub(o.UpdatedAt) < s.ObservedFor
}

func checkContent(d draft.Draft) error {
	switch {
	case d.Unit.IsZero():
		return fmt.Errorf("%w: no unit", ErrInvalid)
	case !d.Kind.Valid():
		return fmt.Errorf("%w: unknown kind %q", ErrInvalid, d.Kind)
	case d.Kind == draft.React && strings.TrimSpace(d.Reaction) == "":
		return fmt.Errorf("%w: a reaction needs an emoji", ErrInvalid)
	case d.Kind != draft.React && strings.TrimSpace(d.Text) == "":
		return fmt.Errorf("%w: empty text", ErrInvalid)
	}
	return nil
}

func (s *Service) approval(ctx context.Context, p Proposal) (draft.Approval, error) {
	if p.Approval != nil {
		if err := p.Approval.Validate(); err != nil {
			return draft.Approval{}, fmt.Errorf("%w: %w", ErrInvalid, err)
		}
		return *p.Approval, nil
	}
	if s.Policies == nil {
		return always, nil
	}
	a, err := s.Policies.For(ctx, p.Unit, p.Origin)
	if err == nil {
		err = a.Validate()
	}
	if err != nil {
		s.logf("draft approval of a %s proposal on %s resolved to always: %v", p.Kind, p.Unit, err)
		return always, nil
	}
	return a, nil
}

// targetsOutside reports an agent's draft aimed at another conversation or person than its unit's,
// which only the user may approve: an injected instruction must not post elsewhere as the user.
func targetsOutside(d draft.Draft) bool {
	t := strings.TrimSpace(d.Target)
	return (d.Origin.Kind == draft.OriginTurn || d.Origin.Kind == draft.OriginSession) && t != "" && !strings.EqualFold(t, d.Unit.Key)
}

var autoSendable = []draft.Kind{draft.Reply, draft.Nudge, draft.Close, draft.React}

func (s *Service) would(ctx context.Context, d draft.Draft) draft.Would {
	hold := func(reason string) draft.Would { return draft.Would{Policy: draft.WouldHold, Reason: reason} }
	switch {
	case targetsOutside(d):
		return hold("targets outside its unit")
	case d.Origin.Kind != draft.OriginTurn && d.Origin.Kind != draft.OriginAuto:
		return hold(fmt.Sprintf("proposed by %s", d.Origin.Kind))
	case !slices.Contains(autoSendable, d.Kind):
		return hold(fmt.Sprintf("a %s is never sent on its own", d.Kind))
	}
	if len(d.Items) > 0 && s.Items != nil {
		its, err := s.Items.Items(ctx, d.Unit)
		if err != nil {
			return hold("the items could not be read")
		}
		for _, it := range its {
			if it.Confidence == items.Low && slices.Contains(d.Items, it.ID) {
				return hold(fmt.Sprintf("item %s is low confidence", it.ID))
			}
		}
	}
	return draft.Would{Policy: draft.WouldSend, Reason: fmt.Sprintf("a %s from a %s over confident items", d.Kind, d.Origin.Kind)}
}

// publishable refuses d when its provider has no enabled publisher, or its Validator refuses d.
func (s *Service) publishable(ctx context.Context, d draft.Draft) error {
	fx, err := s.effects(d.Unit.Provider)
	if err != nil {
		return err
	}
	if v, ok := fx.(port.Validator); ok {
		if err := v.Validate(ctx, d); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalid, err)
		}
	}
	return nil
}

func (s *Service) effects(p unit.ProviderID) (port.Effects, error) {
	if s.Effects == nil {
		return nil, fmt.Errorf("%w: %s", ErrDisabled, p)
	}
	fx, err := s.Effects(p)
	switch {
	case err != nil && errors.Is(err, ErrDisabled):
		return nil, fmt.Errorf("provider %s: %w", p, err)
	case err != nil:
		return nil, fmt.Errorf("provider %s: %w: %w", p, ErrDisabled, err)
	case fx == nil:
		return nil, fmt.Errorf("%w: %s", ErrDisabled, p)
	}
	return fx, nil
}

// load returns draft id when its state is one of states and, when to is set, may move to to.
func (s *Service) load(ctx context.Context, id draft.ID, to draft.State, states ...draft.State) (draft.Draft, error) {
	d, err := s.Store.Get(ctx, id)
	if err != nil {
		return draft.Draft{}, err
	}
	if !slices.Contains(states, d.State) {
		return draft.Draft{}, fmt.Errorf("draft %d is %s: %w: %w", id, d.State, ErrConflict, draft.ErrTransition)
	}
	if to != "" {
		if err := d.Can(to); err != nil {
			return draft.Draft{}, fmt.Errorf("draft %d: %w: %w", id, ErrConflict, err)
		}
	}
	return d, nil
}

func (s *Service) save(ctx context.Context, d draft.Draft, from draft.State) (draft.Draft, error) {
	d.UpdatedAt = s.Clock.Now()
	got, err := s.Store.Update(ctx, d, from)
	if err != nil {
		return draft.Draft{}, fmt.Errorf("draft %d %s -> %s: %w", d.ID, from, d.State, err)
	}
	s.logf("draft %d %s -> %s (%s on %s)", d.ID, from, d.State, d.Kind, d.Unit)
	return got, nil
}

func setText(d *draft.Draft, text string) error {
	if d.Kind != draft.React && strings.TrimSpace(text) == "" {
		return fmt.Errorf("%w: empty text", ErrInvalid)
	}
	d.Text, d.Edited = text, text != d.Original
	return nil
}

func (s *Service) Edit(ctx context.Context, id draft.ID, text string) (draft.Draft, error) {
	d, err := s.load(ctx, id, "", draft.Proposed, draft.Failed)
	if err != nil {
		return draft.Draft{}, err
	}
	if err := setText(&d, text); err != nil {
		return draft.Draft{}, err
	}
	if d.Approval.Mode == draft.Window {
		d.Approval, d.Deadline = always, time.Time{}
	}
	return s.save(ctx, d, d.State)
}

func (s *Service) Approve(ctx context.Context, id draft.ID, text *string) (draft.Draft, error) {
	d, err := s.load(ctx, id, draft.Approved, draft.Proposed)
	if err != nil {
		return draft.Draft{}, err
	}
	if text != nil {
		if err := setText(&d, *text); err != nil {
			return draft.Draft{}, err
		}
	}
	if err := s.publishable(ctx, d); err != nil {
		return draft.Draft{}, err
	}
	d.State, d.Would.Outcome = draft.Approved, draft.OutcomeAsIs
	if d.Edited {
		d.Would.Outcome = draft.OutcomeEdited
	}
	return s.save(ctx, d, draft.Proposed)
}

func (s *Service) Discard(ctx context.Context, id draft.ID, reason string) (draft.Draft, error) {
	d, err := s.load(ctx, id, draft.Discarded, draft.Proposed, draft.Approved, draft.Failed)
	if err != nil {
		return draft.Draft{}, err
	}
	from := d.State
	if reason == "" {
		reason = "user"
	}
	d.State, d.Reason, d.Would.Outcome = draft.Discarded, reason, draft.OutcomeDiscarded
	return s.save(ctx, d, from)
}

// Retry: failed -> approved. A draft that failed as "not confirmed after a restart" or with an Error
// starting "may have been posted" needs confirm true, else ErrNeedsConfirm.
func (s *Service) Retry(ctx context.Context, id draft.ID, confirm bool) (draft.Draft, error) {
	d, err := s.load(ctx, id, draft.Approved, draft.Failed)
	if err != nil {
		return draft.Draft{}, err
	}
	if !confirm && (d.Error == NotConfirmed || strings.HasPrefix(d.Error, mayHaveBeenPosted)) {
		return draft.Draft{}, fmt.Errorf("draft %d: %w", id, ErrNeedsConfirm)
	}
	if err := s.publishable(ctx, d); err != nil {
		return draft.Draft{}, err
	}
	d.State, d.Error = draft.Approved, ""
	return s.save(ctx, d, draft.Failed)
}

func (s *Service) List(ctx context.Context, f Filter) ([]draft.Draft, error) {
	return s.Store.List(ctx, f)
}

// RecordSent stores a post already made outside the outbox (origin session) as sent.
func (s *Service) RecordSent(ctx context.Context, p Proposal, ev draft.Evidence) (draft.Draft, error) {
	now := s.Clock.Now()
	if p.Origin.Kind == "" {
		p.Origin.Kind = draft.OriginSession
	}
	if ev.At.IsZero() {
		ev.At = now
	}
	d := draft.Draft{
		Unit: p.Unit, Items: p.Items, Entry: p.Entry, Kind: p.Kind, Target: p.Target, Text: p.Text, Reaction: p.Reaction,
		Original: p.Text, Origin: p.Origin, State: draft.Sent, Evidence: ev, CreatedAt: now, UpdatedAt: now, SendingAt: now,
	}
	if err := checkContent(d); err != nil {
		return draft.Draft{}, err
	}
	d, err := s.Store.Insert(ctx, d)
	if err != nil {
		return draft.Draft{}, err
	}
	s.logf("draft %d recorded sent: %s on %s by %s", d.ID, d.Kind, d.Unit, d.Origin.Kind)
	return d, s.afterSent(ctx, d)
}

func (s *Service) afterSent(ctx context.Context, d draft.Draft) error {
	var errs []error
	if s.Evidence != nil {
		if err := s.Evidence(ctx, d); err != nil {
			s.logf("draft %d: its evidence was not recorded", d.ID)
			errs = append(errs, fmt.Errorf("evidence of draft %d: %w", d.ID, err))
		}
	}
	for _, hook := range s.OnSent {
		if err := hook(ctx, d); err != nil {
			s.logf("draft %d: an after-send hook failed", d.ID)
			errs = append(errs, fmt.Errorf("after sending draft %d: %w", d.ID, err))
		}
	}
	return errors.Join(errs...)
}

// Send is the sender pass: every approved draft -> sending -> sent | failed, one Publish each, no retry.
func (s *Service) Send(ctx context.Context) (sent, failed int, err error) {
	approved, err := s.Store.List(ctx, Filter{States: []draft.State{draft.Approved}})
	if err != nil {
		return 0, 0, err
	}
	var errs []error
	for _, d := range slices.Backward(approved) {
		ok, err := s.sendOne(ctx, d)
		switch {
		case errors.Is(err, errClaimLost):
			continue
		case ok:
			sent++
		case err == nil:
			failed++
		}
		if err != nil {
			errs = append(errs, err)
		}
	}
	return sent, failed, errors.Join(errs...)
}

var errClaimLost = errors.New("another pass or decision took the draft")

// sendOne reports whether d was sent; a nil error with false means d is now failed.
func (s *Service) sendOne(ctx context.Context, d draft.Draft) (bool, error) {
	fx, fxErr := s.effects(d.Unit.Provider)
	d.State, d.SendingAt = draft.Sending, s.Clock.Now()
	d, err := s.save(ctx, d, draft.Approved)
	if errors.Is(err, ErrConflict) {
		return false, errClaimLost
	}
	if err != nil {
		return false, err
	}
	if fxErr != nil {
		return false, s.fail(ctx, d, fxErr.Error())
	}
	ev, pubErr := fx.Publish(ctx, d)
	if pubErr != nil {
		return false, s.fail(ctx, d, pubErr.Error())
	}
	return s.settleSent(ctx, d, ev)
}

func (s *Service) fail(ctx context.Context, d draft.Draft, reason string) error {
	d.State, d.Error = draft.Failed, reason
	_, err := s.save(ctx, d, draft.Sending)
	return err
}

// settleSent stores d as sent with ev and runs the after-send hooks; stored reports the first.
func (s *Service) settleSent(ctx context.Context, d draft.Draft, ev draft.Evidence) (stored bool, err error) {
	if ev.At.IsZero() {
		ev.At = s.Clock.Now()
	}
	d.State, d.Evidence = draft.Sent, ev
	saved, err := s.save(ctx, d, draft.Sending)
	if errors.Is(err, ErrConflict) {
		s.logf("draft %d was published but changed state meanwhile", d.ID)
	}
	if err != nil {
		return false, err
	}
	return true, s.afterSent(ctx, saved)
}

// Recover runs once at daemon start: every sending draft -> Verify -> sent | failed;
// a draft whose provider answers ErrDisabled stays sending for the next Recover.
func (s *Service) Recover(ctx context.Context) error {
	stuck, err := s.Store.List(ctx, Filter{States: []draft.State{draft.Sending}})
	if err != nil {
		return err
	}
	var errs []error
	for _, d := range slices.Backward(stuck) {
		fx, err := s.effects(d.Unit.Provider)
		if err != nil {
			s.logf("draft %d stays sending: its provider %s is off", d.ID, d.Unit.Provider)
			continue
		}
		ev, found, verr := fx.Verify(ctx, d, d.SendingAt)
		switch {
		case verr != nil && strings.HasPrefix(verr.Error(), mayHaveBeenPosted):
			err = s.fail(ctx, d, verr.Error())
		case verr != nil || !found:
			err = s.fail(ctx, d, NotConfirmed)
		default:
			_, err = s.settleSent(ctx, d, ev)
		}
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// EvidenceOps are the items ledger ops that record sent draft d on each of its items;
// the caller applies them with provenance items.Effects.
func EvidenceOps(d draft.Draft) []items.Op {
	anchor := unit.Anchor{Unit: d.Unit, Ref: fmt.Sprintf("draft:%d:%s", d.ID, d.Kind), Permalink: d.Evidence.Permalink, At: d.Evidence.At}
	ops := make([]items.Op, 0, len(d.Items))
	for _, id := range d.Items {
		ops = append(ops, items.Op{Op: items.OpEvidence, Unit: d.Unit, Item: id, Anchor: anchor,
			Evidence: &items.Evidence{Kind: items.EvidenceSentDraft, Anchor: anchor}})
	}
	return ops
}

// EvidenceTo is an Evidence that applies a sent draft's EvidenceOps through apply, with provenance effects.
func EvidenceTo(apply func(ctx context.Context, prov items.Provenance, ops []items.Op) error) Evidence {
	return func(ctx context.Context, d draft.Draft) error {
		ops := EvidenceOps(d)
		if len(ops) == 0 {
			return nil
		}
		return apply(ctx, items.Effects, ops)
	}
}

func (s *Service) logf(format string, args ...any) {
	if s.Log != nil {
		s.Log(format, args...)
	}
}
