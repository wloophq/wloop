package cli

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/brief"
	"github.com/wloophq/wloop/internal/core/intake"
	"github.com/wloophq/wloop/internal/core/lock"
	"github.com/wloophq/wloop/internal/daemon"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/source"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

// extractingSource is an on-demand BriefSource whose job asks the brief
// intent's fields itself.
type extractingSource struct {
	brief.GitHubSource
	intent func() brief.Intent
}

func (s *extractingSource) UseIntent(intent func() brief.Intent)          { s.intent = intent }
func (s *extractingSource) Want(context.Context, unit.Unit) (bool, error) { return true, nil }
func (s *extractingSource) Watch(func(unit.Unit))                         {}

func TestAnExtractingSourceGetsItsProvidersBriefIntentWithoutAQueue(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	userProfile(t, te, map[string]string{
		"intents/thread-brief/intent.toml": `label = "Brief"
summary = "A thread's brief"
builds = "brief"
provider = "slack"
prompt = "prompts/brief.md"
schema = "schema.json"
`,
		"intents/thread-brief/schema.json": `{"type":"object","properties":{"urgency":{"type":"string"}}}`,
	})
	src := &extractingSource{}
	te.Env.wireBriefs(&serveWiring{Modules: []source.Module{{ID: "slack", Briefs: src}}, Engine: &engineDeps{}, Log: func(string, ...any) {}})
	if src.intent == nil {
		t.Fatal("no brief intent")
	}
	if got := src.intent().Schema.Missing(brief.Brief{}); !slices.Equal(got, []string{"urgency"}) {
		t.Fatalf("the extraction's fields: %v", got)
	}
}

func TestASlackOnlyDaemonBriefsSlackUnitsOnDemand(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	te.cfg = withoutWindows(te.cfg)
	off := false
	te.cfg.GitHub.Enabled = &off
	te.cfg.Brief.Enabled = true
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), storeFile), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	seedSlack(t, db)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	te.serveDeps(ctx, db, te.cfg, t.Logf, &lock.Mutex{}, &daemon.AppState{})
	steps, ok := te.Env.engine.Steps.(*stepSet)
	if !ok || steps.d.Briefs == nil {
		t.Fatalf("the engine's steps have no briefs: %T", te.Env.engine.Steps)
	}
	rd, err := steps.d.Briefs.Peek(ctx, slackThread2, true, nil)
	if err != nil || rd.Cause == intake.CauseOff {
		t.Fatalf("a Slack first turn: %+v %v", rd, err)
	}
}
