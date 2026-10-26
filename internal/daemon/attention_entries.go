package daemon

import (
	"context"
	"errors"
	"hash/fnv"
	"slices"
	"strconv"
	"strings"

	"github.com/wloophq/wloop/internal/api"
	coregtd "github.com/wloophq/wloop/internal/core/gtd"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gtd"
	dmessage "github.com/wloophq/wloop/internal/domain/message"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/readmodel/prrow"
)

// Drafts lists the outbox's drafts; *outbox.Service satisfies it.
type Drafts interface {
	List(ctx context.Context, f outbox.Filter) ([]draft.Draft, error)
}

const stampToken = "20060102T150405.000000000"

// notifiedDrafts are the drafts that notify: proposed, or failed and waiting on a retry.
var notifiedDrafts = []draft.State{draft.Proposed, draft.Failed}

// attention is e's notification, nil when it posts none: an Inbox entry notifies once, a draft of the Outbox
// once unless it serves an Inbox entry, whose id the draft then keys. A draft is never approved from a
// notification, which shows its text cut. Nothing of a disabled plugin notifies.
func (ev *entryViewer) attention(ctx context.Context, e gtd.Entry) *api.Attention {
	if e.Kind == gtd.KindDraft {
		return ev.draftAttention(ctx, e)
	}
	if e.State != gtd.Inbox || !ev.s.unitsEnabled(e.Units) {
		return nil
	}
	cat, token, a, ok := ev.inboxNotice(ctx, e)
	if !ok {
		return nil
	}
	if d, ok := ev.proposedFor(ctx, e.ID); ok {
		token = hashToken(token, draftPrefix+strconv.FormatInt(int64(d.ID), 10), d.UpdatedAt.UTC().Format(stampToken))
	}
	a.Category = string(cat)
	a.ID = api.NotificationID(cat, string(e.ID), token)
	return &a
}

// unitsEnabled says whether units hold a unit of an enabled plugin, or none at all.
func (s *Server) unitsEnabled(units []unit.Unit) bool {
	return len(units) == 0 || slices.ContainsFunc(units, func(u unit.Unit) bool { return s.pluginEnabled(u.Provider) })
}

func verbAction(v gtd.Verb) string { return "gtd:" + string(v) }

// inboxNotice is what an Inbox entry notifies; ok is false when it notifies nothing. A question's message and
// an entry derived from a pull request's items notify only through the row's notice, when the entry is the one
// that notice stands for (the question's message, the derived item of a round, a hold, a block or a waiting
// agent), so they notify what the row did and only once. Any other entry notifies what it says of itself.
func (ev *entryViewer) inboxNotice(ctx context.Context, e gtd.Entry) (api.Category, string, api.Attention, bool) {
	m, isMsg := ev.message(ctx, e)
	question := isMsg && m.Kind == dmessage.Question
	record := int64(-1)
	if question {
		record = ev.recordOf(ctx, m)
	}
	n, row, served, ok := ev.rowNotice(ctx, e)
	switch {
	case ok && n.Case == prrow.NoticeQuestion && question && n.Record == record:
		return api.QuestionCategory(record), n.Token, n.Attention, true
	case ok && n.Case != prrow.NoticeQuestion && e.Kind == gtd.KindAction && holdsItem(e, n.Case.Item()):
		a := n.Attention
		a.Actions = ev.asVerbs(e, row, a.Actions)
		if n.Case == prrow.NoticeHeld {
			return api.CategoryHeld, n.Token, a, true
		}
		return api.CategoryInbox, n.Token, a, true
	case served && (question || e.Kind == gtd.KindAction && e.Origin == gtd.OriginDerived):
		return "", "", api.Attention{}, false
	case isMsg:
		cat, token, a := messageNotice(e, m, record)
		return cat, token, a, true
	}
	a := api.Attention{Title: "In your Inbox", Subtitle: unitsLabel(e.Units), Body: e.Title, Actions: ev.inboxActions(ctx, e)}
	return api.CategoryInbox, hashToken(e.Title), a, true
}

// inboxActions are Draft for me when the entry's delegate runs an action that drafts headless, and Reply and
// Send reply when the entry takes a reply.
func (ev *entryViewer) inboxActions(ctx context.Context, e gtd.Entry) []api.AttentionAction {
	out := []api.AttentionAction{}
	if ev.s.draftsHeadless(ctx, ev.g, e) {
		out = append(out, api.AttentionAction{ID: api.AttentionDraftForMe, Title: "Draft for me"})
	}
	if ev.s.replyRefusal(e) == "" {
		out = append(out, api.AttentionAction{ID: api.AttentionReply, Title: "Reply", TextInput: true},
			api.AttentionAction{ID: api.AttentionSendReply, Title: "Send reply", TextInput: true})
	}
	return out
}

