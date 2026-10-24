package config

import (
	"encoding/json"
	"testing"
)

func TestPluginEnabled(t *testing.T) {
	off, on := false, true
	c := Config{Plugins: map[string]Plugin{"github": {Enabled: &off}, "slack": {Enabled: &on}, "other": {}}}
	for id, want := range map[string]bool{"github": false, "slack": true, "other": true, "absent": true} {
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
		t.Errorf("%+v", c.Plugins)
	}
}
