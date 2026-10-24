//go:build !unix

package tui

import (
	"os"
	"time"
)

// readable never waits here: the reader blocks in its read, and the TUI
// runs no editor.
func readable(*os.File, time.Duration) (bool, error) { return true, nil }
