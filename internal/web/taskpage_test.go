package web

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/card"
)

func TestTheDetailListsTheProfilesPrimaryVerbsAndTheRestInThePalette(t *testing.T) {
	cat := taskCatalog()
	cat.Verbs = []api.VerbSpec{{Verb: "next", Label: "Next", Key: "x"}, {Verb: "done", Label: "Done", Key: "e"}, {Verb: "waiting", Label: "Waiting", Key: "w"},
		{Verb: "focus", Label: "Focus", Key: "f"}}
	cat.DetailVerbs = []string{"done", "waiting"}
	e := task("t-1", "next", func(e *api.TaskView) {
		e.Verbs = []api.VerbState{{Verb: "next", Enabled: true}, {Verb: "done", Enabled: true}, {Verb: "waiting", Enabled: true}, {Verb: "focus", Enabled: true}}
	})
	var got struct {
		Buttons []string
		Keys    []string
		Palette []string
		Ran     []string
	}
	runTaskPageOn(t, cat, `
let opened = null;
detailEnv.palette = (o) => { opened = o; };
const verbs = h("div", {}, ...primaryVerbs(input.answers.task));
const buttons = walk(verbs, (x) => x.tag === "button");
await buttons[buttons.length - 1].on.click();
await opened.items[0].run();
done({
  Buttons: buttons.map((b) => cls(b, "lb")[0].textContent),
  Keys: buttons.map((b) => walk(b, (x) => x.tag === "kbd").map((k) => k.textContent).join("")),
  Palette: opened.items.map((i) => i.label + " " + i.key),
  Ran: posts.map((p) => p.path),
});`, map[string]any{"task": e}, &got)
	if !slices.Equal(got.Buttons, []string{"Done", "Waiting", "More 2"}) {
		t.Errorf("buttons %q, want the profile's verbs, then More over the rest", got.Buttons)
	}
	if !slices.Equal(got.Keys, []string{"e", "w", "⌘K"}) {
		t.Errorf("keys %q", got.Keys)
	}
	if !slices.Equal(got.Palette, []string{"Next x", "Focus f"}) || !slices.Equal(got.Ran, []string{"/api/tasks/t-1/next"}) {
		t.Errorf("the palette %q ran %q", got.Palette, got.Ran)
	}
}

// tabsOf is the tabs a task shows and the one selected, after taskState.tab is set to picked.
func tabsOf(t *testing.T, e api.TaskView, picked string) (tabs []string, selected string) {
	t.Helper()
	var got struct {
		Tabs     []string
		Selected string
		Strip    []string
	}
	runTaskPage(t, `
taskState.tab = input.answers.picked;
const out = taskTabs(input.answers.task);
done({ Tabs: out.tabs.map((x) => x.id), Selected: out.selected,
  Strip: walk(out.body, (x) => x.attrs.role === "tab" && x.attrs["aria-selected"] === "true").map((x) => x.attrs["data-focus"]) });`,
		map[string]any{"task": e, "picked": picked}, &got)
	if !slices.Equal(got.Strip, []string{"task:tab:" + got.Selected}) {
		t.Errorf("the strip selects %q, the page %q", got.Strip, got.Selected)
	}
	return got.Tabs, got.Selected
}

func TestEmptyTabsDoNotShow(t *testing.T) {
	tabs, selected := tabsOf(t, task("t-1", "next", nil), "")
	if !slices.Equal(tabs, []string{"timeline"}) || selected != "timeline" {
		t.Errorf("a task with nothing in its tabs shows %q selected %q, want the timeline alone", tabs, selected)
	}
	tabs, _ = tabsOf(t, pageTask(t), "")
	if !slices.Equal(tabs, []string{"brief", "timeline", "asks", "related", "runs"}) {
		t.Errorf("a task with something in each tab shows %q", tabs)
	}
}

func TestAVanishedTabFallsBackToTheFirst(t *testing.T) {
	e := pageTask(t)
	e.Runs = nil
	if tabs, selected := tabsOf(t, e, "runs"); slices.Contains(tabs, "runs") || selected != "brief" {
		t.Errorf("tabs %q selected %q, want the first shown once the Runs tab empties", tabs, selected)
	}
	if _, selected := tabsOf(t, e, "asks"); selected != "asks" {
		t.Errorf("selected %q, want the tab picked while it shows", selected)
	}
}

