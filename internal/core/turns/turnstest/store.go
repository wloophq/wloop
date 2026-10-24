package turnstest

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"github.com/wloophq/wloop/internal/domain/unit"
	"maps"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/turns"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/turn"
)

type Option func(*config)

type config struct{ t testing.TB }

// Strict makes a fake fail t on a save that carries no event.
func Strict(t testing.TB) Option { return func(c *config) { c.t = t } }

func newConfig(opts []Option) config {
	var c config
	for _, o := range opts {
		o(&c)
	}
	return c
}

func (c config) want(evs []port.Event, save string) {
	if c.t != nil && len(evs) == 0 {
		c.t.Helper()
		c.t.Errorf("turnstest: %s without an event", save)
	}
}

// eventLog keeps the events of a fake as a store would: a keyed event once,
// an unkeyed one moved a nanosecond on until its time is free.
type eventLog struct{ list []port.Event }

func checkEvents(evs []port.Event) error {
	for _, ev := range evs {
		if ev.At.IsZero() {
			return fmt.Errorf("the %s event has no time", ev.Kind)
		}
	}
	return nil
}

func (l *eventLog) add(evs []port.Event) {
	for _, ev := range evs {
		ev.At = ev.At.UTC()
		if ev.Key != "" {
			if !slices.ContainsFunc(l.list, func(o port.Event) bool { return o.Unit == ev.Unit && o.Kind == ev.Kind && o.Key == ev.Key }) {
				l.list = append(l.list, ev)
			}
			continue
		}
		for slices.ContainsFunc(l.list, func(o port.Event) bool {
			return o.Key == "" && o.Unit == ev.Unit && o.Kind == ev.Kind && o.At.Equal(ev.At)
		}) {
			ev.At = ev.At.Add(time.Nanosecond)
		}
		l.list = append(l.list, ev)
	}
}

// Store is an in-memory turns.Store.
type Store struct {
	cfg config

	mu                  sync.Mutex
	books               map[unit.Unit]turn.Book
	seq, record, answer int64
	events              eventLog
}

var (
	_ turns.Store   = (*Store)(nil)
	_ turns.Reports = (*Store)(nil)
)

func NewStore(opts ...Option) *Store {
	return &Store{cfg: newConfig(opts), books: map[unit.Unit]turn.Book{}}
}

// Put stores b without events, keeping the Seq and IDs it sets, and returns
// it as stored.
func (s *Store) Put(b turn.Book) turn.Book {
	s.mu.Lock()
	defer s.mu.Unlock()
	got, err := s.save(b, true)
	if err != nil {
		panic(err)
	}
	return got
}

// Events is every event the saves wrote, in order.
func (s *Store) Events() []port.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.events.list)
}

func (s *Store) Books(_ context.Context, refs ...unit.Unit) (map[unit.Unit]turn.Book, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(refs) == 0 {
		refs = slices.Collect(maps.Keys(s.books))
	}
	out := make(map[unit.Unit]turn.Book, len(refs))
	for _, r := range refs {
		out[r] = s.book(r)
	}
	return out, nil
}

func (s *Store) book(r unit.Unit) turn.Book {
	b, ok := s.books[r]
	if !ok {
		return turn.Book{Unit: r}
	}
	return cloneBook(b)
}

func (s *Store) BookOf(_ context.Context, k turns.Key) (turn.Book, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if k == (turns.Key{}) {
		return turn.Book{}, false, nil
	}
	var found unit.Unit
	var newest int64
	for r, b := range s.books {
		for _, t := range b.Turns {
			if t.Seq > newest && (k.Turn == "" || t.ID == k.Turn) && (k.Session == "" || t.Session == k.Session || t.SentTo == k.Session) {
				found, newest = r, t.Seq
			}
		}
	}
	if newest == 0 {
		return turn.Book{}, false, nil
	}
	return s.book(found), true, nil
}

func (s *Store) UnitsOf(_ context.Context, handles []string) (map[string]unit.Unit, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]unit.Unit, len(handles))
	for _, h := range handles {
		if h == "" {
			continue
		}
		var newest int64
		for u, b := range s.books {
			for _, t := range b.Turns {
				if t.Seq > newest && (t.Session == h || t.SentTo == h) {
					out[h], newest = u, t.Seq
				}
			}
		}
	}
	return out, nil
}

