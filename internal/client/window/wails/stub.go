//go:build !darwin || !purego

package wails

import (
	"github.com/wloophq/wloop/internal/client/texts"
	"github.com/wloophq/wloop/internal/client/window"
)

// Available reports whether this binary can host the window: only a darwin
// build with the purego tag can.
func Available() bool { return false }

// Run needs a darwin build with the purego tag.
func Run(string, *texts.Catalog, func(window.Host)) { panic("the window needs macOS and the purego build tag") }
