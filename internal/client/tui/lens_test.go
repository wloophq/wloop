package tui

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/catalog"
	"github.com/wloophq/wloop/internal/domain/unit"
	intentload "github.com/wloophq/wloop/internal/intent"
)

// lensCatalog is a profile's views, verbs and states the terminal knows nothing of beforehand.
func lensCatalog() api.ViewsCatalog {
	return api.ViewsCatalog{
		Views: []api.TaskViewSpec{{ID: "stalled", Label: "Stalled", Group: "task.state", Rail: true}, {ID: "mine", Label: "Mine", Badge: true, Rail: true},
			{ID: "sends", Label: "Sends", Over: "gates", Rail: true}},
		States: []api.StateSpec{{Name: "parked", Label: "Parked", Category: "waiting"}, {Name: "doing", Label: "Doing", Category: "active"}},
		Facets: []api.FacetSpec{{ID: "state", Fact: "task.state", Label: "State", Group: true, Menu: true, Rank: []string{"parked", "doing"},
			Labels: map[string]string{"parked": "Parked", "doing": "Doing"}, Empty: "none"}},
		Lens: testLensWords, DefaultSort: "View order",
		Verbs: []api.VerbSpec{{Verb: "park", Handler: "waiting", Label: "Park", Input: "Parked on whom"}, {Verb: "toss", Handler: "drop", Label: "Toss"},
			{Verb: "hand", Handler: "delegate", Label: "Hand", Input: "What should the agent do"}, {Verb: "tidy", Label: "Tidy"}},
	}
}

// testLensWords are the bundled profile's lens words.
var testLensWords = catalog.LensWords(bundledViews().Lens)

func lensTask(id, title, state string) api.TaskView {
	e := task(id, title, state)
	e.Facets = map[string][]string{"state": {state}}
	e.Verbs = []api.VerbState{{Verb: "park", Enabled: true}, {Verb: "toss", Enabled: true}, {Verb: "hand", Enabled: true}, {Verb: "tidy", Enabled: true}}
	return e
}

func TestTUIListsLensViewsFromTheCatalog(t *testing.T) {
	m := newModel(t)
	m.SetViews(lensCatalog())
	if v, ok := m.viewOnScreen(); !ok || v.ID != "stalled" {
		t.Fatalf("started on %+v, want the catalog's first view", v)
	}
	withFocus := newModel(t)
	withFocus.SetViews(api.ViewsCatalog{Views: []api.TaskViewSpec{{ID: "stalled", Label: "Stalled", Rail: true}, {ID: "focus", Label: "Focus", Rail: true}}})
	if v, ok := withFocus.viewOnScreen(); !ok || v.ID != "stalled" {
		t.Fatalf("started on %+v, want the catalog's first view over a later focus", v)
	}
	m.SetTasks("stalled", []api.TaskView{lensTask("t-1", "Ship the export", "doing"), lensTask("t-2", "Call the bank", "parked"),
		lensTask("t-3", "Read the spec", "doing")}, api.Counters{Views: map[string]int{"stalled": 3, "mine": 1, "sends": 0}})
	m.SetLoading([]api.Loading{{Plugin: "acme", Reason: "importing the threads"}})
	screen := screenOf(m, 120, 30)
	for _, want := range []string{"[Stalled 3]", "Mine 1", "Sends 0", "loading acme: importing the threads"} {
		if !strings.Contains(screen, want) {
			t.Fatalf("screen lacks %q:\n%s", want, screen)
		}
	}
	parked, doing := strings.Index(screen, " Parked\n"), strings.Index(screen, " Doing\n")
	if parked < 0 || doing < 0 || parked > doing || strings.Index(screen, "Call the bank") > doing {
		t.Fatalf("the groups do not follow the served states' order and labels:\n%s", screen)
	}
	for _, name := range []string{"Focus", "Inbox", "Next", "Waiting", "Outbox"} {
		if strings.Contains(screen, name) {
			t.Fatalf("the screen names %q, a view the catalog does not serve:\n%s", name, screen)
		}
	}

	m.navigate("sends")
	d := task("draft:9", "Reply in the release thread", "inbox")
	d.Gates = draftGates(api.Draft{ID: 9, Rev: 1, Purpose: "reply", Where: "#releases thread", Text: "Ship it tonight.", State: "pending"})
	m.SetTasks("sends", []api.TaskView{d}, api.Counters{})
	m.SetLoading(nil)
	if screen := screenOf(m, 120, 30); !strings.Contains(screen, "#releases thread") || !strings.Contains(screen, "Ship it tonight.") || strings.Contains(screen, "loading") {
		t.Fatalf("a view over gates does not list its drafts by target:\n%s", screen)
	}

	stale := row(4, "Retry the export", "approval", "needs-me")
	stale.Stale = true
	m = newModel(t)
	m.rowsLoading = []api.Loading{{Plugin: "github", Reason: "starting"}}
	m.SetData([]api.Row{stale}, api.Health{})
	if screen := screenOf(m, 120, 30); !strings.Contains(screen, staleMark+" Retry the export") || !strings.Contains(screen, "loading github: starting") {
		t.Fatalf("a stale row is not marked, or the loading plugin not named:\n%s", screen)
	}
}

