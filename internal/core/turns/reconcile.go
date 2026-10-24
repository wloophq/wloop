package turns

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

const (
	// Quiet is how long a done pane's terminal prints nothing before its turn
	// counts as stopped.
	Quiet = 2 * time.Minute
	// SentGrace is how long a sent turn waits for an agent to start it,
	// plus one fast job.
	SentGrace = 2 * time.Minute
)

const (
	goneMessage         = "the session's process is gone"
	neverStartedMessage = "the agent never started the turn"
)

type Report struct {
	Unrecorded []unit.Unit
	// Closed are the asked turns my review on GitHub closed.
	Closed       []unit.Unit
	GoneSessions []string
	// Queued are the pull requests with an answer queued for a busy session,
	// for Deliver.
	Queued []unit.Unit
	// Warnings are the Orca reads that failed; a workspace whose terminals
	// could not be read counts as listing none.
	Warnings []string
}

// Reconcile closes the open turns whose sessions stopped without a record,
// that no agent started, or that my review on GitHub answered. At start-up one
// observation of a done pane suffices and a session a complete census misses
// counts as stopped; later, only a closed one does.
func (s *Service) Reconcile(ctx context.Context, startup bool) (Report, error) {
	w, err := s.observe(ctx)
	if err != nil {
		return Report{}, err
	}
	s.mu.Lock()
	w.doneBefore = s.doneSeen
	s.doneSeen = w.doneNow
	s.mu.Unlock()

	var rep Report
	if startup {
		sessions, err := s.Registry.Sessions(ctx)
		if err != nil {
			return Report{}, err
		}
		for _, sess := range sessions {
			if w.gone(ctx, sess) {
				rep.GoneSessions = append(rep.GoneSessions, sess.ID)
			}
		}
	}
	refs, err := s.Store.Open(ctx)
	if err != nil || len(refs) == 0 {
		rep.Warnings = w.warnings
		return rep, err
	}
	books, err := s.Store.Books(ctx, refs...)
	if err != nil {
		return Report{}, err
	}
	var changed []unit.Unit
	for _, ref := range refs {
		b := books[ref]
		if _, ok := queuedAnswer(b); ok {
			rep.Queued = append(rep.Queued, ref)
		}
		for _, t := range b.Turns {
			if !t.State.Open() || headless(t) {
				continue
			}
			rec, ok, err := s.fallback(ctx, w, &b, t, startup)
			if err != nil {
				return rep, err
			}
			if !ok {
				continue
			}
			closed, err := s.closeTurn(ctx, ref, t, lastRecordID(&b, t.Seq), rec)
			if err != nil {
				return rep, err
			}
			if !closed {
				continue
			}
			if rec.State == record.Done {
				rep.Closed = append(rep.Closed, ref)
			} else {
				rep.Unrecorded = append(rep.Unrecorded, ref)
			}
			if !slices.Contains(changed, ref) {
				changed = append(changed, ref)
			}
		}
	}
	s.changed(ctx, changed...)
	rep.Warnings = w.warnings
	return rep, nil
}

// ReconcileMyReview is the reconcile that closes an asked turn as done once
// the user's review appears on GitHub.
const ReconcileMyReview = "my_review"

func (s *Service) fallback(ctx context.Context, w *world, b *turn.Book, t turn.Turn, startup bool) (record.Record, bool, error) {
	now := s.now()
	switch {
	case t.State == turn.Asked && s.GitHub != nil && s.Intents != nil && s.Intents.Reconcile(string(t.Intent)) == ReconcileMyReview:
		if rec, ok, err := s.myReview(ctx, b, t); err != nil || ok {
			return rec, ok, err
		}
	case t.State == turn.Sent:
		if now.Sub(t.SentAt) < SentGrace+s.FastJob {
			return record.Record{}, false, nil
		}
		busy, err := w.busy(ctx, t.SentTo)
		if err != nil || busy {
			return record.Record{}, false, err
		}
		return record.Unrecorded(neverStartedMessage), true, nil
	}
	if t.Session == "" || !b.OwesRecord(t.Seq) {
		return record.Record{}, false, nil
	}
	sess, ok, err := s.Registry.Session(ctx, t.Session)
	if err != nil || !ok || sess.WorkspaceID == "" {
		return record.Record{}, false, err
	}
	term, listed, err := w.terminal(ctx, sess)
	if err != nil {
		return record.Record{}, false, err
	}
	if !listed {
		return record.Unrecorded(goneMessage), (startup || sess.State() != turn.SessionActive) && w.gone(ctx, sess), nil
	}
	pane, done := w.done(sess.Pane)
	if !done || (!startup && !w.doneTwice(sess.Pane)) || now.Sub(term.LastOutput) < Quiet {
		return record.Record{}, false, nil
	}
	since := t.StartedAt
	if at, ok := b.AnsweredAt(t.Seq); ok && at.After(since) {
		since = at
	}
	if last, ok, err := LastStop(ctx, s.Registry, t.Session); err != nil || (ok && last.At.After(since)) {
		return record.Record{}, false, err
	}
	return record.Unrecorded(pane.LastMessage), true, nil
}

var verdicts = map[string]string{"APPROVED": "APPROVE", "CHANGES_REQUESTED": "REQUEST_CHANGES", "COMMENTED": "COMMENT"}

