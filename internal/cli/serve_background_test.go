package cli

import (
	"context"
	"runtime"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/core/lock"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

// cliGoroutines are the stacks, by goroutine header, of the goroutines
// running this package's code.
func cliGoroutines() map[string]string {
	buf := make([]byte, 1<<22)
	buf = buf[:runtime.Stack(buf, true)]
	out := map[string]string{}
	for g := range strings.SplitSeq(string(buf), "\n\n") {
		header, _, _ := strings.Cut(g, " [")
		for line := range strings.SplitSeq(g, "\n") {
			if strings.Contains(line, "/internal/cli/") && !strings.Contains(line, "_test.go:") {
				out[header] = g
				break
			}
		}
	}
	return out
}

// Not parallel: it reads every goroutine of the process.
func TestStopServingWaitsForServesJobs(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	before := cliGoroutines()
	te.serveDeps(context.Background(), te.db, te.cfg, t.Logf, &lock.Mutex{}, nil)
	te.serveDeps(context.Background(), te.db, te.cfg, t.Logf, &lock.Mutex{}, nil)
	started := 0
	for header, g := range cliGoroutines() {
		if _, ok := before[header]; ok {
			continue
		}
		started++
		if !strings.Contains(g, "sync.(*WaitGroup).Go.func1") {
			t.Errorf("serve started a job it does not wait for:\n%s", g)
		}
	}
	if started == 0 {
		t.Fatal("serve started no job")
	}
	te.stopServing()
	for header, g := range cliGoroutines() {
		if _, ok := before[header]; !ok {
			t.Errorf("a job serve started still runs:\n%s", g)
		}
	}
}
