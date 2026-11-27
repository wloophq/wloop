package tasks

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

func TestAPassLoadsOnceWhatEveryTaskReads(t *testing.T) {
	p := &Pass{}
	ctx := WithPass(context.Background(), p)
	var loads int
	var mu sync.Mutex
	read := func() (map[string]int, error) {
		mu.Lock()
		defer mu.Unlock()
		loads++
		return map[string]int{"open": loads}, nil
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := Once(ctx, "sessions", read)
			if err != nil || got["open"] != 1 {
				t.Errorf("got %v, %v", got, err)
			}
		}()
	}
	wg.Wait()
	if loads != 1 {
		t.Fatalf("loaded %d times", loads)
	}
	if _, err := Once(ctx, "gates", func() (int, error) { return 0, errors.New("down") }); err == nil {
		t.Fatal("the error was lost")
	}
	if got, err := Once(ctx, "gates", func() (int, error) { return 3, nil }); err != nil || got != 3 {
		t.Fatalf("after a failed load the next reader got %v, %v; want a fresh load", got, err)
	}
	if got, err := Once(context.Background(), "sessions", read); err != nil || got["open"] != 2 {
		t.Fatalf("outside a pass: %v, %v", got, err)
	}
}

func TestALoadCancelledWithItsCallerIsLoadedAgainByTheNext(t *testing.T) {
	p := &Pass{}
	gone, cancel := context.WithCancel(WithPass(context.Background(), p))
	cancel()
	if _, err := Once(gone, "items", func() (int, error) { return 0, gone.Err() }); !errors.Is(err, context.Canceled) {
		t.Fatalf("the cancelled caller got %v", err)
	}
	got, err := Once(WithPass(context.Background(), p), "items", func() (int, error) { return 7, nil })
	if err != nil || got != 7 {
		t.Fatalf("the next caller got %v, %v; want a fresh load", got, err)
	}
}

func TestACallerWaitingOnAnotherCallersCancelledLoadLoadsItself(t *testing.T) {
	ctx := WithPass(context.Background(), &Pass{})
	loads := 0
	got, err := Once(ctx, "items", func() (int, error) {
		loads++
		if loads == 1 {
			return 0, context.Canceled
		}
		return 7, nil
	})
	if err != nil || got != 7 || loads != 2 {
		t.Fatalf("got %v, %v after %d loads; want 7 after 2", got, err, loads)
	}
}

func TestAPassUnitsAreEveryTasksOwnAndItemsUnitsOnce(t *testing.T) {
	pr := unit.Unit{Provider: "github", Key: "acme/api#1"}
	thread := unit.Unit{Provider: "slack", Key: "T1/C1/1.2"}
	p := &Pass{Tasks: []task.Task{
		{ID: "t-1", Units: []unit.Unit{pr}, Items: []task.ItemRef{{Unit: pr, Item: "i1"}, {Unit: thread, Item: "i2"}}},
		{ID: "t-2", Units: []unit.Unit{thread}},
		{ID: "t-3"},
	}}
	got := p.Units()
	if len(got) != 2 || got[0] != pr || got[1] != thread {
		t.Fatalf("units %v", got)
	}
}

func TestATasksFactsMayReadOncePerPassWhileTheyAreRead(t *testing.T) {
	p := &Pass{}
	ctx := WithPass(context.Background(), p)
	got, err := p.Facts("t-1", func() (workflow.Facts, error) {
		n, err := Once(ctx, "sessions", func() (int, error) { return 3, nil })
		return workflow.Facts{"runs": n}, err
	})
	if err != nil || got["runs"] != 3 {
		t.Fatalf("%v, %v", got, err)
	}
}

func TestAPageLoadsEachKeyOnceAndNothingOutsideItIsKept(t *testing.T) {
	var loads int
	read := func() (int, error) { loads++; return loads, nil }
	ctx := WithPage(context.Background())
	for range 3 {
		if got, err := PageOnce(ctx, "book", read); got != 1 || err != nil {
			t.Fatalf("PageOnce = %d, %v, want the first load", got, err)
		}
	}
	if got, _ := PageOnce(WithPage(context.Background()), "book", read); got != 2 {
		t.Errorf("another page's PageOnce = %d, want a load of its own", got)
	}
	if got, _ := PageOnce(context.Background(), "book", read); got != 3 {
		t.Errorf("PageOnce outside a page = %d, want a load each call", got)
	}
}

