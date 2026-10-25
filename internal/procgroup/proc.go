// Package procgroup runs child processes that may fork, such as an agent's CLI,
// so that stopping the child stops what it started.
package procgroup

import (
	"context"
	"os/exec"
	"time"
)

// Cmd is an exec.Cmd whose Run stops the child's whole process group when
// its context is cancelled or KillAll runs. Use Run: the embedded Output and
// CombinedOutput bypass it.
type Cmd struct {
	*exec.Cmd
	grace time.Duration
}

// Command is exec.CommandContext for a child that may fork. Cancelling ctx
// sends SIGTERM to the child's process group, and SIGKILL to whatever is
// left of it after grace.
func Command(ctx context.Context, grace time.Duration, name string, args ...string) *Cmd {
	c := &Cmd{Cmd: exec.CommandContext(ctx, name, args...), grace: grace}
	c.WaitDelay = grace
	group(c)
	return c
}
