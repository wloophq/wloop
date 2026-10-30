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
