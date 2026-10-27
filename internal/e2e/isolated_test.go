package e2e

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/cli"
	"github.com/wloophq/wloop/internal/config"
)

const isolatedDM = "D000000TEST"

func isolatedScript(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("..", "..", "scripts", "isolated-daemon.sh"))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// fakeDaemon is a binary named like the real one that sleeps on serve,
// succeeds on pause and answers config get http.port from the config it
// would read.
func fakeDaemon(t *testing.T) string {
	t.Helper()
	return fakeDaemonNamed(t, brand.StateDir())
}

func fakeDaemonNamed(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	script := fmt.Sprintf(`#!/bin/sh
case "$1" in
serve) exec sleep 300 ;;
pause) exit 0 ;;
config)
	f="$XDG_CONFIG_HOME/%s/config.toml"
	if [ -f "$f" ]; then sed -n '/^\[http\]/,/^\[/s/^port = \([0-9]*\)$/\1/p' "$f"; else echo 0; fi
	exit 0 ;;
esac
exit 3
`, brand.StateDir())
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// shortDir is a temporary directory short enough for the daemon's socket
// path under it.
func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "iso")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func isolated(t *testing.T, args ...string) (string, error) {
	t.Helper()
	return isolatedEnv(t, nil, args...)
}

// isolatedEnv runs the script with env added to the test's environment.
func isolatedEnv(t *testing.T, env []string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("bash", append([]string{isolatedScript(t)}, args...)...)
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func refusedWith(t *testing.T, out string, err error, want string) {
	t.Helper()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 || !strings.Contains(out, want) {
		t.Errorf("%v %q, want a refusal naming %q", err, out, want)
	}
}

func mustIsolated(t *testing.T, args ...string) string {
	t.Helper()
	out, err := isolated(t, args...)
	if err != nil {
		t.Fatalf("%v: %v\n%s", args, err, out)
	}
	return out
}

// setupIsolated sets up <parent>/<name> and stops its daemon at cleanup.
func setupIsolated(t *testing.T, parent, name string, index int, flags ...string) string {
	t.Helper()
	dir := filepath.Join(parent, name)
	mustIsolated(t, append([]string{"setup", dir, fakeDaemon(t), isolatedDM, strconv.Itoa(index)}, flags...)...)
	t.Cleanup(func() { isolated(t, "stop", dir) })
	return dir
}

func daemonPid(t *testing.T, dir string) int {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "daemon.pid"))
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.SplitN(string(raw), "\n", 2)[0])
	if err != nil {
		t.Fatal(err)
	}
	return pid
}

func alive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

func waitGone(t *testing.T, pid int) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if !alive(pid) {
			return
		}
	}
	t.Fatalf("pid %d still runs", pid)
}

func TestIsolatedConfigDisablesAutomation(t *testing.T) {
	dir := setupIsolated(t, shortDir(t), "acceptance", 2)
	file := filepath.Join(dir, "config", brand.StateDir(), "config.toml")
	c, err := config.LoadFile(file, cli.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	if c.Brief.Enabled {
		t.Error("brief on")
	}
	if c.Turns.PerUnitPerDay != 1 {
		t.Errorf("turns.per_unit_per_day %d, want the lowest that is a limit, 1", c.Turns.PerUnitPerDay)
	}
	if c.PluginEnabled("github") || c.PluginEnabled("slack") {
		t.Errorf("plugins github %v slack %v", c.PluginEnabled("github"), c.PluginEnabled("slack"))
	}
	if c.HTTP.Port != 47912 || c.MCP.Port != 47812 {
		t.Errorf("ports http %d mcp %d", c.HTTP.Port, c.MCP.Port)
	}
	manifests, err := filepath.Glob(filepath.Join("..", "..", "profiles", "default", "workflows", "*", "workflow.toml"))
	if err != nil || len(manifests) == 0 {
		t.Fatalf("no workflow manifests: %v", err)
	}
	for _, m := range manifests {
		name := filepath.Base(filepath.Dir(m))
		w, ok := c.Workflows[name]
		if !ok || w.Enabled == nil || *w.Enabled {
			t.Errorf("workflow %s is not disabled: %+v", name, w)
		}
	}
	if len(c.Workflows) != len(manifests) {
		t.Errorf("%d workflows configured, %d in the profile", len(c.Workflows), len(manifests))
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Slack struct {
			KeychainService string   `toml:"keychain_service"`
			PostAllowlist   []string `toml:"post_allowlist"`
			RequestLog      bool     `toml:"request_log"`
		} `toml:"slack"`
	}
	if err := toml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Slack.KeychainService != "wloop-e2e-slack" || !slices.Equal(doc.Slack.PostAllowlist, []string{isolatedDM}) || !doc.Slack.RequestLog {
		t.Errorf("slack %+v", doc.Slack)
	}
	for _, sub := range []string{"state", "config", "gh", "bin", "home"} {
		if fi, err := os.Stat(filepath.Join(dir, sub)); err != nil || fi.Mode().Perm() != 0o700 {
			t.Errorf("%s: %v %v", sub, fi, err)
		}
	}
	if out, err := isolated(t, "setup", dir, fakeDaemon(t), isolatedDM); err == nil {
		t.Errorf("a second setup on the same directory: %s", out)
	}
	withSlack := setupIsolated(t, shortDir(t), "s", 3, "--slack")
	if c, err := config.LoadFile(filepath.Join(withSlack, "config", brand.StateDir(), "config.toml"), cli.Defaults()); err != nil || !c.PluginEnabled("slack") || c.PluginEnabled("github") {
		t.Errorf("--slack: %v", err)
	}
}

func TestIsolatedSetupRefusesAConfigTheBinaryIgnores(t *testing.T) {
	dir := filepath.Join(shortDir(t), "acceptance")
	out, err := isolated(t, "setup", dir, fakeDaemonNamed(t, "other"), isolatedDM)
	refusedWith(t, out, err, "does not read")
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("the refused setup left %s: %v", dir, err)
	}
	out, err = isolatedEnv(t, []string{"ISOLATED_STATE_NAME=" + brand.StateDir()}, "setup", dir, fakeDaemonNamed(t, "other"), isolatedDM)
	if err != nil {
		t.Fatalf("with ISOLATED_STATE_NAME: %v %s", err, out)
	}
}

