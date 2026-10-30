package views_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/wloophq/wloop/internal/domain/unit"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/adapter/view/testsink"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/turns/turnstest"
	"github.com/wloophq/wloop/internal/core/views"
	"github.com/wloophq/wloop/internal/core/views/viewstest"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/view"
)

var _ views.Books = (*sqlite.Store)(nil)

var (
	r7      = unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}
	r8      = unit.Unit{Provider: unit.GitHub, Key: "acme/api#8"}
	r9      = unit.Unit{Provider: unit.GitHub, Key: "acme/api#9"}
	sandbox = unit.Unit{Provider: unit.GitHub, Key: "acme/sandbox#1"}
	t0      = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
)

type clock struct{}

func (clock) Now() time.Time { return t0 }

type publisher struct {
	mu  sync.Mutex
	evs []port.Event
}

func (p *publisher) Publish(evs ...port.Event) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.evs = append(p.evs, evs...)
}

func (p *publisher) kinds(ref unit.Unit) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []string
	for _, ev := range p.evs {
		if ev.Unit == ref {
			out = append(out, ev.Kind)
		}
	}
	return out
}

// orcaBoard is a scripted board with Orca's column names: the projection
// owns reviewing, needs-me, reviewed and completed; any other column is the
// user's.
type orcaBoard struct {
	mu     sync.Mutex
	cards  map[unit.Unit]*orcaCard
	writes []view.Item
}

type orcaCard struct {
	column, comment string
	unread          bool
}

var orcaColumns = map[view.Status]string{view.Working: "reviewing", view.NeedsMe: "needs-me", view.Reviewed: "reviewed", view.Done: "completed"}

func newOrcaBoard() *orcaBoard { return &orcaBoard{cards: map[unit.Unit]*orcaCard{}} }

func (b *orcaBoard) Name() string { return "orca" }

func (b *orcaBoard) Manages(role view.Role, column string) bool {
	if role != view.ReviewRole {
		return false
	}
	for _, c := range orcaColumns {
		if c == column {
			return true
		}
	}
	return false
}

func (b *orcaBoard) Apply(_ context.Context, items []view.Item, force map[unit.Unit]bool) ([]view.Applied, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []view.Applied
	for _, it := range items {
		c, ok := b.cards[it.Unit]
		if !ok || it.Status == view.None {
			continue
		}
		b.writes = append(b.writes, it)
		a := view.Applied{Unit: it.Unit, Role: it.Role, Status: it.Status, Unread: it.Unread, Summary: it.Summary, At: t0}
		if force[it.Unit] || c.column == "" || b.Manages(it.Role, c.column) {
			c.column = orcaColumns[it.Status]
			a.Column = c.column
		} else {
			a.Override = c.column
		}
		c.comment, c.unread = it.Summary, it.Unread
		out = append(out, a)
	}
	return out, nil
}

func (b *orcaBoard) Placements(context.Context) (map[unit.Unit]string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := map[unit.Unit]string{}
	for ref, c := range b.cards {
		out[ref] = c.column
	}
	return out, nil
}

func (b *orcaBoard) place(ref unit.Unit, column string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if c, ok := b.cards[ref]; ok {
		c.column = column
		return
	}
	b.cards[ref] = &orcaCard{column: column}
}

func (b *orcaBoard) card(ref unit.Unit) orcaCard {
	b.mu.Lock()
	defer b.mu.Unlock()
	if c, ok := b.cards[ref]; ok {
		return *c
	}
	return orcaCard{}
}

func (b *orcaBoard) writesOf(ref unit.Unit) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for _, it := range b.writes {
		if it.Unit == ref {
			n++
		}
	}
	return n
}

type fixture struct {
	books *turnstest.Store
	state *viewstest.State
	pub   *publisher
	svc   *views.Service
}

func newFixture(t *testing.T, sinks ...views.Sink) *fixture {
	t.Helper()
	f := &fixture{
		books: turnstest.NewStore(turnstest.Strict(t)),
		state: viewstest.NewState(viewstest.Strict(t)),
		pub:   &publisher{},
	}
	f.svc = &views.Service{
		Books:   f.books,
		Intents: turnstest.Intents(),
		State:   f.state,
		Sinks:   sinks,
		Pub:     f.pub,
		Clock:   clock{},
		Log:     t.Logf,
		Backoff: func(int) time.Duration { return 5 * time.Millisecond },
	}
	return f
}

func (f *fixture) run(t *testing.T) { t.Helper(); runService(t, f.svc) }

func runService(t *testing.T, svc *views.Service) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		svc.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
}

func (f *fixture) applied(t *testing.T, sink string, ref unit.Unit) (view.Applied, bool) {
	t.Helper()
	return f.appliedAs(t, sink, view.Key{Unit: ref, Role: view.ReviewRole})
}

func (f *fixture) appliedAs(t *testing.T, sink string, k view.Key) (view.Applied, bool) {
	t.Helper()
	got, err := f.state.Applied(context.Background(), sink)
	if err != nil {
		t.Fatal(err)
	}
	a, ok := got[k]
	return a, ok
}

