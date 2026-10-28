package config

import (
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"

	"github.com/wloophq/wloop/internal/intent/builtin"
)

var tomlBlock = regexp.MustCompile("(?s)```toml\n(.*?)```")

// TestTheDocsWriteNoRenamedConfigKey reads every toml block of the docs
// site and fails on a key the configuration renamed, which would not load
// if copied.
func TestTheDocsWriteNoRenamedConfigKey(t *testing.T) {
	root := filepath.Join("..", "..", "docs", "src", "content", "docs")
	n := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") || strings.Contains(path, "upgrade.md") {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range tomlBlock.FindAllStringSubmatch(string(raw), -1) {
			var tree map[string]any
			if toml.Unmarshal([]byte(m[1]), &tree) != nil {
				continue
			}
			n++
			for _, k := range keyPaths("", tree) {
				if to := builtin.KeyRenamedTo(k); to != "" {
					t.Errorf("%s writes %s, which is %s now", path, k, to)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("no toml block read")
	}
}

func keyPaths(at string, m map[string]any) []string {
	var out []string
	for _, k := range slices.Sorted(maps.Keys(m)) {
		p := k
		if at != "" {
			p = at + "." + k
		}
		out = append(out, p)
		if sub, ok := m[k].(map[string]any); ok {
			out = append(out, keyPaths(p, sub)...)
		}
	}
	return out
}
