package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wloophq/wloop/internal/brand"
)

func TestResolveProfile(t *testing.T) {
	cfgHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgHome)
	home, _ := os.UserHomeDir()
	for in, want := range map[string]string{
		"":                            "",
		"personal":                    filepath.Join(cfgHome, brand.StateDir(), "profiles", "personal"),
		"./mine":                      filepath.Join(cfgHome, brand.StateDir(), "mine"),
		"~/p/" + brand.Name + "-prof": filepath.Join(home, "p", brand.Name+"-prof"),
		"/abs/profile":                "/abs/profile",
	} {
		if got := ResolveProfile(in); got != want {
			t.Errorf("ResolveProfile(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLoadResolvesProfilesAndAgentHome(t *testing.T) {
	cfgHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgHome)
	file := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(file, []byte(`profile = "personal"
agent_home = "~/.claude-personal"

[[projects]]
match = "acme/*"
agent_home = "/h"
`), 0o600)
	c, err := LoadFile(file, tools())
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(cfgHome, brand.StateDir(), "profiles", "personal"); c.Profile != want {
		t.Fatalf("profile %q", c.Profile)
	}
	if p := c.For("acme", "api"); p.AgentHome != "/h" || p.Profile != c.Profile {
		t.Fatalf("project %+v", p)
	}
}
