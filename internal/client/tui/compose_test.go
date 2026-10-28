package tui

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/card"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestDelegateTabHandsToAnAgent(t *testing.T) {
	m := viewsModel(t)
	press(m, Key{Rune: 'd'})
	if m.prompt == nil || !strings.Contains(m.prompt.label, "Tab: hand to an agent") {
		t.Fatalf("d did not ask: %+v", m.prompt)
	}
	press(m, Key{Name: KeyTab})
	if !strings.HasPrefix(m.prompt.label, "hand ") {
		t.Fatalf("Tab did not switch: %q", m.prompt.label)
	}
	c, ok := press(m, append(typed("look at the logs"), Key{Name: KeyEnter})...)
	if want := map[string]string{"guidance": "look at the logs", "mode": "session"}; !ok || c.verb != "delegate" || !reflect.DeepEqual(c.args, want) {
		t.Fatalf("%+v %v", c, ok)
	}
	m.Done(c, nil, nil)
	press(m, Key{Rune: 'd'}, Key{Name: KeyTab}, Key{Name: KeyTab})
	if c, ok := press(m, Key{Name: KeyEnter}); !ok || c.args != nil {
		t.Fatalf("Tab twice: %+v %v", c, ok)
	}
}

func TestDelegateSaysWhyNoAgentSessionCanRun(t *testing.T) {
	m := viewsModel(t)
	focus := viewTasks()["focus"][0]
	focus.Verbs = append(focus.Verbs, api.VerbState{Verb: "delegate", Enabled: true, Runners: []api.RunnerState{
		{Runner: "headless", Enabled: true}, {Runner: "session", Reason: "no workspace manager is enabled"}}})
	m.SetTasks("focus", []api.TaskView{focus}, viewCounters())
	press(m, Key{Rune: 'd'})
	if m.prompt == nil || !strings.Contains(m.prompt.label, "no agent session: no workspace manager is enabled") {
		t.Fatalf("d did not say why: %+v", m.prompt)
	}
	press(m, Key{Name: KeyTab})
	if c, ok := press(m, Key{Name: KeyEnter}); !ok || c.args != nil {
		t.Fatalf("Tab switched to a session anyway: %+v %v", c, ok)
	}
}

func TestReplySavesOrSendsAfterAy(t *testing.T) {
	m := viewsModel(t)
	press(m, Key{Rune: 'r'})
	if m.prompt == nil || !strings.Contains(m.prompt.label, "saved to the Outbox") {
		t.Fatalf("r did not ask: %+v", m.prompt)
	}
	press(m, Key{Name: KeyEnter})
	if !strings.Contains(m.status, "cancelled") {
		t.Fatalf("an empty reply: %q", m.status)
	}
	press(m, Key{Rune: 'r'}, Key{Name: KeyTab})
	if _, ok := press(m, append(typed("Shipped"), Key{Name: KeyEnter})...); ok || m.prompt == nil || !m.prompt.confirm || !strings.Contains(m.prompt.label, "now?") {
		t.Fatalf("send now did not ask for a y: %+v", m.prompt)
	}
	c, ok := press(m, Key{Rune: 'y'})
	if want := map[string]string{"text": "Shipped", "send": "now"}; !ok || c.verb != "reply" || c.task != "e1" || !reflect.DeepEqual(c.args, want) {
		t.Fatalf("%+v %v", c, ok)
	}
}

func TestCtrlEHandsThePromptToTheEditor(t *testing.T) {
	m := viewsModel(t)
	press(m, Key{Rune: 'r'})
	press(m, append(typed("First"), Key{Name: KeyCtrlE})...)
	e := m.TakeEdit()
	if e == nil || e.Text != "First" || m.prompt != nil || m.TakeEdit() != nil {
		t.Fatalf("edit %+v, prompt %+v", e, m.prompt)
	}
	c, ok := m.Edited(e, "First line\r\n\nSecond line\x07\n", nil)
	if want := map[string]string{"text": "First line\n\nSecond line", "send": "draft"}; !ok || c.verb != "reply" || !reflect.DeepEqual(c.args, want) {
		t.Fatalf("%+v %v", c, ok)
	}
	if _, ok := m.Edited(e, "", errors.New("vi exited 1")); ok || !strings.Contains(m.status, "the editor failed: vi exited 1") {
		t.Fatalf("a failed editor: %q", m.status)
	}
	if _, err := edit(&fakeTerm{}, "x"); err == nil || !strings.Contains(err.Error(), "no editor here") {
		t.Fatalf("a terminal with no editor: %v", err)
	}
}

