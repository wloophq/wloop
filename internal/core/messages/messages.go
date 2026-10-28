// Package messages keeps the subjects of the question, post and proposal gates: a turn's question, an agent's
// post and a proposal. Each open message waits on its gate; resolving the gate delivers its answer to the
// session or applies its decision, and closes the message.
package messages

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/wloophq/wloop/internal/core/gates"
	coreitems "github.com/wloophq/wloop/internal/core/items"
	"github.com/wloophq/wloop/internal/core/port"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/gate"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/message"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// Books reads the turn books, as turns.Store does.
type Books interface {
	Books(ctx context.Context, units ...unit.Unit) (map[unit.Unit]turn.Book, error)
	BookOf(ctx context.Context, k turns.Key) (turn.Book, bool, error)
}

// Turns answers, continues and dismisses a unit's question and notes the answers an agent read, as
// turns.Service does.
type Turns interface {
	Answer(ctx context.Context, r turns.AnswerRequest) (turns.AnswerResult, error)
	Continue(ctx context.Context, u unit.Unit) error
	Dismiss(ctx context.Context, u unit.Unit) error
	MarkRead(ctx context.Context, u unit.Unit, ids []int64) error
}

// Ledger is the items ledger's proposals.
type Ledger interface {
	Proposals(ctx context.Context, state coreitems.ProposalState) ([]coreitems.Proposal, error)
	Decide(ctx context.Context, id int64, accept bool) (coreitems.Result, error)
}

// Tasks names the capture task a message on no unit gets after its text, and closes the message task a
// message of an earlier version holds, as tasks.Service does.
type Tasks interface {
	Close(ctx context.Context, id task.ID, to task.Category) error
	Task(ctx context.Context, id task.ID) (task.Task, error)
	Retitle(ctx context.Context, id task.ID, title string) error
}

// Gates are the gates the messages are the subjects of, as gates.Service opens and supersedes them.
type Gates interface {
	Open(ctx context.Context, g gate.Gate, p gates.Policy) (gate.Gate, error)
	Supersede(ctx context.Context, id int64, by task.Actor) error
	Where(ctx context.Context, q gates.Query) ([]gate.Gate, error)
}

// Intents names the intents whose questions an answer addresses, and the missing gate of each.
type Intents interface {
	Answerable() []string
	Rules(intent string) (dintent.RecordRules, error)
}

// PostsPerTurn is how many agent posts of one turn may be open at once.
const PostsPerTurn = 3

var (
	ErrNoMessage = errors.New("no such message")
	// ErrClosed is an answer to a message that is no longer open.
	ErrClosed = errors.New("the message is closed")
	// ErrNoStartedTurn refuses task_post from a session with no started turn.
	ErrNoStartedTurn = errors.New("this session has no started turn")
)

// Capped refuses a post over PostsPerTurn.
type Capped struct{ Max int }

func (c Capped) Error() string {
	return fmt.Sprintf("this turn already has %d open posts, the most it may have; wait for the user's answers", c.Max)
}

type Service struct {
	Store   message.Store
	Books   Books
	Turns   Turns
	Ledger  Ledger // nil: no proposal messages
	Tasks   Tasks
	Gates   Gates
	Intents Intents
	Clock   port.Clock
	Log     func(format string, args ...any)
	// OnAnswer runs when a core or workflow proposal's gate is resolved, before it closes; nil only closes it.
	OnAnswer func(ctx context.Context, m message.Message, a Answer) error
	// ProposalText words a ledger proposal's message, as the profile's proposal gate does; nil leaves it
	// without text.
	ProposalText func(p coreitems.Proposal) string
	// ChatDeps serve the chat on the tasks; zero serves none.
	ChatDeps ChatDeps

	mu sync.Mutex
	// gating serializes opening the messages' gates, so a message gets one.
	gating sync.Mutex
}

func (s *Service) now() time.Time { return s.Clock.Now() }

func (s *Service) logf(format string, args ...any) {
	if s.Log != nil {
		s.Log(format, args...)
	}
}

type turnKey struct {
	unit unit.Unit
	seq  int64
}

