package mcp

import (
	"crypto/subtle"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
)

var errKey = api.MCPKeyHeader + " is missing or stale: run `" + brand.Command + " install` and start the session again"

func keyMatches(want, got string) bool {
	return want != "" && subtle.ConstantTimeCompare([]byte(want), []byte(got)) == 1
}
