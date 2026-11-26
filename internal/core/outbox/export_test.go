package outbox

// keys counts the keys b holds a read of.
func (b *RecentBases) keys() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.reads)
}
