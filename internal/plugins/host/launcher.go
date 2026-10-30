package host

import (
	"context"
	"os/exec"
	"sync"

	"github.com/wloophq/wloop/internal/procgroup"
	"github.com/wloophq/wloop/pkg/plugin"
)

// ExecLauncher starts plugin binaries as children of the daemon, each the
// leader of a process group procgroup lists, so a daemon that exits, or
// the next one after a crash, stops what they started.
type ExecLauncher struct{}

var _ plugin.Launcher = ExecLauncher{}

// Launch starts spec's binary and completes the handshake, checking its
// checksum when spec has one.
func (ExecLauncher) Launch(_ context.Context, spec plugin.Spec) (plugin.Process, error) {
	cmd := exec.Command(spec.Path)
	procgroup.Prepare(cmd)
	p, err := plugin.Exec(spec, cmd)
	if cmd.Process == nil {
		return p, err
	}
	release := procgroup.Adopt(cmd.Process.Pid)
	if err != nil {
		release()
		return nil, err
	}
	return &execProcess{Process: p, pid: cmd.Process.Pid, release: release}, nil
}

// execProcess unlists its group once it ended.
type execProcess struct {
	plugin.Process
	pid     int
	release func()
	once    sync.Once
}

// Pid is the process's id.
func (p *execProcess) Pid() int { return p.pid }

func (p *execProcess) Exited() bool {
	if !p.Process.Exited() {
		return false
	}
	p.once.Do(p.release)
	return true
}

func (p *execProcess) Kill() {
	p.Process.Kill()
	p.once.Do(p.release)
}
