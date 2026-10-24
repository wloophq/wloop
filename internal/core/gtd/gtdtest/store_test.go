package gtdtest_test

import (
	"testing"

	coregtd "github.com/wloophq/wloop/internal/core/gtd"
	"github.com/wloophq/wloop/internal/core/gtd/gtdtest"
)

func TestEntryStoreContract(t *testing.T) {
	gtdtest.Contract(t, func(t *testing.T) coregtd.Store { return gtdtest.New() })
}