func (f *fixture) stored(kind string, ref unit.Unit) bool {
	return slices.ContainsFunc(f.state.Events(), func(ev port.Event) bool { return ev.Kind == kind && ev.Unit == ref })
}

// eventually advances the bubble's clock until cond holds.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for synctest.Wait(); !cond(); synctest.Wait() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func reviewTurn(seq int64, state turn.State) turn.Turn {
	return turn.Turn{Seq: seq, Intent: turn.Intent(dintent.Review), ID: "r1", State: state, Session: "term_1", SentAt: t0}
}

func rec(id, seq int64, st record.State, summary string) turn.Record {
	r := record.Record{State: st, Summary: summary}
	if st == record.Asked {
		r.Resume = "r"
		r.Question = &record.Question{Kind: "confirm", Text: summary}
	}
	if st == record.Done {
		r.Outcome = &record.Outcome{Verdict: "COMMENT", URL: "u"}
	}
	return turn.Record{ID: id, TurnSeq: seq, Hash: summary, At: t0, Record: r}
}

func asked(ref unit.Unit, summary string) turn.Book {
	return turn.Book{Unit: ref, Turns: []turn.Turn{reviewTurn(1, turn.Asked)}, Records: []turn.Record{rec(1, 1, record.Asked, summary)}}
}

func TestOrcaSinkManagedColumns(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		board := newOrcaBoard()
		f := newFixture(t, views.Sink{ViewSink: board, Placements: board})
		f.run(t)
		board.place(r7, "reviewing")

		f.books.Put(asked(r7, "keep both columns?"))
		f.svc.Changed(ctx, r7)
		eventually(t, "the asked card", func() bool { _, ok := f.applied(t, "orca", r7); return ok })
		if c := board.card(r7); c != (orcaCard{"needs-me", "asks: keep both columns?", true}) {
			t.Fatalf("asked card = %+v", c)
		}

		board.place(r7, "later")
		f.books.Put(turn.Book{Unit: r7, Turns: []turn.Turn{reviewTurn(1, turn.Asked)},
			Records: []turn.Record{rec(1, 1, record.Asked, "keep both columns?"), rec(2, 1, record.Asked, "publish now?")}})
		f.svc.Changed(ctx, r7)
		eventually(t, "the override", func() bool { a, _ := f.applied(t, "orca", r7); return a.Override == "later" })
		if c := board.card(r7); c != (orcaCard{"later", "asks: publish now?", true}) {
			t.Fatalf("card the user put in later = %+v", c)
		}
		if !f.stored(views.KindViewOverride, r7) || !slices.Contains(f.pub.kinds(r7), views.KindViewOverride) {
			t.Fatalf("events %v carry no override", f.pub.kinds(r7))
		}

		board.place(r7, "reviewed")
		if n, err := f.svc.Reconcile(ctx); err != nil || n != 1 {
			t.Fatalf("reconcile = %d, %v", n, err)
		}
		eventually(t, "the override cleared", func() bool { a, _ := f.applied(t, "orca", r7); return a.Override == "" })
		if c := board.card(r7); c != (orcaCard{"needs-me", "asks: publish now?", true}) {
			t.Fatalf("card put back in reviewed = %+v", c)
		}
	})
}

func TestPlacementOverride(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		sink := testsink.New("test")
		f := newFixture(t, views.Sink{ViewSink: sink, Placements: sink})
		f.run(t)
		f.books.Put(asked(r7, "keep both?"))
		f.state.Put("test", view.Applied{Unit: r7, Role: view.ReviewRole, Status: view.NeedsMe, Unread: true, Summary: "asks: keep both?", Column: string(view.NeedsMe), At: t0})
		sink.Place(r7, "parked")

		if n, err := f.svc.Reconcile(ctx); err != nil || n != 1 {
			t.Fatalf("reconcile with a placement outside = %d, %v", n, err)
		}
		eventually(t, "the override", func() bool { a, _ := f.applied(t, "test", r7); return a.Override == "parked" })
		if c, m, u := sink.Card(r7); c != "parked" || m != "asks: keep both?" || !u {
			t.Fatalf("card = (%q, %q, %v)", c, m, u)
		}
		if n, err := f.svc.Reconcile(ctx); err != nil || n != 0 {
			t.Fatalf("reconcile with the override stored = %d, %v", n, err)
		}

		sink.Place(r7, string(view.Reviewed))
		if n, err := f.svc.Reconcile(ctx); err != nil || n != 1 {
			t.Fatalf("reconcile with a placement back inside = %d, %v", n, err)
		}
		eventually(t, "the override cleared", func() bool { a, _ := f.applied(t, "test", r7); return a.Override == "" })
		if c, _, _ := sink.Card(r7); c != string(view.NeedsMe) {
			t.Fatalf("card back inside is in %q", c)
		}
	})
}

