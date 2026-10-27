package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/workspace/fake"
)

const triageManifest = `label = "Triage"
summary = "Triage the open questions of a unit"
workspace = "linked_else_main"
provider = "any"
session = { strategy = "new", title = "triage" }

[help]
target = "a pull request or a Slack thread"
examples = ["acme/api#7 start with the oldest"]

[headline]
resumed = "triage of {{.unit.ref}}"
[[headline.new]]
line = "Triage {{.unit.ref}}."

[record]
states = ["done", "failed"]
`

func TestTheHelpTopicsOfIntentsComeFromTheirManifests(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	review, err := os.ReadFile(filepath.Join("..", "..", "profiles", "default", "intents", "review", "intent.toml"))
	if err != nil {
		t.Fatal(err)
	}
	ours := strings.Replace(string(review), "Review someone else's pull request in its own worktree", "Review with the house rules", 1)
	ours = strings.Replace(ours, "look at the migration first", "check the house rules", 1)
	if ours == string(review) {
		t.Fatal("the bundled review manifest has no help to override")
	}
	userProfile(t, te, map[string]string{
		filepath.Join("intents", "triage", "intent.toml"):          triageManifest,
		filepath.Join("intents", "triage", "instructions.md.tmpl"): "{{.Headline}}",
		filepath.Join("intents", "review", "intent.toml"):          ours,
	})
	main := helpOut(t, te, "help")
	for _, want := range []string{"\n" + groupYourIntents + "\n", "\n  triage ", "Triage the open questions of a unit\n", "Review with the house rules\n"} {
		if !strings.Contains(main, want) {
			t.Errorf("the help lacks %q:\n%s", want, main)
		}
	}
	if strings.Contains(main, "Review someone else's pull request in its own worktree") {
		t.Errorf("the help keeps the bundled review summary over the profile's:\n%s", main)
	}
	triage := helpOut(t, te, "triage", "--help")
	unwrapped := strings.Join(strings.Fields(triage), " ")
	for _, want := range []string{te.command() + " triage <url|ref|permalink|slack:key> [guidance...]",
		"The target is a pull request or a Slack thread; words after it are guidance for the turn.",
		te.command() + " triage acme/api#7 start with the oldest"} {
		if !strings.Contains(unwrapped, want) {
			t.Errorf("triage --help lacks %q:\n%s", want, triage)
		}
	}
	if strings.Contains(triage, "Slack message permalink") || strings.Contains(triage, "github.com/acme/api/pull/7") {
		t.Errorf("triage --help carries words its manifest does not:\n%s", triage)
	}
	if got := helpOut(t, te, "review", "--help"); !strings.Contains(got, te.command()+" review https://github.com/acme/api/pull/7 check the house rules\n") {
		t.Errorf("review --help lacks the profile's example:\n%s", got)
	}
}
