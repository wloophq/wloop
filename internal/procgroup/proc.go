// Package procgroup runs child processes that may fork, such as an agent's
// CLI, so that stopping the child stops what it started.
package procgroup

import "time"

// grace is how long a group has after SIGTERM before SIGKILL.
var grace = 5 * time.Second
