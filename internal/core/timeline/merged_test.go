package timeline_test

import (
	"context"
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/core/timeline"
	"github.com/wloophq/wloop/internal/domain/merge"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

const (
	survivor = task.ID("t-1111111111111111")
	merged   = task.ID("t-2222222222222222")
)

func TestTheSurvivorsTimelineIncludesTheMergedTasks(t *testing.T) {
	thread := unit.Unit{Provider: "slack", Key: "T000/C000/1.2"}
	l := &log{}
	l.add(task.UnitOf(merged), "message")      // 1
	l.add(thread, "message")                   // 2
	l.add(task.UnitOf(survivor), "verb_merge") // 3
	l.add(task.UnitOf(merged), "turn_ended")   // 4
	r := timeline.Merged{Log: l, Holdings: holdings{survivor: {thread, task.UnitOf(merged)}}}

	got, err := r.Of(context.Background(), survivor, timeline.Filter{})
	if err != nil || !slices.Equal(seqs(got), []int64{4, 3, 2, 1}) {
		t.Fatalf("the survivor's timeline %v %v, want the merged task's events in it", seqs(got), err)
	}
	if got[0].Unit != task.UnitOf(merged) {
		t.Fatalf("a merged task's event keeps its unit: %+v", got[0])
	}
}

func TestTheTimelineLeavesTheMergeRecordOut(t *testing.T) {
	l := &log{}
	l.add(task.UnitOf(merged), "message")        // 1
	l.add(task.UnitOf(survivor), "message")      // 2
	l.add(task.UnitOf(merged), merge.RecordKind) // 3
	r := timeline.Merged{Log: l, Holdings: holdings{survivor: {task.UnitOf(merged)}}}
	ctx := context.Background()

	if got, _ := r.Of(ctx, survivor, timeline.Filter{}); !slices.Equal(seqs(got), []int64{2, 1}) {
		t.Fatalf("the timeline %v, want no merge record", seqs(got))
	}
	if page, _ := r.Of(ctx, survivor, timeline.Filter{Limit: 1}); !slices.Equal(seqs(page), []int64{2}) {
		t.Fatalf("the first page %v, want it full without the merge record", seqs(page))
	}
	if asked, _ := r.Of(ctx, merged, timeline.Filter{Kinds: []string{merge.RecordKind}}); len(asked) != 0 {
		t.Fatalf("the merge record asked for by kind %v", seqs(asked))
	}
}

func TestTheTimelineLeavesTheKindsItExceptsOut(t *testing.T) {
	l := &log{}
	l.add(task.UnitOf(survivor), "task_created") // 1
	l.add(task.UnitOf(survivor), "message")      // 2
	l.add(task.UnitOf(survivor), "classified")   // 3
	r := timeline.Merged{Log: l, Holdings: holdings{}, Except: []string{"task_created", "classified"}}
	ctx := context.Background()

	if got, _ := r.Of(ctx, survivor, timeline.Filter{}); !slices.Equal(seqs(got), []int64{2}) {
		t.Fatalf("the timeline %v, want only the message", seqs(got))
	}
	if asked, _ := r.Of(ctx, survivor, timeline.Filter{Kinds: []string{"classified"}}); len(asked) != 0 {
		t.Fatalf("an excepted kind asked for by kind %v", seqs(asked))
	}
}
