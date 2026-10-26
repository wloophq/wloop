package cli

import (
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
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
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
	switch {
	case ok && b.Unit.Provider == slackPlugin:
	case slacksource.IsPermalink(a.Target):
		return s.draftOutside(ctx, a)
	case !ok:
		return mcp.DraftResult{}, errNoDraftTurn
	default:
		return mcp.DraftResult{}, fmt.Errorf("this session's turn is on %s; slack_draft drafts outside a Slack unit only on a message permalink target", b.Unit.Label())
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
	d, err := s.outbox.Propose(ctx, outbox.Proposal{Unit: b.Unit, Items: its, Entry: turns.EntryOf(*t), Kind: draft.Kind(a.Kind), Purpose: a.Purpose,
		Delivery: draft.Delivery(a.Delivery), Target: a.Target, Text: a.Text, Reaction: a.Reaction, Effects: a.Effects,
		Origin: draft.Origin{Kind: draft.OriginTurn, TurnSeq: t.Seq, Intent: string(t.Intent), Workflow: t.Workflow}})
	if err != nil {
		if errors.Is(err, send.ErrNotLoggedIn) {
			s.sender.loggedOut()
		}
		return mcp.DraftResult{}, err
	}
	return s.result(ctx, d), nil
}

// draftOutside proposes a on the thread of its permalink target, a unit no
// turn of the session is on: as the session's, with no items or effects, so
// it waits for the user whatever an intent's policy says.
func (s *slackDrafts) draftOutside(ctx context.Context, a mcp.DraftArgs) (mcp.DraftResult, error) {
	switch {
	case len(a.Items) > 0:
		return mcp.DraftResult{}, errors.New("items: a draft outside the turn's Slack unit answers none of its items")
	case len(a.Effects) > 0:
		return mcp.DraftResult{}, errors.New("effects: a draft outside the turn's Slack unit declares none")
	}
	u, err := slackThreadOf(ctx, s.sender.kit, a.Target)
	if err != nil {
		return mcp.DraftResult{}, err
	}
	d, err := s.outbox.Propose(ctx, outbox.Proposal{Unit: u, Kind: draft.Kind(a.Kind), Purpose: a.Purpose, Delivery: draft.Delivery(a.Delivery),
		Target: a.Target, Text: a.Text, Reaction: a.Reaction, Origin: draft.Origin{Kind: draft.OriginSession}})
	if err != nil {
		if errors.Is(err, send.ErrNotLoggedIn) {
			s.sender.loggedOut()
		}
		return mcp.DraftResult{}, err
	}
	return s.result(ctx, d), nil
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

// sentPurpose is the purpose of a post a session made itself.
const sentPurpose = "reply"

// sessionSends records a session's slack_send_message post as a sent draft
// when it landed in a tracked unit; it never changes or fails the post.
type sessionSends struct {
	token   func() (auth.Token, error)
	landing slacksource.Landing
	outbox  *outbox.Service
	log     func(format string, args ...any)
}

func (r sessionSends) record(ctx context.Context, req send.Request, sent send.Sent) {
	ctx = context.WithoutCancel(ctx)
	tok, err := r.token()
	if err != nil {
		r.logf("slack_sent_unrecorded channel=%s ts=%s error=%v", sent.Channel, sent.TS, err)
		return
	}
	u, ok, err := r.landing.UnitOf(ctx, tok.Team, sent.Channel, sent.TS, slacksource.ThreadOf(sent.Permalink))
	if err != nil {
		r.logf("slack_sent_unrecorded channel=%s ts=%s error=%v", sent.Channel, sent.TS, err)
		return
	}
	if !ok {
		return
	}
	d, err := r.outbox.RecordSent(ctx, outbox.Proposal{Unit: u, Purpose: sentPurpose, Delivery: draft.Message, Target: req.Target, Text: req.Text},
		draft.Evidence{Ref: sent.TS, Permalink: sent.Permalink})
	if err != nil {
		r.logf("slack_sent_unrecorded channel=%s ts=%s error=%v", sent.Channel, sent.TS, err)
		return
	}
	r.logf("slack_sent_recorded draft=%d unit=%s", d.ID, u)
}

func (r sessionSends) logf(format string, args ...any) {
	if r.log != nil {
		r.log(format, args...)
	}
}
