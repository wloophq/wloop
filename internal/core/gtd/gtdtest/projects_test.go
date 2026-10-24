package gtdtest

import (
	"testing"

	coregtd "github.com/wloophq/wloop/internal/core/gtd"
)

func TestProjectStoreContract(t *testing.T) {
	ProjectsContract(t, func(t *testing.T) (coregtd.Store, coregtd.ProjectStore) {
		s := New()
		return s, &Projects{Entries: s}
	})
}
