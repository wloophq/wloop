// Package proposals is the job that proposes drafts on its own: a nudge for a stale waiting entry, a closing
// reply for a unit the user started and finished, a question for unassigned items, and what each enabled
// plugin proposes. Every draft waits for its rule's approval policy; nothing moves an entry.
package proposals

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	coregtd "github.com/wloophq/wloop/internal/core/gtd"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gtd"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// The core rules' ids, as Origin.Rule and as keys of the proposals config block.
const (
	RuleStalledNudge = "stalled-nudge"
	RuleClosure      = "closure"
	RuleAskOwner     = "ask-owner"
)

// ReasonAssigned is the discard reason of an ask-owner draft whose items got an owner first.
const ReasonAssigned = "assigned"

// ReasonMoot is the discard reason of a nudge or closure draft whose entry no longer calls for it.
const ReasonMoot = "moot"

var coreRules = []string{RuleStalledNudge, RuleClosure, RuleAskOwner}

// Outbox is what the job needs of the outbox service (*outbox.Service).
type Outbox interface {
	Propose(ctx context.Context, p outbox.Proposal) (draft.Draft, error)
	List(ctx context.Context, f outbox.Filter) ([]draft.Draft, error)
	Discard(ctx context.Context, id draft.ID, reason string) (draft.Draft, error)
}

// Entries is the entry store the job reads (coregtd.Store).
type Entries interface {
	Entries(ctx context.Context, q gtd.Query) ([]gtd.Entry, error)
	Entry(ctx context.Context, id gtd.ID) (gtd.Entry, error)
}

// Nudges records a sent nudge on its waiting entries (*coregtd.Service).
type Nudges interface {
	Nudged(ctx context.Context, ids []gtd.ID, u unit.Unit, at time.Time) error
}

// Items reads a unit's items (the items ledger).
type Items interface {
	Items(ctx context.Context, u unit.Unit) ([]items.Item, error)
}

// Source is an enabled plugin's Proposals; its provider gets no generic stalled nudge.
type Source struct {
	Provider  unit.ProviderID
	Proposals func(ctx context.Context, now time.Time) ([]outbox.Proposal, error)
}

type Job struct {
	Outbox  Outbox
	Entries Entries
	Nudges  Nudges
	// Facts reads a unit's card facts; a plugin rule's draft on a unit whose closure is posted is withdrawn. nil: none.
	Facts   coregtd.Facts
	Items   Items
	Sources []Source
	// Enabled says whether a provider's plugin is enabled; nil: every one is.
	Enabled func(unit.ProviderID) bool
	// Approval is a rule's configured approval; nil: always.
	Approval func(rule string) draft.Approval
	// StaleDays is how many working days a waiting entry waits before its nudge; 0 waits for Resurface only.
	StaleDays int
	// RenudgeAfter is how long a nudge, or a sent, discarded or observed draft of a key, keeps the key quiet.
	RenudgeAfter time.Duration
	Clock        port.Clock
	Log          func(format string, args ...any) // ids, rules, kinds and units; never text

	// mu makes each pass, its dedupe checks and its proposals, one step: the job is the daemon's only auto proposer.
	mu sync.Mutex
	// refused is the last refusal logged per rule key, so a refusal repeating every pass is logged once.
	refused map[string]string
	// stampedTo is the newest sent draft Stamp handled.
	stampedTo time.Time
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
		return draft.Approval{Mode: draft.Always}
	}
	return j.Approval(rule)
}

func (j *Job) logf(format string, args ...any) {
	if j.Log != nil {
		j.Log(format, args...)
	}
}

