package tui

import (
	"os"
	"slices"
	"testing"
	"time"
)

// enter and leave need a real terminal (MakeRaw, the console mode) and are
// covered by the manual smoke; read and its recovery run on a pipe.

func TestReadDecodesWhatTheTerminalSends(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	tty := &TTY{in: r, keys: make(chan Key, 16)}
	go tty.read()
	w.Write([]byte("\x1b[Aq"))
	w.Close()
	var got []Key
	for k := range tty.Keys() {
		got = append(got, k)
	}
	if !slices.Equal(withoutReads(got), []Key{{Name: KeyUp}, {Rune: 'q'}}) || tty.Err() != nil {
		t.Fatalf("%+v %v", got, tty.Err())
	}
}

func TestReadSurvivesAPanicInTheDecoder(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	keys := make(chan Key)
	close(keys)
	tty := &TTY{in: r, keys: keys}
	ended := make(chan struct{})
	go func() {
		tty.read()
		close(ended)
	}()
	w.Write([]byte("a"))
	time.Sleep(20 * time.Millisecond)
	w.Write([]byte("b"))
	select {
	case <-ended:
	case <-time.After(2 * time.Second):
		t.Fatal("read stayed blocked after the decoder died")
	}
	if tty.Err() == nil {
		t.Fatal("the panic was not reported")
	}
}
