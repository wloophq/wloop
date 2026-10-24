package tui

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/term"

	"github.com/wloophq/wloop/internal/brand"
)

// The alternate screen, a hidden cursor and bracketed paste, so a paste
// arrives as text and never as keys.
const (
	enterScreen = "\x1b[?1049h\x1b[?25l\x1b[?2004h"
	leaveScreen = "\x1b[?2004l\x1b[?25h\x1b[?1049l"
)

var ErrNotTerminal = errors.New(brand.Command + " tui needs a terminal on stdin and stdout")

// TTY is the process's terminal in raw mode on the alternate screen.
type TTY struct {
	in, out *os.File
	old     *term.State
	restore func() error
	keys    chan Key
	failed  atomic.Value // error: the reader panicked
	// gate holds the reader off the terminal while paused, so an editor the
	// TUI runs reads every key typed into it.
	gate   sync.Mutex
	paused bool
}

func Open() (*TTY, error) {
	in, out := os.Stdin, os.Stdout
	if !term.IsTerminal(int(in.Fd())) || !term.IsTerminal(int(out.Fd())) {
		return nil, ErrNotTerminal
	}
	t := &TTY{in: in, out: out, keys: make(chan Key, 16)}
	if err := t.enter(); err != nil {
		return nil, err
	}
	go t.read()
	return t, nil
}

func (t *TTY) enter() error {
	old, err := term.MakeRaw(int(t.in.Fd()))
	if err != nil {
		return err
	}
	// The mode to restore is the one before the first enter: after a
	// failed leave the terminal may still be raw.
	if t.old == nil {
		t.old = old
	}
	if t.restore, err = enableVT(t.out); err != nil {
		return errors.Join(err, term.Restore(int(t.in.Fd()), t.old))
	}
	if _, err := t.out.WriteString(enterScreen); err != nil {
		return errors.Join(err, t.leave())
	}
	return nil
}

func (t *TTY) leave() error {
	_, err := t.out.WriteString(leaveScreen)
	return errors.Join(err, t.restore(), term.Restore(int(t.in.Fd()), t.old))
}

const (
	// escWait is how long a lone ESC waits for the rest of a sequence
	// before it counts as the Esc key.
	escWait = 50 * time.Millisecond
	// pasteIdle is how long a paste may pause before the rest of it is
	// dropped instead of read as keys.
	pasteIdle = 2 * time.Second
	// pollWait is how long the reader waits for input before it looks
	// again whether it is paused.
	pollWait = 100 * time.Millisecond
)

// read ends the keys on a panic too, so Run returns and the terminal is
// restored; Err says what happened.
func (t *TTY) read() {
	chunks := make(chan []byte)
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		defer t.recover()
		pump(chunks, t.keys, escWait, pasteIdle)
	}()
	defer t.recover()
	defer close(chunks)
	buf := make([]byte, 256)
	for {
		ready, err := readable(t.in, pollWait)
		if err != nil {
			return
		}
		if !ready {
			continue
		}
		t.gate.Lock()
		if t.paused {
			t.gate.Unlock()
			time.Sleep(pollWait / 5)
			continue
		}
		n, err := t.in.Read(buf)
		t.gate.Unlock()
		if n > 0 {
			select {
			case chunks <- bytes.Clone(buf[:n]):
			case <-stopped:
				return
			}
		}
		if err != nil {
			return
		}
	}
}

func (t *TTY) Keys() <-chan Key { return t.keys }

// pause holds the reader off the terminal, or lets it read again.
func (t *TTY) pause(on bool) {
	t.gate.Lock()
	t.paused = on
	t.gate.Unlock()
}

func (t *TTY) Size() (int, int) {
	w, h, err := term.GetSize(int(t.out.Fd()))
	if err != nil || w <= 0 || h <= 0 {
		return 80, 24
	}
	return w, h
}

func (t *TTY) Draw(lines []string) error {
	var b strings.Builder
	b.WriteString("\x1b[H")
	for i, l := range lines {
		if i > 0 {
			b.WriteString("\r\n")
		}
		b.WriteString(l)
		b.WriteString("\x1b[K")
	}
	b.WriteString("\x1b[J")
	_, err := t.out.WriteString(b.String())
	return err
}

func (t *TTY) recover() {
	if p := recover(); p != nil {
		t.failed.CompareAndSwap(nil, fmt.Errorf(brand.Command+" tui: reading the terminal: %v", p))
	}
}

// Err is why the keys ended early, nil when they did not.
func (t *TTY) Err() error {
	err, _ := t.failed.Load().(error)
	return err
}

// Close leaves the alternate screen and restores the terminal's mode.
func (t *TTY) Close() error { return t.leave() }
