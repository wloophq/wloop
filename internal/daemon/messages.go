package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/messages"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/gate"
	dmessage "github.com/wloophq/wloop/internal/domain/message"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

// registerMessages makes the dismiss verb of a message task an earlier version opened resolve the task's
// open gate as its refusing intent does.
func (s *Server) registerMessages() {
	if s.deps.Tasks == nil || s.deps.Messages == nil {
		return
	}
	s.deps.Tasks.Register(task.OriginMessage, coretasks.HandlerDrop, func(ctx context.Context, e task.Task, req coretasks.Request) (coretasks.Result, error) {
		var out strings.Builder
		ok, err := s.resolveTaskGate(ctx, e, builtin.GateRefuses, req, &out)
		switch {
		case err != nil:
			return coretasks.Result{}, err
		case !ok:
			return coretasks.Result{}, fmt.Errorf("%w: task %s has no open gate to dismiss", coretasks.ErrVerb, e.ID)
		}
		s.recordVerb(ctx, s.deps.Tasks, e, req)
		got, err := s.deps.Tasks.Task(ctx, e.ID)
		if err != nil {
			return coretasks.Result{}, err
		}
		return coretasks.Result{Task: got, Lines: lines(out.String())}, nil
	})
}

// Deliver is a resolved question or post gate's consequence: the messages deliver its answer, under the loop
// lock with the gate's unit read ahead, as an answer typed or resumed expects.
func (s *Server) Deliver(ctx context.Context, g gate.Gate) error {
	if s.deps.Messages == nil {
		return errors.New("the Inbox is not wired")
	}
	if !LoopHeld(ctx) {
		ctx = s.readAhead(ctx, g.Unit, false)
		s.deps.LoopMu.Lock()
		defer s.deps.LoopMu.Unlock()
		ctx = withLoopHeld(ctx)
	}
	return s.deps.Messages.Deliver(ctx, g)
}

// Decide is a resolved proposal gate's consequence: a verb a gate held runs, once approved, as the person
// through the verb path a person's request takes, its bridge and reply included; any other proposal is the
// messages'.
func (s *Server) Decide(ctx context.Context, g gate.Gate) error {
	req, ok := coretasks.ProposedVerb(g)
	switch {
	case ok && g.Status != gate.Approved:
		return nil
	case ok:
		return s.runApprovedVerb(ctx, g, req)
	case s.deps.Messages == nil:
		return errors.New("the Inbox is not wired")
	}
	return s.deps.Messages.Decide(ctx, g)
}

