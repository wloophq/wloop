package archtest

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/wloophq/wloop/internal/plugins/bundled"
)

const webStatic = "internal/web/static"

// ruleWebProvider flags each line of a cockpit source that quotes a
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
	ids, err := bundledIDs()
	if err != nil {
		panic(err)
	}
	return ids
}

// bundledIDs are the ids of the bundled plugins' manifests, sorted.
var bundledIDs = sync.OnceValues(func() ([]string, error) {
	ms, err := bundled.Manifests()
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(ms))
	for i, m := range ms {
		ids[i] = m.ID
	}
	slices.Sort(ids)
	return ids, nil
})

// TestCockpitScriptsNameNoPlugin checks that the cockpit's modules and
// components quote no plugin's id.
func TestCockpitScriptsNameNoPlugin(t *testing.T) {
	for file, src := range cockpitSources(t) {
		for _, b := range ruleWebProvider(file, src, pluginIDs()) {
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
