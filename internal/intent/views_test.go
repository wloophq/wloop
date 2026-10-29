package intent

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/workflow"
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
// as the two files: [verbs.*], [states.*], [props.*], [owed_by] and [chat]
// tables in tasks.toml, the rest in views.toml.
func splitLists(src string) map[string]string {
	out := map[string]string{}
	file := ViewsFile
	for _, line := range strings.SplitAfter(src, "\n") {
		if strings.HasPrefix(line, "[") {
			file = ViewsFile
			if strings.HasPrefix(line, "[verbs") || strings.HasPrefix(line, "[states") || strings.HasPrefix(line, "[props") || strings.HasPrefix(line, "[owed_by") || strings.HasPrefix(line, "[chat]") {
				file = TasksFile
			}
		}
		out[file] += line
	}
	return out
}

// lists are the views and the verbs' texts of tasks.toml.
type lists struct {
	ViewsDef
	Verbs map[string]VerbText
}

// label is view id's label; "" when the profile declares none.
func (l lists) label(id string) string {
	v, _ := l.View(id)
	return v.Label
}

// key is view id's key; "" when it has none.
func (l lists) key(id string) string {
	v, _ := l.View(id)
	return v.Key
}

// step is the label of the review step that walks view id; "" when none does.
func (l lists) step(id string) string {
	for _, st := range l.Review {
		if st.View == id {
			return st.Label
		}
	}
	return ""
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
	texts, tasks, problems := loadViews(layers, fsys, nil)
	return lists{texts, tasks.Verbs}, problems.Error()
}

func TestEveryVerbAndViewIsWorded(t *testing.T) {
	views, tasks, problems := loadViews(nil, os.DirFS("../../profiles/default"), nil)
	if len(problems) > 0 {
		t.Fatal(problems.Error())
	}
	texts := lists{views, tasks.Verbs}
	for _, v := range tasks.VerbOrder {
		w := texts.Verbs[v]
		if w.Label == "" || w.Summary == "" {
			t.Errorf("verb %s: label %q, summary %q", v, w.Label, w.Summary)
		}
		if slices.Contains(builtin.InputHandlers, w.Handler) && w.Input == "" {
			t.Errorf("verb %s asks for a value and says nothing", v)
		}
	}
	for _, v := range texts.Views {
		if v.Label == "" {
			t.Errorf("view %s: no label", v.ID)
		}
	}
	for _, st := range texts.Review {
		if st.Label == "" {
			t.Errorf("review step %s: no label", st.View)
		}
	}
}

func TestListTextsComeFromTheProfile(t *testing.T) {
	texts, problems := loadListsWith(t, string(defaultLists()), "[verbs.done]\nsummary = \"Close it\"\n\n[views.needs-me]\nlabel = \"Now\"\n")
	if problems != "" {
		t.Fatal(problems)
	}
	if got := texts.Verbs["done"]; got.Label != "Done" || got.Summary != "Close it" {
		t.Errorf("done: %+v", got)
	}
	if got := texts.label("needs-me"); got != "Now" {
		t.Errorf("needs-me view: %q", got)
	}
}

func TestAUserVerbAndViewNeedTheirLabels(t *testing.T) {
	texts, problems := loadListsWith(t, string(defaultLists()), "[verbs.dne]\nsummary = \"x\"\n\n[views.later]\nwhen = \"true\"\n")
	for _, want := range []string{
		"verbs.dne.label: is required",
		"views.later.label: is required",
	} {
		if !strings.Contains(problems, want) {
			t.Errorf("no %q in:\n%s", want, problems)
		}
	}
	if texts.Verbs["done"].Summary == "x" {
		t.Error("a new verb changed done")
	}
}

func TestAnEmptyListTextKeepsTheBundledOne(t *testing.T) {
	texts, problems := loadListsWith(t, string(defaultLists()), "[verbs.next]\nlabel = \"\"\n\n[views.inbox]\nlabel = \"\"\n")
	for _, want := range []string{"verbs.next.label: is required", "views.inbox.label: is required"} {
		if !strings.Contains(problems, want) {
			t.Errorf("no %q in:\n%s", want, problems)
		}
	}
	if texts.Verbs["next"].Label != "Next" || texts.label("inbox") != "Inbox" {
		t.Errorf("bundled texts lost: %+v %q", texts.Verbs["next"], texts.label("inbox"))
	}
}

