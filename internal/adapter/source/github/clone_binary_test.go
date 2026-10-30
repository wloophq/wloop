package ghsource_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/hashicorp/go-hclog"

	ghsource "github.com/wloophq/wloop/internal/adapter/source/github"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/plugins/host"
	"github.com/wloophq/wloop/pkg/plugin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// fakeGHScript records each call's arguments a line and answers the reads
// the commands make.
const fakeGHScript = `#!/bin/sh
echo "$*" >> "$GH_CALLS"
case "$1 $2" in
  "search prs") printf 'acme/api\nacme/web\n' ;;
  "api user/teams") printf 'acme/backend\n' ;;
  "auth status") [ -e "$GH_LOGGED_OUT" ] && { echo "not logged in" >&2; exit 1; } ;;
esac
exit 0
`

func buildPluginBinary(t *testing.T) string {
	t.Helper()
	mains, err := filepath.Glob(filepath.Join("..", "..", "..", "..", "plugins", "github", "cmd", "*"))
	if err != nil || len(mains) != 1 {
		t.Fatalf("the plugin's main packages %v %v", mains, err)
	}
	bin := filepath.Join(t.TempDir(), "plugin")
	cmd := exec.Command("go", "build", "-o", bin, "./"+filepath.ToSlash(mains[0]))
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return bin
}

func TestThePluginBinaryServesTheCloneCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake gh is a shell script")
	}
	bin := buildPluginBinary(t)
	raw, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	dir := t.TempDir()
	ghDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(ghDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ghDir, "gh"), []byte(fakeGHScript), 0o755); err != nil {
		t.Fatal(err)
	}
	calls, loggedOut := filepath.Join(dir, "calls"), filepath.Join(dir, "logged-out")
	launch := func(path string) *src.Client {
		t.Helper()
		proc, err := host.ExecLauncher{}.Launch(context.Background(), plugin.Spec{ID: "github", Path: bin, Kinds: []plugin.Kind{src.Kind},
			Env:      []string{"PATH=" + path, "GH_CALLS=" + calls, "GH_LOGGED_OUT=" + loggedOut, plugin.BrandEnv + "=" + brand.Name},
			Checksum: sum[:], Logger: hclog.NewNullLogger()})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(proc.Kill)
		c, err := proc.Kind(src.Kind)
		if err != nil {
			t.Fatal(err)
		}
		return src.NewClient(c)
	}
	c := launch(ghDir)
	run := func(c *src.Client, args ...string) (int, string, string) {
		t.Helper()
		var stdout, stderr bytes.Buffer
		exit, err := c.RunCLI(context.Background(), src.CLIRequest{Args: args}, &stdout, &stderr)
		if err != nil {
			t.Fatalf("%q: %v", args, err)
		}
		return exit, stdout.String(), stderr.String()
	}
	lastCall := func() string {
		t.Helper()
		raw, err := os.ReadFile(calls)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
		return lines[len(lines)-1]
	}

	projects := filepath.Join(dir, "projects", "api")
	if exit, out, stderr := run(c, ghsource.CommandClone, "acme/api", projects); exit != 0 || out != "" || stderr != "" {
		t.Fatalf("clone: exit %d, %q, %q", exit, out, stderr)
	}
	if got := lastCall(); got != "repo clone acme/api "+projects {
		t.Fatalf("clone ran gh %q", got)
	}
	if exit, _, stderr := run(c, ghsource.CommandAuth); exit != 0 || stderr != "" {
		t.Fatalf("auth: exit %d, %q", exit, stderr)
	}
	if err := os.WriteFile(loggedOut, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if exit, _, stderr := run(c, ghsource.CommandAuth); exit != 1 || !strings.Contains(stderr, "not logged in") {
		t.Fatalf("logged out: exit %d, %q", exit, stderr)
	}
	if exit, out, _ := run(c, ghsource.CommandReviewedRepos, "2026-09-28"); exit != 0 || out != "acme/api\nacme/web\n" ||
		!strings.Contains(lastCall(), "search prs --reviewed-by=@me --updated=>=2026-09-28") {
		t.Fatalf("reviewed repos: exit %d, %q, gh %q", exit, out, lastCall())
	}
	if exit, out, _ := run(c, ghsource.CommandTeams); exit != 0 || out != "acme/backend\n" {
		t.Fatalf("teams: exit %d, %q", exit, out)
	}
	if exit, _, _ := run(launch(filepath.Join(dir, "empty")), ghsource.CommandAuth); exit != ghsource.ExitNoGH {
		t.Fatalf("no gh on the PATH: exit %d, want %d", exit, ghsource.ExitNoGH)
	}
}
