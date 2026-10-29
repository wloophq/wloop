package turns

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// Service runs the turn use cases: the loop sends, starts, records, answers,
// resumes and reconciles turns, with the calling session as identity.
type Service struct {
	Store    Store
	Registry Registry
	// AgentSessions is nil when nothing keeps agent sessions.
	AgentSessions AgentSessions
	Sessions      Sessions
	Launcher      Launcher
	// Answers are the user's answers asked turns reconcile with; nil reads
	// none.
	Answers Answers
	// Changed tells the views the pull requests whose turns changed; nil
	// does nothing.
	Changed func(ctx context.Context, refs ...unit.Unit)
	Pub     port.Publisher
	Clock   port.Clock
	// Command is how messages name the binary.
	Command string
	// Chats are the chat messages a session's stop tells of; nil tells of none.
	Chats Chats
	// Notices words what the loop tells a session; nil names each notice and its data.
	Notices Notices
	// FastJob is the configured interval of the fast job Reconcile runs on.
	FastJob time.Duration
	// AgentFor is the agent the sessions of the unit's project run, the
	// top-level kind for the zero unit, nil for none; with nil, a session_closed
	// line names no resume command.
	AgentFor func(u unit.Unit) Resumer
	// Intents is the loaded intents; nil is a wiring error that fails the
	// calls that need them.
	Intents Intents
	// Log reports what fails without failing the call; nil drops it.
	Log func(format string, args ...any)
	// Prompts is nil when no transcript can be read.
	Prompts Prompts
	// Items is nil when records' ledger ops are not applied.
	Items Ledger
	// Gates opens the gate an intent's record.missing names when a turn of it
	// ends without the record it requires; nil opens none.
	Gates Gates
	// Results attaches a done record's outcome url to the turn's entry when
	// its intent's record.attach_url says so; nil attaches nothing.
	Results Results
	// Usage is where EndHeadless writes a headless turn's usage; nil writes none.
	Usage UsageWriter
	// NewID mints session ids; nil mints random UUIDs.
	NewID func() string
	// Headless runs a headless turn again after the user answered its question; said is the answer as
	// Book.Said renders it. Set by the wiring to headless.Service.Again; nil routes the answer refused.
	Headless func(ctx context.Context, q turn.Turn, said string) (AnswerResult, error)

	// mu serializes every load, apply and save, so a write re-reads what an
	// earlier one stored. It is never held across Sessions.Type or
	// Launcher.Launch, which call back into the service.
	mu       sync.Mutex
	doneSeen map[string]time.Time
	goneSeen map[string]goneMark
	// gone is the sessions the last Census found missing long enough.
	gone map[string]bool
	// typing counts the lines being typed into each handle.
	typing map[string]int
	// delivering holds the ids of the queued answers a Deliver is routing.
	delivering map[int64]struct{}
	// begun maps the headless turns this process began to their units.
	begun map[int64]unit.Unit
	// unboundMCP holds the MCP initializes of handles no session took yet.
	unboundMCP map[string]time.Time
	// unrecorded are the turns that ended without a record since the last
	// openMissing.
	unrecorded []endedTurn
}

// Refusal is a request the turn's state or the caller's identity refuses.
type Refusal struct{ Reason string }

func (r Refusal) Error() string { return r.Reason }

// InvalidAnswer is an answer the question does not take.
type InvalidAnswer struct{ Reason string }

func (e InvalidAnswer) Error() string { return e.Reason }

// ErrNoIntents is a wiring error: the service has no Intents port.
var ErrNoIntents = errors.New("turns unavailable: no intents are wired")

func (s *Service) answerable() ([]string, error) {
	if s.Intents == nil {
		return nil, ErrNoIntents
	}
	return s.Intents.Answerable(), nil
}

func (s *Service) now() time.Time { return s.Clock.Now() }

func (s *Service) book(ctx context.Context, u unit.Unit) (turn.Book, error) {
	books, err := s.Store.Books(ctx, u)
	if err != nil {
		return turn.Book{}, err
	}
	b := books[u]
	b.Unit = u
	return b, nil
}

func (s *Service) publish(evs ...port.Event) {
	if s.Pub != nil && len(evs) > 0 {
		s.Pub.Publish(evs...)
	}
}

func (s *Service) changed(ctx context.Context, units ...unit.Unit) {
	if s.Changed != nil && len(units) > 0 {
		s.Changed(ctx, units...)
	}
}

func (s *Service) event(u unit.Unit, kind, key, data string) port.Event {
	return port.Event{Unit: u, Kind: kind, Key: key, Data: data, At: s.now()}
}

func bySeq(b *turn.Book, seq int64) (*turn.Turn, bool) {
	for i := len(b.Turns) - 1; i >= 0; i-- {
		if b.Turns[i].Seq == seq {
			return &b.Turns[i], true
		}
	}
	return nil, false
}

// sessionTurn is the handle's newest open turn: started on it, or sent to it
// and not started yet.
func sessionTurn(b *turn.Book, handle string) (*turn.Turn, bool) {
	if handle == "" {
		return nil, false
	}
	for i := len(b.Turns) - 1; i >= 0; i-- {
		t := &b.Turns[i]
		if t.State.Open() && (t.Session == handle || (t.State == turn.Sent && t.SentTo == handle)) {
			return t, true
		}
	}
	return nil, false
}

func lastRecordID(b *turn.Book, seq int64) int64 {
	if r, ok := b.LastRecord(seq); ok {
		return r.ID
	}
	return 0
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

func short(hash string) string {
	if len(hash) > 12 {
		return hash[:12]
	}
	return hash
}

// unitArg names a unit as the loop's commands read it.
func unitArg(u unit.Unit) string { return u.Address() }
