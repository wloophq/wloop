package orca

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"testing"
	"time"
)

var liveReadMethods = []string{"status.get", "repo.list", "worktree.list", "worktree.ps", "terminal.list"}

// liveRead is the only door the live test has to a running Orca: it refuses every
// method that is not a read before it touches the metadata or the socket.
func liveRead(ctx context.Context, r *Runtime, method string, params any) ([]byte, error) {
	if !slices.Contains(liveReadMethods, method) {
		return nil, fmt.Errorf("live test refuses %q: only %v", method, liveReadMethods)
	}
	return r.Call(ctx, method, params)
}

func TestLiveReadRefusesEveryOtherMethod(t *testing.T) {
	f := startFakeRuntime(t, answerOK(`{}`))
	r := &Runtime{UserDataPath: f.dir}
	for _, method := range []string{"", "worktree.set", "worktree.create", "worktree.rm", "worktree.resolvePrBase",
		"repo.add", "terminal.send", "terminal.create", "terminal.close", "terminal.wait", "terminal.read", "status.set"} {
		if _, err := liveRead(context.Background(), r, method, nil); err == nil {
			t.Errorf("%q: not refused", method)
		}
	}
	if n := len(f.seen()); n != 0 {
		t.Fatalf("refused methods reached the runtime %d times", n)
	}
	if _, err := liveRead(context.Background(), r, "status.get", nil); err != nil {
		t.Fatalf("status.get: %v", err)
	}
	if seen := f.seen(); len(seen) != 1 || seen[0].method() != "status.get" {
		t.Fatalf("seen = %v", seen)
	}
	want := []string{"repo.list", "status.get", "terminal.list", "worktree.list", "worktree.ps"}
	got := slices.Sorted(slices.Values(liveReadMethods))
	if !slices.Equal(got, want) {
		t.Fatalf("live methods = %v, want %v", got, want)
	}
}

// LOOP_LIVE_ORCA=1 go test ./internal/workspace/orca -run TestLiveRuntimeReads
func TestLiveRuntimeReads(t *testing.T) {
	if os.Getenv(LiveVar) != "1" {
		t.Skip("set LOOP_LIVE_ORCA=1 to read from the running Orca")
	}
	r := &Runtime{}
	meta, err := readRuntimeMetadata(r.userDataPath())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	params := map[string]any{
		"status.get":    nil,
		"repo.list":     nil,
		"worktree.list": map[string]any{"limit": 1000},
		"worktree.ps":   map[string]any{"limit": 1000},
		"terminal.list": map[string]any{"includeVisualLayouts": false},
	}
	for _, method := range liveReadMethods {
		start := time.Now()
		raw, err := liveRead(ctx, r, method, params[method])
		d := time.Since(start)
		if err != nil {
			t.Fatalf("%s: %v", method, err)
		}
		var f struct {
			OK   bool `json:"ok"`
			Meta struct {
				RuntimeID string `json:"runtimeId"`
			} `json:"_meta"`
		}
		if err := json.Unmarshal(raw, &f); err != nil || !f.OK || f.Meta.RuntimeID != meta.RuntimeID {
			t.Fatalf("%s: ok=%v runtimeId=%q want %q err=%v", method, f.OK, f.Meta.RuntimeID, meta.RuntimeID, err)
		}
		t.Logf("%s: %s, %d bytes", method, d, len(raw))
	}
	for method, limit := range map[string]time.Duration{"worktree.ps": 25 * time.Millisecond, "status.get": 2 * time.Millisecond} {
		var ds []time.Duration
		for range 5 {
			start := time.Now()
			if _, err := liveRead(ctx, r, method, params[method]); err != nil {
				t.Fatalf("%s: %v", method, err)
			}
			ds = append(ds, time.Since(start))
		}
		slices.Sort(ds)
		t.Logf("%s: median %s over %v", method, ds[2], ds)
		if ds[2] >= limit {
			t.Errorf("%s: median %s, want under %s", method, ds[2], limit)
		}
	}
}