func TestAVerbThatAsksNeedsItsQuestion(t *testing.T) {
	src := strings.Replace(string(defaultLists()), "input   = \"Waiting on whom (optional)\"\n", "", 1)
	_, problems := loadListsWith(t, src, "")
	if !strings.Contains(problems, "verbs.waiting.input: is required: its handler waiting asks for a value first") {
		t.Errorf("%s", problems)
	}
	texts, problems := loadListsWith(t, string(defaultLists()), "[verbs.waiting]\ninput = \"On whom?\"\n")
	if problems != "" || texts.Verbs["waiting"].Input != "On whom?" {
		t.Errorf("%q %s", texts.Verbs["waiting"].Input, problems)
	}
}

func TestTheReviewStepsAreWordedByTheProfile(t *testing.T) {
	texts, problems := loadListsWith(t, string(defaultLists()), "[review.waiting]\nlabel = \"Chase what waits\"\n")
	if problems != "" {
		t.Fatal(problems)
	}
	if texts.step("waiting") != "Chase what waits" || texts.step("inbox") != "Inbox to zero" {
		t.Errorf("review %v", texts.Review)
	}
}

func TestAReviewStepNamesAView(t *testing.T) {
	texts, problems := loadListsWith(t, string(defaultLists()), "[review.inbx]\nlabel = \"x\"\n\n[badge]\nviews = [\"inbox\", \"neds-me\"]\n")
	for _, want := range []string{
		"review.inbx: names no view of views.toml; did you mean \"inbox\"?",
		"badge.views[1]: names no view of views.toml; did you mean \"needs-me\"?",
	} {
		if !strings.Contains(problems, want) {
			t.Errorf("no %q in:\n%s", want, problems)
		}
	}
	if slices.ContainsFunc(texts.Review, func(st ReviewStepDef) bool { return st.View == "inbx" }) || !slices.Equal(texts.Badge, []string{"inbox"}) {
		t.Errorf("review %v, badge %v", texts.Review, texts.Badge)
	}
	var walked []string
	for _, st := range texts.Review {
		if _, ok := texts.View(st.View); !ok {
			t.Errorf("review step %s walks no view", st.View)
		}
		walked = append(walked, st.View)
	}
	if want := []string{"inbox", "outbox", "waiting", "review-projects", "someday", "focus"}; !slices.Equal(walked, want) {
		t.Errorf("the review walks %v, want %v", walked, want)
	}
}

func TestAUserViewLoads(t *testing.T) {
	user := "[views.later]\nlabel = \"Later\"\nkey = \"g y\"\nwhen = \"task.prop.priority == 'low' && task.gates_open == 0\"\ngroup = \"task.prop.area\"\nsort = \"-task.prop.due, task.id\"\n\n" +
		"[views.asks]\nlabel = \"Asks\"\nover = \"gates\"\nwhen = \"gate.kind == 'question'\"\n\n" +
		"[review.later]\nlabel = \"Anything to pull in?\"\n\n[badge]\nviews = [\"inbox\", \"later\"]\n"
	texts, problems := loadListsWith(t, string(defaultLists()), user)
	if problems != "" {
		t.Fatal(problems)
	}
	later, ok := texts.View("later")
	if !ok || later.Label != "Later" || later.Key != "g y" || later.Over != task.OverTasks || later.Group != "task.prop.area" || later.When == nil ||
		!slices.Equal(later.Sort, []SortKey{{Fact: "task.prop.due", Desc: true}, {Fact: "task.id"}}) {
		t.Fatalf("later %+v", later)
	}
	for f, want := range map[string]bool{"low": true, "high": false} {
		got, err := later.When.Match(workflow.Vars{Facts: workflow.Facts{"task.prop.priority": f, "task.gates_open": 0}})
		if err != nil || got != want {
			t.Errorf("priority %s: %v %v", f, got, err)
		}
	}
	if asks, ok := texts.View("asks"); !ok || asks.Over != task.OverGates || asks.When == nil {
		t.Errorf("asks %+v", asks)
	}
	if texts.step("later") == "" || !texts.Badged("later") || texts.Badged("needs-me") {
		t.Errorf("review %v, badge %v", texts.Review, texts.Badge)
	}
	_, problems = loadListsWith(t, string(defaultLists()), "[views.later]\nlabel = \"Later\"\nwhen = \"gate.kind == 'draft'\"\ngroup = \"task.nope\"\n")
	for _, want := range []string{"views.later.when:", "views.later.group: task.nope names no fact of a view over tasks"} {
		if !strings.Contains(problems, want) {
			t.Errorf("no %q in:\n%s", want, problems)
		}
	}
}

