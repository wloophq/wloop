package merge_test

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/merge"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

func TestARecordKeepsTheTaskItWentIntoUnderIntoField(t *testing.T) {
	data, err := json.Marshal(merge.Record{From: "t-a", Into: "t-b", At: t0})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil || fields[merge.IntoField] != "t-b" {
		t.Fatalf("%s: %v", data, err)
	}
	r, err := merge.DecodeRecord(string(data))
	if err != nil || r.From != "t-a" || r.Into != "t-b" || !r.At.Equal(t0) {
		t.Fatalf("decoded %+v, %v", r, err)
	}
}

func recordEvent(t *testing.T, from, into task.ID, at time.Time) workflow.Event {
	t.Helper()
	data, err := json.Marshal(merge.Record{From: from, Into: into, At: at})
	if err != nil {
		t.Fatal(err)
	}
	return workflow.Event{Unit: task.UnitOf(from), Kind: merge.RecordKind, Data: string(data), At: at}
}

func TestASplitOfAChainTakesApartWhatItHeldFromEveryTaskThatHeldIt(t *testing.T) {
	splits, unrelates := []string{"split"}, []string{"unrelate"}
	kinds := merge.UndoneKinds(splits, unrelates)
	split, unrelate := task.VerbEventKind("split"), task.VerbEventKind("unrelate")
	if !reflect.DeepEqual(kinds, []string{split, unrelate, merge.RecordKind}) {
		t.Fatalf("kinds %v", kinds)
	}
	rejected := []merge.Undone{{A: "t-x", B: "t-y", At: t0}}
	evs := []workflow.Event{
		recordEvent(t, "t-c", "t-a", t0.Add(time.Minute)),
		recordEvent(t, "t-a", "t-b", t0.Add(2*time.Minute)),
		recordEvent(t, "t-b", "t-z", t0.Add(3*time.Minute)),
		{Unit: task.UnitOf("t-a"), Kind: split, At: now},
		{Unit: task.UnitOf("t-p"), Kind: unrelate, Key: task.PairKey("t-q", now), At: now},
		{Unit: task.UnitOf("t-p"), Kind: unrelate, Key: "not a pair", At: now},
	}
	want := []merge.Undone{{A: "t-x", B: "t-y", At: t0},
		{A: "t-a", B: "t-b", At: now}, {A: "t-a", B: "t-z", At: now},
		{A: "t-c", B: "t-b", At: now}, {A: "t-c", B: "t-z", At: now},
		{A: "t-p", B: "t-q", At: now}}
	if got := merge.UndonePairs(rejected, evs, splits, unrelates); !reflect.DeepEqual(got, want) {
		t.Fatalf("undone\n got %v\nwant %v", got, want)
	}
}

func TestNoSplitVerbReadsNoMergeRecord(t *testing.T) {
	if got := merge.UndoneKinds(nil, []string{"unrelate"}); !reflect.DeepEqual(got, []string{task.VerbEventKind("unrelate")}) {
		t.Fatalf("kinds %v", got)
	}
	if got := merge.UndoneKinds(nil, nil); len(got) != 0 {
		t.Fatalf("kinds %v", got)
	}
}
