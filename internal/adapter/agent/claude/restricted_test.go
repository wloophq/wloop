package claude

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/core/turns"
)

const (
	helpWithRestricted    = "Usage: claude [options]\n  --disable-slash-commands  Disable all skills and commands\n  --restricted   Restricted mode\n  --tools <tools...>\n"
	helpWithoutRestricted = "Usage: claude [options]\n  --tools <tools...>\n"
)

type fakeClaude struct {
	help  string
	calls [][]string
	dirs  []string
}

func (f *fakeClaude) sh(_ context.Context, dir string, _ []string, _, _ string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, args)
	f.dirs = append(f.dirs, dir)
	switch {
	case slices.Equal(args, []string{"--help"}):
		return []byte(f.help), nil
	case slices.Equal(args, []string{"--version"}):
		return []byte("2.0.1 (Claude Code)\n"), nil
	}
	return []byte(envelopeJSON(answerJSON, `""`, false)), nil
}

func (f *fakeClaude) asks() [][]string {
	var out [][]string
	for _, c := range f.calls {
		if len(c) > 0 && c[0] == "-p" {
			out = append(out, c)
		}
	}
	return out
}

func fakeAsker(t *testing.T, f *fakeClaude) Asker {
	t.Helper()
	return Asker{Bin: filepath.Join(t.TempDir(), "claude"), Dir: filepath.Join(t.TempDir(), "asks"), Sh: f.sh}
}

