package eval

import _ "embed"

//go:embed blind.md
var blindTemplate string

// BlindPrompt asks for the viewer's briefing from the tool's output alone.
func BlindPrompt(base map[string]string, toolOutput string) string {
	return fill(blindTemplate, with(base, "TOOL_OUTPUT", toolOutput))
}
