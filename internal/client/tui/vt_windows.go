package tui

import (
	"os"

	"golang.org/x/sys/windows"
)

// enableVT turns on escape sequence processing for a Windows console, which
// a legacy console leaves off, and returns how to put the mode back.
func enableVT(out *os.File) (func() error, error) {
	h := windows.Handle(out.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(h, &mode); err != nil {
		return nil, err
	}
	if err := windows.SetConsoleMode(h, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING); err != nil {
		return nil, err
	}
	return func() error { return windows.SetConsoleMode(h, mode) }, nil
}
