package archtest

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

const coreProfile = "profiles/default"

// ruleProfileProvider flags each key of the core profile's TOML file that is a
// plugin's id: a plugin's own words and settings live in its layer,
// plugins/<id>/profile, which the profile reads only while the plugin is on.
func ruleProfileProvider(file string, src []byte, plugins []string) []string {
	var doc map[string]any
	if err := toml.Unmarshal(src, &doc); err != nil {
		return []string{fmt.Sprintf("%s: %v", file, err)}
	}
	var out []string
	var walk func(path string, v any)
	walk = func(path string, v any) {
		switch v := v.(type) {
		case map[string]any:
			for k, child := range v {
				at := strings.TrimPrefix(path+"."+k, ".")
				if slices.Contains(plugins, k) {
					out = append(out, fmt.Sprintf("%s: %s keys the %s plugin; put it in plugins/%s/profile (R-profileprovider)", file, at, k, k))
				}
				walk(at, child)
			}
		case []any:
			for i, child := range v {
				walk(fmt.Sprintf("%s[%d]", path, i), child)
			}
		}
	}
	walk("", doc)
	slices.Sort(out)
	return out
}

// TestTheCoreProfileKeysNoPlugin checks that no key of the core profile's
// TOML files is a plugin's id.
func TestTheCoreProfileKeysNoPlugin(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..", "..")
	err := filepath.WalkDir(filepath.Join(root, coreProfile), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(p) != ".toml" {
			return err
		}
		src, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		for _, b := range ruleProfileProvider(filepath.ToSlash(rel), src, pluginIDs()) {
			t.Error(b)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
