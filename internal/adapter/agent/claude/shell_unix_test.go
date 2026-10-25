//go:build unix

package claude

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestCancelledCallLeavesNoGrandchild(t *testing.T) {
	for name, sh := range map[string]ShellFunc{"Exec": Exec, "Shell": Shell} {
		t.Run(name, func(t *testing.T) {
			pidFile := filepath.Join(t.TempDir(), "pid")
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() {
				_, _ = sh(ctx, "", nil, "", "/bin/sh", "-c", `sleep 60 >/dev/null 2>&1 & echo $! > "$1"; wait`, "sh", pidFile)
				close(done)
			}()
			pid := readPid(t, pidFile)
			cancel()
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Fatal("the call did not return")
			}
			deadline := time.Now().Add(2 * time.Second)
			for !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
				if time.Now().After(deadline) {
					_ = syscall.Kill(pid, syscall.SIGKILL)
					t.Fatalf("grandchild %d outlived the cancelled call", pid)
				}
				time.Sleep(10 * time.Millisecond)
			}
		})
	}
}

func readPid(t *testing.T, path string) int {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		raw, err := os.ReadFile(path)
		if s := strings.TrimSpace(string(raw)); err == nil && s != "" {
			pid, err := strconv.Atoi(s)
			if err != nil {
				t.Fatal(err)
			}
			return pid
		}
	}
	t.Fatal("the child wrote no pid")
	return 0
}
