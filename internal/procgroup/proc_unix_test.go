//go:build unix

package procgroup

import (
	"bytes"
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

const grace = 200 * time.Millisecond

// family starts a shell that forks a grandchild ignoring SIGTERM, writes
// both pids to a file and waits; it returns the command and the two pids.
func family(t *testing.T, ctx context.Context) (*Cmd, chan error, []int) {
	t.Helper()
	pids := filepath.Join(t.TempDir(), "pids")
	script := `(trap '' TERM; exec sleep 60) & echo $$ $! > "$1"; wait`
	cmd := Command(ctx, grace, "/bin/sh", "-c", script, "sh", pids)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	done := make(chan error, 1)
	go func() { done <- cmd.Run() }()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(pids)
		if fields := strings.Fields(string(raw)); err == nil && len(fields) == 2 {
			var got []int
			for _, f := range fields {
				n, err := strconv.Atoi(f)
				if err != nil {
					t.Fatal(err)
				}
				got = append(got, n)
			}
			return cmd, done, got
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the child wrote no pids: %s", out.String())
	return nil, nil, nil
}

func gone(t *testing.T, pids []int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for _, pid := range pids {
		for !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
			if time.Now().After(deadline) {
				_ = syscall.Kill(pid, syscall.SIGKILL)
				t.Fatalf("process %d survived", pid)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func wait(t *testing.T, done chan error) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return")
	}
}

func TestCancelStopsTheWholeGroup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	_, done, pids := family(t, ctx)
	cancel()
	wait(t, done)
	gone(t, pids)
}

func TestKillAllStopsRunningGroups(t *testing.T) {
	_, done, pids := family(t, context.Background())
	KillAll()
	wait(t, done)
	gone(t, pids)
}

func TestRunReportsTheChildsExit(t *testing.T) {
	if err := Command(context.Background(), grace, "/bin/sh", "-c", "exit 3").Run(); err == nil {
		t.Fatal("want the exit status as an error")
	}
	if err := Command(context.Background(), grace, "/bin/sh", "-c", "exit 0").Run(); err != nil {
		t.Fatal(err)
	}
}