func TestIsolatedEnvStubsOrcaAndGitHub(t *testing.T) {
	dir := setupIsolated(t, shortDir(t), "acceptance", 0)
	for _, c := range [][]string{{"orca", "worktree", "list"}, {"gh", "auth", "token"}, {"claude", "-p", "hi"}, {"codex", "exec", "hi"}, {"launchctl", "print", "x"}} {
		out, err := isolated(t, append([]string{"run", dir}, c...)...)
		refusedWith(t, out, err, "refused")
	}
	leaked := []string{"ORCA_TERMINAL_HANDLE=term-1", "CLAUDE_CONFIG_DIR=/nowhere", "ORCA_AGENT_HOOK_TOKEN=x", "CLAUDE_CODE_SESSION_ID=x", "CLAUDECODE=1", "GITHUB_TOKEN=x", "CODEX_HOME=/nowhere"}
	out, err := isolatedEnv(t, leaked, "run", dir, "sh", "-c", `test ! -e "$ORCA_USER_DATA_PATH" && echo "$GH_TOKEN $GH_CONFIG_DIR $XDG_STATE_HOME $XDG_CONFIG_HOME $GIT_CONFIG_NOSYSTEM $GIT_TERMINAL_PROMPT $LOOP_USER_HOME|$ORCA_TERMINAL_HANDLE$CLAUDE_CONFIG_DIR$ORCA_AGENT_HOOK_TOKEN$CLAUDE_CODE_SESSION_ID$CLAUDECODE$GITHUB_TOKEN$CODEX_HOME|"`)
	if err != nil {
		t.Fatal(err, out)
	}
	want := "isolated-invalid " + filepath.Join(dir, "gh") + " " + filepath.Join(dir, "state") + " " + filepath.Join(dir, "config") + " 1 0 " + filepath.Join(dir, "home") + "||\n"
	if out != want {
		t.Errorf("environment %q, want %q", out, want)
	}
	for _, sub := range []string{"install", "uninstall", "app"} {
		for _, bin := range []string{brand.StateDir(), filepath.Join(dir, "bin", brand.StateDir())} {
			out, err := isolated(t, "run", dir, bin, sub)
			refusedWith(t, out, err, "never install")
		}
	}
	agent := setupIsolated(t, shortDir(t), "agent", 1, "--agent")
	if _, err := os.Stat(filepath.Join(agent, "bin", "claude")); !os.IsNotExist(err) {
		t.Errorf("--agent still stubs claude: %v", err)
	}
	if _, err := os.Stat(filepath.Join(agent, "bin", "codex")); err != nil {
		t.Errorf("--agent dropped the codex stub: %v", err)
	}
	if ents, err := os.ReadDir(filepath.Join(dir, "gh")); err != nil || len(ents) != 0 {
		t.Errorf("GH_CONFIG_DIR %v %v", ents, err)
	}
	if exports, err := isolatedEnv(t, leaked, "env", dir); err != nil || !strings.Contains(exports, "export ORCA_USER_DATA_PATH=") || !strings.Contains(exports, "export GH_TOKEN=isolated-invalid") || !strings.Contains(exports, "unset ORCA_TERMINAL_HANDLE\n") {
		t.Errorf("env:\n%s", exports)
	}
}

