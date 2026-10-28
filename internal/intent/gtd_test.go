package intent

import (
	"github.com/wloophq/wloop/internal/domain/workflow"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/profile"
)

func defaultLists() []byte {
	b, err := os.ReadFile("../../profiles/default/" + ListsFile)
	if err != nil {
		panic(err)
	}
	return b
}

func loadListsWith(t *testing.T, bundled, user string) (ListTexts, string) {
	t.Helper()
	var layers []profile.Layer
	if user != "" {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, ListsFile), []byte(user), 0o600); err != nil {
			t.Fatal(err)
		}
		layers = []profile.Layer{{Name: "user", Dir: dir}}
	}
	texts, problems := loadLists(layers, fstest.MapFS{ListsFile: {Data: []byte(bundled)}})
	return texts, problems.Error()
}

func TestEveryVerbAndViewIsWorded(t *testing.T) {
	texts, problems := BundledLists(os.DirFS("../../profiles/default"))
	if len(problems) > 0 {
		t.Fatal(problems.Error())
	}
	for _, v := range builtin.TaskVerbs {
		if w := texts.Verbs[v]; w.Label == "" || w.Summary == "" {
			t.Errorf("verb %s: label %q, summary %q", v, w.Label, w.Summary)
		}
	}
	for _, v := range builtin.TaskVerbInputs {
		if texts.Verbs[v].Input == "" {
			t.Errorf("verb %s asks for a value and says nothing", v)
		}
	}
	for _, v := range builtin.Views {
		if texts.Views[v] == "" {
			t.Errorf("view %s: no label", v)
		}
	}
}

func TestListTextsComeFromTheProfile(t *testing.T) {
	texts, problems := loadListsWith(t, string(defaultLists()), "[verbs.done]\nsummary = \"Close it\"\n\n[views.next]\nlabel = \"Now\"\n")
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

func TestAUserListsTableNamingNoVerbFails(t *testing.T) {
	texts, problems := loadListsWith(t, string(defaultLists()), "[verbs.dne]\nsummary = \"x\"\n\n[views.nxt]\nlabel = \"x\"\n")
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

func TestAnEmptyListTextKeepsTheBundledOne(t *testing.T) {
	texts, problems := loadListsWith(t, string(defaultLists()), "[verbs.next]\nlabel = \"\"\n\n[views.inbox]\nlabel = \"\"\n")
	for _, want := range []string{"verbs.next.label: is required", "views.inbox.label: is required"} {
		if !strings.Contains(problems, want) {
			t.Errorf("no %q in:\n%s", want, problems)
		}
	}
	if texts.Verbs["next"].Label != "Next" || texts.Views["inbox"] != "Inbox" {
		t.Errorf("bundled texts lost: %+v %q", texts.Verbs["next"], texts.Views["inbox"])
	}
}

func TestABundledListsFileMissingAVerbFails(t *testing.T) {
	src := strings.Replace(string(defaultLists()), "[verbs.someday]", "[verbs.somday]", 1)
	_, problems := loadListsWith(t, src, "")
	if !strings.Contains(problems, "verbs.someday: is missing: every verb has a table") {
		t.Errorf("%s", problems)
	}
}

func TestAVerbThatAsksNeedsItsQuestion(t *testing.T) {
	src := strings.Replace(string(defaultLists()), "input   = \"Waiting on whom (optional)\"\n", "", 1)
	_, problems := loadListsWith(t, src, "")
	if !strings.Contains(problems, "verbs.waiting.input: is required: the verb asks for a value first") {
		t.Errorf("%s", problems)
	}
	texts, problems := loadListsWith(t, string(defaultLists()), "[verbs.waiting]\ninput = \"On whom?\"\n")
	if problems != "" || texts.Verbs["waiting"].Input != "On whom?" {
		t.Errorf("%q %s", texts.Verbs["waiting"].Input, problems)
	}
}

func TestTheReviewStepsAreWordedByTheProfile(t *testing.T) {
	texts, problems := loadListsWith(t, string(defaultLists()), "[review.waiting]\nlabel = \"Chase what waits\"\n\n[review.inbx]\nlabel = \"x\"\n")
	if !strings.Contains(problems, "review.inbx: names no Review step; did you mean \"inbox\"?") {
		t.Errorf("%s", problems)
	}
	if texts.Review["waiting"] != "Chase what waits" || texts.Review["inbox"] != "Inbox to zero" {
		t.Errorf("review %v", texts.Review)
	}
	for _, id := range builtin.ReviewSteps {
		if bundled, _ := BundledLists(os.DirFS("../../profiles/default")); bundled.Review[id] == "" {
			t.Errorf("review step %s has no label", id)
		}
	}
}

func TestAVerbIsOfferedWhereTheProfileSays(t *testing.T) {
	texts, problems := loadListsWith(t, string(defaultLists()), "[verbs.delegate]\noffer = 'entry.kind != \"capture\"'\n\n[verbs.done]\noffer = 'entry.items_open == 0'\n")
	if !strings.Contains(problems, "verbs.done.offer:") {
		t.Errorf("an offer reading a fact it cannot see loads: %s", problems)
	}
	offer := texts.Verbs["delegate"].Offer
	if offer == nil {
		t.Fatal("delegate has no offer")
	}
	for kind, want := range map[string]bool{"capture": false, "action": true} {
		got, err := offer.Match(workflow.Vars{Facts: workflow.Facts{"entry.kind": kind}})
		if err != nil || got != want {
			t.Errorf("%s: offered %v, %v", kind, got, err)
		}
	}
	if texts.Verbs["done"].Offer != nil || texts.Verbs["next"].Offer != nil {
		t.Error("a verb whose offer failed, or that has none, is offered everywhere")
	}
}

func TestSeenTakesNoOffer(t *testing.T) {
	texts, problems := loadListsWith(t, string(defaultLists()), "[verbs.seen]\noffer = 'entry.kind == \"message\"'\n")
	if !strings.Contains(problems, "verbs.seen.offer: seen takes no offer") || texts.Verbs["seen"].Offer != nil {
		t.Errorf("%s", problems)
	}
}
