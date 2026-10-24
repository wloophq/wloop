package claude

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/core/setup"
)

func TestHomeKindDefaultAndSkillsRoot(t *testing.T) {
	k := NewHomeKind(nil)
	if got := k.Default("/u"); got != "/u/.claude" {
		t.Fatalf("default %q", got)
	}
	if got := k.SkillsRoot("/h"); got != "/h" {
		t.Fatalf("skills root %q", got)
	}
}

func TestTheCallersHomeIsTheConfigDir(t *testing.T) {
	if got := (Protocol{}).Home(func(key string) string { return map[string]string{"CLAUDE_CONFIG_DIR": "/p/home"}[key] }); got != "/p/home" {
		t.Fatalf("caller %q", got)
	}
	if got := (Protocol{}).Home(func(string) string { return "" }); got != "" {
		t.Fatalf("caller %q", got)
	}
}

func TestHomeKindFindsTheCallersAndTheManagersHomes(t *testing.T) {
	user := t.TempDir()
	profile := filepath.Join(user, "Library", "Application Support", "orca", "claude-profiles", "p1", "home")
	if err := os.MkdirAll(profile, 0o755); err != nil {
		t.Fatal(err)
	}
	got := NewHomeKind(nil).Find(setup.HomeSpec{UserHome: user, CallerHome: "/caller", ManagerHomes: []string{"/m/b", "/m/a"}})
	if want := []string{filepath.Join(user, ".claude"), "/caller", "/m/b", "/m/a"}; !slices.Equal(got, want) {
		t.Fatalf("found %q, want %q", got, want)
	}
}

func TestHomeKindRegistersTheHeadersHelper(t *testing.T) {
	user, home := t.TempDir(), t.TempDir()
	sh := func(_ context.Context, _ string, _ []string, _ string, _ string, args ...string) ([]byte, error) {
		return nil, fmt.Errorf("unexpected call %q", args)
	}
	h := NewHomeKind(sh).New(setup.HomeSpec{Command: brand.Name, MCPURL: testURL, MCPKey: testKey, UserHome: user, SessionVars: []string{"ORCA_TERMINAL_HANDLE", "CLAUDE_CODE_HOST_SESSION_ID"}})
	if _, err := h.Install(context.Background(), home, false); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(home, ".claude.json"))
	if added := string(raw); !strings.Contains(added, `"headersHelper": "`+brand.Name+` hook mcp-headers"`) {
		t.Fatalf("registration %s", added)
	}
}

func TestHomeKindOwnsADirWithItsFiles(t *testing.T) {
	k := NewHomeKind(nil).(setup.HomeOwner)
	empty, withSettings, withMCP := t.TempDir(), t.TempDir(), t.TempDir()
	for dir, file := range map[string]string{withSettings: "settings.json", withMCP: ".claude.json"} {
		if err := os.WriteFile(filepath.Join(dir, file), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if k.Owns(empty) || k.Owns(filepath.Join(empty, "absent")) || !k.Owns(withSettings) || !k.Owns(withMCP) {
		t.Fatalf("owns empty %v, settings %v, .claude.json %v", k.Owns(empty), k.Owns(withSettings), k.Owns(withMCP))
	}
}
