package turns

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// Warm is how long after its last output a session still takes a typed
// answer; a colder one gets a resume turn.
const Warm = time.Hour

const answeredMax = 200

type AnswerRequest struct {
	Unit           unit.Unit
	Text, OptionID string
	// Yes and No are a confirm's explicit choice; Text is then a note.
	Yes, No bool
	Source  turn.Source
	// Message is the Inbox message answered: the question of the unit's turn, or, when Posted, one the agent
	// posted during turn Turn, whose answer never resumes a turn.
	Message, Turn int64
	Posted        bool
	// Gate is the gate whose resolution the answer is.
	Gate int64
}

func (r AnswerRequest) answer(seq int64) turn.Answer {
	return turn.Answer{TurnSeq: seq, Text: r.Text, OptionID: r.OptionID, Yes: r.Yes, No: r.No, Source: r.Source, Message: r.Message, Gate: r.Gate}
}

type AnswerResult struct {
	Route turn.Route
	// Handle is the session a typed answer went to.
	Handle string
	// Note is why the answer went the way it did, kept in its routed event.
	Note string
}

// Answer stores the user's answer to the pull request's question and routes
// it: typed into the session that asked, queued while it is busy until
// Deliver types it, or a resume turn when that session is gone, cold or not
// turn-capable. An answer to a failed or unrecorded turn always resumes it:
// only a resume turn owes a record.
func (s *Service) Answer(ctx context.Context, r AnswerRequest) (AnswerResult, error) {
	if r.Posted {
		return s.answerPosted(ctx, r)
	}
	s.mu.Lock()
	q, a, said, evs, err := s.storeAnswer(ctx, r)
	s.mu.Unlock()
	if err != nil {
		return AnswerResult{}, err
	}
	s.publish(evs...)
	s.changed(ctx, r.Unit)

	route := AnswerResult{Route: turn.Resumed}
	if !q.State.Stopped() {
		if route, err = s.route(ctx, q, false, false); err != nil {
			return AnswerResult{}, err
		}
	}
	return s.dispatch(ctx, r.Unit, q, a, said, route)
}

// dispatch sends the stored answer a the way route says and saves where it went.
func (s *Service) dispatch(ctx context.Context, ref unit.Unit, q turn.Turn, a turn.Answer, said string, route AnswerResult) (AnswerResult, error) {
	switch route.Route {
	case turn.Typed:
		var err error
		if route, err = s.typeNotice(ctx, ref, route, a); err != nil {
			return AnswerResult{}, err
		}
	case turn.Resumed:
		if headless(q) {
			route = s.runHeadless(ctx, q, said)
			break
		}
		if err := s.Launcher.Launch(ctx, Launch{Unit: ref, Intent: q.Intent, ResumeOf: q.Seq, Answer: said, Inputs: q.Inputs}); errors.Is(err, ErrWorkspaceGone) {
			route = AnswerResult{Route: turn.Refused, Note: "the workspace is gone"}
		} else if err != nil {
			return AnswerResult{}, err
		}
	}
	if err := s.saveRoute(ctx, ref, q, a.ID, route); err != nil {
		return AnswerResult{}, err
	}
	if route.Route == turn.Refused {
		if route.Note != "" {
			return route, Refusal{"the answer was not delivered: " + route.Note}
		}
		return route, Refusal{"the answer reached the agent's terminal but no turn started; look at its terminal"}
	}
	return route, nil
}

// typeNotice types into the route's session the notice that a was given, which carries no answer: the agent
// reads it from the Inbox. The route turns refused when the agent took the line without submitting it.
func (s *Service) typeNotice(ctx context.Context, ref unit.Unit, route AnswerResult, a turn.Answer) (AnswerResult, error) {
	line := s.answeredNotice(ref, a)
	typed := s.Typing(route.Handle)
	submitted, err := s.Sessions.Type(ctx, route.Handle, line)
	if err == nil {
		if terr := s.Typed(ctx, route.Handle, line); terr != nil {
			s.logf("noting the answer typed into %s: %v", route.Handle, terr)
		}
	}
	typed()
	if err != nil {
		return AnswerResult{}, err
	}
	if !submitted {
		return AnswerResult{Route: turn.Refused}, nil
	}
	return route, nil
}

