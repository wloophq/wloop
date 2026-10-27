package intent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/profile"
)

func defaultGTD() []byte {
	b, err := os.ReadFile("../../profiles/default/" + GTDFile)
	if err != nil {
		panic(err)
	}
	return b
}

func loadGTDWith(t *testing.T, bundled, user string) (GTDTexts, string) {
	t.Helper()
	var layers []profile.Layer
	if user != "" {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, GTDFile), []byte(user), 0o600); err != nil {
			t.Fatal(err)
		}
		layers = []profile.Layer{{Name: "user", Dir: dir}}
	}
	texts, problems := loadGTD(layers, fstest.MapFS{GTDFile: {Data: []byte(bundled)}})
	return texts, problems.Error()
}

func TestEveryVerbAndViewIsWorded(t *testing.T) {
	texts, problems := BundledGTD(os.DirFS("../../profiles/default"))
	if len(problems) > 0 {
		t.Fatal(problems.Error())
	}
	for _, v := range builtin.GTDVerbs {
		if w := texts.Verbs[v]; w.Label == "" || w.Summary == "" {
			t.Errorf("verb %s: label %q, summary %q", v, w.Label, w.Summary)
		}
	}
	for _, v := range builtin.GTDVerbInputs {
		if texts.Verbs[v].Input == "" {
			t.Errorf("verb %s asks for a value and says nothing", v)
		}
	}
	for _, v := range builtin.GTDViews {
		if texts.Views[v] == "" {
			t.Errorf("view %s: no label", v)
		}
	}
}

func TestGTDTextsComeFromTheProfile(t *testing.T) {
	texts, problems := loadGTDWith(t, string(defaultGTD()), "[verbs.done]\nsummary = \"Close it\"\n\n[views.next]\nlabel = \"Now\"\n")
	if problems != "" {
		t.Fatal(problems)
	}
	if got := texts.Verbs["done"]; got.Label != "Done" || got.Summary != "Close it" {
		t.Errorf("done: %+v", got)
	}
	if got := texts.Views["next"]; got != "Now" {
		t.Errorf("next view: %q", got)
	}
}

func TestAUserGTDTableNamingNoVerbFails(t *testing.T) {
	texts, problems := loadGTDWith(t, string(defaultGTD()), "[verbs.dne]\nsummary = \"x\"\n\n[views.nxt]\nlabel = \"x\"\n")
	for _, want := range []string{
		"verbs.dne: names no verb; did you mean \"done\"?",
		"views.nxt: names no view; did you mean \"next\"?",
	} {
		if !strings.Contains(problems, want) {
			t.Errorf("no %q in:\n%s", want, problems)
		}
	}
	if texts.Verbs["done"].Summary == "x" {
		t.Error("a table naming no verb changed done")
	}
}

func TestAnEmptyGTDTextKeepsTheBundledOne(t *testing.T) {
	texts, problems := loadGTDWith(t, string(defaultGTD()), "[verbs.next]\nlabel = \"\"\n\n[views.inbox]\nlabel = \"\"\n")
	for _, want := range []string{"verbs.next.label: is required", "views.inbox.label: is required"} {
		if !strings.Contains(problems, want) {
			t.Errorf("no %q in:\n%s", want, problems)
		}
	}
	if texts.Verbs["next"].Label != "Next" || texts.Views["inbox"] != "Inbox" {
		t.Errorf("bundled texts lost: %+v %q", texts.Verbs["next"], texts.Views["inbox"])
	}
}

func TestABundledGTDFileMissingAVerbFails(t *testing.T) {
	src := strings.Replace(string(defaultGTD()), "[verbs.someday]", "[verbs.somday]", 1)
	_, problems := loadGTDWith(t, src, "")
	if !strings.Contains(problems, "verbs.someday: is missing: every verb has a table") {
		t.Errorf("%s", problems)
	}
}

func TestAVerbThatAsksNeedsItsQuestion(t *testing.T) {
	src := strings.Replace(string(defaultGTD()), "input   = \"Waiting on whom (optional)\"\n", "", 1)
	_, problems := loadGTDWith(t, src, "")
	if !strings.Contains(problems, "verbs.waiting.input: is required: the verb asks for a value first") {
		t.Errorf("%s", problems)
	}
	texts, problems := loadGTDWith(t, string(defaultGTD()), "[verbs.waiting]\ninput = \"On whom?\"\n")
	if problems != "" || texts.Verbs["waiting"].Input != "On whom?" {
		t.Errorf("%q %s", texts.Verbs["waiting"].Input, problems)
	}
}