func (s *Service) myReview(ctx context.Context, b *turn.Book, t turn.Turn) (record.Record, bool, error) {
	last, ok := b.LastRecord(t.Seq)
	if !ok {
		return record.Record{}, false, nil
	}
	reviews, err := s.GitHub.MyReviews(ctx, b.Unit, last.At)
	if err != nil {
		return record.Record{}, false, err
	}
	var newest *MyReview
	for i, r := range reviews {
		if _, known := verdicts[r.State]; known && r.At.After(last.At) && (newest == nil || r.At.After(newest.At)) {
			newest = &reviews[i]
		}
	}
	if newest == nil {
		return record.Record{}, false, nil
	}
	url := newest.URL
	switch {
	case url == "" && newest.ID != "":
		url = urlOf(b.Unit) + "#pullrequestreview-" + newest.ID
	case url == "":
		url = urlOf(b.Unit)
	}
	return record.Record{
		State:   record.Done,
		Summary: "my review on GitHub closed the question: " + newest.State,
		Outcome: &record.Outcome{Verdict: verdicts[newest.State], URL: url},
	}, true, nil
}

// closeTurn records rec for t unless the turn changed since it was read: a
// record that arrives first wins.
func (s *Service) closeTurn(ctx context.Context, ref unit.Unit, seen turn.Turn, seenRecord int64, rec record.Record) (bool, error) {
	s.mu.Lock()
	b, err := s.book(ctx, ref)
	if err != nil {
		s.mu.Unlock()
		return false, err
	}
	t, ok := bySeq(&b, seen.Seq)
	if !ok || t.State != seen.State || t.Session != seen.Session || lastRecordID(&b, t.Seq) != seenRecord {
		s.mu.Unlock()
		return false, nil
	}
	hash := b.RecordHash(t.Seq, rec)
	b.Record(turn.Record{TurnSeq: t.Seq, Session: t.Session, Head: t.Head, Hash: hash, At: s.now(), Acked: true, Record: rec})
	ev := s.event(ref, KindTurnEnded, t.ID+":"+short(hash), rec.EventData())
	_, err = s.Store.Save(ctx, b, ev)
	s.mu.Unlock()
	if err != nil {
		return false, err
	}
	s.publish(ev)
	return true, nil
}

// world is one run's view of Orca: signals, never a turn's state.
type world struct {
	s                   *Service
	agents              []AgentObservation
	terminals           map[string][]Terminal
	census              *Census
	doneNow, doneBefore map[string]time.Time
	warnings            []string
}

func (s *Service) observe(ctx context.Context) (*world, error) {
	agents, err := s.Sessions.Agents(ctx)
	if err != nil {
		return nil, err
	}
	w := &world{s: s, agents: agents, terminals: map[string][]Terminal{}, doneNow: map[string]time.Time{}}
	for _, a := range agents {
		if a.State == turn.AgentDone && a.Pane != "" {
			w.doneNow[a.Pane] = s.now()
		}
	}
	return w, nil
}

func (w *world) terminal(ctx context.Context, sess turn.Session) (Terminal, bool, error) {
	terms, ok := w.terminals[sess.WorkspaceID]
	if !ok {
		var err error
		if terms, err = w.s.Sessions.Terminals(ctx, sess.WorkspaceID); err != nil {
			w.warnings = append(w.warnings, fmt.Sprintf("the terminals of %s: %v", sess.WorkspaceID, err))
			terms = nil
		}
		w.terminals[sess.WorkspaceID] = terms
	}
	i := slices.IndexFunc(terms, func(t Terminal) bool { return t.Session == sess.ID })
	if i < 0 {
		return Terminal{}, false, nil
	}
	return terms[i], true, nil
}

// gone says whether its manager's complete census lists no process for the
// session; an unreadable or incomplete census counts no process gone.
func (w *world) gone(ctx context.Context, sess turn.Session) bool {
	if sess.WorkspaceID == "" {
		return false
	}
	if w.census == nil {
		c, err := w.s.Sessions.Census(ctx)
		switch {
		case err != nil:
			w.warnings = append(w.warnings, fmt.Sprintf("the terminal census: %v; no session counted gone", err))
		case !c.Complete:
			w.warnings = append(w.warnings, "a process census is incomplete; none of its manager's sessions counted gone")
		}
		w.census = &c
	}
	if !w.census.CompleteOf(sess.Manager) {
		return false
	}
	_, listed := w.census.Sessions[sess.ID]
	return !listed
}

func (w *world) done(pane string) (AgentObservation, bool) {
	if pane == "" {
		return AgentObservation{}, false
	}
	i := slices.IndexFunc(w.agents, func(a AgentObservation) bool { return a.Pane == pane && a.State == turn.AgentDone })
	if i < 0 {
		return AgentObservation{}, false
	}
	return w.agents[i], true
}

func (w *world) doneTwice(pane string) bool {
	_, ok := w.doneBefore[pane]
	return ok
}

// busy says whether the handle's agent is working; with no known pane, the
// workspace's agents decide.
func (w *world) busy(ctx context.Context, handle string) (bool, error) {
	if handle == "" {
		return false, nil
	}
	sess, ok, err := w.s.Registry.Session(ctx, handle)
	if err != nil || !ok {
		return false, err
	}
	if sess.Pane != "" {
		return busy(sess, w.agents, turn.AgentWorking), nil
	}
	return busy(sess, w.agents, turn.AgentWorking, turn.AgentWaiting), nil
}
