package plugin

import (
	"os"
	"sync"

	"github.com/hashicorp/go-hclog"
)

// stderr is the process's stderr before go-plugin's Serve replaces os.Stderr
// with a pipe; the host parses the log lines it reads there.
var stderr = os.Stderr

var logger = sync.OnceValue(func() hclog.Logger {
	return hclog.New(&hclog.LoggerOptions{Output: stderr, JSONFormat: true, Level: hclog.Trace})
})

// Logger is the plugin's logger. It writes JSON lines to stderr, which the
// host relays to its own log at the host's level; a plugin never writes to
// stdout.
func Logger() hclog.Logger { return logger() }
