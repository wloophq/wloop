//go:build unix

package daemon

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
)

func TestRunKillsTheGroupsACrashLeft(t *testing.T) {
	d := testDeps(t)
	left := exec.Command("/bin/sh", "-c", "sleep 600")
	left.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := left.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() { _ = left.Wait(); close(exited) }()
	t.Cleanup(func() { _ = syscall.Kill(-left.Process.Pid, syscall.SIGKILL) })
	if err := os.MkdirAll(d.StateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d.StateDir, groupsFile), []byte(strconv.Itoa(left.Process.Pid)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stop := runServer(t, New(d))
	waitFor(t, func() bool { _, err := os.Stat(d.InfoPath); return err == nil })
	waitFor(t, func() bool {
		select {
		case <-exited:
			return true
		default:
			return false
		}
	})
	if err := stop(); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
