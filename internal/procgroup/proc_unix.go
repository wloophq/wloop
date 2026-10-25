//go:build unix

package procgroup

import (
	"errors"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

var running sync.Map

func group(c *Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error {
		err := syscall.Kill(-c.Process.Pid, syscall.SIGTERM)
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return err
	}
}

// Run starts the child and waits for it, and on cancellation for the rest
// of its group too.
func (c *Cmd) Run() error {
	var cancelled atomic.Pointer[time.Time]
	if cancel := c.Cancel; cancel != nil {
		c.Cancel = func() error {
			now := time.Now()
			cancelled.Store(&now)
			return cancel()
		}
	}
	if err := c.Start(); err != nil {
		return err
	}
	pgid := c.Process.Pid
	running.Store(pgid, struct{}{})
	defer running.Delete(pgid)
	err := c.Wait()
	if at := cancelled.Load(); at != nil {
		reap(pgid, at.Add(c.grace))
	}
	return err
}

// reap waits until deadline for the group to empty and kills what is left.
func reap(pgid int, deadline time.Time) {
	for time.Now().Before(deadline) {
		if errors.Is(syscall.Kill(-pgid, 0), syscall.ESRCH) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
}

// KillAll sends SIGKILL to the group of every Run still waiting, for a
// process about to exit before its children do.
func KillAll() {
	running.Range(func(pgid, _ any) bool {
		_ = syscall.Kill(-pgid.(int), syscall.SIGKILL)
		return true
	})
}
