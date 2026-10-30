// Command orca-stub serves an Orca runtime with no repository, worktree or
// terminal, for scripts/isolated-daemon.sh: its daemon finds a reachable
// workspace manager, so the sweep runs its workflows, and every write a
// session would need is refused.
//
// Usage: orca-stub <user-data-dir>
package main

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/wloophq/wloop/internal/workspace/orca/orcatest"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: orca-stub <user-data-dir>")
		os.Exit(2)
	}
	dir := os.Args[1]
	if err := os.MkdirAll(dir, 0o700); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	sock := filepath.Join(dir, "s")
	_ = os.Remove(sock)
	_, stop, err := orcatest.Serve(dir, sock, orcatest.World{})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	<-sig
	_ = stop()
	os.Remove(filepath.Join(dir, "orca-runtime.json"))
}
