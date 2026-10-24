//go:build unix

package tui

import (
	"cmp"
	"errors"
	"os"
	"os/exec"
	"time"

	"golang.org/x/sys/unix"

	"github.com/wloophq/wloop/internal/brand"
)

// readable waits up to wait for f to have input; false when it has none yet.
func readable(f *os.File, wait time.Duration) (bool, error) {
	fds := []unix.PollFd{{Fd: int32(f.Fd()), Events: unix.POLLIN}}
	n, err := unix.Poll(fds, int(wait.Milliseconds()))
	if errors.Is(err, unix.EINTR) {
		return false, nil
	}
	return n > 0, err
}

// Edit gives the terminal to $VISUAL, else $EDITOR, else vi, on a file
// holding initial, and returns the file as the editor left it.
func (t *TTY) Edit(initial string) (string, error) {
	f, err := os.CreateTemp("", brand.Command+"-*.md")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	_, err = f.WriteString(initial)
	if err = errors.Join(err, f.Close()); err != nil {
		return "", err
	}
	t.pause(true)
	defer t.pause(false)
	if err := t.leave(); err != nil {
		return "", errors.Join(err, t.enter())
	}
	editor := cmp.Or(os.Getenv("VISUAL"), os.Getenv("EDITOR"), "vi")
	cmd := exec.Command("/bin/sh", "-c", editor+` "$1"`, "sh", f.Name())
	cmd.Stdin, cmd.Stdout, cmd.Stderr = t.in, t.out, t.out
	ran := cmd.Run()
	if err := t.enter(); err != nil {
		return "", errors.Join(ran, err)
	}
	if ran != nil {
		return "", ran
	}
	raw, err := os.ReadFile(f.Name())
	return string(raw), err
}