// answerPosted stores the answer to a message the agent posted during its turn and routes it to the session
// that posted it; it never resumes a turn.
func (s *Service) answerPosted(ctx context.Context, r AnswerRequest) (AnswerResult, error) {
	if strings.TrimSpace(r.Text) == "" && r.OptionID == "" && !r.Yes && !r.No {
		return AnswerResult{}, InvalidAnswer{"an answer needs the text, an option, yes or no"}
	}
	s.mu.Lock()
	b, err := s.book(ctx, r.Unit)
	if err != nil {
		s.mu.Unlock()
		return AnswerResult{}, err
	}
	t, ok := bySeq(&b, r.Turn)
	if !ok || t.Session == "" {
		s.mu.Unlock()
		return AnswerResult{}, fmt.Errorf("%w: no turn %d with a session on %s", turn.ErrNoTurn, r.Turn, r.Unit.Label())
	}
	if t.State != turn.Started {
		s.mu.Unlock()
		return AnswerResult{}, fmt.Errorf("%w: turn %d on %s is %s; a posted message takes answers only while its turn is started", turn.ErrClosed, r.Turn, r.Unit.Label(), t.State)
	}
	q := *t
	a := r.answer(q.Seq)
	a.At, a.Posted = s.now(), true
	b.Answer(a)
	evs := []port.Event{s.event(r.Unit, KindAnswered, "", oneLine(b.Said(a), answeredMax))}
	saved, err := s.Store.Save(ctx, b, evs...)
	s.mu.Unlock()
	if err != nil {
		return AnswerResult{}, err
	}
	a = newestOf(saved, q.Seq)
	s.publish(evs...)
	s.changed(ctx, r.Unit)
	route, err := s.route(ctx, q, false, true)
	if err != nil {
		return AnswerResult{}, err
	}
	return s.dispatchPosted(ctx, r.Unit, q, a, route)
}

// newestOf is the newest stored answer of turn seq.
func newestOf(b turn.Book, seq int64) turn.Answer {
	var out turn.Answer
	for _, a := range b.Answers {
		if a.TurnSeq == seq && a.ID > out.ID {
			out = a
		}
	}
	return out
}

// dispatchPosted sends a posted message's stored answer a the way route says; a session that is gone keeps
// the answer stored and refused.
func (s *Service) dispatchPosted(ctx context.Context, ref unit.Unit, q turn.Turn, a turn.Answer, route AnswerResult) (AnswerResult, error) {
	switch route.Route {
	case turn.Typed:
		var err error
		if route, err = s.typeNotice(ctx, ref, route, a); err != nil {
			return AnswerResult{}, err
		}
	case turn.Resumed:
		route = AnswerResult{Route: turn.Refused, Note: "the session that posted the message is gone"}
	}
	if err := s.saveRoute(ctx, ref, q, a.ID, route); err != nil {
		return AnswerResult{}, err
	}
	if route.Route == turn.Refused && route.Note == "" {
		return route, Refusal{"the answer reached the agent's terminal but the agent did not take it; look at its terminal"}
	}
	return route, nil
}

// storeAnswer refuses the turn's queued answer: the newest one replaces it.
func (s *Service) storeAnswer(ctx context.Context, r AnswerRequest) (turn.Turn, turn.Answer, string, []port.Event, error) {
	b, err := s.book(ctx, r.Unit)
	if err != nil {
		return turn.Turn{}, turn.Answer{}, "", nil, err
	}
	answerable, err := s.answerable()
	if err != nil {
		return turn.Turn{}, turn.Answer{}, "", nil, err
	}
	q, ok := b.Question(answerable)
	if !ok || (q.State != turn.Asked && !q.State.Stopped()) {
		return turn.Turn{}, turn.Answer{}, "", nil, fmt.Errorf("%w on %s", turn.ErrNoQuestion, r.Unit.Label())
	}
	if err := answerFits(&b, q.Seq, r); err != nil {
		return turn.Turn{}, turn.Answer{}, "", nil, err
	}
	evs := s.refuseQueued(&b, q.Seq, true)
	a := r.answer(q.Seq)
	a.At = s.now()
	said := b.Said(a)
	b.Answer(a)
	evs = append(evs, s.event(r.Unit, KindAnswered, "", oneLine(said, answeredMax)))
	saved, err := s.Store.Save(ctx, b, evs...)
	if err != nil {
		return turn.Turn{}, turn.Answer{}, "", nil, err
	}
	return *q, newestOf(saved, q.Seq), said, evs, nil
}

// refuseQueued refuses the queued answers of turn seq; keepPosted leaves those to messages the agent posted.
func (s *Service) refuseQueued(b *turn.Book, seq int64, keepPosted bool) []port.Event {
	var evs []port.Event
	for i := range b.Answers {
		if a := &b.Answers[i]; a.TurnSeq == seq && a.Route == turn.Queued && !(keepPosted && a.Posted) {
			a.Route = turn.Refused
			evs = append(evs, s.routed(b.Unit, a.ID, AnswerResult{Route: turn.Refused}))
		}
	}
	return evs
}

