package catalog

import (
	"testing"

	"github.com/wloophq/wloop/internal/domain/task"
	intentload "github.com/wloophq/wloop/internal/intent"
)

func TestTaskFactsFillEveryFactABy(t *testing.T) {
	f := TaskFacts(task.Task{ID: "t-1", Kind: task.KindCapture, State: "next", Category: task.CategoryReady}, intentload.TasksDef{})
	for _, name := range intentload.ByFacts {
		if _, ok := f[name]; !ok {
			t.Errorf("TaskFacts leaves out %s, which a verb's by may read", name)
		}
	}
}
