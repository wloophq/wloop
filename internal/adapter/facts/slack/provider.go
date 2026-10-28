// Package factslack puts the Slack units the extraction stores behind the
// engine's Units, the Slack plugin's workflow facts (Module.Facts): every
// Slack unit a live entry is attached to, with its facts in slot slack.
package factslack

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/items"
	dslack "github.com/wloophq/wloop/internal/domain/slack"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

// Provider is the slack subject's facts provider. It wakes the sweep when
// the next unowned item turns a working day old.
type Provider struct {
	db *sqlite.DB
	// clock's location is where working days are counted.
	clock port.Clock
	// endedAfter is how long a unit stays quiet before it has ended.
	endedAfter time.Duration

	mu  sync.Mutex
	due time.Time
}

var (
	_ automation.Units  = (*Provider)(nil)
	_ automation.Dueer  = (*Provider)(nil)
	_ automation.Scoper = (*Provider)(nil)
)

func New(db *sqlite.DB, clock port.Clock, endedAfter time.Duration) *Provider {
	return &Provider{db: db, clock: clock, endedAfter: endedAfter}
}

// List is every Slack unit a live entry is attached to, in unit order.
func (p *Provider) List(ctx context.Context, _ unit.ProviderID) ([]unit.Unit, error) {
	es, err := p.db.Tasks().Tasks(ctx, task.Query{View: task.ViewOpen})
	if err != nil {
		return nil, err
	}
	var out []unit.Unit
	for _, e := range es {
		for _, u := range e.Units {
			if u.Provider == dslack.Provider {
				out = append(out, u)
			}
		}
	}
	slices.SortFunc(out, func(a, b unit.Unit) int {
		return cmp.Or(cmp.Compare(a.Provider, b.Provider), cmp.Compare(a.Key, b.Key))
	})
	out = slices.Compact(out)
	if err := p.keepDue(ctx, out); err != nil {
		return nil, err
	}
	return out, nil
}

// keepDue keeps for Due the earliest time after now an unowned item of
// units turns a working day old.
func (p *Provider) keepDue(ctx context.Context, units []unit.Unit) error {
	got, err := p.db.Items().Load(ctx, units)
	if err != nil {
		return err
	}
	now := p.clock.Now()
	var due time.Time
	for _, its := range got {
		for _, it := range its {
			if !unowned(it) || it.CreatedAt.IsZero() {
				continue
			}
			if at := task.AddWorkdays(it.CreatedAt, 1, now.Location()); at.After(now) && (due.IsZero() || at.Before(due)) {
				due = at
			}
		}
	}
	p.mu.Lock()
	p.due = due
	p.mu.Unlock()
	return nil
}

// Due is the earliest time the last List saw an unowned item turn a
// working day old.
func (p *Provider) Due() time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.due
}

// Facts are u's facts in slot slack; slack.settled_at is zero until Scope
// reads the workflow's drafts.
func (p *Provider) Facts(ctx context.Context, u unit.Unit) (workflow.Facts, error) {
	k, err := dslack.ParseUnit(u)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", automation.ErrUnitGone, err)
	}
	now := p.clock.Now()
	states, err := p.db.Extraction().States(ctx, []unit.Unit{u})
	if err != nil {
		return nil, err
	}
	st := states[u]
	kind, err := p.conversation(ctx, k)
	if err != nil {
		return nil, err
	}
	es, err := p.db.Tasks().TasksOf(ctx, u)
	if err != nil {
		return nil, err
	}
	got, err := p.db.Items().Load(ctx, []unit.Unit{u})
	if err != nil {
		return nil, err
	}
	newest, err := p.newestMessage(ctx, k)
	if err != nil {
		return nil, err
	}
	open, orphans, age := 0, 0, 0
	var updated time.Time
	for _, it := range got[u] {
		updated = later(updated, it.UpdatedAt)
		if it.State == items.Open {
			open++
		}
		if !unowned(it) {
			continue
		}
		orphans++
		if !it.CreatedAt.IsZero() {
			age = max(age, workdaysSince(it.CreatedAt, now, now.Location()))
		}
	}
	return workflow.Facts{
		"slack.channel":                k.Conversation,
		"slack.conversation":           kind,
		"slack.started_by_me":          st.StartedByViewer,
		"slack.closure_posted":         st.ClosurePosted || st.ClosureMarked,
		"slack.ended":                  st.ClosurePosted || st.ClosureMarked || p.quiet(newest, now),
		"slack.live_entry":             slices.ContainsFunc(es, func(e task.Task) bool { return liveOn(e, u) }),
		"slack.items_open":             open,
		"slack.items_unowned":          orphans,
		"slack.items_unowned_age_days": age,
		"slack.settled_at":             time.Time{},
		"slack.fingerprint":            u.String() + "|" + newest + "|" + updated.UTC().Format(time.RFC3339Nano),
	}, nil
}

