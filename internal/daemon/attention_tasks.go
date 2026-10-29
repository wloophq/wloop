package daemon

import (
	"cmp"
	"context"
	"errors"
	"hash/fnv"
	"slices"
	"strconv"
	"strings"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/outbox"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gate"
	dmessage "github.com/wloophq/wloop/internal/domain/message"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/readmodel"
)

// Drafts lists the outbox's drafts; *outbox.Service satisfies it.
type Drafts interface {
	List(ctx context.Context, f outbox.Filter) ([]draft.Draft, error)
}

const stampToken = "20060102T150405.000000000"

// notifiedDrafts are the drafts that notify: proposed, or failed and waiting on a retry.
var notifiedDrafts = []draft.State{draft.Held, draft.Failed}

// attention is e's notification, nil when it posts none: an Inbox task notifies once, a task anywhere else once
// while a question, post or proposal gate is open on it, a draft of the Outbox once unless it serves a task that
// notifies, whose id the draft then keys. A draft is never approved from a notification, which shows its text
// cut. Nothing of a disabled plugin notifies.
func (ev *taskViewer) attention(ctx context.Context, e task.Task) *api.Attention {
	a := ev.notice(ctx, e)
	if a != nil {
		words := ev.s.loaded().Tasks.Verbs
		a.Actions = slices.DeleteFunc(a.Actions, func(act api.AttentionAction) bool {
			v, ok := strings.CutPrefix(act.ID, verbAction(""))
			return ok && !offers(words, e, task.Verb(v))
		})
		for i := range a.Actions {
			ev.s.wordInput(&a.Actions[i])
		}
	}
	return a
}

// inputNotices are the notices that word a text input's placeholder and button, by its action.
var inputNotices = map[string][2]string{
	api.AttentionReply:     {builtin.NoticeReplyPlaceholder, builtin.NoticeReplyButton},
	api.AttentionSendReply: {builtin.NoticeSendReplyPlaceholder, builtin.NoticeSendReplyButton},
}

// wordInput gives a text input action its placeholder and button, as the profile's notices word them.
func (s *Server) wordInput(a *api.AttentionAction) {
	if !a.TextInput {
		return
	}
	n, ok := inputNotices[a.ID]
	if !ok {
		n = [2]string{builtin.NoticeAnswerPlaceholder, builtin.NoticeAnswerButton}
	}
	a.Placeholder, a.Button = s.notice(n[0], nil), s.notice(n[1], nil)
}