func TestRoundStartForcesAndClearsOverride(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		board := newOrcaBoard()
		f := newFixture(t, views.Sink{ViewSink: board, Placements: board})
		f.run(t)
		board.place(r7, "later")
		f.state.Put("orca", view.Applied{Unit: r7, Role: view.ReviewRole, Status: view.Reviewed, Summary: "commented", Override: "later", At: t0})
		f.books.Put(turn.Book{Unit: r7, Turns: []turn.Turn{reviewTurn(1, turn.Sent)}})

		f.svc.RoundStarted(ctx, r7)
		eventually(t, "the forced write", func() bool { a, _ := f.applied(t, "orca", r7); return a.Status == view.Working })
		if a, _ := f.applied(t, "orca", r7); a.Override != "" {
			t.Fatalf("the override stayed: %+v", a)
		}
		if c := board.card(r7); c.column != "reviewing" {
			t.Fatalf("card after a round start = %+v", c)
		}
		if !slices.Contains(f.pub.kinds(r7), views.KindViewOverride) {
			t.Fatalf("events %v carry no override change", f.pub.kinds(r7))
		}
	})
}

func TestAFailingSinkIsIsolated(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		broken, working := testsink.New("broken"), testsink.New("working")
		broken.Fail(errors.New("board is down"))
		f := newFixture(t, views.Sink{ViewSink: broken}, views.Sink{ViewSink: working})
		refs := []unit.Unit{r7, r8, sandbox}
		for _, r := range refs {
			f.books.Put(asked(r, "keep both?"))
		}
		f.run(t)

		start := time.Now()
		f.svc.Changed(ctx, refs...)
		if d := time.Since(start); d != 0 {
			t.Fatalf("Changed took %s", d)
		}
		eventually(t, "the working sink's batch", func() bool { return len(working.Batches()) > 0 })
		if b := working.Batches(); len(b) != 1 || len(b[0]) != len(refs) {
			t.Fatalf("working batches = %v", b)
		}
		eventually(t, "a published sink failure", func() bool { return slices.Contains(f.pub.kinds(r7), views.KindViewSinkFailed) })
		if !f.stored(views.KindViewSinkFailed, r7) {
			t.Fatal("the published failure was not stored")
		}
		broken.Fail(nil)
		eventually(t, "the retried items", func() bool {
			got, _ := f.state.Applied(ctx, "broken")
			return len(got) == len(refs)
		})
	})
}

func TestNudgeNeverMovesAReviewCard(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		board := newOrcaBoard()
		f := newFixture(t, views.Sink{ViewSink: board, Placements: board})
		f.run(t)
		board.place(r7, "reviewing")
		board.place(r8, "reviewing")
		f.books.Put(turn.Book{Unit: r7, Turns: []turn.Turn{reviewTurn(1, turn.Done)}, Records: []turn.Record{rec(1, 1, record.Done, "commented")}})
		f.svc.Changed(ctx, r7)
		eventually(t, "the reviewed card", func() bool { return board.card(r7).column == "reviewed" })

		f.books.Put(turn.Book{Unit: r7, Turns: []turn.Turn{{Seq: 2, Intent: turn.Intent(dintent.Nudge), ID: "n1", State: turn.Asked, Session: "term_1", SentAt: t0}},
			Records: []turn.Record{rec(2, 2, record.Asked, "ping them?")}})
		f.books.Put(asked(r8, "keep both?"))
		f.svc.Changed(ctx, r7, r8)
		eventually(t, "the other card", func() bool { return board.card(r8).column == "needs-me" })
		if c := board.card(r7); c != (orcaCard{"reviewed", "commented", false}) || board.writesOf(r7) != 1 {
			t.Fatalf("a nudge moved the review card: %+v after %d writes", c, board.writesOf(r7))
		}
	})
}

func TestPausedRepositoriesGetNoWrites(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		sink := testsink.New("test")
		f := newFixture(t, views.Sink{ViewSink: sink, Placements: sink})
		var mu sync.Mutex
		paused := map[unit.Unit]bool{r7: true}
		f.svc.Paused = func(_ context.Context, u unit.Unit) bool {
			mu.Lock()
			defer mu.Unlock()
			return paused[u]
		}
		f.run(t)
		f.books.Put(asked(r7, "keep both?"))
		f.books.Put(asked(sandbox, "keep both?"))

		writes := func(ref unit.Unit) int {
			n := 0
			for _, b := range sink.Batches() {
				for _, it := range b {
					if it.Unit == ref {
						n++
					}
				}
			}
			return n
		}
		f.svc.Changed(ctx, r7, sandbox)
		eventually(t, "the sandbox write", func() bool { _, ok := f.applied(t, "test", sandbox); return ok })
		if n := writes(r7); n != 0 {
			t.Fatalf("a paused repository got %d writes", n)
		}

		mu.Lock()
		paused = nil
		mu.Unlock()
		if _, err := f.svc.Reconcile(ctx); err != nil {
			t.Fatal(err)
		}
		eventually(t, "the write after resume", func() bool { _, ok := f.applied(t, "test", r7); return ok })
		if n, err := f.svc.Reconcile(ctx); err != nil || n != 0 {
			t.Fatalf("a reconcile after the write = %d, %v", n, err)
		}
		if n := writes(r7); n != 1 {
			t.Fatalf("the resumed item was written %d times", n)
		}
	})
}

