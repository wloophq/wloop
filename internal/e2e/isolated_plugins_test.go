package e2e

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/brand/brandtest"
	"github.com/wloophq/wloop/internal/cli"
	"github.com/wloophq/wloop/internal/config"
)

// buildRelease builds the binary and each plugin as `make build` places
// them: the binary in a directory, each plugin in plugins/<id>/ beside it
// with its manifest and profile layer.
func buildRelease(t *testing.T, ids ...string) string {
	t.Helper()
	dir := t.TempDir()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	build := func(out, pkg string) {
		t.Helper()
		cmd := exec.Command("go", "build", "-ldflags", brandFlags(), "-o", out, pkg)
		cmd.Dir = root
		if raw, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go build %s: %v\n%s", pkg, err, raw)
		}
	}
	bin := filepath.Join(dir, brand.Name)
	build(bin, "./cmd/"+brandtest.DefaultName)
	for _, id := range ids {
		to := filepath.Join(dir, "plugins", id)
		build(filepath.Join(to, brand.PluginBinary(id)), "./plugins/"+id+"/cmd/"+brandtest.DefaultName+"-plugin-"+id)
		if raw, err := exec.Command("cp", "-R", filepath.Join(root, "plugins", id, "plugin.toml"), filepath.Join(root, "plugins", id, "profile"), to).CombinedOutput(); err != nil {
			t.Fatalf("copying %s's manifest and profile: %v\n%s", id, err, raw)
		}
	}
	return bin
}

// brandFlags build a binary with this test's brand.
func brandFlags() string {
	const pkg = "github.com/wloophq/wloop/internal/brand."
	return fmt.Sprintf(`-X %sName=%s -X "%sCommand=%s" -X %sApp=%s -X %sOrg=%s`, pkg, brand.Name, pkg, brand.Command, pkg, brand.App, pkg, brand.Org)
}

// e2eKeychain is the Keychain service the isolated config names, the
// script's own, whatever the build's brand.
const e2eKeychain = brandtest.DefaultName + "-e2e-slack"

func TestIsolatedDaemonRunsThePlugins(t *testing.T) {
	bin := buildRelease(t, "github", "slack")
	dir := filepath.Join(shortDir(t), "p")
	if out, err := isolated(t, "setup", dir, bin, isolatedDM, "7", "--slack", "--plugins", "slack"); err == nil {
		t.Errorf("--slack with --plugins slack: %s", out)
	}
	mustIsolated(t, "setup", dir, bin, isolatedDM, "7", "--plugins", "github,slack")
	t.Cleanup(func() { isolated(t, "stop", dir) })
	for _, id := range []string{"github", "slack"} {
		if _, err := os.Stat(filepath.Join(dir, "bin", "plugins", id, brand.PluginBinary(id))); err != nil {
			t.Errorf("setup did not copy %s's binary: %v", id, err)
		}
	}
	file := filepath.Join(dir, "config", brand.StateDir(), "config.toml")
	c, err := config.LoadFile(file, cli.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"github", "slack"} {
		if !c.PluginEnabled(id) || c.PluginTrust(id) != "path" {
			t.Errorf("%s: enabled %v trust %q", id, c.PluginEnabled(id), c.PluginTrust(id))
		}
	}
	if c.Slack.Ingest.Socket == nil || *c.Slack.Ingest.Socket {
		t.Error("--plugins slack runs Slack's socket")
	}
	if c.Slack.KeychainService != e2eKeychain {
		t.Errorf("slack's Keychain service %q, want the e2e one", c.Slack.KeychainService)
	}
	// The e2e Keychain item may hold a live token; the daemon here reads one no machine has.
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	raw = []byte(strings.Replace(string(raw), `"`+e2eKeychain+`"`, fmt.Sprintf(`"keychain-test-%d"`, time.Now().UnixNano()), 1))
	raw = withFreePorts(t, raw)
	if err := os.WriteFile(file, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	mustIsolated(t, "start", dir)
	log := filepath.Join(dir, "daemon.log")
	want := []string{"plugin github: runs " + filepath.Join(dir, "bin", "plugins", "github", brand.PluginBinary("github")) + ", trusted by its path",
		"plugin slack: runs " + filepath.Join(dir, "bin", "plugins", "slack", brand.PluginBinary("slack")) + ", trusted by its path",
		"plugin github: loaded", "plugin slack: loaded"}
	var got string
	for deadline := time.Now().Add(90 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		b, _ := os.ReadFile(log)
		got = string(b)
		if containsAll(got, want) {
			break
		}
	}
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("the daemon's log lacks %q:\n%s", w, got)
		}
	}
	for _, id := range []string{"github", "slack"} {
		if _, err := os.Stat(filepath.Join(dir, "state", brand.StateDir(), "plugins", id, "plugin.db")); err != nil {
			t.Errorf("%s opened no store of its own: %v", id, err)
		}
	}
}

