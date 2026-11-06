// Package evalout guards where an eval command writes what real calls returned.
package evalout

import (
	"fmt"
	"os"
	"path/filepath"
)

// OutsideRepository refuses path, the value of --flagName, when it is empty or inside a git checkout, so real
// content never lands where it could be committed.
func OutsideRepository(flagName, path string) error {
	if path == "" {
		return fmt.Errorf("--%s is required", flagName)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	for dir, rest := abs, ""; ; dir, rest = filepath.Dir(dir), filepath.Join(filepath.Base(dir), rest) {
		if real, err := filepath.EvalSymlinks(dir); err == nil {
			abs = filepath.Join(real, rest)
			break
		}
		if filepath.Dir(dir) == dir {
			break
		}
	}
	for dir := abs; ; dir = filepath.Dir(dir) {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return fmt.Errorf("--%s %s is inside the git checkout %s; write eval outputs outside any repository", flagName, abs, dir)
		}
		if filepath.Dir(dir) == dir {
			return nil
		}
	}
}
