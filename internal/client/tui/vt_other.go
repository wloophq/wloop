//go:build !windows

package tui

import "os"

func enableVT(*os.File) (func() error, error) { return func() error { return nil }, nil }
