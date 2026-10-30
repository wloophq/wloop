package daemon

import (
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brief"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/turn"
)

func wireBriefStatus(s brief.Status) *api.BriefStatus {
	return &api.BriefStatus{Ready: s.Ready, Pending: s.Pending, Failed: s.Failed, LastError: s.LastError, PausedUntil: s.PausedUntil}
}

// wireTurnView is the turn an answer goes to, else the book's newest, with
// its records and answers; nil when the book has no turn.
func wireTurnView(b turn.Book, answerable []string) *api.TurnView {
	t, ok := b.Question(answerable)
	if !ok {
		if t, ok = b.Newest(); !ok {
			return nil
		}
	}
	v := &api.TurnView{Turn: wireTurn(*t), Records: []api.TurnRecord{}, Answers: []api.TurnAnswer{}}
	for _, r := range b.Records {
		if r.TurnSeq == t.Seq {
			v.Records = append(v.Records, api.TurnRecord{ID: r.ID, TurnSeq: r.TurnSeq, Session: r.Session, AgentSession: r.AgentSession,
				Head: r.Head, Hash: r.Hash, At: r.At, Acked: r.Acked, Record: r.Record})
		}
	}
	for _, a := range b.Answers {
		if a.TurnSeq == t.Seq {
			v.Answers = append(v.Answers, wireAnswer(a))
		}
	}
	if last, ok := b.LastRecord(t.Seq); ok && last.Record.State == record.UnrecordedState {
		v.LastMessage = last.Record.Resume
	}
	return v
}

func wireTurn(t turn.Turn) api.Turn {
	return api.Turn{Seq: t.Seq, Intent: string(t.Intent), ID: t.ID, Trigger: string(t.Trigger), Guidance: t.Guidance, Head: t.Head,
		SentTo: t.SentTo, Session: t.Session, State: string(t.State), SentAt: t.SentAt, StartedAt: t.StartedAt, EndedAt: t.EndedAt,
		ResumeOf: t.ResumeOf, ReplacedBy: t.ReplacedBy, Before: string(t.Before),
		Agent: t.Agent, BriefRun: t.BriefRun, AgentSession: t.AgentSession, Runner: string(t.Runner), Workflow: t.Workflow, Role: t.Role}
}

func wireAnswer(a turn.Answer) api.TurnAnswer {
	w := api.TurnAnswer{ID: a.ID, TurnSeq: a.TurnSeq, Text: a.Text, OptionID: a.OptionID, Yes: a.Yes, No: a.No,
		Source: string(a.Source), Route: string(a.Route), Session: a.Session, At: a.At, Message: a.Message, Gate: a.Gate, Posted: a.Posted}
	if !a.NoticedAt.IsZero() {
		w.NoticedAt = &a.NoticedAt
	}
	if !a.ReadAt.IsZero() {
		w.ReadAt = &a.ReadAt
	}
	return w
}