// Run is one pass: it discards the ask-owner drafts whose items got an owner, then proposes what every rule
// calls for now and no draft of the same rule and key holds back.
func (j *Job) Run(ctx context.Context) (Summary, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	now := j.Clock.Now()
	var sum Summary
	rd := &reader{items: j.Items, cache: map[unit.Unit][]items.Item{}}
	discarded, err := j.withdraw(ctx, rd)
	sum.Discarded = discarded
	if err != nil {
		return sum, err
	}
	open, err := j.Entries.Entries(ctx, gtd.Query{View: gtd.ViewOpen})
	if err != nil {
		return sum, err
	}
	var props []outbox.Proposal
	var errs []error
	add := func(ps []outbox.Proposal, err error) {
		props = append(props, ps...)
		if err != nil {
			errs = append(errs, err)
		}
	}
	add(j.stalled(ctx, rd, open, now))
	add(j.closures(ctx, rd, open))
	add(j.askOwner(ctx, rd, open, now))
	add(j.fromSources(ctx, now))
	for _, p := range props {
		a := j.approval(p.Origin.Rule)
		p.Approval = &a
		switch err := j.propose(ctx, p, now); {
		case err == nil:
			sum.Proposed++
		case errors.Is(err, errHeld):
			sum.Held++
		default:
			sum.Refused++
			j.refusal(p, err)
		}
	}
	return sum, errors.Join(errs...)
}

var errHeld = errors.New("held by a draft of the same rule and key")

