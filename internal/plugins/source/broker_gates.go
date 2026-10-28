package source

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/items"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/pkg/plugin"
	"github.com/wloophq/wloop/pkg/plugin/manifest"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

type gates struct{ b *Broker }

// OpenGate proposes the plugin's draft, which the host's approval policy
// holds or releases. With a session, the host sets the draft's origin from
// the session's newest turn, and on that turn's unit its task and, when it
// names none, its items; it refuses an item that is not the unit's and an
// effect the turn's intent does not allow. A draft on another unit than
// the turn's answers none of its items and declares no effect. Without a
// session, the draft is on the plugin's own unit and its origin is the
// plugin's rule.
func (g gates) OpenGate(ctx context.Context, req src.GateRequest) (src.GateRef, error) {
	h := g.b.Hosted
	if h == nil || h.Drafts == nil {
		return src.GateRef{}, src.ErrUnsupported
	}
	d := req.Draft
	p := outbox.Proposal{Items: d.Items, Kind: draft.Kind(d.Kind), Target: d.Target, Text: d.Text, Reaction: d.Reaction, Effects: d.Effects,
		Purpose: d.Purpose, Delivery: draft.Delivery(d.Delivery)}
	if p.Kind != "" {
		p.Purpose, p.Delivery = "", ""
	}
	if req.Session == "" {
		u, err := g.b.Units.Own(g.b.ID, d.Unit)
		if err != nil {
			return src.GateRef{}, err
		}
		if d.Origin.Kind != string(draft.OriginAuto) || d.Origin.Rule == "" {
			return src.GateRef{}, errors.New("a draft a plugin proposes on its own names its rule as an auto origin")
		}
		p.Unit = u
		p.Origin = draft.Origin{Kind: draft.OriginAuto, Rule: d.Origin.Rule, Key: d.Origin.Key}
		return g.open(ctx, p)
	}
	if h.Sessions == nil {
		return src.GateRef{}, src.ErrUnsupported
	}
	u, err := g.b.Units.Of(d.Unit)
	if err != nil {
		return src.GateRef{}, err
	}
	p.Unit = u
	b, t, err := g.turnOf(ctx, req.Session)
	if errors.Is(err, errNoTurn) {
		return g.openOutside(ctx, p, req.Session)
	}
	if err != nil {
		return src.GateRef{}, err
	}
	if u != b.Unit {
		switch {
		case len(p.Items) > 0:
			return src.GateRef{}, errors.New("items: a draft outside the turn's unit answers none of its items")
		case len(p.Effects) > 0:
			return src.GateRef{}, errors.New("effects: a draft outside the turn's unit declares none")
		case u.Provider != unit.ProviderID(g.b.ID):
			return src.GateRef{}, fmt.Errorf("plugin %s drafts on %s, which is neither its unit nor the session's", g.b.ID, u)
		}
		p.Origin = sessionOrigin(b, t, u, draft.OriginTurn)
		return g.open(ctx, p)
	}
	if u.Provider != unit.ProviderID(g.b.ID) && h.Drafts.PublisherOf(draft.Draft{Unit: u, Target: p.Target}) != unit.ProviderID(g.b.ID) {
		return src.GateRef{}, fmt.Errorf("this session's turn is on %s; plugin %s drafts outside its own units only on a target it posts to, "+
			"from a unit whose own plugin posts nothing", u.Label(), g.b.ID)
	}
	if len(p.Items) == 0 {
		p.Items = turns.ItemsOf(*t)
	}
	if err := g.checkItems(ctx, u, p.Items); err != nil {
		return src.GateRef{}, err
	}
	allowed := h.allowedEffects(string(t.Intent))
	for _, e := range p.Effects {
		if !slices.Contains(allowed, e) {
			return src.GateRef{}, fmt.Errorf("the effect %s is not one the %s intent lists in drafts.allowed_effects", e, t.Intent)
		}
	}
	p.Task, p.Origin = turns.TaskOf(*t), sessionOrigin(b, t, u, draft.OriginTurn)
	return g.open(ctx, p)
}

// openOutside proposes p for a live session with no turn: on one of the
// plugin's own units, answering no item and declaring no effect, its
// origin the session.
func (g gates) openOutside(ctx context.Context, p outbox.Proposal, session string) (src.GateRef, error) {
	switch {
	case p.Unit.Provider != unit.ProviderID(g.b.ID):
		return src.GateRef{}, fmt.Errorf("%w: plugin %s drafts for it only on its own units", errNoTurn, g.b.ID)
	case len(p.Items) > 0:
		return src.GateRef{}, fmt.Errorf("%w: its drafts answer no items", errNoTurn)
	case len(p.Effects) > 0:
		return src.GateRef{}, fmt.Errorf("%w: its drafts declare no effects", errNoTurn)
	}
	if err := liveSession(ctx, g.b.Hosted.Sessions, session); err != nil {
		return src.GateRef{}, err
	}
	p.Origin = draft.Origin{Kind: draft.OriginSession}
	return g.open(ctx, p)
}

func (g gates) open(ctx context.Context, p outbox.Proposal) (src.GateRef, error) {
	d, err := g.b.Hosted.Drafts.Propose(ctx, p)
	if err != nil {
		return src.GateRef{}, err
	}
	return src.GateRef{Gate: "draft", Draft: int64(d.ID), State: string(d.State)}, nil
}

// liveSession refuses a session the host does not know or that has closed.
func liveSession(ctx context.Context, sessions SessionBooks, id string) error {
	if sessions == nil {
		return src.ErrUnsupported
	}
	s, ok, err := sessions.Session(ctx, id)
	switch {
	case err != nil:
		return err
	case !ok || !s.ClosedAt.IsZero():
		return fmt.Errorf("session %q is not live", id)
	}
	return nil
}