func TestAPageReadsTasksFromItsPass(t *testing.T) {
	u, v := unit.Unit{Provider: "acme", Key: "1"}, unit.Unit{Provider: "acme", Key: "2"}
	w := unit.Unit{Provider: "acme", Key: "3"}
	pass := &Pass{Tasks: []task.Task{{ID: "t-1", Units: []unit.Unit{u}}, {ID: "t-2", Items: []task.ItemRef{{Unit: u, Item: "a"}, {Unit: v, Item: "b"}},
		Resolved: []task.ItemRef{{Unit: w, Item: "c"}}}}}
	ctx := WithPage(WithPass(context.Background(), pass))
	if e, ok := PageTask(ctx, "t-2"); !ok || e.ID != "t-2" {
		t.Errorf("PageTask = %+v, %t", e, ok)
	}
	if _, ok := PageTask(ctx, "t-9"); ok {
		t.Error("PageTask held a task the pass does not")
	}
	if es, ok := PageTasksOf(ctx, u); !ok || len(es) != 2 {
		t.Errorf("PageTasksOf(u) = %+v, %t, want both tasks", es, ok)
	}
	if es, _ := PageTasksOf(ctx, v); len(es) != 1 || es[0].ID != "t-2" {
		t.Errorf("PageTasksOf(v) = %+v, want t-2", es)
	}
	if es, _ := PageTasksOf(ctx, w); len(es) != 1 || es[0].ID != "t-2" {
		t.Errorf("PageTasksOf(w) = %+v, want t-2, which resolved an item of it", es)
	}
	if _, ok := PageTask(WithPass(context.Background(), pass), "t-1"); ok {
		t.Error("PageTask read a pass outside a page")
	}
	if _, ok := PageTasksOf(WithPage(context.Background()), u); ok {
		t.Error("PageTasksOf read a page without a pass")
	}
}

func TestAPageLooksUpSomeKeysInOneLoadAndReadsAMissAlone(t *testing.T) {
	var loads, ones int
	all := func() (map[int]string, error) { loads++; return map[int]string{1: "one", 2: "two"}, nil }
	one := func(k int) func() (string, bool, error) {
		return func() (string, bool, error) { ones++; return "alone", k != 9, nil }
	}
	page := WithPage(context.Background())
	for _, k := range []int{1, 2, 1} {
		if got, ok, err := PageLookupSome(page, "words", k, all, one(k)); err != nil || !ok || got == "alone" {
			t.Fatalf("key %d in a page: %q, %v, %v", k, got, ok, err)
		}
	}
	if got, ok, err := PageLookupSome(page, "words", 3, all, one(3)); err != nil || !ok || got != "alone" {
		t.Fatalf("a key the load misses: %q, %v, %v; want it read alone", got, ok, err)
	}
	if got, ok, _ := PageLookupSome(page, "words", 9, all, one(9)); ok || got != "alone" {
		t.Fatalf("a key nothing holds: %q, %v", got, ok)
	}
	if loads != 1 || ones != 2 {
		t.Fatalf("%d loads and %d single reads in a page, want 1 and 2", loads, ones)
	}
	if got, _, _ := PageLookupSome(context.Background(), "words", 1, all, one(1)); got != "alone" || loads != 1 {
		t.Fatalf("outside a page: %q after %d loads; want it read alone", got, loads)
	}
	if got, _, _ := PageLookupSome(page, "other", 1, nil, one(1)); got != "alone" {
		t.Fatalf("with no load: %q; want it read alone", got)
	}
}

func TestAPageLooksUpEveryKeyInOneLoad(t *testing.T) {
	var loads, ones int
	all := func() (map[int]string, error) { loads++; return map[int]string{1: "one"}, nil }
	one := func() (string, bool, error) { ones++; return "alone", true, nil }
	page := WithPage(context.Background())
	for _, k := range []int{1, 2, 1, 2} {
		got, ok, err := PageLookup(page, "words", k, all, one)
		if err != nil || ok != (k == 1) || ok && got != "one" {
			t.Fatalf("key %d in a page: %q, %v, %v", k, got, ok, err)
		}
	}
	if loads != 1 || ones != 0 {
		t.Fatalf("%d loads and %d single reads in a page, want 1 and 0", loads, ones)
	}
	if got, _, _ := PageLookup(context.Background(), "words", 1, all, one); got != "alone" {
		t.Fatalf("outside a page: %q; want it read alone", got)
	}
}
