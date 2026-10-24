package turns

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// StopRequest is what the Stop hook reports; Agent is the hook's agent.
type StopRequest struct {
	Session, AgentSession, PromptID, LastMessage string
	Agent, Transcript                            string
}

type StopAnswer struct {
	Block  bool
	Reason string
}

// Stop decides the Stop hook of the handle's session: allow, or block once
// per prompt when its turn owes a record.
func (s *Service) Stop(ctx context.Context, r StopRequest) (StopAnswer, error) {
	s.mu.Lock()
	ans, ref, saved, err := s.stop(ctx, r)
	s.mu.Unlock()
	s.publish(saved...)
	if ref != nil {
		s.changed(ctx, *ref)
	}
	if err != nil {
		return StopAnswer{}, err
	}
	return ans, nil
}

// stop returns the events of every save that succeeded, also with an error.
func (s *Service) stop(ctx context.Context, r StopRequest) (StopAnswer, *unit.Unit, []port.Event, error) {
	var evs []port.Event
	sess, ok, err := s.Registry.Session(ctx, r.Session)
	if err != nil {
		return StopAnswer{}, nil, nil, err
	}
	s.saveAgentSession(ctx, AgentSessionInfo{ID: r.AgentSession, Kind: r.Agent, Session: r.Session,
		Home: sess.Home, Worktree: sess.Worktree, Transcript: r.Transcript})
	if ok && r.AgentSession != "" && sess.AgentSession != "" && sess.AgentSession != r.AgentSession {
		ev, err := s.applySession(ctx, r.Session, func(cur *turn.Session, now time.Time) (string, string) {
			cur.AgentSession, cur.StartedAt = r.AgentSession, now
			return KindSessionStarted, r.Session + ":" + now.Format(time.RFC3339Nano)
		})
		if err != nil {
			return StopAnswer{}, nil, nil, err
		}
		evs = append(evs, ev)
	}

	units, err := s.Store.UnitsOf(ctx, []string{r.Session})
	if err != nil {
		return StopAnswer{}, nil, evs, err
	}
	u, has := units[r.Session]
	if !has {
		return StopAnswer{}, nil, evs, nil
	}

	books, err := s.Store.Books(ctx, u)
	if err != nil {
		return StopAnswer{}, nil, evs, err
	}
	b := books[u]
	t, found := sessionTurn(&b, r.Session)
	facts := turn.StopFacts{HasTurn: found}
	if found {
		for _, rec := range b.Records {
			facts.Unacked = facts.Unacked || (rec.TurnSeq == t.Seq && !rec.Acked)
		}
		facts.Owes = b.OwesRecord(t.Seq)
		last, ok, err := LastStop(ctx, s.Registry, r.Session)
		if err != nil {
			return StopAnswer{}, nil, evs, err
		}
		facts.BlockedThisPrompt = ok && last.Blocked && last.PromptID == r.PromptID
	}

	now := s.now()
	stop := turn.Stop{Session: r.Session, PromptID: r.PromptID, AgentSession: r.AgentSession, Message: r.LastMessage, At: now}
	stopped := s.event(b.Unit, KindTurnStopped, r.Session+":"+r.PromptID, "")
	var ans StopAnswer
	var changed *unit.Unit
	switch turn.DecideStop(facts) {
	case turn.Allow:
		return StopAnswer{}, nil, evs, nil
	case turn.AckAndAllow:
		for i := range b.Records {
			if rec := &b.Records[i]; rec.TurnSeq == t.Seq && !rec.Acked {
				rec.Acked, stop.RecordID = true, rec.ID
			}
		}
		if _, err := s.Store.Save(ctx, b, stopped); err != nil {
			return StopAnswer{}, nil, evs, err
		}
		evs = append(evs, stopped)
	case turn.LogAndAllow:
	case turn.Block:
		stop.Blocked = true
		stopped = s.event(b.Unit, KindStopBlocked, r.Session+":"+r.PromptID, t.ID)
		ans = StopAnswer{Block: true, Reason: s.blockReason(b.Unit, t.ID)}
	case turn.StoreUnrecorded:
		rec := record.Unrecorded(r.LastMessage)
		hash := b.RecordHash(t.Seq, rec)
		b.Record(turn.Record{TurnSeq: t.Seq, Session: r.Session, AgentSession: r.AgentSession, Head: t.Head, Hash: hash, At: now, Acked: true, Record: rec})
		ended := s.event(b.Unit, KindTurnEnded, t.ID+":"+short(hash), rec.EventData())
		saved, err := s.Store.Save(ctx, b, ended)
		if err != nil {
			return StopAnswer{}, nil, evs, err
		}
		if last, ok := saved.LastRecord(t.Seq); ok {
			stop.RecordID = last.ID
		}
		evs = append(evs, ended)
		changed = &b.Unit
	}
	if err := s.Registry.LogStop(ctx, stop, stopped); err != nil {
		return StopAnswer{}, changed, evs, err
	}
	if !slices.Contains(evs, stopped) {
		evs = append(evs, stopped)
	}
	return ans, changed, evs, nil
}

func (s *Service) blockReason(ref unit.Unit, id string) string {
	return fmt.Sprintf("This "+brand.Name+" turn on %[1]s has no record. Call the `turn_end` tool of the "+brand.Name+" MCP server with turn %[2]s "+
		"(load it with ToolSearch if it is deferred), or, without that tool, write %[4]s/turn-%[2]s.json and run "+
		"`%[3]s turn end %[1]s --turn %[2]s --file %[4]s/turn-%[2]s.json`, then end your turn. "+
		"If the turn could not be done, record state failed with what broke.", urlOf(ref), id, s.Command, turn.ScratchDir)
}