var errNoTurn = errors.New("the session has no turn")

// turnOf is the book of the session's turns and its newest turn.
func (g gates) turnOf(ctx context.Context, session string) (dturn.Book, *dturn.Turn, error) {
	b, ok, err := g.b.Hosted.Sessions.BookOf(ctx, session)
	if err != nil {
		return dturn.Book{}, nil, err
	}
	if !ok {
		return dturn.Book{}, nil, errNoTurn
	}
	t, ok := b.OfSession(session)
	if !ok {
		return dturn.Book{}, nil, errNoTurn
	}
	return b, t, nil
}

func (g gates) checkItems(ctx context.Context, u unit.Unit, ids []string) error {
	if len(ids) == 0 || g.b.Hosted.Items == nil {
		return nil
	}
	its, err := g.b.Hosted.Items.Query(ctx, coreitemsQuery(u))
	if err != nil {
		return err
	}
	for _, id := range ids {
		if !slices.ContainsFunc(its, func(it items.Item) bool { return it.ID == id }) {
			return fmt.Errorf("item %s is not an item of %s", id, u.Label())
		}
	}
	return nil
}

// sessionOrigin is the origin of a post the session of book b makes on u:
// its turn t, on b's unit when that is not u.
func sessionOrigin(b dturn.Book, t *dturn.Turn, u unit.Unit, kind draft.OriginKind) draft.Origin {
	if t == nil {
		return draft.Origin{Kind: draft.OriginSession}
	}
	o := draft.Origin{Kind: kind, TurnSeq: t.Seq, Intent: string(t.Intent), Workflow: t.Workflow}
	if b.Unit != u {
		o.Unit = b.Unit
	}
	return o
}

// RecordSent records a post one of the plugin's tools made at once, on the
// plugin's unit it landed in, as a sent draft whose origin is the session's
// newest turn, or the session itself when it has none or the call named
// no session, with a draft gate already approved by the agent, its rule the
// tool. A tool the manifest does not mark posts = "now", or a named
// session that is not live, is refused.
func (g gates) RecordSent(ctx context.Context, d src.Draft, session, tool string) (src.GateRef, error) {
	h := g.b.Hosted
	if h == nil || h.Drafts == nil || h.Sessions == nil {
		return src.GateRef{}, src.ErrUnsupported
	}
	if !postsNow(g.b.Manifest, tool) {
		return src.GateRef{}, fmt.Errorf("tool %s of plugin %s does not post at once", tool, g.b.ID)
	}
	if session != "" {
		if err := liveSession(ctx, h.Sessions, session); err != nil {
			return src.GateRef{}, err
		}
	}
	u, err := g.b.Units.Own(g.b.ID, d.Unit)
	if err != nil {
		return src.GateRef{}, err
	}
	origin := draft.Origin{Kind: draft.OriginSession}
	if b, t, err := g.turnOf(ctx, session); session != "" && err == nil {
		origin = sessionOrigin(b, t, u, draft.OriginSession)
	}
	p := outbox.Proposal{Unit: u, Purpose: d.Purpose, Delivery: draft.Delivery(d.Delivery), Target: d.Target, Text: d.Text, Reaction: d.Reaction,
		Replaced: d.Replaced, Origin: origin, Tool: tool}
	sent, err := h.Drafts.RecordSent(ctx, p, draft.Evidence(d.Evidence))
	if err != nil {
		return src.GateRef{}, err
	}
	return src.GateRef{Gate: "draft", Draft: int64(sent.ID), State: string(sent.State)}, nil
}

// postsNow says the manifest marks tool posts = "now".
func postsNow(m *manifest.Source, tool string) bool {
	return m != nil && slices.ContainsFunc(m.Tools, func(t manifest.Tool) bool { return t.Name == tool && t.Posts == "now" })
}

// RehomeDrafts moves the drafts of each move's From onto its To, both the
// plugin's own units, in one host transaction.
func (g gates) RehomeDrafts(ctx context.Context, moves []src.UnitMove) (int, error) {
	if g.b.Drafts == nil {
		return 0, src.ErrUnsupported
	}
	byFrom := make(map[unit.Unit]unit.Unit, len(moves))
	var errs []error
	for _, m := range moves {
		from, err := g.b.Units.Own(g.b.ID, m.From)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		to, err := g.b.Units.Own(g.b.ID, m.To)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		byFrom[from] = to
	}
	if err := errors.Join(errs...); err != nil {
		return 0, err
	}
	return g.b.Drafts.RehomeDrafts(ctx, byFrom)
}

// SetOverride sets the host overrides of one of the plugin's own units a
// plugin may set (port.PluginOverrides), compare-and-set over ifMatch in
// one host transaction. It takes no loop lock.
func (g gates) SetOverride(ctx context.Context, w plugin.Unit, set, ifMatch map[string]string) (bool, error) {
	if g.b.Overrides == nil {
		return false, src.ErrUnsupported
	}
	u, err := g.b.Units.Own(g.b.ID, w)
	if err != nil {
		return false, err
	}
	for n := range set {
		if !slices.Contains(port.PluginOverrides, n) {
			return false, fmt.Errorf("plugin %s sets override %q, which is not one of %v", g.b.ID, n, port.PluginOverrides)
		}
	}
	for n := range ifMatch {
		if !slices.Contains(port.Overrides, n) {
			return false, fmt.Errorf("plugin %s matches override %q, which is not one of %v", g.b.ID, n, port.Overrides)
		}
	}
	return g.b.Overrides.SetOverride(ctx, u, set, ifMatch)
}
