package present

import "testing"

func TestFailedStepsCacheBounded(t *testing.T) {
	t.Parallel()
	var c stepCache
	for job := int64(1); job <= 600; job++ {
		c.Store(job, stepsOf{steps: []string{"step"}})
	}
	if n := c.Len(); n != 512 {
		t.Fatalf("%d entries, want 512", n)
	}
	if _, ok := c.Load(89); !ok {
		t.Fatal("a recent job was evicted")
	}
	c.Store(601, stepsOf{})
	if _, ok := c.Load(89); !ok {
		t.Fatal("a job read just before the eviction was dropped")
	}
	if _, ok := c.Load(90); ok {
		t.Fatal("the least recently used job survived")
	}
	if _, ok := c.Load(88); ok {
		t.Fatal("the oldest job is still cached")
	}
}