// notice is e's notification before the profile's offers drop the buttons of verbs it does not offer.
func (ev *taskViewer) notice(ctx context.Context, e task.Task) *api.Attention {
	if e.Origin == task.OriginDraft {
		return ev.draftAttention(ctx, e)
	}
	if !ev.s.unitsEnabled(e.Units) {
		return nil
	}
	cat, token, a, ok := ev.unitNotice(ctx, e)
	if !ok {
		if _, gated := ev.message(ctx, e); e.Category != task.CategoryInbox && !gated {
			return nil
		}
		cat, token, a, ok = ev.inboxNotice(ctx, e)
	}
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

func verbAction(v task.Verb) string { return api.TaskActionPrefix + string(v) }

// unitNotice is what the own task of a unit the read model serves notifies while it is open and not deferred,
// which a mute holds: the unit's row notice, keyed on the task and on the gate the notice stands for (the
// round's plan gate, the block's trust gate, the question's record), so the task notifies what the row did
// and only once. ok is false when the task's first unit is not served, or its row notifies nothing.
func (ev *taskViewer) unitNotice(ctx context.Context, e task.Task) (api.Category, string, api.Attention, bool) {
	n, row, served, ok := ev.rowNotice(ctx, e)
	if !served || !ok || !e.Open() || e.Category == task.CategoryDeferred || !ev.unitsOwn(ctx, e) {
		return "", "", api.Attention{}, false
	}
	if n.Question {
		if m, isMsg := ev.message(ctx, e); !isMsg || m.Kind != dmessage.Question || ev.recordOf(ctx, m) != n.Record {
			return "", "", api.Attention{}, false
		}
		return api.QuestionCategory(n.Record), n.Token, n.Attention, true
	}
	a := n.Attention
	a.Actions = ev.asVerbs(e, row, a.Actions)
	token := n.Token
	if g, ok := ev.openGate(ctx, e, n.Gate); ok {
		token = "g" + strconv.FormatInt(int64(g.ID), 10)
	}
	if n.Held {
		return api.CategoryHeld, token, a, true
	}
	return api.CategoryInbox, token, a, true
}

// unitsOwn says whether e is its first unit's own task (task.UnitTask), the one task its row's notice stands
// for.
func (ev *taskViewer) unitsOwn(ctx context.Context, e task.Task) bool {
	ts, err := ev.g.TasksOf(ctx, e.Units[0])
	if err != nil {
		ev.s.logf("notifications: the tasks of %s: %v", e.Units[0].Label(), err)
		return false
	}
	own, ok := task.UnitTask(ts, e.Units[0])
	return ok && own.ID == e.ID
}

// openGate is e's oldest open gate of kind k; false when k is empty or none is open.
func (ev *taskViewer) openGate(ctx context.Context, e task.Task, k gate.Kind) (gate.Gate, bool) {
	if k == "" || ev.s.deps.GateReader == nil {
		return gate.Gate{}, false
	}
	gs, err := ev.s.deps.GateReader.OfTask(ctx, e.ID)
	if err != nil {
		ev.s.logf("notifications: the gates of %s: %v", e.ID, err)
		return gate.Gate{}, false
	}
	i := slices.IndexFunc(gs, func(g gate.Gate) bool { return g.Status == gate.Open && g.Kind == k })
	if i < 0 {
		return gate.Gate{}, false
	}
	return gs[i], true
}

// inboxNotice is what an Inbox task no row notice stands for notifies; ok is false when it notifies nothing. A
// question's message, and a task derived from a unit the read model serves, notify only through the row.
func (ev *taskViewer) inboxNotice(ctx context.Context, e task.Task) (api.Category, string, api.Attention, bool) {
	m, isMsg := ev.message(ctx, e)
	question := isMsg && m.Kind == dmessage.Question
	record := int64(-1)
	if question {
		record = ev.recordOf(ctx, m)
	}
	if _, _, served, _ := ev.rowNotice(ctx, e); served && (question || e.Origin == task.OriginDerived) {
		return "", "", api.Attention{}, false
	}
	if isMsg {
		cat, token, a := ev.s.messageNotice(e, m, record)
		return cat, token, a, true
	}
	a := api.Attention{Title: ev.s.notice(builtin.NoticeNotifyInbox, nil), Subtitle: unitsLabel(e.Units), Body: e.Title, Actions: ev.inboxActions(ctx, e)}
	return api.CategoryInbox, hashToken(e.Title), a, true
}

// inboxActions are Draft for me when the task's delegate runs an action that drafts headless, and Reply and
// Send reply when the task takes a reply, the one the reply verb's send picks first.
func (ev *taskViewer) inboxActions(ctx context.Context, e task.Task) []api.AttentionAction {
	out := []api.AttentionAction{}
	if ev.s.draftsHeadless(ctx, ev.g, e) {
		out = append(out, api.AttentionAction{ID: api.AttentionDraftForMe, Title: ev.s.notice(builtin.NoticeNotifyDraftForMe, nil)})
	}
	if ev.s.replyRefusal(e) == "" {
		replies := []api.AttentionAction{{ID: api.AttentionReply, Title: ev.s.notice(builtin.NoticeNotifyReply, nil), TextInput: true},
			{ID: api.AttentionSendReply, Title: ev.s.notice(builtin.NoticeNotifySendReply, nil), TextInput: true}}
		if ev.s.replySend(ev.s.replyVerb()) == sendNow {
			slices.Reverse(replies)
		}
		out = append(out, replies...)
	}
	return out
}

// draftsHeadless says whether e's delegate runs one enabled action that runs headless.
func (s *Server) draftsHeadless(ctx context.Context, g *coretasks.Service, e task.Task) bool {
	b, ok := s.bridge(ctx, g, e, task.VerbDelegate)
	if !ok {
		return false
	}
	st, runs := b.runs()
	return runs && s.bridgeMode(st.ID, string(turn.RunnerHeadless)) == nil
}

// rowNotice is the notice of e's first unit's row; served says the unit is the forge's and the read model
// serves it, ok that its row notifies.
func (ev *taskViewer) rowNotice(ctx context.Context, e task.Task) (n readmodel.Notice, row api.Row, served, ok bool) {
	if len(e.Units) == 0 || !ev.s.forge(e.Units[0]) {
		return readmodel.Notice{}, api.Row{}, false, false
	}
	rd, served, err := ev.s.readRow(ctx, e.Units[0])
	if err != nil {
		ev.s.logf("notifications: the row of %s: %v", e.Units[0].Label(), err)
	}
	if !served {
		return readmodel.Notice{}, api.Row{}, false, false
	}
	n, ok = rd.Notice()
	return n, rd.Row, true, ok
}

// asVerbs names an action the task's verb runs, as the bridge would pick it, by that verb: the task's
// button then moves the task as its verb does.
func (ev *taskViewer) asVerbs(e task.Task, row api.Row, acts []api.AttentionAction) []api.AttentionAction {
	served := ev.s.servedSpecs()
	verbOf := map[string]task.Verb{}
	for _, sp := range served {
		if v := task.Verb(sp.Verb); slices.Contains(bridged, v) && !ev.g.Handles(e, v) {
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

// message is the message of e's oldest open question, post or proposal gate.
func (ev *taskViewer) message(ctx context.Context, e task.Task) (dmessage.Message, bool) {
	if ev.s.deps.Messages == nil || ev.s.deps.GateReader == nil {
		return dmessage.Message{}, false
	}
	gs, err := ev.s.deps.GateReader.OfTask(ctx, e.ID)
	if err != nil {
		ev.s.logf("notifications: the gates of %s: %v", e.ID, err)
		return dmessage.Message{}, false
	}
	for _, g := range gs {
		if g.Status != gate.Open {
			continue
		}
		if m, ok := ev.s.messageOf(ctx, g); ok && m.Open() {
			return m, true
		}
		if m, ok := ev.missingMessage(ctx, g); ok {
			return m, true
		}
	}
	return dmessage.Message{}, false
}

// missingMessage is the question missing gate g asks, as a message of its turn worded by the gate; false
// for any other gate.
func (ev *taskViewer) missingMessage(ctx context.Context, g gate.Gate) (dmessage.Message, bool) {
	id, ok := turns.MissingTurn(g.Subject)
	if !ok || g.Kind != gate.KindQuestion || ev.s.deps.Messages.Books == nil {
		return dmessage.Message{}, false
	}
	books, err := ev.s.deps.Messages.Books.Books(ctx, g.Unit)
	if err != nil {
		ev.s.logf("notifications: the turns of %s: %v", g.Unit.Label(), err)
		return dmessage.Message{}, false
	}
	b := books[g.Unit]
	t, ok := b.ByID(id)
	if !ok {
		return dmessage.Message{}, false
	}
	return dmessage.Message{Unit: g.Unit, Kind: dmessage.Question, Text: ev.s.gateWords(g), Origin: dmessage.Origin{Kind: dmessage.OriginTurn, TurnSeq: t.Seq}}, true
}

// recordOf is the turn record a question message asks from: its turn's last record, 0 when the turn holds
// none.
func (ev *taskViewer) recordOf(ctx context.Context, m dmessage.Message) int64 {
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

// messageNotice is a message task's notification from the message alone; its dismiss and approve buttons run the
// verbs whose handlers drop and approve, worded as the profile words them.
func (s *Server) messageNotice(e task.Task, m dmessage.Message, record int64) (api.Category, string, api.Attention) {
	a := api.Attention{Subtitle: unitsLabel(e.Units), Body: m.Text, Image: "ask"}
	if a.Subtitle == "" {
		a.Subtitle = m.Unit.Label()
	}
	var dismiss []api.AttentionAction
	if e.Origin == task.OriginMessage {
		v, label := s.handlerVerb(handlerDrop)
		dismiss = []api.AttentionAction{{ID: verbAction(v), Title: label}}
	}
	switch m.Kind {
	case dmessage.Question:
		a.Title = s.notice(builtin.NoticeNotifyQuestion, nil)
		a.Actions = slices.Concat(s.choices(m), []api.AttentionAction{{ID: "answer:text", Title: s.notice(builtin.NoticeNotifyReply, nil), TextInput: true}}, dismiss)
		return api.QuestionCategory(max(record, 0)), strconv.FormatInt(max(record, 0), 10), a
	case dmessage.Proposal:
		a.Title = s.notice(builtin.NoticeNotifyProposal, nil)
		v, label := s.handlerVerb(handlerApprove)
		a.Actions = slices.Concat([]api.AttentionAction{{ID: verbAction(v), Title: label}}, dismiss)
	default:
		a.Title = s.notice(builtin.NoticeNotifyPost, nil)
		a.Actions = slices.Concat([]api.AttentionAction{{ID: "answer:text", Title: s.notice(builtin.NoticeNotifyReply, nil), TextInput: true}}, dismiss)
	}
	return api.CategoryInbox, hashToken(strconv.FormatInt(m.ID, 10), m.Text), a
}

// The handlers of the verbs a notification's buttons run besides a reply.
const (
	handlerDrop    = "drop"
	handlerApprove = "approve"
)

// handlerVerb is the verb whose handler is h and its label, as the profile words them; h itself when no verb runs it.
func (s *Server) handlerVerb(h string) (task.Verb, string) {
	l := s.loaded().Tasks
	for _, v := range builtin.TaskVerbs {
		if l.Handler(v) == h {
			return task.Verb(v), cmp.Or(l.Verbs[v].Label, bundledWords().tasks.Verbs[v].Label, v)
		}
	}
	return task.Verb(h), h
}

// choices are a message's one-tap answers, the recommended one first.
func (s *Server) choices(m dmessage.Message) []api.AttentionAction {
	var out []api.AttentionAction
	if m.Confirm {
		out = []api.AttentionAction{{ID: "answer:yes", Title: s.notice(builtin.NoticeNotifyYes, nil)}, {ID: "answer:no", Title: s.notice(builtin.NoticeNotifyNo, nil)}}
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
		out[0].Title = s.notice(builtin.NoticeNotifyRecommended, map[string]any{"Label": out[0].Title})
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
func (ev *taskViewer) openDrafts(ctx context.Context) []draft.Draft {
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

// proposedFor is the newest proposed draft that serves task id.
func (ev *taskViewer) proposedFor(ctx context.Context, id task.ID) (draft.Draft, bool) {
	var best draft.Draft
	found := false
	for _, d := range ev.openDrafts(ctx) {
		if d.Task == string(id) && d.State == draft.Held && ev.s.pluginEnabled(d.Unit.Provider) && (!found || d.ID > best.ID) {
			best, found = d, true
		}
	}
	return best, found
}

// draftAttention is a draft task's notification, nil when the draft serves an Inbox task that notifies, which
// then notifies for both.
func (ev *taskViewer) draftAttention(ctx context.Context, e task.Task) *api.Attention {
	n, err := strconv.ParseInt(strings.TrimPrefix(string(e.ID), draftPrefix), 10, 64)
	if err != nil {
		return nil
	}
	i := slices.IndexFunc(ev.openDrafts(ctx), func(d draft.Draft) bool { return int64(d.ID) == n })
	if i < 0 {
		return nil
	}
	d := ev.drafts[i]
	if !ev.s.pluginEnabled(d.Unit.Provider) || d.State == draft.Held && ev.servesInbox(ctx, d) {
		return nil
	}
	a := api.Attention{Category: string(api.CategoryOutbox), Subtitle: d.Unit.Label(), Body: draftBody(d)}
	a.Title = ev.s.notice(builtin.NoticeNotifyDraft, nil)
	if d.State == draft.Failed {
		a.Title = ev.s.notice(builtin.NoticeNotifyDraftFailed, nil)
	}
	drop, _ := ev.s.handlerVerb(handlerDrop)
	a.Actions = []api.AttentionAction{{ID: "open", Title: ev.s.notice(builtin.NoticeNotifyOpen, nil)}, {ID: verbAction(drop), Title: ev.s.notice(builtin.NoticeNotifyDiscard, nil)}}
	a.ID = api.NotificationID(api.CategoryOutbox, string(e.ID), d.UpdatedAt.UTC().Format(stampToken))
	return &a
}

func (ev *taskViewer) servesInbox(ctx context.Context, d draft.Draft) bool {
	if d.Task == "" {
		return false
	}
	e, err := ev.g.Task(ctx, task.ID(d.Task))
	if err != nil && !errors.Is(err, coretasks.ErrNoTask) {
		ev.s.logf("notifications: the task of draft %d: %v", d.ID, err)
	}
	return err == nil && e.Origin != task.OriginDraft && ev.attention(ctx, e) != nil
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
