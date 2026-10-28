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

// Prepare does nothing without process groups.
func Prepare(cmd *exec.Cmd) { cmd.WaitDelay = grace }

// Adopt does nothing without process groups.
func Adopt(int) (release func()) { return func() {} }
