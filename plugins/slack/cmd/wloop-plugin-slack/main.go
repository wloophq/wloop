// Command wloop-plugin-slack is the Slack source plugin's binary, which the
// host runs as a child process.
package main

import (
	sdk "github.com/wloophq/wloop/pkg/plugin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/slack"
)

func main() {
	sdk.Serve(map[sdk.Kind]sdk.Impl{src.Kind: src.New(&slack.Plugin{})})
}
