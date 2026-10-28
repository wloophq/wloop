package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/catalog"
	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/messages"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

// questionIntent is the row action name of a question gate's intent; false for any other name.
func questionIntent(l *intentload.Loaded, name string) (catalog.GateIntent, bool) {
	g, ok := catalog.RowGateIntent(l, name)
	return g, ok && g.Kind == gate.KindQuestion
}

// actOnQuestion resolves u's open question gate, its turn's question or the gate its stopped turn opened, with
// the intent of g's role that gate offers, as by. With no question gate open, an answer answers again a
// question already answered, or a stopped turn no gate holds, and the others continue or dismiss the turn's
// question.
func (s *Server) actOnQuestion(ctx context.Context, g catalog.GateIntent, u unit.Unit, req api.ActionRequest, by task.Actor, out io.Writer) error {
	open, _, ok, err := s.openRowGate(ctx, gate.KindQuestion, u)
	if err == nil && !ok && s.deps.Messages != nil {
		if err = s.deps.Messages.Reconcile(ctx, u); err == nil {
			open, _, ok, err = s.openRowGate(ctx, gate.KindQuestion, u)
		}
	}
	if err != nil {
		return err
	}
	if !ok || s.deps.Gates == nil {
		return s.questionWithoutGate(ctx, g, u, req, out)
	}
	def, _ := s.loaded().Gates.Gate(open.Name)
	in, ok := intentOfRole(def, open, g.Role)
	if !ok {
		return conflict{msg: fmt.Sprintf("the question on %s offers no intent that %s", u.Label(), g.Role)}
	}
	r := gates.Resolution{Intent: in.Name, By: by}
	if err := s.checkAnswer(ctx, open, req); err != nil {
		return err
	}
	if in.Answer {
		if r.Answer = s.answerFor(ctx, open, req, builtin.GateTakesText); r.Answer == "" {
			return badRequest{in.Name + " needs your answer"}
		}
	}
	return s.resolveGate(ctx, open, r, req.Source, out)
}

// questionWithoutGate is g's role on u's question with no question gate open.
func (s *Server) questionWithoutGate(ctx context.Context, g catalog.GateIntent, u unit.Unit, req api.ActionRequest, out io.Writer) error {
	if s.deps.Turns == nil || s.deps.Messages == nil {
		return errTurnsUnavailable
	}
	switch g.Role {
	case builtin.GateTakesText:
		m, ok, err := s.deps.Messages.Question(ctx, u)
		if err != nil {
			return err
		}
		source, err := answerSource(req.Source)
		if err != nil {
			return err
		}
		ctx = s.readAhead(ctx, u, false)
		s.deps.LoopMu.Lock()
		defer s.deps.LoopMu.Unlock()
		a := messages.Answer{Text: strings.TrimSpace(req.Guidance), OptionID: req.OptionID, Yes: req.Yes, No: req.No, Source: source}
		var res turns.AnswerResult
		if ok && !m.Open() {
			res, err = s.deps.Messages.Reanswer(ctx, m.ID, a)
		} else {
			res, err = s.deps.Turns.Answer(ctx, turns.AnswerRequest{Unit: u, Text: a.Text, OptionID: a.OptionID, Yes: a.Yes, No: a.No, Source: source})
		}
		var refused turns.Refusal
		if err != nil && !errors.As(err, &refused) {
			return err
		}
		writeRoute(out, res.Route, cmpOr(res.Note, refused.Reason))
		return nil
	case builtin.GateRefuses:
		if err := s.deps.Turns.Dismiss(ctx, u); err != nil {
			return err
		}
		fmt.Fprintln(out, "question on "+u.Label()+" dismissed")
		return nil
	}
	ctx = s.readAhead(ctx, u, false)
	s.deps.LoopMu.Lock()
	defer s.deps.LoopMu.Unlock()
	if err := s.deps.Turns.Continue(ctx, u); err != nil {
		return err
	}
	fmt.Fprintln(out, "the agent continues on "+u.Label())
	return nil
}