// quiet says whether the unit whose newest stored message is newest has
// been quiet for endedAfter at now; false with no message stored.
func (p *Provider) quiet(newest string, now time.Time) bool {
	at := dslack.TSTime(newest)
	return !at.IsZero() && now.Sub(at) >= p.endedAfter
}

// liveOn says whether e is a live, unmuted entry attached to u.
func liveOn(e task.Task, u unit.Unit) bool {
	return e.Live() && !e.Snooze.Mute && slices.Contains(e.Units, u)
}

// Scope sets slack.settled_at to the newest settled draft workflow wf
// proposed on u.
func (p *Provider) Scope(ctx context.Context, wf string, u unit.Unit, f workflow.Facts) (workflow.Facts, error) {
	ds, err := p.db.Drafts().List(ctx, outbox.Filter{Unit: &u, Workflow: wf})
	if err != nil {
		return nil, err
	}
	out := maps.Clone(f)
	out["slack.settled_at"] = draft.SettledAt(ds, wf, u)
	return out, nil
}

// conversation is the kind of k's conversation: dm, group (a multi-person
// DM) or channel, public or private.
func (p *Provider) conversation(ctx context.Context, k dslack.Key) (string, error) {
	rows, err := p.db.Slack().Conversations(ctx, []string{k.Conversation})
	if err != nil {
		return "", err
	}
	kind := dslack.KindChannel
	if k.Segment != "" {
		kind = dslack.KindIM
	}
	if len(rows) > 0 && rows[0].Kind != "" {
		kind = dslack.ConversationKind(rows[0].Kind)
	}
	switch kind {
	case dslack.KindIM:
		return "dm", nil
	case dslack.KindMPIM:
		return "group", nil
	}
	return "channel", nil
}

// newestMessage is the ts of k's newest stored message, "" when none: a
// thread's replies, or the messages a DM segment's range anchors.
func (p *Provider) newestMessage(ctx context.Context, k dslack.Key) (string, error) {
	q := sqlite.MessageQuery{Thread: k.TS, Limit: 1, Newest: true}
	if k.Segment != "" {
		s, err := p.db.Segments().Segment(ctx, dslack.Provider, k.Conversation, k.Segment)
		switch {
		case errors.Is(err, sqlite.ErrUnknownSegment):
			return "", nil
		case err != nil:
			return "", err
		}
		q = sqlite.MessageQuery{From: s.FromTS, To: s.ToTS, Limit: 1, Newest: true}
	}
	msgs, err := p.db.Slack().Messages(ctx, k.Conversation, q)
	if err != nil || len(msgs) == 0 {
		return "", err
	}
	return msgs[len(msgs)-1].TS, nil
}

// unowned says whether it is open with no assignee, nobody waited on and no
// basis.
func unowned(it items.Item) bool {
	return it.State == items.Open && len(it.Assignees)+len(it.WaitingOn) == 0 && cmp.Or(it.Basis, items.NoBasis) == items.NoBasis
}

func later(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// workdaysSince is the most working days n with since moved n working days
// no later than now.
func workdaysSince(since, now time.Time, loc *time.Location) int {
	n := 0
	for !task.AddWorkdays(since, n+1, loc).After(now) {
		n++
	}
	return n
}
