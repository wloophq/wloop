package turn

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"
	"strconv"
	"time"

	"github.com/wloophq/wloop/internal/domain/record"
)

var (
	ErrNoTurn       = errors.New("no such turn")
	ErrClosed       = errors.New("the turn is closed")
	ErrOtherSession = errors.New("the turn is started on another session")
)

func (b *Book) Send(t Turn) *Turn {
	return b.send(t, func(*Turn) bool { return true })
}

// SendOn is Send for a turn that will start on session: it replaces only
// the open turns of its intent sent to or started on that session.
func (b *Book) SendOn(t Turn, session string) *Turn {
	return b.send(t, func(o *Turn) bool { return o.Session == session || o.SentTo == session })
}

func (b *Book) send(t Turn, replaces func(*Turn) bool) *Turn {
	if t.ResumeOf != 0 {
		if r, ok := b.bySeq(t.ResumeOf); ok {
			t.Trigger = r.Trigger
		}
	}
	for i := range b.Turns {
		if o := &b.Turns[i]; o.Intent == t.Intent && o.State.Open() && replaces(o) {
			o.Before, o.ReplacedBy = o.State, 0
			o.State, o.EndedAt = Replaced, t.SentAt
		}
	}
	t.State = Sent
	b.Turns = append(b.Turns, t)
	return &b.Turns[len(b.Turns)-1]
}

// Withdraw undoes the send of turn seq: it becomes withdrawn and the turns it
// replaced get their state back.
func (b *Book) Withdraw(seq int64, at time.Time) (*Turn, error) {
	t, ok := b.bySeq(seq)
	switch {
	case !ok:
		return nil, ErrNoTurn
	case t.State != Sent:
		return t, ErrClosed
	}
	t.State, t.EndedAt = Withdrawn, at
	for i := range b.Turns {
		if o := &b.Turns[i]; o.State == Replaced && o.ReplacedBy == seq && o.Before != "" {
			o.State, o.EndedAt, o.Before, o.ReplacedBy = o.Before, time.Time{}, "", 0
		}
	}
	return t, nil
}

func (b *Book) Start(id, handle string, at time.Time) (*Turn, error) {
	t, ok := b.ByID(id)
	switch {
	case !ok:
		return nil, ErrNoTurn
	case t.State == Sent:
		t.State, t.Session, t.StartedAt = Started, handle, at
		return t, nil
	case !t.State.Open():
		return t, ErrClosed
	case t.Session != handle:
		return t, ErrOtherSession
	}
	return t, nil
}

// RecordHash is the hash rec is stored under as the next record of turn seq:
// its content chained to the turn's record before it, so that only a retry of
// the turn's last record, with no answer since, repeats a hash.
func (b *Book) RecordHash(seq int64, rec record.Record) string {
	content := rec.Hash()
	ids := b.recordIDs(seq)
	if len(ids) == 0 {
		return content
	}
	last, _ := b.LastRecord(seq)
	before := int64(0)
	if len(ids) > 1 {
		before = ids[len(ids)-2]
	}
	if last.Hash == chain(content, before) && !b.answeredSince(seq, last.At) {
		return last.Hash
	}
	return chain(content, last.ID)
}

func (b *Book) answeredSince(seq int64, since time.Time) bool {
	return slices.ContainsFunc(b.Answers, func(a Answer) bool {
		return a.TurnSeq == seq && a.Route != Refused && a.At.After(since)
	})
}

func chain(content string, before int64) string {
	if before == 0 {
		return content
	}
	sum := sha256.Sum256([]byte(content + ":" + strconv.FormatInt(before, 10)))
	return hex.EncodeToString(sum[:])
}

func (b *Book) recordIDs(seq int64) []int64 {
	var ids []int64
	for _, r := range b.Records {
		if r.TurnSeq == seq {
			ids = append(ids, r.ID)
		}
	}
	return ids
}

// Record adds r unless it repeats the turn's last record.
func (b *Book) Record(r Record) (added bool) {
	if last, ok := b.LastRecord(r.TurnSeq); ok && last.Hash == r.Hash {
		return false
	}
	b.Records = append(b.Records, r)
	if t, ok := b.bySeq(r.TurnSeq); ok && t.State != Replaced && t.State != Dismissed && t.State != Withdrawn {
		t.State = State(r.Record.State)
		t.EndedAt = time.Time{}
		if r.Record.State.ClosesTurn() {
			t.EndedAt = r.At
		}
	}
	if r.Record.User != "" {
		b.Answers = append(b.Answers, Answer{TurnSeq: r.TurnSeq, Text: r.Record.User, Source: Terminal, Route: Direct, At: r.At})
	}
	return true
}

