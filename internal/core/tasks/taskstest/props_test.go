package taskstest_test

import (
	"testing"

	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/core/tasks/taskstest"
	"github.com/wloophq/wloop/internal/domain/task"
)

func TestPropStoreContract(t *testing.T) {
	taskstest.PropContract(t, func(t *testing.T) (coretasks.PropStore, func(task.ID)) {
		p := taskstest.NewProps()
		return p, p.Add
	})
}
