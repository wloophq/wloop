package daemon

import "os"

// stateDirMode keeps the state directory, and the older files in it whose
// own modes are wider, to the user.
const stateDirMode = 0o700

// ensureStateDir creates dir at stateDirMode or tightens it to that mode.
func ensureStateDir(dir string) error {
	if err := os.MkdirAll(dir, stateDirMode); err != nil {
		return err
	}
	return os.Chmod(dir, stateDirMode)
}
