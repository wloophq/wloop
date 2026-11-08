package daemon

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/outbox"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

// replyRefusal is why the user cannot reply on e, "" when they can.
func (s *Server) replyRefusal(e task.Task) string {
	switch {
	case s.deps.Outbox == nil:
		return errOutboxUnavailable.Error()
	case len(e.Units) == 0:
		return "the task has no conversation to reply in"
	case !aboutUnits(e):
		return "a project task takes no reply"
	}
	return ""
}

// handlerReply is the verb handler that writes the composer's reply.
const handlerReply = "reply"

// replies says whether v is a verb whose handler writes a reply.
func (s *Server) replies(v task.Verb) bool {
	return s.loaded().Tasks.Handler(string(v)) == handlerReply
}

// replySend is what a reply of verb v does when it names no send, as the profile says, "draft" by default;
// "" for a verb whose handler is not reply.
func (s *Server) replySend(v string) string {
	if !s.replies(task.Verb(v)) {
		return ""
	}
	return cmp.Or(s.loaded().Tasks.Verbs[v].Send, sendDraft)
}

// The reply sends: sendNow sends a reply at once, sendDraft holds it for approval.
const (
	sendNow   = "now"
	sendDraft = "draft"
)

// replyVerb is the profile's verb whose handler is reply, "" when none is.
func (s *Server) replyVerb() string {
	for _, v := range s.verbOrder() {
		if s.replies(task.Verb(v)) {
			return v
		}
	}
	return ""
}

// runReply holds the reply the task's args carry as a user draft on its first unit, for the task and its
// items there; send "now" approves it at once and "draft" holds it, and with no send the verb's profile
// send says which.
func (s *Server) runReply(ctx context.Context, w http.ResponseWriter, g *coretasks.Service, e task.Task, v task.Verb, req api.TaskRequest, q requester) {
	if why := s.replyRefusal(e); why != "" {
		writeTasksError(w, fmt.Errorf("%w: %s", coretasks.ErrVerb, why))
		return
	}
	send, err := sendArg(cmp.Or(req.Args["send"], s.replySend(string(v))))
	if err != nil {
		writeTasksError(w, err)
		return
	}
	broadcast, err := broadcastArg(req.Args)
	if err != nil {
		writeTasksError(w, err)
		return
	}
	c := replyDraft(e)
	c.Kind, c.Text, c.Reaction, c.Broadcast, c.Send, c.Source = req.Args["kind"], req.Args["text"], req.Args["reaction"], broadcast, send, req.Source
	d, err := s.writeUserDraft(ctx, c, q)
	if err != nil {
		writeTasksError(w, taskDraftError(err))
		return
	}
	s.recordVerb(ctx, g, e, q.task(coretasks.Request{Verb: v, Source: req.Source}))
	now, err := g.Task(ctx, e.ID)
	if err != nil {
		now = e
	}
	view, err := s.taskViews(ctx, g, false).view(ctx, now)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, api.TaskResult{Task: view, Lines: []string{s.userDraftLine(d)}})
}

// replyDraft is the draft a reply on e writes: on its first unit, for the task and its items there.
func replyDraft(e task.Task) api.DraftCreate {
	u := e.Units[0]
	c := api.DraftCreate{Unit: u, Task: string(e.ID)}
	for _, ref := range e.Items {
		if ref.Unit == u {
			c.Items = append(c.Items, ref.Item)
		}
	}
	return c
}