func TestIsolatedStopKillsOnlyItsPid(t *testing.T) {
	dir := setupIsolated(t, shortDir(t), "acceptance", 0)
	other := exec.Command("sleep", "300")
	if err := other.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { other.Process.Kill(); other.Wait() })

	mustIsolated(t, "pause", dir)
	if _, err := os.Stat(filepath.Join(dir, "daemon.pid")); !os.IsNotExist(err) {
		t.Errorf("pause left a daemon: %v", err)
	}
	mustIsolated(t, "start", dir)
	pid := daemonPid(t, dir)
	if !alive(pid) {
		t.Fatal("the daemon is not running")
	}
	if out, err := isolated(t, "start", dir); err == nil {
		t.Errorf("a second start of the same directory: %s", out)
	}
	mustIsolated(t, "stop", dir)
	waitGone(t, pid)
	if !alive(other.Process.Pid) {
		t.Fatal("stop killed a process it did not start")
	}

	stale := strconv.Itoa(other.Process.Pid) + "\nMon Jan  1 00:00:00 2001\n"
	if err := os.WriteFile(filepath.Join(dir, "daemon.pid"), []byte(stale), 0o600); err != nil {
		t.Fatal(err)
	}
	mustIsolated(t, "stop", dir)
	if !alive(other.Process.Pid) {
		t.Fatal("stop signaled a reused pid")
	}
}

func TestIsolatedSocketLockIsExclusive(t *testing.T) {
	parent := shortDir(t)
	a := setupIsolated(t, parent, "a", 0, "--slack")
	b := setupIsolated(t, parent, "b", 1, "--slack")
	plain := setupIsolated(t, parent, "c", 2)
	mustIsolated(t, "start", a)
	for _, args := range [][]string{{"start", b}, {"pause", b}} {
		out, err := isolated(t, args...)
		refusedWith(t, out, err, "socket is held")
	}
	if _, err := os.Stat(filepath.Join(b, "daemon.pid")); !os.IsNotExist(err) {
		t.Errorf("the refused daemon has a pid file: %v", err)
	}
	mustIsolated(t, "start", plain)
	mustIsolated(t, "stop", plain)
	mustIsolated(t, "stop", a)
	if _, err := os.Stat(filepath.Join(parent, "slack-socket.lock")); !os.IsNotExist(err) {
		t.Errorf("stop left the lock: %v", err)
	}
	mustIsolated(t, "start", b)
	mustIsolated(t, "stop", b)
}

