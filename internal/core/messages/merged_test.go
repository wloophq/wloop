package messages_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/messages"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/turn"
)

// aliased reads a merged task's id as its survivor, as the store's alias does.
type aliased struct {
	messages.Tasks
	into map[task.ID]task.ID
}

func (a aliased) Task(ctx context.Context, id task.ID) (task.Task, error) {
	if to, ok := a.into[id]; ok {
		id = to
	}
	return a.Tasks.Task(ctx, id)
}

// mergeRig is a chat rig with task merged folded into task survivor: the merged id reads as the survivor, and
// the log lists merged among the tasks merged into survivor.
func mergeRig(t *testing.T) (r *rig, log *taskLog, survivor, merged task.Task) {
	t.Helper()
	r, log = chatRig(t)
	survivor = r.capture(t, "Ship the export", nil)
	merged = r.capture(t, "Export the ledger", nil)
	return r, log, survivor, merged
}

func merge(r *rig, log *taskLog, survivor, merged task.Task) {
	r.svc.Tasks = aliased{Tasks: r.tasks, into: map[task.ID]task.ID{merged.ID: survivor.ID}}
	log.mergeInto(survivor.ID, merged.ID)
	r.svc.ChatDeps.Merged = log
}

func split(r *rig, log *taskLog, survivor task.Task) {
	r.svc.Tasks = r.tasks
	log.mergeInto(survivor.ID)
}

func chatTexts(read messages.TaskRead) []string {
	var out []string
	for _, ev := range read.Chat {
		out = append(out, ev.Data)
	}
	return out
}

func TestTheSurvivorsChatIncludesTheMergedTasks(t *testing.T) {
	r, log, survivor, merged := mergeRig(t)
	ctx := context.Background()
	if _, err := r.svc.Chat(ctx, messages.ChatRequest{Task: merged.ID, Text: "Start with March."}); err != nil {
		t.Fatal(err)
	}
	merge(r, log, survivor, merged)

	got, err := r.svc.Chat(ctx, messages.ChatRequest{Task: survivor.ID, Text: "Then ship it."})
	if err != nil || got.Route != messages.ChatStarted {
		t.Fatalf("chat went %+v %v", got, err)
	}
	launched := r.launch.launches
	if len(launched) != 2 || launched[1].Unit != task.UnitOf(survivor.ID) || launched[1].Guidance != "Start with March.\n\nThen ship it." {
		t.Fatalf("the survivor's chat turn %+v, want the merged task's unread message first", launched)
	}
}

func TestAMergedTasksLiveSessionStillReadsItsTask(t *testing.T) {
	r, log, survivor, merged := mergeRig(t)
	ctx := context.Background()
	own := task.UnitOf(merged.ID)
	r.books.Put(turn.Book{Unit: own, Turns: []turn.Turn{{Seq: 1, Intent: "chat", ID: "c1", Session: "term_1", SentTo: "term_1", State: turn.Started, SentAt: t0}}})
	r.reg.Put(turn.Session{ID: "term_1", WorkspaceID: "ws", Unit: own, StartedAt: t0})
	merge(r, log, survivor, merged)

	got, err := r.svc.Chat(ctx, messages.ChatRequest{Task: survivor.ID, Text: "Ship it Friday."})
	if err != nil || got.Route != messages.ChatTyped || got.Session != "term_1" {
		t.Fatalf("the survivor's chat went %+v %v, want the merged task's live session typed", got, err)
	}
	read, err := r.svc.ReadTask(ctx, messages.ReadRequest{Session: "term_1"})
	if err != nil {
		t.Fatalf("the merged task's session reads its task: %v", err)
	}
	if read.Task.ID != survivor.ID || !slices.Equal(chatTexts(read), []string{`{"text":"Ship it Friday."}`}) {
		t.Fatalf("the merged task's session read %+v", read)
	}
}

func TestSplitTakesTheMergedTasksHistoryOffTheSurvivor(t *testing.T) {
	r, log, survivor, merged := mergeRig(t)
	ctx := context.Background()
	if _, err := r.svc.Chat(ctx, messages.ChatRequest{Task: merged.ID, Text: "Start with March."}); err != nil {
		t.Fatal(err)
	}
	own := task.UnitOf(merged.ID)
	r.books.Put(turn.Book{Unit: own, Turns: []turn.Turn{{Seq: 1, Intent: "chat", ID: "c1", Session: "term_1", SentTo: "term_1", State: turn.Done, SentAt: t0.Add(-time.Hour)}}})
	merge(r, log, survivor, merged)
	split(r, log, survivor)

	if _, err := r.svc.Chat(ctx, messages.ChatRequest{Task: survivor.ID, Text: "Then ship it."}); err != nil {
		t.Fatal(err)
	}
	launched := r.launch.launches
	if last := launched[len(launched)-1]; last.Unit != task.UnitOf(survivor.ID) || last.Guidance != "Then ship it." {
		t.Fatalf("the survivor's chat turn after the split %+v, want none of the split task's messages", last)
	}
	r.reg.Put(turn.Session{ID: "term_1", WorkspaceID: "ws", Unit: own, StartedAt: t0})
	r.books.Put(turn.Book{Unit: own, Turns: []turn.Turn{{Seq: 1, Intent: "chat", ID: "c1", Session: "term_1", SentTo: "term_1", State: turn.Started, SentAt: t0}}})
	read, err := r.svc.ReadTask(ctx, messages.ReadRequest{Session: "term_1"})
	if err != nil || read.Task.ID != merged.ID {
		t.Fatalf("the split task's session reads %+v %v, want its own task again", read.Task, err)
	}
}