// withFreePorts points the isolated config's HTTP and MCP ports at ports
// free now: another isolated daemon on this machine may hold its index's.
func withFreePorts(t *testing.T, config []byte) []byte {
	t.Helper()
	n := 0
	out := regexp.MustCompile(`(?m)^port = \d+$`).ReplaceAllStringFunc(string(config), func(string) string {
		n++
		return "port = " + strconv.Itoa(freePort(t))
	})
	if n != 2 {
		t.Fatalf("the isolated config has %d ports, want the HTTP and the MCP one:\n%s", n, config)
	}
	return []byte(out)
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func containsAll(s string, parts []string) bool {
	for _, p := range parts {
		if !strings.Contains(s, p) {
			return false
		}
	}
	return true
}

// v6StoreWith is a version 6 store, from its frozen schema, holding a
// pull request, a Slack message and the GitHub viewer.
func v6StoreWith(t *testing.T) string {
	t.Helper()
	schema, err := filepath.Abs(filepath.Join("..", "adapter", "store", "sqlite", "testdata", "schema-v6.sql"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "source", "store.db")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	script := ".read " + schema + "\n" + `PRAGMA user_version = 6;
INSERT INTO meta (key, value) VALUES ('compatible_version', '4'), ('viewer:github', 'ana');
INSERT INTO pull_requests (owner, repo, number, title, state) VALUES ('acme', 'api', 7, 'Fix the build', 'OPEN');
INSERT INTO slack_messages (conversation, ts, text) VALUES ('C1', '1.1', 'hello');
`
	cmd := exec.Command("sqlite3", path)
	cmd.Stdin = strings.NewReader(script)
	if raw, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("a version 6 store: %v\n%s", err, raw)
	}
	return path
}

func TestASnapshotsPluginsImportAndReport(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("snapshot needs sqlite3")
	}
	bin := buildRelease(t, "github", "slack")
	src := v6StoreWith(t)
	dir := filepath.Join(shortDir(t), "c5")
	mustIsolated(t, "setup", dir, bin, isolatedDM, "6", "--plugins", "github,slack")
	t.Cleanup(func() { isolated(t, "stop", dir) })
	file := filepath.Join(dir, "config", brand.StateDir(), "config.toml")
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	// The e2e Keychain item may hold a live token; the daemon here reads one no machine has.
	raw = []byte(strings.Replace(string(raw), `"`+e2eKeychain+`"`, fmt.Sprintf(`"keychain-test-%d"`, time.Now().UnixNano()), 1))
	raw = withFreePorts(t, raw)
	if err := os.WriteFile(file, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	beside := filepath.Join(filepath.Dir(src), "plugins", "acme", "import.db")
	if err := os.MkdirAll(filepath.Dir(beside), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(beside, []byte("an export"), 0o600); err != nil {
		t.Fatal(err)
	}
	mustIsolated(t, "snapshot", dir, src)
	if raw, err := os.ReadFile(filepath.Join(dir, "state", brand.StateDir(), "plugins", "acme", "import.db")); err != nil || string(raw) != "an export" {
		t.Errorf("snapshot did not copy the import.db beside the store: %q %v", raw, err)
	}
	if out, err := isolated(t, "start", dir); err == nil {
		t.Errorf("a copy served with its jobs on, without --jobs: %s", out)
	}
	mustIsolated(t, "run", dir, filepath.Join(dir, "bin", brand.Name), "store", "migrate", "--yes")
	mustIsolated(t, "start", dir, "--jobs")
	state := filepath.Join(dir, "state", brand.StateDir())
	want := []string{"plugin github: loaded", "plugin slack: loaded"}
	var got string
	for deadline := time.Now().Add(90 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		b, _ := os.ReadFile(filepath.Join(dir, "daemon.log"))
		got = string(b)
		if containsAll(got, want) {
			break
		}
	}
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("the daemon's log lacks %q:\n%s", w, got)
		}
	}
	for id, table := range map[string]string{"github": "pull_requests", "slack": "slack_messages"} {
		if _, err := os.Stat(filepath.Join(state, "plugins", id, "import.db.done")); err != nil {
			t.Errorf("%s did not import its export: %v", id, err)
		}
		out, err := exec.Command("sqlite3", "file:"+filepath.Join(state, "plugins", id, "plugin.db")+"?mode=ro", "SELECT count(*) FROM "+table).CombinedOutput()
		if err != nil || strings.TrimSpace(string(out)) != "1" {
			t.Errorf("%s's store holds %q rows of %s (%v), want the snapshot's one", id, out, table, err)
		}
	}
}
