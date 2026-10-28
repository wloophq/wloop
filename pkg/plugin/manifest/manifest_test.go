package manifest

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/pkg/plugin"
)

const acme = `
id = "acme"
kinds = ["source"]
protocol = 1

[source]
slot = "acme"
keys = ["acme/api#1"]
`

func TestManifestValidates(t *testing.T) {
	t.Run("a valid manifest loads with its kind tables", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, File), []byte(acme), 0o600); err != nil {
			t.Fatal(err)
		}
		m, err := Load(dir)
		if err != nil {
			t.Fatal(err)
		}
		if m.ID != "acme" || m.Protocol != plugin.Protocol || !slices.Equal(m.Kinds, []plugin.Kind{"source"}) || m.Dir != dir {
			t.Errorf("manifest %+v", m)
		}
		var src struct {
			Slot string   `toml:"slot"`
			Keys []string `toml:"keys"`
		}
		if err := m.Decode("source", &src); err != nil {
			t.Fatal(err)
		}
		if src.Slot != "acme" || !slices.Equal(src.Keys, []string{"acme/api#1"}) {
			t.Errorf("source table %+v", src)
		}
		var narrow struct {
			Slot string `toml:"slot"`
		}
		if err := m.Decode("source", &narrow); err == nil || !strings.Contains(err.Error(), "keys") {
			t.Errorf("a key the kind does not read decoded: %v", err)
		}
		if err := m.Decode("agent", &narrow); err == nil {
			t.Error("a kind the manifest does not declare decoded")
		}
	})
	t.Run("a missing manifest names its path", func(t *testing.T) {
		dir := t.TempDir()
		if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), filepath.Join(dir, File)) {
			t.Errorf("Load = %v", err)
		}
	})
	for _, c := range []struct{ name, src, want string }{
		{"not TOML", `id = `, "plugin.toml"},
		{"no id", "kinds = [\"source\"]\nprotocol = 1\n[source]\n", "id"},
		{"a bad id", "id = \"Acme Corp\"\nkinds = [\"source\"]\nprotocol = 1\n[source]\n", "id"},
		{"no protocol", "id = \"acme\"\nkinds = [\"source\"]\n[source]\n", "protocol"},
		{"a protocol this SDK does not speak", "id = \"acme\"\nkinds = [\"source\"]\nprotocol = 9\n[source]\n", "protocol 9"},
		{"no kinds", "id = \"acme\"\nprotocol = 1\n", "kinds"},
		{"a repeated kind", "id = \"acme\"\nkinds = [\"source\", \"source\"]\nprotocol = 1\n[source]\n", "twice"},
		{"a bad kind name", "id = \"acme\"\nkinds = [\"Source\"]\nprotocol = 1\n[Source]\n", "Source"},
		{"a kind without its table", "id = \"acme\"\nkinds = [\"source\"]\nprotocol = 1\n", "[source]"},
		{"a table for no declared kind", "id = \"acme\"\nkinds = [\"source\"]\nprotocol = 1\n[source]\n[agent]\n", "[agent]"},
		{"an unknown top-level key", "id = \"acme\"\nkinds = [\"source\"]\nprotocol = 1\nversion = \"2\"\n[source]\n", "version"},
		{"a kind that is not a table", "id = \"acme\"\nkinds = [\"source\"]\nprotocol = 1\nsource = 3\n", "[source]"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, err := Parse([]byte(c.src)); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("Parse = %v, want an error naming %q", err, c.want)
			}
		})
	}
}