func (s *Service) routed(ref unit.Unit, id int64, route AnswerResult) port.Event {
	return s.event(ref, KindAnswerRouted, strconv.FormatInt(id, 10)+":"+string(route.Route), strings.TrimSpace(string(route.Route)+" "+route.Handle)+noteOf(route))
}

func noteOf(route AnswerResult) string {
	if route.Note == "" {
		return ""
	}
	return ": " + route.Note
}

// answerFits refuses what the turn's question does not take: an option it
// does not offer, yes or no to anything but a confirm, both, and no answer
// at all unless it is a confirm's no.
func answerFits(b *turn.Book, seq int64, r AnswerRequest) error {
	var q record.Question
	if last, ok := b.LastRecord(seq); ok && last.Record.Question != nil {
		q = *last.Record.Question
	}
	confirm := q.Kind == "confirm"
	switch {
	case r.OptionID != "" && !slices.ContainsFunc(q.Options, func(o record.Option) bool { return o.ID == r.OptionID }):
		return InvalidAnswer{fmt.Sprintf("the question on %s has no option %q", r.Unit.Label(), r.OptionID)}
	case r.Yes && r.No:
		return InvalidAnswer{"an answer is yes or no, not both"}
	case (r.Yes || r.No) && !confirm:
		return InvalidAnswer{"yes and no answer only a confirm"}
	case strings.TrimSpace(r.Text) == "" && r.OptionID == "" && !r.Yes && !confirm:
		return InvalidAnswer{"an answer needs the text, an option or yes"}
	}
	return nil
}

// route reads Orca fresh, and an unreadable Orca as a session it cannot see, as
// Reconcile does: the answer resumes. A session that just stopped is not busy, whatever
// Orca still shows.
// A posted message's answer is typed into a cold session too: only a question's answer resumes one.
func (s *Service) route(ctx context.Context, q turn.Turn, stopped, posted bool) (AnswerResult, error) {
	resume := AnswerResult{Route: turn.Resumed}
	if q.Session == "" {
		return resume, nil
	}
	sess, ok, err := s.Registry.Session(ctx, q.Session)
	if err != nil || !ok {
		return resume, err
	}
	if sess.WorkspaceID == "" || !sess.ClosedAt.IsZero() {
		return resume, nil
	}
	s.refresh()
	terms, err := s.Sessions.Terminals(ctx, sess.WorkspaceID)
	if err != nil {
		return resume, nil
	}
	i := slices.IndexFunc(terms, func(t Terminal) bool { return t.Session == sess.ID })
	if i < 0 {
		return resume, nil
	}
	agents, err := s.Sessions.Agents(ctx)
	if err != nil {
		return resume, nil
	}
	switch {
	case !stopped && busy(sess, agents, turn.AgentWorking, turn.AgentWaiting):
		return AnswerResult{Route: turn.Queued}, nil
	case !sess.Capable() || (!posted && s.now().Sub(terms[i].LastOutput) >= Warm):
		return resume, nil
	}
	return AnswerResult{Route: turn.Typed, Handle: sess.ID}, nil
}

// busy says whether the session's agent is in one of states; with no known
// pane, any agent of its workspace in one of them counts.
func busy(sess turn.Session, agents []AgentObservation, states ...turn.AgentState) bool {
	return slices.ContainsFunc(agents, func(a AgentObservation) bool {
		if sess.Pane != "" {
			return a.Pane == sess.Pane && slices.Contains(states, a.State)
		}
		return sess.WorkspaceID != "" && a.WorkspaceID == sess.WorkspaceID && slices.Contains(states, a.State)
	})
}

// Deliver routes the queued answers of units, as Answer would have, once
// their sessions are no longer busy. A queued answer whose turn no longer
// waits on the user is refused.
func (s *Service) Deliver(ctx context.Context, units ...unit.Unit) error {
	var errs []error
	for _, u := range units {
		errs = append(errs, s.deliver(ctx, u, ""))
	}
	return errors.Join(errs...)
}

// DeliverStopped routes the queued answer of the handle's session, which just
// stopped, whatever Orca still shows of it.
func (s *Service) DeliverStopped(ctx context.Context, handle string) error {
	b, ok, err := s.Store.BookOf(ctx, Key{Session: handle})
	if err != nil || !ok {
		return err
	}
	return s.deliver(ctx, b.Unit, handle)
}

func queuedAnswer(b turn.Book) (turn.Answer, bool) {
	i := slices.IndexFunc(b.Answers, func(a turn.Answer) bool { return a.Route == turn.Queued })
	if i < 0 {
		return turn.Answer{}, false
	}
	return b.Answers[i], true
}

