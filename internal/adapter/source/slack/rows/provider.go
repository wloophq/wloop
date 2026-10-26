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

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/api"
	coreitems "github.com/wloophq/wloop/internal/core/items"
	"github.com/wloophq/wloop/internal/domain/items"
	dslack "github.com/wloophq/wloop/internal/domain/slack"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/readmodel"
)

// ActiveFor is how long after its last message an unwatched unit without
// open items keeps its row.
const ActiveFor = 14 * 24 * time.Hour

// Source is what the rows read: the store and the viewer the plugin learned.
type Source struct {
	DB *sqlite.DB
	// Viewer is the logged-in team and user; an error is an unknown viewer.
	Viewer func(ctx context.Context) (team, user string, err error)
	// Now nil is time.Now.
	Now func() time.Time
	// Activity nil shows no activity.
	Activity readmodel.Activities
}

// Provider is the Slack rows provider.
type Provider struct{ src Source }

var (
	_ readmodel.Provider  = (*Provider)(nil)
	_ readmodel.Presenter = (*Provider)(nil)
)

func New(s Source) *Provider { return &Provider{src: s} }

func (p *Provider) Provider() unit.ProviderID { return dslack.Provider }

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
		if o.Kind == sqlite.SlackThread {
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
	open, err := p.src.DB.Items().Query(ctx, coreitems.Query{State: items.Open, Providers: []unit.ProviderID{dslack.Provider}})
	if err != nil {
		return nil, err
	}
	for _, it := range open {
		out = append(out, it.Unit)
	}
	slices.SortFunc(out, api.CompareUnits)
	return slices.Compact(out), nil
}

// teams are the team of each object's conversation, the viewer's when the store has none.
func (p *Provider) teams(ctx context.Context, objs []sqlite.ObjectRow) (map[string]string, error) {
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

// input is what one unit's row derives from.
type input struct {
	key      dslack.Key
	conv     sqlite.ConversationRow
	object   sqlite.ObjectRow
	msgs     []sqlite.MessageRow
	state    sqlite.ExtractionState
	hasState bool
	items    []items.Item
	users    map[string]sqlite.UserRow
	viewer   string
	// host is the workspace's web host, such as "acme.slack.com"; "" when unknown.
	host string
	// activity is nil when the loop spent nothing on the unit.
	activity *turn.Activity
}

func (p *Provider) activities(ctx context.Context) (map[unit.Unit]turn.Activity, error) {
	if p.src.Activity == nil {
		return nil, nil
	}
	return p.src.Activity.Activities(ctx, dslack.Provider)
}

// Load reads each unit's conversation, object, messages, extraction state,
// items and the users they name. A unit with neither a message nor an
// extraction state has no row.
func (p *Provider) Load(ctx context.Context, units []unit.Unit) (map[unit.Unit]any, error) {
	db := p.src.DB.Slack()
	_, viewer := p.viewer(ctx)
	states, err := p.src.DB.Extraction().States(ctx, units)
	if err != nil {
		return nil, err
	}
	its, err := p.src.DB.Items().Load(ctx, units)
	if err != nil {
		return nil, err
	}
	sample, err := db.Permalink(ctx)
	if err != nil {
		return nil, err
	}
	acts, err := p.activities(ctx)
	if err != nil {
		return nil, err
	}
	host := slackHost(sample)
	convs := map[string]sqlite.ConversationRow{}
	users := map[string]sqlite.UserRow{}
	var ids []string
	out := make(map[unit.Unit]any, len(units))
	for _, u := range units {
		k, err := dslack.ParseUnit(u)
		if err != nil {
			continue
		}
		in := &input{key: k, items: its[u], users: users, viewer: viewer, host: host}
		if a, ok := acts[u]; ok {
			in.activity = &a
		}
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

func (p *Provider) conversation(ctx context.Context, cache map[string]sqlite.ConversationRow, id string) (sqlite.ConversationRow, error) {
	if c, ok := cache[id]; ok {
		return c, nil
	}
	rows, err := p.src.DB.Slack().Conversations(ctx, []string{id})
	if err != nil {
		return sqlite.ConversationRow{}, err
	}
	c := sqlite.ConversationRow{ID: id}
	if len(rows) > 0 {
		c = rows[0]
	}
	cache[id] = c
	return c, nil
}

// facts are the unit's object and messages: a thread's replies, or the
// messages a DM segment's range anchors.
func (p *Provider) facts(ctx context.Context, k dslack.Key) (sqlite.ObjectRow, []sqlite.MessageRow, error) {
	db := p.src.DB.Slack()
	if k.Segment == "" {
		o, _, err := db.Object(ctx, dslack.ThreadObject(k.Conversation, k.TS))
		if err != nil {
			return o, nil, err
		}
		msgs, err := db.Messages(ctx, k.Conversation, sqlite.MessageQuery{Thread: k.TS})
		return o, msgs, err
	}
	o, _, err := db.Object(ctx, dslack.DMObject(k.Conversation))
	if err != nil {
		return o, nil, err
	}
	s, err := p.src.DB.Segments().Segment(ctx, dslack.Provider, k.Conversation, k.Segment)
	switch {
	case err == nil:
	case errors.Is(err, sqlite.ErrUnknownSegment):
		return o, nil, nil
	default:
		return o, nil, fmt.Errorf("segment %s: %w", k.Segment, err)
	}
	msgs, err := db.Messages(ctx, k.Conversation, sqlite.MessageQuery{From: s.FromTS, To: s.ToTS})
	return o, msgs, err
}

func (p *Provider) Derive(base readmodel.Base, in any, now time.Time) (api.Row, error) {
	x, ok := in.(*input)
	if !ok {
		return api.Row{}, fmt.Errorf("slackrows: input %T", in)
	}
	return derive(x, base, now), nil
}
