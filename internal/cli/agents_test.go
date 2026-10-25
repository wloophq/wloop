package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/adapter/agent/claude"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/turns"
)

func TestTheConfigsPinsReachTheAgentsHeadlessCalls(t *testing.T) {
	t.Parallel()
	for name, raw := range map[string]string{
		"agent_models": `{"brief": {"enabled": true}, "agent_models": {"claude": {"fast": "x"}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := config.LoadFile(path, Defaults())
			if err != nil {
				t.Fatal(err)
			}
			var args []string
			sh := func(_ context.Context, _ string, _ []string, _, _ string, a ...string) ([]byte, error) {
				args = a
				return []byte(`{"type":"result","is_error":false,"result":"{}"}`), nil
			}
			e := &Env{Agents: map[string]turns.Agent{"claude": claude.Agent{AskSh: sh}}}
			a, err := e.asker(cfg, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := a.Ask(context.Background(), turns.Ask{Tier: cfg.Brief.Tier}); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(strings.Join(args, " "), "--model x") {
				t.Fatalf("args %v", args)
			}
		})
	}
}

func TestAKindWithoutAHeadlessShellIsRefusedAtStartup(t *testing.T) {
	t.Parallel()
	e := &Env{Agents: map[string]turns.Agent{"claude": claude.Agent{}}}
	if _, err := e.asker(Defaults(), t.TempDir()); err == nil {
		t.Fatal("an asker without a shell")
	}
}

func TestUnknownAgentNamesTheSupportedOnes(t *testing.T) {
	t.Parallel()
	e := &Env{Agents: map[string]turns.Agent{"claude": claude.Agent{}, "aider": claude.Agent{}}}
	_, err := e.agent("codex")
	if err == nil || err.Error() != `agent "codex" is not supported; supported: aider, claude` {
		t.Fatalf("err = %v", err)
	}
	if a, err := e.agent("claude"); err != nil || a.Name() != "claude" {
		t.Fatalf("%v %v", a, err)
	}
}

func TestAnAgentWithoutHeadlessIsRefused(t *testing.T) {
	t.Parallel()
	e := &Env{Agents: map[string]turns.Agent{"plain": plainKind{}}}
	if _, err := e.headlessAgent("plain"); err == nil || err.Error() != `agent "plain" cannot run headless` {
		t.Fatalf("err = %v", err)
	}
}

type plainKind struct{ turns.Agent }

func (plainKind) Name() string { return "plain" }
