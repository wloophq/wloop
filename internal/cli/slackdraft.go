package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"

	slacksource "github.com/wloophq/wloop/internal/adapter/source/slack"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/draft"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/items"
	dslack "github.com/wloophq/wloop/internal/domain/slack"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	slackapi "github.com/wloophq/wloop/internal/slack"
	"github.com/wloophq/wloop/internal/slack/auth"
	"github.com/wloophq/wloop/internal/slack/send"
	"github.com/wloophq/wloop/internal/transport/mcp"
)

var errNoDraftTurn = errors.New("this session has no turn; slack_draft drafts on the unit of a turn sent to or started in this session")

type sessionBooks interface {
	BookOf(ctx context.Context, k turns.Key) (dturn.Book, bool, error)
}

type unitItems interface {
	Items(ctx context.Context, u unit.Unit) ([]items.Item, error)
}

// slackDrafts is the daemon's mcp.SlackDrafts: a draft on the unit of the
// calling session's newest turn, its origin read from the turns book.
type slackDrafts struct {
	sender *slackSender
	books  sessionBooks
	items  unitItems // nil checks no item id
	outbox *outbox.Service
	pub    *slacksource.Publisher
	// intents is the loaded registry whose drafts.allowed_effects bound a draft's
	// effects; nil allows none.
	intents func() *dintent.Registry
}

func (s *slackDrafts) Draft(ctx context.Context, handle string, a mcp.DraftArgs) (mcp.DraftResult, error) {
	if !s.sender.LoggedIn() {
		return mcp.DraftResult{}, send.ErrNotLoggedIn
	}
	if a.Text != "" {
		text, err := s.sender.kit.Send.Mentions(ctx, a.Text, func(user string) string { return "@{" + string(slackPlugin) + ":" + user + "}" })
		if err != nil {
			return mcp.DraftResult{}, err
		}
		a.Text = text
	}
	b, ok, err := s.books.BookOf(ctx, turns.Key{Session: handle})
	if err != nil {
		return mcp.DraftResult{}, err
	}
	var t *dturn.Turn
	if ok {
		t, ok = b.OfSession(handle)
	}
	onSlack := ok && b.Unit.Provider == slackPlugin
	if send.IsMessage(a.Target) {
		u, err := s.sender.messageUnit(ctx, a.Target)
		if err != nil {
			return mcp.DraftResult{}, err
		}
		if !onSlack || u != b.Unit {
			return s.draftOutside(ctx, u, a, sessionOrigin(b, t, u, draft.OriginTurn))
		}
	}
	switch {
	case onSlack:
	case !ok:
		return mcp.DraftResult{}, errNoDraftTurn
	case s.outbox.PublisherOf(draft.Draft{Unit: b.Unit, Target: a.Target}) != slackPlugin:
		return mcp.DraftResult{}, fmt.Errorf("this session's turn is on %s; slack_draft drafts outside a Slack unit only on a message ref or permalink target, "+
			"or on a channel or person target from a unit whose own plugin posts nothing", b.Unit.Label())
	}
	its := a.Items
	if len(its) == 0 {
		its = turns.ItemsOf(*t)
	}
	if err := s.checkItems(ctx, b.Unit, its); err != nil {
		return mcp.DraftResult{}, err
	}
	def := s.intentDef(string(t.Intent))
	if err := checkEffects(def, string(t.Intent), a.Effects); err != nil {
		return mcp.DraftResult{}, err
	}
	return s.propose(ctx, outbox.Proposal{Unit: b.Unit, Items: its, Task: turns.TaskOf(*t), Kind: draft.Kind(a.Kind),
		Target: a.Target, Text: a.Text, Reaction: a.Reaction, Effects: a.Effects,
		Origin: sessionOrigin(b, t, b.Unit, draft.OriginTurn)})
}

// draftOutside proposes a on u, the unit of its message target, which is
// not the unit of the session's turn: it answers none of that unit's items
// and declares no effect.
func (s *slackDrafts) draftOutside(ctx context.Context, u unit.Unit, a mcp.DraftArgs, origin draft.Origin) (mcp.DraftResult, error) {
	switch {
	case len(a.Items) > 0:
		return mcp.DraftResult{}, errors.New("items: a draft outside the turn's Slack unit answers none of its items")
	case len(a.Effects) > 0:
		return mcp.DraftResult{}, errors.New("effects: a draft outside the turn's Slack unit declares none")
	}
	return s.propose(ctx, outbox.Proposal{Unit: u, Kind: draft.Kind(a.Kind),
		Target: a.Target, Text: a.Text, Reaction: a.Reaction, Origin: origin})
}

func (s *slackDrafts) propose(ctx context.Context, p outbox.Proposal) (mcp.DraftResult, error) {
	d, err := s.outbox.Propose(ctx, p)
	if err != nil {
		if errors.Is(err, send.ErrNotLoggedIn) {
			s.sender.loggedOut()
		}
		return mcp.DraftResult{}, err
	}
	return s.result(ctx, d), nil
}

// sessionOrigin is the origin of a post the session of book b makes on u: its
// turn t, on b's unit when that is not u; a session with no turn has kind
// session.
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

func (s *slackDrafts) result(ctx context.Context, d draft.Draft) mcp.DraftResult {
	where, err := s.pub.Where(ctx, d)
	if err != nil {
		where = ""
	}
	return mcp.DraftResult{ID: int64(d.ID), Unit: d.Unit.Label(), Conversation: where, State: string(d.State)}
}

