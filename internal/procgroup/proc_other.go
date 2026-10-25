//go:build !unix

package procgroup

func group(*Cmd) {}

// Run runs the child; without process groups, cancellation kills the child alone.
func (c *Cmd) Run() error { return c.Cmd.Run() }

// KillAll does nothing without process groups.
func KillAll() {}