func TestNoTurnNoWrite(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		sink := testsink.New("test")
		f := newFixture(t, views.Sink{ViewSink: sink, Placements: sink})
		f.run(t)
		sink.Place(r7, string(view.Working))
		f.books.Put(turn.Book{Unit: r7, Turns: []turn.Turn{{Seq: 1, Intent: turn.Intent(dintent.Nudge), ID: "n1", State: turn.Started, Session: "term_1", SentAt: t0}}})
		f.books.Put(asked(r8, "keep both?"))

		f.svc.Changed(ctx, r7, r8, sandbox)
		f.svc.RoundStarted(ctx, sandbox)
		if _, err := f.svc.Reconcile(ctx); err != nil {
			t.Fatal(err)
		}
		eventually(t, "the other write", func() bool { _, ok := f.applied(t, "test", r8); return ok })
		for _, b := range sink.Batches() {
			for _, it := range b {
				if it.Unit != r8 {
					t.Fatalf("a pull request without a review turn got a write: %+v", it)
				}
			}
		}
	})
}

func TestAMoveBetweenManagedColumnsIsReasserted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		board := newOrcaBoard()
		f := newFixture(t, views.Sink{ViewSink: board, Placements: board})
		f.run(t)
		board.place(r7, "reviewing")
		f.books.Put(asked(r7, "keep both?"))
		f.svc.Changed(ctx, r7)
		eventually(t, "the asked card", func() bool { _, ok := f.applied(t, "orca", r7); return ok })

		board.place(r7, "reviewed")
		if n, err := f.svc.Reconcile(ctx); err != nil || n != 1 {
			t.Fatalf("reconcile = %d, %v", n, err)
		}
		eventually(t, "the card back in needs-me", func() bool { return board.card(r7).column == "needs-me" })
		if n, err := f.svc.Reconcile(ctx); err != nil || n != 0 {
			t.Fatalf("reconcile after the re-assert = %d, %v", n, err)
		}

		restarted := &views.Service{Books: f.books, Intents: turnstest.Intents(), State: f.state, Sinks: []views.Sink{{ViewSink: board, Placements: board}}, Pub: f.pub, Clock: clock{}, Log: t.Logf}
		runService(t, restarted)
		board.place(r7, "completed")
		if n, err := restarted.Reconcile(ctx); err != nil || n != 1 {
			t.Fatalf("reconcile after a restart = %d, %v", n, err)
		}
		eventually(t, "the card back in needs-me after a restart", func() bool { return board.card(r7).column == "needs-me" })
	})
}

// picky fails every item of fail, skips every item of skip without a word,
// and applies the rest.
type picky struct {
	*testsink.Sink
	skip unit.Unit

	mu      sync.Mutex
	fail    unit.Unit
	batches [][]view.Item
}

func (p *picky) heal() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.fail = unit.Unit{}
}

func (p *picky) Apply(ctx context.Context, items []view.Item, force map[unit.Unit]bool) ([]view.Applied, error) {
	p.mu.Lock()
	p.batches = append(p.batches, slices.Clone(items))
	fail := p.fail
	p.mu.Unlock()
	var keep []view.Item
	var errs []error
	for _, it := range items {
		switch it.Unit {
		case fail:
			errs = append(errs, &views.ItemError{Key: it.Key(), Err: errors.New("workspace is gone")})
		case p.skip:
		default:
			keep = append(keep, it)
		}
	}
	got, err := p.Sink.Apply(ctx, keep, force)
	return got, errors.Join(append(errs, err)...)
}

func (p *picky) seen() [][]view.Item {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.batches)
}

func TestOneFailingItemDoesNotStallTheSink(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		sink := &picky{Sink: testsink.New("test"), fail: r7, skip: r8}
		f := newFixture(t, views.Sink{ViewSink: sink})
		f.svc.Backoff = func(int) time.Duration { return time.Hour }
		f.run(t)
		for _, r := range []unit.Unit{r7, r8, r9, sandbox} {
			f.books.Put(asked(r, "keep both?"))
		}

		f.svc.Changed(ctx, r7, r8, sandbox)
		eventually(t, "the healthy item", func() bool { _, ok := f.applied(t, "test", sandbox); return ok })
		eventually(t, "the stored failure", func() bool { return f.stored(views.KindViewSinkFailed, r7) })
		if f.stored(views.KindViewSinkFailed, r8) || f.stored(views.KindViewSinkFailed, sandbox) {
			t.Fatalf("a failure was stored for an item the sink did not fail: %+v", f.state.Events())
		}

		f.svc.Changed(ctx, r9)
		eventually(t, "a new item during the backoff", func() bool { _, ok := f.applied(t, "test", r9); return ok })
		b := sink.seen()
		if len(b) != 2 || len(b[1]) != 1 || b[1][0].Unit != r9 {
			t.Fatalf("batches = %+v, want the second to hold only the new item", b)
		}
	})
}