// intentDef is the loaded definition of intent; the zero Def, which allows
// no effect, when none is loaded.
func (s *slackDrafts) intentDef(intent string) dintent.Def {
	if s.intents != nil {
		if r := s.intents(); r != nil {
			if def, ok := r.Get(dintent.Name(intent)); ok {
				return def
			}
		}
	}
	return dintent.Def{}
}

// checkEffects refuses an effect the session's intent does not list in its
// drafts.allowed_effects: an agent never grants itself what sending does.
func checkEffects(def dintent.Def, intent string, effects []string) error {
	for _, e := range effects {
		if !slices.Contains(def.AllowedEffects, e) {
			return fmt.Errorf("the effect %s is not one the %s intent lists in drafts.allowed_effects", e, intent)
		}
	}
	return nil
}

func (s *slackDrafts) checkItems(ctx context.Context, u unit.Unit, ids []string) error {
	if len(ids) == 0 || s.items == nil {
		return nil
	}
	its, err := s.items.Items(ctx, u)
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

// The purposes of the posts a session makes itself.
const (
	sentPurpose      = "reply"
	reactedPurpose   = "react"
	unreactedPurpose = "unreact"
	editedPurpose    = "edit"
)

// sessionSends records each post a session makes itself as a sent draft, on
// the unit it landed in, tracked or not, with the session's turn as its
// origin; it never changes or fails the post.
type sessionSends struct {
	token   func() (auth.Token, error)
	client  *slackapi.Client
	landing slacksource.Landing
	books   sessionBooks
	outbox  *outbox.Service
	log     func(format string, args ...any)
}

func (r sessionSends) post(ctx context.Context, session string, req send.Request, sent send.Sent) {
	where := func(ctx context.Context, team string) (unit.Unit, error) {
		thread := cmp.Or(sent.ThreadTS, sent.TS)
		u, ok, err := r.landing.UnitOf(ctx, team, sent.Channel, sent.TS, sent.ThreadTS)
		if err != nil || ok {
			return u, err
		}
		return dslack.ThreadUnit(team, sent.Channel, thread), nil
	}
	r.record(ctx, session, where, outbox.Proposal{Purpose: sentPurpose, Delivery: draft.Message, Target: req.Target, Text: req.Text},
		draft.Evidence{Ref: send.Ref(sent.Channel, sent.TS), Permalink: sent.Permalink})
}

func (r sessionSends) reaction(ctx context.Context, session string, req send.Reaction, got send.Reacted) {
	purpose := reactedPurpose
	if req.Remove {
		purpose = unreactedPurpose
	}
	r.record(ctx, session, r.messageUnit(req.Message), outbox.Proposal{Purpose: purpose, Delivery: draft.Reaction, Target: req.Message,
		Reaction: got.Name}, draft.Evidence{Ref: send.Ref(got.Channel, got.TS), Permalink: got.Permalink})
}

func (r sessionSends) edit(ctx context.Context, session string, req send.Edit, got send.Edited) {
	r.record(ctx, session, r.messageUnit(req.Message), outbox.Proposal{Purpose: editedPurpose, Delivery: draft.EditDelivery, Target: req.Message,
		Text: req.Text, Replaced: got.Replaced}, draft.Evidence{Ref: send.Ref(got.Channel, got.TS), Permalink: got.Permalink})
}

func (r sessionSends) messageUnit(message string) func(context.Context, string) (unit.Unit, error) {
	return func(ctx context.Context, team string) (unit.Unit, error) {
		return r.landing.MessageUnit(ctx, r.client, team, message)
	}
}

// record keeps p as a sent draft on the unit where says the post landed in.
func (r sessionSends) record(ctx context.Context, session string, where func(ctx context.Context, team string) (unit.Unit, error),
	p outbox.Proposal, ev draft.Evidence) {
	ctx = context.WithoutCancel(ctx)
	tok, err := r.token()
	if err != nil {
		r.logf("slack_sent_unrecorded message=%s error=%v", ev.Ref, err)
		return
	}
	u, err := where(ctx, tok.Team)
	if err != nil {
		r.logf("slack_sent_unrecorded message=%s error=%v", ev.Ref, err)
		return
	}
	p.Unit, p.Origin = u, r.origin(ctx, session, u)
	d, err := r.outbox.RecordSent(ctx, p, ev)
	if err != nil {
		r.logf("slack_sent_unrecorded message=%s error=%v", ev.Ref, err)
		return
	}
	r.logf("slack_sent_recorded draft=%d unit=%s", d.ID, u)
}

// origin is the session's origin for a post on u; a book that cannot be
// read leaves the turn out.
func (r sessionSends) origin(ctx context.Context, session string, u unit.Unit) draft.Origin {
	if session == "" || r.books == nil {
		return draft.Origin{Kind: draft.OriginSession}
	}
	b, ok, err := r.books.BookOf(ctx, turns.Key{Session: session})
	if err != nil || !ok {
		return draft.Origin{Kind: draft.OriginSession}
	}
	t, ok := b.OfSession(session)
	if !ok {
		return draft.Origin{Kind: draft.OriginSession}
	}
	return sessionOrigin(b, t, u, draft.OriginSession)
}

func (r sessionSends) logf(format string, args ...any) {
	if r.log != nil {
		r.log(format, args...)
	}
}
