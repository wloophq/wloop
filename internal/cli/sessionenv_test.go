package cli

import (
	"context"
	"strings"
	"testing"

	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

func nudgeCommand(t *testing.T, env map[string]string) string {
	t.Helper()
	m := nudgeManager(mainWS)
	te := newTestEnv(t, m)
	te.gh.basics = openBasics
	te.Getenv = func(k string) string { return env[k] }
	if err := nudgePR(context.Background(), te.Env, te.cfg, r7, nudgeLine, false, "", "/home/me/.claude-work"); err != nil {
		t.Fatal(err)
	}
	if len(m.Commands) != 1 {
		t.Fatalf("commands %v", m.Commands)
	}
	return m.Commands[0]
}

func TestAgentSessionCarriesTheDaemonsXDGDirs(t *testing.T) {
	plain := nudgeCommand(t, nil)
	got := nudgeCommand(t, map[string]string{"XDG_CONFIG_HOME": "/iso/config", "XDG_STATE_HOME": "/iso/my state"})
	if want := "XDG_CONFIG_HOME='/iso/config' XDG_STATE_HOME='/iso/my state' " + plain; got != want {
		t.Fatalf("command\n got %q\nwant %q", got, want)
	}
}

func TestAgentSessionSkipsUnusableXDGDirs(t *testing.T) {
	plain := nudgeCommand(t, nil)
	if launches([]string{plain})[0] != "CLAUDE_CONFIG_DIR='/home/me/.claude-work' claude" {
		t.Fatalf("command %q", plain)
	}
	got := nudgeCommand(t, map[string]string{"XDG_CONFIG_HOME": "relative/config", "XDG_STATE_HOME": ""})
	if got != plain {
		t.Fatalf("command\n got %q\nwant %q", got, plain)
	}
}

func TestAgentSessionWithoutAPromptCarriesTheDaemonsXDGDirs(t *testing.T) {
	m := &fake.Manager{RepoList: []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}}}
	te := newTestEnv(t, m)
	te.gh.basics = adoptBasics
	te.git.branch["/worktrees/feat/theirs"] = "feat/theirs"
	te.Getenv = func(k string) string { return map[string]string{"XDG_STATE_HOME": "/iso/state"}[k] }
	if code := te.run("take-over", "https://github.com/acme/api/pull/20"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if len(m.Commands) != 1 || !strings.HasPrefix(m.Commands[0], "XDG_STATE_HOME='/iso/state' claude ") {
		t.Fatalf("commands %q", m.Commands)
	}
}
