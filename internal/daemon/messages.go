package daemon

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/messages"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	dmessage "github.com/wloophq/wloop/internal/domain/message"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// registerMessages makes the message tasks' answer, dismiss and approve verbs answer their messages.
func (s *Server) registerMessages() {
	if s.deps.Tasks == nil || s.deps.Messages == nil {
		return
	}
	s.deps.Tasks.Register(task.KindMessage, string(task.VerbAnswer), s.taskVerb(task.VerbAnswer))
	s.deps.Tasks.Register(task.KindMessage, string(task.VerbDismiss), s.taskVerb(task.VerbDismiss))
	s.deps.Tasks.Register(task.KindMessage, string(task.VerbApprove), s.taskVerb(task.VerbApprove))
}

func (s *Server) taskVerb(verb task.Verb) coretasks.VerbHandler {
	return func(ctx context.Context, e task.Task, req coretasks.Request) (coretasks.Result, error) {
		m, ok, err := s.deps.Messages.Store.ByTask(ctx, string(e.ID))
		if err != nil {
			return coretasks.Result{}, err
		}
		if !ok {
			return coretasks.Result{}, fmt.Errorf("%w: task %s holds no message", coretasks.ErrVerb, e.ID)
		}
		var out bytes.Buffer
		switch verb {
		case task.VerbDismiss:
			err = s.dismissMessage(ctx, m, &out)
		case task.VerbApprove:
			var ar api.ActionRequest
			switch {
			case m.Kind == dmessage.Proposal && slices.ContainsFunc(m.Options, func(o record.Option) bool { return o.ID == messages.OptionAccept }):
				ar.OptionID = messages.OptionAccept
			case m.Confirm:
				ar.Yes = true
			default:
				return coretasks.Result{}, fmt.Errorf("%w: approve takes a proposal or a yes or no question", coretasks.ErrVerb)
			}
			ar.Source = req.Source
			err = s.answerMessage(ctx, m, ar, &out)
		default:
			var ar api.ActionRequest
			if ar, err = actionArgs(req); err == nil {
				err = s.answerMessage(ctx, m, ar, &out)
			}
		}
		if err != nil {
			return coretasks.Result{}, err
		}
		s.recordVerb(ctx, s.deps.Tasks, e, req)
		got, err := s.deps.Tasks.Task(ctx, e.ID)
		if err != nil {
			return coretasks.Result{}, err
		}
		return coretasks.Result{Task: got, Lines: lines(out.String())}, nil
	}
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

// answerMessage answers m as the unit's answer action answers its question: the same source check, the same
// loop mutex and read-ahead around the turn, and the same lines.
func (s *Server) answerMessage(ctx context.Context, m dmessage.Message, req api.ActionRequest, out io.Writer) error {
	if !m.Unit.IsZero() && !s.pluginEnabled(m.Unit.Provider) {
		return pluginOff{m.Unit.Provider}
	}
	source := turn.Source(req.Source)
	if source == "" {
		source = turn.Cockpit
	}
	if !slices.Contains(answerSources, source) {
		return badRequest{fmt.Sprintf("source %q is not one of cockpit, notification, tui, terminal", req.Source)}
	}
	a := messages.Answer{Text: strings.TrimSpace(req.Guidance), OptionID: req.OptionID, Yes: req.Yes, No: req.No, Source: source}
	if m.Kind == dmessage.Proposal {
		if _, err := s.deps.Messages.Answer(ctx, m.ID, a); err != nil {
			return err
		}
		fmt.Fprintln(out, "proposal answered")
		return nil
	}
	ctx = s.readAhead(ctx, m.Unit, false)
	s.deps.LoopMu.Lock()
	defer s.deps.LoopMu.Unlock()
	res, err := s.deps.Messages.Answer(ctx, m.ID, a)
	if err != nil {
		return err
	}
	switch res.Route {
	case turn.Typed:
		fmt.Fprintln(out, "answer sent to the agent session")
	case turn.Resumed:
		fmt.Fprintln(out, "answer stored; a new session resumes the turn with it")
	case turn.Queued:
		fmt.Fprintln(out, api.AnswerQueued)
	case turn.Refused:
		fmt.Fprintln(out, "answer stored; "+res.Note)
	}
	return nil
}

func (s *Server) dismissMessage(ctx context.Context, m dmessage.Message, out io.Writer) error {
	if !m.Unit.IsZero() && !s.pluginEnabled(m.Unit.Provider) {
		return pluginOff{m.Unit.Provider}
	}
	if err := s.deps.Messages.Dismiss(ctx, m.ID); err != nil {
		return err
	}
	if m.Kind == dmessage.Question {
		fmt.Fprintln(out, "question on "+m.Unit.Label()+" dismissed")
	} else {
		fmt.Fprintf(out, "message %d dismissed\n", m.ID)
	}
	return nil
}

// pluginOff refuses a message of a disabled plugin's unit.
type pluginOff struct{ provider unit.ProviderID }

func (p pluginOff) Error() string { return string(p.provider) + " is disabled" }

// taskMessage is the message a KindMessage task carries, nil when it holds none.
func (s *Server) taskMessage(ctx context.Context, task string) (*api.Message, error) {
	if s.deps.Messages == nil {
		return nil, nil
	}
	m, ok, err := s.deps.Messages.Store.ByTask(ctx, task)
	if err != nil || !ok {
		return nil, err
	}
	c, err := s.deps.Messages.Conversation(ctx, m)
	if err != nil {
		return nil, err
	}
	v := messageView(m)
	for _, a := range c.Answers {
		v.Answers = append(v.Answers, wireAnswer(a))
	}
	return &v, nil
}

func messageView(m dmessage.Message) api.Message {
	return api.Message{ID: m.ID, TaskID: m.TaskID, Unit: m.Unit, Kind: string(m.Kind), Text: m.Text, Options: m.Options,
		Recommend: m.Recommend, Confirm: m.Confirm, Origin: m.Origin.Kind, State: string(m.State), CreatedAt: m.CreatedAt}
}
