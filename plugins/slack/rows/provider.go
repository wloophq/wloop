// Package slackrows serves Slack units as read model rows: threads and DM
// segments with their extraction verdict and open items.
package slackrows

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	dslack "github.com/wloophq/wloop/plugins/slack/domain"
	"github.com/wloophq/wloop/plugins/slack/items"
	"github.com/wloophq/wloop/plugins/slack/store"
	"github.com/wloophq/wloop/plugins/slack/unit"
)

// ActiveFor is how long after its last message an unwatched unit without
// open items keeps its row.
const ActiveFor = 14 * 24 * time.Hour

// Source is what the rows read: the store, the viewer the plugin learned
// and the host's items.
type Source struct {
	DB *store.DB
	// Viewer is the logged-in team and user; an error is an unknown viewer.
	Viewer func(ctx context.Context) (team, user string, err error)
	// Now nil is time.Now.
	Now func() time.Time
	// Items are the host ledger's items of units.
	Items func(ctx context.Context, units []unit.Unit) (map[unit.Unit][]items.Item, error)
	// Open are the host ledger's open items of every Slack unit.
	Open func(ctx context.Context) (map[unit.Unit][]items.Item, error)
}

// Provider is the Slack rows provider.
type Provider struct{ src Source }

func New(s Source) *Provider { return &Provider{src: s} }

func (p *Provider) now() time.Time {
	if p.src.Now != nil {
		return p.src.Now()
	}
	return time.Now()
}

func (p *Provider) viewer(ctx context.Context) (team, user string) {
	if p.src.Viewer == nil {
		return "", ""
	}
	team, user, err := p.src.Viewer(ctx)
	if err != nil {
		return "", ""
	}
	return team, user
}

// Listed are the threads and open DM segments of the objects watched or
// active within ActiveFor, and every Slack unit with an open item.
func (p *Provider) Listed(ctx context.Context) ([]unit.Unit, error) {
	db := p.src.DB.Slack()
	objs, err := db.RowObjects(ctx, p.now().Add(-ActiveFor))
	if err != nil {
		return nil, err
	}
	teams, err := p.teams(ctx, objs)
	if err != nil {
		return nil, err
	}
	var out []unit.Unit
	for _, o := range objs {
		team := teams[o.Conversation]
		if o.Kind == store.SlackThread {
			if team != "" {
				out = append(out, dslack.ThreadUnit(team, o.Conversation, o.TS))
			}
			continue
		}
		segs, err := p.src.DB.Segments().Of(ctx, dslack.Provider, o.Conversation)
		if err != nil {
			return nil, err
		}
		for _, s := range segs {
			if t := cmp.Or(s.Team, team); t != "" {
				out = append(out, dslack.Key{Team: t, Conversation: o.Conversation, Segment: s.ID}.Unit())
			}
		}
	}
	if p.src.Open != nil {
		open, err := p.src.Open(ctx)
		if err != nil {
			return nil, err
		}
		for u := range open {
			if u.Provider == dslack.Provider {
				out = append(out, u)
			}
		}
	}
	slices.SortFunc(out, unit.Compare)
	return slices.Compact(out), nil
}

// teams are the team of each object's conversation, the viewer's when the store has none.
func (p *Provider) teams(ctx context.Context, objs []store.ObjectRow) (map[string]string, error) {
	ids := make([]string, 0, len(objs))
	for _, o := range objs {
		ids = append(ids, o.Conversation)
	}
	slices.Sort(ids)
	convs, err := p.src.DB.Slack().Conversations(ctx, slices.Compact(ids))
	if err != nil {
		return nil, err
	}
	viewerTeam, _ := p.viewer(ctx)
	out := make(map[string]string, len(ids))
	for _, id := range ids {
		out[id] = viewerTeam
	}
	for _, c := range convs {
		out[c.ID] = cmp.Or(c.Team, viewerTeam)
	}
	return out, nil
}

// Input is what one unit's row derives from.
type Input struct {
	key      dslack.Key
	conv     store.ConversationRow
	object   store.ObjectRow
	msgs     []store.MessageRow
	state    store.ExtractionState
	hasState bool
	items    []items.Item
	users    map[string]store.UserRow
	viewer   string
	// host is the workspace's web host, such as "acme.slack.com"; "" when unknown.
	host string
}

