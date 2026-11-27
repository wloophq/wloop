package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigWindowDefaults(t *testing.T) {
	def := Window{Enabled: true, Hotkey: "ctrl+option+space", Float: FloatOff, FloatHotkey: "cmd+option+.", FloatMorning: "09:00"}
	if got := Defaults().Window; got != def {
		t.Fatalf("default %+v", got)
	}
	with := func(f func(*Window)) Window { w := def; f(&w); return w }
	for raw, want := range map[string]Window{
		``:                                          def,
		`window = {hotkey = ""}`:                    with(func(w *Window) { w.Hotkey = "" }),
		`window = {enabled = false}`:                with(func(w *Window) { w.Enabled = false }),
		`window = {hotkey = "cmd+k"}`:               with(func(w *Window) { w.Hotkey = "cmd+k" }),
		`window = {float = "on"}`:                   with(func(w *Window) { w.Float = FloatOn }),
		`window = {float_hotkey = "cmd+j"}`:         with(func(w *Window) { w.FloatHotkey = "cmd+j" }),
		`window = {float_morning = "07:30"}`:        with(func(w *Window) { w.FloatMorning = "07:30" }),
		`window = {float_over_full_screen = true}`:  with(func(w *Window) { w.FloatOverFullScreen = true }),
		`window = {float_filter = "source:github"}`: with(func(w *Window) { w.FloatFilter = "source:github" }),
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

func TestConfigWindowFloatRefusesBadValues(t *testing.T) {
	for _, raw := range []string{`window = {float = "yes"}`, `window = {float_morning = "9am"}`, `window = {float_morning = ""}`} {
		path := filepath.Join(t.TempDir(), "config.toml")
		if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadFile(path, tools()); err == nil {
			t.Errorf("%s loaded", raw)
		}
	}
}