func TestTUIVerbsComeFromTheCatalog(t *testing.T) {
	m := newModel(t)
	m.SetViews(lensCatalog())
	m.SetKeys(append(slices.Clone(m.keys), api.KeyView{Key: "w", Action: "park", Context: ctxList}, api.KeyView{Key: "t", Action: "tidy", Context: ctxList},
		api.KeyView{Key: "d", Action: "hand", Context: ctxList}, api.KeyView{Key: "Backspace", Action: "toss", Context: ctxList}))
	m.SetTasks("stalled", []api.TaskView{lensTask("t-1", "Ship the export", "doing")}, api.Counters{})

	if c, ok := press(m, Key{Rune: 'w'}); ok || m.prompt == nil || !strings.HasPrefix(m.prompt.label, "Parked on whom") {
		t.Fatalf("w did not ask the served input of the verb that runs waiting: %+v %+v", c, m.prompt)
	}
	c, ok := press(m, append(typed("ana"), Key{Name: KeyEnter})...)
	if !ok || c.verb != "park" || !reflect.DeepEqual(c.args, map[string]string{"on": "ana"}) {
		t.Fatalf("w then ana: %+v %v", c, ok)
	}
	m.Done(c, nil, nil)
	if c, ok = press(m, Key{Rune: 't'}); !ok || c.verb != "tidy" || c.task != "t-1" {
		t.Fatalf("a served verb with no handler did not run: %+v %v", c, ok)
	}
	m.Done(c, nil, nil)
	if _, ok := press(m, Key{Rune: 'd'}); ok || m.prompt == nil || !strings.Contains(m.prompt.label, "What should the agent do") {
		t.Fatalf("the verb that runs delegate did not ask for guidance: %+v", m.prompt)
	}
	if c, ok = press(m, append(typed("short"), Key{Name: KeyEnter})...); !ok || c.verb != "hand" || c.args["guidance"] != "short" {
		t.Fatalf("hand: %+v %v", c, ok)
	}
	m.Done(c, nil, nil)
	if c, ok = press(m, Key{Name: KeyBackspace}); !ok || c.verb != "toss" {
		t.Fatalf("Backspace did not run the verb bound to it: %+v %v", c, ok)
	}
	m.Done(c, nil, nil)

	m.SetKeys(append(slices.Clone(m.keys), api.KeyView{Key: "x", Action: "next", Context: ctxList}))
	if c, ok := press(m, Key{Rune: 'x'}); ok {
		t.Fatalf("a binding of a verb the catalog does not serve ran: %+v", c)
	}
	if items := m.paletteItems(); !slices.ContainsFunc(items, func(it paletteItem) bool { return it.label == "Tidy" }) {
		t.Fatalf("the palette lacks the served verb Tidy")
	}
}

