package daemon

import (
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brief"
	dgh "github.com/wloophq/wloop/internal/domain/github"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/turn"
)

func wireEvent(e dinv.Event) api.Event {
	return api.Event{PR: e.PR, Unit: e.PR.Unit(), Kind: e.Kind, Key: e.Key, Actor: e.Actor, At: e.At, Data: e.Data}
}

func wireEvents(evs []dinv.Event) []api.Event {
	out := make([]api.Event, len(evs))
	for i, e := range evs {
		out[i] = wireEvent(e)
	}
	return out
}

func wireBriefView(v *brief.View) *api.BriefView {
	if v == nil {
		return nil
	}
	out := &api.BriefView{State: v.State, Model: v.Model, At: v.At, Error: v.Error, Stale: v.Stale}
	if b := v.Brief; b != nil {
		out.Brief = &api.Brief{Goal: b.Goal, Context: b.Context, Kind: b.Kind, Size: b.Size, Risk: b.Risk,
			RiskWhy: b.RiskWhy, OpenQuestions: b.OpenQuestions, Tags: b.Tags, Extra: b.Extra, ExtraVersions: b.ExtraVersions}
	}
	return out
}

func wireBriefStatus(s brief.Status) *api.BriefStatus {
	return &api.BriefStatus{Ready: s.Ready, Pending: s.Pending, Failed: s.Failed, LastError: s.LastError, PausedUntil: s.PausedUntil}
}

func wireRateLimit(rl dgh.RateLimit) api.RateLimit {
	return api.RateLimit{Cost: rl.Cost, Remaining: rl.Remaining, Limit: rl.Limit, ResetAt: rl.ResetAt}
}

func wireLimit(l dgh.Limit) api.RateLimit {
	return api.RateLimit{Remaining: l.Remaining, Limit: l.Limit, ResetAt: l.ResetAt}
}

func wireCandidate(c dgh.Candidate, b *brief.View) api.Candidate {
	return api.Candidate{Ref: c.Ref, Unit: c.Ref.Unit(), URL: c.URL, Title: c.Title, Author: c.Author, State: c.State, IsDraft: c.IsDraft,
		CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt, Additions: c.Additions, Deletions: c.Deletions,
		ChangedFiles: c.ChangedFiles, HeadBranch: c.HeadBranch, BaseBranch: c.BaseBranch, ReviewDecision: c.ReviewDecision,
		ReviewRequests: c.ReviewRequests, ChecksState: c.ChecksState, Comments: c.Comments, MyReview: c.MyReview,
		MyReviewOnHead: c.MyReviewOnHead, Brief: wireBriefView(b)}
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
		Source: string(a.Source), Route: string(a.Route), Session: a.Session, At: a.At, Message: a.Message, Posted: a.Posted}
	if !a.NoticedAt.IsZero() {
		w.NoticedAt = &a.NoticedAt
	}
	if !a.ReadAt.IsZero() {
		w.ReadAt = &a.ReadAt
	}
	return w
}
