//go:build !unix

package procgroup

import "os/exec"

// Run runs cmd; without process groups, cancellation kills the child alone.
func Run(cmd *exec.Cmd) error {
	cmd.WaitDelay = grace
	return cmd.Run()
}

// KillAll does nothing without process groups.
func KillAll() {}

// Track does nothing without process groups.
func Track(string) error { return nil }
