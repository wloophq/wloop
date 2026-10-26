package intake

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
)

var (
	ErrUnknown    = errors.New("no such intake")
	ErrNotWaiting = errors.New("the intake is not waiting")
	ErrDriven     = errors.New("the intake is already being driven")
)

// Service drives intakes. The daemon accepts a request, then drives the
// intake on its own goroutine; Without and Cancel wake that goroutine.
type Service struct {
	Store    Store
	Briefs   Briefs
	Firsts   Firsts
	Starter  Starter
	Payloads Payloads
	Clock    port.Clock
	Pub      port.Publisher
	// Lock is the daemon's loop mutex, held only while the turn starts.
	Lock sync.Locker
	// After is time.After when nil.
	After func(time.Duration) <-chan time.Time
	// Log reports what fails after an intake's change is saved; nil drops
	// it.
	Log func(format string, args ...any)
	// Needs are the brief fields an intent needs, its manifest's
	// brief.need; nil needs none.
	Needs func(intent string) []string

	// mu serializes every load, change and save of an intake; it is never
	// held across a Ready, a wait or a Start.
	mu    sync.Mutex
	wakes map[int64]chan struct{}
}

// Accept answers a new intake, or the open one of the request's pull request
// and intent, joined. A joined intake still waiting takes the request's
// guidance and NoBrief; one already starting is answered unchanged, and the
// caller tells the user the request changed nothing.
func (s *Service) Accept(ctx context.Context, r Request) (Intake, bool, error) {
	i := Intake{Unit: r.Unit, Intent: r.Intent, Source: r.Source, Request: r.Payload, State: Accepted, AcceptedAt: s.now()}
	if r.NoBrief {
		i.Brief, i.Note, i.Log = BriefWithout, NoteNoBrief, []string{NoteNoBrief}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	got, joined, err := s.Store.Accept(ctx, i, port.Event{Unit: i.Unit, Kind: KindAccepted, Data: i.Note, At: i.AcceptedAt})
	if err != nil {
		return Intake{}, false, err
	}
	if !joined {
		s.publish(s.event(got, KindAccepted, got.Note))
		for _, line := range got.Log {
			s.publish(s.event(got, KindLog, line))
		}
		return got, false, nil
	}
	if r.CanJoin != nil {
		if err := r.CanJoin(got); err != nil {
			return got, true, err
		}
	}
	if !got.State.waiting() {
		return got, true, nil
	}
	changed := false
	var evs, notices []port.Event
	if r.Guidance != "" {
		payload, err := s.Payloads.WithGuidance(got.Request, r.Guidance)
		if err != nil {
			return Intake{}, false, err
		}
		got.Request, changed = payload, true
		got.Note = NoteGuidance
		notices = append(notices, s.log(&got, NoteGuidance))
		ev := s.event(got, KindGuidance, NoteGuidance)
		ev.Key = fmt.Sprintf("%d:%d", got.Seq, countOf(got.Log, NoteGuidance))
		evs = append(evs, ev)
	}
	without := r.NoBrief && got.Brief != BriefWithout
	if without {
		got.Brief, changed = BriefWithout, true
		notices = append(notices, s.note(&got, NoteNoBrief)...)
	}
	if !changed {
		return got, true, nil
	}
	if err := s.Store.Save(ctx, got, evs...); err != nil {
		return Intake{}, false, err
	}
	s.publish(append(evs, notices...)...)
	if without {
		s.wake(got.Seq)
	}
	return got, true, nil
}

func countOf(log []string, line string) int {
	n := 0
	for _, l := range log {
		if l == line {
			n++
		}
	}
	return n
}

// Drive takes the intake from where it stands to its end: it waits for the
// brief while one is coming, then starts the turn. A Starter's error fails
// the intake and is not returned; a canceled ctx leaves a waiting intake as
// it is, for Recover. Only one Drive of an intake runs at a time; another
// answers ErrDriven.
func (s *Service) Drive(ctx context.Context, seq int64) (Intake, error) {
	wake, err := s.watch(seq)
	if err != nil {
		return Intake{}, err
	}
	defer s.unwatch(seq)
	var waited string
	for {
		s.mu.Lock()
		i, err := s.load(ctx, seq)
		s.mu.Unlock()
		if err != nil || !i.State.waiting() {
			return i, err
		}
		changed, release := s.Briefs.Changed(i.Unit)
		rd := Readiness{Go: true, Without: true}
		if i.Brief != BriefWithout {
			if rd, err = s.ready(ctx, i); err != nil {
				release()
				if ctx.Err() != nil {
					return i, ctx.Err()
				}
				return s.fail(ctx, seq, err.Error())
			}
		}
		i, start, err := s.decide(ctx, seq, rd, &waited)
		if err != nil || !i.State.waiting() && !start {
			release()
			return i, err
		}
		if start {
			release()
			return s.start(ctx, i)
		}
		var next <-chan time.Time
		if !rd.Next.IsZero() {
			next = s.after(max(rd.Next.Sub(s.now()), 0))
		}
		select {
		case <-ctx.Done():
			release()
			return i, ctx.Err()
		case <-changed:
		case <-next:
		case <-wake:
		}
		release()
	}
}

func (s *Service) ready(ctx context.Context, i Intake) (Readiness, error) {
	first, err := s.Firsts.First(ctx, i.Unit)
	if err != nil {
		return Readiness{}, err
	}
	var need []string
	if s.Needs != nil {
		need = s.Needs(string(i.Intent))
	}
	return s.Briefs.Ready(ctx, i.Unit, first, need)
}

// decide applies rd to the intake as stored now, which a Without or a Cancel
// may have changed since rd was asked, and moves it to starting when it
// goes. waited is the last wait logged, so a wait that did not change is not
// logged again; a line equal to the log's last one, as a Drive resumed after
// a restart writes first, is not logged either.
func (s *Service) decide(ctx context.Context, seq int64, rd Readiness, waited *string) (Intake, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i, err := s.load(ctx, seq)
	if err != nil || !i.State.waiting() {
		return i, false, err
	}
	var evs, notices []port.Event
	switch {
	case i.Brief == BriefWithout:
	case rd.Go:
		switch {
		case rd.Cause == CauseOff:
			i.Brief = BriefOff
		case rd.Without:
			i.Brief = BriefWithout
		default:
			i.Brief = BriefReady
		}
		if line := s.withoutLine(rd); line != "" && rd.Note != "" && rd.Note != i.Note {
			i.Note = rd.Note
			notices = append(notices, s.log(&i, line))
		} else {
			notices = s.note(&i, rd.Note)
		}
	default:
		lines := s.waitLines(i, rd, *waited)
		if wait := fmt.Sprint(rd.Wait, rd.Attempt, rd.LastError, rd.Next); wait != *waited {
			*waited = wait
			i.Note = lines[len(lines)-1]
			for _, l := range lines {
				if n := len(i.Log); n > 0 && i.Log[n-1] == l {
					continue
				}
				notices = append(notices, s.log(&i, l))
			}
		}
		if i.State == Accepted {
			i.State, i.BriefingAt = Briefing, s.now()
			evs = append(evs, s.event(i, KindBriefing, i.Note))
		}
		if len(evs)+len(notices) == 0 {
			return i, false, nil
		}
		if err := s.Store.Save(ctx, i, evs...); err != nil {
			return i, false, err
		}
		s.publish(append(evs, notices...)...)
		return i, false, nil
	}
	if err := ctx.Err(); err != nil {
		return i, false, err
	}
	i.State, i.StartingAt = Starting, s.now()
	evs = append(evs, s.event(i, KindStarting, i.Note))
	if err := s.Store.Save(context.WithoutCancel(ctx), i, evs...); err != nil {
		return i, false, err
	}
	s.publish(append(evs, notices...)...)
	return i, true, nil
}

// waitLines are the lines a wait logs, as the user reads them: the brief
// being made, or the failed attempt with the next one's time. A wait after
// a retry does not repeat the failure the retry logged.
func (s *Service) waitLines(i Intake, rd Readiness, waited string) []string {
	failed := fmt.Sprintf("brief attempt %d failed: %s", max(rd.Attempt-1, 1), rd.LastError)
	if rd.Wait == WaitRetry {
		if !rd.Next.IsZero() {
			failed += "; next attempt at " + s.clock(rd.Next)
		}
		return []string{failed}
	}
	briefing := fmt.Sprintf("briefing %s (attempt %d; briefs take 10 to 20 s)", i.Unit.Label(), max(rd.Attempt, 1))
	if rd.LastError != "" && !strings.HasPrefix(waited, WaitRetry) {
		return []string{failed, briefing}
	}
	return []string{briefing}
}

// withoutLine is the line of a turn that starts without its brief because
// it gave up or briefs are unavailable; "" for any other cause.
func (s *Service) withoutLine(rd Readiness) string {
	switch rd.Cause {
	case CauseGaveUp:
		noun := "attempts"
		if rd.Attempt == 1 {
			noun = "attempt"
		}
		return fmt.Sprintf("brief gave up after %d %s: %s; starting without it", rd.Attempt, noun, rd.LastError)
	case CauseBreaker:
		return fmt.Sprintf("briefs are unavailable since %s: %s; starting without the brief", s.clock(rd.Since), rd.LastError)
	}
	return ""
}

// clock is t's hour and minute where the service's clock runs, the
// user's zone in the daemon.
func (s *Service) clock(t time.Time) string {
	return t.In(s.now().Location()).Format("15:04")
}

// start runs the turn. Its saves outlive ctx: once the action ran, the
// intake must not stay starting, which Recover would fail as never sent. A
// ctx done while it waits for the loop mutex leaves it starting, for
// Recover, since nothing ran.
func (s *Service) start(ctx context.Context, i Intake) (Intake, error) {
	keep := context.WithoutCancel(ctx)
	out := &logWriter{s: s, ctx: keep, i: &i}
	st, ran, err := s.run(ctx, i, out)
	if !ran {
		return i, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out.flushLocked()
	i.EndedAt = s.now()
	var ev port.Event
	if err != nil {
		i.State, i.Error = Failed, err.Error()
		ev = s.event(i, KindFailed, i.Error)
	} else {
		i.State, i.TurnSeq, i.TurnID = Started, st.TurnSeq, st.TurnID
		ev = s.event(i, KindStarted, i.Note)
	}
	if err := s.Store.Save(keep, i, ev); err != nil {
		return i, err
	}
	s.publish(ev)
	return i, nil
}

func (s *Service) run(ctx context.Context, i Intake, out *logWriter) (StartedTurn, bool, error) {
	if a, ok := s.Starter.(AheadStarter); ok {
		ctx = a.ReadAhead(ctx, i)
	}
	if s.Lock != nil {
		if err := lock(ctx, s.Lock); err != nil {
			return StartedTurn{}, false, err
		}
		defer s.Lock.Unlock()
	}
	st, err := s.Starter.Start(ctx, i, out)
	return st, true, err
}

// lockPoll is how often a start that waits for the loop mutex tries it.
const lockPoll = 10 * time.Millisecond

// lock takes l, giving up when ctx is done first; a Locker without
// TryLock is waited for whatever ctx does.
func lock(ctx context.Context, l sync.Locker) error {
	t, ok := l.(interface{ TryLock() bool })
	if !ok {
		l.Lock()
		return nil
	}
	tick := time.NewTicker(lockPoll)
	defer tick.Stop()
	for !t.TryLock() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
	return nil
}

func (s *Service) fail(ctx context.Context, seq int64, reason string) (Intake, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i, err := s.load(ctx, seq)
	if err != nil || !i.State.Open() {
		return i, err
	}
	return i, s.failLocked(ctx, &i, reason)
}

func (s *Service) failLocked(ctx context.Context, i *Intake, reason string) error {
	i.State, i.Error, i.EndedAt = Failed, reason, s.now()
	notice := s.log(i, reason)
	ev := s.event(*i, KindFailed, reason)
	if err := s.Store.Save(ctx, *i, ev); err != nil {
		return err
	}
	s.publish(ev, notice)
	return nil
}

// Without makes a waiting intake start without the brief.
func (s *Service) Without(ctx context.Context, seq int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i, err := s.waiting(ctx, seq)
	if err != nil || i.Brief == BriefWithout {
		return err
	}
	i.Brief = BriefWithout
	notices := s.note(&i, NoteWithout)
	if err := s.Store.Save(ctx, i); err != nil {
		return err
	}
	s.publish(notices...)
	s.wake(seq)
	s.dropWant(ctx, i)
	return nil
}

func (s *Service) Cancel(ctx context.Context, seq int64) error {
	return s.CancelFor(ctx, seq, NoteCanceled)
}

// CancelFor cancels a waiting intake with note as the reason.
func (s *Service) CancelFor(ctx context.Context, seq int64, note string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i, err := s.waiting(ctx, seq)
	if err != nil {
		return err
	}
	i.State, i.EndedAt = Canceled, s.now()
	notices := s.note(&i, note)
	ev := s.event(i, KindCanceled, note)
	if err := s.Store.Save(ctx, i, ev); err != nil {
		return err
	}
	s.publish(append([]port.Event{ev}, notices...)...)
	s.wake(seq)
	s.dropWant(ctx, i)
	return nil
}

// dropWant drops the want of i's brief once no intake of its pull request
// waits for the brief. It runs after i is saved, so a failure is logged and
// changes nothing for i.
func (s *Service) dropWant(ctx context.Context, i Intake) {
	open, err := s.Store.Open(ctx)
	if err == nil {
		if slices.ContainsFunc(open, func(o Intake) bool { return o.Unit == i.Unit && o.State.waiting() && o.Brief != BriefWithout }) {
			return
		}
		err = s.Briefs.Unwant(ctx, i.Unit)
	}
	if err != nil {
		s.logf("intake %d: dropping the want of the brief of %s: %v", i.Seq, i.Unit.Label(), err)
	}
}

func (s *Service) logf(format string, args ...any) {
	if s.Log != nil {
		s.Log(format, args...)
	}
}

// Recover fails the intakes a stopped daemon left starting, since their
// action may have half run, and answers the waiting ones for the caller to
// drive again.
func (s *Service) Recover(ctx context.Context) ([]int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	open, err := s.Store.Open(ctx)
	if err != nil {
		return nil, err
	}
	var again []int64
	for _, i := range open {
		if i.State.waiting() {
			again = append(again, i.Seq)
			continue
		}
		if i.State == Starting {
			if err := s.failLocked(ctx, &i, NoteStopped); err != nil {
				return again, err
			}
		}
	}
	return again, nil
}

func (s *Service) load(ctx context.Context, seq int64) (Intake, error) {
	i, ok, err := s.Store.Intake(ctx, seq)
	if err != nil {
		return Intake{}, err
	}
	if !ok {
		return Intake{}, fmt.Errorf("intake %d: %w", seq, ErrUnknown)
	}
	return i, nil
}

func (s *Service) waiting(ctx context.Context, seq int64) (Intake, error) {
	i, err := s.load(ctx, seq)
	if err != nil {
		return i, err
	}
	if !i.State.waiting() {
		return i, fmt.Errorf("intake %d is %s: %w", seq, i.State, ErrNotWaiting)
	}
	return i, nil
}

// note sets the intake's note and logs it, answering the notice to publish
// once saved; a note already set logs nothing.
func (s *Service) note(i *Intake, note string) []port.Event {
	if note == "" || note == i.Note {
		return nil
	}
	i.Note = note
	return []port.Event{s.log(i, note)}
}

func (s *Service) log(i *Intake, line string) port.Event {
	i.Log = append(i.Log, line)
	return s.event(*i, KindLog, line)
}

func (s *Service) watch(seq int64) (chan struct{}, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.wakes[seq]; ok {
		return nil, fmt.Errorf("intake %d: %w", seq, ErrDriven)
	}
	if s.wakes == nil {
		s.wakes = map[int64]chan struct{}{}
	}
	ch := make(chan struct{}, 1)
	s.wakes[seq] = ch
	return ch, nil
}

func (s *Service) unwatch(seq int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.wakes, seq)
}

func (s *Service) wake(seq int64) {
	select {
	case s.wakes[seq] <- struct{}{}:
	default:
	}
}

func (s *Service) event(i Intake, kind, data string) port.Event {
	return port.Event{Unit: i.Unit, Kind: kind, Key: strconv.FormatInt(i.Seq, 10), Data: data, At: s.now()}
}

func (s *Service) publish(evs ...port.Event) {
	if s.Pub != nil && len(evs) > 0 {
		s.Pub.Publish(evs...)
	}
}

func (s *Service) now() time.Time {
	if s.Clock == nil {
		return time.Now()
	}
	return s.Clock.Now()
}

func (s *Service) after(d time.Duration) <-chan time.Time {
	if s.After == nil {
		return time.After(d)
	}
	return s.After(d)
}

// logSaveEvery is the least time between two saves of an intake's log while
// its action writes.
const logSaveEvery = time.Second

// logWriter publishes each line the action writes as it comes and stores the
// log at most once per logSaveEvery, with a trailing save for the lines a
// skipped save left out. A failed line save is not reported: the final save
// stores the whole log.
type logWriter struct {
	s        *Service
	ctx      context.Context
	i        *Intake
	buf      []byte
	saved    time.Time
	dirty    bool
	armed    bool
	finished bool
}

func (w *logWriter) Write(p []byte) (int, error) {
	w.s.mu.Lock()
	defer w.s.mu.Unlock()
	w.buf = append(w.buf, p...)
	var lines []string
	for {
		n := bytes.IndexByte(w.buf, '\n')
		if n < 0 {
			break
		}
		lines = append(lines, string(w.buf[:n]))
		w.buf = w.buf[n+1:]
	}
	w.save(lines)
	return len(p), nil
}

func (w *logWriter) flushLocked() {
	if len(w.buf) > 0 {
		w.save([]string{string(w.buf)})
		w.buf = nil
	}
	w.finished = true
}

func (w *logWriter) save(lines []string) {
	if len(lines) == 0 {
		return
	}
	notices := make([]port.Event, 0, len(lines))
	for _, l := range lines {
		notices = append(notices, w.s.log(w.i, l))
	}
	if now := w.s.now(); w.saved.IsZero() || now.Sub(w.saved) >= logSaveEvery {
		w.store(now)
	} else {
		w.dirty = true
		if !w.armed {
			w.armed = true
			go w.trail(logSaveEvery - now.Sub(w.saved))
		}
	}
	w.s.publish(notices...)
}

func (w *logWriter) store(now time.Time) {
	stored := *w.i
	stored.Log = slices.Clone(w.i.Log)
	_ = w.s.Store.Save(w.ctx, stored)
	w.saved = now
	w.dirty = false
}

func (w *logWriter) trail(d time.Duration) {
	<-w.s.after(d)
	w.s.mu.Lock()
	defer w.s.mu.Unlock()
	w.armed = false
	if w.dirty && !w.finished {
		w.store(w.s.now())
	}
}
