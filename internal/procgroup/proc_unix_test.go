//go:build unix

package procgroup

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	grace = 200 * time.Millisecond
	os.Exit(m.Run())
}

func fresh(t *testing.T) {
	t.Helper()
	reset()
	t.Cleanup(reset)
}

// family runs a shell that forks a grandchild ignoring SIGTERM, writes both
// pids to a file and runs tail, which returns the run's error and the pids.
func family(t *testing.T, ctx context.Context, tail string) (chan error, []int) {
	t.Helper()
	pids := filepath.Join(t.TempDir(), "pids")
	script := `(trap '' TERM; exec sleep 60) & echo $$ $! > "$1"; ` + tail
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", script, "sh", pids)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	done := make(chan error, 1)
	go func() { done <- Run(cmd) }()
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
			return done, got
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the child wrote no pids: %s", out.String())
	return nil, nil
}

func gone(t *testing.T, pids ...int) {
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

func wait(t *testing.T, done chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return")
		return nil
	}
}

func TestCancelStopsTheWholeGroup(t *testing.T) {
	fresh(t)
	ctx, cancel := context.WithCancel(context.Background())
	done, pids := family(t, ctx, "wait")
	cancel()
	wait(t, done)
	gone(t, pids...)
}

func TestAnExitedChildLeavesNoGroupBehind(t *testing.T) {
	fresh(t)
	done, pids := family(t, context.Background(), "exit 0")
	wait(t, done)
	gone(t, pids...)
}

func TestKillAllStopsRunningGroups(t *testing.T) {
	fresh(t)
	done, pids := family(t, context.Background(), "wait")
	KillAll()
	wait(t, done)
	gone(t, pids...)
}

func TestAGroupAddedAfterKillAllThatStartedBeforeIsKilled(t *testing.T) {
	fresh(t)
	child := exec.Command("/bin/sh", "-c", "sleep 60")
	child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	started := time.Now()
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { _ = child.Wait() }()
	KillAll()
	add(child.Process.Pid, started)
	defer remove(child.Process.Pid)
	gone(t, child.Process.Pid)
}

func TestARunAfterKillAllRuns(t *testing.T) {
	fresh(t)
	KillAll()
	if err := Run(exec.Command("/bin/sh", "-c", "exit 0")); err != nil {
		t.Fatal(err)
	}
}

func TestRunReportsTheChildsExit(t *testing.T) {
	fresh(t)
	if err := Run(exec.Command("/bin/sh", "-c", "exit 3")); err == nil {
		t.Fatal("want the exit status as an error")
	}
	if err := Run(exec.Command("/bin/sh", "-c", "exit 0")); err != nil {
		t.Fatal(err)
	}
}

func TestTrackKillsTheGroupsAPreviousProcessLeft(t *testing.T) {
	fresh(t)
	left := exec.Command("/bin/sh", "-c", "sleep 60")
	left.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := left.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { _ = left.Wait() }()
	path := filepath.Join(t.TempDir(), "groups")
	if err := os.WriteFile(path, []byte(strconv.Itoa(left.Process.Pid)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Track(path); err != nil {
		t.Fatal(err)
	}
	gone(t, left.Process.Pid)
}

func TestTrackListsTheLiveGroups(t *testing.T) {
	fresh(t)
	path := filepath.Join(t.TempDir(), "groups")
	if err := Track(path); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done, pids := family(t, ctx, "wait")
	listedEventually(t, path, pids[:1])
	if err := Track(path); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(pids[0], 0); err != nil {
		t.Fatalf("a second Track killed this process's own group: %v", err)
	}
	cancel()
	wait(t, done)
	if got := listed(t, path); len(got) != 0 {
		t.Fatalf("listed %v after the run", got)
	}
}

// listedEventually waits for path to list want: the child can write its pids
// before Run, back from Start, has added its group.
func listedEventually(t *testing.T, path string, want []int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		got := listed(t, path)
		if slices.Equal(got, want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("listed %v, want %v", got, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func listed(t *testing.T, path string) []int {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []int
	for _, f := range strings.Fields(string(raw)) {
		n, err := strconv.Atoi(f)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, n)
	}
	return out
}