func TestPropsShowRiskWithItsReasonAndTheCardsSignals(t *testing.T) {
	e := pageTask(t)
	e.Brief = &api.RecordView{Kind: "brief", Data: raw(t, map[string]any{"risk": "high", "risk_why": "Data loss", "tags": []string{"lang:go", "area:api", "team:core"}})}
	e.Cards[0].Card.Signals = []card.Signal{{Key: "checks", Value: "failing", Tone: "bad"}}
	cat := taskCatalog()
	cat.Facets = []api.FacetSpec{{ID: "tag", Fact: "brief.tags", ShowMax: 2}}
	var got struct {
		Rows [][]string
		Tone string
		Tab  string
	}
	runTaskPageOn(t, cat, `
const props = taskProps(input.answers.task);
const dts = walk(props, (x) => x.tag === "dt"), dds = walk(props, (x) => x.tag === "dd");
await cls(props, "more")[0].on.click();
done({ Rows: dts.map((d, i) => [d.textContent, dds[i].textContent]), Tone: dds[dts.findIndex((d) => d.textContent === "checks")].attrs.class, Tab: taskState.tab });`,
		map[string]any{"task": e}, &got)
	rows := map[string]string{}
	for _, r := range got.Rows {
		rows[r[0]] = r[1]
	}
	if r := rows["Risk"]; !strings.Contains(r, "high") || !strings.Contains(r, "Data loss") {
		t.Errorf("risk reads %q, want the brief's risk with its reason", r)
	}
	if rows["checks"] != "failing" || got.Tone != "tone-bad" {
		t.Errorf("the card's signal reads %q in %q", rows["checks"], got.Tone)
	}
	if rows["Tags"] != "lang:goarea:api+1" || got.Tab != "brief" {
		t.Errorf("tags read %q, the rest opened %q: want two tags, the count of the rest opening the Brief tab", rows["Tags"], got.Tab)
	}
}

func TestATaskWithAPendingDraftShowsItsCard(t *testing.T) {
	d := pendingDraft()
	d.Task = "t-0123456789abcdef"
	e := pageTask(t)
	e.Drafts = []int64{d.ID}
	e.Gates = append(e.Gates, api.GateView{ID: d.Gate, Task: e.ID, Kind: "draft", Name: "draft", Status: "open", Subject: raw(t, d),
		Intents: []api.IntentView{{ID: "approve", Label: "Approve"}, {ID: "discard", Label: "Discard"}}})
	answers := pageAnswers(t)
	answers["/api/tasks/"+e.ID] = e
	answers["/api/drafts?task="+e.ID] = api.DraftsResponse{Drafts: []api.Draft{d}}
	var got struct {
		Cards []string
		Gates []string
		Asked []string
		Posts []any
	}
	runTaskPage(t, `
const box = await page("t-0123456789abcdef");
await focus(box, "detail:approve:draft:7").on.click();
done({ Cards: cls(box, "d-draft").map((c) => c.textContent), Gates: cls(box, "d-gate").map((g) => g.attrs["data-gate"]), Asked: asked, Posts: posts });`, answers, &got)
	if len(got.Cards) != 1 || !strings.Contains(got.Cards[0], "Any news on the retry change?") {
		t.Errorf("draft cards %q, want the pending draft's", got.Cards)
	}
	if slices.Contains(got.Gates, "12") {
		t.Errorf("gates %q: the draft's gate shows beside its card", got.Gates)
	}
	if !slices.Contains(got.Asked, "/api/drafts?task=t-0123456789abcdef") {
		t.Errorf("the page asked %q, want the task's drafts", got.Asked)
	}
	want := []any{map[string]any{"path": "/api/gates/12/approve", "body": map[string]any{"source": "cockpit", "args": map[string]any{"rev": "3"}}}}
	if mustJS(t, got.Posts) != mustJS(t, want) {
		t.Errorf("approving posted %v, want %v", got.Posts, want)
	}
}

func TestATaskWithoutDraftsAsksForNone(t *testing.T) {
	var got []string
	runTaskPage(t, `await page("t-0123456789abcdef"); done(asked);`, pageAnswers(t), &got)
	for _, u := range got {
		if strings.HasPrefix(u, "/api/drafts") {
			t.Errorf("a task without pending drafts asked %q", u)
		}
	}
}