// Load reads each unit's conversation, object, messages, extraction state,
// items and the users they name. A unit with neither a message nor an
// extraction state has no row.
func (p *Provider) Load(ctx context.Context, units []unit.Unit) (map[unit.Unit]*Input, error) {
	db := p.src.DB.Slack()
	_, viewer := p.viewer(ctx)
	states, err := p.src.DB.Extraction().States(ctx, units)
	if err != nil {
		return nil, err
	}
	its := map[unit.Unit][]items.Item{}
	if p.src.Items != nil {
		if its, err = p.src.Items(ctx, units); err != nil {
			return nil, err
		}
	}
	sample, err := db.Permalink(ctx)
	if err != nil {
		return nil, err
	}
	host := slackHost(sample)
	convs := map[string]store.ConversationRow{}
	users := map[string]store.UserRow{}
	var ids []string
	out := make(map[unit.Unit]*Input, len(units))
	for _, u := range units {
		k, err := dslack.ParseUnit(u)
		if err != nil {
			continue
		}
		in := &Input{key: k, items: its[u], users: users, viewer: viewer, host: host}
		in.state, in.hasState = states[u]
		if in.conv, err = p.conversation(ctx, convs, k.Conversation); err != nil {
			return nil, err
		}
		if in.object, in.msgs, err = p.facts(ctx, k); err != nil {
			return nil, err
		}
		if len(in.msgs) == 0 && !in.hasState {
			continue
		}
		ids = append(ids, in.conv.DMUser)
		for _, m := range in.msgs {
			ids = append(ids, m.User)
			ids = append(ids, m.Mentions...)
		}
		out[u] = in
	}
	ids = append(ids, viewer)
	slices.Sort(ids)
	got, err := db.Users(ctx, slices.DeleteFunc(slices.Compact(ids), func(id string) bool { return id == "" }))
	if err != nil {
		return nil, err
	}
	for _, r := range got {
		users[r.ID] = r
	}
	return out, nil
}

func (p *Provider) conversation(ctx context.Context, cache map[string]store.ConversationRow, id string) (store.ConversationRow, error) {
	if c, ok := cache[id]; ok {
		return c, nil
	}
	rows, err := p.src.DB.Slack().Conversations(ctx, []string{id})
	if err != nil {
		return store.ConversationRow{}, err
	}
	c := store.ConversationRow{ID: id}
	if len(rows) > 0 {
		c = rows[0]
	}
	cache[id] = c
	return c, nil
}

// facts are the unit's object and messages: a thread's replies, or the
// messages a DM segment's range anchors.
func (p *Provider) facts(ctx context.Context, k dslack.Key) (store.ObjectRow, []store.MessageRow, error) {
	db := p.src.DB.Slack()
	if k.Segment == "" {
		o, _, err := db.Object(ctx, dslack.ThreadObject(k.Conversation, k.TS))
		if err != nil {
			return o, nil, err
		}
		msgs, err := db.Messages(ctx, k.Conversation, store.MessageQuery{Thread: k.TS})
		return o, msgs, err
	}
	o, _, err := db.Object(ctx, dslack.DMObject(k.Conversation))
	if err != nil {
		return o, nil, err
	}
	s, err := p.src.DB.Segments().Segment(ctx, dslack.Provider, k.Conversation, k.Segment)
	switch {
	case err == nil:
	case errors.Is(err, store.ErrUnknownSegment):
		return o, nil, nil
	default:
		return o, nil, fmt.Errorf("segment %s: %w", k.Segment, err)
	}
	msgs, err := db.Messages(ctx, k.Conversation, store.MessageQuery{From: s.FromTS, To: s.ToTS})
	return o, msgs, err
}

// Derive is the row of in.
func Derive(in *Input) Row { return derive(in) }

// Key is the unit's key.
func (in *Input) Key() dslack.Key { return in.key }

// Conversation is the unit's conversation as stored.
func (in *Input) Conversation() store.ConversationRow { return in.conv }

// Messages are the unit's stored messages, ascending.
func (in *Input) Messages() []store.MessageRow { return in.msgs }

// State is the unit's last extraction; false before any.
func (in *Input) State() (store.ExtractionState, bool) { return in.state, in.hasState }

// Items are the unit's items in the host's ledger.
func (in *Input) Items() []items.Item { return in.items }
