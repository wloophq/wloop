package taskstest_test

import (
	"testing"

	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/core/tasks/taskstest"
)

func TestEntryStoreContract(t *testing.T) {
	taskstest.Contract(t, func(t *testing.T) coretasks.Store { return taskstest.New() })
}
