//go:build darwin

package main

import "runtime"

// AppKit only works on the process's main thread; the app command runs it.
func init() { runtime.LockOSThread() }