func TestEditADraftFromItsDetail(t *testing.T) {
	m := viewsModel(t)
	chord(m, "go")
	press(m, Key{Name: KeyEnter})
	if _, ok := press(m, Key{Rune: 'i'}); ok || m.prompt != nil {
		t.Fatalf("a draft of two lines asked on the bottom line: %+v", m.prompt)
	}
	e := m.TakeEdit()
	if e == nil || e.Text != "Ship it tonight.\nRollback plan in the doc." {
		t.Fatalf("edit %+v", e)
	}
	c, ok := m.Edited(e, "Ship it tomorrow.", nil)
	if !ok || c.task != "draft:11" || c.verb != "edit" || c.args["text"] != "Ship it tomorrow." {
		t.Fatalf("%+v %v", c, ok)
	}

	one := task("draft:12", "Thanks", "inbox")
	one.Kind, one.Verbs = "draft", []api.VerbState{{Verb: "edit", Enabled: true}}
	one.Gates = draftGates(api.Draft{ID: 12, Rev: 1, Kind: "reply", Purpose: "reply", Text: "Thanks!", State: "held"})
	m.SetTasks("outbox", []api.TaskView{one}, viewCounters())
	m.Done(c, nil, nil)
	press(m, Key{Name: KeyEnter}, Key{Rune: 'i'})
	if m.prompt == nil || string(m.prompt.text) != "Thanks!" || !m.prompt.editor {
		t.Fatalf("prompt %+v", m.prompt)
	}
	c, ok = press(m, append(typed(" Merged."), Key{Name: KeyEnter})...)
	if !ok || c.task != "draft:12" || c.args["text"] != "Thanks! Merged." {
		t.Fatalf("%+v %v", c, ok)
	}
}

func TestTaskDetailListsAndPicksItsDrafts(t *testing.T) {
	m := viewsModel(t)
	e := m.tasks["focus"][0]
	press(m, Key{Name: KeyEnter})
	m.SetDetail(e.ID, e, nil)
	m.SetDrafts(e.ID, []api.Draft{
		{ID: 4, Task: e.ID, Kind: "reply", Purpose: "reply", Text: "Friday works", State: "held", Rev: 2, Where: "#ops thread", Origin: api.DraftOrigin{Kind: "turn"},
			Gate: 6, Intents: map[string]string{"approved": "approve", "rejected": "discard"}, Actions: []api.ActionState{{ID: "draft.edit", Enabled: true}}},
		{ID: 3, Task: e.ID, Kind: "react", Purpose: "react", Reaction: "eyes", State: "sent", Actions: []api.ActionState{{ID: "draft.edit", Reason: "the draft is sent"}}},
	})
	screen := screenOf(m, 100, 40)
	for _, s := range []string{"drafts", "1. reply · held · from turn", "Friday works", "2. react · sent", ":eyes:"} {
		if !strings.Contains(screen, s) {
			t.Fatalf("the detail lacks %q:\n%s", s, screen)
		}
	}
	press(m, Key{Rune: '3'})
	if m.picked != 0 || m.status != "no draft 3" {
		t.Fatalf("3: picked %d, %q", m.picked, m.status)
	}
	press(m, Key{Rune: '2'}, Key{Rune: 'a'})
	if m.prompt != nil || !strings.Contains(m.status, "the draft is sent") {
		t.Fatalf("approve on a sent draft: %+v %q", m.prompt, m.status)
	}
	press(m, Key{Rune: '1'})
	if !strings.Contains(screenOf(m, 100, 40), "› 1. reply") {
		t.Fatal("the pick is not marked")
	}
	press(m, Key{Rune: 'a'})
	if m.prompt == nil || !m.prompt.confirm || m.prompt.label != "post to #ops thread?" {
		t.Fatalf("approve did not ask: %+v", m.prompt)
	}
	c, ok := press(m, Key{Rune: 'y'})
	if want := map[string]string{"rev": "2", "text": "Friday works"}; !ok || c.task != "draft:4" || c.verb != "approve" || !reflect.DeepEqual(c.args, want) {
		t.Fatalf("%+v %v", c, ok)
	}
	m.Done(c, nil, nil)
	press(m, Key{Name: KeyEsc})
	if m.picked != 0 || m.detailID == "" {
		t.Fatalf("Esc: picked %d, detail %q", m.picked, m.detailID)
	}
}