func TestBothRolesAreTracked(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		sink := testsink.New("test")
		f := newFixture(t, views.Sink{ViewSink: sink})
		f.run(t)
		f.books.Put(turn.Book{Unit: r7,
			Turns:   []turn.Turn{reviewTurn(1, turn.Asked), {Seq: 2, Intent: turn.Intent(dintent.Address), ID: "a1", State: turn.Asked, Session: "term_2", SentAt: t0}},
			Records: []turn.Record{rec(1, 1, record.Asked, "keep both?"), rec(2, 2, record.Asked, "rebase now?")}})

		f.svc.Changed(ctx, r7)
		author := view.Key{Unit: r7, Role: view.AuthorRole}
		eventually(t, "both items", func() bool {
			_, review := f.applied(t, "test", r7)
			_, own := f.appliedAs(t, "test", author)
			return review && own
		})
		if a, _ := f.applied(t, "test", r7); a.Summary != "asks: keep both?" {
			t.Fatalf("review item = %+v", a)
		}
		if a, _ := f.appliedAs(t, "test", author); a.Summary != "asks: rebase now?" || a.Status != view.NeedsMe {
			t.Fatalf("author item = %+v", a)
		}
		if n, err := f.svc.Reconcile(ctx); err != nil || n != 0 {
			t.Fatalf("reconcile = %d, %v", n, err)
		}
	})
}

func TestAuthorCardNeedsMeThenInProgress(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		sink := testsink.New("test")
		f := newFixture(t, views.Sink{ViewSink: sink, Placements: sink})
		f.run(t)
		author := view.Key{Unit: r7, Role: view.AuthorRole}
		address := turn.Turn{Seq: 1, Intent: turn.Intent(dintent.Address), ID: "a1", State: turn.Asked, Session: "term_1", SentAt: t0}
		f.books.Put(turn.Book{Unit: r7, Turns: []turn.Turn{address}, Records: []turn.Record{rec(1, 1, record.Asked, "rebase now?")}})

		f.svc.Changed(ctx, r7)
		eventually(t, "needs-me", func() bool { a, _ := f.appliedAs(t, "test", author); return a.Status == view.NeedsMe })
		if c, m, u := sink.Card(r7); c != string(view.NeedsMe) || m != "asks: rebase now?" || !u {
			t.Fatalf("card = (%q, %q, %v)", c, m, u)
		}

		address.State = turn.Done
		f.books.Put(turn.Book{Unit: r7, Turns: []turn.Turn{address},
			Records: []turn.Record{rec(1, 1, record.Asked, "rebase now?"), rec(2, 1, record.Done, "rebased")}})
		f.svc.Changed(ctx, r7)
		eventually(t, "in progress", func() bool { a, _ := f.appliedAs(t, "test", author); return a.Status == view.InProgress })
		if c, m, u := sink.Card(r7); c != string(view.InProgress) || m != "rebased" || u {
			t.Fatalf("card = (%q, %q, %v)", c, m, u)
		}
		if _, ok := f.applied(t, "test", r7); ok {
			t.Fatal("an address turn wrote a review item")
		}
	})
}

type placements func(ctx context.Context) (map[unit.Unit]string, error)

func (p placements) Placements(ctx context.Context) (map[unit.Unit]string, error) { return p(ctx) }

func TestAPlacementErrorStillComparesApplied(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		sink := testsink.New("test")
		broken := placements(func(context.Context) (map[unit.Unit]string, error) { return nil, errors.New("board is down") })
		f := newFixture(t, views.Sink{ViewSink: sink, Placements: broken})
		f.run(t)
		f.books.Put(asked(r7, "keep both?"))

		n, err := f.svc.Reconcile(ctx)
		if err == nil || n != 1 {
			t.Fatalf("reconcile = %d, %v", n, err)
		}
		eventually(t, "the write", func() bool { _, ok := f.applied(t, "test", r7); return ok })
	})
}

func TestAHangingPlacementReaderDelaysNoOtherSink(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		slow, quick := testsink.New("slow"), testsink.New("quick")
		release := make(chan struct{})
		hanging := placements(func(ctx context.Context) (map[unit.Unit]string, error) {
			<-release
			return nil, nil
		})
		f := newFixture(t, views.Sink{ViewSink: slow, Placements: hanging}, views.Sink{ViewSink: quick, Placements: quick})
		f.run(t)
		f.books.Put(asked(r7, "keep both?"))

		done := make(chan struct{})
		go func() {
			defer close(done)
			f.svc.Reconcile(ctx)
		}()
		eventually(t, "the quick sink's write", func() bool { _, ok := f.applied(t, "quick", r7); return ok })
		select {
		case <-done:
			t.Fatal("reconcile returned before the hanging sink answered")
		default:
		}
		close(release)
		<-done
		eventually(t, "the slow sink's write", func() bool { _, ok := f.applied(t, "slow", r7); return ok })
	})
}

func (p *picky) writes(ref unit.Unit) int {
	n := 0
	for _, b := range p.Batches() {
		for _, it := range b {
			if it.Unit == ref {
				n++
			}
		}
	}
	return n
}

