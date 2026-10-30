package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/ingest"
	"github.com/wloophq/wloop/internal/core/ingest/ingesttest"
	"github.com/wloophq/wloop/internal/readmodel"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

func complete() pluginModule {
	return pluginModule{
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
		spoil func(*pluginModule)
		want  string
	}{
		{"no provider", func(m *pluginModule) { m.ID = "" }, "needs a provider"},
		{"no codec", func(m *pluginModule) { m.Codec = nil }, "codec of its provider"},
		{"another provider's codec", func(m *pluginModule) { m.ID = "slack" }, "codec of its provider"},
		{"no job", func(m *pluginModule) { m.Ingest.Prepare = nil }, "ingestion job"},
		{"no interval", func(m *pluginModule) { m.Ingest.Every = 0 }, "ingestion job"},
		{"no rows", func(m *pluginModule) { m.Rows = nil }, "its rows"},
	} {
		m := complete()
		c.spoil(&m)
		if err := m.Valid(); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", c.name, err)
		}
	}
}

func TestTheGitHubModuleServesItsUnitEvents(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	m, err := githubHostModule(config.Defaults(), moduleDeps{Inventory: te.Store, Clock: clockFunc(te.Now), Log: t.Logf})
	if err != nil || m.UnitEvents == nil {
		t.Fatalf("module: UnitEvents %v, %v", m.UnitEvents, err)
	}
}
