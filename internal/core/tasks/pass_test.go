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
	if _, err := Once(ctx, "gates", func() (int, error) { t.Fatal("loaded again after an error"); return 0, nil }); err == nil {
		t.Fatal("the error was not kept")
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
