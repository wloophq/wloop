package ghsource_test

import "testing"

func TestThePluginBinaryServesTheCloneCommand(t *testing.T) {
	t.Skip("X8a1: plugin.toml declares clone, auth, reviewed-repos and teams, which only ghsource.Local serves; the out-of-process binary's RunCLI must serve them too, or a round's clone and doctor's and init's checks fail once the plugin runs")
}
