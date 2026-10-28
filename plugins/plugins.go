// Package plugins holds the plugins the release bundles, one directory each.
// Its own code only embeds their manifests, which the host reads whether or
// not a plugin's binary is installed.
package plugins

import "embed"

// Manifests holds <id>/plugin.toml of every bundled plugin.
//
//go:embed */plugin.toml
var Manifests embed.FS
