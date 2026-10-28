// Package messages keeps the Inbox messages: questions of turns, agent posts
// and proposals that ask a decision.
package messages

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	coreitems "github.com/wloophq/wloop/internal/core/items"
	"github.com/wloophq/wloop/internal/core/port"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/core/turns"
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

// Turns answers and dismisses a unit's question and notes the answers an agent read, as turns.Service does.
type Turns interface {
	Answer(ctx context.Context, r turns.AnswerRequest) (turns.AnswerResult, error)
	Dismiss(ctx context.Context, u unit.Unit) error
	MarkRead(ctx context.Context, u unit.Unit, ids []int64) error
}

// Ledger is the items ledger's proposals.
type Ledger interface {
	Proposals(ctx context.Context, state coreitems.ProposalState) ([]coreitems.Proposal, error)
	Decide(ctx context.Context, id int64, accept bool) (coreitems.Result, error)
}

// Tasks opens and closes the Inbox entry of each message, as task.Service does.
type Tasks interface {
	Open(ctx context.Context, r coretasks.OpenRequest) (task.Task, error)
	Close(ctx context.Context, id task.ID, to task.Category) error
	Task(ctx context.Context, id task.ID) (task.Task, error)
	Retitle(ctx context.Context, id task.ID, title string) error
}

// Intents names the intents whose questions an answer addresses.
type Intents interface{ Answerable() []string }

// PostsPerTurn is how many agent posts of one turn may be open at once.
const PostsPerTurn = 3

// The options of a ledger proposal's message.
const (
	OptionAccept = "accept"
	OptionReject = "reject"
)

var (
	ErrNoMessage = errors.New("no such message")
	// ErrClosed is an answer or a dismiss of a message that is no longer open.
	ErrClosed = errors.New("the message is closed")
	// ErrNoStartedTurn refuses inbox_post from a session with no started turn.
	ErrNoStartedTurn = errors.New("this session has no started turn")
)

// Capped refuses a post over PostsPerTurn.
type Capped struct{ Max int }

func (c Capped) Error() string {
	return fmt.Sprintf("this turn already has %d open Inbox messages, the most it may have; wait for the user's answers", c.Max)
}