func TestTUIDetailShowsAsksGatesAndRuns(t *testing.T) {
	m := newModel(t)
	m.SetViews(lensCatalog())
	e := lensTask("t-1", "Answer Ana on the export", "doing")
	e.Units = []unit.Unit{ref(7).Unit()}
	e.Asks = []api.AskView{{Item: "i-1", Summary: "Can the export ship today?", Asker: "Ana", Permalink: "https://chat.example/p1",
		Quotes: []string{"we need it before the board meeting"}}}
	e.Gates = []api.GateView{{ID: 31, Task: "t-1", Kind: "plan", Status: "open", Text: "Run the follow-up round?",
		Intents: []api.IntentView{{ID: "go", Label: "Go"}, {ID: "hold", Label: "Hold"}, {ID: "steer", Label: "Steer", Answer: true}}}}
	e.Runs = []api.RunView{{Seq: 4, Intent: "follow-up", State: "running", Manager: "Orca", Workspace: "acme-api-7", SessionState: "working",
		Resume: "agent --resume s-1"}}
	m.SetTasks("stalled", []api.TaskView{e}, api.Counters{})
	reads := uint64(1)
	if c, ok := m.Key(Key{Name: KeyEnter, Read: reads}); !ok || !c.fetch {
		t.Fatalf("Enter: %+v", c)
	}
	m.SetDetail("t-1", e, nil)
	screen := screenOf(m, 120, 40)
	for _, want := range []string{"Doing", "Can the export ship today? · Ana", "> we need it before the board meeting", "https://chat.example/p1",
		"Run the follow-up round?", "1. Go", "2. Hold", "3. Steer", "follow-up · running · session working", "Orca · acme-api-7",
		"resume: agent --resume s-1"} {
		if !strings.Contains(screen, want) {
			t.Fatalf("detail lacks %q:\n%s", want, screen)
		}
	}

	reads++
	c, ok := m.Key(Key{Rune: '2', Read: reads})
	if !ok || c.gate != 31 || c.intent != "hold" || c.args != nil {
		t.Fatalf("2 on the gate: %+v %v", c, ok)
	}
	d := &fakeDaemon{}
	if _, err := carry(context.Background(), Config{Daemon: d}, c); err != nil {
		t.Fatal(err)
	}
	if want := []gateResolve{{31, "hold", "tui", nil}}; !reflect.DeepEqual(d.resolves, want) {
		t.Fatalf("resolved %+v", d.resolves)
	}

	reads++
	if _, ok := m.Key(Key{Rune: '3', Read: reads}); ok || m.prompt == nil {
		t.Fatal("an intent that takes an answer did not ask for it")
	}
	c, ok = press(m, append(typed("look at the retries"), Key{Name: KeyEnter})...)
	if !ok || c.gate != 31 || c.intent != "steer" || c.args["answer"] != "look at the retries" {
		t.Fatalf("3 then text: %+v %v", c, ok)
	}

	q := e
	msg, _ := json.Marshal(api.Message{ID: 5, Kind: "question", Text: "Which window?"})
	q.Gates = []api.GateView{{ID: 32, Kind: "question", Status: "open", Subject: msg,
		Intents: []api.IntentView{{ID: "answer", Label: "Answer", Answer: true}, {ID: "dismiss", Label: "Dismiss"}}}}
	m.SetDetail("t-1", q, nil)
	if screen := screenOf(m, 120, 40); !strings.Contains(screen, "Which window?") || !strings.Contains(screen, "2. Dismiss") {
		t.Fatalf("a question gate with no choices does not offer its intents:\n%s", screen)
	}
}

func TestAViewSaysWhatItListsInTheHelpAndWhatItShowsEmpty(t *testing.T) {
	m := newModel(t)
	cat := lensCatalog()
	cat.Views[0].Summary, cat.Views[0].Empty = "Tasks waiting too long.", "Nothing is stalled."
	m.SetViews(cat)
	m.SetKeys(append(slices.Clone(m.keys), api.KeyView{Key: "g t", Action: "nav:stalled", Context: ctxGlobal, Owner: "core", Help: "Stalled", Summary: "Tasks waiting too long."}))
	m.SetTasks("stalled", nil, api.Counters{})
	if screen := screenOf(m, 120, 30); !strings.Contains(screen, "Nothing is stalled.") {
		t.Fatalf("the empty view lacks its empty:\n%s", screen)
	}
	m.filter = []rune("zzz")
	if screen := screenOf(m, 120, 30); !strings.Contains(screen, `Nothing in Stalled matches "zzz".`) {
		t.Fatalf("a filter keeps its own words:\n%s", screen)
	}
	m.filter = nil
	press(m, Key{Rune: '?'})
	if screen := squash(screenOf(m, 400, 120)); !strings.Contains(screen, "Tasks waiting too long.") {
		t.Errorf("the help lacks the view's summary:\n%s", screen)
	}
}

