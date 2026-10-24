// Package brand holds the product name; every user-visible name derives from it.
package brand

// Set with -ldflags -X; every other user-visible name derives from them.
var (
	Name    = "wloop"
	Command = "wloop"
	App     = "wloop"
	// Org is the GitHub organization that hosts the repository and the docs.
	Org = "wloophq"
)

func MCPServer() string { return Name }

func ToolPrefix() string { return "mcp__" + Name + "__" }

// LaunchAgent is the daemon's LaunchAgent label and the app bundle's
// identifier; the menu bar's label adds ".app".
func LaunchAgent() string { return "io.github." + Org + "." + Name }

// StateDir is the leaf name of the state and the config directories.
func StateDir() string { return Name }

func SkillName(verb string) string { return Name + "-" + verb }

// DocsURL is the documentation site's root, with its trailing slash.
func DocsURL() string { return "https://" + Org + ".github.io/" + Name + "/" }
