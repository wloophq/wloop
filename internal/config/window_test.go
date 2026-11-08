package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigWindowDefaults(t *testing.T) {
	if got := Defaults().Window; got != (Window{Enabled: true, Hotkey: "ctrl+option+space"}) {
		t.Fatalf("default %+v", got)
	}
	for raw, want := range map[string]Window{
		``:                            {Enabled: true, Hotkey: "ctrl+option+space"},
		`window = {hotkey = ""}`:      {Enabled: true, Hotkey: ""},
		`window = {enabled = false}`:  {Enabled: false, Hotkey: "ctrl+option+space"},
		`window = {hotkey = "cmd+k"}`: {Enabled: true, Hotkey: "cmd+k"},
	} {
		path := filepath.Join(t.TempDir(), "config.toml")
		if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		c, err := LoadFile(path, tools())
		if err != nil || c.Window != want {
			t.Errorf("%s: %+v %v, want %+v", raw, c.Window, err, want)
		}
	}
}
