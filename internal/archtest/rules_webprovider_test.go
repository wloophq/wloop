package archtest

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

const webStatic = "internal/web/static"

// ruleWebProvider flags each line of a cockpit script that quotes a
// plugin's id: the client reads providers from what the daemon serves and
// never branches on one.
func ruleWebProvider(file, src string, plugins []string) []string {
	quoted := regexp.MustCompile("[\"'`](" + strings.Join(plugins, "|") + ")[\"'`]")
	var out []string
	for i, line := range strings.Split(src, "\n") {
		if m := quoted.FindStringSubmatch(line); m != nil {
			out = append(out, fmt.Sprintf("%s:%d quotes the %s plugin's id; read the provider from the served data (R-webprovider)", file, i+1, m[1]))
		}
	}
	return out
}

func pluginIDs() []string {
	ids := make([]string, 0, len(pluginRoots))
	for id := range pluginRoots {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

func TestCockpitScriptsNameNoPlugin(t *testing.T) {
	dir := filepath.Join("..", "..", webStatic)
	files, err := filepath.Glob(filepath.Join(dir, "*.js"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no scripts under %s: %v", webStatic, err)
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, b := range ruleWebProvider(webStatic+"/"+filepath.Base(f), string(raw), pluginIDs()) {
			t.Error(b)
		}
	}
}

func TestRWebProviderFlagsAPlantedLiteral(t *testing.T) {
	ids := []string{"github", "slack"}
	src := "const a = u.provider === \"github\";\nconst b = { provider: 'slack' };\nconst c = `slack`;\nconst d = \"github.com\";\nconst e = \"session\";\n"
	flagged(t, "quoted ids", ruleWebProvider("x.js", src, ids), 3, "(R-webprovider)")
	flagged(t, "a host is not an id", ruleWebProvider("x.js", `const PR_URL = /^https:\/\/github\.com\//;`, ids), 0, "")
	flagged(t, "the line", ruleWebProvider("x.js", "\n\nif (p === \"slack\") {}", ids), 1, "x.js:3")
}