func queuedQuestionAnswer(b turn.Book) (turn.Answer, bool) {
	i := slices.IndexFunc(b.Answers, func(a turn.Answer) bool { return a.Route == turn.Queued && !a.Posted })
	if i < 0 {
		return turn.Answer{}, false
	}
	return b.Answers[i], true
}

func (s *Service) deliver(ctx context.Context, ref unit.Unit, handle string) error {
	b, err := s.book(ctx, ref)
	if err != nil {
		return err
	}
	var errs []error
	for _, a := range b.Answers {
		if a.Route == turn.Queued && a.Posted {
			errs = append(errs, s.deliverPosted(ctx, ref, b, a, handle))
		}
	}
	return errors.Join(append(errs, s.deliverQuestion(ctx, ref, b, handle))...)
}

func (s *Service) deliverPosted(ctx context.Context, ref unit.Unit, b turn.Book, a turn.Answer, handle string) error {
	t, ok := bySeq(&b, a.TurnSeq)
	if ok && handle != "" && t.Session != handle {
		return nil
	}
	route := AnswerResult{Route: turn.Resumed}
	if ok {
		var err error
		if route, err = s.route(ctx, *t, handle != "", true); err != nil || route.Route == turn.Queued {
			return err
		}
	}
	if route.Route == turn.Typed && handle != "" && !s.promptBack(ctx, ref, handle) {
		return nil
	}
	if !s.claim(ctx, ref, a.ID) {
		return nil
	}
	defer s.release(a.ID)
	var q turn.Turn
	if ok {
		q = *t
	}
	_, err := s.dispatchPosted(ctx, ref, q, a, route)
	if refused := (Refusal{}); errors.As(err, &refused) {
		s.logf("the queued answer %d on %s: %s", a.ID, ref, refused.Reason)
		return nil
	}
	return err
}

func (s *Service) deliverQuestion(ctx context.Context, ref unit.Unit, b turn.Book, handle string) error {
	a, ok := queuedQuestionAnswer(b)
	if !ok {
		return nil
	}
	answerable, err := s.answerable()
	if err != nil {
		return err
	}
	q, ok := b.Question(answerable)
	if !ok || q.Seq != a.TurnSeq || (q.State != turn.Asked && !q.State.Stopped()) {
		if !s.claim(ctx, ref, a.ID) {
			return nil
		}
		defer s.release(a.ID)
		return s.saveRoute(ctx, ref, turn.Turn{}, a.ID, AnswerResult{Route: turn.Refused})
	}
	if handle != "" && q.Session != handle {
		return nil
	}
	route := AnswerResult{Route: turn.Resumed}
	if !q.State.Stopped() {
		if route, err = s.route(ctx, *q, handle != "", false); err != nil || route.Route == turn.Queued {
			return err
		}
	}
	if route.Route == turn.Typed && handle != "" && !s.promptBack(ctx, ref, handle) {
		return nil
	}
	if !s.claim(ctx, ref, a.ID) {
		return nil
	}
	defer s.release(a.ID)
	_, err = s.dispatch(ctx, ref, *q, a, b.Said(a), route)
	if refused := (Refusal{}); errors.As(err, &refused) {
		s.logf("the queued answer %d on %s: %s", a.ID, ref, refused.Reason)
		return nil
	}
	return err
}

// StopIdleWait is how long the answer queued for a session that just
// stopped waits for the agent's prompt before it is left to the sweep.
const StopIdleWait = 15 * time.Second

// promptBack waits for the stopped agent to take input again: a line typed
// while it still ends its turn runs, but the app sees no turn start and the
// answer would read refused.
func (s *Service) promptBack(ctx context.Context, ref unit.Unit, handle string) bool {
	w, ok := s.Sessions.(IdleWaiter)
	if !ok {
		return true
	}
	idle, err := w.WaitIdle(ctx, handle, StopIdleWait)
	if err != nil || !idle {
		s.logf("the queued answer on %s waits for the sweep: the agent of %s took no input within %s (%v)", ref, handle, StopIdleWait, err)
		return false
	}
	return true
}

// claim takes the queued answer id for one Deliver; an unreadable book takes
// nothing.
func (s *Service) claim(ctx context.Context, ref unit.Unit, id int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := s.book(ctx, ref)
	if err != nil || !slices.ContainsFunc(b.Answers, func(a turn.Answer) bool { return a.ID == id && a.Route == turn.Queued }) {
		return false
	}
	if _, taken := s.delivering[id]; taken {
		return false
	}
	if s.delivering == nil {
		s.delivering = map[int64]struct{}{}
	}
	s.delivering[id] = struct{}{}
	return true
}