func TestAFailedItemOfAPausedRepositoryDoesNotSpin(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		sink := &picky{Sink: testsink.New("test"), fail: r7}
		f := newFixture(t, views.Sink{ViewSink: sink, Placements: sink})
		f.svc.Backoff = func(int) time.Duration { return 30 * time.Millisecond }
		var mu sync.Mutex
		paused, asks := false, 0
		f.svc.Paused = func(context.Context, unit.Unit) bool {
			mu.Lock()
			defer mu.Unlock()
			asks++
			return paused
		}
		calls := func() int {
			mu.Lock()
			defer mu.Unlock()
			return asks
		}
		f.run(t)
		f.books.Put(asked(r7, "keep both?"))

		f.svc.Changed(ctx, r7)
		eventually(t, "the stored failure", func() bool { return f.stored(views.KindViewSinkFailed, r7) })
		mu.Lock()
		paused = true
		mu.Unlock()
		sink.heal()
		time.Sleep(40 * time.Millisecond)
		synctest.Wait()
		before := calls()
		time.Sleep(time.Minute)
		synctest.Wait()
		if n := calls() - before; n != 0 {
			t.Fatalf("the worker asked about the pause %d times while idle", n)
		}
		if n := sink.writes(r7); n != 0 {
			t.Fatalf("a paused item was written %d times", n)
		}

		mu.Lock()
		paused = false
		mu.Unlock()
		if _, err := f.svc.Reconcile(ctx); err != nil {
			t.Fatal(err)
		}
		eventually(t, "the write after resume", func() bool { _, ok := f.applied(t, "test", r7); return ok })
		if _, err := f.svc.Reconcile(ctx); err != nil {
			t.Fatal(err)
		}
		if n := sink.writes(r7); n != 1 {
			t.Fatalf("the resumed item was written %d times", n)
		}
	})
}

func TestANewerOrForcedItemSkipsAnEarlierBackoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		sink := &picky{Sink: testsink.New("test"), fail: r7}
		f := newFixture(t, views.Sink{ViewSink: sink})
		f.svc.Backoff = func(int) time.Duration { return time.Hour }
		f.run(t)

		f.books.Put(asked(r7, "keep both?"))
		f.svc.Changed(ctx, r7)
		eventually(t, "the stored failure", func() bool { return f.stored(views.KindViewSinkFailed, r7) })
		sink.heal()
		f.books.Put(turn.Book{Unit: r7, Turns: []turn.Turn{reviewTurn(1, turn.Asked)},
			Records: []turn.Record{rec(1, 1, record.Asked, "keep both?"), rec(2, 1, record.Asked, "publish now?")}})
		f.svc.Changed(ctx, r7)
		eventually(t, "the newer item", func() bool { a, _ := f.applied(t, "test", r7); return a.Summary == "asks: publish now?" })

		f.books.Put(asked(r8, "keep both?"))
		sink.mu.Lock()
		sink.fail = r8
		sink.mu.Unlock()
		f.svc.Changed(ctx, r8)
		eventually(t, "the second failure", func() bool { return f.stored(views.KindViewSinkFailed, r8) })
		sink.heal()
		f.svc.RoundStarted(ctx, r8)
		eventually(t, "the forced item", func() bool { _, ok := f.applied(t, "test", r8); return ok })
	})
}

func TestANilIntentsPortPutsNoTurnOnABoard(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		sink := testsink.New("test")
		f := newFixture(t, views.Sink{ViewSink: sink, Placements: sink})
		f.svc.Intents = nil
		f.run(t)
		f.books.Put(asked(r8, "keep both?"))

		f.svc.Changed(ctx, r8)
		if _, err := f.svc.Reconcile(ctx); err != nil {
			t.Fatal(err)
		}
		if got := sink.Batches(); len(got) != 0 {
			t.Fatalf("a write with no intents wired: %+v", got)
		}
	})
}

var thread = unit.Unit{Provider: "thread", Key: "C1 thread 9"}

func TestTheSinkAcceptsAUnitWithoutCard(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		board := newOrcaBoard()
		f := newFixture(t, views.Sink{ViewSink: board, Placements: board})
		f.run(t)
		board.place(r7, "reviewing")

		f.books.Put(asked(thread, "which file?"))
		f.books.Put(asked(r7, "keep both columns?"))
		f.svc.Changed(ctx, thread, r7)
		eventually(t, "the card of r7", func() bool { _, ok := f.applied(t, "orca", r7); return ok })

		if board.writesOf(thread) != 0 {
			t.Errorf("a unit with no card was written %d times", board.writesOf(thread))
		}
		if f.stored(views.KindViewSinkFailed, thread) {
			t.Error("a unit with no card failed the sink")
		}

		sink := testsink.New("test")
		g := newFixture(t, views.Sink{ViewSink: sink, Placements: sink})
		g.run(t)
		g.books.Put(asked(thread, "which file?"))
		g.svc.Changed(ctx, thread)
		eventually(t, "the unit of another provider", func() bool { _, ok := g.applied(t, "test", thread); return ok })
	})
}

func TestTwoUnitsOnOneCardNewestOwns(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		older := view.Item{Unit: r7, Role: view.ReviewRole, Status: view.Working, At: t0}
		newer := view.Item{Unit: thread, Role: view.ReviewRole, Status: view.NeedsMe, At: t0.Add(time.Minute)}
		alone := view.Item{Unit: r8, Role: view.ReviewRole, Status: view.Working, At: t0}
		cards := map[unit.Unit]string{r7: "ws1", thread: "ws1"}
		cardOf := func(u unit.Unit) (string, bool) { c, ok := cards[u]; return c, ok }

		got := views.NewestOwnsTheCard([]view.Item{older, newer, alone}, cardOf)
		if len(got) != 2 || got[0] != newer || got[1] != alone {
			t.Fatalf("items = %+v, want the newer unit's and the one without a card", got)
		}

		tied := views.NewestOwnsTheCard([]view.Item{{Unit: r7, At: t0}, {Unit: thread, At: t0}}, cardOf)
		if len(tied) != 1 || tied[0].Unit != thread {
			t.Fatalf("a tie keeps %+v, want the greater unit", tied)
		}
	})
}

