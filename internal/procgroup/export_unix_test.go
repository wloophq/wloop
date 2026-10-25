//go:build unix

package procgroup

import "time"

func reset() {
	mu.Lock()
	defer mu.Unlock()
	clear(live)
	record, killed = "", time.Time{}
}
