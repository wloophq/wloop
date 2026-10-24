package source_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/ingest"
	"github.com/wloophq/wloop/internal/core/ingest/ingesttest"
	"github.com/wloophq/wloop/internal/readmodel"
	"github.com/wloophq/wloop/internal/source"
)

func complete() source.Module {
	return source.Module{
		ID:    ingesttest.Provider,
		Codec: ingesttest.Codec(),
		Ingest: ingest.Job{Every: time.Minute, Prepare: func(context.Context, time.Time) (ingest.Plan, string, error) {
			return ingest.Plan{}, "", nil
		}},
		Rows: func(readmodel.RowDeps) readmodel.Provider { return nil },
	}
}

func TestAModuleNeedsItsCodecJobAndRows(t *testing.T) {
	if err := complete().Valid(); err != nil {
		t.Fatalf("a complete module: %v", err)
	}
	for _, c := range []struct {
		name  string
		spoil func(*source.Module)
		want  string
	}{
		{"no provider", func(m *source.Module) { m.ID = "" }, "needs a provider"},
		{"no codec", func(m *source.Module) { m.Codec = nil }, "codec of its provider"},
		{"another provider's codec", func(m *source.Module) { m.ID = "slack" }, "codec of its provider"},
		{"no job", func(m *source.Module) { m.Ingest.Prepare = nil }, "ingestion job"},
		{"no interval", func(m *source.Module) { m.Ingest.Every = 0 }, "ingestion job"},
		{"no rows", func(m *source.Module) { m.Rows = nil }, "its rows"},
	} {
		m := complete()
		c.spoil(&m)
		if err := m.Valid(); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", c.name, err)
		}
	}
}
