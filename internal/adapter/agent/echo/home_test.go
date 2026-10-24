package echo_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/adapter/agent/echo"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/client/harness"
	"github.com/wloophq/wloop/internal/core/setup"
)

var _ harness.Protocol = echo.Protocol{}

func TestHomeKindKeepsItsHomesUnderRoot(t *testing.T) {
	root := t.TempDir()
	k := echo.HomeKind{Root: root}
	if got := k.Default("/elsewhere"); got != filepath.Join(root, ".echo") {
		t.Fatalf("default %q", got)
	}
	got := k.Find(setup.HomeSpec{UserHome: "/elsewhere", CallerHome: "/caller", ManagerHomes: []string{"/m"}})
	if want := []string{filepath.Join(root, ".echo"), "/caller", "/m"}; !slices.Equal(got, want) {
		t.Fatalf("found %q", got)
	}
	if got := (echo.Protocol{}).Home(func(v string) string { return map[string]string{"ECHO_HOME": "/h"}[v] }); got != "/h" {
		t.Fatalf("caller %q", got)
	}
	if k.SkillsRoot("/h") != "/h" {
		t.Fatal("skills root")
	}
}

func TestHomeInstallsChecksAndRemoves(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	k := echo.HomeKind{Root: t.TempDir()}
	h := k.New(setup.HomeSpec{Command: brand.Name, MCPURL: "http://127.0.0.1:1/mcp", SessionVars: []string{"FAKE_TERMINAL"}})
	problems := func() (n int) {
		fs, err := h.Check(ctx, home)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range fs {
			if f.Problem != "" {
				n++
			}
		}
		return n
	}
	if problems() == 0 || k.Owns(home) {
		t.Fatal("an empty home checks clean")
	}
	if lines, err := h.Install(ctx, home, true); err != nil || len(lines) != 1 || k.Owns(home) {
		t.Fatalf("dry run %q %v", lines, err)
	}
	if _, err := h.Install(ctx, home, false); err != nil || problems() != 0 || !k.Owns(home) {
		t.Fatalf("install: %v, %d problems", err, problems())
	}
	if lines, _ := h.Install(ctx, home, false); len(lines) != 0 {
		t.Fatalf("second install %q", lines)
	}
	if _, err := h.Remove(ctx, home, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, brand.Name+".json")); err == nil {
		t.Fatal("remove left the file")
	}
}

func TestProtocolReadsItsPayloads(t *testing.T) {
	p := echo.Protocol{}
	if s, src, cwd, err := p.SessionStart([]byte(`{"id":"s-1"}`)); s != "s-1" || src != "startup" || cwd != "" || err != nil {
		t.Fatalf("start %q %q %q %v", s, src, cwd, err)
	}
	if s, id, last, err := p.Stop([]byte(`{"id":"s-1","prompt":"p","last":"m"}`)); s != "s-1" || id != "p" || last != "m" || err != nil {
		t.Fatalf("stop %q %q %q %v", s, id, last, err)
	}
	if _, _, _, err := p.Stop([]byte(`{`)); err == nil {
		t.Fatal("malformed payload read")
	}
	if got := string(p.Block("owe a record")); got != "BLOCK owe a record\n" {
		t.Fatalf("block %q", got)
	}
	env := func(v string) string { return map[string]string{"ECHO_SESSION": "s", "ECHO_HOME": "/h"}[v] }
	if p.SessionID(env) != "s" || p.Home(env) != "/h" {
		t.Fatal("env")
	}
}