func (s *Store) Save(_ context.Context, b turn.Book, evs ...port.Event) (turn.Book, error) {
	s.cfg.want(evs, "Store.Save")
	if err := checkEvents(evs); err != nil {
		return turn.Book{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	got, err := s.save(b, false)
	if err != nil {
		return turn.Book{}, err
	}
	s.events.add(evs)
	return got, nil
}

func (s *Store) save(b turn.Book, seed bool) (turn.Book, error) {
	in := cloneBook(b)
	stored := cloneBook(s.books[b.Unit])
	stored.Unit = b.Unit
	seq, recordID, answerID := s.seq, s.record, s.answer
	var newest int64
	for _, t := range in.Turns {
		i := slices.IndexFunc(stored.Turns, func(o turn.Turn) bool { return o.Seq == t.Seq })
		switch {
		case t.Seq != 0 && i >= 0:
			stored.Turns[i] = t
			continue
		case t.Seq != 0 && !seed:
			return turn.Book{}, fmt.Errorf("%s has no turn %d", b.Unit, t.Seq)
		case t.Seq == 0:
			seq++
			t.Seq = seq
		}
		seq = max(seq, t.Seq)
		newest = t.Seq
		stored.Turns = append(stored.Turns, t)
	}
	for i, t := range stored.Turns {
		if newest != 0 && t.State == turn.Replaced && t.Before != "" && t.ReplacedBy == 0 {
			stored.Turns[i].ReplacedBy = newest
		}
	}
	for _, r := range in.Records {
		if r.TurnSeq == 0 {
			r.TurnSeq = newest
		}
		i := slices.IndexFunc(stored.Records, func(o turn.Record) bool { return o.ID == r.ID })
		switch {
		case r.ID != 0 && i >= 0:
			stored.Records[i] = r
			continue
		case r.ID != 0 && !seed:
			return turn.Book{}, fmt.Errorf("%s has no record %d", b.Unit, r.ID)
		case slices.ContainsFunc(stored.Records, func(o turn.Record) bool { return o.TurnSeq == r.TurnSeq && o.Hash == r.Hash }):
			continue
		case r.ID == 0:
			recordID++
			r.ID = recordID
		}
		recordID = max(recordID, r.ID)
		stored.Records = append(stored.Records, r)
	}
	for _, a := range in.Answers {
		if a.TurnSeq == 0 {
			a.TurnSeq = newest
		}
		i := slices.IndexFunc(stored.Answers, func(o turn.Answer) bool { return o.ID == a.ID })
		switch {
		case a.ID != 0 && i >= 0:
			stored.Answers[i] = a
			continue
		case a.ID != 0 && !seed:
			return turn.Book{}, fmt.Errorf("%s has no answer %d", b.Unit, a.ID)
		case a.ID == 0:
			answerID++
			a.ID = answerID
		}
		answerID = max(answerID, a.ID)
		stored.Answers = append(stored.Answers, a)
	}
	slices.SortFunc(stored.Turns, func(x, y turn.Turn) int { return cmp.Compare(x.Seq, y.Seq) })
	slices.SortFunc(stored.Records, func(x, y turn.Record) int { return cmp.Compare(x.ID, y.ID) })
	slices.SortFunc(stored.Answers, func(x, y turn.Answer) int { return cmp.Compare(x.ID, y.ID) })
	s.seq, s.record, s.answer = seq, recordID, answerID
	if len(stored.Turns)+len(stored.Records)+len(stored.Answers) > 0 {
		s.books[b.Unit] = stored
	}
	return cloneBook(stored), nil
}

func (s *Store) Open(context.Context) ([]unit.Unit, error) {
	return s.refs(func(t turn.Turn) bool { return t.State.Open() }), nil
}

func (s *Store) WithTurns(_ context.Context, i turn.Intent) ([]unit.Unit, error) {
	return s.refs(func(t turn.Turn) bool { return t.Intent == i && t.State != turn.Withdrawn }), nil
}

func (s *Store) refs(match func(turn.Turn) bool) []unit.Unit {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []unit.Unit
	for r, b := range s.books {
		if slices.ContainsFunc(b.Turns, match) {
			out = append(out, r)
		}
	}
	slices.SortFunc(out, func(a, b unit.Unit) int {
		if a.Less(b) {
			return -1
		}
		if b.Less(a) {
			return 1
		}
		return 0
	})
	return out
}

func cloneBook(b turn.Book) turn.Book {
	out := turn.Book{Unit: b.Unit, Turns: slices.Clone(b.Turns), Answers: slices.Clone(b.Answers)}
	for _, r := range b.Records {
		r.Record = cloneRecord(r.Record)
		out.Records = append(out.Records, r)
	}
	for i := range out.Turns {
		t := &out.Turns[i]
		t.SentAt, t.StartedAt, t.EndedAt = utc(t.SentAt), utc(t.StartedAt), utc(t.EndedAt)
		t.Inputs = maps.Clone(t.Inputs)
	}
	for i := range out.Records {
		out.Records[i].At = utc(out.Records[i].At)
	}
	for i := range out.Answers {
		out.Answers[i].At = utc(out.Answers[i].At)
	}
	return out
}

func cloneRecord(r record.Record) record.Record {
	raw, err := json.Marshal(r)
	if err != nil {
		panic(err)
	}
	var out record.Record
	if err := json.Unmarshal(raw, &out); err != nil {
		panic(err)
	}
	return out
}

func utc(t time.Time) time.Time {
	if t.IsZero() {
		return time.Time{}
	}
	return t.UTC().Round(0)
}

var (
	r7  = githubUnit("acme/api#7")
	r8  = githubUnit("acme/api#8")
	r9  = githubUnit("acme/web#9")
	at0 = time.Date(2026, 10, 17, 12, 0, 0, 123456789, time.UTC)
)

func githubUnit(key string) unit.Unit { return unit.Unit{Provider: unit.GitHub, Key: key} }

func ev(u unit.Unit, kind, key string, at time.Time) port.Event {
	return port.Event{Unit: u, Kind: kind, Key: key, At: at}
}

func mustSave(t *testing.T, s turns.Store, b turn.Book) turn.Book {
	t.Helper()
	got, err := s.Save(context.Background(), b, ev(b.Unit, turns.KindTurnSent, b.Turns[len(b.Turns)-1].ID, at0))
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func sent(i turn.Intent, id string, st turn.State, minutes int) turn.Turn {
	return turn.Turn{Intent: i, ID: id, Trigger: turn.Automated, State: st, SentAt: at0.Add(time.Duration(minutes) * time.Minute)}
}

// RunStore checks a turns.Store; newStore returns an empty one.
// Repository is what RunStore exercises: the turns store and its reports.
type Repository interface {
	turns.Store
	turns.Reports
}

func RunStore(t *testing.T, newStore func(t *testing.T) Repository) {
	ctx := context.Background()

	t.Run("save assigns ids and books return every field", func(t *testing.T) {
		s := newStore(t)
		first := mustSave(t, s, turn.Book{Unit: r7, Turns: []turn.Turn{{
			Intent: turn.Intent(dintent.Review), ID: "r1", Trigger: turn.ByHand, Guidance: "look at the migration", Head: "h1",
			SentTo: "term_1", Session: "term_1", State: turn.Asked,
			SentAt: at0, StartedAt: at0.Add(time.Second), EndedAt: at0.Add(2 * time.Second),
			Inputs: map[string]any{"self_review": true, "situation": "Waiting on: @bia"},
		}}})
		if len(first.Turns) != 1 || first.Turns[0].Seq == 0 {
			t.Fatalf("no seq assigned: %+v", first.Turns)
		}
		seq := first.Turns[0].Seq
		b := first
		b.Turns = append(b.Turns, turn.Turn{
			Intent: turn.Intent(dintent.Address), ID: "r2", Trigger: turn.Automated, Head: "h2", SentTo: "term_2",
			State: turn.Sent, SentAt: at0.Add(time.Minute), ResumeOf: seq,
		})
		b.Records = append(b.Records, turn.Record{
			TurnSeq: seq, Session: "term_1", AgentSession: "s-1", Head: "h1", Hash: "abcdef0123456789", At: at0.Add(3 * time.Second), Acked: true,
			Record: record.Record{
				State: record.Asked, Summary: "keep both columns?",
				Outcome:  &record.Outcome{Verdict: "COMMENT", URL: "https://github.com/acme/api/pull/7#r1", Blocking: 1, NonBlocking: 2},
				Question: &record.Question{Kind: "choice", Text: "Which one?", Options: []record.Option{{ID: "a", Label: "A", Consequence: "drops B", Publishes: true}}, Recommend: "a", Publishes: true},
				Artifact: &record.Artifact{Kind: "review", Body: "body", Items: []record.Item{{Path: "x.go", Line: 3, Body: "nit"}}},
				Resume:   "resume", Next: "next", User: "the user said",
			},
		})
		b.Answers = append(b.Answers, turn.Answer{
			TurnSeq: seq, Text: "keep both", OptionID: "a", Yes: true, No: true, Source: turn.Cockpit, Route: turn.Typed, Session: "term_1", At: at0.Add(4 * time.Second),
		})
		got := mustSave(t, s, b)
		if len(got.Turns) != 2 || got.Turns[1].Seq <= seq || len(got.Records) != 1 || got.Records[0].ID == 0 || len(got.Answers) != 1 || got.Answers[0].ID == 0 {
			t.Fatalf("ids not assigned: %+v", got)
		}
		want := b
		want.Turns[1].Seq, want.Records[0].ID, want.Answers[0].ID = got.Turns[1].Seq, got.Records[0].ID, got.Answers[0].ID
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("save returned\n%+v\nwant\n%+v", got, want)
		}
		books, err := s.Books(ctx, r7, r8)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(books[r7], want) {
			t.Fatalf("books returned\n%+v\nwant\n%+v", books[r7], want)
		}
		if e, ok := books[r8]; !ok || e.Unit != r8 || len(e.Turns)+len(e.Records)+len(e.Answers) != 0 {
			t.Fatalf("a unit with no turn: %+v %v", e, ok)
		}
		all, err := s.Books(ctx)
		if err != nil || len(all) != 1 || !reflect.DeepEqual(all[r7], want) {
			t.Fatalf("every book: %+v %v", all, err)
		}
	})

	t.Run("a replaced turn names the turn sent and its state, and a withdraw restores it", func(t *testing.T) {
		s := newStore(t)
		b := mustSave(t, s, turn.Book{Unit: r7, Turns: []turn.Turn{sent(turn.Intent(dintent.Review), "r1", turn.Asked, 0)}})
		b.Send(turn.Turn{Intent: turn.Intent(dintent.Review), ID: "r2", Trigger: turn.ByHand, SentAt: at0.Add(time.Hour)})
		b = mustSave(t, s, b)
		if old, sentNow := b.Turns[0], b.Turns[1]; old.State != turn.Replaced || old.Before != turn.Asked || old.ReplacedBy != sentNow.Seq {
			t.Fatalf("replaced turn %+v, sent %+v", old, sentNow)
		}
		books, _ := s.Books(ctx, r7)
		if !reflect.DeepEqual(books[r7], b) {
			t.Fatalf("books\n%+v\nwant\n%+v", books[r7], b)
		}
		if _, err := b.Withdraw(b.Turns[1].Seq, at0.Add(2*time.Hour)); err != nil {
			t.Fatal(err)
		}
		got := mustSave(t, s, b)
		if old := got.Turns[0]; old.State != turn.Asked || old.Before != "" || old.ReplacedBy != 0 || !old.EndedAt.IsZero() || got.Turns[1].State != turn.Withdrawn {
			t.Fatalf("after the withdraw: %+v", got.Turns)
		}
		only := mustSave(t, s, turn.Book{Unit: r8, Turns: []turn.Turn{sent(turn.Intent(dintent.Address), "r3", turn.Withdrawn, 0)}})
		refs, err := s.WithTurns(ctx, turn.Intent(dintent.Address))
		if err != nil || len(refs) != 0 || len(only.Turns) != 1 {
			t.Fatalf("with address turns %v %v", refs, err)
		}
	})

	t.Run("save updates the entries it already has", func(t *testing.T) {
		s := newStore(t)
		b := mustSave(t, s, turn.Book{Unit: r7,
			Turns:   []turn.Turn{sent(turn.Intent(dintent.Review), "r1", turn.Started, 0)},
			Records: []turn.Record{{Hash: "h", At: at0, Record: record.Record{State: record.Asked, Summary: "q"}}},
			Answers: []turn.Answer{{Text: "yes", Source: turn.TUI, Route: turn.Pending, At: at0}},
		})
		if b.Records[0].TurnSeq != b.Turns[0].Seq || b.Answers[0].TurnSeq != b.Turns[0].Seq {
			t.Fatalf("entries of a new turn not tied to it: %+v", b)
		}
		b.Turns[0].State, b.Turns[0].EndedAt = turn.Done, at0.Add(time.Hour)
		b.Turns[0].Inputs = map[string]any{"situation": "Waiting on: @cy"}
		b.Records[0].Acked = true
		b.Answers[0].Route, b.Answers[0].Session = turn.Typed, "term_1"
		got := mustSave(t, s, b)
		if !reflect.DeepEqual(got, b) {
			t.Fatalf("got\n%+v\nwant\n%+v", got, b)
		}
		books, _ := s.Books(ctx, r7)
		if !reflect.DeepEqual(books[r7], b) {
			t.Fatalf("books\n%+v\nwant\n%+v", books[r7], b)
		}
	})

	t.Run("a record of the same turn and hash is one row", func(t *testing.T) {
		s := newStore(t)
		b := mustSave(t, s, turn.Book{Unit: r7, Turns: []turn.Turn{sent(turn.Intent(dintent.Review), "r1", turn.Started, 0)}})
		seq := b.Turns[0].Seq
		rec := turn.Record{TurnSeq: seq, Hash: "same", At: at0, Record: record.Record{State: record.Done, Summary: "done"}}
		b.Records = append(b.Records, rec, rec)
		b = mustSave(t, s, b)
		b.Records = append(b.Records, rec)
		got := mustSave(t, s, b)
		if len(got.Records) != 1 {
			t.Fatalf("records %+v", got.Records)
		}
		other := rec
		other.Hash = "other"
		got.Records = append(got.Records, other)
		if got = mustSave(t, s, got); len(got.Records) != 2 {
			t.Fatalf("a record of another hash: %+v", got.Records)
		}
	})

	t.Run("an identical record after an answer is stored", func(t *testing.T) {
		s := newStore(t)
		b := mustSave(t, s, turn.Book{Unit: r7, Turns: []turn.Turn{sent(turn.Intent(dintent.Review), "r1", turn.Started, 0)}})
		seq := b.Turns[0].Seq
		asked := record.Record{State: record.Asked, Summary: "q"}
		add := func(b turn.Book, m int) turn.Book {
			b.Record(turn.Record{TurnSeq: seq, Hash: b.RecordHash(seq, asked), At: at0.Add(time.Duration(m) * time.Minute), Record: asked})
			return mustSave(t, s, b)
		}
		b = add(b, 1)
		b.Answer(turn.Answer{TurnSeq: seq, Text: "go", Source: turn.Cockpit, At: at0.Add(2 * time.Minute)}).Route = turn.Typed
		b = mustSave(t, s, b)
		b = add(b, 3)
		if b = add(b, 4); len(b.Records) != 2 {
			t.Fatalf("records %+v", b.Records)
		}
	})

	t.Run("book of a turn id or a handle is the newest turn's", func(t *testing.T) {
		s := newStore(t)
		older := mustSave(t, s, turn.Book{Unit: r7, Turns: []turn.Turn{sent(turn.Intent(dintent.Review), "abc", turn.Done, 0)}})
		started := sent(turn.Intent(dintent.Address), "def", turn.Started, 1)
		started.Session = "term_1"
		mustSave(t, s, turn.Book{Unit: r8, Turns: []turn.Turn{sent(turn.Intent(dintent.Review), "abc", turn.Sent, 2), started}})
		sentTo := sent(turn.Intent(dintent.Nudge), "ghi", turn.Sent, 3)
		sentTo.SentTo = "term_1"
		mustSave(t, s, turn.Book{Unit: r9, Turns: []turn.Turn{sentTo}})
		for _, c := range []struct {
			k    turns.Key
			want unit.Unit
			ok   bool
		}{
			{turns.Key{Turn: "abc"}, r8, true},
			{turns.Key{Turn: "def"}, r8, true},
			{turns.Key{Session: "term_1"}, r9, true},
			{turns.Key{Turn: "def", Session: "term_1"}, r8, true},
			{turns.Key{Turn: "nope"}, unit.Unit{}, false},
			{turns.Key{Session: "term_9"}, unit.Unit{}, false},
			{turns.Key{}, unit.Unit{}, false},
		} {
			b, ok, err := s.BookOf(ctx, c.k)
			if err != nil || ok != c.ok || b.Unit != c.want {
				t.Errorf("BookOf(%+v) = %s %v %v, want %s %v", c.k, b.Unit, ok, err, c.want, c.ok)
			}
		}
		b, _, _ := s.BookOf(ctx, turns.Key{Turn: "abc"})
		if len(b.Turns) != 2 || older.Turns[0].Seq >= b.Turns[0].Seq {
			t.Fatalf("the book of turn abc: %+v", b)
		}
	})

	t.Run("units of handles is the newest turn's", func(t *testing.T) {
		s := newStore(t)
		started := sent(turn.Intent(dintent.Address), "def", turn.Started, 1)
		started.Session = "term_1"
		mustSave(t, s, turn.Book{Unit: r8, Turns: []turn.Turn{started}})
		sentTo := sent(turn.Intent(dintent.Nudge), "ghi", turn.Sent, 3)
		sentTo.SentTo = "term_1"
		mustSave(t, s, turn.Book{Unit: r9, Turns: []turn.Turn{sentTo}})
		other := sent(turn.Intent(dintent.Review), "jkl", turn.Sent, 4)
		other.SentTo = "term_2"
		ran := sent(turn.Intent(dintent.Address), "mno", turn.Started, 5)
		ran.Session = "term_3"
		mustSave(t, s, turn.Book{Unit: r7, Turns: []turn.Turn{other, ran}})
		got, err := s.UnitsOf(ctx, []string{"term_1", "term_2", "term_2", "term_3", "term_9", ""})
		if err != nil || len(got) != 3 || got["term_1"] != r9 || got["term_2"] != r7 || got["term_3"] != r7 {
			t.Fatalf("UnitsOf = %v %v, want term_1 %s, term_2 %s and term_3 %s", got, err, r9, r7, r7)
		}
		if got, err := s.UnitsOf(ctx, nil); err != nil || len(got) != 0 {
			t.Fatalf("UnitsOf(nil) = %v %v", got, err)
		}
	})

	t.Run("open and with turns", func(t *testing.T) {
		s := newStore(t)
		mustSave(t, s, turn.Book{Unit: r9, Turns: []turn.Turn{sent(turn.Intent(dintent.Review), "a", turn.Replaced, 0), sent(turn.Intent(dintent.Review), "b", turn.Asked, 1)}})
		mustSave(t, s, turn.Book{Unit: r7, Turns: []turn.Turn{sent(turn.Intent(dintent.Review), "c", turn.Done, 0), sent(turn.Intent(dintent.Nudge), "d", turn.Started, 1)}})
		mustSave(t, s, turn.Book{Unit: r8, Turns: []turn.Turn{sent(turn.Intent(dintent.Nudge), "e", turn.Settled, 0), sent(turn.Intent(dintent.Address), "f", turn.Failed, 1)}})
		open, err := s.Open(ctx)
		if err != nil || !sameUnits(open, []unit.Unit{r7, r9}) {
			t.Fatalf("open %v %v", open, err)
		}
		reviews, err := s.WithTurns(ctx, turn.Intent(dintent.Review))
		if err != nil || !sameUnits(reviews, []unit.Unit{r7, r9}) {
			t.Fatalf("with review turns %v %v", reviews, err)
		}
		if probes, err := s.WithTurns(ctx, turn.Intent(dintent.Probe)); err != nil || len(probes) != 0 {
			t.Fatalf("with probe turns %v %v", probes, err)
		}
	})

	t.Run("a save whose events cannot be written stores nothing", func(t *testing.T) {
		s := newStore(t)
		b := turn.Book{Unit: r7, Turns: []turn.Turn{sent(turn.Intent(dintent.Review), "r1", turn.Sent, 0)}}
		if _, err := s.Save(ctx, b, ev(r7, turns.KindTurnSent, "r1", at0), ev(r7, turns.KindAnswered, "", time.Time{})); err == nil {
			t.Fatal("saved an event with no time")
		}
		books, err := s.Books(ctx, r7)
		if err != nil || len(books[r7].Turns) != 0 {
			t.Fatalf("the book was stored: %+v %v", books[r7], err)
		}
		if open, _ := s.Open(ctx); len(open) != 0 {
			t.Fatalf("open %v", open)
		}
	})

	t.Run("save refuses an entry it does not have", func(t *testing.T) {
		s := newStore(t)
		b := turn.Book{Unit: r7, Turns: []turn.Turn{sent(turn.Intent(dintent.Review), "r1", turn.Sent, 0)}}
		b.Turns[0].Seq = 999
		if _, err := s.Save(ctx, b, ev(r7, turns.KindTurnSent, "r1", at0)); err == nil {
			t.Fatal("saved a turn it does not have")
		}
	})
}

func sameUnits(got, want []unit.Unit) bool {
	order := func(a, b unit.Unit) int {
		if a.Less(b) {
			return -1
		}
		if b.Less(a) {
			return 1
		}
		return 0
	}
	return slices.Equal(slices.SortedFunc(slices.Values(got), order), slices.SortedFunc(slices.Values(want), order))
}