// Reconcile makes the open question messages match the books: one per turn whose newest record is asked,
// each with its open gate; others closed, their gates superseded. A missing gate whose turn no longer
// waits on the user is superseded too. With no units it reconciles every book.
func (s *Service) Reconcile(ctx context.Context, units ...unit.Unit) error {
	gated, missing, err := s.reconcile(ctx, units)
	errs := []error{err, s.gate(ctx, gated)}
	for _, g := range missing {
		if _, err := s.OpenMissing(ctx, g); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// OpenMissing opens g, the missing gate of a turn that failed or ended without its record, unless one with
// its subject is open on its unit already; it returns the open one. A gate on a task that closed moves off it,
// as a message's does, and a run whose own task closed asks nothing more: it returns no gate.
func (s *Service) OpenMissing(ctx context.Context, g gate.Gate) (gate.Gate, error) {
	s.gating.Lock()
	defer s.gating.Unlock()
	open, err := s.Gates.Where(ctx, gates.Query{Kind: gate.KindQuestion, Unit: g.Unit})
	if err != nil {
		return gate.Gate{}, err
	}
	if i := slices.IndexFunc(open, func(o gate.Gate) bool { return o.Unit == g.Unit && o.Subject == g.Subject }); i >= 0 {
		if !s.closedTask(ctx, open[i].Task) {
			return open[i], nil
		}
		if err := s.Gates.Supersede(ctx, open[i].ID, task.ActorWorkflow); err != nil {
			return gate.Gate{}, fmt.Errorf("moving missing gate %d off its closed task: %w", open[i].ID, err)
		}
	}
	if own, ok := task.IDOf(g.Unit); ok && s.closedTask(ctx, own) {
		return gate.Gate{}, nil
	}
	if s.closedTask(ctx, g.Task) {
		g.Task = ""
	}
	return s.Gates.Open(ctx, g, nil)
}

// missingOf is the missing gate b's question turn calls for: one that stopped, still waits on the user, and
// whose intent names a missing gate.
func (s *Service) missingOf(u unit.Unit, b *turn.Book, answerable []string) (gate.Gate, bool) {
	q, ok := b.Question(answerable)
	if !ok || !q.State.Stopped() || !waits(b, q.ID) {
		return gate.Gate{}, false
	}
	rules, err := s.Intents.Rules(string(q.Intent))
	if err != nil || rules.Missing == "" {
		return gate.Gate{}, false
	}
	g := gate.Gate{Unit: u, Name: rules.Missing, Subject: turns.MissingSubject(q.ID)}
	if id, ok := task.IDOf(u); ok {
		g.Task = id
	}
	return g, true
}

func (s *Service) reconcile(ctx context.Context, units []unit.Unit) ([]message.Message, []gate.Gate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	books, err := s.Books.Books(ctx, units...)
	if err != nil {
		return nil, nil, err
	}
	open, err := s.Store.Open(ctx)
	if err != nil {
		return nil, nil, err
	}
	answerable := s.Intents.Answerable()
	want := map[turnKey]message.Message{}
	var missing []gate.Gate
	for u, b := range books {
		if m, ok := questionOf(u, b, answerable, s.now()); ok {
			want[turnKey{u, m.Origin.TurnSeq}] = m
		}
		if g, ok := s.missingOf(u, &b, answerable); ok {
			missing = append(missing, g)
		}
	}
	slices.SortFunc(missing, func(a, b gate.Gate) int { return strings.Compare(a.Unit.String(), b.Unit.String()) })
	inScope := func(u unit.Unit) bool { return len(units) == 0 || slices.Contains(units, u) }
	var errs []error
	var gated []message.Message
	for _, m := range open {
		if !inScope(m.Unit) {
			continue
		}
		switch m.Kind {
		case message.AgentPost:
			if b, ok := books[m.Unit]; ok && !postable(&b, m) {
				errs = append(errs, s.close(ctx, m, ""))
				continue
			}
			gated = append(gated, m)
		case message.Question:
			k := turnKey{m.Unit, m.Origin.TurnSeq}
			w, ok := want[k]
			if !ok {
				b := books[m.Unit]
				errs = append(errs, s.close(ctx, m, closing(&b, m.Origin.TurnSeq)))
				continue
			}
			delete(want, k)
			w.ID, w.TaskID, w.CreatedAt = m.ID, m.TaskID, m.CreatedAt
			if !sameContent(w, m) {
				if m, err = s.Store.Upsert(ctx, w); err != nil {
					errs = append(errs, err)
					continue
				}
			}
			gated = append(gated, m)
		}
	}
	keys := make([]turnKey, 0, len(want))
	for k := range want {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b turnKey) int {
		switch {
		case a.unit.Less(b.unit):
			return -1
		case b.unit.Less(a.unit):
			return 1
		}
		return int(a.seq - b.seq)
	})
	for _, k := range keys {
		m, err := s.Store.Upsert(ctx, want[k])
		if err != nil {
			errs = append(errs, err)
			continue
		}
		gated = append(gated, m)
	}
	errs = append(errs, s.missing(ctx, books, inScope))
	return gated, missing, errors.Join(errs...)
}

// missing supersedes each open missing gate on books whose turn no longer waits on the user: it ran again,
// was answered or dismissed, or a newer turn of its intent took its place.
func (s *Service) missing(ctx context.Context, books map[unit.Unit]turn.Book, inScope func(unit.Unit) bool) error {
	open, err := s.Gates.Where(ctx, gates.Query{Kind: gate.KindQuestion})
	if err != nil {
		return err
	}
	var errs []error
	for _, g := range open {
		id, ok := turns.MissingTurn(g.Subject)
		if !ok || !inScope(g.Unit) {
			continue
		}
		b, ok := books[g.Unit]
		if !ok || waits(&b, id) {
			continue
		}
		errs = append(errs, s.Gates.Supersede(ctx, g.ID, task.ActorWorkflow))
	}
	return errors.Join(errs...)
}

// waits says whether turn id of b still waits on the user: it stopped, no answer came since its last record,
// and it is its intent's newest turn.
func waits(b *turn.Book, id string) bool {
	t, ok := b.ByID(id)
	if !ok || !t.State.Stopped() {
		return false
	}
	if newest, ok := b.Newest(t.Intent); ok && newest.Seq != t.Seq {
		return false
	}
	var since time.Time
	if last, ok := b.LastRecord(t.Seq); ok {
		since = last.At
	}
	return !answeredSince(b, t.Seq, since)
}

// postable says whether an agent post can still take an answer: its turn is still started, so the answer is
// stored before any record of that turn and never reads as the answer to the turn's question.
func postable(b *turn.Book, m message.Message) bool {
	for _, t := range b.Turns {
		if t.Seq == m.Origin.TurnSeq {
			return t.State == turn.Started
		}
	}
	return false
}

func sameContent(a, b message.Message) bool {
	return a.Text == b.Text && a.Recommend == b.Recommend && a.Confirm == b.Confirm && a.Origin.Session == b.Origin.Session &&
		slices.Equal(a.Options, b.Options)
}

// questionOf is the open question message the book calls for on u, as the unit's row shows it: the turn an
// answer addresses, when it is asked and has no answer since its last record. A turn that failed or ended
// without its record asks through its intent's missing gate instead.
func questionOf(u unit.Unit, b turn.Book, answerable []string, now time.Time) (message.Message, bool) {
	q, ok := b.Question(answerable)
	if !ok || q.State != turn.Asked {
		return message.Message{}, false
	}
	last, recorded := b.LastRecord(q.Seq)
	if recorded && answeredSince(&b, q.Seq, last.At) {
		return message.Message{}, false
	}
	m := message.Message{Unit: u, Kind: message.Question, CreatedAt: now,
		Origin: message.Origin{Kind: message.OriginTurn, TurnSeq: q.Seq, Session: q.Session}}
	switch {
	case recorded && last.Record.Question != nil:
		rq := last.Record.Question
		m.Text, m.Options, m.Recommend, m.Confirm = rq.Text, slices.Clone(rq.Options), rq.Recommend, rq.Kind == "confirm"
	case recorded:
		m.Text = strings.TrimSpace(firstOf(last.Record.Summary, last.Record.Resume))
	}
	if recorded && last.Record.Suspicious {
		m.Text = record.SuspectedInjection + m.Text
	}
	m.Text = clean(m.Text)
	return m, true
}

func firstOf(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

// answeredSince says whether turn seq holds an answer the user gave since since that was not refused.
func answeredSince(b *turn.Book, seq int64, since time.Time) bool {
	_, ok := lastAnswer(b, seq, since)
	return ok
}

func lastAnswer(b *turn.Book, seq int64, since time.Time) (turn.Answer, bool) {
	for i := len(b.Answers) - 1; i >= 0; i-- {
		a := b.Answers[i]
		if a.TurnSeq == seq && a.Route != turn.Refused && a.Route != turn.Direct && !a.At.Before(since) {
			return a, true
		}
	}
	return turn.Answer{}, false
}

// closing is the answer a question message of turn seq closes with: the one the agent reads when the user
// answered it without its gate, "" when the turn was dismissed or a newer turn, record or outcome took it.
func closing(b *turn.Book, seq int64) string {
	var since time.Time
	if last, ok := b.LastRecord(seq); ok {
		since = last.At
	}
	if a, ok := lastAnswer(b, seq, since); ok {
		return oneLine(b.Said(a))
	}
	return ""
}

// close supersedes m's open gate and closes m with answer.
func (s *Service) close(ctx context.Context, m message.Message, answer string) error {
	if g, ok, err := s.openGate(ctx, m); err != nil {
		return err
	} else if ok {
		if err := s.Gates.Supersede(ctx, g.ID, task.ActorWorkflow); err != nil && !errors.Is(err, gates.ErrResolved) {
			return fmt.Errorf("superseding the gate of message %d: %w", m.ID, err)
		}
	}
	return s.closeMessage(ctx, m, answer)
}

// closeMessage closes m with answer, and the message task an earlier version opened for it: done when
// answered, dropped otherwise. A task that refuses to move keeps the message open, so the next reconcile
// tries again.
func (s *Service) closeMessage(ctx context.Context, m message.Message, answer string) error {
	if m.TaskID != "" {
		to := task.CategoryDropped
		if answer != "" {
			to = task.CategoryDone
		}
		e, err := s.Tasks.Task(ctx, task.ID(m.TaskID))
		switch {
		case errors.Is(err, coretasks.ErrNoTask):
		case err != nil:
			return err
		case e.Kind == task.KindMessage:
			if err := s.Tasks.Close(ctx, e.ID, to); err != nil {
				return fmt.Errorf("closing the task of message %d: %w", m.ID, err)
			}
		}
	}
	return s.Store.Close(ctx, m.ID, answer, s.now())
}

// subjectOf is the Subject of m's gates.
func subjectOf(m message.Message) string { return strconv.FormatInt(m.ID, 10) }

// openGate is m's open gate; ok is false when none is open.
func (s *Service) openGate(ctx context.Context, m message.Message) (gate.Gate, bool, error) {
	open, err := s.Gates.Where(ctx, gates.Query{Kind: m.Kind.Gate(), Unit: m.Unit})
	if err != nil {
		return gate.Gate{}, false, err
	}
	i := slices.IndexFunc(open, func(g gate.Gate) bool { return g.Unit == m.Unit && g.Subject == subjectOf(m) })
	if i < 0 {
		return gate.Gate{}, false, nil
	}
	return open[i], true, nil
}

// gate gives each of ms that is still open its gate when it has none open.
func (s *Service) gate(ctx context.Context, ms []message.Message) error {
	var errs []error
	for _, m := range ms {
		if _, err := s.gated(ctx, m); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// gated is m with its gate open: the one it has while its task is open, else a new one on m's task, or on
// its unit's open task, or on a capture task named after m's text when m is on no unit. A ledger proposal's
// message supersedes the gate its proposal waited on before the message existed.
func (s *Service) gated(ctx context.Context, m message.Message) (message.Message, error) {
	s.gating.Lock()
	defer s.gating.Unlock()
	if g, ok, err := s.openGate(ctx, m); err != nil {
		return m, err
	} else if ok {
		if !s.closedTask(ctx, g.Task) {
			return m, nil
		}
		if err := s.Gates.Supersede(ctx, g.ID, task.ActorWorkflow); err != nil {
			return m, fmt.Errorf("moving the gate of message %d off its closed task: %w", m.ID, err)
		}
		m.TaskID = ""
	}
	if cur, ok, err := s.Store.Message(ctx, m.ID); err != nil {
		return m, err
	} else if !ok || !cur.Open() {
		return m, nil
	}
	if m.Origin.Kind == message.OriginLedger {
		if err := s.supersedeProposal(ctx, m.Unit, m.Origin.Proposal); err != nil {
			return m, err
		}
	}
	g, err := s.Gates.Open(ctx, gate.Gate{Kind: m.Kind.Gate(), Unit: m.Unit, Task: task.ID(m.TaskID), Subject: subjectOf(m)}, nil)
	if err != nil {
		return m, fmt.Errorf("opening the gate of message %d: %w", m.ID, err)
	}
	if m.Unit.IsZero() && m.TaskID == "" && g.Task != "" {
		if err := s.Tasks.Retitle(ctx, g.Task, title(m.Text)); err != nil {
			s.logf("messages: naming the task of message %d: %v", m.ID, err)
		}
	}
	if g.Task == "" || string(g.Task) == m.TaskID {
		return m, nil
	}
	m.TaskID = string(g.Task)
	return s.Store.Upsert(ctx, m)
}

// closedTask says whether task id is done or dropped, where no list shows the gate it holds.
func (s *Service) closedTask(ctx context.Context, id task.ID) bool {
	if id == "" || s.Tasks == nil {
		return false
	}
	e, err := s.Tasks.Task(ctx, id)
	if err != nil {
		return false
	}
	return e.Category == task.CategoryDone || e.Category == task.CategoryDropped
}

// supersedeProposal supersedes the open gate item proposal id waited on before its message existed.
func (s *Service) supersedeProposal(ctx context.Context, u unit.Unit, id int64) error {
	open, err := s.Gates.Where(ctx, gates.Query{Kind: gate.KindProposal, Unit: u})
	if err != nil {
		return err
	}
	for _, g := range open {
		if g.Subject == gate.ProposalSubject(id) {
			if err := s.Gates.Supersede(ctx, g.ID, task.ActorWorkflow); err != nil {
				return fmt.Errorf("superseding the gate of item proposal %d: %w", id, err)
			}
		}
	}
	return nil
}

// ReconcileProposals makes one open proposal message, with its gate, per open items.Proposal; one whose
// proposal is no longer open closes.
func (s *Service) ReconcileProposals(ctx context.Context) error {
	if s.Ledger == nil {
		return nil
	}
	gated, err := s.reconcileProposals(ctx)
	return errors.Join(err, s.gate(ctx, gated))
}

func (s *Service) reconcileProposals(ctx context.Context) ([]message.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ps, err := s.Ledger.Proposals(ctx, coreitems.ProposalOpen)
	if err != nil {
		return nil, err
	}
	open, err := s.Store.Open(ctx)
	if err != nil {
		return nil, err
	}
	pending := map[int64]bool{}
	for _, p := range ps {
		pending[p.ID] = true
	}
	var errs []error
	var gated []message.Message
	have := map[int64]bool{}
	for _, m := range open {
		if m.Origin.Kind != message.OriginLedger {
			continue
		}
		have[m.Origin.Proposal] = true
		if !pending[m.Origin.Proposal] {
			answer, err := s.decidedIntent(ctx, m)
			if err == nil {
				err = s.close(ctx, m, answer)
			}
			errs = append(errs, err)
			continue
		}
		gated = append(gated, m)
	}
	for _, p := range ps {
		if have[p.ID] {
			continue
		}
		m, err := s.Store.Upsert(ctx, s.proposalMessage(p))
		if err != nil {
			errs = append(errs, err)
			continue
		}
		gated = append(gated, m)
	}
	return gated, errors.Join(errs...)
}

// decidedIntent is the intent m's newest gate closed with, "" when none closed it.
func (s *Service) decidedIntent(ctx context.Context, m message.Message) (string, error) {
	all, err := s.Gates.Where(ctx, gates.Query{Kind: m.Kind.Gate(), Unit: m.Unit, Statuses: []gate.Status{gate.Approved, gate.Rejected, gate.Answered, gate.Dismissed}})
	if err != nil {
		return "", err
	}
	answer := ""
	for _, g := range all {
		if g.Subject == subjectOf(m) {
			answer = g.Intent
		}
	}
	return answer, nil
}

// proposalMessage is the message of ledger proposal p, worded by ProposalText.
func (s *Service) proposalMessage(p coreitems.Proposal) message.Message {
	m := message.Message{Unit: p.Unit, Kind: message.Proposal, CreatedAt: s.now(), Origin: message.Origin{Kind: message.OriginLedger, Proposal: p.ID}}
	if s.ProposalText != nil {
		m.Text = clean(s.ProposalText(p))
	}
	return m
}

// Post is task_post: an open agent_post message, with its gate, on the unit of the session's started turn.
func (s *Service) Post(ctx context.Context, p PostRequest) (message.Message, error) {
	if err := checkPost(p); err != nil {
		return message.Message{}, err
	}
	b, ok, err := s.Books.BookOf(ctx, turns.Key{Session: p.Session})
	if err != nil {
		return message.Message{}, err
	}
	var t *turn.Turn
	if ok {
		t, ok = b.OfSession(p.Session)
	}
	if !ok || t.State != turn.Started || t.Session != p.Session {
		return message.Message{}, ErrNoStartedTurn
	}
	m, err := s.post(ctx, b.Unit, t.Seq, p)
	if err != nil {
		return message.Message{}, err
	}
	return s.gated(ctx, m)
}

func (s *Service) post(ctx context.Context, u unit.Unit, seq int64, p PostRequest) (message.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	open, err := s.Store.Open(ctx)
	if err != nil {
		return message.Message{}, err
	}
	n := 0
	for _, m := range open {
		if m.Kind == message.AgentPost && m.Unit == u && m.Origin.TurnSeq == seq {
			n++
		}
	}
	if n >= PostsPerTurn {
		return message.Message{}, Capped{PostsPerTurn}
	}
	return s.Store.Upsert(ctx, message.Message{Unit: u, Kind: message.AgentPost, Text: strings.TrimSpace(p.Text),
		Options: slices.Clone(p.Options), Recommend: p.Recommend, Confirm: p.Confirm, CreatedAt: s.now(),
		Origin: message.Origin{Kind: message.OriginAgent, TurnSeq: seq, Session: p.Session}})
}

// checkPost applies a turn record's question rules to a post: kind choice with options, confirm, else free.
func checkPost(p PostRequest) error {
	q := record.Question{Kind: "free", Text: p.Text, Options: p.Options, Recommend: p.Recommend}
	switch {
	case p.Confirm:
		q.Kind = "confirm"
	case len(p.Options) > 0:
		q.Kind = "choice"
	}
	rec := record.Record{State: record.Asked, Summary: "post", Resume: "post", Question: &q}
	var problems record.Problems
	if !errors.As(rec.ValidateShape(), &problems) {
		return nil
	}
	var out record.Problems
	for _, line := range problems {
		if rest, ok := strings.CutPrefix(line, "question."); ok {
			out = append(out, rest)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// Propose opens a proposal message of a workflow or core rule, with its gate, one per unit and rule while it
// is open; with TaskID its gate is on that task.
func (s *Service) Propose(ctx context.Context, p ProposeRequest) (message.Message, error) {
	if p.Origin.Kind != message.OriginCore && p.Origin.Kind != message.OriginWorkflow {
		return message.Message{}, fmt.Errorf("a proposal comes from a core rule or a workflow, not %q", p.Origin.Kind)
	}
	if strings.TrimSpace(p.Origin.Workflow) == "" {
		return message.Message{}, errors.New("a proposal names its rule or workflow")
	}
	if strings.TrimSpace(p.Text) == "" {
		return message.Message{}, errors.New("a proposal needs its text")
	}
	s.mu.Lock()
	m, err := s.Store.Upsert(ctx, message.Message{Unit: p.Unit, TaskID: p.TaskID, Kind: message.Proposal, Text: clean(p.Text),
		Options: slices.Clone(p.Options), Origin: p.Origin, CreatedAt: s.now()})
	s.mu.Unlock()
	if err != nil {
		return message.Message{}, err
	}
	return s.gated(ctx, m)
}

// Delivery is what the caller of a gate's resolution learns of the answer it delivered: the Source it gives
// the answer, and the Route the answer took.
type Delivery struct {
	Source turn.Source
	Route  turns.AnswerResult
}

type deliveryKey struct{}

// WithDelivery is ctx carrying d, which Deliver reads the answer's source from and fills with its route.
func WithDelivery(ctx context.Context, d *Delivery) context.Context {
	return context.WithValue(ctx, deliveryKey{}, d)
}

// delivery is ctx's Delivery, a fresh one when it carries none.
func delivery(ctx context.Context) *Delivery {
	if d, ok := DeliveryOf(ctx); ok {
		return d
	}
	return &Delivery{}
}

// DeliveryOf is the Delivery ctx carries; false when it carries none.
func DeliveryOf(ctx context.Context) (*Delivery, bool) {
	d, ok := ctx.Value(deliveryKey{}).(*Delivery)
	return d, ok && d != nil
}

// Deliver is a resolved question or post gate's consequence: the answer goes to the session that asked, a
// dismissal dismisses the question, and the message closes. A missing gate's answer resumes its turn with it,
// an answer with no text resumes it as it was, and a dismissal dismisses it.
func (s *Service) Deliver(ctx context.Context, g gate.Gate) error {
	if id, ok := turns.MissingTurn(g.Subject); ok {
		return s.deliverMissing(ctx, g, id)
	}
	m, err := s.subject(ctx, g)
	if err != nil {
		return err
	}
	d := delivery(ctx)
	switch {
	case m.Kind == message.AgentPost:
		return s.deliverPost(ctx, g, m, d)
	case g.Status == gate.Dismissed || g.Status == gate.Rejected:
		if err := s.Turns.Dismiss(ctx, m.Unit); err != nil && !errors.Is(err, turn.ErrNoQuestion) {
			return err
		}
		return s.closeAfter(ctx, m, "")
	case strings.TrimSpace(g.Answer) == "":
		if err := s.Turns.Continue(ctx, m.Unit); err != nil {
			return err
		}
		return s.closeAfter(ctx, m, "")
	}
	a := AnswerOf(m, g.Answer)
	said, err := saidTo(m, a)
	if err != nil {
		return err
	}
	d.Route, err = s.Turns.Answer(ctx, turns.AnswerRequest{Unit: m.Unit, Message: m.ID, Gate: g.ID, Text: a.Text, OptionID: a.OptionID,
		Yes: a.Yes, No: a.No, Source: d.Source})
	var refused turns.Refusal
	if err != nil && !errors.As(err, &refused) {
		return err
	}
	return errors.Join(err, s.closeAfter(ctx, m, oneLine(said)))
}

// deliverPost answers m, an agent's post, into the session that posted it, or closes it unanswered.
func (s *Service) deliverPost(ctx context.Context, g gate.Gate, m message.Message, d *Delivery) error {
	if g.Status != gate.Answered {
		return s.closeAfter(ctx, m, "")
	}
	a := AnswerOf(m, g.Answer)
	said, err := saidTo(m, a)
	if err != nil {
		return err
	}
	req := turns.AnswerRequest{Unit: m.Unit, Message: m.ID, Gate: g.ID, Posted: true, Turn: m.Origin.TurnSeq, Text: a.Text, OptionID: a.OptionID,
		Yes: a.Yes, No: a.No, Source: d.Source}
	d.Route, err = s.Turns.Answer(ctx, req)
	var refused turns.Refusal
	switch {
	case errors.Is(err, turn.ErrClosed):
		return errors.Join(fmt.Errorf("%w: %w", ErrClosed, err), s.closeAfter(ctx, m, ""))
	case err != nil && !errors.As(err, &refused):
		return err
	}
	return errors.Join(err, s.closeAfter(ctx, m, oneLine(said)))
}

// closeAfter closes m with answer once its gate resolved, then reconciles its unit.
func (s *Service) closeAfter(ctx context.Context, m message.Message, answer string) error {
	s.mu.Lock()
	err := s.close(ctx, m, answer)
	s.mu.Unlock()
	if m.Unit.IsZero() {
		return err
	}
	if rerr := s.Reconcile(ctx, m.Unit); rerr != nil {
		s.logf("messages: reconciling %s after its gate: %v", m.Unit, rerr)
	}
	return err
}

// deliverMissing resumes or dismisses turn id, which failed or ended without its record.
func (s *Service) deliverMissing(ctx context.Context, g gate.Gate, id string) error {
	books, err := s.Books.Books(ctx, g.Unit)
	if err != nil {
		return err
	}
	b := books[g.Unit]
	if q, ok := b.Question(s.Intents.Answerable()); !ok || q.ID != id || !waits(&b, id) {
		return fmt.Errorf("%w: turn %s on %s no longer waits on you", turn.ErrNoQuestion, id, g.Unit.Label())
	}
	d := delivery(ctx)
	switch {
	case g.Status == gate.Dismissed || g.Status == gate.Rejected:
		return s.Turns.Dismiss(ctx, g.Unit)
	case strings.TrimSpace(g.Answer) == "":
		return s.Turns.Continue(ctx, g.Unit)
	}
	d.Route, err = s.Turns.Answer(ctx, turns.AnswerRequest{Unit: g.Unit, Gate: g.ID, Text: strings.TrimSpace(g.Answer), Source: d.Source})
	return err
}

// Reanswer answers question message id again, given its gate closed answered while its turn still asks:
// the newer answer replaces a queued one or adds to it, and rewords the message.
func (s *Service) Reanswer(ctx context.Context, id int64, a Answer) (turns.AnswerResult, error) {
	m, ok, err := s.Store.Message(ctx, id)
	switch {
	case err != nil:
		return turns.AnswerResult{}, err
	case !ok:
		return turns.AnswerResult{}, fmt.Errorf("%w: %d", ErrNoMessage, id)
	case m.Kind != message.Question || m.Open() || m.Answer == "":
		return turns.AnswerResult{}, fmt.Errorf("%w: message %d takes no newer answer", ErrClosed, id)
	}
	books, err := s.Books.Books(ctx, m.Unit)
	if err != nil {
		return turns.AnswerResult{}, err
	}
	b := books[m.Unit]
	q, asks := asking(&b, s.Intents.Answerable())
	if prev, ok := answeredQuestion(&b, m.Origin.TurnSeq); !asks || q.Seq != m.Origin.TurnSeq || !ok || prev != m.ID {
		return turns.AnswerResult{}, fmt.Errorf("%w: message %d", ErrClosed, id)
	}
	res, err := s.Turns.Answer(ctx, turns.AnswerRequest{Unit: m.Unit, Message: m.ID, Text: a.Text, OptionID: a.OptionID, Yes: a.Yes, No: a.No, Source: a.Source})
	if err != nil {
		return res, err
	}
	if books, err = s.Books.Books(ctx, m.Unit); err == nil {
		b = books[m.Unit]
		if said := closing(&b, m.Origin.TurnSeq); said != "" {
			err = s.Store.Close(ctx, m.ID, said, s.now())
		}
	}
	return res, err
}

// Decide is a resolved proposal gate's consequence: an item proposal is accepted when the gate is approved
// and rejected otherwise, a workflow's or core rule's goes to OnAnswer, and its message closes. A ledger
// proposal is decided under the lock ReconcileProposals holds, which would close its message unanswered; one
// whose operations the ledger refuses stays open on a new gate, and Decide returns ErrNotApplied.
func (s *Service) Decide(ctx context.Context, g gate.Gate) error {
	approved := g.Status == gate.Approved
	if id, ok := proposalOf(g.Subject); ok {
		if s.Ledger == nil {
			return errors.New("no ledger decides item proposals")
		}
		res, err := s.Ledger.Decide(ctx, id, approved)
		if err != nil || len(res.Refused) == 0 {
			return err
		}
		return s.notApplied(ctx, gate.Gate{Kind: gate.KindProposal, Unit: g.Unit, Task: g.Task, Subject: g.Subject}, res)
	}
	m, err := s.subject(ctx, g)
	if err != nil {
		return err
	}
	if m.Kind != message.Proposal {
		return fmt.Errorf("gate %d: message %d is a %s, not a proposal", g.ID, m.ID, m.Kind)
	}
	if m.Origin.Kind != message.OriginLedger {
		if s.OnAnswer != nil {
			if err := s.OnAnswer(ctx, m, Answer{Yes: approved, No: !approved}); err != nil {
				return err
			}
		}
		return s.closeAfter(ctx, m, g.Intent)
	}
	res, err := s.decideLedger(ctx, m, approved, g.Intent)
	if err != nil || len(res.Refused) == 0 {
		return err
	}
	reopen := gate.Gate{Kind: gate.KindProposal, Unit: m.Unit, Task: g.Task, Subject: subjectOf(m)}
	if cur, ok, err := s.Store.Message(ctx, m.ID); err != nil || !ok || !cur.Open() {
		reopen.Subject = gate.ProposalSubject(m.Origin.Proposal)
	}
	return s.notApplied(ctx, reopen, res)
}

// decideLedger decides m's item proposal and, when the ledger applies it, closes m with intent.
func (s *Service) decideLedger(ctx context.Context, m message.Message, approved bool, intent string) (coreitems.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var res coreitems.Result
	if s.Ledger != nil {
		var err error
		if res, err = s.Ledger.Decide(ctx, m.Origin.Proposal, approved); err != nil && !errors.Is(err, coreitems.ErrNoProposal) {
			return res, err
		}
	}
	if len(res.Refused) > 0 {
		return res, nil
	}
	return res, s.close(ctx, m, intent)
}

// ErrNotApplied is an approved proposal whose operations the ledger refused: it stays open, on a new gate.
var ErrNotApplied = errors.New("the approved proposal was not applied")

// notApplied opens g again, held for a person, so the proposal res refused reads open, and says why.
func (s *Service) notApplied(ctx context.Context, g gate.Gate, res coreitems.Result) error {
	why := make([]string, len(res.Refused))
	for i, r := range res.Refused {
		why[i] = r.Reason
	}
	err := fmt.Errorf("%w: %s", ErrNotApplied, strings.Join(why, "; "))
	s.gating.Lock()
	defer s.gating.Unlock()
	open, oerr := s.Gates.Where(ctx, gates.Query{Kind: gate.KindProposal, Unit: g.Unit})
	if oerr != nil {
		return errors.Join(err, oerr)
	}
	if slices.ContainsFunc(open, func(o gate.Gate) bool { return o.Unit == g.Unit && o.Subject == g.Subject }) {
		return err
	}
	if _, oerr := s.Gates.Open(ctx, g, holdForPerson{}); oerr != nil {
		return errors.Join(err, fmt.Errorf("opening its gate again: %w", oerr))
	}
	return err
}

// holdForPerson holds a gate for a person: a proposal the ledger refused waits for one, whatever releases
// proposals.
type holdForPerson struct{}

func (holdForPerson) Decide(context.Context, gate.Gate) (gates.Decision, error) {
	return gates.Decision{Mode: gate.ModeWait}, nil
}

// proposalPrefix is what gate.ProposalSubject puts before an item proposal's id.
var proposalPrefix = strings.TrimSuffix(gate.ProposalSubject(0), "0")

// proposalOf is the item proposal a gate on no message decides; false for any other subject.
func proposalOf(subject string) (int64, bool) {
	rest, ok := strings.CutPrefix(subject, proposalPrefix)
	if !ok {
		return 0, false
	}
	id, err := strconv.ParseInt(rest, 10, 64)
	return id, err == nil
}

// subject is the open message g is the gate of.
func (s *Service) subject(ctx context.Context, g gate.Gate) (message.Message, error) {
	id, err := strconv.ParseInt(g.Subject, 10, 64)
	if err != nil {
		return message.Message{}, fmt.Errorf("%w: gate %d names %q", ErrNoMessage, g.ID, g.Subject)
	}
	m, ok, err := s.Store.Message(ctx, id)
	switch {
	case err != nil:
		return message.Message{}, err
	case !ok:
		return message.Message{}, fmt.Errorf("%w: %d", ErrNoMessage, id)
	case !m.Open():
		return message.Message{}, fmt.Errorf("%w: message %d", ErrClosed, id)
	}
	return m, nil
}

// AnswerOf reads a gate's answer to m: a first line naming one of m's options, or yes or no on a confirm,
// chooses it, and the lines after it are the note; any other text is the answer itself.
func AnswerOf(m message.Message, text string) Answer {
	text = strings.TrimSpace(text)
	first, rest, _ := strings.Cut(text, "\n")
	first, rest = strings.TrimSpace(first), strings.TrimSpace(rest)
	switch {
	case slices.ContainsFunc(m.Options, func(o record.Option) bool { return o.ID == first }):
		return Answer{OptionID: first, Text: rest}
	case m.Confirm && first == yes:
		return Answer{Yes: true, Text: rest}
	case m.Confirm && first == no:
		return Answer{No: true, Text: rest}
	}
	return Answer{Text: text}
}

// The words of a confirm's answer.
const (
	yes = "yes"
	no  = "no"
)

// GateAnswer is a's text as a question or post gate takes it, which AnswerOf reads back: the option, or yes
// or no, on its first line, then the note.
func GateAnswer(a Answer) string {
	choice := a.OptionID
	switch {
	case choice == "" && a.Yes:
		choice = yes
	case choice == "" && a.No:
		choice = no
	}
	note := strings.TrimSpace(a.Text)
	switch {
	case choice == "":
		return note
	case note == "" || note == choice:
		return choice
	}
	return choice + "\n" + note
}

// saidTo is the answer to a message as the agent reads it, refusing what the message does not take as turns
// refuses it for a question.
func saidTo(m message.Message, a Answer) (string, error) {
	note := strings.TrimSpace(a.Text)
	var choice string
	switch {
	case a.OptionID != "":
		i := slices.IndexFunc(m.Options, func(o record.Option) bool { return o.ID == a.OptionID })
		if i < 0 {
			return "", turns.InvalidAnswer{Reason: fmt.Sprintf("message %d has no option %q", m.ID, a.OptionID)}
		}
		choice = m.Options[i].Answer()
	case a.Yes && a.No:
		return "", turns.InvalidAnswer{Reason: "an answer is yes or no, not both"}
	case (a.Yes || a.No) && !m.Confirm:
		return "", turns.InvalidAnswer{Reason: "yes and no answer only a confirm"}
	case a.Yes:
		choice = yes
	case a.No:
		choice = no
	}
	switch {
	case choice == "" && note == "" && m.Confirm:
		return no, nil
	case choice == "" && note == "":
		return "", turns.InvalidAnswer{Reason: "an answer needs the text, an option or yes"}
	case choice == "":
		return note, nil
	case note == "" || note == choice:
		return choice, nil
	}
	return choice + ". " + note, nil
}

const titleMax = 120

// title is the first line of text, cut to titleMax runes.
func title(text string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	if r := []rune(line); len(r) > titleMax {
		return string(r[:titleMax-1]) + "…"
	}
	return line
}

// clean drops control characters but newlines and tabs, and bidi overrides and isolates as record validation
// does, and cuts text to message.TextMax bytes.
func clean(text string) string {
	text = strings.Map(func(r rune) rune {
		if r != '\n' && r != '\t' && (unicode.IsControl(r) || (r >= 0x202A && r <= 0x202E) || (r >= 0x2066 && r <= 0x2069)) {
			return -1
		}
		return r
	}, strings.TrimSpace(text))
	if len(text) <= message.TextMax {
		return text
	}
	cut := message.TextMax
	for cut > 0 && text[cut]&0xC0 == 0x80 {
		cut--
	}
	return text[:cut]
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

type PostRequest struct {
	Session, Text, Recommend string
	Options                  []record.Option
	Confirm                  bool
}

type ProposeRequest struct {
	Unit    unit.Unit
	TaskID  string // the task its gate is on; "" is its unit's task, or a capture task with no unit
	Text    string
	Options []record.Option
	Origin  message.Origin // Kind core or workflow; Workflow names the rule (dedupe key with Unit)
}

type Answer struct {
	Text, OptionID string
	Yes, No        bool
	Source         turn.Source
}