func TestTheFilterReadsATasksWords(t *testing.T) {
	e := task("e7", "Rollout date", "inbox")
	e.UnitLabels, e.Units = []string{"#ops"}, []unit.Unit{{Provider: "slack", Key: "T1/C1/1.1"}}
	e.Items = []api.ItemView{{Summary: "Confirm the freeze with Dana"}}
	e.Cards = []api.CardView{{Card: card.Card{LastHuman: &card.Human{Who: items.Person{Name: "Ana Lima"}}}}}
	e = waitingOn(e, "Bruno")
	for q, want := range map[string]bool{"#ops": true, "freeze dana": true, "ana": true, "bruno rollout": true, "zzz": false, "": true} {
		if got := taskMatches(e, q); got != want {
			t.Errorf("%q: %v, want %v", q, got, want)
		}
	}
}

func TestOpeningADetailFetchesItsDrafts(t *testing.T) {
	d := &fakeDaemon{tasks: viewTasks(), drafts: []api.Draft{{ID: 4, Task: "e1"}, {ID: 5, Task: "e9"}}}
	got, err := carry(context.Background(), Config{Daemon: d}, Command{Unit: unit.Unit{}, task: "e1", fetch: true})
	if err != nil || got.task.ID != "e1" || len(got.drafts) != 1 || got.drafts[0].ID != 4 {
		t.Fatalf("%+v %v", got, err)
	}
	got, err = carry(context.Background(), Config{Daemon: d}, Command{task: "draft:11", fetch: true})
	if err != nil || got.task.ID != "draft:11" || got.drafts != nil {
		t.Fatalf("a draft's own detail: %+v %v", got, err)
	}
}

// draftGates are the gates of a task carrying d.
func draftGates(d api.Draft) []api.GateView { return []api.GateView{api.DraftGate(d)} }

func TestADraftTaskOffersTheVerbsThatRunItsHandlers(t *testing.T) {
	cat := api.ViewsCatalog{Verbs: []api.VerbSpec{{Verb: "approve", Handler: "approve"}, {Verb: "dismiss"},
		{Verb: "edit", Handler: "retry"}, {Verb: "retry", Handler: "edit"}}}
	d := api.Draft{ID: 4, State: "failed", Actions: []api.ActionState{{ID: "draft.edit"}, {ID: "draft.retry", Enabled: true}}}
	got := map[string]bool{}
	for _, v := range draftTask(cat, d, "").Verbs {
		got[v.Verb] = v.Enabled
	}
	if want := map[string]bool{"approve": false, "dismiss": true, "edit": true, "retry": false}; !maps.Equal(got, want) {
		t.Fatalf("verbs %v, want %v", got, want)
	}
	held := api.Draft{ID: 5, State: "held", Intents: map[string]string{"approved": "approve", "rejected": "discard"}}
	if vs := draftTask(api.ViewsCatalog{Verbs: []api.VerbSpec{{Verb: "dismiss", Handler: "context"}}}, held, "").Verbs; len(vs) != 0 {
		t.Fatalf("a profile with no approve handler and no own dismiss offers %+v", vs)
	}
}

func TestADraftKeyRunsTheHandlerOfItsVerb(t *testing.T) {
	m := viewsModel(t)
	m.viewsCat.Verbs = []api.VerbSpec{{Verb: "approve", Handler: "approve"}, {Verb: "dismiss"},
		{Verb: "edit", Handler: "retry"}, {Verb: "retry", Handler: "edit"}}
	e := m.tasks["focus"][0]
	d := api.Draft{ID: 4, Task: e.ID, Rev: 3, Kind: "reply", Purpose: "reply", Text: "Friday works", State: "failed",
		Actions: []api.ActionState{{ID: "draft.edit", Enabled: true}, {ID: "draft.retry", Enabled: true}}}
	m.pickedVerb(e, d, "edit", 0)
	if p := m.prompt; p == nil || !p.confirm || !strings.Contains(p.label, "post again") || p.cmd.verb != "edit" || p.cmd.args["rev"] != "3" {
		t.Fatalf("the verb whose handler is retry asked %+v", p)
	}
	m.prompt = nil
	m.pickedVerb(e, d, "retry", 0)
	if p := m.prompt; p == nil || p.confirm || p.action != "edit" || p.cmd.verb != "" && p.cmd.verb != "retry" {
		t.Fatalf("the verb whose handler is edit asked %+v", p)
	}
}
