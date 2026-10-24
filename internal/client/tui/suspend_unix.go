//go:build unix

package tui

import (
	"errors"
	"syscall"
)

// Suspend gives the terminal back as it was, stops the process group like
// a shell's Ctrl-Z, and takes the terminal again once the job resumes.
func (t *TTY) Suspend() error {
	if err := t.leave(); err != nil {
		return errors.Join(err, t.enter())
	}
	if err := syscall.Kill(0, syscall.SIGTSTP); err != nil {
		return errors.Join(err, t.enter())
	}
	return t.enter()
}
