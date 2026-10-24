package claude

import (
	"context"
	"strings"
	"testing"
)

func TestShellMixesStderrButExecDoesNot(t *testing.T) {
	ctx := context.Background()
	out, err := Shell(ctx, "", nil, "", "sh", "-c", "echo out; echo err >&2")
	if err != nil || !strings.Contains(string(out), "out\n") || !strings.Contains(string(out), "err\n") {
		t.Fatalf("Shell: %q %v", out, err)
	}
	out, err = Exec(ctx, "", nil, "", "sh", "-c", "echo out; echo err >&2")
	if err != nil || string(out) != "out\n" {
		t.Fatalf("Exec: %q %v", out, err)
	}
}

func TestShellAppliesEnvOverTheProcesssAndDropsClaudeCode(t *testing.T) {
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("LOOP_SHELL_KEEP", "kept")
	t.Setenv("LOOP_SHELL_UNSET", "gone")
	t.Setenv("LOOP_SHELL_SET", "old")
	out, err := Shell(context.Background(), t.TempDir(), []string{"LOOP_SHELL_UNSET", "LOOP_SHELL_SET=new"}, "in",
		"sh", "-c", `echo "$CLAUDECODE|$LOOP_SHELL_KEEP|$LOOP_SHELL_UNSET|$LOOP_SHELL_SET|$(cat)"`)
	if err != nil || string(out) != "|kept||new|in\n" {
		t.Fatalf("%q %v", out, err)
	}
}
