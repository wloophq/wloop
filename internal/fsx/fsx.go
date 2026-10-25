// Package fsx holds file system helpers the adapters and the CLI share.
package fsx

import "os"

// SameFile reports whether a and b both exist and name one file, through
// any symlinks.
func SameFile(a, b string) bool {
	fa, errA := os.Stat(a)
	fb, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(fa, fb)
}
