package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/config"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/profile"
	"github.com/wloophq/wloop/internal/turn"
)

func TestServeLocalePicksConfigThenDetected(t *testing.T) {
	avail := []string{"en", "pt-BR"}
	for _, c := range []struct {
		cfg      string
		detected []string
		want     string
	}{
		{"", []string{"pt-PT"}, "pt-BR"},
		{"", []string{"fr-FR", "en-GB"}, "en"},
		{"", nil, "en"},
		{"en", []string{"pt-BR"}, "en"},
		{"fr", []string{"pt-BR"}, "fr"},
	} {
		if got := serveLocale(config.Config{UI: config.UI{Locale: c.cfg}}, c.detected, avail); got != c.want {
			t.Errorf("%q %q: %q, want %q", c.cfg, c.detected, got, c.want)
		}
	}
}

func TestALocaleChangedInTheConfigFileReloadsTheRegistry(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.toml")
	write := func(body string, at time.Time) {
		if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(file, at, at); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Unix(1000, 0)
	write(`ui = {locale = "en"}`, now)
	load := func() (config.Config, error) { return config.LoadFile(file, Defaults()) }
	cfg, err := load()
	if err != nil {
		t.Fatal(err)
	}
	h := config.NewHolder(cfg, config.HolderOptions{Path: file, Load: load, Now: func() time.Time { return now }})
	layers := func() []profile.Layer { return profile.Chain(h.Config()) }
	c, err := turn.NewCache(layers, intentload.CacheOptions{Locale: func() string {
		return serveLocale(h.Config(), []string{"en-US"}, intentload.Locales(layers(), turn.BundledFS()))
	}})
	if err != nil {
		t.Fatal(err)
	}
	if l := c.Get(); l.Locale != "en" {
		t.Fatalf("%q", l.Locale)
	}
	now = now.Add(config.StatEvery)
	write(`ui = {locale = "pt-BR"}`, now)
	if l := c.Get(); l.Locale != "pt-BR" || h.Warning().Key != "" {
		t.Fatalf("%q, warning %q", l.Locale, h.Warning())
	}
}