type factsFor struct {
	mu    sync.Mutex
	facts map[unit.Unit]*view.AuthorFacts
	calls []bool
}

func (f *factsFor) Author(_ context.Context, u unit.Unit, needLocal bool) (*view.AuthorFacts, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, needLocal)
	got := f.facts[u]
	if got != nil && needLocal {
		withLocal := *got
		withLocal.Local = &view.LocalFacts{Clean: true, Head: "h1", PRHead: "h1"}
		return &withLocal, nil
	}
	return got, nil
}

func addressBook(ref unit.Unit, state turn.State, summary string) turn.Book {
	t := turn.Turn{Seq: 1, Intent: turn.Intent(dintent.Address), ID: "a1", State: state, Session: "term_1", SentAt: t0}
	return turn.Book{Unit: ref, Turns: []turn.Turn{t}, Records: []turn.Record{rec(1, 1, record.Done, summary)}}
}

func TestTheSinkReceivesAuthorColumns(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		sink := testsink.New("test").WithAuthorColumns()
		f := newFixture(t, views.Sink{ViewSink: sink, Placements: sink})
		facts := &factsFor{facts: map[unit.Unit]*view.AuthorFacts{
			r7: {State: "OPEN", Approved: true, ChecksPass: true},
			r8: {State: "OPEN", ChangesRequestedOnHead: true},
			r9: {State: "MERGED"},
		}}
		f.svc.Facts = facts
		f.run(t)
		for _, u := range []unit.Unit{r7, r8, r9} {
			f.books.Put(addressBook(u, turn.Done, "pushed"))
			sink.Place(u, string(view.InReview))
		}
		f.svc.Changed(ctx, r7, r8, r9)
		want := map[unit.Unit]view.Status{r8: view.InProgress, r9: view.Completed}
		for u, st := range want {
			eventually(t, string(st), func() bool {
				a, _ := f.appliedAs(t, "test", view.Key{Unit: u, Role: view.AuthorRole})
				return a.Status == st
			})
		}
		if c, m, u := sink.Card(r8); c != string(view.InProgress) || m != "changes requested" || !u {
			t.Fatalf("card = (%q, %q, %v)", c, m, u)
		}
		eventually(t, "ready", func() bool {
			a, _ := f.appliedAs(t, "test", view.Key{Unit: r7, Role: view.AuthorRole})
			return a.Status == view.Ready
		})
		if c, m, _ := sink.Card(r7); c != string(view.Ready) || m != "approved and checks pass" {
			t.Fatalf("card = (%q, %q)", c, m)
		}
		if !slices.Contains(facts.calls, true) {
			t.Fatal("the checkout was never asked for")
		}
	})
}

func TestAMergedReviewProjectsCompleted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		sink := testsink.New("test").WithAuthorColumns()
		f := newFixture(t, views.Sink{ViewSink: sink, Placements: sink})
		f.svc.Facts = &factsFor{facts: map[unit.Unit]*view.AuthorFacts{r7: {State: "MERGED"}}}
		f.run(t)
		f.books.Put(asked(r7, "keep both?"))
		f.svc.Changed(ctx, r7)
		eventually(t, "completed", func() bool { a, _ := f.applied(t, "test", r7); return a.Status == view.Completed })
		if _, _, u := sink.Card(r7); u {
			t.Fatal("a merged review is still unread")
		}
	})
}

func TestTheProjectionHonorsPause(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		sink := testsink.New("test")
		f := newFixture(t, views.Sink{ViewSink: sink, Placements: sink})
		f.svc.Facts = &factsFor{facts: map[unit.Unit]*view.AuthorFacts{r7: {State: "MERGED"}}}
		f.svc.Paused = func(_ context.Context, u unit.Unit) bool { return u == r7 }
		f.run(t)
		f.books.Put(addressBook(r7, turn.Done, "pushed"))
		f.svc.Changed(ctx, r7)
		synctest.Wait()
		if n := len(sink.Batches()); n != 0 {
			t.Fatalf("a paused repository got %d batches", n)
		}
	})
}

func TestReconcileWithoutFactsFollowsTheAppliedStatus(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		sink := testsink.New("test")
		f := newFixture(t, views.Sink{ViewSink: sink, Placements: sink})
		f.run(t)
		author := view.Key{Unit: r7, Role: view.AuthorRole}
		address := turn.Turn{Seq: 1, Intent: turn.Intent(dintent.Address), ID: "a1", State: turn.Asked, Session: "term_1", SentAt: t0}
		f.books.Put(turn.Book{Unit: r7, Turns: []turn.Turn{address}, Records: []turn.Record{rec(1, 1, record.Asked, "rebase now?")}})
		f.svc.Changed(ctx, r7)
		eventually(t, "needs-me", func() bool { a, _ := f.appliedAs(t, "test", author); return a.Status == view.NeedsMe })

		sink.Place(r7, "parked")
		address.State = turn.Done
		f.books.Put(turn.Book{Unit: r7, Turns: []turn.Turn{address},
			Records: []turn.Record{rec(1, 1, record.Asked, "rebase now?"), rec(2, 1, record.Done, "rebased")}})
		if n, err := f.svc.Reconcile(ctx); err != nil || n != 1 {
			t.Fatalf("reconcile = %d, %v, want the in-progress item queued", n, err)
		}
	})
}