// walStore writes a WAL store with a meta table holding rows through the
// sqlite3 shell, and leaves it as a stopped daemon would: no -shm, and with
// wal also a -wal holding the rows' frames.
func walStore(t *testing.T, rows map[string]string, wal bool) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "live.db")
	shell := exec.Command("sqlite3", path)
	in, err := shell.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := shell.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := shell.Start(); err != nil {
		t.Fatal(err)
	}
	script := "PRAGMA journal_mode = WAL;\nPRAGMA wal_autocheckpoint = 0;\nCREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);\n"
	for k, v := range rows {
		script += fmt.Sprintf("INSERT INTO meta VALUES ('%s', '%s');\n", k, v)
	}
	if _, err := io.WriteString(in, script+".print written\n"); err != nil {
		t.Fatal(err)
	}
	sc := bufio.NewScanner(out)
	for sc.Scan() && sc.Text() != "written" {
	}
	stopped := path
	if wal {
		stopped = filepath.Join(t.TempDir(), "store.db")
		for _, ext := range []string{"", "-wal"} {
			raw, err := os.ReadFile(path + ext)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(stopped+ext, raw, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	in.Close()
	if err := shell.Wait(); err != nil {
		t.Fatal(err)
	}
	return stopped
}

func metaCount(t *testing.T, path string) int {
	t.Helper()
	out, err := exec.Command("sqlite3", path, "SELECT count(*) FROM meta").Output()
	if err != nil {
		t.Fatal(err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestIsolatedSnapshotOfStoppedWALStore(t *testing.T) {
	src := walStore(t, map[string]string{"viewer": "octo", "timings_since": "x"}, true)
	before := map[string][]byte{}
	mtimes := map[string]time.Time{}
	for _, f := range []string{src, src + "-wal"} {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		fi, _ := os.Stat(f)
		before[f], mtimes[f] = raw, fi.ModTime()
	}
	dir := setupIsolated(t, shortDir(t), "m1", 0)
	mustIsolated(t, "snapshot", dir, src)
	dest := filepath.Join(dir, "state", brand.StateDir(), "store.db")
	if got := metaCount(t, dest); got != 3 {
		t.Errorf("the copy holds %d meta rows, want the 2 of the source and its viewer:github", got)
	}
	if fi, err := os.Stat(dest); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("copy mode %v %v", fi, err)
	}
	if _, err := os.Stat(src + "-shm"); !os.IsNotExist(err) {
		t.Errorf("the snapshot created the source's -shm: %v", err)
	}
	for f, raw := range before {
		got, err := os.ReadFile(f)
		fi, _ := os.Stat(f)
		if err != nil || !bytes.Equal(got, raw) || !fi.ModTime().Equal(mtimes[f]) {
			t.Errorf("the snapshot changed %s", filepath.Base(f))
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "copied")); err != nil {
		t.Errorf("no copied marker: %v", err)
	}

	bare := setupIsolated(t, shortDir(t), "m2", 1)
	out, err := isolated(t, "snapshot", bare, walStore(t, map[string]string{"timings_since": "x"}, false))
	refusedWith(t, out, err, "no stored GitHub viewer")
	if _, err := os.Stat(filepath.Join(bare, "state", brand.StateDir(), "store.db")); !os.IsNotExist(err) {
		t.Errorf("a refused snapshot left its copy: %v", err)
	}
}

func TestIsolatedCopiedStoreRefusesJobs(t *testing.T) {
	dir := setupIsolated(t, shortDir(t), "m1", 0)
	mustIsolated(t, "snapshot", dir, walStore(t, map[string]string{"viewer:github": "octo"}, false))
	for _, args := range [][]string{{"start", dir}, {"start", dir, "--mcp-port", "47899"}, {"run", dir, brand.StateDir(), "serve"}} {
		out, err := isolated(t, args...)
		refusedWith(t, out, err, "only with --no-jobs")
	}
	mustIsolated(t, "start", dir, "--no-jobs")
	mustIsolated(t, "stop", dir)
	mustIsolated(t, "clean", dir)
	if _, err := os.Stat(filepath.Join(dir, "copied")); !os.IsNotExist(err) {
		t.Errorf("clean left the copied marker: %v", err)
	}
}

func TestIsolatedRunRefusesTheRealStore(t *testing.T) {
	dir := setupIsolated(t, shortDir(t), "acceptance", 0)
	home := t.TempDir()
	xdg := []string{"XDG_STATE_HOME=" + filepath.Join(home, "state"), "XDG_CONFIG_HOME=" + filepath.Join(home, "config")}
	real := filepath.Join(home, "state", brand.StateDir(), "store.db")
	for _, args := range [][]string{
		{"run", dir, "sh", "-c", "true", real},
		{"run", dir, brand.StateDir(), "serve", "--store=" + real},
		{"run", dir, "cat", filepath.Join(home, "config", brand.StateDir(), "config.toml")},
		{"snapshot", dir, "/nonexistent"},
	} {
		out, err := isolatedEnv(t, xdg, args...)
		if args[0] == "snapshot" {
			refusedWith(t, out, err, "no store")
			continue
		}
		refusedWith(t, out, err, "the user's real")
	}
	out, err := isolatedEnv(t, []string{"HOME=" + home, "XDG_STATE_HOME=", "XDG_CONFIG_HOME=relative"}, "run", dir, "sh", "-c", "true", filepath.Join(home, ".local", "state", brand.StateDir()))
	refusedWith(t, out, err, "the user's real")
	out, err = isolatedEnv(t, xdg, "setup", filepath.Join(home, "state", brand.StateDir(), "x"), fakeDaemon(t), isolatedDM)
	refusedWith(t, out, err, "the user's real")
}

func TestIsolatedSetupRefusesALongSocketPath(t *testing.T) {
	dir := filepath.Join(shortDir(t), strings.Repeat("x", 100))
	out, err := isolated(t, "setup", dir, fakeDaemon(t), isolatedDM)
	if err == nil || !strings.Contains(out, "over a unix socket") {
		t.Fatalf("%v %s", err, out)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("the refused setup left %s: %v", dir, err)
	}
}

func TestIsolatedSetupWritesTheMCPKeyAndExposesIt(t *testing.T) {
	dir := setupIsolated(t, shortDir(t), "keyed", 2)
	path := filepath.Join(dir, "state", brand.StateDir(), "mcp.key")
	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("key file %v %v", fi, err)
	}
	raw, _ := os.ReadFile(path)
	key := strings.TrimSpace(string(raw))
	if len(key) < 20 {
		t.Fatalf("key %q", key)
	}
	out, err := isolated(t, "run", dir, "sh", "-c", `printf %s "$ISOLATED_MCP_KEY"`)
	if err != nil || out != key {
		t.Fatalf("run: %q %v", out, err)
	}
	if exports := mustIsolated(t, "env", dir); !strings.Contains(exports, "export ISOLATED_MCP_KEY="+key+"\n") {
		t.Fatalf("env:\n%s", exports)
	}
	mustIsolated(t, "start", dir)
	if log, _ := os.ReadFile(filepath.Join(dir, "daemon.log")); strings.Contains(string(log), key) {
		t.Fatal("the key reached the daemon log")
	}
}