func TestViewsKeepTheirDeclaredOrderAcrossLayers(t *testing.T) {
	user := "[views.zeta]\nlabel = \"Zeta\"\nwhen = \"true\"\n\n[views.inbox]\nlabel = \"In\"\n\n[views.alpha]\nlabel = \"Alpha\"\nwhen = \"true\"\n\n" +
		"[review.zeta]\nlabel = \"z\"\n\n[review.alpha]\nlabel = \"a\"\n"
	texts, problems := loadListsWith(t, string(defaultLists()), user)
	if problems != "" {
		t.Fatal(problems)
	}
	bundled, _ := BundledViews(os.DirFS("../../profiles/default"))
	if want := append(bundled.IDs(), "zeta", "alpha"); !slices.Equal(texts.IDs(), want) {
		t.Errorf("views %v, want %v", texts.IDs(), want)
	}
	if want := []string{"inbox", "needs-me", "waiting", "chase", "fits-now", "blocked-by-me", "stalled", "in-loop", "approved", "snoozed", "focus", "projects", "someday", "logbook", "done", "outbox", "review-projects", "review"}; !slices.Equal(bundled.IDs(), want) {
		t.Errorf("bundled views %v, want %v", bundled.IDs(), want)
	}
	var steps []string
	for _, st := range texts.Review {
		steps = append(steps, st.View)
	}
	if want := []string{"inbox", "outbox", "waiting", "review-projects", "someday", "focus", "zeta", "alpha"}; !slices.Equal(steps, want) {
		t.Errorf("steps %v, want %v", steps, want)
	}
}

func TestAVerbIsOfferedWhereTheProfileSays(t *testing.T) {
	texts, problems := loadListsWith(t, string(defaultLists()), "[verbs.delegate]\noffer = 'task.prop.kind != \"capture\"'\n\n[verbs.done]\noffer = 'task.items_open == 0'\n")
	if !strings.Contains(problems, "verbs.done.offer:") {
		t.Errorf("an offer reading a fact it cannot see loads: %s", problems)
	}
	offer := texts.Verbs["delegate"].Offer
	if offer == nil {
		t.Fatal("delegate has no offer")
	}
	for kind, want := range map[string]bool{"capture": false, "thread": true} {
		got, err := offer.Match(workflow.Vars{Facts: workflow.Facts{"task.prop.kind": kind}})
		if err != nil || got != want {
			t.Errorf("%s: offered %v, %v", kind, got, err)
		}
	}
	if texts.Verbs["done"].Offer != nil || texts.Verbs["next"].Offer != nil {
		t.Error("a verb whose offer failed, or that has none, is offered everywhere")
	}
}

func TestSeenTakesNoOffer(t *testing.T) {
	texts, problems := loadListsWith(t, string(defaultLists()), "[verbs.seen]\noffer = 'task.prop.kind == \"capture\"'\n")
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
	got, problems := loadListsWith(t, string(defaultLists()), "[views.waiting]\nkey = \"g x\"\n\n[views.review-projects]\nkey = \"l\"\n\n[views.focus]\nkey = \"g p\"\n")
	if got.key("waiting") != "g x" || got.key("inbox") != "g i" || got.key("review-projects") != "" || got.key("logbook") != "" {
		t.Errorf("keys %+v", got.Views)
	}
	for _, want := range []string{
		"views.review-projects.key: l is not a chord",
		"views.focus.key: g p is the key of views.projects too",
	} {
		if !strings.Contains(problems, want) {
			t.Errorf("no %q in:\n%s", want, problems)
		}
	}
}
