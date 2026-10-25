package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlacementRuleIsValidated(t *testing.T) {
	for _, c := range []struct{ rules, want string }{
		{`[{"match": "slack:*"}, {"match": "slack:[C1]*"}]`, "placement[1]: match"},
		{`[{"match": "slack:*", "checkout": "work/api"}]`, "placement[0]: checkout"},
		{`[{"match": ""}]`, "placement[0]: match is empty"},
	} {
		file := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(file, []byte(`{"placement": `+c.rules+`}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadFile(file, tools()); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", c.rules, err, c.want)
		}
	}
	file := filepath.Join(t.TempDir(), "config.json")
	ok := `{"placement": [{"match": "slack:T000/C001/*", "repo": "acme/api", "checkout": "main", "role": "work", "shared": true}, {"match": "*", "checkout": "/abs/path"}]}`
	if err := os.WriteFile(file, []byte(ok), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadFile(file, tools())
	if err != nil {
		t.Fatal(err)
	}
	m := c.PlacementMap()
	if len(m.Rules) != 2 || m.Rules[0].Repo != "acme/api" || !m.Rules[0].Shared || m.Rules[1].Checkout != "/abs/path" {
		t.Fatalf("map = %+v", m)
	}
}
