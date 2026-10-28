package taskstest

import (
	"testing"

	coretasks "github.com/wloophq/wloop/internal/core/tasks"
)

func TestProjectStoreContract(t *testing.T) {
	ProjectsContract(t, func(t *testing.T) (coretasks.Store, coretasks.ProjectStore) {
		s := New()
		return s, &Projects{Entries: s}
	})
}
