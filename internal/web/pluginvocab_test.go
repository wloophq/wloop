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

// scriptDebts are, by script of static/, the words of the GitHub plugin's
// vocabulary it still quotes, and how often. Most collide with the host's
// own words (a DOM id, a YAML key, a connection state, a built-in lens
// field); the view and facet ids among them are custom views and lens fields
// not yet served. A count only shrinks: a lower count fails until the entry
// is lowered.
var scriptDebts = map[string]map[string]int{
	"app.js": {
		"closed": 3, "draft": 2, "guidance": 5, "review": 1, "wait": 1,
		"age": 2, "author": 3, "due": 6, "is": 5, "kind": 2, "lane": 2, "where": 1, "risk": 1, "size": 2, "status": 1, "tag": 5, "type": 1, "waits-on": 1,
		"all": 1, "dash": 7, "explore": 12, "mine": 0, "plan": 7, "recent": 6,
	},
	"dash.js": {
		"draft": 1, "dash": 5,
		"author": 4, "ci": 4, "due": 2, "is": 5, "kind": 10, "opened": 5, "risk": 5, "size": 5, "tag": 8, "type": 2, "wait": 1, "waits-on": 2, "where": 3,
	},
	"detail.js":  {"draft": 1, "merged": 4, "all": 4},
	"keys.js":    {"review": 3, "plan": 6},
	"palette.js": {"merged": 1},
	"plugins.js": {"author": 1},
	"rail.js":    {"waits-on": 4, "explore": 1, "plan": 1, "recent": 1},
	"row.js":     {"age": 1, "kind": 1},
	"task.js":    {"draft": 2, "merged": 1, "kind": 1, "all": 1},
	"toolbar.js": {"kind": 1, "explore": 1, "recent": 1},
	"views.js":   {"draft": 1, "where": 1, "review": 1, "plan": 1},
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

func TestNoScriptNamesTheGitHubPluginsVocabulary(t *testing.T) {
	scripts, err := fs.Glob(files, "static/*.js")
	if err != nil {
		t.Fatal(err)
	}
	vocab := githubVocabulary(t)
	seen := map[string]bool{}
	for _, path := range scripts {
		name := strings.TrimPrefix(path, "static/")
		seen[name] = true
		for _, s := range strayWords(staticJS(t, name), vocab, scriptDebts[name]) {
			t.Errorf("%s: %s", name, s)
		}
	}
	for name := range scriptDebts {
		if !seen[name] {
			t.Errorf("scriptDebts names %s, which static/ does not hold", name)
		}
	}
}

func TestAPlantedGitHubEventKindIsCaught(t *testing.T) {
	planted := appJS(t) + `\nconst X = new Set(["review_requested"]);`
	got := strings.Join(strayWords(planted, githubVocabulary(t), scriptDebts["app.js"]), "\n")
	if !strings.Contains(got, "event kind review_requested") {
		t.Errorf("a planted event kind went unseen: %s", got)
	}
}

func TestAnEventShowsItsDataTheWayItsCatalogSays(t *testing.T) {
	var got []string
	nodeRun(t, catalogPrelude(t)+jsBlock(t, appJS(t), "EVENT_DATA"), []string{"eventData"}, `process.stdout.write(JSON.stringify([
  eventData({ kind: "review", data: "APPROVED:carol" }),
  eventData({ kind: "merged", data: "0123456789abcdef" }),
  eventData({ kind: "review_requested", data: "team:acme/web" }),
  eventData({ kind: "commented", data: "a note" }),
  eventData({ kind: "merged" }),
]));`, nil, &got)
	if want := []string{"APPROVED", "0123456", "", "a note", ""}; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestTheDetailShowsItsPluginsFieldsAsTheCatalogWordsThem(t *testing.T) {
	var got [][]any
	prelude := catalogPrelude(t) + `const unitOf = (x) => x.unit;
const h = (tag, attrs, text) => [tag, attrs.title || "", text];
`
	nodeRun(t, prelude, []string{"detailFacts"}, `process.stdout.write(JSON.stringify([
  ...detailFacts({ unit: { provider: "github" } }, { profile_chain: "base > acme" }, { next_guidance: "focus on tests" }),
  ...detailFacts({ unit: { provider: "slack" } }, { profile_chain: "base" }, {}),
].filter(Boolean)));`, nil, &got)
	if len(got) != 2 {
		t.Fatalf("got %v", got)
	}
	labels := []string{}
	for _, pair := range got {
		dt := pair[0].([]any)
		labels = append(labels, dt[2].(string))
	}
	if strings.Join(labels, ",") != "Next round guidance,Review profile" {
		t.Errorf("labels %v", labels)
	}
}