// replyBroadcast is the broadcast a reply the user writes on e would take, as its publisher's Placer places it: "on"
// or "off" in a thread that can also post to its conversation, "" otherwise or when the publisher cannot tell. Each
// answer is kept for draftWhereRetry by the draft's unit and items, so a detail refreshed often asks the publisher
// once a minute; one lookup is bounded as a draft's destination is. A failure is kept too, unless the page's deadline
// cut it short: that one says nothing about the publisher.
func (s *Server) replyBroadcast(ctx context.Context, e task.Task) string {
	if s.replyRefusal(e) != "" || s.replyVerb() == "" {
		return ""
	}
	c := replyDraft(e)
	c.Kind = api.DraftReply
	kind, err := userDraftKind(c)
	if err != nil {
		return ""
	}
	key := c.Unit.String() + "\x00" + strings.Join(c.Items, "\x00")
	now := time.Now()
	if v, ok := s.replyBroadcasts.Load(key); ok {
		if seen := v.(broadcastSeen); now.Sub(seen.at) < draftWhereRetry {
			return string(seen.broadcast)
		}
	}
	by := now.Add(s.whereTimeout())
	page, ok := ctx.Value(whereByKey{}).(time.Time)
	pageBound := ok && page.Before(by)
	if pageBound {
		by = page
	}
	if !now.Before(by) {
		return ""
	}
	lctx, cancel := context.WithDeadline(ctx, by)
	defer cancel()
	b, err := s.deps.Outbox.BroadcastOf(lctx, outbox.Proposal{Unit: c.Unit, Items: c.Items, Task: c.Task, Kind: kind,
		Origin: draft.Origin{Kind: draft.OriginUser}})
	if err != nil {
		if pageBound && lctx.Err() != nil || ctx.Err() != nil {
			return ""
		}
		b = draft.NoBroadcast
	}
	s.keepLookup(&s.replyBroadcasts, key, broadcastSeen{broadcast: b, at: now}, now)
	return string(b)
}

type broadcastSeen struct {
	broadcast draft.Broadcast
	at        time.Time
}

// broadcastArg is the broadcast a reply's broadcast argument asks for, "" when it names none.
func broadcastArg(args map[string]string) (string, error) {
	raw, ok := args["broadcast"]
	if !ok {
		return "", nil
	}
	switch raw {
	case "true":
		return string(draft.BroadcastOn), nil
	case "false":
		return string(draft.BroadcastOff), nil
	}
	return "", fmt.Errorf("%w: broadcast is true or false, not %q", coretasks.ErrArgs, raw)
}

func sendArg(raw string) (bool, error) {
	switch raw {
	case "", "draft":
		return false, nil
	case "now":
		return true, nil
	}
	return false, fmt.Errorf("%w: send is \"now\" or \"draft\", not %q", coretasks.ErrArgs, raw)
}

// userDraftLine says where a user's draft went, as the profile words it.
func (s *Server) userDraftLine(d draft.Draft) string {
	if d.State == draft.Queued {
		return s.notice(builtin.NoticeDraftSent, map[string]any{"Draft": int64(d.ID)})
	}
	return s.notice(builtin.NoticeDraftSaved, map[string]any{"Draft": int64(d.ID)})
}

func (s *Server) handleDraftCreate(w http.ResponseWriter, r *http.Request) {
	if s.deps.Outbox == nil {
		writeActionError(w, errOutboxUnavailable)
		return
	}
	var c api.DraftCreate
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, draftsBodyMax)).Decode(&c); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	ctx := context.WithoutCancel(r.Context())
	if c.Task != "" && s.deps.Tasks != nil {
		e, err := s.deps.Tasks.Task(ctx, task.ID(c.Task))
		switch {
		case errors.Is(err, coretasks.ErrNoTask):
			writeError(w, http.StatusNotFound, err.Error())
			return
		case err != nil:
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		case !slices.Contains(e.Units, c.Unit):
			writeError(w, http.StatusBadRequest, fmt.Sprintf("task %s is not about %s", c.Task, c.Unit.Label()))
			return
		}
	}
	q, ok := s.requester(w, r)
	if !ok {
		return
	}
	d, err := s.writeUserDraft(ctx, c, q)
	if err != nil {
		writeDraftError(w, err)
		return
	}
	g, _, err := s.deps.Outbox.GateOf(ctx, d)
	if err != nil {
		writeDraftError(w, err)
		return
	}
	writeJSON(w, s.draftDetail(ctx, d, g, nil))
}