type Service struct {
	Store   message.Store
	Books   Books
	Turns   Turns
	Ledger  Ledger // nil: no proposal messages
	Tasks   Tasks
	Intents Intents
	Clock   port.Clock
	Log     func(format string, args ...any)
	// OnAnswer runs when a core or workflow proposal is answered, before it closes; nil only closes it.
	OnAnswer func(ctx context.Context, m message.Message, a Answer) error

	mu sync.Mutex
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

// Reconcile makes the open question messages match the books: one per turn whose newest record is asked
// or that stopped waiting on the user; others closed. With no units it reconciles every book.
func (s *Service) Reconcile(ctx context.Context, units ...unit.Unit) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	books, err := s.Books.Books(ctx, units...)
	if err != nil {
		return err
	}
	open, err := s.Store.Open(ctx)
	if err != nil {
		return err
	}
	answerable := s.Intents.Answerable()
	want := map[turnKey]message.Message{}
	for u, b := range books {
		if m, ok := questionOf(u, b, answerable, s.now()); ok {
			want[turnKey{u, m.Origin.TurnSeq}] = m
		}
	}
	inScope := func(u unit.Unit) bool { return len(units) == 0 || slices.Contains(units, u) }
	var errs []error
	for _, m := range open {
		if m.Kind == message.AgentPost && inScope(m.Unit) {
			if b, ok := books[m.Unit]; !postable(&b, m) && ok {
				errs = append(errs, s.close(ctx, m, message.Superseded, ""))
			}
			continue
		}
		if m.Kind != message.Question || !inScope(m.Unit) {
			continue
		}
		k := turnKey{m.Unit, m.Origin.TurnSeq}
		w, ok := want[k]
		if !ok {
			b := books[m.Unit]
			st, said := closing(&b, m.Origin.TurnSeq)
			errs = append(errs, s.close(ctx, m, st, said))
			continue
		}
		delete(want, k)
		w.ID, w.TaskID, w.CreatedAt = m.ID, m.TaskID, m.CreatedAt
		if !sameContent(w, m) {
			if w.TaskID != "" {
				if err := s.Tasks.Retitle(ctx, task.ID(w.TaskID), title(w.Text)); err != nil && !errors.Is(err, coretasks.ErrNoTask) {
					errs = append(errs, err)
					continue
				}
			}
			if m, err = s.Store.Upsert(ctx, w); err != nil {
				errs = append(errs, err)
				continue
			}
		}
		if _, err := s.ensureTask(ctx, m); err != nil {
			errs = append(errs, err)
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
		if _, err := s.create(ctx, want[k]); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
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
// answer addresses, when it is asked or stopped and has no answer since its last record.
func questionOf(u unit.Unit, b turn.Book, answerable []string, now time.Time) (message.Message, bool) {
	q, ok := b.Question(answerable)
	if !ok || (q.State != turn.Asked && !q.State.Stopped()) {
		return message.Message{}, false
	}
	last, recorded := b.LastRecord(q.Seq)
	if recorded && answeredSince(&b, q.Seq, last.At) {
		return message.Message{}, false
	}
	m := message.Message{Unit: u, Kind: message.Question, State: message.Open, CreatedAt: now,
		Origin: message.Origin{Kind: message.OriginTurn, TurnSeq: q.Seq, Session: q.Session}}
	switch {
	case recorded && last.Record.Question != nil:
		rq := last.Record.Question
		m.Text, m.Options, m.Recommend, m.Confirm = rq.Text, slices.Clone(rq.Options), rq.Recommend, rq.Kind == "confirm"
	case q.State == turn.Failed:
		m.Text = "The agent's turn failed"
	case q.State == turn.Unrecorded:
		m.Text = "The agent ended its turn without a record"
	default:
		m.Text = "The agent waits on you"
	}
	if recorded && last.Record.Question == nil {
		if detail := strings.TrimSpace(firstOf(last.Record.Summary, last.Record.Resume)); detail != "" {
			m.Text += ": " + detail
		}
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

// closing is how a question message of turn seq closes: answered with what the agent reads, dismissed, or
// superseded by a newer turn, record or outcome.
func closing(b *turn.Book, seq int64) (message.State, string) {
	var since time.Time
	if last, ok := b.LastRecord(seq); ok {
		since = last.At
	}
	if a, ok := lastAnswer(b, seq, since); ok {
		return message.Answered, oneLine(b.Said(a))
	}
	for _, t := range b.Turns {
		if t.Seq == seq && t.State == turn.Dismissed {
			return message.Dismissed, ""
		}
	}
	return message.Superseded, ""
}

// create stores a new open message and opens its Inbox entry.
func (s *Service) create(ctx context.Context, m message.Message) (message.Message, error) {
	m.State = message.Open
	if m.CreatedAt.IsZero() {
		m.CreatedAt = s.now()
	}
	got, err := s.Store.Upsert(ctx, m)
	if err != nil {
		return message.Message{}, err
	}
	return s.ensureTask(ctx, got)
}

// ensureTask opens the Inbox entry of an open message that has none yet.
func (s *Service) ensureTask(ctx context.Context, m message.Message) (message.Message, error) {
	if m.TaskID != "" || m.State != message.Open {
		return m, nil
	}
	var u *unit.Unit
	if !m.Unit.IsZero() {
		u = &m.Unit
	}
	e, err := s.Tasks.Open(ctx, coretasks.OpenRequest{Kind: task.KindMessage, Title: title(m.Text), Unit: u, Origin: task.OriginMessage})
	if err != nil {
		return m, fmt.Errorf("opening the entry of message %d: %w", m.ID, err)
	}
	m.TaskID = string(e.ID)
	return s.Store.Upsert(ctx, m)
}

// close closes m and the entry it opened: done when answered, dropped otherwise. An entry that refuses to move
// keeps the message open, so the next reconcile tries again.
func (s *Service) close(ctx context.Context, m message.Message, st message.State, answer string) error {
	if m.TaskID != "" {
		to := task.CategoryDropped
		if st == message.Answered {
			to = task.CategoryDone
		}
		e, err := s.Tasks.Task(ctx, task.ID(m.TaskID))
		switch {
		case errors.Is(err, coretasks.ErrNoTask):
		case err != nil:
			return err
		case e.Kind == task.KindMessage:
			if err := s.Tasks.Close(ctx, e.ID, to); err != nil {
				return fmt.Errorf("closing the entry of message %d: %w", m.ID, err)
			}
		}
	}
	return s.Store.Close(ctx, m.ID, st, answer, s.now())
}

// ReconcileProposals makes one open proposal message per open items.Proposal; closed when it is decided.
func (s *Service) ReconcileProposals(ctx context.Context) error {
	if s.Ledger == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ps, err := s.Ledger.Proposals(ctx, coreitems.ProposalOpen)
	if err != nil {
		return err
	}
	open, err := s.Store.Open(ctx)
	if err != nil {
		return err
	}
	pending := map[int64]bool{}
	for _, p := range ps {
		pending[p.ID] = true
	}
	var errs []error
	var stale []message.Message
	have := map[int64]bool{}
	for _, m := range open {
		if m.Origin.Kind != message.OriginLedger {
			continue
		}
		have[m.Origin.Proposal] = true
		if !pending[m.Origin.Proposal] {
			stale = append(stale, m)
		} else if _, err := s.ensureTask(ctx, m); err != nil {
			errs = append(errs, err)
		}
	}
	for _, p := range ps {
		if have[p.ID] {
			continue
		}
		m, err := s.Store.Upsert(ctx, proposalMessage(p, s.now()))
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if _, err := s.ensureTask(ctx, m); err != nil {
			errs = append(errs, err)
		}
	}
	if len(stale) > 0 {
		decided, err := s.decided(ctx)
		if err != nil {
			return errors.Join(append(errs, err)...)
		}
		for _, m := range stale {
			answer, ok := decided[m.Origin.Proposal]
			if !ok {
				errs = append(errs, s.close(ctx, m, message.Superseded, ""))
				continue
			}
			errs = append(errs, s.close(ctx, m, message.Answered, answer))
		}
	}
	return errors.Join(errs...)
}

// decided maps each decided proposal to its message's answer.
func (s *Service) decided(ctx context.Context) (map[int64]string, error) {
	out := map[int64]string{}
	for st, answer := range map[coreitems.ProposalState]string{coreitems.ProposalAccepted: OptionAccept, coreitems.ProposalRejected: OptionReject} {
		ps, err := s.Ledger.Proposals(ctx, st)
		if err != nil {
			return nil, err
		}
		for _, p := range ps {
			out[p.ID] = answer
		}
	}
	return out, nil
}

func proposalMessage(p coreitems.Proposal, now time.Time) message.Message {
	text := fmt.Sprintf("Proposed %s of item %s on %s", p.Op.Op, p.Item, p.Unit.Label())
	if r := strings.TrimSpace(p.Reason); r != "" {
		text += ": " + r
	}
	text += fmt.Sprintf(" (from %s; you confirmed the item)", p.Provenance)
	return message.Message{Unit: p.Unit, Kind: message.Proposal, Text: clean(text), State: message.Open, CreatedAt: now,
		Options: []record.Option{
			{ID: OptionAccept, Label: "Accept", Consequence: "applies the change to the item"},
			{ID: OptionReject, Label: "Reject", Consequence: "keeps the item as you confirmed it"},
		},
		Origin: message.Origin{Kind: message.OriginLedger, Proposal: p.ID}}
}

// Post is inbox_post: an open agent_post message on the unit of the session's started turn.
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
	s.mu.Lock()
	defer s.mu.Unlock()
	open, err := s.Store.Open(ctx)
	if err != nil {
		return message.Message{}, err
	}
	n := 0
	for _, m := range open {
		if m.Kind == message.AgentPost && m.Unit == b.Unit && m.Origin.TurnSeq == t.Seq {
			n++
		}
	}
	if n >= PostsPerTurn {
		return message.Message{}, Capped{PostsPerTurn}
	}
	return s.create(ctx, message.Message{Unit: b.Unit, Kind: message.AgentPost, Text: strings.TrimSpace(p.Text),
		Options: slices.Clone(p.Options), Recommend: p.Recommend, Confirm: p.Confirm,
		Origin: message.Origin{Kind: message.OriginAgent, TurnSeq: t.Seq, Session: p.Session}})
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

// Propose opens a proposal message of a workflow or core rule, one per unit and rule while it is open; with
// TaskID it attaches to that entry instead of opening one.
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
	defer s.mu.Unlock()
	m, err := s.Store.Upsert(ctx, message.Message{Unit: p.Unit, TaskID: p.TaskID, Kind: message.Proposal, Text: clean(p.Text),
		Options: slices.Clone(p.Options), Origin: p.Origin, State: message.Open, CreatedAt: s.now()})
	if err != nil {
		return message.Message{}, err
	}
	return s.ensureTask(ctx, m)
}

// Answer answers an open message: a question through turns.Service.Answer unchanged, an agent post into the
// session that posted it, a proposal by deciding it.
func (s *Service) Answer(ctx context.Context, id int64, a Answer) (turns.AnswerResult, error) {
	m, err := s.answerable(ctx, id)
	if err != nil {
		return turns.AnswerResult{}, err
	}
	switch m.Kind {
	case message.Question:
		if m.State == message.Open {
			if err := s.current(ctx, m); err != nil {
				return turns.AnswerResult{}, err
			}
		}
		res, err := s.Turns.Answer(ctx, turns.AnswerRequest{Unit: m.Unit, Message: m.ID, Text: a.Text, OptionID: a.OptionID, Yes: a.Yes, No: a.No, Source: a.Source})
		if err == nil && m.State == message.Answered {
			s.reanswered(ctx, m)
		}
		if rerr := s.Reconcile(ctx, m.Unit); rerr != nil {
			s.logf("messages: reconciling %s after an answer: %v", m.Unit, rerr)
		}
		return res, err
	case message.AgentPost:
		said, err := saidTo(m, a)
		if err != nil {
			return turns.AnswerResult{}, err
		}
		books, err := s.Books.Books(ctx, m.Unit)
		if err != nil {
			return turns.AnswerResult{}, err
		}
		if b := books[m.Unit]; !postable(&b, m) {
			if rerr := s.Reconcile(ctx, m.Unit); rerr != nil {
				s.logf("messages: reconciling %s: %v", m.Unit, rerr)
			}
			return turns.AnswerResult{}, fmt.Errorf("%w: message %d: the turn that posted it has ended", ErrClosed, m.ID)
		}
		req := turns.AnswerRequest{Unit: m.Unit, Message: m.ID, Posted: true, Turn: m.Origin.TurnSeq,
			Text: strings.TrimSpace(a.Text), OptionID: a.OptionID, Yes: a.Yes, No: a.No || (m.Confirm && !a.Yes && a.OptionID == "" && strings.TrimSpace(a.Text) == ""), Source: a.Source}
		res, err := s.Turns.Answer(ctx, req)
		if errors.Is(err, turn.ErrClosed) {
			if rerr := s.Reconcile(ctx, m.Unit); rerr != nil {
				s.logf("messages: reconciling %s: %v", m.Unit, rerr)
			}
			return res, fmt.Errorf("%w: %w", ErrClosed, err)
		}
		if err != nil {
			return res, err
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		return res, s.close(ctx, m, message.Answered, oneLine(said))
	case message.Proposal:
		return turns.AnswerResult{}, s.decide(ctx, m, a)
	}
	return turns.AnswerResult{}, fmt.Errorf("message %d is of kind %q", m.ID, m.Kind)
}

// answerable is message id while it takes an answer: open, or a question already answered whose turn still
// asks, which a newer answer replaces or adds to.
func (s *Service) answerable(ctx context.Context, id int64) (message.Message, error) {
	m, ok, err := s.Store.Message(ctx, id)
	if err != nil || !ok || m.Kind != message.Question || m.State != message.Answered {
		return s.open(ctx, id)
	}
	books, err := s.Books.Books(ctx, m.Unit)
	if err != nil {
		return message.Message{}, err
	}
	b := books[m.Unit]
	if q, asks := asking(&b, s.Intents.Answerable()); asks && q.Seq == m.Origin.TurnSeq {
		if id, ok := answeredQuestion(&b, q.Seq); ok && id == m.ID {
			return m, nil
		}
	}
	return message.Message{}, fmt.Errorf("%w: message %d is %s", ErrClosed, id, m.State)
}

// reanswered words a question message already answered with its newest answer.
func (s *Service) reanswered(ctx context.Context, m message.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	books, err := s.Books.Books(ctx, m.Unit)
	if err != nil {
		s.logf("messages: rewording message %d: %v", m.ID, err)
		return
	}
	b := books[m.Unit]
	if st, said := closing(&b, m.Origin.TurnSeq); st == message.Answered {
		if err := s.Store.Close(ctx, m.ID, st, said, s.now()); err != nil {
			s.logf("messages: rewording message %d: %v", m.ID, err)
		}
	}
}

func (s *Service) open(ctx context.Context, id int64) (message.Message, error) {
	m, ok, err := s.Store.Message(ctx, id)
	switch {
	case err != nil:
		return message.Message{}, err
	case !ok:
		return message.Message{}, fmt.Errorf("%w: %d", ErrNoMessage, id)
	case m.State != message.Open:
		return message.Message{}, fmt.Errorf("%w: message %d is %s", ErrClosed, id, m.State)
	}
	return m, nil
}

// current refuses a question message whose turn no longer waits on the user, and closes it.
func (s *Service) current(ctx context.Context, m message.Message) error {
	books, err := s.Books.Books(ctx, m.Unit)
	if err != nil {
		return err
	}
	if q, ok := questionOf(m.Unit, books[m.Unit], s.Intents.Answerable(), s.now()); ok && q.Origin.TurnSeq == m.Origin.TurnSeq {
		return nil
	}
	if err := s.Reconcile(ctx, m.Unit); err != nil {
		s.logf("messages: reconciling %s: %v", m.Unit, err)
	}
	return fmt.Errorf("%w on %s", turn.ErrNoQuestion, m.Unit.Label())
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
		choice = "yes"
	case a.No:
		choice = "no"
	}
	switch {
	case choice == "" && note == "" && m.Confirm:
		return "no", nil
	case choice == "" && note == "":
		return "", turns.InvalidAnswer{Reason: "an answer needs the text, an option or yes"}
	case choice == "":
		return note, nil
	case note == "" || note == choice:
		return choice, nil
	}
	return choice + ". " + note, nil
}

// decide answers a proposal: a ledger one by deciding it (accept or yes accepts, reject or no rejects), a
// core or workflow one through OnAnswer.
func (s *Service) decide(ctx context.Context, m message.Message, a Answer) error {
	if m.Origin.Kind == message.OriginLedger {
		var accept bool
		switch {
		case a.OptionID == OptionAccept || (a.OptionID == "" && a.Yes && !a.No):
			accept = true
		case a.OptionID == OptionReject || (a.OptionID == "" && a.No && !a.Yes):
		default:
			return turns.InvalidAnswer{Reason: fmt.Sprintf("a proposal takes %s or %s", OptionAccept, OptionReject)}
		}
		if _, err := s.Ledger.Decide(ctx, m.Origin.Proposal, accept); errors.Is(err, coreitems.ErrNoProposal) {
			return errors.Join(fmt.Errorf("%w: %w", ErrClosed, err), s.ReconcileProposals(ctx))
		} else if err != nil {
			return err
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		answer := OptionReject
		if accept {
			answer = OptionAccept
		}
		return s.close(ctx, m, message.Answered, answer)
	}
	said, err := saidTo(m, a)
	if err != nil {
		return err
	}
	if s.OnAnswer != nil {
		if err := s.OnAnswer(ctx, m, a); err != nil {
			return err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.close(ctx, m, message.Answered, oneLine(said))
}

// Dismiss closes a message without an answer: a question's turn is dismissed as turns.Service.Dismiss does,
// a ledger proposal is rejected.
func (s *Service) Dismiss(ctx context.Context, id int64) error {
	m, err := s.open(ctx, id)
	if err != nil {
		return err
	}
	switch {
	case m.Kind == message.Question:
		if err := s.current(ctx, m); err != nil {
			return err
		}
		if err := s.Turns.Dismiss(ctx, m.Unit); err != nil {
			return err
		}
		return s.Reconcile(ctx, m.Unit)
	case m.Origin.Kind == message.OriginLedger:
		if _, err := s.Ledger.Decide(ctx, m.Origin.Proposal, false); err != nil && !errors.Is(err, coreitems.ErrNoProposal) {
			return err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.close(ctx, m, message.Dismissed, "")
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
	TaskID  string // attach to an existing entry; "" opens a new Inbox entry
	Text    string
	Options []record.Option // answering an option calls OnAnswer
	Origin  message.Origin  // Kind core or workflow; Workflow names the rule (dedupe key with Unit)
}

type Answer struct {
	Text, OptionID string
	Yes, No        bool
	Source         turn.Source
}
