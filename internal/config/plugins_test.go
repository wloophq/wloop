package config

import (
	"encoding/json"
	"slices"
	"testing"
)

func TestPluginEnabled(t *testing.T) {
	off, on := false, true
	c := Config{Plugins: Plugins{GitHub: GitHub{Plugin: Plugin{Enabled: &off}}, Slack: Slack{Plugin: Plugin{Enabled: &on}}, Other: map[string]Plugin{"other": {}, "wiki": {Enabled: &off}}}}
	for id, want := range map[string]bool{"github": false, "slack": true, "other": true, "wiki": false, "absent": true} {
		if got := c.PluginEnabled(id); got != want {
			t.Errorf("%s: %v, want %v", id, got, want)
		}
	}
	if !(Config{}).PluginEnabled("github") {
		t.Error("a config with no plugins disables github")
	}
}

func TestPluginsDecode(t *testing.T) {
	var c Config
	if err := json.Unmarshal([]byte(`{"plugins": {"github": {"enabled": false}, "slack": {}}}`), &c); err != nil {
		t.Fatal(err)
	}
	if c.PluginEnabled("github") || !c.PluginEnabled("slack") {
		t.Errorf("github %v, slack %v", c.GitHub.Enabled, c.Slack.Enabled)
	}
}

func TestPluginSwitchesAreTheNamespacesWithEnabled(t *testing.T) {
	got := Config{Plugins: Plugins{Other: map[string]Plugin{"wiki": {}}}}.PluginSwitches()
	slices.Sort(got)
	if want := []string{"github", "slack", "wiki"}; !slices.Equal(got, want) {
		t.Errorf("%v, want %v", got, want)
	}
}
