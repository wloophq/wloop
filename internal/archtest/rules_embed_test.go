package archtest

import (
	"fmt"
	"path"
	"slices"
	"strings"
	"testing"
)

// embedAllowed is prose a package outside profiles/ may still embed: a
// module-relative file, or a directory and everything under it.
type embedAllowed struct{ path, reason string }

// embedKept is prose that stays in Go for good.
var embedKept = []embedAllowed{
	{"skills", "the skills install writes into agent homes"},
	{"internal/adapter/source/slack/eval", "the extraction eval's own prompts, a dev tool"},
}

// embedMoving is prose that moves to the profile; the list only shrinks.
var embedMoving = []embedAllowed{
	{"internal/turn/templates", "turn partials"},
	{"internal/adapter/source/slack/extract/prompt.md", "the Slack extraction prompt"},
	{"internal/brief/prompt.md", "the brief prompt"},
	{"internal/brief/taxonomy.md", "the brief taxonomy"},
}

var proseExts = []string{".md", ".tmpl", ".txt"}

// ruleEmbed flags each file with a prose extension that dir embeds outside
// profiles/ and the allowlist; used marks the entries that matched.
func ruleEmbed(dir string, embedded []string, allow []embedAllowed, used map[int]bool) []string {
	if under(dir, "profiles") {
		return nil
	}
	var out []string
	for _, e := range embedded {
		if !slices.Contains(proseExts, path.Ext(e)) {
			continue
		}
		file := path.Join(dir, e)
		i := slices.IndexFunc(allow, func(a embedAllowed) bool { return file == a.path || strings.HasPrefix(file, a.path+"/") })
		if i >= 0 {
			used[i] = true
			continue
		}
		out = append(out, fmt.Sprintf("%s: prose embedded outside profiles/; text an agent or a person reads lives in the profile (R-embed)", file))
	}
	return out
}

func staleEmbeds(allow []embedAllowed, used map[int]bool) []string {
	var out []string
	for i, a := range allow {
		if !used[i] {
			out = append(out, fmt.Sprintf("embed allowlist entry %s (%s) matches nothing; remove it", a.path, a.reason))
		}
	}
	return out
}

func TestProseIsEmbeddedOnlyFromProfiles(t *testing.T) {
	t.Parallel()
	allow := slices.Concat(embedKept, embedMoving)
	used := map[int]bool{}
	for _, p := range packages(t) {
		dir, ok := local(p.ImportPath)
		if !ok {
			continue
		}
		for _, b := range ruleEmbed(dir, p.EmbedFiles, allow, used) {
			t.Error(b)
		}
	}
	for _, s := range staleEmbeds(allow, used) {
		t.Error(s)
	}
}