// writeUserDraft proposes the draft the user wrote, under the wait policy whatever the unit's, and approves its
// gate right away when c.Send: the same approval and publish path a draft takes from the Outbox.
func (s *Server) writeUserDraft(ctx context.Context, c api.DraftCreate, q requester) (draft.Draft, error) {
	if c.Send && !q.user() {
		return draft.Draft{}, fmt.Errorf("%w: a draft sent at once %s", gates.ErrRefused, unattended("is approved: it"))
	}
	source := turn.Source(c.Source)
	if source == "" {
		source = turn.Cockpit
	}
	switch {
	case !slices.Contains(answerSources, source):
		return draft.Draft{}, badRequest{fmt.Sprintf("source %q is not one of cockpit, notification, tui, terminal", c.Source)}
	case c.Unit.IsZero():
		return draft.Draft{}, badRequest{"a draft needs the unit it posts on"}
	case !s.pluginEnabled(c.Unit.Provider):
		return draft.Draft{}, pluginOff{c.Unit.Provider}
	}
	kind, err := userDraftKind(c)
	if err != nil {
		return draft.Draft{}, err
	}
	broadcast := draft.Broadcast(c.Broadcast)
	if !broadcast.Valid() {
		return draft.Draft{}, badRequest{fmt.Sprintf("broadcast is %q or %q, not %q", draft.BroadcastOn, draft.BroadcastOff, c.Broadcast)}
	}
	if err := s.targetIn(ctx, strings.TrimSpace(c.Target), c.Unit); err != nil {
		return draft.Draft{}, err
	}
	s.draftMu.Lock()
	defer s.draftMu.Unlock()
	origin := draft.Origin{Kind: draft.OriginUser}
	if !q.user() {
		origin.Session = q.Session
	}
	o := s.deps.Outbox
	d, err := o.Propose(ctx, outbox.Proposal{Unit: c.Unit, Items: c.Items, Task: c.Task, Kind: kind, Text: c.Text, Target: strings.TrimSpace(c.Target),
		Reaction: strings.Trim(strings.TrimSpace(c.Reaction), ":"), Broadcast: broadcast, Origin: origin, Policy: draft.Fixed(draft.Approval{Mode: draft.Wait})})
	if errors.Is(err, outbox.ErrDisabled) {
		err = noPublisher{c.Unit.Provider, err}
	}
	if err != nil || !c.Send {
		return d, err
	}
	s.logf("draft %d sent at once by the user from the %s", d.ID, source)
	got, err := s.decideDraft(ctx, d, gate.Approved, nil, q)
	if err != nil {
		return d, fmt.Errorf("draft %d is in the Outbox, not approved: %w", d.ID, err)
	}
	return got, nil
}

func userDraftKind(c api.DraftCreate) (draft.Kind, error) {
	switch k := userKind(c); k {
	case api.DraftReply:
		if c.Reaction != "" {
			return "", badRequest{"a reply takes text, not a reaction"}
		}
		return draft.Kind(k), nil
	case api.DraftReact:
		if strings.TrimSpace(c.Text) != "" {
			return "", badRequest{"a reaction takes an emoji, not text"}
		}
		return draft.Kind(k), nil
	default:
		return "", badRequest{fmt.Sprintf("a draft you write is a %s or a %s, not %q", api.DraftReply, api.DraftReact, k)}
	}
}

// userKind is the kind c names, else react for a reaction alone and reply otherwise.
func userKind(c api.DraftCreate) string {
	switch {
	case c.Kind != "":
		return c.Kind
	case c.Reaction != "" && strings.TrimSpace(c.Text) == "":
		return api.DraftReact
	}
	return api.DraftReply
}

// targetIn refuses a draft target that does not land in u: the target is a
// message of u, as the plugins resolve it, or none.
func (s *Server) targetIn(ctx context.Context, target string, u unit.Unit) error {
	if target == "" {
		return nil
	}
	if s.deps.Resolve == nil {
		return badRequest{fmt.Sprintf("no plugin resolves the target %q", target)}
	}
	got, ok, err := s.deps.Resolve(ctx, target)
	switch {
	case err != nil:
		return err
	case !ok:
		return badRequest{fmt.Sprintf("the target %q names no message", target)}
	case got != u:
		return badRequest{fmt.Sprintf("the target %q is a message of %s, not of %s", target, got.Label(), u.Label())}
	}
	return nil
}
