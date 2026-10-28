package ghsource_test

import "testing"

func TestThePluginBinaryServesTheCloneCommand(t *testing.T) {
	t.Skip("X8a1: plugin.toml declares clone, which only ghsource.Local serves; the out-of-process binary's RunCLI must serve it too, or a round's clone fails once the plugin runs")
}
