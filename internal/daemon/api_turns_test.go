package daemon

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/port"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/core/tasks/taskstest"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/core/turns/turnstest"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

var (
	turnsZone = time.FixedZone("BRT", -3*3600)
	turnsNow  = time.Date(2026, 10, 8, 10, 0, 0, 0, turnsZone)
	turnsUnit = unit.Unit{Provider: unit.Session, Key: "term_live"}
)

func turnsLiveServer(t *testing.T, ts ...turn.Turn) *Server {
	t.Helper()
	books := turnstest.NewStore()
	books.Put(turn.Book{Unit: turnsUnit, Turns: ts})
	return newServerWith(t, func(d *Deps) {
		d.Now = func() time.Time { return turnsNow }
		d.Turns = &turns.Service{Intents: turnstest.Intents(), Store: books}
	})
}

func liveTurnAt(seq int64, intent string, state turn.State, started, ended time.Time) turn.Turn {
	return turn.Turn{Seq: seq, Intent: turn.Intent(intent), State: state, SentAt: started, StartedAt: started, EndedAt: ended}
}

func getTurnsLive(t *testing.T, s *Server) api.LiveTurns {
	t.Helper()
	var got api.LiveTurns
	if code := get(t, s, api.TurnsLivePath, &got); code != 200 {
		t.Fatalf("%s: %d", api.TurnsLivePath, code)
	}
	return got
}

func seqs(ts []api.LiveTurn) []int64 {
	out := []int64{}
	for _, t := range ts {
		out = append(out, t.Seq)
	}
	return out
}

func TestTurnsLiveSplitsRunningDoneAsked(t *testing.T) {
	t.Parallel()
	at := turnsNow.Add(-time.Hour)
	s := turnsLiveServer(t,
		liveTurnAt(1, "review", turn.Started, at, time.Time{}),
		liveTurnAt(2, "review", turn.Sent, at, time.Time{}),
		liveTurnAt(3, "review", turn.Asked, at, time.Time{}),
		liveTurnAt(4, "review", turn.Done, at.Add(-30*time.Minute), at.Add(-20*time.Minute)),
		liveTurnAt(5, "review", turn.Settled, at.Add(-10*time.Minute), at.Add(-5*time.Minute)),
		liveTurnAt(6, "review", turn.Withdrawn, at, at),
	)
	got := getTurnsLive(t, s)
	if want := []int64{1, 2}; !equalSeqs(seqs(got.Running), want) {
		t.Errorf("running %v, want %v", seqs(got.Running), want)
	}
	if want := []int64{5, 4}; !equalSeqs(seqs(got.Done), want) {
		t.Errorf("done %v, want %v (newest first)", seqs(got.Done), want)
	}
	if got.Asked != 1 {
		t.Errorf("asked %d, want 1", got.Asked)
	}
	d := got.Done[1]
	if d.Unit != turnsUnit.String() || d.Intent != "review" || d.DurationMS != (10*time.Minute).Milliseconds() {
		t.Errorf("done turn %+v", d)
	}
}

func equalSeqs(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestTurnsLiveDoneOnlySinceLocalMidnight(t *testing.T) {
	t.Parallel()
	midnight := time.Date(2026, 10, 8, 0, 0, 0, 0, turnsZone)
	s := turnsLiveServer(t,
		liveTurnAt(1, "review", turn.Done, midnight.Add(-20*time.Minute), midnight.Add(-10*time.Minute)),
		liveTurnAt(2, "review", turn.Done, midnight.Add(-5*time.Minute), midnight.Add(time.Minute)),
	)
	if got := seqs(getTurnsLive(t, s).Done); !equalSeqs(got, []int64{2}) {
		t.Fatalf("done %v, want only the turn that ended after local midnight", got)
	}
}

func TestTurnsLiveUsualIsP50ByIntent(t *testing.T) {
	t.Parallel()
	start := turnsNow.Add(-48 * time.Hour)
	end := func(d time.Duration) time.Time { return start.Add(d) }
	old := turnsNow.Add(-20 * 24 * time.Hour)
	s := turnsLiveServer(t,
		liveTurnAt(1, "review", turn.Done, start, end(time.Minute)),
		liveTurnAt(2, "review", turn.Done, start, end(4*time.Minute)),
		liveTurnAt(3, "review", turn.Done, start, end(9*time.Minute)),
		liveTurnAt(4, "review", turn.Done, old, old.Add(time.Hour)),
		liveTurnAt(5, "address", turn.Done, start, end(2*time.Minute)),
	)
	usual := getTurnsLive(t, s).Usual
	if usual["review"] != (4*time.Minute).Milliseconds() || usual["address"] != (2*time.Minute).Milliseconds() || len(usual) != 2 {
		t.Fatalf("usual %v, want review 4m and address 2m, the turn outside 14 days left out", usual)
	}
}

func TestTurnStateChangeSetsChangeTurns(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newFlagRig(t)
		r.log.Note(port.TopicTurns, turnsUnit)
		c, ok := r.published()
		if !ok || !c.Turns {
			t.Fatalf("a turn's state moving published %+v (%v), want Turns", c, ok)
		}
		r.log.Note(port.TopicGates)
		if c, ok := r.published(); !ok || c.Turns {
			t.Fatalf("a gate note published %+v (%v), want no Turns", c, ok)
		}
	})
}

