package cli

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/client/menubar"
)

func fastUpgradePoll(t *testing.T) {
	t.Helper()
	defer func(d time.Duration) { t.Cleanup(func() { upgradePoll = d }) }(upgradePoll)
	upgradePoll = 10 * time.Millisecond
}

func writeBinary(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path+".new", []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path+".new", path); err != nil {
		t.Fatal(err)
	}
}

func fired(ch <-chan struct{}, within time.Duration) bool {
	select {
	case <-ch:
		return true
	case <-time.After(within):
		return false
	}
}

func TestWatchBinaryFiresOnAReplacedBinary(t *testing.T) {
	fastUpgradePoll(t)
	path := filepath.Join(t.TempDir(), brand.Name)
	writeBinary(t, path, "v1")
	ch := watchBinary(t.Context(), path)
	if fired(ch, 100*time.Millisecond) {
		t.Fatal("fired on an unchanged binary")
	}
	writeBinary(t, path, "v2 build")
	if !fired(ch, 2*time.Second) {
		t.Fatal("did not fire on a replaced binary")
	}
}

func TestWatchBinaryIgnoresAMissingPath(t *testing.T) {
	fastUpgradePoll(t)
	path := filepath.Join(t.TempDir(), brand.Name)
	ch := watchBinary(t.Context(), path)
	writeBinary(t, path, "v1")
	if fired(ch, 100*time.Millisecond) {
		t.Fatal("fired for a path that did not exist at the start")
	}
}

func TestServeRestartsInPlaceWhenTheBinaryIsReplaced(t *testing.T) {
	fastUpgradePoll(t)
	te := newServeEnv(t)
	te.Executable = filepath.Join(t.TempDir(), brand.StateDir())
	writeBinary(t, te.Executable, "v1")
	var reexecs atomic.Int32
	te.Reexec = func() error { reexecs.Add(1); return nil }
	done := make(chan error, 1)
	go func() {
		done <- runServe(context.Background(), te.Env, []string{"--no-jobs", "--mcp-port", strconv.Itoa(freePort(t))})
	}()
	waitHealth(t, te.Env, "the daemon", func(api.Health) bool { return true })
	writeBinary(t, te.Executable, "v2 build")
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serve kept running the replaced binary")
	}
	if n := reexecs.Load(); n != 1 {
		t.Fatalf("reexec ran %d times", n)
	}
}

func appOnBundle(t *testing.T) (*testEnv, string) {
	t.Helper()
	fastUpgradePoll(t)
	te, _ := bundleEnv(t)
	source := te.Executable
	mustBuild(t, te)
	te.Executable = bundleExe(te.Home)
	te.NativeAvailable = func() bool { return true }
	return te, source
}

// runAppUntil runs the app, replaces its source once the menu is up, and
// waits for done.
func runAppUntil(t *testing.T, te *testEnv, source string, done <-chan struct{}) {
	t.Helper()
	te.QuitNative = func() {
		select {
		case <-done:
		default:
			t.Error("quit through AppKit, which exits 0 and is not restarted")
		}
	}
	te.RunNative = func(setup func(menubar.Tray, menubar.Notifier)) {
		setup(&recTray{}, silentNotifier{})
		writeBinary(t, source, "binary v3")
		<-done
	}
	go func() { _ = runApp(t.Context(), te.Env, nil) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the app kept running after its source was replaced")
	}
}

func TestAppExitsToBeRestartedWhenItsSourceIsReplaced(t *testing.T) {
	te, source := appOnBundle(t)
	done := make(chan struct{})
	te.Reexec = func() error { t.Error("exec under a running AppKit"); return nil }
	te.Exit = func(code int) {
		if code != exitRebuilt {
			t.Errorf("exit %d, want %d", code, exitRebuilt)
		}
		close(done)
	}
	runAppUntil(t, te, source, done)
}
