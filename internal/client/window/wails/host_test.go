//go:build darwin && purego

package wails

import "github.com/wloophq/wloop/internal/client/window"

var _ window.Host = (*host)(nil)
