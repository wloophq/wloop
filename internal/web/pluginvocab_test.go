package web

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/wloophq/wloop/pkg/plugin/manifest"
)

// appJSDebts are the words of the GitHub plugin's vocabulary app.js still
// quotes, and how often. Most collide with the host's own words (a DOM id,
// a YAML key, a connection state, a built-in lens field); the view and
// facet ids among them are custom views and lens fields not yet served. The
// count only shrinks: a lower count fails until the entry is lowered.
var appJSDebts = map[string]int{
	"closed": 3, "draft": 2, "guidance": 7, "review": 1, "wait": 1,
	"age": 4, "author": 8, "due": 6, "is": 7, "kind": 6, "lane": 3, "repo": 5, "risk": 3, "size": 4, "status": 4, "tag": 5, "type": 3, "waits-on": 7,
	"all": 1, "approved": 2, "dash": 7, "done": 3, "explore": 14, "mine": 3, "plan": 9, "recent": 8, "stalled": 5,
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

func TestAppJSNamesNoneOfTheGitHubPluginsVocabulary(t *testing.T) {
	for _, s := range strayWords(appJS(t), githubVocabulary(t), appJSDebts) {
		t.Error(s)
	}
}

func TestAPlantedGitHubEventKindIsCaught(t *testing.T) {
	planted := appJS(t) + `\nconst X = new Set(["review_requested"]);`
	got := strings.Join(strayWords(planted, githubVocabulary(t), appJSDebts), "\n")
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