func whereTask(id, title, state, where string) api.TaskView {
	e := lensTask(id, title, state)
	e.Facets["where"] = []string{where}
	return e
}

func whereModel(t *testing.T) *Model {
	c := lensCatalog()
	c.Facets = append(c.Facets, api.FacetSpec{ID: "where", Label: "Where", Group: true, Menu: true, Sort: "text", Empty: "nowhere"})
	m := newModel(t)
	m.SetViews(c)
	m.SetTasks("stalled", []api.TaskView{whereTask("t-1", "Ship the export", "doing", "general"), whereTask("t-2", "Call the bank", "parked", "ops"),
		whereTask("t-3", "Read the spec", "doing", "general")}, api.Counters{})
	return m
}

func titles(tasks []api.TaskView) []string {
	var out []string
	for _, e := range tasks {
		out = append(out, e.Title)
	}
	return out
}

func TestTheTUIFiltersATaskViewByQualifier(t *testing.T) {
	m := whereModel(t)
	filterBy(m, "where:general")
	if got := titles(m.visibleTasks()); !slices.Equal(got, []string{"Read the spec", "Ship the export"}) && !slices.Equal(got, []string{"Ship the export", "Read the spec"}) {
		t.Fatalf("where:general keeps %v", got)
	}
	press(m, Key{Name: KeyEsc})
	filterBy(m, "-where:general")
	if got := titles(m.visibleTasks()); !slices.Equal(got, []string{"Call the bank"}) {
		t.Fatalf("-where:general keeps %v", got)
	}
	press(m, Key{Name: KeyEsc})
	filterBy(m, "where:general spec")
	if got := titles(m.visibleTasks()); !slices.Equal(got, []string{"Read the spec"}) {
		t.Fatalf("a qualifier and a word keep %v", got)
	}
	press(m, Key{Name: KeyEsc})
	filterBy(m, "nowhere:general")
	if got := m.visibleTasks(); len(got) != 0 {
		t.Fatalf("an unknown field is a free word nothing holds, kept %v", titles(got))
	}
}

func TestTheTUIFiltersARowViewByQualifier(t *testing.T) {
	m := newModel(t)
	a, b := row(7, "Batch export", "review", "needs-me"), row(8, "Cache warmup", "review", "needs-me")
	a.Facets["where"], b.Facets["where"] = []string{"acme/api"}, []string{"acme/web"}
	m.SetData([]api.Row{a, b}, api.Health{})
	filterBy(m, "where:acme/web")
	if got := m.visible(); len(got) != 1 || got[0].Unit != b.Unit {
		t.Fatalf("where:acme/web keeps %v", got)
	}
	press(m, Key{Name: KeyEsc})
	filterBy(m, "-where:web")
	if got := m.visible(); len(got) != 1 || got[0].Unit != a.Unit {
		t.Fatalf("-where:web keeps %v", got)
	}
}

func TestTheTUIFiltersARowViewByThePluginsFacetsAlone(t *testing.T) {
	m := whereModel(t)
	a, b := row(7, "Batch export", "review", "needs-me"), row(8, "Cache warmup", "review", "needs-me")
	a.Facets["where"], b.Facets["where"] = []string{"acme/api"}, []string{"acme/web"}
	m.SetData([]api.Row{a, b}, api.Health{})
	m.view = 0
	filterBy(m, "where:web")
	if got := m.visible(); len(got) != 1 || got[0].Unit != b.Unit {
		t.Fatalf("where:web matches the plugin's suffix where, kept %v", got)
	}
}

