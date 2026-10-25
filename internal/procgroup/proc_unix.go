//go:build unix

package procgroup

import (
	"errors"
	"maps"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

var (
	mu     sync.Mutex
	live   = map[int]struct{}{}
	record string
	// killed is when KillAll last ran: a Run that started before it but
	// added its group after missed it.
	killed time.Time
)

// Run runs cmd, made by exec.Command or exec.CommandContext, as the leader
// of its own process group. Cancelling cmd's context sends the group
// SIGTERM. Once the child exits, whatever is left of its group gets SIGTERM
// and, after the grace, SIGKILL.
func Run(cmd *exec.Cmd) error {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.WaitDelay = grace
	var cancelled atomic.Pointer[time.Time]
	if cmd.Cancel != nil {
		cmd.Cancel = func() error {
			now := time.Now()
			cancelled.Store(&now)
			return signal(cmd.Process.Pid, syscall.SIGTERM)
		}
	}
	started := time.Now()
	if err := cmd.Start(); err != nil {
		return err
	}
	pgid := cmd.Process.Pid
	add(pgid, started)
	defer remove(pgid)
	err := cmd.Wait()
	deadline := time.Now().Add(grace)
	if at := cancelled.Load(); at != nil {
		deadline = at.Add(grace)
	} else {
		_ = signal(pgid, syscall.SIGTERM)
	}
	for exists(pgid) {
		if !time.Now().Before(deadline) {
			_ = signal(pgid, syscall.SIGKILL)
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	return err
}

func signal(pgid int, sig syscall.Signal) error {
	err := syscall.Kill(-pgid, sig)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

func exists(pgid int) bool { return !errors.Is(syscall.Kill(-pgid, 0), syscall.ESRCH) }

// KillAll sends SIGKILL to every group a Run is waiting on, for a process
// about to exit. A group that starts after stays listed for Track.
func KillAll() {
	mu.Lock()
	defer mu.Unlock()
	killed = time.Now()
	for pgid := range live {
		_ = signal(pgid, syscall.SIGKILL)
	}
}

// Track kills the groups a previous process listed at path, which a crash
// left running, and from then on keeps path listing the live groups. The
// caller holds the lock that makes it the only such process.
func Track(path string) error {
	mu.Lock()
	defer mu.Unlock()
	raw, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for f := range strings.FieldsSeq(string(raw)) {
		pgid, err := strconv.Atoi(f)
		if _, ours := live[pgid]; err == nil && pgid > 1 && !ours {
			_ = signal(pgid, syscall.SIGKILL)
		}
	}
	record = path
	return write()
}

func add(pgid int, started time.Time) {
	mu.Lock()
	defer mu.Unlock()
	live[pgid] = struct{}{}
	if started.Before(killed) {
		_ = signal(pgid, syscall.SIGKILL)
	}
	_ = write()
}

func remove(pgid int) {
	mu.Lock()
	defer mu.Unlock()
	delete(live, pgid)
	_ = write()
}

func write() error {
	if record == "" {
		return nil
	}
	var b strings.Builder
	for _, pgid := range slices.Sorted(maps.Keys(live)) {
		b.WriteString(strconv.Itoa(pgid))
		b.WriteByte('\n')
	}
	tmp := record + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, record)
}
