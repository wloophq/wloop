package web

import (
	"bytes"
	"context"
	"os/exec"
	"sync"
	"syscall"
	"testing"
	"time"
)

// headlessChrome is the path of a Chrome the layout tests drive, "" when none is installed.
func headlessChrome() string {
	for _, p := range []string{"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome", "google-chrome", "chromium"} {
		if path, err := exec.LookPath(p); err == nil {
			return path
		}
	}
	return ""
}

// dumpedHTML says a --dump-dom command finished: it writes the page's DOM in one piece.
func dumpedHTML(stdout, _ []byte) bool { return bytes.Contains(stdout, []byte("</html>")) }

// runChrome runs a headless Chrome with args until done, given what it has written to stdout and stderr, says its
// command finished, then kills it and returns its stdout. On a loaded machine Chrome's teardown outlasts its
// command by many seconds, or ends in exit status 2 when its teardown watchdog fires, so the command's output, not
// the process's exit, ends the run.
func runChrome(t *testing.T, done func(stdout, stderr []byte) bool, args ...string) []byte {
	t.Helper()
	chrome := headlessChrome()
	if chrome == "" {
		t.Skip("no Chrome to lay the page out")
	}
	return runUntil(t, done, chrome, args...)
}

// runUntil runs name with args in a process group of its own until done says its command finished, then kills the
// group and returns what it wrote to stdout; it fails t when the process exits, or a minute passes, first.
func runUntil(t *testing.T, done func(stdout, stderr []byte) bool, name string, args ...string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	out := &runOutput{done: done, finished: make(chan struct{})}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout, cmd.Stderr = out.writer(&out.stdout), out.writer(&out.stderr)
	// Chrome's helpers write into the profile under t.TempDir; killing the whole group lets the directory go.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = time.Second
	if err := cmd.Start(); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	var err error
	select {
	case <-out.finished:
		cancel()
		<-exited
	case err = <-exited:
	}
	stdout, stderr, finished := out.result()
	if !finished {
		t.Fatalf("%s: %v, before its command finished: %s", name, err, tail(stderr))
	}
	return stdout
}

// runOutput collects what a run writes and closes finished once done says its command finished.
type runOutput struct {
	mu             sync.Mutex
	stdout, stderr bytes.Buffer
	done           func(stdout, stderr []byte) bool
	finished       chan struct{}
	closed         bool
}

func (o *runOutput) writer(buf *bytes.Buffer) writerFunc {
	return func(p []byte) (int, error) {
		o.mu.Lock()
		defer o.mu.Unlock()
		buf.Write(p)
		if !o.closed && o.done(o.stdout.Bytes(), o.stderr.Bytes()) {
			o.closed = true
			close(o.finished)
		}
		return len(p), nil
	}
}

func (o *runOutput) result() (stdout, stderr []byte, finished bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return bytes.Clone(o.stdout.Bytes()), bytes.Clone(o.stderr.Bytes()), o.closed
}

type writerFunc func(p []byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

func tail(b []byte) []byte {
	const n = 2000
	if len(b) > n {
		return b[len(b)-n:]
	}
	return b
}
