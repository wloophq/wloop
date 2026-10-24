package turns

import (
	"context"
	"errors"
	"fmt"
	"time"

	coreitems "github.com/wloophq/wloop/internal/core/items"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

type RecordRequest struct {
	Session, AgentSession, Turn string
	// Unit is set when the command line names it; MCP does not.
	Unit unit.Unit
	// Via is "mcp" or "socket", written into turn_ended's data.
	Via    string
	Record record.Record
}

// KindLateRecord is the event of a record stored for the history of a
// replaced or dismissed turn, whose state it leaves alone.
const KindLateRecord = "late_record" // Key = turn id:hash[:12]

type Recorded struct {
	Unit  unit.Unit
	State record.State
	Added bool
	// Note tells the agent its record changes nothing: the turn was replaced
	// or dismissed.
	Note string
	// Ledger is what the items ledger made of the record's ops; nil when
	// none were applied.
	Ledger *coreitems.Result
	// Dropped counts the record's ops left unapplied because the turn had
	// already ended or was replaced.
	Dropped int
}

// DroppedNote tells the agent its ops did not reach the ledger; "" when
// none were dropped.
func (r Recorded) DroppedNote() string {
	if r.Dropped == 0 {
		return ""
	}
	return "Ledger: not applied; this turn already ended (or was replaced). List them in your next turn's record."
}

// RecordTurnEnd stores the record an agent ended its turn with. It returns
// record.Problems for a malformed record and a Refusal when the turn is not
// the caller's.
func (s *Service) RecordTurnEnd(ctx context.Context, r RecordRequest) (Recorded, error) {
	return s.recordEnd(ctx, r, false)
}

// recordEnd stores a record; only EndHeadless records a headless turn, and it records nothing else.
func (s *Service) recordEnd(ctx context.Context, r RecordRequest, byHeadless bool) (Recorded, error) {
	s.mu.Lock()
	got, evs, ops, err := s.recordTurnEnd(ctx, r, byHeadless)
	s.mu.Unlock()
	if err != nil {
		return got, err
	}
	if len(ops) > 0 {
		got.Ledger = s.applyLedger(context.WithoutCancel(ctx), ops)
	}
	if len(evs) > 0 {
		s.publish(evs...)
		s.changed(ctx, got.Unit)
	}
	return got, nil
}

// applyLedger applies a stored record's ops. A whole-call failure refuses
// each op with its reason; one the agent can retry says so, since the same
// record applies its ops again and an applied op re-applies as a no-op.
func (s *Service) applyLedger(ctx context.Context, ops []items.Op) *coreitems.Result {
	res, err := s.Items.Apply(ctx, items.Turn, ops)
	if err == nil {
		return &res
	}
	s.logf("turn ledger: %v", err)
	reason := fmt.Sprintf("may not have applied (%v); calling turn_end again with the same record is safe", err)
	if disabled := (*coreitems.DisabledError)(nil); errors.As(err, &disabled) {
		reason = disabled.Error()
	}
	res = coreitems.Result{Applied: []coreitems.Applied{}, Proposed: []coreitems.Proposal{}, Refused: make([]coreitems.Refused, 0, len(ops))}
	for i, o := range ops {
		res.Refused = append(res.Refused, coreitems.Refused{Index: i, Op: o, Reason: reason})
	}
	return &res
}

// ledgerOps are the ops of the turn's last record when it is the record
// that ended the turn: no earlier record of the turn closed it.
func (s *Service) ledgerOps(b *turn.Book, seq int64) []items.Op {
	if s.Items == nil {
		return nil
	}
	var recs []turn.Record
	for _, r := range b.Records {
		if r.TurnSeq == seq {
			recs = append(recs, r)
		}
	}
	if len(recs) == 0 {
		return nil
	}
	for _, r := range recs[:len(recs)-1] {
		if r.Record.State.ClosesTurn() {
			return nil
		}
	}
	return turn.LedgerOps(b.Unit, recs[len(recs)-1])
}

func (s *Service) recordTurnEnd(ctx context.Context, r RecordRequest, byHeadless bool) (Recorded, []port.Event, []items.Op, error) {
	b, ok, err := s.Store.BookOf(ctx, Key{Turn: r.Turn})
	if err != nil {
		return Recorded{}, nil, nil, err
	}
	t, found := b.ByID(r.Turn)
	if !ok || !found || r.Turn == "" {
		return Recorded{}, nil, nil, Refusal{fmt.Sprintf("no turn %q; record the turn your instructions name", r.Turn)}
	}
	if headless(*t) != byHeadless {
		if byHeadless {
			return Recorded{}, nil, nil, Refusal{fmt.Sprintf("turn %s is not headless", r.Turn)}
		}
		return Recorded{}, nil, nil, Refusal{fmt.Sprintf("turn %s is headless; its call records it", r.Turn)}
	}
	if !owns(t, r.Session) {
		return Recorded{}, nil, nil, Refusal{"this turn belongs to another session; record only the turn your instructions name"}
	}
	note := lateNote(&b, t)
	if note == "" {
		if err := s.newestOf(ctx, r.Session, b.Unit, t.Seq); err != nil {
			return Recorded{}, nil, nil, err
		}
	}
	if !r.Unit.IsZero() && r.Unit != b.Unit {
		return Recorded{}, nil, nil, Refusal{fmt.Sprintf("turn %s is on %s, not %s", r.Turn, b.Unit.Label(), r.Unit.Label())}
	}
	if s.Intents == nil {
		return Recorded{}, nil, nil, ErrNoIntents
	}
	rules, err := s.Intents.Rules(string(t.Intent))
	if err != nil {
		return Recorded{}, nil, nil, Refusal{fmt.Sprintf("%v; the turn's record cannot be checked", err)}
	}
	if err := r.Record.Validate(rules); err != nil {
		return Recorded{}, nil, nil, err
	}
	if err := opsOnUnit(r.Record, b.Unit); err != nil {
		return Recorded{}, nil, nil, err
	}
	hash := b.RecordHash(t.Seq, r.Record)
	added := b.Record(turn.Record{
		TurnSeq: t.Seq, Session: r.Session, AgentSession: r.AgentSession, Head: t.Head, Hash: hash,
		At: s.now(), Record: r.Record,
	})
	out := Recorded{Unit: b.Unit, State: r.Record.State, Added: added, Note: note}
	if s.Items != nil {
		out.Dropped = len(r.Record.Ledger)
	}
	if note != "" {
		if !added {
			return out, nil, nil, nil
		}
		late := s.event(b.Unit, KindLateRecord, r.Turn+":"+short(hash), r.Record.EventData())
		_, err := s.Store.Save(ctx, b, late)
		return out, nil, nil, err
	}
	ops := s.ledgerOps(&b, t.Seq)
	if len(ops) > 0 {
		out.Dropped = 0
	}
	if !added {
		return out, nil, ops, nil
	}
	data := r.Record.EventData()
	if r.Via != "" {
		data += " (via " + r.Via + ")"
	}
	ev := s.event(b.Unit, KindTurnEnded, r.Turn+":"+short(hash), data)
	if _, err := s.Store.Save(ctx, b, ev); err != nil {
		return Recorded{}, nil, nil, err
	}
	return out, []port.Event{ev}, ops, nil
}

// opsOnUnit refuses a record whose op names a unit other than the turn's;
// an anchor and a link's target may still name another unit.
func opsOnUnit(rec record.Record, u unit.Unit) error {
	var p record.Problems
	for i, o := range rec.Ledger {
		if !o.Unit.IsZero() && o.Unit != u {
			p = append(p, fmt.Sprintf("ledger[%d] unit: ops act on this turn's unit (%s); leave unit out", i, u.Label()))
		}
	}
	if len(p) == 0 {
		return nil
	}
	return p
}

func lateNote(b *turn.Book, t *turn.Turn) string {
	switch t.State {
	case turn.Dismissed:
		return "this turn was dismissed; your record changes nothing"
	case turn.Replaced:
		if by, ok := bySeq(b, t.ReplacedBy); ok {
			return fmt.Sprintf("this turn was replaced by turn %s; your record changes nothing", by.ID)
		}
		return "this turn was replaced; your record changes nothing"
	}
	return ""
}

// owns says whether the caller may record t: its session, the session it was
// sent to, or any caller without a handle for a turn by hand started outside
// Orca.
func owns(t *turn.Turn, handle string) bool {
	if handle == "" {
		return t.Session == "" && t.SentTo == ""
	}
	return t.Session == handle || t.SentTo == handle
}

func (s *Service) newestOf(ctx context.Context, handle string, ref unit.Unit, seq int64) error {
	if handle == "" {
		return nil
	}
	hb, ok, err := s.Store.BookOf(ctx, Key{Session: handle})
	if err != nil || !ok {
		return err
	}
	if newest, ok := sessionTurn(&hb, handle); ok && (hb.Unit != ref || newest.Seq != seq) {
		return Refusal{fmt.Sprintf("this session's turn is now %s; record that one", newest.ID)}
	}
	return nil
}

// SessionStart is what the SessionStart hook reports; Agent is the hook's
// agent.
type SessionStart struct {
	Session, Pane, WorkspaceID, AgentSession, Source, Cwd, Home string
	Agent, Transcript                                           string
}

// SessionStarted upserts the session the SessionStart hook reports, filling
// empty fields and never clearing one.
func (s *Service) SessionStarted(ctx context.Context, st SessionStart) error {
	if st.Session == "" {
		return errors.New("a session start needs a handle")
	}
	var saved turn.Session
	err := s.saveSession(ctx, st.Session, func(cur *turn.Session, now time.Time) (string, string) {
		for _, f := range []struct {
			dst *string
			src string
		}{{&cur.Pane, st.Pane}, {&cur.WorkspaceID, st.WorkspaceID}, {&cur.Worktree, st.Cwd}, {&cur.Home, st.Home}} {
			if *f.dst == "" {
				*f.dst = f.src
			}
		}
		if st.AgentSession != "" {
			cur.AgentSession = st.AgentSession
		}
		if st.Source == "startup" || st.Source == "resume" {
			cur.StartedAt = now
		}
		saved = *cur
		return KindSessionStarted, st.Session + ":" + now.Format(time.RFC3339Nano)
	})
	if err != nil {
		return err
	}
	s.saveAgentSession(ctx, AgentSessionInfo{ID: st.AgentSession, Kind: st.Agent, Session: st.Session,
		Home: saved.Home, Worktree: saved.Worktree, Transcript: st.Transcript})
	return nil
}

// MCPSeen notes an MCP initialize from the process the handle addresses; it
// may arrive before the SessionStart hook, and before the loop bound the
// handle of a session it started, which then takes it when bound.
func (s *Service) MCPSeen(ctx context.Context, handle string) error {
	if handle == "" {
		return errors.New("an MCP sighting needs a handle")
	}
	id, ok, err := s.SessionOfHandle(ctx, handle)
	if err != nil {
		return err
	}
	if !ok {
		s.mu.Lock()
		s.noteUnboundMCP(handle, s.now())
		s.mu.Unlock()
		return nil
	}
	return s.saveSession(ctx, id, func(cur *turn.Session, now time.Time) (string, string) {
		cur.MCPAt = now
		return KindMCPSeen, id + ":" + now.Format(time.RFC3339Nano)
	})
}

func (s *Service) saveSession(ctx context.Context, handle string, apply func(cur *turn.Session, now time.Time) (kind, key string)) error {
	s.mu.Lock()
	ev, err := s.applySession(ctx, handle, apply)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	s.publish(ev)
	return nil
}

func (s *Service) applySession(ctx context.Context, handle string, apply func(cur *turn.Session, now time.Time) (kind, key string)) (port.Event, error) {
	cur, ok, err := s.Registry.Session(ctx, handle)
	if err != nil {
		return port.Event{}, err
	}
	if !ok {
		cur = turn.Session{ID: handle}
	}
	now := s.now()
	kind, key := apply(&cur, now)
	ev := s.event(unit.Unit{}, kind, key, "")
	return ev, s.Registry.SaveSession(ctx, cur, ev)
}
