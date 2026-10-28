package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/profile"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

func TestAPluginsProfileReadsItsBundledLayerUnderTheUsers(t *testing.T) {
	profile.UsePlugins(nil)
	t.Cleanup(func() { profile.UsePlugins(nil) })
	const prompt = "prompts/slack-extraction.md"
	user := t.TempDir()
	cfg := config.Config{Profile: user}
	read := pluginProfile("slack", func() config.Config { return cfg })

	shipped, err := read(context.Background(), prompt)
	if err != nil || !strings.Contains(string(shipped), "{{") {
		t.Fatalf("the bundled layer's prompt: %v %.40q", err, shipped)
	}
	if _, err := read(context.Background(), "tools.toml"); err != nil {
		t.Errorf("the bundled layer's tools.toml: %v", err)
	}

	mine := filepath.Join(user, profile.PluginsDir, "slack", "prompts")
	if err := os.MkdirAll(mine, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mine, "slack-extraction.md"), []byte("my prompt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := read(context.Background(), prompt); err != nil || string(got) != "my prompt" {
		t.Errorf("the user's prompt: %v %q", err, got)
	}
}

type askOnlyAgent struct {
	turns.Agent
	asker turns.Asker
}

func (a askOnlyAgent) Asker(string, map[turns.Tier]string) (turns.Asker, error) { return a.asker, nil }

type spawnAsker struct{}

func (spawnAsker) Ask(ctx context.Context, _ turns.Ask) (turns.Answer, error) {
	meter.Add(ctx, meter.Sample{Res: meter.Agent, Op: "ask", Spawn: true})
	return turns.Answer{JSON: []byte(`{}`)}, nil
}

func TestAPluginsAskIsMeteredUnderTheScopeItNames(t *testing.T) {
	reg := meter.NewRegistry(nil)
	cfg := config.Config{Agent: "fake"}
	e := &Env{StateDir: t.TempDir(), meter: reg, LoadConfig: func() (config.Config, error) { return cfg, nil },
		Agents: map[string]turns.Agent{"fake": askOnlyAgent{asker: spawnAsker{}}}}
	if _, err := e.pluginAsk("slack")(context.Background(), src.AskRequest{Prompt: "p", MeterScope: "job:extract:slack"}); err != nil {
		t.Fatal(err)
	}
	for _, tot := range reg.Totals() {
		if tot.Name == "job:extract:slack" {
			if got := tot.Tally.Total(meter.Agent); got.Spawns != 1 || tot.Runs != 0 {
				t.Fatalf("job:extract:slack %+v runs %d, want one agent spawn and no run of its own", got, tot.Runs)
			}
			return
		}
	}
	t.Fatalf("no job:extract:slack scope in %+v", reg.Totals())
}
