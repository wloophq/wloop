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
func (s *Server) runReply(ctx context.Context, w http.ResponseWriter, g *coretasks.Service, e task.Task, v task.Verb, req api.TaskRequest, actor task.Actor) {
	if why := s.replyRefusal(e); why != "" {
		writeTasksError(w, fmt.Errorf("%w: %s", coretasks.ErrVerb, why))
		return
	}
	send, err := sendArg(cmp.Or(req.Args["send"], s.replySend(string(v))))
	if err != nil {
		writeTasksError(w, err)
		return
	}
	u := e.Units[0]
	c := api.DraftCreate{Unit: u, Task: string(e.ID), Kind: req.Args["kind"], Text: req.Args["text"], Reaction: req.Args["reaction"],
		Send: send, Source: req.Source}
	for _, ref := range e.Items {
		if ref.Unit == u {
			c.Items = append(c.Items, ref.Item)
		}
	}
	d, err := s.writeUserDraft(ctx, c, actor)
	if err != nil {
		writeTasksError(w, taskDraftError(err))
		return
	}
	s.recordVerb(ctx, g, e, coretasks.Request{Verb: v, Source: req.Source, Actor: actor})
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
	actor, ok := requestActor(w, r)
	if !ok {
		return
	}
	d, err := s.writeUserDraft(ctx, c, actor)
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
func (s *Server) writeUserDraft(ctx context.Context, c api.DraftCreate, actor task.Actor) (draft.Draft, error) {
	if c.Send && actor != task.ActorPerson {
		return draft.Draft{}, fmt.Errorf("%w: a draft sent at once is approved by a person, not by %s", gates.ErrRefused, cmp.Or(string(actor), "no one"))
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
	if err := s.targetIn(ctx, strings.TrimSpace(c.Target), c.Unit); err != nil {
		return draft.Draft{}, err
	}
	s.draftMu.Lock()
	defer s.draftMu.Unlock()
	o := s.deps.Outbox
	d, err := o.Propose(ctx, outbox.Proposal{Unit: c.Unit, Items: c.Items, Task: c.Task, Kind: kind, Text: c.Text, Target: strings.TrimSpace(c.Target),
		Reaction: strings.Trim(strings.TrimSpace(c.Reaction), ":"), Origin: draft.Origin{Kind: draft.OriginUser}, Policy: draft.Fixed(draft.Approval{Mode: draft.Wait})})
	if errors.Is(err, outbox.ErrDisabled) {
		err = noPublisher{c.Unit.Provider, err}
	}
	if err != nil || !c.Send {
		return d, err
	}
	s.logf("draft %d sent at once by the user from the %s", d.ID, source)
	got, err := s.decideDraft(ctx, d, gate.Approved, nil, actor)
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