func (b *Book) Answer(a Answer) *Answer {
	a.Route = Pending
	b.Answers = append(b.Answers, a)
	return &b.Answers[len(b.Answers)-1]
}

func (b *Book) Dismiss(at time.Time, answerable []string) (*Turn, error) {
	t, ok := b.Question(answerable)
	if !ok {
		return nil, ErrNoTurn
	}
	if t.State != Asked && !t.State.Stopped() {
		return t, ErrClosed
	}
	t.State, t.EndedAt = Dismissed, at
	return t, nil
}

// Question is the turn an answer or a dismiss acts on: among the newest turn
// of each answerable intent, the newest one asked or stopped, else the newest
// of them.
func (b *Book) Question(answerable []string) (*Turn, bool) {
	var q *Turn
	for _, name := range answerable {
		if t, ok := b.Newest(Intent(name)); ok && (q == nil || waitsOnUser(t) && !waitsOnUser(q) || waitsOnUser(t) == waitsOnUser(q) && t.Seq > q.Seq) {
			q = t
		}
	}
	return q, q != nil
}

func waitsOnUser(t *Turn) bool { return t.State == Asked || t.State.Stopped() }

// Newest is the newest turn of any of intents, or of any intent when none is
// named.
func (b *Book) Newest(intents ...Intent) (*Turn, bool) {
	return b.newest(func(t Turn) bool { return len(intents) == 0 || slices.Contains(intents, t.Intent) })
}

func (b *Book) OfSession(handle string) (*Turn, bool) {
	return b.newest(func(t Turn) bool { return t.Session == handle || t.SentTo == handle })
}

func (b *Book) ByID(id string) (*Turn, bool) {
	return b.newest(func(t Turn) bool { return t.ID == id })
}

func (b *Book) bySeq(seq int64) (*Turn, bool) {
	for i := len(b.Turns) - 1; i >= 0; i-- {
		if b.Turns[i].Seq == seq {
			return &b.Turns[i], true
		}
	}
	return nil, false
}

// newest skips withdrawn turns.
func (b *Book) newest(match func(Turn) bool) (*Turn, bool) {
	for i := len(b.Turns) - 1; i >= 0; i-- {
		if b.Turns[i].State != Withdrawn && match(b.Turns[i]) {
			return &b.Turns[i], true
		}
	}
	return nil, false
}

func (b *Book) LastRecord(seq int64) (*Record, bool) {
	for i := len(b.Records) - 1; i >= 0; i-- {
		if b.Records[i].TurnSeq == seq {
			return &b.Records[i], true
		}
	}
	return nil, false
}

// OwesRecord says whether the next stop of the turn's session must carry a
// record: the turn is started with none, or asked and the loop typed an answer
// into that session after the record.
func (b *Book) OwesRecord(seq int64) bool {
	t, ok := b.bySeq(seq)
	if !ok {
		return false
	}
	last, recorded := b.LastRecord(seq)
	switch t.State {
	case Started:
		return !recorded
	case Asked:
		return recorded && slices.ContainsFunc(b.Answers, func(a Answer) bool {
			return a.TurnSeq == seq && a.Route.Noticed() && a.Session == t.Session && a.At.After(last.At)
		})
	}
	return false
}

func (b *Book) OpenQuestion(i Intent) bool {
	t, ok := b.Newest(i)
	return ok && t.State == Asked
}

// QuestionMovedPast says whether the open question of intent i is about a
// head other than head, both known, with no answer on its way to it.
func (b *Book) QuestionMovedPast(i Intent, head string) bool {
	t, ok := b.Newest(i)
	if !ok || t.State != Asked || t.Head == "" || head == "" || t.Head == head {
		return false
	}
	return !b.QuestionAnswered(i)
}

// QuestionAnswered says whether the open question of intent i has an answer
// stored since its last record, refused ones aside.
func (b *Book) QuestionAnswered(i Intent) bool {
	t, ok := b.Newest(i)
	if !ok || t.State != Asked {
		return false
	}
	var since time.Time
	if last, ok := b.LastRecord(t.Seq); ok {
		since = last.At
	}
	return b.answeredSince(t.Seq, since)
}

// AnsweredAt is when the newest answer noticed or resumed after the turn's last
// record arrived.
func (b *Book) AnsweredAt(seq int64) (time.Time, bool) {
	var since time.Time
	if last, ok := b.LastRecord(seq); ok {
		since = last.At
	}
	for i := len(b.Answers) - 1; i >= 0; i-- {
		a := b.Answers[i]
		if a.TurnSeq == seq && (a.Route.Noticed() || a.Route == Resumed) && a.At.After(since) {
			return a.At, true
		}
	}
	return time.Time{}, false
}
