package cli

import (
	"context"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/turn"
)

func TestStartingWorkByHandCommitsTheTask(t *testing.T) {
	t.Parallel()
	r, p := taskWorkflows(t)
	ctx := context.Background()
	capture := func(title string, move func(*task.Task)) task.ID {
		e, err := r.te.tasks.Capture(ctx, title, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		if move != nil {
			e = r.taskOf(e.ID)
			move(&e)
			if err := r.db.Tasks().Apply(ctx, []task.Change{{Op: task.OpUpdate, Task: e}}); err != nil {
				t.Fatal(err)
			}
		}
		return e.ID
	}
	someday := func(e *task.Task) { e.State, e.Category = "someday", task.CategoryDeferred }
	done := func(e *task.Task) { e.State, e.Category = "done", task.CategoryDone }
	started := capture("started", nil)
	asleep := capture("asleep", someday)
	chatted := capture("chatted", nil)
	automated := capture("automated", nil)
	finished := capture("finished", done)
	idle := capture("idle", nil)
	books := sqlite.New(r.db)
	open := func(id task.ID, intent string, trig turn.Trigger, st turn.State, more ...turn.Turn) {
		b := turn.Book{Unit: task.UnitOf(id), Turns: append([]turn.Turn{{Intent: turn.Intent(intent), ID: string(id) + intent, Trigger: trig, State: st, SentAt: r.now}}, more...)}
		if _, err := books.Save(ctx, b); err != nil {
			t.Fatal(err)
		}
	}
	open(started, "start", turn.ByHand, turn.Started)
	open(asleep, "start", turn.ByHand, turn.Sent)
	open(chatted, "chat", turn.ByHand, turn.Started)
	open(automated, "start", turn.Automated, turn.Started)
	open(finished, "start", turn.ByHand, turn.Started)
	open(idle, "start", turn.ByHand, turn.Done)
	if err := p.pass(ctx); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[task.ID]task.State{started: "next", asleep: "next", chatted: "inbox", automated: "inbox", finished: "done", idle: "inbox"} {
		if got := r.taskOf(id).State; got != want {
			t.Errorf("%s: %s, want %s", r.taskOf(id).Title, got, want)
		}
	}

	moved := r.taskOf(started)
	someday(&moved)
	if err := r.db.Tasks().Apply(ctx, []task.Change{{Op: task.OpUpdate, Task: moved}}); err != nil {
		t.Fatal(err)
	}
	if err := p.pass(ctx); err != nil {
		t.Fatal(err)
	}
	if got := r.taskOf(started).State; got != "someday" {
		t.Fatalf("the turn that committed the task moved it back from Someday: %s", got)
	}
	open(started, "start", turn.ByHand, turn.Started, turn.Turn{Intent: "start", ID: "again", Trigger: turn.ByHand, State: turn.Started, SentAt: r.now.Add(time.Minute)})
	if err := p.pass(ctx); err != nil {
		t.Fatal(err)
	}
	if got := r.taskOf(started).State; got != "next" {
		t.Fatalf("a new turn by hand left the task in %s", got)
	}
}
