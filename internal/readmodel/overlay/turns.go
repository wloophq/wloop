package overlay

import (
	"fmt"
	"slices"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/turns"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/turn"
)

// Run is what a unit's row reads of its turn book.
type Run struct {
	Turn Turn
	// Session is the handle of a started turn's session, "" otherwise.
	Session string
	// Question is the open question or stop of the book, nil for none.
	Question *api.QuestionView
	// Unstarted are the ids of the turns that ended before their agent
	// started them.
	Unstarted map[string]bool
}

// RunOf is what a row reads of b: its newest turn of an intent of reg that
// drives the row, else its newest turn; false for a book with no turn.
func RunOf(b turn.Book, reg *dintent.Registry) (Run, bool) {
	t, ok := b.Newest(drivingIntents(reg)...)
	if !ok {
		if t, ok = b.Newest(); !ok {
			return Run{}, false
		}
	}
	f := Turn{Intent: string(t.Intent), Role: t.Role, State: string(t.State), At: t.SentAt}
	if !t.StartedAt.IsZero() {
		f.At = t.StartedAt
	}
	if r, ok := b.LastRecord(t.Seq); ok {
		f.Summary, f.At = r.Record.Headline(), r.At
	}
	f.AnsweredAt, _ = b.AnsweredAt(t.Seq)
	run := Run{Turn: f, Unstarted: Unstarted(b)}
	if q, ok := b.Question(reg.Answerable()); ok {
		qLast, _ := b.LastRecord(q.Seq)
		run.Question, _ = QuestionOf(q, qLast)
	}
	if t.State == turn.Started && t.Session != "" {
		run.Session = t.Session
	}
	return run, true
}

// Unstarted are the ids of b's turns that ended before their agent started
// them, which posted nothing.
func Unstarted(b turn.Book) map[string]bool {
	var out map[string]bool
	for _, t := range b.Turns {
		if !t.State.Open() && t.StartedAt.IsZero() {
			if out == nil {
				out = map[string]bool{}
			}
			out[t.ID] = true
		}
	}
	return out
}

// drivingIntents are the intents whose newest turn drives the row; with
// none, any turn does.
func drivingIntents(reg *dintent.Registry) []turn.Intent {
	var out []turn.Intent
	for _, d := range reg.All() {
		if d.DrivesRow {
			out = append(out, turn.Intent(d.Name))
		}
	}
	return out
}

// QuestionOf is the question view of a turn that waits on the user, from
// its last record; ok is false for any other turn.
func QuestionOf(t *turn.Turn, last *turn.Record) (*api.QuestionView, bool) {
	if t.State != turn.Asked && !t.State.Stopped() {
		return nil, false
	}
	q := &api.QuestionView{State: string(t.State)}
	if last == nil {
		return q, true
	}
	q.RecordID = last.ID
	if rq := last.Record.Question; rq != nil {
		q.Kind, q.Text, q.Options, q.Recommend, q.Publishes = rq.Kind, rq.Text, slices.Clone(rq.Options), rq.Recommend, rq.Publishes
	}
	if o := last.Record.Outcome; o != nil {
		q.Verdict, q.Blocking, q.NonBlocking = o.Verdict, o.Blocking, o.NonBlocking
	}
	if last.Record.State == record.UnrecordedState {
		q.LastMessage = last.Record.Resume
	}
	return q, true
}

// SessionWaiting says whether the agents show sess's agent waiting; with
// no known pane, any agent of its workspace counts.
func SessionWaiting(sess turn.Session, agents []turns.AgentObservation) bool {
	return slices.ContainsFunc(agents, func(a turns.AgentObservation) bool {
		if a.State != turn.AgentWaiting {
			return false
		}
		if sess.Pane != "" {
			return a.Pane == sess.Pane
		}
		return sess.WorkspaceID != "" && a.WorkspaceID == sess.WorkspaceID
	})
}

// Briefing is the line of a row waiting for its brief: the attempt and
// the last error.
func Briefing(i api.Intake) string {
	switch {
	case i.Attempt > 1 && i.LastError != "":
		return fmt.Sprintf("briefing… (attempt %d; %s)", i.Attempt, i.LastError)
	case i.Attempt > 0:
		return fmt.Sprintf("briefing… (attempt %d)", i.Attempt)
	}
	return "briefing…"
}