// propose proposes p unless a draft of its unit, kind, rule and key is open, failed, a sent close, or was sent,
// discarded or observed less than RenudgeAfter ago; a draft the job withdrew (assigned, moot) holds nothing back.
func (j *Job) propose(ctx context.Context, p outbox.Proposal, now time.Time) error {
	prior, err := j.Outbox.List(ctx, outbox.Filter{Unit: &p.Unit, Rule: p.Origin.Rule, Key: p.Origin.Key})
	if err != nil {
		return err
	}
	for _, d := range prior {
		if d.Kind != p.Kind || d.Origin.Kind != draft.OriginAuto {
			continue
		}
		switch {
		case slices.Contains(draft.Open, d.State), d.State == draft.Failed, d.State == draft.Sent && d.Kind == draft.Close:
			return errHeld
		case d.State == draft.Discarded && (d.Reason == ReasonAssigned || d.Reason == ReasonMoot):
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
		j.logf("proposals: %s %s on %s not proposed: no enabled publisher", p.Origin.Rule, p.Kind, p.Unit)
		return
	}
	j.logf("proposals: %s %s on %s not proposed: %v", p.Origin.Rule, p.Kind, p.Unit, err)
}

// reader caches a pass's item reads by unit.
type reader struct {
	items Items
	cache map[unit.Unit][]items.Item
}

func (r *reader) of(ctx context.Context, u unit.Unit) ([]items.Item, error) {
	if its, ok := r.cache[u]; ok {
		return its, nil
	}
	its, err := r.items.Items(ctx, u)
	if err != nil {
		return nil, err
	}
	its = slices.Clone(its)
	slices.SortStableFunc(its, func(a, b items.Item) int {
		return cmp.Or(a.CreatedAt.Compare(b.CreatedAt), strings.Compare(a.ID, b.ID))
	})
	r.cache[u] = its
	return its, nil
}

// unitOf is the unit an entry belongs to: its first attached unit, else its first item's.
func unitOf(e gtd.Entry) (unit.Unit, bool) {
	if len(e.Units) > 0 {
		return e.Units[0], true
	}
	if len(e.Items) > 0 {
		return e.Items[0].Unit, true
	}
	return unit.Unit{}, false
}

func (j *Job) plugged(p unit.ProviderID) bool {
	return slices.ContainsFunc(j.Sources, func(s Source) bool { return s.Provider == p })
}

// stale says whether waiting entry e is due a nudge: past Resurface when set, else StaleDays working days
// after Since, and not nudged in the last RenudgeAfter.
func (j *Job) stale(e gtd.Entry, now time.Time) bool {
	w := e.Waiting
	if w == nil || e.State != gtd.Waiting || len(w.On) == 0 {
		return false
	}
	due := w.Resurface
	if due.IsZero() {
		if j.StaleDays <= 0 || w.Since.IsZero() {
			return false
		}
		due = gtd.AddWorkdays(w.Since, j.StaleDays, now.Location())
	}
	if due.After(now) {
		return false
	}
	return w.LastNudge.IsZero() || now.Sub(w.LastNudge) >= j.RenudgeAfter
}

// stalled is one nudge per person a stale waiting entry waits on, listing the entry's open items that person owes.
func (j *Job) stalled(ctx context.Context, rd *reader, open []gtd.Entry, now time.Time) ([]outbox.Proposal, error) {
	var out []outbox.Proposal
	var errs []error
	for _, e := range open {
		u, ok := unitOf(e)
		if !ok || !j.stale(e, now) || !j.enabled(u.Provider) || j.plugged(u.Provider) {
			continue
		}
		its, err := rd.of(ctx, u)
		if err != nil {
			errs = append(errs, fmt.Errorf("items of %s: %w", u, err))
			continue
		}
		held := heldItems(e, its)
		if len(held) == 0 {
			continue
		}
		for _, p := range e.Waiting.On {
			if !strings.Contains(p.Key, ":") {
				continue
			}
			owed := owedBy(p, held)
			out = append(out, outbox.Proposal{
				Unit: u, Items: ids(owed), Entry: string(e.ID), Kind: draft.Nudge,
				Text:   nudgeText(p, owed),
				Origin: draft.Origin{Kind: draft.OriginAuto, Rule: RuleStalledNudge, Key: string(e.ID) + ":" + p.Key},
			})
		}
	}
	return out, errors.Join(errs...)
}

// heldItems are the open items of its that entry e holds.
func heldItems(e gtd.Entry, its []items.Item) []items.Item {
	var out []items.Item
	for _, it := range its {
		if it.State == items.Open && slices.Contains(e.Items, gtd.ItemRef{Unit: it.Unit, Item: it.ID}) {
			out = append(out, it)
		}
	}
	return out
}

// owedBy are the items of its waiting on p; all of them when none names p (a waiting the user set by hand).
func owedBy(p items.Person, its []items.Item) []items.Item {
	keys := append([]string{p.Key}, p.Aliases...)
	var out []items.Item
	for _, it := range its {
		if slices.ContainsFunc(it.WaitingOn, func(w items.Person) bool { return w.Is(items.Identity{Keys: keys}) }) {
			out = append(out, it)
		}
	}
	if len(out) == 0 {
		return its
	}
	return out
}

// closures is one closing reply per close entry the user has not set aside, listing each item's resolution.
func (j *Job) closures(ctx context.Context, rd *reader, open []gtd.Entry) ([]outbox.Proposal, error) {
	var out []outbox.Proposal
	var errs []error
	for _, e := range open {
		if e.Kind != gtd.KindClose || e.State != gtd.Inbox && e.State != gtd.Next {
			continue
		}
		u, ok := unitOf(e)
		if !ok || !j.enabled(u.Provider) || j.plugged(u.Provider) {
			continue
		}
		its, err := rd.of(ctx, u)
		if err != nil {
			errs = append(errs, fmt.Errorf("items of %s: %w", u, err))
			continue
		}
		var done []items.Item
		for _, it := range its {
			if it.State == items.Resolved && it.Unit == u {
				done = append(done, it)
			}
		}
		if len(done) == 0 {
			continue
		}
		out = append(out, outbox.Proposal{
			Unit: u, Items: ids(done), Entry: string(e.ID), Kind: draft.Close, Text: closeText(done),
			Origin: draft.Origin{Kind: draft.OriginAuto, Rule: RuleClosure, Key: u.String()},
		})
	}
	return out, errors.Join(errs...)
}

// askOwner is one question per unit the user has a live, unmuted entry on, naming its open items with no
// assignee, nobody waited on and no basis that are a working day old.
func (j *Job) askOwner(ctx context.Context, rd *reader, open []gtd.Entry, now time.Time) ([]outbox.Proposal, error) {
	var units []unit.Unit
	for _, e := range open {
		if !e.Live() || e.Snooze.Mute {
			continue
		}
		for _, u := range e.Units {
			if !slices.Contains(units, u) && j.enabled(u.Provider) && !j.plugged(u.Provider) {
				units = append(units, u)
			}
		}
	}
	var out []outbox.Proposal
	var errs []error
	for _, u := range units {
		its, err := rd.of(ctx, u)
		if err != nil {
			errs = append(errs, fmt.Errorf("items of %s: %w", u, err))
			continue
		}
		orphans := unowned(its, u, now)
		if len(orphans) == 0 {
			continue
		}
		out = append(out, outbox.Proposal{
			Unit: u, Items: ids(orphans), Kind: draft.AskOwner, Text: askText(orphans),
			Origin: draft.Origin{Kind: draft.OriginAuto, Rule: RuleAskOwner, Key: u.String()},
		})
	}
	return out, errors.Join(errs...)
}

func unowned(its []items.Item, u unit.Unit, now time.Time) []items.Item {
	var out []items.Item
	for _, it := range its {
		if it.Unit != u || it.State != items.Open || len(it.Assignees)+len(it.WaitingOn) > 0 || cmp.Or(it.Basis, items.NoBasis) != items.NoBasis {
			continue
		}
		if it.CreatedAt.IsZero() || gtd.AddWorkdays(it.CreatedAt, 1, now.Location()).After(now) {
			continue
		}
		out = append(out, it)
	}
	return out
}

// withdraw discards each proposed auto draft its rule no longer calls for: an ask-owner draft one of whose items
// got an assignee, someone to wait on, or resolved (assigned); a stalled nudge whose entry is gone, not waiting,
// no longer waits on its person or holds none of its items open, a closure whose close entry is not open, and a
// plugin rule's draft on a unit whose card reports its closure posted (moot).
func (j *Job) withdraw(ctx context.Context, rd *reader) (int, error) {
	proposed, err := j.Outbox.List(ctx, outbox.Filter{States: []draft.State{draft.Proposed}})
	if err != nil {
		return 0, err
	}
	n := 0
	var errs []error
	for _, d := range proposed {
		if d.Origin.Kind != draft.OriginAuto || !j.enabled(d.Unit.Provider) {
			continue
		}
		var reason string
		var err error
		switch {
		case d.Origin.Rule == RuleAskOwner && d.Kind == draft.AskOwner:
			reason, err = j.assigned(ctx, rd, d)
		case d.Origin.Rule == RuleStalledNudge && d.Kind == draft.Nudge:
			reason, err = j.mootNudge(ctx, rd, d)
		case d.Origin.Rule == RuleClosure && d.Kind == draft.Close:
			reason, err = j.mootClosure(ctx, d)
		case !slices.Contains(coreRules, d.Origin.Rule):
			reason, err = j.closed(ctx, d)
		}
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

func (j *Job) assigned(ctx context.Context, rd *reader, d draft.Draft) (string, error) {
	its, err := rd.of(ctx, d.Unit)
	if err != nil {
		return "", fmt.Errorf("items of %s: %w", d.Unit, err)
	}
	if slices.ContainsFunc(its, func(it items.Item) bool {
		return slices.Contains(d.Items, it.ID) && (len(it.Assignees)+len(it.WaitingOn) > 0 || it.State != items.Open)
	}) {
		return ReasonAssigned, nil
	}
	return "", nil
}

// entryOf is the entry a draft serves; ok false when it is gone.
func (j *Job) entryOf(ctx context.Context, d draft.Draft) (gtd.Entry, bool, error) {
	e, err := j.Entries.Entry(ctx, gtd.ID(d.Entry))
	if errors.Is(err, coregtd.ErrNoEntry) {
		return gtd.Entry{}, false, nil
	}
	return e, err == nil, err
}

func (j *Job) mootNudge(ctx context.Context, rd *reader, d draft.Draft) (string, error) {
	e, ok, err := j.entryOf(ctx, d)
	switch {
	case err != nil:
		return "", err
	case !ok:
		return ReasonMoot, nil
	}
	person, _ := strings.CutPrefix(d.Origin.Key, d.Entry+":")
	if e.State != gtd.Waiting || e.Waiting == nil || !slices.ContainsFunc(e.Waiting.On, func(p items.Person) bool { return p.Key == person }) {
		return ReasonMoot, nil
	}
	its, err := rd.of(ctx, d.Unit)
	if err != nil {
		return "", fmt.Errorf("items of %s: %w", d.Unit, err)
	}
	if !slices.ContainsFunc(heldItems(e, its), func(it items.Item) bool { return slices.Contains(d.Items, it.ID) }) {
		return ReasonMoot, nil
	}
	return "", nil
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

func (j *Job) mootClosure(ctx context.Context, d draft.Draft) (string, error) {
	e, ok, err := j.entryOf(ctx, d)
	switch {
	case err != nil:
		return "", err
	case !ok || !e.Open():
		return ReasonMoot, nil
	}
	return "", nil
}

// fromSources is what each enabled plugin proposes, keeping of its Origin only the rule id: the job sets the
// kind, the key and, in Run, the rule's configured approval.
func (j *Job) fromSources(ctx context.Context, now time.Time) ([]outbox.Proposal, error) {
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
			case rule == "" || slices.Contains(coreRules, rule):
				j.logf("proposals: %s proposed under rule %q on %s; dropped", src.Provider, rule, p.Unit)
				continue
			}
			out = append(out, outbox.Proposal{
				Unit: p.Unit, Items: slices.Clone(p.Items), Kind: p.Kind, Target: p.Target, Text: p.Text, Reaction: p.Reaction,
				Origin: draft.Origin{Kind: draft.OriginAuto, Rule: rule, Key: p.Unit.String()},
			})
		}
	}
	return out, errors.Join(errs...)
}

// Sent records a sent nudge on the waiting entries it served: its Entry, else every live waiting entry of its
// unit. It is the outbox's OnSent hook.
func (j *Job) Sent(ctx context.Context, d draft.Draft) error {
	if d.Kind != draft.Nudge || d.State != draft.Sent || j.Nudges == nil {
		return nil
	}
	var ids []gtd.ID
	if d.Entry != "" {
		ids = []gtd.ID{gtd.ID(d.Entry)}
	}
	return j.Nudges.Nudged(ctx, ids, d.Unit, cmp.Or(d.Evidence.At, d.UpdatedAt))
}

// Stamp runs Sent over the sent nudges stored since the last Stamp, so a nudge sent by a service without the
// hook, or before a restart, still stamps its entries.
func (j *Job) Stamp(ctx context.Context) error {
	sent, err := j.Outbox.List(ctx, outbox.Filter{States: []draft.State{draft.Sent}})
	if err != nil {
		return err
	}
	j.mu.Lock()
	from := j.stampedTo
	j.mu.Unlock()
	newest := from
	var errs []error
	for _, d := range slices.Backward(sent) {
		if d.Kind != draft.Nudge || !d.UpdatedAt.After(from) {
			continue
		}
		if err := j.Sent(ctx, d); err != nil {
			errs = append(errs, fmt.Errorf("stamping draft %d: %w", d.ID, err))
		}
		if len(errs) == 0 && d.UpdatedAt.After(newest) {
			newest = d.UpdatedAt
		}
	}
	j.mu.Lock()
	if newest.After(j.stampedTo) {
		j.stampedTo = newest
	}
	j.mu.Unlock()
	return errors.Join(errs...)
}

func ids(its []items.Item) []string {
	out := make([]string, len(its))
	for i, it := range its {
		out[i] = it.ID
	}
	return out
}
