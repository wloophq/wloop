// Package profiles embeds the bundled profile, the lowest layer of every
// profile: the judgment blocks, the intents and the workflows.
package profiles

import "embed"

//go:embed default/intent=*.md default/intents default/workflows
var Default embed.FS