func (s *Service) release(id int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.delivering, id)
}

func (s *Service) saveRoute(ctx context.Context, ref unit.Unit, q turn.Turn, id int64, route AnswerResult) error {
	s.mu.Lock()
	b, err := s.book(ctx, ref)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	i := slices.IndexFunc(b.Answers, func(a turn.Answer) bool { return a.ID == id })
	if i < 0 {
		s.mu.Unlock()
		return fmt.Errorf("%s has no answer %d", ref.Label(), id)
	}
	b.Answers[i].Route, b.Answers[i].Session = route.Route, route.Handle
	if route.Route == turn.Typed {
		b.Answers[i].NoticedAt = s.now()
	}
	evs := []port.Event{s.routed(ref, id, route)}
	if route.Route == turn.Resumed {
		b.Answers[i].ReadAt = s.now()
		evs = append(evs, s.event(ref, KindTurnResumed, q.ID+":"+strconv.FormatInt(id, 10), ""))
	}
	_, err = s.Store.Save(ctx, b, evs...)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	s.publish(evs...)
	s.changed(ctx, ref)
	return nil
}

// Continue resumes the unit's asked or stopped turn with no answer.
func (s *Service) Continue(ctx context.Context, u unit.Unit) error {
	b, err := s.book(ctx, u)
	if err != nil {
		return err
	}
	answerable, err := s.answerable()
	if err != nil {
		return err
	}
	q, ok := b.Question(answerable)
	if !ok || (q.State != turn.Asked && !q.State.Stopped()) {
		return fmt.Errorf("%w on %s", turn.ErrNoQuestion, u.Label())
	}
	if headless(*q) {
		if route := s.runHeadless(ctx, *q, ""); route.Route == turn.Refused {
			return Refusal{"the headless turn did not run again: " + route.Note}
		}
	} else if err := s.Launcher.Launch(ctx, Launch{Unit: u, Intent: q.Intent, ResumeOf: q.Seq, Inputs: q.Inputs}); err != nil {
		return err
	}
	s.mu.Lock()
	b, err = s.book(ctx, u)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	ev := s.event(u, KindTurnResumed, q.ID+":"+s.now().Format(time.RFC3339Nano), "")
	_, err = s.Store.Save(ctx, b, ev)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	s.publish(ev)
	s.changed(ctx, u)
	return nil
}

// Dismiss closes the unit's asked or stopped turn without an answer.
func (s *Service) Dismiss(ctx context.Context, u unit.Unit) error {
	s.mu.Lock()
	b, err := s.book(ctx, u)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	answerable, err := s.answerable()
	if err != nil {
		s.mu.Unlock()
		return err
	}
	t, err := b.Dismiss(s.now(), answerable)
	if errors.Is(err, turn.ErrNoTurn) || errors.Is(err, turn.ErrClosed) {
		s.mu.Unlock()
		return fmt.Errorf("%w on %s", turn.ErrNoQuestion, u.Label())
	}
	if err != nil {
		s.mu.Unlock()
		return err
	}
	evs := append([]port.Event{s.event(u, KindQuestionDismissed, t.ID, "")}, s.refuseQueued(&b, t.Seq, true)...)
	_, err = s.Store.Save(ctx, b, evs...)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	s.publish(evs...)
	s.changed(ctx, u)
	return nil
}

// runHeadless runs headless turn q again with said; nothing is typed into any session.
func (s *Service) runHeadless(ctx context.Context, q turn.Turn, said string) AnswerResult {
	if s.Headless == nil {
		return AnswerResult{Route: turn.Refused, Note: "headless turns are not wired"}
	}
	route, err := s.Headless(ctx, q, said)
	if err != nil {
		return AnswerResult{Route: turn.Refused, Note: err.Error()}
	}
	return route
}

// MarkRead notes that the agent read the answers ids of the unit, now; one read before keeps its time.
func (s *Service) MarkRead(ctx context.Context, u unit.Unit, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	s.mu.Lock()
	b, err := s.book(ctx, u)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	now := s.now()
	var evs []port.Event
	for i := range b.Answers {
		if a := &b.Answers[i]; a.ReadAt.IsZero() && slices.Contains(ids, a.ID) {
			a.ReadAt = now
			evs = append(evs, s.event(u, KindAnswerRead, strconv.FormatInt(a.ID, 10)+":"+now.Format(time.RFC3339Nano), ""))
		}
	}
	if len(evs) == 0 {
		s.mu.Unlock()
		return nil
	}
	_, err = s.Store.Save(ctx, b, evs...)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	s.publish(evs...)
	s.changed(ctx, u)
	return nil
}
