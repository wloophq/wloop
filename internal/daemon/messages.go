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
	coregtd "github.com/wloophq/wloop/internal/core/gtd"
	"github.com/wloophq/wloop/internal/core/messages"
	"github.com/wloophq/wloop/internal/domain/gtd"
	dmessage "github.com/wloophq/wloop/internal/domain/message"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// registerMessages makes the message entries' answer, dismiss and approve verbs answer their messages.
func (s *Server) registerMessages() {
	if s.deps.GTD == nil || s.deps.Messages == nil {
		return
	}
	s.deps.GTD.Register(gtd.KindMessage, gtd.VerbAnswer, s.entryVerb(gtd.VerbAnswer))
	s.deps.GTD.Register(gtd.KindMessage, gtd.VerbDismiss, s.entryVerb(gtd.VerbDismiss))
	s.deps.GTD.Register(gtd.KindMessage, gtd.VerbApprove, s.entryVerb(gtd.VerbApprove))
}

func (s *Server) entryVerb(verb gtd.Verb) coregtd.VerbHandler {
	return func(ctx context.Context, e gtd.Entry, req coregtd.Request) (coregtd.Result, error) {
		m, ok, err := s.deps.Messages.Store.ByEntry(ctx, string(e.ID))
		if err != nil {
			return coregtd.Result{}, err
		}
		if !ok {
			return coregtd.Result{}, fmt.Errorf("%w: entry %s holds no message", coregtd.ErrVerb, e.ID)
		}
		var out bytes.Buffer
		switch verb {
		case gtd.VerbDismiss:
			err = s.dismissMessage(ctx, m, &out)
		case gtd.VerbApprove:
			var ar api.ActionRequest
			switch {
			case m.Kind == dmessage.Proposal && slices.ContainsFunc(m.Options, func(o record.Option) bool { return o.ID == messages.OptionAccept }):
				ar.OptionID = messages.OptionAccept
			case m.Confirm:
				ar.Yes = true
			default:
				return coregtd.Result{}, fmt.Errorf("%w: approve takes a proposal or a yes or no question", coregtd.ErrVerb)
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
			return coregtd.Result{}, err
		}
		s.recordVerb(ctx, s.deps.GTD, e, verb, req.Source)
		got, err := s.deps.GTD.Entry(ctx, e.ID)
		if err != nil {
			return coregtd.Result{}, err
		}
		return coregtd.Result{Entry: got, Lines: lines(out.String())}, nil
	}
}

// actionArgs reads an entry verb's args as the fields of api.ActionRequest.
func actionArgs(req coregtd.Request) (api.ActionRequest, error) {
	ar := api.ActionRequest{Guidance: req.Args["guidance"], OptionID: req.Args["option_id"], Source: req.Source}
	for key, dst := range map[string]*bool{"yes": &ar.Yes, "no": &ar.No} {
		if v, ok := req.Args[key]; ok {
			b, err := strconv.ParseBool(v)
			if err != nil {
				return api.ActionRequest{}, fmt.Errorf("%w: %s is true or false, not %q", coregtd.ErrArgs, key, v)
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

// entryMessage is the message a KindMessage entry carries, nil when it holds none.
func (s *Server) entryMessage(ctx context.Context, entry string) (*api.Message, error) {
	if s.deps.Messages == nil {
		return nil, nil
	}
	m, ok, err := s.deps.Messages.Store.ByEntry(ctx, entry)
	if err != nil || !ok {
		return nil, err
	}
	v := messageView(m)
	return &v, nil
}

func messageView(m dmessage.Message) api.Message {
	return api.Message{ID: m.ID, EntryID: m.EntryID, Unit: m.Unit, Kind: string(m.Kind), Text: m.Text, Options: m.Options,
		Recommend: m.Recommend, Confirm: m.Confirm, Origin: m.Origin.Kind, State: string(m.State), CreatedAt: m.CreatedAt}
}