func TestTheTUIGroupsByAnOfferedField(t *testing.T) {
	m := whereModel(t)
	press(m, Key{Rune: '='})
	if want := "group: Where"; m.status != want {
		t.Fatalf("status %q, want %q", m.status, want)
	}
	screen := screenOf(m, 120, 20)
	general, ops := strings.Index(screen, " general\n"), strings.Index(screen, " ops\n")
	if general < 0 || ops < general || strings.Index(screen, "Read the spec") > ops || strings.Index(screen, "Call the bank") < ops {
		t.Fatalf("tasks are not under their where heads:\n%s", screen)
	}
	press(m, Key{Rune: '='})
	if want := "group: None"; m.status != want {
		t.Fatalf("status %q, want %q", m.status, want)
	}
	if screen := screenOf(m, 120, 20); strings.Contains(screen, " general\n") || strings.Contains(screen, " Parked\n") {
		t.Fatalf("a view picked to no group still shows heads:\n%s", screen)
	}
	press(m, Key{Rune: '='})
	if want := "group: State (default)"; m.status != want {
		t.Fatalf("status %q, want %q", m.status, want)
	}
	if screen := screenOf(m, 120, 20); !strings.Contains(screen, " Parked\n") {
		t.Fatalf("the view's own group is back, without its heads:\n%s", screen)
	}
}

func TestTheTUIOffersOnlyTheViewsFields(t *testing.T) {
	m := whereModel(t)
	var seen []string
	for range 5 {
		press(m, Key{Rune: '='})
		seen = append(seen, m.status)
	}
	want := []string{"group: Where", "group: None", "group: State (default)", "group: Where", "group: None"}
	if !slices.Equal(seen, want) {
		t.Fatalf("the group menu went %q, want %q", seen, want)
	}
	m.SetTasks("stalled", []api.TaskView{lensTask("t-1", "Ship the export", "doing")}, api.Counters{})
	seen = nil
	for range 3 {
		press(m, Key{Rune: '='})
		seen = append(seen, m.status)
	}
	if want := []string{"group: State (default)", "group: None", "group: State (default)"}; !slices.Equal(seen, want) {
		t.Fatalf("with no task holding where the menu went %q, want %q", seen, want)
	}
}

func TestTheTUISortsByAnOfferedField(t *testing.T) {
	m := whereModel(t)
	press(m, Key{Rune: '='}, Key{Rune: '='}, Key{Rune: '-'})
	if want := "sort: Where"; m.status != want {
		t.Fatalf("status %q, want %q", m.status, want)
	}
	if got := titles(m.visibleTasks()); !slices.Equal(got, []string{"Ship the export", "Read the spec", "Call the bank"}) {
		t.Fatalf("sorted by where: %v", got)
	}
	press(m, Key{Rune: '-'})
	if want := "sort: Where (descending)"; m.status != want {
		t.Fatalf("status %q, want %q", m.status, want)
	}
	if got := titles(m.visibleTasks()); !slices.Equal(got, []string{"Call the bank", "Ship the export", "Read the spec"}) {
		t.Fatalf("sorted by where descending: %v", got)
	}
	press(m, Key{Rune: '-'})
	if want := "sort: View order"; m.status != want {
		t.Fatalf("status %q, want %q", m.status, want)
	}
}

func TestTheTUISaysTheLensWordsOfTheProfile(t *testing.T) {
	c := lensCatalog()
	c.Facets = append(c.Facets, api.FacetSpec{ID: "where", Label: "Where", Group: true, Menu: true, Sort: "text", Empty: "nowhere"})
	c.Lens.Grouped, c.Lens.Sorted, c.Lens.Descending, c.Lens.NoLens = "Grouped by {{.label}}", "Sorted by {{.label}}", "{{.label}}, last first", "No lens here"
	m := newModel(t)
	m.SetViews(c)
	m.SetTasks("stalled", []api.TaskView{whereTask("t-1", "Ship the export", "doing", "general"), whereTask("t-2", "Call the bank", "parked", "ops")}, api.Counters{})
	var seen []string
	for _, k := range []rune{'=', '-', '-', '-'} {
		press(m, Key{Rune: k})
		seen = append(seen, m.status)
	}
	if want := []string{"Grouped by Where", "Sorted by Where", "Sorted by Where, last first", "Sorted by View order"}; !slices.Equal(seen, want) {
		t.Fatalf("the lens said %q, want %q", seen, want)
	}
	m.view = m.planView()
	m.cycleGroup()
	if m.status != "No lens here" {
		t.Fatalf("on the plan the lens said %q", m.status)
	}
}

