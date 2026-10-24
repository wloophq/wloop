// Package profiles embeds the bundled profile, the lowest layer of every
// profile: the judgment blocks and the intents' manifests.
package profiles

import "embed"

//go:embed default/intent=*.md default/intents default/workflows
var Default embed.FS
