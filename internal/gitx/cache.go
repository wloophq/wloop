package gitx

import "sync"

const cacheLimit = 4096

// Cache remembers ancestry answers, which never change for a pair of
// commits git knows, across the sweeps of one process.
type Cache struct {
	mu       sync.Mutex
	ancestry map[ancestryKey]bool
}

type ancestryKey struct{ common, ancestor, commit string }

func (c *Cache) ancestor(k ancestryKey) (answer, ok bool) {
	if c == nil {
		return false, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	answer, ok = c.ancestry[k]
	return answer, ok
}

func (c *Cache) remember(k ancestryKey, answer bool) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ancestry == nil || len(c.ancestry) >= cacheLimit {
		c.ancestry = map[ancestryKey]bool{}
	}
	c.ancestry[k] = answer
}
