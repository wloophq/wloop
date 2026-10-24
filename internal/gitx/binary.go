package gitx

import (
	"context"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

// xcrunShim is the git macOS ships in /usr/bin: a stub that asks xcrun for
// the active developer directory on every run, which costs about 8 ms a
// spawn on top of git itself.
const xcrunShim = "/usr/bin/git"

var developerGit = sync.OnceValue(func() string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "xcrun", "--find", "git").Output()
	if err != nil {
		return ""
	}
	path := strings.TrimSpace(string(out))
	if _, err := exec.LookPath(path); err != nil {
		return ""
	}
	return path
})

// Binary is the git to spawn: the one PATH names, except that the macOS
// xcrun shim is replaced by the git it would run.
func Binary() string {
	if runtime.GOOS != "darwin" {
		return "git"
	}
	if path, err := exec.LookPath("git"); err != nil || path != xcrunShim {
		return "git"
	}
	if git := developerGit(); git != "" {
		return git
	}
	return "git"
}
