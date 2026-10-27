package mcp

import (
	"os"
	"sync"

	"github.com/pelletier/go-toml/v2"
)

// bundledTexts are the tools' texts of the default profile, as the daemon
// serves them.
var bundledTexts = sync.OnceValue(func() map[string]ToolText {
	src, err := os.ReadFile("../../../profiles/default/tools.toml")
	if err != nil {
		panic(err)
	}
	var doc map[string]struct {
		Description string            `toml:"description"`
		Params      map[string]string `toml:"params"`
		Answers     map[string]string `toml:"answers"`
	}
	if err := toml.Unmarshal(src, &doc); err != nil {
		panic(err)
	}
	out := map[string]ToolText{}
	for name, t := range doc {
		out[name] = ToolText{Description: t.Description, Params: t.Params, Answers: t.Answers}
	}
	return out
})
