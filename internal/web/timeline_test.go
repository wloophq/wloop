package web

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
)

func TestEventLabelReadsTheCatalog(t *testing.T) {
	var got struct {
		Label, Unknown, Tone, Review string
		Loop, Other, Me              bool
	}
	nodeRun(t, jsConst(t, appJS(t), "catalog"), []string{"applyCatalog", "fieldOf", "eventLabel", "eventTone", "loopEvent"},
		`applyCatalog({ events: [{ provider: "", kind: "held", label: "pausado", tone: "me" }, { provider: "", kind: "turn_sent", label: "turno enviado", loop: true, tone: "loop" },
  { provider: "acme", kind: "review", label: "revisou", tone: "people" }] });
process.stdout.write(JSON.stringify({ label: eventLabel("held"), tone: eventTone("held"), review: eventTone("review"), loop: loopEvent("turn_sent"), me: loopEvent("held"),
  unknown: eventLabel("some_kind"), other: loopEvent("some_kind") }));`, nil, &got)
	if got.Label != "pausado" || got.Tone != "me" || got.Review != "people" || !got.Loop || got.Me || got.Unknown != "some kind" || got.Other {
		t.Fatalf("%+v", got)
	}
	for _, gone := range []string{"EVENT_LABEL", "LOOP_EVENTS", "loopEvents"} {
		if strings.Contains(appJS(t), gone) {
			t.Errorf("app.js still has %s; an event kind's words and tone are the catalog's", gone)
		}
	}
}

func TestTheActivityDrawsEachEventInItsServedTone(t *testing.T) {
	var got []string
	nodeRun(t, treeJS+jsConst(t, appJS(t), "catalog")+`
const state = { activity: [], activityLoaded: true };
let activityLoopOnly = false, activitySelected = "";
const list = h("ol", {});
list.replaceChildren = (...c) => { list.children = c; };
const document = { getElementById: () => list };
function t(key) { return key; }
function readHash() { return { view: "recent", pr: "", q: "" }; }
function activityPasses() { return true; }
function dayLabel() { return "Today"; }
function keyOf(e) { return e.key; }
function clock(at) { return at; }
function actorLabel(a) { return a; }
function eventRef() { return "acme/api#8"; }
function writeHash() {}
function activityEmptyNote() { return []; }
`, []string{"applyCatalog", "fieldOf", "eventLabel", "eventTone", "loopEvent", "eventData", "renderActivity"}, `
applyCatalog({ events: [{ provider: "", kind: "held", label: "paused", tone: "me" }, { provider: "", kind: "turn_sent", label: "turn sent", loop: true, tone: "loop" }] });
state.activity = [{ event: { kind: "held", key: "a", at: "2026-10-27T15:00:00Z" }, title: "Retry flaky uploads" },
  { event: { kind: "turn_sent", key: "b", at: "2026-10-27T15:01:00Z" }, title: "Retry flaky uploads" }];
renderActivity({});
const evs = list.children.filter((li) => String(li.attrs.class).includes("a-ev"));
process.stdout.write(JSON.stringify(evs.map((li) => li.attrs.class + "|" + text(li))));`, nil, &got)
	if len(got) != 2 || !strings.HasPrefix(got[0], "a-ev ev-held me|") || !strings.Contains(got[0], "paused") || !strings.HasPrefix(got[1], "a-ev ev-turn_sent loop|") || !strings.Contains(got[1], "turn sent") {
		t.Fatalf("%q", got)
	}
}

func TestTheTimelineTabFoldsRawEventsInEachNode(t *testing.T) {
	var got struct {
		Count            string
		Labels, Summary  []string
		Raw, Tones, Time []string
		Prose            string
	}
	at := time.Date(2026, 10, 27, 9, 39, 0, 0, time.UTC)
	var round []api.TimelineEvent
	for i := range 11 {
		round = append(round, api.TimelineEvent{Seq: int64(i + 1), Kind: "turn_started", Actor: "agent", At: at.Add(time.Duration(i) * time.Minute)})
	}
	round[10].Data = json.RawMessage(`{"text":"Checked the **four** threads."}`)
	nodes := []api.TimelineNode{
		{Rule: "review", Label: "dana reviewed: approved", Tone: "people", At: at.Add(26 * time.Hour), Actor: "dana",
			Events: []api.TimelineEvent{{Seq: 20, Kind: "review", Actor: "dana", At: at.Add(26 * time.Hour)}}},
		{Rule: "round", Label: "Agent round, 10m", Tone: "loop", At: at, End: at.Add(10 * time.Minute), Events: round},
	}
	answers := pageAnswers(t)
	answers[nodesPath("t-0123456789abcdef", "")] = nodes
	runTaskPage(t, `
Date.now = () => Date.parse("2026-10-28T12:00:00Z");
I18N.strings["{n} event"] = { one: "{n} event", other: "{n} events" };
taskState.tab = "timeline";
const box = await page("t-0123456789abcdef");
const tl = cls(box, "tl")[0];
const evs = cls(tl, "ev");
const tab = walk(box, (x) => x.attrs["data-focus"] === "task:tab:timeline")[0];
done({
  Count: cls(tab, "n").map((n) => n.textContent).join(""),
  Labels: evs.map((n) => cls(n, "lb")[0].textContent),
  Summary: walk(tl, (x) => x.tag === "summary").map((n) => n.textContent),
  Raw: walk(tl, (x) => x.tag === "details").map((d) => String(walk(d, (x) => x.tag === "li").length)),
  Tones: evs.map((n) => n.attrs.class),
  Time: evs.map((n) => cls(n, "tm")[0].tag),
  Prose: cls(evs[1], "dim").map((n) => n.textContent).join(""),
});`, answers, &got)
	if strings.Join(got.Labels, "|") != "dana reviewed: approved|Agent round, 10m" {
		t.Fatalf("labels %q", got.Labels)
	}
	if strings.Join(got.Summary, "|") != "11 events" || strings.Join(got.Raw, "|") != "11" {
		t.Errorf("a node folds its raw events, a single one folds none: %q %q", got.Summary, got.Raw)
	}
	if strings.Join(got.Tones, "|") != "ev people|ev loop" || strings.Join(got.Time, "|") != "time|time" {
		t.Errorf("tones %q, times %q", got.Tones, got.Time)
	}
	if got.Prose != "Checked the four threads." {
		t.Errorf("the newest prose, as markdown: %q", got.Prose)
	}
	if got.Count != "12" {
		t.Errorf("the tab counts the events: %q", got.Count)
	}
}

func TestFillWordPicksThePluralForm(t *testing.T) {
	var got []string
	nodeRun(t, staticJS(t, "i18n.js"), []string{"fillWord"}, `
const en = '{{.n}} open {{plural .n "one" "question" "other" "questions"}} in the brief';
const pt = '{{.n}} {{plural .n "one" "pergunta aberta" "other" "perguntas abertas"}}';
const out = [fillWord(en, {n: 1}), fillWord(en, {n: 2})];
I18N.locale = "pt-BR";
out.push(fillWord(pt, {n: 0}), fillWord(pt, {n: 3}), fillWord("{{.unit}}: {{.shown}}", {unit: "Item", shown: 2}));
process.stdout.write(JSON.stringify(out));`, nil, &got)
	want := []string{"1 open question in the brief", "2 open questions in the brief", "0 pergunta aberta", "3 perguntas abertas", "Item: 2"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("%q", got)
	}
}
