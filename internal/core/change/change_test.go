package change

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func gh(n int) unit.Unit {
	return unit.Unit{Provider: unit.GitHub, Key: fmt.Sprintf("o/r#%d", n)}
}

func TestNoteAdvancesByOne(t *testing.T) {
	l := NewLog(8, nil)
	if l.Gen() != 0 {
		t.Fatalf("Gen = %d, want 0", l.Gen())
	}
	for want := port.Generation(1); want <= 20; want++ {
		c := l.Note(port.TopicPRs, gh(1))
		if c.Gen != want || l.Gen() != want {
			t.Fatalf("note %d: Change.Gen = %d, Gen() = %d", want, c.Gen, l.Gen())
		}
		if c.Epoch != l.Epoch() || len(c.Epoch) != 16 {
			t.Fatalf("epoch %q, log epoch %q", c.Epoch, l.Epoch())
		}
	}
	if NewLog(8, nil).Epoch() == l.Epoch() {
		t.Fatal("two logs share an epoch")
	}
}

func TestSinceMergesAndSortsUnits(t *testing.T) {
	l := NewLog(DefaultWindow, nil)
	l.Note(port.TopicPRs, gh(3), gh(1), gh(3))
	l.Note(port.TopicTurns, gh(2), gh(1))
	l.Note(port.TopicPRs, unit.Unit{})
	got := l.Since(0, port.RowInputs)
	want := port.Change{
		Gen: 3, Epoch: l.Epoch(),
		Topics: port.TopicPRs | port.TopicTurns,
		Units:  []unit.Unit{gh(1), gh(2), gh(3)},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Since = %+v, want %+v", got, want)
	}
	if got := l.Since(1, port.RowInputs); !reflect.DeepEqual(got.Units, []unit.Unit{gh(1), gh(2)}) {
		t.Fatalf("Since(1).Units = %v", got.Units)
	}
	if got := l.Since(3, port.RowInputs); !got.Empty() || got.Gen != 3 || got.Units != nil {
		t.Fatalf("Since(Gen) = %+v, want empty at 3", got)
	}
}

func TestSinceFiltersByTopic(t *testing.T) {
	l := NewLog(DefaultWindow, nil)
	l.Note(port.TopicLoopEvents, gh(1))
	l.Note(port.TopicRounds, gh(2))
	l.Note(port.TopicSourceEvents|port.TopicLoopEvents, gh(3))
	l.NoteAll(port.TopicMeta)

	got := l.Since(0, port.SweepInputs)
	if got.Topics != port.TopicSourceEvents || got.All || !reflect.DeepEqual(got.Units, []unit.Unit{gh(3)}) {
		t.Fatalf("Since(SweepInputs) = %+v", got)
	}
	if got := l.Since(0, port.TopicViews); !got.Empty() || got.Gen != 4 {
		t.Fatalf("Since(Views) = %+v, want empty at 4", got)
	}
	if got := l.Since(0, port.HealthInputs); !got.All || got.Topics != port.TopicMeta || got.Units != nil {
		t.Fatalf("Since(HealthInputs) = %+v, want All on Meta", got)
	}
	if got := l.Since(0, port.RowInputs); got.All || got.Has(port.TopicMeta) || !got.Has(port.TopicLoopEvents) {
		t.Fatalf("Since(RowInputs) = %+v", got)
	}
}

func TestSinceOlderThanWindowIsAll(t *testing.T) {
	l := NewLog(4, nil)
	for i := 1; i <= 10; i++ {
		l.Note(port.TopicPRs, gh(i))
	}
	got := l.Since(6, port.RowInputs)
	if got.All || !reflect.DeepEqual(got.Units, sorted(gh(7), gh(8), gh(9), gh(10))) {
		t.Fatalf("Since(6) at the window's edge = %+v, want covered", got)
	}
	got = l.Since(5, port.RowInputs)
	if !got.All || got.Topics != port.RowInputs || got.Units != nil || got.Gen != 10 {
		t.Fatalf("Since(5) outside the window = %+v, want All", got)
	}
	got = l.Since(11, port.TopicPRs)
	if !got.All || got.Topics != port.TopicPRs {
		t.Fatalf("Since ahead of Gen = %+v, want All", got)
	}
}

func sorted(us ...unit.Unit) []unit.Unit { return normalize(us) }

func TestNoteWithoutUnitsIsUnitless(t *testing.T) {
	l := NewLog(DefaultWindow, nil)
	c := l.Note(port.TopicPause)
	if c.All || c.Units != nil || c.Topics != port.TopicPause {
		t.Fatalf("Note without units = %+v, want a unit-less change", c)
	}
	l.Note(port.TopicPRs, gh(1))
	if got := l.Since(0, port.RowInputs); got.All || !reflect.DeepEqual(got.Units, []unit.Unit{gh(1)}) {
		t.Fatalf("Since = %+v, want only the noted unit", got)
	}
	c = l.NoteAll(port.TopicPRs)
	if !c.All || c.Units != nil {
		t.Fatalf("NoteAll = %+v, want All", c)
	}
	if got := l.Since(0, port.RowInputs); !got.All || got.Units != nil {
		t.Fatalf("Since across NoteAll = %+v, want All", got)
	}
}

