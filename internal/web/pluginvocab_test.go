package web

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/wloophq/wloop/pkg/plugin/manifest"
)

// sourceDebts are, by cockpit source under ui/src, the words of the GitHub plugin's vocabulary it still quotes,
// and how often. Most collide with the host's own words (a DOM id, a YAML key, a connection state, a built-in lens
// field); the view and facet ids among them are custom views and lens fields not yet served. A count only shrinks:
// a lower count fails until the entry is lowered.
var sourceDebts = map[string]map[string]int{
	"components/chrome/Candidate.svelte":     {"draft": 1, "status": 1, "wait": 1},
	"components/chrome/Dash.svelte":          {"dash": 3, "is": 1},
	"components/chrome/Explore.svelte":       {"explore": 4, "status": 1},
	"components/chrome/Notice.svelte":        {"status": 1},
	"components/chrome/UpdateBanner.svelte":  {"status": 1},
	"components/chrome/dash.ts":              {"author": 4, "ci": 4, "dash": 1, "draft": 1, "due": 2, "is": 4, "kind": 10, "opened": 5, "risk": 5, "size": 5, "tag": 8, "type": 2, "wait": 1, "waits-on": 2, "where": 3},
	"components/chrome/explore.ts":           {"author": 1, "draft": 1, "explore": 1, "review": 1, "size": 1},
	"components/chrome/facts.ts":             {"author": 1},
	"components/chrome/plugins.svelte.ts":    {"author": 1},
	"components/chrome/rail.ts":              {"waits-on": 4},
	"components/chrome/sync.ts":              {"closed": 1},
	"components/chrome/toolbar.ts":           {"author": 1, "kind": 1, "where": 1},
	"components/chrome/warnings.ts":          {"closed": 1},
	"components/detail/BriefBlock.svelte":    {"risk": 1, "size": 1, "type": 1},
	"components/detail/GuidanceBox.svelte":   {"guidance": 1},
	"components/detail/QuestionBlock.svelte": {"guidance": 1},
	"components/detail/TagChips.svelte":      {"tag": 1},
	"components/detail/UnitDetail.svelte":    {"age": 1, "guidance": 1, "kind": 1},
	"components/detail/unit.ts":              {"all": 4, "author": 1, "draft": 1},
	"components/keys/keys.ts":                {"review": 3},
	"components/rows/Activity.svelte":        {"kind": 1},
	"components/rows/Lane.svelte":            {"lane": 1},
	"components/rows/RowView.svelte":         {"age": 1, "kind": 1, "where": 1},
	"components/task/TaskProps.svelte":       {"risk": 1},
	"components/task/TaskRelated.svelte":     {"merged": 3},
	"components/task/TaskTimeline.svelte":    {"all": 1},
	"components/task/TaskWhen.svelte":        {"age": 2},
	"components/task/task.ts":                {"draft": 2, "merged": 2, "risk": 2},
	"lib/catalog.ts":                         {"review": 1},
	"lib/lens.ts":                            {"age": 1, "all": 1, "author": 2, "due": 6, "is": 5, "lane": 1, "tag": 4, "waits-on": 1, "where": 1},
	"lib/palette.ts":                         {"merged": 1},
	"lib/routes.ts":                          {"dash": 1, "explore": 1},
	"lib/tasks.ts":                           {"draft": 1},
	"state/loaders.svelte.ts":                {"closed": 1, "review": 1},
	"state/sync.svelte.ts":                   {"all": 1, "closed": 1},
}

// githubVocabulary is every event kind, intent, view id and facet id the
// bundled GitHub plugin declares.
func githubVocabulary(t *testing.T) map[string]string {
	t.Helper()
	m, err := manifest.Load("../../plugins/github")
	if err != nil {
		t.Fatal(err)
	}
	src, err := m.Source()
	if err != nil {
		t.Fatal(err)
	}
	vocab := map[string]string{}
	for _, e := range src.Events {
		vocab[e.Name] = "event kind"
	}
	if src.UI != nil {
		for _, v := range src.UI.Views {
			vocab[v.ID] = "view id"
		}
		for _, f := range src.UI.Facets {
			vocab[f.ID] = "facet id"
		}
	}
	intents, err := os.ReadDir(filepath.Join("..", "..", "plugins", "github", "profile", "intents"))
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range intents {
		vocab[d.Name()] = "intent"
	}
	return vocab
}

// strayWords are the vocabulary's words src quotes more often than debts
// allow, or less often than they say.
func strayWords(src string, vocab map[string]string, debts map[string]int) []string {
	var out []string
	for word, what := range vocab {
		n := len(regexp.MustCompile("[\"'`]"+regexp.QuoteMeta(word)+"[\"'`]").FindAllStringIndex(src, -1))
		if n != debts[word] {
			out = append(out, fmt.Sprintf("%s %s: quoted %d, allowed %d", what, word, n, debts[word]))
		}
	}
	sort.Strings(out)
	return out
}

// cockpitSourceFiles are the cockpit's modules and components under ui/src, by their path there, tests left out.
func cockpitSourceFiles(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir("ui/src", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel := strings.TrimPrefix(filepath.ToSlash(p), "ui/src/")
		if strings.HasSuffix(rel, ".test.ts") || strings.HasPrefix(rel, "testing/") || !(strings.HasSuffix(rel, ".ts") || strings.HasSuffix(rel, ".svelte")) {
			return nil
		}
		b, err := os.ReadFile(p)
		out[rel] = string(b)
		return err
	})
	if err != nil || len(out) == 0 {
		t.Fatalf("no cockpit sources: %v", err)
	}
	return out
}

func TestNoScriptNamesTheGitHubPluginsVocabulary(t *testing.T) {
	vocab := githubVocabulary(t)
	sources := cockpitSourceFiles(t)
	for name, src := range sources {
		for _, s := range strayWords(src, vocab, sourceDebts[name]) {
			t.Errorf("%s: %s", name, s)
		}
	}
	for name, debts := range sourceDebts {
		if _, ok := sources[name]; !ok {
			t.Errorf("sourceDebts names %s, which ui/src does not hold", name)
		}
		for word := range debts {
			if _, ok := vocab[word]; !ok {
				t.Errorf("sourceDebts of %s names %s, which the GitHub plugin no longer declares", name, word)
			}
		}
	}
}

func TestAPlantedGitHubEventKindIsCaught(t *testing.T) {
	planted := `const X = new Set(["review_requested"]);`
	got := strings.Join(strayWords(planted, githubVocabulary(t), nil), "\n")
	if !strings.Contains(got, "event kind review_requested") {
		t.Errorf("a planted event kind went unseen: %s", got)
	}
}
