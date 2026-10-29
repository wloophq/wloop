package menubar

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/api"
)

// lensViews are a profile's views the menu bar knows nothing of beforehand: two badged, one not.
func lensViews() []api.TaskViewSpec {
	return []api.TaskViewSpec{{ID: "stalled", Label: "Stalled"}, {ID: "mine", Label: "Mine", Badge: true}, {ID: "asks", Label: "Asks", Badge: true}}
}

func TestMenubarSectionsComeFromTheProfile(t *testing.T) {
	r := newRig(t)
	r.d.views = lensViews()
	r.d.tasks["mine"] = []api.TaskView{plainTask(1)}
	r.d.tasks["asks"] = []api.TaskView{plainTask(2), plainTask(3)}
	r.d.tasks["stalled"] = []api.TaskView{plainTask(4)}
	r.c.Refresh(context.Background())
	got := titles(r.tray.menu)
	mine, asks := index(got, "Mine (1)"), index(got, "Asks (2)")
	if mine < 0 || asks < mine || index(got, "Stalled") >= 0 || index(got, "Inbox") >= 0 || index(got, "Focus") >= 0 {
		t.Fatalf("menu %q, want the profile's badge views in its order", got)
	}
	it, ok := find(r.tray.menu, "Open cockpit")
	if !ok {
		t.Fatalf("menu %q", got)
	}
	it.Action()
	waitFor(t, func() bool { return slices.Equal(r.openedURLs(), []string{"cockpit#view=mine"}) })

	r.d.mu.Lock()
	r.d.loading = []api.Loading{{Plugin: "acme", Reason: "importing the threads"}}
	r.d.mu.Unlock()
	stale := reviewRow(9)
	stale.Stale = true
	r.d.rows["loop"] = []api.Row{stale}
	r.c.Refresh(context.Background())
	got = titles(r.tray.menu)
	if index(got, "Loading acme: importing the threads") != 0 {
		t.Fatalf("menu %q, want the loading plugin first", got)
	}
	if it, ok := find(r.tray.menu, "acme/api#9 · "); !ok || !strings.HasSuffix(it.Title, staleMark) {
		t.Fatalf("a stale row is not marked: %q", got)
	}
}

func TestBadgeTitleCountsBadgeViews(t *testing.T) {
	r := newRig(t)
	r.d.views = lensViews()
	r.d.counters = api.Counters{Views: map[string]int{"stalled": 9, "mine": 2, "asks": 3}, Badge: 5}
	r.c.Refresh(context.Background())
	if got := r.title(); got != "PR 5" {
		t.Fatalf("title %q, want the count of the badge views", got)
	}
	r.d.mu.Lock()
	r.d.views = []api.TaskViewSpec{{ID: "stalled", Label: "Stalled", Badge: true}}
	r.d.counters = api.Counters{Views: map[string]int{"stalled": 9, "mine": 2, "asks": 3}, Badge: 9}
	r.d.mu.Unlock()
	r.c.Refresh(context.Background())
	if got, menu := r.title(), titles(r.tray.menu); got != "PR 9" || index(menu, "Stalled (0)") < 0 || index(menu, "Mine") >= 0 {
		t.Fatalf("title %q, menu %q after the profile badged another view", got, menu)
	}
}

func TestMenubarNotifiesAnOpenGate(t *testing.T) {
	r := newRig(t)
	r.d.views = lensViews()
	r.d.verbSpecs = []api.VerbSpec{{Verb: "respond", Handler: "answer", Label: "Respond"}, {Verb: "toss", Handler: "drop", Label: "Toss"}}
	e := plainTask(6)
	e.State, e.Category, e.Title = "parked", "waiting", "Pick the window"
	subject, _ := json.Marshal(api.Message{ID: 6, Unit: uref(6), Kind: "question", Text: "Which window?"})
	e.Gates = []api.GateView{{ID: 6, Kind: "question", Status: "open", Subject: subject,
		Intents: []api.IntentView{{ID: "answer", Label: "Answer", Answer: true}, {ID: "dismiss", Label: "Dismiss"}}}}
	e.Attention = &api.Attention{ID: api.NotificationID("Q6", e.ID, "6"), Category: "Q6", Title: "Agent asks", Subtitle: uref(6).Key, Body: "Which window?",
		Actions: []api.AttentionAction{{ID: "answer:text", Title: "Reply", TextInput: true}, {ID: "task:toss", Title: "Toss"}}}
	r.d.tasks["mine"] = []api.TaskView{e}
	r.c.Refresh(context.Background())
	posted := r.notes.snapshot()
	if len(posted) != 1 || posted[0].ID != e.Attention.ID {
		t.Fatalf("posted %+v, want the open gate of a task outside the Inbox", posted)
	}
	r.c.Respond(context.Background(), Response{ID: e.Attention.ID, Identifier: "answer:text", Action: ActionAnswer, Text: "after ten"})
	if v := r.d.snapshotVerbs(); len(v) != 1 || v[0].verb != "respond" || v[0].req.Args["guidance"] != "after ten" {
		t.Fatalf("the answer ran %+v, want the served verb whose handler is answer", v)
	}

	d := draftTask(7)
	d.Attention.Actions = []api.AttentionAction{{ID: "open", Title: "Open"}, {ID: "task:toss", Title: "Toss"}, {ID: "task:approve", Title: "Approve"}}
	r.d.tasks["asks"] = []api.TaskView{d}
	r.c.Refresh(context.Background())
	for _, n := range r.notes.snapshot() {
		if n.ID == d.Attention.ID && !slices.Equal(buttons(n), []string{"open Open", "task:toss Toss"}) {
			t.Fatalf("a draft offers %q, want open and the served verb that drops it", buttons(n))
		}
	}
}
