package intent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/wloophq/wloop/internal/keys"
	"github.com/wloophq/wloop/internal/profile"
)

func TestEveryCoreKeyHasItsHelp(t *testing.T) {
	texts, problems := BundledKeys(os.DirFS("../../profiles/default"))
	if len(problems) > 0 {
		t.Fatal(problems.Error())
	}
	for _, b := range keys.Core {
		if texts.Help(string(b.Context), b.Action) == "" {
			t.Errorf("%s (%s) has no help", b.Key, b.Action)
		}
	}
	if got := texts.Help("question", "answer:3"); got != "answer option 3" {
		t.Errorf("answer:3: %q", got)
	}
}

func TestKeyHelpComesFromTheProfile(t *testing.T) {
	bundled, err := os.ReadFile("../../profiles/default/" + KeysFile)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	user := "[help.global]\n\"nav:inbox\" = \"go to the Inbox\"\n\"nav:inbx\" = \"x\"\n\n[help.list]\n\"move:up\" = \"\"\n"
	if err := os.WriteFile(filepath.Join(dir, KeysFile), []byte(user), 0o600); err != nil {
		t.Fatal(err)
	}
	texts, problems := loadKeys([]profile.Layer{{Name: "user", Dir: dir}}, fstest.MapFS{KeysFile: {Data: bundled}})
	if texts.Help("global", "nav:inbox") != "go to the Inbox" || texts.Help("list", "move:up") != "move up" {
		t.Errorf("help %q, %q", texts.Help("global", "nav:inbox"), texts.Help("list", "move:up"))
	}
	if texts.Help("list", "approve") == texts.Help("plan", "approve") {
		t.Errorf("approve reads alike in the list and the plan: %q", texts.Help("plan", "approve"))
	}
	for _, want := range []string{`help.global.nav:inbx: names no core key action of global; did you mean "nav:inbox"?`, "help.list.move:up: is required"} {
		if !strings.Contains(problems.Error(), want) {
			t.Errorf("no %q in:\n%s", want, problems.Error())
		}
	}
}
