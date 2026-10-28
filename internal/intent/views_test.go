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
	var out []byte
	for _, f := range []string{TasksFile, ViewsFile} {
		b, err := os.ReadFile("../../profiles/default/" + f)
		if err != nil {
			panic(err)
		}
		out = append(append(out, b...), '\n')
	}
	return out
}

// splitLists is src, the tables of tasks.toml and views.toml together,
// as the two files: [verbs.*], [states.*] and [props.*] tables in
// tasks.toml, the rest in views.toml.
func splitLists(src string) map[string]string {
	out := map[string]string{}
	file := ViewsFile
	for _, line := range strings.SplitAfter(src, "\n") {
		if strings.HasPrefix(line, "[") {
			file = ViewsFile
			if strings.HasPrefix(line, "[verbs") || strings.HasPrefix(line, "[states") || strings.HasPrefix(line, "[props") {
				file = TasksFile
			}
		}
		out[file] += line
	}
	return out
}

// lists are the views' texts and the verbs' of tasks.toml.
type lists struct {
	ListTexts
	Verbs map[string]VerbText
}

func loadListsWith(t *testing.T, bundled, user string) (lists, string) {
	t.Helper()
	var layers []profile.Layer
	if user != "" {
		dir := t.TempDir()
		for file, src := range splitLists(user) {
			if err := os.WriteFile(filepath.Join(dir, file), []byte(src), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		layers = []profile.Layer{{Name: "user", Dir: dir}}
	}
	fsys := fstest.MapFS{}
	for file, src := range splitLists(bundled) {
		fsys[file] = &fstest.MapFile{Data: []byte(src)}
	}
	texts, tasks, problems := loadLists(layers, fsys, nil)
	return lists{texts, tasks.Verbs}, problems.Error()
}

func TestEveryVerbAndViewIsWorded(t *testing.T) {
	views, tasks, problems := loadLists(nil, os.DirFS("../../profiles/default"), nil)
	if len(problems) > 0 {
		t.Fatal(problems.Error())
	}
	texts := lists{views, tasks.Verbs}
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
	if !strings.Contains(problems, "review.inbx: names no review step; did you mean \"inbox\"?") {
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
	texts, problems := loadListsWith(t, string(defaultLists()), "[verbs.delegate]\noffer = 'task.kind != \"capture\"'\n\n[verbs.done]\noffer = 'task.items_open == 0'\n")
	if !strings.Contains(problems, "verbs.done.offer:") {
		t.Errorf("an offer reading a fact it cannot see loads: %s", problems)
	}
	offer := texts.Verbs["delegate"].Offer
	if offer == nil {
		t.Fatal("delegate has no offer")
	}
	for kind, want := range map[string]bool{"capture": false, "action": true} {
		got, err := offer.Match(workflow.Vars{Facts: workflow.Facts{"task.kind": kind}})
		if err != nil || got != want {
			t.Errorf("%s: offered %v, %v", kind, got, err)
		}
	}
	if texts.Verbs["done"].Offer != nil || texts.Verbs["next"].Offer != nil {
		t.Error("a verb whose offer failed, or that has none, is offered everywhere")
	}
}

func TestSeenTakesNoOffer(t *testing.T) {
	texts, problems := loadListsWith(t, string(defaultLists()), "[verbs.seen]\noffer = 'task.kind == \"message\"'\n")
	if !strings.Contains(problems, "verbs.seen.offer: seen takes no offer") || texts.Verbs["seen"].Offer != nil {
		t.Errorf("%s", problems)
	}
}

func TestAGatedVerbNamesItsGate(t *testing.T) {
	_, problems := loadListsWith(t, string(defaultLists()), "[verbs.done.by]\nagent = \"gate\"\n")
	if !strings.Contains(problems, "verbs.done.gate: is required") {
		t.Errorf("%s", problems)
	}
	_, problems = loadListsWith(t, string(defaultLists()), "[verbs.done]\ngate = \"proposal\"\n[verbs.done.by]\nagent = \"gate\"\n")
	if strings.Contains(problems, "verbs.done.gate") {
		t.Errorf("%s", problems)
	}
}

func TestAViewsKeyComesFromTheProfile(t *testing.T) {
	got, problems := loadListsWith(t, string(defaultLists()), "[views.waiting]\nkey = \"g x\"\n\n[views.someday]\nkey = \"g g\"\n\n[views.logbook]\nkey = \"l\"\n\n[views.focus]\nkey = \"g p\"\n")
	if got.Keys["waiting"] != "g x" || got.Keys["inbox"] != "g i" || got.Keys["someday"] != "" || got.Keys["logbook"] != "" {
		t.Errorf("keys %v", got.Keys)
	}
	for _, want := range []string{
		"views.someday.key: g g is the core key of nav:github",
		"views.logbook.key: l is not a chord",
		"views.focus.key: g p is the key of views.projects too",
	} {
		if !strings.Contains(problems, want) {
			t.Errorf("no %q in:\n%s", want, problems)
		}
	}
}