// draftsHeadless says whether e's delegate runs one enabled action that runs headless.
func (s *Server) draftsHeadless(ctx context.Context, g *coregtd.Service, e gtd.Entry) bool {
	b, ok := s.bridge(ctx, g, e, gtd.VerbDelegate)
	if !ok {
		return false
	}
	st, runs := b.runs()
	return runs && s.bridgeMode(st.ID, string(turn.ModeHeadless)) == nil
}

// rowNotice is the notice of e's first unit's row; served says the unit is a pull request the read model
// serves, ok that its row notifies.
func (ev *entryViewer) rowNotice(ctx context.Context, e gtd.Entry) (n prrow.Notice, row api.Row, served, ok bool) {
	if len(e.Units) == 0 || e.Units[0].Provider != unit.GitHub {
		return prrow.Notice{}, api.Row{}, false, false
	}
	rd, served, err := ev.s.readRow(ctx, e.Units[0])
	if err != nil {
		ev.s.logf("notifications: the row of %s: %v", e.Units[0].Label(), err)
	}
	if !served {
		return prrow.Notice{}, api.Row{}, false, false
	}
	n, ok = ev.s.deriverAt(rd.Base, rd.At).Notice(rd.Row, rd.In)
	return n, rd.Row, true, ok
}

func holdsItem(e gtd.Entry, prefix string) bool {
	return prefix != "" && slices.ContainsFunc(e.Items, func(r gtd.ItemRef) bool { return strings.HasPrefix(r.Item, prefix) })
}

// asVerbs names an action the entry's verb runs, as the bridge would pick it, by that verb: the entry's
// button then moves the entry as its verb does.
func (ev *entryViewer) asVerbs(e gtd.Entry, row api.Row, acts []api.AttentionAction) []api.AttentionAction {
	served := ev.s.servedSpecs()
	verbOf := map[string]gtd.Verb{}
	for _, sp := range served {
		if v := gtd.Verb(sp.Verb); slices.Contains(bridged, v) && !ev.g.Handles(e.Kind, v) {
			verbOf[sp.ID] = v
		}
	}
	out := make([]api.AttentionAction, 0, len(acts))
	for _, a := range acts {
		if v, ok := verbOf[a.ID]; ok {
			if st, runs := rankOffered(served, row, v).runs(); runs && st.ID == a.ID {
				a.ID = verbAction(v)
			}
		}
		out = append(out, a)
	}
	return out
}

func (ev *entryViewer) message(ctx context.Context, e gtd.Entry) (dmessage.Message, bool) {
	if e.Kind != gtd.KindMessage || ev.s.deps.Messages == nil {
		return dmessage.Message{}, false
	}
	m, ok, err := ev.s.deps.Messages.Store.ByEntry(ctx, string(e.ID))
	if err != nil {
		ev.s.logf("notifications: the message of %s: %v", e.ID, err)
	}
	return m, ok && err == nil
}

// recordOf is the turn record a question message asks from: its turn's last record, 0 when the turn holds
// none.
func (ev *entryViewer) recordOf(ctx context.Context, m dmessage.Message) int64 {
	if m.Unit.IsZero() || ev.s.deps.Messages.Books == nil {
		return 0
	}
	books, err := ev.s.deps.Messages.Books.Books(ctx, m.Unit)
	if err != nil {
		ev.s.logf("notifications: the turns of %s: %v", m.Unit.Label(), err)
		return 0
	}
	b, ok := books[m.Unit]
	if !ok {
		return 0
	}
	if last, ok := b.LastRecord(m.Origin.TurnSeq); ok {
		return last.ID
	}
	return 0
}

// messageNotice is a message entry's notification from the message alone.
func messageNotice(e gtd.Entry, m dmessage.Message, record int64) (api.Category, string, api.Attention) {
	a := api.Attention{Subtitle: unitsLabel(e.Units), Body: m.Text, Image: "ask"}
	if a.Subtitle == "" {
		a.Subtitle = m.Unit.Label()
	}
	dismiss := api.AttentionAction{ID: verbAction(gtd.VerbDismiss), Title: "Dismiss"}
	switch m.Kind {
	case dmessage.Question:
		a.Title = "Agent asks"
		a.Actions = append(choices(m), api.AttentionAction{ID: "answer:text", Title: "Reply", TextInput: true}, dismiss)
		return api.QuestionCategory(max(record, 0)), strconv.FormatInt(max(record, 0), 10), a
	case dmessage.Proposal:
		a.Title = "Proposal"
		a.Actions = append(choices(m), dismiss)
	default:
		a.Title = "Agent posts"
		a.Actions = []api.AttentionAction{{ID: "answer:text", Title: "Reply", TextInput: true}, dismiss}
	}
	return api.CategoryInbox, hashToken(strconv.FormatInt(m.ID, 10), m.Text), a
}

