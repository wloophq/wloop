package tui

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// lensCatalog is a profile's views, verbs and states the terminal knows nothing of beforehand.
func lensCatalog() api.ViewsCatalog {
	return api.ViewsCatalog{
		Views: []api.TaskViewSpec{{ID: "stalled", Label: "Stalled", Group: "task.state"}, {ID: "mine", Label: "Mine", Badge: true},
			{ID: "sends", Label: "Sends", Over: "gates"}},
		States: []api.StateSpec{{Name: "parked", Label: "Parked", Category: "waiting"}, {Name: "doing", Label: "Doing", Category: "active"}},
		Verbs: []api.VerbSpec{{Verb: "park", Handler: "waiting", Label: "Park", Input: "Parked on whom"}, {Verb: "toss", Handler: "drop", Label: "Toss"},
			{Verb: "hand", Handler: "delegate", Label: "Hand", Input: "What should the agent do"}, {Verb: "tidy", Label: "Tidy"}},
	}
}

func lensTask(id, title, state string) api.TaskView {
	e := task(id, title, state)
	e.Verbs = []api.VerbState{{Verb: "park", Enabled: true}, {Verb: "toss", Enabled: true}, {Verb: "hand", Enabled: true}, {Verb: "tidy", Enabled: true}}
	return e
}

func TestTUIListsLensViewsFromTheCatalog(t *testing.T) {
	m := newModel(t)
	m.SetViews(lensCatalog())
	if v, ok := m.viewOnScreen(); !ok || v.ID != "stalled" {
		t.Fatalf("started on %+v, want the catalog's first view", v)
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
