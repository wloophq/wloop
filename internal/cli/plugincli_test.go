package cli

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/source"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

const cliPlugin unit.ProviderID = "fakecli"

// withCLIPlugin compiles in, beside the real plugins, a plugin whose CLI is
// called name and records the arguments of each run.
func withCLIPlugin(t *testing.T, name string, got *[][]string) {
	t.Helper()
	saved := plugins
	plugins = append(slices.Clone(saved), source.Plugin{ID: cliPlugin, Codec: pr.Codec{}, Build: func(config.Config, source.Deps) (source.Module, error) {
		return source.Module{ID: cliPlugin, CLI: &source.CLI{Name: name, Usage: name + " thread <url>", Run: func(_ context.Context, args []string, out, _ io.Writer) error {
			*got = append(*got, args)
			_, err := fmt.Fprintln(out, "plugin ran")
			return err
		}}}, nil
	}})
	t.Cleanup(func() { plugins = saved })
}

func disablePlugin(cfg *config.Config, id unit.ProviderID) {
	off := false
	cfg.Plugins.Other = map[string]config.Plugin{string(id): {Enabled: &off}}
}

func TestPluginCLIDispatch(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	var got [][]string
	withCLIPlugin(t, "thread", &got)

	if code := Run(context.Background(), te.Env, []string{"thread", "show", "--all"}); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if len(got) != 1 || !slices.Equal(got[0], []string{"show", "--all"}) || !strings.Contains(te.out.String(), "plugin ran") {
		t.Fatalf("args %v, out %q", got, te.out)
	}

	disablePlugin(&te.cfg, cliPlugin)
	te.out.Reset()
	if code := Run(context.Background(), te.Env, []string{string(cliPlugin), "show"}); code != 1 || !strings.Contains(te.err.String(), "the fakecli plugin is disabled") {
		t.Fatalf("while disabled: exit %d: %s", code, te.err)
	}
	if len(got) != 1 {
		t.Fatalf("a disabled plugin's CLI ran: %v", got)
	}
	if err := runPluginCLI(context.Background(), te.Env, cliPlugin, []string{"x"}); err == nil || err.Error() != "the fakecli plugin is disabled" {
		t.Fatalf("runPluginCLI while disabled: %v", err)
	}
}

func TestCoreCommandWinsOverPluginCLI(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	var got [][]string
	withCLIPlugin(t, "version", &got)

	if code := Run(context.Background(), te.Env, []string{"version"}); code != 0 || strings.Contains(te.out.String(), "plugin ran") || len(got) != 0 {
		t.Fatalf("exit %d, out %q, plugin runs %v", code, te.out, got)
	}
	if err := runPluginCLI(context.Background(), te.Env, cliPlugin, []string{"thread", "https://example.test/x"}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !slices.Equal(got[0], []string{"thread", "https://example.test/x"}) {
		t.Fatalf("subcommands passed on: %v", got)
	}
	if err := runPluginCLI(context.Background(), te.Env, "nope", nil); err == nil {
		t.Fatal("an unknown plugin ran")
	}
}
