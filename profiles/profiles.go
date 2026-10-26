// Package profiles embeds the bundled profile, the lowest layer of every
// profile: the judgment blocks, the intents, the workflows, the partials and
// the prompts.
package profiles

import "embed"

//go:embed default
var Default embed.FS