func TestAskArgvUnchangedWithoutHeadlessFields(t *testing.T) {
	raw, err := os.ReadFile("testdata/ask-argv.golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var golden []string
	if err := json.Unmarshal(raw, &golden); err != nil {
		t.Fatal(err)
	}
	f := &fakeClaude{help: helpWithoutRestricted}
	a := fakeAsker(t, f)
	if _, err := a.Ask(context.Background(), turns.Ask{Tier: turns.TierBalanced, Prompt: "p", Schema: `{"type":"object"}`, MaxCostUSD: 0.5}); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 1 || !slices.Equal(f.calls[0], golden) {
		t.Fatalf("argv %q, want %q", f.calls, golden)
	}
}

func TestHeadlessArgvIsRestricted(t *testing.T) {
	f := &fakeClaude{help: helpWithRestricted}
	a := fakeAsker(t, f)
	checkout, dossier := t.TempDir(), t.TempDir()
	_, err := a.Ask(context.Background(), turns.Ask{Tier: turns.TierBalanced, Prompt: "p", Schema: `{"type":"object"}`, MaxCostUSD: 0.5,
		Tools: turns.ReadOnlyTools, Dirs: []string{checkout, dossier}})
	if err != nil {
		t.Fatal(err)
	}
	asks := f.asks()
	if len(asks) != 1 {
		t.Fatalf("calls %q", f.calls)
	}
	want := []string{"-p", "--output-format", "json", "--json-schema", `{"type":"object"}`,
		"--tools", "Read,Grep,Glob", "--no-session-persistence", "--strict-mcp-config", "--restricted",
		"--add-dir", checkout, "--add-dir", dossier, "--settings", `{"disableAllHooks":true}`,
		"--disable-slash-commands", "--model", "sonnet", "--max-budget-usd", "0.5"}
	if !slices.Equal(asks[0], want) {
		t.Fatalf("argv %q\nwant %q", asks[0], want)
	}
	if slices.Contains(asks[0], "--setting-sources") || slices.ContainsFunc(asks[0], func(s string) bool { return strings.Contains(s, "project") }) {
		t.Errorf("argv loads a settings source: %q", asks[0])
	}
	if d := f.dirs[len(f.dirs)-1]; d != a.Dir {
		t.Errorf("ran in %s, want %s", d, a.Dir)
	}
}

func TestHeadlessArgvWithDirsAndNoTools(t *testing.T) {
	f := &fakeClaude{help: helpWithRestricted}
	a := fakeAsker(t, f)
	dir := t.TempDir()
	if _, err := a.Ask(context.Background(), turns.Ask{Dirs: []string{dir}}); err != nil {
		t.Fatal(err)
	}
	got := f.asks()[0]
	if i := slices.Index(got, "--tools"); i < 0 || got[i+1] != "" || !slices.Contains(got, "--restricted") || slices.Contains(got, "--setting-sources") {
		t.Fatalf("argv %q", got)
	}
}

func TestAskRefusesToolsOutsideTheAllowlist(t *testing.T) {
	for _, tool := range []string{"Edit", "Write", "Bash", "WebFetch", "Agent", "NotebookEdit", "mcp__x__y", "read", ""} {
		t.Run(tool, func(t *testing.T) {
			f := &fakeClaude{help: helpWithRestricted}
			a := fakeAsker(t, f)
			_, err := a.Ask(context.Background(), turns.Ask{Tools: []string{"Read", tool}, Dirs: []string{t.TempDir()}})
			if !errors.Is(err, turns.ErrToolNotAllowed) || len(f.calls) != 0 {
				t.Fatalf("err %v calls %q", err, f.calls)
			}
		})
	}
}

func TestAskRefusesRelativeDirs(t *testing.T) {
	for _, dir := range []string{"", "rel", "./rel", "../up"} {
		t.Run(dir, func(t *testing.T) {
			f := &fakeClaude{help: helpWithRestricted}
			a := fakeAsker(t, f)
			_, err := a.Ask(context.Background(), turns.Ask{Tools: turns.ReadOnlyTools, Dirs: []string{t.TempDir(), dir}})
			if err == nil || !strings.Contains(err.Error(), "absolute") || len(f.calls) != 0 {
				t.Fatalf("err %v calls %q", err, f.calls)
			}
		})
	}
}

func TestAskNeedsRestricted(t *testing.T) {
	f := &fakeClaude{help: helpWithoutRestricted}
	a := fakeAsker(t, f)
	for _, q := range []turns.Ask{{Tools: turns.ReadOnlyTools}, {Dirs: []string{t.TempDir()}}} {
		if _, err := a.Ask(context.Background(), q); !errors.Is(err, turns.ErrNoRestricted) || len(f.asks()) != 0 {
			t.Fatalf("%+v: err %v asks %q", q, err, f.asks())
		}
	}
	if _, err := a.Ask(context.Background(), turns.Ask{Prompt: "p"}); err != nil || len(f.asks()) != 1 {
		t.Fatalf("a plain ask: err %v asks %q", err, f.asks())
	}
}

func TestTheProbeRunsOncePerBinary(t *testing.T) {
	f := &fakeClaude{help: helpWithRestricted}
	a := fakeAsker(t, f)
	for range 3 {
		if _, err := a.Ask(context.Background(), turns.Ask{Tools: turns.ReadOnlyTools}); err != nil {
			t.Fatal(err)
		}
	}
	helps := 0
	for _, c := range f.calls {
		if slices.Equal(c, []string{"--help"}) {
			helps++
		}
	}
	if helps != 1 {
		t.Fatalf("--help ran %d times", helps)
	}
	other := fakeAsker(t, f)
	if _, err := other.Ask(context.Background(), turns.Ask{Tools: turns.ReadOnlyTools}); err != nil {
		t.Fatal(err)
	}
	if n := len(f.calls) - len(f.asks()); n != 2 {
		t.Fatalf("another binary path probed %d times in all, want 2", n)
	}
}

func TestAFailedProbeIsNotCached(t *testing.T) {
	calls := 0
	sh := func(_ context.Context, _ string, _ []string, _, _ string, args ...string) ([]byte, error) {
		calls++
		if args[0] == "--help" && calls == 1 {
			return nil, errors.New("boom")
		}
		if args[0] == "--help" {
			return []byte(helpWithRestricted), nil
		}
		return []byte(envelopeJSON(answerJSON, `""`, false)), nil
	}
	a := Asker{Bin: filepath.Join(t.TempDir(), "claude"), Dir: t.TempDir(), Sh: sh}
	if _, err := a.Ask(context.Background(), turns.Ask{Tools: turns.ReadOnlyTools}); err == nil || errors.Is(err, turns.ErrNoRestricted) {
		t.Fatalf("a failed probe: %v", err)
	}
	if _, err := a.Ask(context.Background(), turns.Ask{Tools: turns.ReadOnlyTools}); err != nil {
		t.Fatalf("after a failed probe: %v", err)
	}
}

func TestKindReportsRestrictedSupport(t *testing.T) {
	f := &fakeClaude{help: helpWithoutRestricted}
	warn, err := Agent{AskSh: f.sh}.RestrictedWarning(context.Background())
	if err != nil || !strings.Contains(warn, "--restricted") || !strings.Contains(warn, "2.0.1") {
		t.Fatalf("warning %q err %v", warn, err)
	}
	f.help = helpWithRestricted
	if warn, err := (Agent{AskSh: f.sh}).RestrictedWarning(context.Background()); err != nil || warn != "" {
		t.Fatalf("warning %q err %v", warn, err)
	}
}

func TestHeadlessArgvSkipsSlashCommandsFlagClaudeLacks(t *testing.T) {
	f := &fakeClaude{help: "  --restricted   Restricted mode\n"}
	a := fakeAsker(t, f)
	if _, err := a.Ask(context.Background(), turns.Ask{Tools: turns.ReadOnlyTools}); err != nil {
		t.Fatal(err)
	}
	if got := f.asks()[0]; slices.Contains(got, "--disable-slash-commands") || !slices.Contains(got, "--restricted") {
		t.Fatalf("argv %q", got)
	}
}

func TestTheProbeFollowsSymlinksToTheInstalledVersion(t *testing.T) {
	dir := t.TempDir()
	for _, v := range []string{"v1", "v2"} {
		if err := os.WriteFile(filepath.Join(dir, v), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(dir, "claude")
	if err := os.Symlink(filepath.Join(dir, "v1"), link); err != nil {
		t.Fatal(err)
	}
	f := &fakeClaude{help: helpWithoutRestricted}
	a := Asker{Bin: link, Dir: t.TempDir(), Sh: f.sh}
	if _, err := a.Ask(context.Background(), turns.Ask{Tools: turns.ReadOnlyTools}); !errors.Is(err, turns.ErrNoRestricted) {
		t.Fatalf("v1: %v", err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "v2"), link); err != nil {
		t.Fatal(err)
	}
	f.help = helpWithRestricted
	if _, err := a.Ask(context.Background(), turns.Ask{Tools: turns.ReadOnlyTools}); err != nil {
		t.Fatalf("after the upgrade: %v", err)
	}
}

func TestReadySaysWhetherAHeadlessCallCanRun(t *testing.T) {
	f := &fakeClaude{help: helpWithoutRestricted}
	a := fakeAsker(t, f)
	if err := a.Ready(context.Background(), turns.Ask{Tools: turns.ReadOnlyTools}); !errors.Is(err, turns.ErrNoRestricted) || len(f.asks()) != 0 {
		t.Fatalf("without --restricted: %v, asks %q", err, f.asks())
	}
	if err := a.Ready(context.Background(), turns.Ask{Tools: []string{"Bash"}}); !errors.Is(err, turns.ErrToolNotAllowed) {
		t.Fatalf("a write tool: %v", err)
	}
	if err := a.Ready(context.Background(), turns.Ask{}); err != nil {
		t.Fatalf("a plain call: %v", err)
	}
	g := &fakeClaude{help: helpWithRestricted}
	if err := fakeAsker(t, g).Ready(context.Background(), turns.Ask{Dirs: []string{t.TempDir()}}); err != nil || len(g.asks()) != 0 {
		t.Fatalf("with --restricted: %v, asks %q", err, g.asks())
	}
}
