package turn

import (
	"os"
	"testing"

	"github.com/wloophq/wloop/internal/core/ingest/ingesttest"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

func TestMain(m *testing.M) {
	builtin.Providers = append(builtin.Providers, ingesttest.Provider)
	os.Exit(m.Run())
}
