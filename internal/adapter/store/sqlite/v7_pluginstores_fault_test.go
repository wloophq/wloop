package sqlite

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"
)

// v7DropFaults are the store directories whose plugin tables' drop fails.
var v7DropFaults sync.Map

func init() {
	v7BeforeDrop = func(dir string) error {
		if _, ok := v7DropFaults.Load(resolved(dir)); ok {
			return errors.New("an injected fault before the drop")
		}
		return nil
	}
}

// failDropIn makes the drop of the plugin tables fail for the store whose
// directory is dir, until healDrop.
func failDropIn(t *testing.T, dir string) {
	t.Helper()
	v7DropFaults.Store(resolved(dir), true)
	t.Cleanup(func() { healDrop(dir) })
}

func healDrop(dir string) { v7DropFaults.Delete(resolved(dir)) }

func resolved(dir string) string {
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		return r
	}
	return dir
}