// choices are a message's one-tap answers, the recommended one first.
func choices(m dmessage.Message) []api.AttentionAction {
	var out []api.AttentionAction
	if m.Confirm {
		out = []api.AttentionAction{{ID: "answer:yes", Title: "Yes"}, {ID: "answer:no", Title: "No"}}
	} else {
		for _, o := range m.Options {
			out = append(out, api.AttentionAction{ID: "answer:option:" + o.ID, Title: o.Label})
		}
	}
	rec := "answer:option:" + m.Recommend
	if m.Confirm {
		rec = "answer:" + m.Recommend
	}
	if i := slices.IndexFunc(out, func(a api.AttentionAction) bool { return m.Recommend != "" && a.ID == rec }); i > 0 {
		r := out[i]
		out = append([]api.AttentionAction{r}, slices.Delete(out, i, i+1)...)
	}
	if len(out) > 0 && m.Recommend != "" && out[0].ID == rec {
		out[0].Title += " (recommended)"
	}
	return out
}

func unitsLabel(us []unit.Unit) string {
	if len(us) == 0 {
		return ""
	}
	return us[0].Label()
}

// hashToken keys a notification that lasts as long as parts, so a restart replaces it instead of adding
// another.
func hashToken(parts ...string) string {
	h := fnv.New64a()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return strconv.FormatUint(h.Sum64(), 36)
}

// openDrafts are the drafts that notify, read once per viewer.
func (ev *entryViewer) openDrafts(ctx context.Context) []draft.Draft {
	if ev.drafts != nil || ev.s.deps.Drafts == nil {
		return ev.drafts
	}
	ds, err := ev.s.deps.Drafts.List(ctx, outbox.Filter{States: notifiedDrafts})
	if err != nil {
		ev.s.logf("notifications: drafts: %v", err)
	}
	ev.drafts = ds
	if ev.drafts == nil {
		ev.drafts = []draft.Draft{}
	}
	return ev.drafts
}

// proposedFor is the newest proposed draft that serves entry id.
func (ev *entryViewer) proposedFor(ctx context.Context, id gtd.ID) (draft.Draft, bool) {
	var best draft.Draft
	found := false
	for _, d := range ev.openDrafts(ctx) {
		if d.Entry == string(id) && d.State == draft.Proposed && ev.s.pluginEnabled(d.Unit.Provider) && (!found || d.ID > best.ID) {
			best, found = d, true
		}
	}
	return best, found
}

// draftAttention is a draft entry's notification, nil when the draft serves an Inbox entry that notifies, which
// then notifies for both.
func (ev *entryViewer) draftAttention(ctx context.Context, e gtd.Entry) *api.Attention {
	n, err := strconv.ParseInt(strings.TrimPrefix(string(e.ID), draftPrefix), 10, 64)
	if err != nil {
		return nil
	}
	i := slices.IndexFunc(ev.openDrafts(ctx), func(d draft.Draft) bool { return int64(d.ID) == n })
	if i < 0 {
		return nil
	}
	d := ev.drafts[i]
	if !ev.s.pluginEnabled(d.Unit.Provider) || d.State == draft.Proposed && ev.servesInbox(ctx, d) {
		return nil
	}
	a := api.Attention{Category: string(api.CategoryOutbox), Subtitle: d.Unit.Label(), Body: draftBody(d)}
	a.Title = "Draft to approve"
	if d.State == draft.Failed {
		a.Title = "Draft failed"
	}
	a.Actions = []api.AttentionAction{{ID: "open", Title: "Open"}, {ID: verbAction(gtd.VerbDismiss), Title: "Discard"}}
	a.ID = api.NotificationID(api.CategoryOutbox, string(e.ID), d.UpdatedAt.UTC().Format(stampToken))
	return &a
}

func (ev *entryViewer) servesInbox(ctx context.Context, d draft.Draft) bool {
	if d.Entry == "" {
		return false
	}
	e, err := ev.g.Entry(ctx, gtd.ID(d.Entry))
	if err != nil && !errors.Is(err, coregtd.ErrNoEntry) {
		ev.s.logf("notifications: the entry of draft %d: %v", d.ID, err)
	}
	return err == nil && e.Kind != gtd.KindDraft && ev.attention(ctx, e) != nil
}

func draftBody(d draft.Draft) string {
	text := d.Text
	if d.Reaction != "" {
		text = ":" + d.Reaction + ":"
	}
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	if r := []rune(line); len(r) > 200 {
		line = string(r[:200]) + "…"
	}
	return d.Purpose + ": " + line
}