type countingTasks struct {
	*taskstest.Store
	lists int
}

func (c *countingTasks) Tasks(ctx context.Context, q task.Query) ([]task.Task, error) {
	c.lists++
	return c.Store.Tasks(ctx, q)
}

func TestATurnNoteAlonePublishesTurnsWithoutReadingTheTasks(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	store := &countingTasks{Store: taskstest.New()}
	s.deps.Tasks = coretasks.New(coretasks.Deps{Store: store})
	sub := s.hub.subscribe("test")
	t.Cleanup(func() { s.hub.unsubscribe(sub) })
	s.moved.attach()
	s.publishTasks(context.Background(), port.Change{}, port.Change{Topics: port.TopicTurns})
	c, ok := sub.take()
	if !ok || !c.Turns || c.Counters != nil || c.All || len(c.Units) > 0 {
		t.Fatalf("published %+v (%v), want Turns alone", c, ok)
	}
	if store.lists != 0 {
		t.Fatalf("a turn note read the tasks %d times", store.lists)
	}
}

func TestTurnsLiveLeavesOutTheTurnsOfAnIntentThatReportsNothing(t *testing.T) {
	t.Parallel()
	at := turnsNow.Add(-time.Hour)
	var quiet string
	for _, d := range bundledLoaded().Registry.All() {
		if !d.Reports {
			quiet = string(d.Name)
			break
		}
	}
	if quiet == "" {
		t.Fatal("no bundled intent reports nothing")
	}
	s := turnsLiveServer(t,
		liveTurnAt(1, quiet, turn.Started, at, time.Time{}),
		liveTurnAt(2, quiet, turn.Done, at, at.Add(time.Minute)),
		liveTurnAt(3, "review", turn.Done, at, at.Add(time.Minute)),
	)
	got := getTurnsLive(t, s)
	if len(got.Running) != 0 || !equalSeqs(seqs(got.Done), []int64{3}) || len(got.Usual) != 1 {
		t.Fatalf("live %+v, want only the review turn", got)
	}
}

func TestTurnsLiveDismissedCountsTowardUsualButStaysOutOfDone(t *testing.T) {
	t.Parallel()
	at := turnsNow.Add(-time.Hour)
	s := turnsLiveServer(t, liveTurnAt(1, "review", turn.Dismissed, at, at.Add(3*time.Minute)))
	got := getTurnsLive(t, s)
	if len(got.Done) != 0 || got.Usual["review"] != (3*time.Minute).Milliseconds() {
		t.Fatalf("live %+v", got)
	}
}

func TestTurnsLiveUsualCountsTurnsThatEndedInTheWindowWhenTheyWereSentBefore(t *testing.T) {
	t.Parallel()
	sent := turnsNow.Add(-20 * 24 * time.Hour)
	s := turnsLiveServer(t, liveTurnAt(1, "review", turn.Done, sent, turnsNow.Add(-24*time.Hour)))
	if got := getTurnsLive(t, s).Usual["review"]; got != (19 * 24 * time.Hour).Milliseconds() {
		t.Fatalf("usual %d", got)
	}
}

func TestTurnsLiveMidnightOnADaylightSavingDay(t *testing.T) {
	t.Parallel()
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skip(err)
	}
	now := time.Date(2026, 10, 25, 10, 0, 0, 0, berlin)
	midnight := time.Date(2026, 10, 25, 0, 0, 0, 0, berlin)
	books := turnstest.NewStore()
	books.Put(turn.Book{Unit: turnsUnit, Turns: []turn.Turn{
		liveTurnAt(1, "review", turn.Done, midnight.Add(-30*time.Minute), midnight.Add(-10*time.Minute)),
		liveTurnAt(2, "review", turn.Done, midnight.Add(10*time.Minute), midnight.Add(30*time.Minute)),
	}})
	s := newServerWith(t, func(d *Deps) {
		d.Now = func() time.Time { return now }
		d.Turns = &turns.Service{Intents: turnstest.Intents(), Store: books}
	})
	if got := seqs(getTurnsLive(t, s).Done); !equalSeqs(got, []int64{2}) {
		t.Fatalf("done %v, want only the turn after the day's local midnight", got)
	}
}

func TestTurnsLiveLabelsEachTurnWithItsIntentsLabel(t *testing.T) {
	t.Parallel()
	at := turnsNow.Add(-time.Hour)
	s := turnsLiveServer(t, liveTurnAt(1, "review", turn.Started, at, time.Time{}), liveTurnAt(2, "unheard-of", turn.Started, at, time.Time{}))
	def, ok := s.loaded().Registry.Get("review")
	if !ok || def.Label == "" || def.Label == "review" {
		t.Fatalf("the bundled review intent has no label of its own: %+v", def)
	}
	got := getTurnsLive(t, s)
	if len(got.Running) != 2 || got.Running[0].Label != def.Label || got.Running[1].Label != "unheard-of" {
		t.Fatalf("running %+v, want review labelled %q and an intent the registry lacks labelled by its name", got.Running, def.Label)
	}
}