// TestBracketsSwitchTabsOnlyInTheDetail checks that ] and [ switch the task page's tabs when the focus is in
// the detail, and step the review when it is not, in the review mode.
func TestBracketsSwitchTabsOnlyInTheDetail(t *testing.T) {
	var got struct{ Calls []string }
	src := strings.Replace(staticJS(t, "keys.js"), `if (typeof document !== "undefined" && typeof state !== "undefined") installKeys(appEnv());`, "", 1)
	bindings, _ := json.Marshal(webBindings(t))
	runTaskPage(t, strings.Replace(src, `"use strict";`, "", 1)+`
const calls = [];
const keys = createKeys({
  selected: () => null, verbs: () => [],
  viewContext: () => "review",
  step: (a) => { calls.push("step:" + a); return true; },
  detailAt: (el) => el.inDetail,
  tab: (dir) => { calls.push("tab:" + dir); return true; },
});
keys.setBindings(`+string(bindings)+`);
const press = (key, inDetail) => keys.handle({ key, target: { tagName: "DIV", inDetail }, defaultPrevented: false, preventDefault() {} });
await press("]", false);
await press("[", false);
await press("]", true);
await press("[", true);
done({ Calls: calls });`, pageAnswers(t), &got)
	if want := []string{"step:next", "step:prev", "tab:next", "tab:prev"}; !slices.Equal(got.Calls, want) {
		t.Errorf("calls %q, want the review's steps outside the detail and the tabs inside it: %q", got.Calls, want)
	}
}

func TestArrowKeysMoveAlongTheTabs(t *testing.T) {
	var got []string
	runTaskPage(t, `
taskState.tab = "";
const box = await page("t-0123456789abcdef");
const strip = cls(box, "tabs")[0];
const seen = [];
for (const key of ["ArrowRight", "ArrowRight", "ArrowLeft", "ArrowLeft", "ArrowLeft"]) {
  strip.on.keydown({ key, preventDefault() {} });
  seen.push(taskState.tab);
}
detailState.task = await detailEnv.getJSON("/api/tasks/t-0123456789abcdef");
detailPane.tab("prev");
seen.push(taskState.tab);
done(seen);`, pageAnswers(t), &got)
	if want := []string{"timeline", "asks", "timeline", "brief", "runs", "related"}; !slices.Equal(got, want) {
		t.Errorf("tabs %q, want %q", got, want)
	}
}

func TestPropsUseTheProfilesWords(t *testing.T) {
	e := pageTask(t)
	e.Props = map[string]json.RawMessage{"kind": raw(t, "capture"), "priority": raw(t, "high"), "owner": raw(t, map[string]string{"key": "github:ana", "name": "Ana"})}
	e.Brief = &api.RecordView{Kind: "brief", Data: raw(t, map[string]any{"risk": "high", "risk_why": "Perda de dados", "size": "m"})}
	cat := taskCatalog()
	cat.Props = []api.PropSpec{{Name: "kind", Type: "enum", Values: []string{"start", "capture"}}, {Name: "priority", Type: "enum", Values: []string{"low", "high"}}, {Name: "owner", Type: "actor"}}
	cat.Facets = []api.FacetSpec{
		{ID: "prop.kind", Fact: "task.prop.kind", Label: "Tipo", Labels: map[string]string{"capture": "captura"}},
		{ID: "prop.priority", Fact: "task.prop.priority", Label: "Prioridade"},
		{ID: "brief-risk", Fact: "brief.risk", Label: "Risco", Labels: map[string]string{"high": "alto"}},
		{ID: "brief-size", Fact: "brief.size", Label: "Tamanho", Labels: map[string]string{"m": "M"}},
	}
	var got [][]string
	runTaskPageOn(t, cat, `
const props = taskProps(input.answers.task);
const dts = walk(props, (x) => x.tag === "dt"), dds = walk(props, (x) => x.tag === "dd");
done(dts.map((d, i) => [d.textContent, dds[i].textContent]));`, map[string]any{"task": e}, &got)
	want := [][]string{{"Tipo", "captura"}, {"Prioridade", "high"}, {"owner", "Ana"}, {"Risco", "alto · Perda de dados"}, {"Tamanho", "M"}}
	if mustJS(t, got) != mustJS(t, want) {
		t.Errorf("props %v, want %v", got, want)
	}
}

func TestTheDetailShowsEachSignalOnce(t *testing.T) {
	e := pageTask(t)
	e.Cards[0].Card.Signals = []card.Signal{{Key: "checks", Value: "failing", Tone: "bad"}}
	answers := pageAnswers(t)
	answers["/api/tasks/"+e.ID] = e
	var got []string
	runTaskPage(t, `
const box = await page("t-0123456789abcdef");
done(walk(box, (x) => x.children.some((c) => c.data !== undefined && c.data.includes("failing"))).map((x) => x.tag));`, answers, &got)
	if !slices.Equal(got, []string{"dd"}) {
		t.Errorf("the signal shows in %q, want one dd", got)
	}
}
