package merge

import (
	"encoding/json"
	"slices"
	"time"

	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

// IntoField is the field of a Record's JSON that names the task it went into, which a store reads in place.
const IntoField = "Into"

// DecodeRecord is the Record the data of a RecordKind event keeps.
func DecodeRecord(data string) (Record, error) {
	var r Record
	err := json.Unmarshal([]byte(data), &r)
	return r, err
}

// Undone is tasks A and B the user took apart at At: by rejecting a gate that proposed pairing them, by a split
// of one out of the other, or by unrelating them.
type Undone struct {
	A, B task.ID
	At   time.Time
}

// UndoneKinds are the kinds of the events UndonePairs reads, for the verbs splits and unrelates.
func UndoneKinds(splits, unrelates []string) []string {
	if len(splits) == 0 {
		return task.VerbEventKinds(unrelates)
	}
	return append(task.VerbEventKinds(append(slices.Clone(splits), unrelates...)), RecordKind)
}

// UndonePairs are rejected and the pairs evs took apart, evs being events of UndoneKinds, oldest first, on the
// tasks' own units. An unrelate verb's event keys the other task, as task.PairKey writes it. A split takes
// apart each merge record of a task's unit from the first event of a split verb on that unit after it, which a
// split writes on both tasks' units: the task and those merged, directly or through others, into it, from the
// task it went into and those that one went into in turn.
func UndonePairs(rejected []Undone, evs []workflow.Event, splits, unrelates []string) []Undone {
	splitKinds, unrelateKinds := task.VerbEventKinds(splits), task.VerbEventKinds(unrelates)
	out := slices.Clone(rejected)
	merged := map[task.ID]task.ID{}
	for _, ev := range evs {
		id, ok := task.IDOf(ev.Unit)
		if !ok {
			continue
		}
		switch {
		case ev.Kind == RecordKind:
			if r, err := DecodeRecord(ev.Data); err == nil && r.From == id && r.Into != "" {
				merged[id] = r.Into
			}
		case slices.Contains(unrelateKinds, ev.Kind):
			if other, paired := task.PairOfKey(ev.Key); paired {
				out = append(out, Undone{A: id, B: other, At: ev.At})
			}
		case slices.Contains(splitKinds, ev.Kind):
			into, ok := merged[id]
			if !ok {
				continue
			}
			for _, x := range append([]task.ID{id}, foldedInto(merged, id)...) {
				for _, y := range append([]task.ID{into}, survivorsOf(merged, into)...) {
					out = append(out, Undone{A: x, B: y, At: ev.At})
				}
			}
			delete(merged, id)
		}
	}
	return out
}

// foldedInto are the tasks merged, directly or through others, into id, which a split of id takes apart with it.
func foldedInto(merged map[task.ID]task.ID, id task.ID) []task.ID {
	var out []task.ID
	for next := []task.ID{id}; len(next) > 0; {
		cur := next[0]
		next = next[1:]
		for x, into := range merged {
			if into == cur && x != id && !slices.Contains(out, x) {
				out = append(out, x)
				next = append(next, x)
			}
		}
	}
	slices.Sort(out)
	return out
}

// survivorsOf are the tasks into is itself merged into, nearest first, which held id until a split of it.
func survivorsOf(merged map[task.ID]task.ID, into task.ID) []task.ID {
	var out []task.ID
	for next, ok := merged[into]; ok && !slices.Contains(out, next) && next != into; next, ok = merged[next] {
		out = append(out, next)
	}
	return out
}
