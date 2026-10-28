package github

import "github.com/wloophq/wloop/internal/ghcli"

// CLI is gh on the PATH, each call counted as one spawned GitHub sample.
func CLI() ghcli.Runner { return ghcli.Metered(ghcli.New()) }