func bundledFacets(t *testing.T) []api.FacetSpec {
	t.Helper()
	fsys := os.DirFS("../../../profiles/default")
	views, problems := intentload.BundledViews(fsys)
	if len(problems) > 0 {
		t.Fatal(problems.Error())
	}
	tasks, problems := intentload.BundledTasks(fsys)
	if len(problems) > 0 {
		t.Fatal(problems.Error())
	}
	return catalog.TaskFacets(views.Facets, tasks, []string{"calls", "computer"})
}

func TestTheTUIGroupsStatesAndContextsAsTheBundledProfileDeclares(t *testing.T) {
	m := newModel(t)
	c := lensCatalog()
	c.Facets = bundledFacets(t)
	c.Views = []api.TaskViewSpec{{ID: "stalled", Label: "Stalled", Rail: true}}
	m.SetViews(c)
	a, b, d, e := lensTask("t-1", "Done one", "done"), lensTask("t-2", "Inbox one", "inbox"), lensTask("t-3", "Next one", "next"), lensTask("t-4", "Other", "next")
	d.Facets["contexts"], e.Facets["contexts"] = []string{"computer"}, []string{"calls"}
	m.SetTasks("stalled", []api.TaskView{a, b, d, e}, api.Counters{})
	heads := func(group string) []string {
		m.picks = map[string]lensPick{"task:stalled": {group: group}}
		tasks, hs := m.arrangedTasks()
		var out []string
		for i, h := range hs {
			if h != "" {
				out = append(out, h+":"+tasks[i].ID)
			}
		}
		return out
	}
	if got, want := heads("state"), []string{"Inbox:t-2", "Next:t-3", "Done:t-1"}; !slices.Equal(got, want) {
		t.Errorf("state heads %v, want %v", got, want)
	}
	if got, want := heads("contexts"), []string{"calls:t-4", "computer:t-3", "no context:t-1"}; !slices.Equal(got, want) {
		t.Errorf("context heads %v, want %v", got, want)
	}
}

func TestTheTUIShowsAnItemUnderEachOfItsValues(t *testing.T) {
	m := whereModel(t)
	c := lensCatalog()
	c.Facets = append(c.Facets, api.FacetSpec{ID: "tag", Label: "Tag", Match: "prefix", Menu: true})
	m.SetViews(c)
	a, b := lensTask("t-1", "Ship the export", "doing"), lensTask("t-2", "Call the bank", "parked")
	a.Facets["tag"], b.Facets["tag"] = []string{"area:billing", "area:ops"}, []string{"area:ops"}
	m.SetTasks("stalled", []api.TaskView{a, b}, api.Counters{})
	m.picks = map[string]lensPick{"task:stalled": {group: "tag:area"}}
	tasks, heads := m.arrangedTasks()
	var got []string
	for i, h := range heads {
		got = append(got, h+"|"+tasks[i].ID)
	}
	if want := []string{"area:billing|t-1", "area:ops|t-1", "|t-2"}; !slices.Equal(got, want) {
		t.Fatalf("groups %q, want %q", got, want)
	}
	press(m, Key{Rune: 'j'}, Key{Rune: 'j'})
	if sel, _ := m.selectedTask(); sel.ID != "t-2" || m.taskAt != 2 {
		t.Fatalf("the cursor did not walk past the repeated task: %q at %d", sel.ID, m.taskAt)
	}
}

func TestTheTUIBuildsALensItemOncePerArrival(t *testing.T) {
	m := whereModel(t)
	first, _ := m.lensViewOnScreen()
	m.visibleTasks()
	again, _ := m.lensViewOnScreen()
	if &first.items[0] != &again.items[0] {
		t.Fatal("the lens items were rebuilt")
	}
	m.SetData([]api.Row{row(7, "Batch export", "review", "needs-me")}, api.Health{})
	a, _ := m.rowsOf("needs-me")
	b, _ := m.rowsOf("needs-me")
	if len(a) != 1 || len(b) != 1 {
		t.Fatalf("rows %d %d", len(a), len(b))
	}
	_, x := m.rowsOf("needs-me")
	_, y := m.rowsOf("needs-me")
	if &x[0].Texts[0] != &y[0].Texts[0] {
		t.Fatal("the row items were rebuilt")
	}
}
