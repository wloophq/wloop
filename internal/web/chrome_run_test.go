package web

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestARunEndsWhenItsCommandFinishesThoughTheProcessLingers stands a shell in for a Chrome whose teardown, on a
// loaded runner, outlasts its dump or ends in exit status 2: the run returns the dump either way, and the helper
// the process left running dies with it.
func TestARunEndsWhenItsCommandFinishesThoughTheProcessLingers(t *testing.T) {
	pid := filepath.Join(t.TempDir(), "helper.pid")
	for _, c := range []struct{ name, script string }{
		{"a teardown that hangs", `sleep 600 & echo $! > ` + pid + `; echo '<html>dumped</html>'; wait`},
		{"a teardown the watchdog ends", `echo '<html>dumped</html>'; exit 2`},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := runUntil(t, dumpedHTML, "sh", "-c", c.script)
			if string(got) != "<html>dumped</html>\n" {
				t.Errorf("stdout %q, want the dump", got)
			}
		})
	}
	b, err := os.ReadFile(pid)
	if err != nil {
		t.Fatal(err)
	}
	helper, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(10 * time.Second); !errors.Is(syscall.Kill(helper, 0), syscall.ESRCH); {
		if time.Now().After(deadline) {
			t.Fatalf("the helper %d outlived the run", helper)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
