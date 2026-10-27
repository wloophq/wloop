package mcp

import (
	"sync"

	"github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/turn"
)

// bundledTexts are the tools' texts of the bundled profile, as the loader
// reads them.
var bundledTexts = sync.OnceValue(func() map[string]ToolText {
	tools, problems := intent.BundledTools(turn.BundledFS())
	if len(problems) > 0 {
		panic(problems.Error())
	}
	out := map[string]ToolText{}
	for name, t := range tools {
		out[name] = ToolText{Description: t.Description, Params: t.Params, Answers: t.Answers}
	}
	return out
})