func TestWithoutFactsTheAuthorRoleKeepsItsColumns(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		sink := testsink.New("test")
		f := newFixture(t, views.Sink{ViewSink: sink, Placements: sink})
		f.run(t)
		address := turn.Turn{Seq: 1, Intent: turn.Intent(dintent.Address), ID: "a1", State: turn.Asked, Session: "term_1", SentAt: t0}
		f.books.Put(turn.Book{Unit: r7, Turns: []turn.Turn{address}, Records: []turn.Record{rec(1, 1, record.Asked, "rebase now?")}})
		sink.Place(r7, string(view.Completed))
		f.svc.Changed(ctx, r7)
		eventually(t, "override", func() bool {
			a, _ := f.appliedAs(t, "test", view.Key{Unit: r7, Role: view.AuthorRole})
			return a.Override == string(view.Completed)
		})
		if c, _, _ := sink.Card(r7); c != string(view.Completed) {
			t.Fatalf("the card moved to %q", c)
		}
	})
}

type countingBoard struct {
	*orcaBoard
	applies atomic.Int32
}

func (b *countingBoard) Apply(ctx context.Context, items []view.Item, force map[unit.Unit]bool) ([]view.Applied, error) {
	b.applies.Add(1)
	return b.orcaBoard.Apply(ctx, items, force)
}

func TestFlushCoalescesForceAndChange(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		board := &countingBoard{orcaBoard: newOrcaBoard()}
		f := newFixture(t, views.Sink{ViewSink: board, Placements: board})
		f.svc.Debounce = 200 * time.Millisecond
		f.run(t)
		board.place(r7, "reviewing")
		f.books.Put(turn.Book{Unit: r7, Turns: []turn.Turn{reviewTurn(1, turn.Sent)}})

		f.svc.RoundStarted(ctx, r7)
		time.Sleep(f.svc.Debounce / 10)
		f.svc.Changed(ctx, r7)
		time.Sleep(10 * f.svc.Debounce)
		synctest.Wait()
		if _, ok := f.applied(t, "orca", r7); !ok {
			t.Fatal("no flush")
		}
		if n := board.applies.Load(); n != 1 {
			t.Errorf("%d flushes for a round start and a change within the debounce, want 1", n)
		}
		if n := board.writesOf(r7); n != 1 {
			t.Errorf("%d writes of the item, want 1", n)
		}
	})
}

func TestAnAuthorCardWithoutATurnMovesOnItsFacts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		sink := testsink.New("test").WithAuthorColumns()
		f := newFixture(t, views.Sink{ViewSink: sink, Placements: sink})
		facts := &factsFor{facts: map[unit.Unit]*view.AuthorFacts{
			r7: {State: "OPEN", Approved: true, ChecksPass: true, AuthorWorkspace: true},
			r8: {State: "OPEN", ChangesRequestedOnHead: true, AuthorWorkspace: true},
			r9: {State: "OPEN", ChangesRequestedOnHead: true},
		}}
		f.svc.Facts = facts
		f.run(t)
		for _, u := range []unit.Unit{r7, r8, r9} {
			f.books.Put(turn.Book{Unit: u})
			sink.Place(u, string(view.InReview))
		}
		f.svc.Changed(ctx, r7, r8, r9)
		for u, st := range map[unit.Unit]view.Status{r7: view.Ready, r8: view.InProgress} {
			eventually(t, string(st), func() bool {
				a, _ := f.appliedAs(t, "test", view.Key{Unit: u, Role: view.AuthorRole})
				return a.Status == st
			})
		}
		if c, _, _ := sink.Card(r9); c != string(view.InReview) {
			t.Fatalf("a card with no author workspace moved to %q", c)
		}
	})
}

func TestAFinishedReviewCardCompletesWithoutATurn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		sink := testsink.New("test").WithAuthorColumns()
		f := newFixture(t, views.Sink{ViewSink: sink, Placements: sink})
		f.svc.Facts = &factsFor{facts: map[unit.Unit]*view.AuthorFacts{
			r7: {State: "MERGED", ReviewWorkspace: true},
			r8: {State: "OPEN", ReviewWorkspace: true},
		}}
		f.run(t)
		f.books.Put(turn.Book{Unit: r7})
		f.books.Put(turn.Book{Unit: r8})
		f.svc.Changed(ctx, r7, r8)
		eventually(t, "completed", func() bool {
			a, _ := f.appliedAs(t, "test", view.Key{Unit: r7, Role: view.ReviewRole})
			return a.Status == view.Completed
		})
		if _, ok := f.appliedAs(t, "test", view.Key{Unit: r8, Role: view.ReviewRole}); ok {
			t.Fatal("an open review without a turn got a card")
		}
	})
}