func TestAChangeCarriesUnitsOfSeveralProviders(t *testing.T) {
	l := NewLog(DefaultWindow, nil)
	thread := unit.Unit{Provider: "thread", Key: "C01/1700000000.0001"}
	session := unit.Unit{Provider: unit.Session, Key: "term-1"}
	c := l.Note(port.TopicLoopEvents, thread, gh(7), session)
	want := []unit.Unit{gh(7), session, thread}
	if !reflect.DeepEqual(c.Units, want) {
		t.Fatalf("Note.Units = %v, want %v", c.Units, want)
	}
	l.Note(port.TopicTurns, unit.Unit{Provider: unit.Workspace, Key: "wt-9"}, thread)
	got := l.Since(0, port.RowInputs)
	want = []unit.Unit{gh(7), session, thread, {Provider: unit.Workspace, Key: "wt-9"}}
	if !reflect.DeepEqual(got.Units, want) {
		t.Fatalf("Since.Units = %v, want %v", got.Units, want)
	}
}

func TestSubscribeCoalescesABurst(t *testing.T) {
	l := NewLog(DefaultWindow, nil)
	poke, cancel := Poke(l, port.TopicPRs)
	defer cancel()
	for i := 0; i < 1000; i++ {
		l.Note(port.TopicPRs, gh(i))
	}
	if n := len(poke); n != 1 {
		t.Fatalf("pending pokes = %d, want 1", n)
	}
	<-poke
	l.Note(port.TopicMeta)
	if n := len(poke); n != 0 {
		t.Fatalf("a note outside the filter poked: %d pending", n)
	}
	cancel()
	l.Note(port.TopicPRs)
	if n := len(poke); n != 0 {
		t.Fatalf("a cancelled poke fired: %d pending", n)
	}
}

func TestNoteNeverBlocks(t *testing.T) {
	l := NewLog(DefaultWindow, nil)
	_, cancel := Poke(l, port.RowInputs)
	defer cancel()
	done := make(chan struct{})
	go func() {
		for i := 0; i < 10000; i++ {
			l.Note(port.TopicPRs, gh(i))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Note blocked on a subscriber that never reads")
	}
}

func TestConcurrentNotesAreTotal(t *testing.T) {
	l := NewLog(DefaultWindow, nil)
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 10000; i++ {
				l.Note(port.TopicPRs, gh(w))
				if i%1000 == 0 {
					l.Since(l.Gen()-1, port.RowInputs)
				}
			}
		}()
	}
	wg.Wait()
	if g := l.Gen(); g != 80000 {
		t.Fatalf("Gen = %d, want 80000", g)
	}
}

func TestChangesInOrder(t *testing.T) {
	l := NewLog(DefaultWindow, nil)
	var seen []port.Generation
	cancel := l.Subscribe(func(c port.Change) { seen = append(seen, c.Gen) })
	defer cancel()
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				l.Note(port.TopicTurns, gh(w))
			}
		}()
	}
	wg.Wait()
	if len(seen) != 400 {
		t.Fatalf("subscriber saw %d notes, want 400", len(seen))
	}
	for i, g := range seen {
		if g != port.Generation(i+1) {
			t.Fatalf("note %d has generation %d: not gapless and ordered", i, g)
		}
	}
}

func TestPanickingSubscriberIsDropped(t *testing.T) {
	var logged []string
	l := NewLog(DefaultWindow, func(format string, args ...any) {
		logged = append(logged, fmt.Sprintf(format, args...))
	})
	calls := 0
	l.Subscribe(func(port.Change) {
		calls++
		panic("boom")
	})
	var after []port.Generation
	cancel := l.Subscribe(func(c port.Change) { after = append(after, c.Gen) })
	defer cancel()

	l.Note(port.TopicPRs, gh(1))
	l.Note(port.TopicPRs, gh(2))

	if calls != 1 {
		t.Fatalf("panicking subscriber called %d times, want 1", calls)
	}
	if !reflect.DeepEqual(after, []port.Generation{1, 2}) {
		t.Fatalf("other subscriber saw %v, want [1 2]", after)
	}
	if len(logged) != 1 || !strings.Contains(logged[0], "boom") {
		t.Fatalf("logged %q, want one line naming the panic", logged)
	}
	if l.Gen() != 2 {
		t.Fatalf("Gen = %d, want 2", l.Gen())
	}
}