// runApprovedVerb runs req, the verb approved gate g proposed on its task, as the verb route runs a person's,
// from the client the approval came from.
func (s *Server) runApprovedVerb(ctx context.Context, g gate.Gate, req coretasks.Request) error {
	if s.deps.Tasks == nil {
		return errTasksUnavailable
	}
	e, err := s.deps.Tasks.Task(ctx, g.Task)
	if err != nil {
		return err
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, "/", nil)
	if err != nil {
		return err
	}
	source := string(turn.Cockpit)
	if d, ok := messages.DeliveryOf(ctx); ok && slices.Contains(intakeSources, string(d.Source)) {
		source = string(d.Source)
	}
	rec := &bufferedResponse{header: http.Header{}, code: http.StatusOK}
	s.doTaskVerb(rec, r, s.deps.Tasks, e, req, api.TaskRequest{Args: req.Args, Source: source})
	if rec.code/100 != 2 {
		var body struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(rec.body.Bytes(), &body)
		return fmt.Errorf("%s on %s: %d %s", req.Verb, e.ID, rec.code, strings.TrimSpace(cmpOr(body.Error, rec.body.String())))
	}
	return nil
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// actionArgs reads a task verb's args as the fields of api.ActionRequest.
func actionArgs(req coretasks.Request) (api.ActionRequest, error) {
	ar := api.ActionRequest{Guidance: req.Args["guidance"], OptionID: req.Args["option_id"], Source: req.Source}
	for key, dst := range map[string]*bool{"yes": &ar.Yes, "no": &ar.No} {
		if v, ok := req.Args[key]; ok {
			b, err := strconv.ParseBool(v)
			if err != nil {
				return api.ActionRequest{}, fmt.Errorf("%w: %s is true or false, not %q", coretasks.ErrArgs, key, v)
			}
			*dst = b
		}
	}
	return ar, nil
}

// gateAnswer is the text an answer request gives a gate.
func gateAnswer(req api.ActionRequest) string {
	return messages.GateAnswer(messages.Answer{Text: strings.TrimSpace(req.Guidance), OptionID: req.OptionID, Yes: req.Yes, No: req.No})
}

// answerSource is the source an answer request names, the cockpit when it names none; false for one that is
// no answer source.
func answerSource(raw string) (turn.Source, error) {
	source := turn.Source(raw)
	if source == "" {
		source = turn.Cockpit
	}
	if !slices.Contains(answerSources, source) {
		return "", badRequest{fmt.Sprintf("source %q is not one of cockpit, notification, tui, terminal", raw)}
	}
	return source, nil
}

// resolveTaskGate resolves e's oldest open gate that takes role, as req's actor with req's args: the answer a
// question or post gate takes, its recommended option or a confirm's yes when the verb approves; false when no
// open gate takes role.
func (s *Server) resolveTaskGate(ctx context.Context, e task.Task, role builtin.GateRole, req coretasks.Request, out io.Writer) (bool, error) {
	ar, err := actionArgs(req)
	if err != nil {
		return false, err
	}
	g, r, ok, err := s.taskGate(ctx, e, role, ar)
	if err != nil || !ok {
		return ok, err
	}
	if role == builtin.GateTakesText {
		if err := s.checkAnswer(ctx, g, ar); err != nil {
			return true, err
		}
	}
	r.By = req.Actor
	return true, s.resolveGate(ctx, g, r, ar.Source, out)
}

// taskGate is e's oldest open gate that takes role and how ar resolves it; false when none does.
func (s *Server) taskGate(ctx context.Context, e task.Task, role builtin.GateRole, ar api.ActionRequest) (gate.Gate, gates.Resolution, bool, error) {
	if s.deps.GateReader == nil || s.deps.Gates == nil {
		return gate.Gate{}, gates.Resolution{}, false, nil
	}
	all, err := s.deps.GateReader.OfTask(ctx, e.ID)
	if err != nil {
		return gate.Gate{}, gates.Resolution{}, false, err
	}
	for _, g := range all {
		if g.Status != gate.Open || g.Kind == gate.KindDraft {
			continue
		}
		def, _ := s.loaded().Gates.Gate(g.Name)
		want, answer := role, ""
		if role != builtin.GateRefuses && (g.Kind == gate.KindQuestion || g.Kind == gate.KindPost) {
			if answer = s.answerFor(ctx, g, ar, role); answer == "" && role == builtin.GateApproves {
				continue
			}
			want = builtin.GateTakesText
		}
		if in, ok := intentOfRole(def, g, want); ok {
			return g, gates.Resolution{Intent: in.Name, Answer: answer}, true, nil
		}
	}
	return gate.Gate{}, gates.Resolution{}, false, nil
}

// answerFor is the answer a verb of role gives question or post gate g: the request's own when it answers, the
// message's recommended option or a confirm's yes when it approves; "" when it gives none.
func (s *Server) answerFor(ctx context.Context, g gate.Gate, ar api.ActionRequest, role builtin.GateRole) string {
	m, ok := s.messageOf(ctx, g)
	if role == builtin.GateTakesText {
		if a := gateAnswer(ar); a != "" || !ok || !m.Confirm {
			return a
		}
		return messages.GateAnswer(messages.Answer{No: true})
	}
	switch {
	case !ok:
		return ""
	case m.Recommend != "" && slices.ContainsFunc(m.Options, func(o record.Option) bool { return o.ID == m.Recommend }):
		return messages.GateAnswer(messages.Answer{OptionID: m.Recommend})
	case m.Confirm:
		return messages.GateAnswer(messages.Answer{Yes: true})
	}
	return ""
}

// checkAnswer refuses, before g resolves, an answer its message does not take: an option it does not offer,
// yes or no on what is no confirm, or both. A gate takes its answer as text, which would read them as text.
func (s *Server) checkAnswer(ctx context.Context, g gate.Gate, ar api.ActionRequest) error {
	m, ok := s.messageOf(ctx, g)
	switch {
	case !ok:
	case ar.OptionID != "" && !slices.ContainsFunc(m.Options, func(o record.Option) bool { return o.ID == ar.OptionID }):
		return turns.InvalidAnswer{Reason: fmt.Sprintf("the question on %s has no option %q", m.Unit.Label(), ar.OptionID)}
	case ar.Yes && ar.No:
		return turns.InvalidAnswer{Reason: "an answer is yes or no, not both"}
	case (ar.Yes || ar.No) && !m.Confirm:
		return turns.InvalidAnswer{Reason: "yes and no answer only a confirm"}
	}
	return nil
}

// intentOfRole is the first intent g offers whose role is role, as gates.toml declares it.
func intentOfRole(def intentload.GateDef, g gate.Gate, role builtin.GateRole) (intentload.GateIntent, bool) {
	i := slices.IndexFunc(def.Intents, func(in intentload.GateIntent) bool {
		return builtin.GateRoleOf(in.Status, in.Answer) == role && slices.Contains(g.Intents, in.Name)
	})
	if i < 0 {
		return intentload.GateIntent{}, false
	}
	return def.Intents[i], true
}

// resolveGate resolves g with r, an answer given from source, and writes where its answer went.
func (s *Server) resolveGate(ctx context.Context, g gate.Gate, r gates.Resolution, source string, out io.Writer) error {
	if !g.Unit.IsZero() && !s.pluginEnabled(g.Unit.Provider) {
		return pluginOff{g.Unit.Provider}
	}
	src, err := answerSource(source)
	if err != nil {
		return err
	}
	d := &messages.Delivery{Source: src}
	err = s.deps.Gates.Resolve(messages.WithDelivery(ctx, d), g.ID, r)
	var refused turns.Refusal
	if err != nil && !errors.As(err, &refused) {
		return err
	}
	if d.Route.Route == "" {
		fmt.Fprintf(out, "%s: %s\n", s.gateWords(g), r.Intent)
		return nil
	}
	writeRoute(out, d.Route.Route, cmpOr(d.Route.Note, refused.Reason))
	return nil
}

// writeRoute says where an answer went.
func writeRoute(out io.Writer, route turn.Route, note string) {
	switch route {
	case turn.Typed:
		fmt.Fprintln(out, "answer sent to the agent session")
	case turn.Resumed:
		fmt.Fprintln(out, "answer stored; a new session resumes the turn with it")
	case turn.Queued:
		fmt.Fprintln(out, api.AnswerQueued)
	case turn.Refused:
		fmt.Fprintln(out, "answer stored; "+note)
	}
}

// gateWords is how gates.toml names g.
func (s *Server) gateWords(g gate.Gate) string {
	def, _ := s.loaded().Gates.Gate(g.Name)
	return cmpOr(def.Label, g.Name)
}

// messageOf is the message question, post or proposal gate g is the gate of.
func (s *Server) messageOf(ctx context.Context, g gate.Gate) (dmessage.Message, bool) {
	if s.deps.Messages == nil || g.Kind != gate.KindQuestion && g.Kind != gate.KindPost && g.Kind != gate.KindProposal {
		return dmessage.Message{}, false
	}
	id, err := strconv.ParseInt(g.Subject, 10, 64)
	if err != nil {
		return dmessage.Message{}, false
	}
	m, ok, err := s.deps.Messages.Store.Message(ctx, id)
	if err != nil {
		s.logf("the message of gate %d: %v", g.ID, err)
	}
	return m, ok && err == nil && m.Kind.Gate() == g.Kind
}

// gateMessage is the id of the message of question or post gate raw names; false for any other.
func (s *Server) gateMessage(ctx context.Context, raw string) (int64, bool) {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || s.deps.GateReader == nil {
		return 0, false
	}
	g, err := s.deps.GateReader.Get(ctx, id)
	if err != nil || g.Kind != gate.KindQuestion && g.Kind != gate.KindPost {
		return 0, false
	}
	m, ok := s.messageOf(ctx, g)
	return m.ID, ok
}

// messageView is m as a question, post or proposal gate serves it, with the user's answers to it.
func (s *Server) messageView(ctx context.Context, m dmessage.Message) api.Message {
	v := api.Message{ID: m.ID, Unit: m.Unit, Kind: string(m.Kind), Text: m.Text, Options: m.Options, Recommend: m.Recommend, Confirm: m.Confirm,
		Origin: m.Origin.Kind, CreatedAt: m.CreatedAt}
	c, err := s.deps.Messages.Conversation(ctx, m)
	if err != nil {
		s.logf("the answers to message %d: %v", m.ID, err)
	}
	for _, a := range c.Answers {
		v.Answers = append(v.Answers, wireAnswer(a))
	}
	return v
}

// pluginOff refuses a message of a disabled plugin's unit.
type pluginOff struct{ provider unit.ProviderID }

func (p pluginOff) Error() string { return string(p.provider) + " is disabled" }
