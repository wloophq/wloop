package web

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/record"
)

// questionGate is an open question gate of task t-1 whose message is text, with options, and whose intents are
// answer, which takes text, and dismiss.
func questionGate(t *testing.T, text, recommend string, options ...record.Option) api.GateView {
	t.Helper()
	subject, err := json.Marshal(api.Message{ID: 3, Unit: prUnit, Kind: "question", Text: text, Recommend: recommend, Options: options})
	if err != nil {
		t.Fatal(err)
	}
	return api.GateView{ID: 3, Task: "t-1", Kind: "question", Name: "question", Status: "open", Text: "The agent asks", Subject: subject,
		Intents: []api.IntentView{{ID: "answer", Label: "Answer", Answer: true}, {ID: "dismiss", Label: "Dismiss"}}}
}

// stepGate is an open review-diff step of task t-1 about its change acme/api#9.
func stepGate() api.GateView {
	return api.GateView{ID: 5, Task: "t-1", Kind: "step", Name: "review-diff", Status: "open", Text: "Review the diff of acme/api#9, 3 files changed",
		Units: []api.PlanUnit{{Label: "acme/api#9", Title: "Batch export", Link: "https://forge.example/acme/api/changes/9"}},
		Intents: []api.IntentView{{ID: "approve", Label: "Looks good", Summary: "Mark the diff reviewed; posts nothing."},
			{ID: "dismiss", Label: "Dismiss", Summary: "Close this step without reviewing; the task stays as it is."}}}
}

// stepCard is what a step gate's card shows: its title, each unit it is about with where it links, and what each
// answer does.
type stepCard struct {
	Title   string
	About   []string
	Links   []string
	Says    []string
	Buttons []string
}

// stepCardJS reads a drawn card c as a stepCard.
const stepCardJS = `(() => {
  const nodes = (n, pred, out = []) => {
    if (n && Array.isArray(n.children)) { if (pred(n)) out.push(n); n.children.forEach((k) => nodes(k, pred, out)); }
    return out;
  };
  const of = (n, k) => nodes(n, (x) => String(x.attrs.class || "").split(" ").includes(k));
  const words = (n) => (n == null || n === false ? "" : typeof n !== "object" ? String(n)
    : typeof n.textContent === "string" ? n.textContent : (n.children || []).map(words).join(""));
  return {
    Title: of(c, "d-sub").map(words).join(""),
    About: of(c, "gate-about").map(words),
    Links: nodes(c, (x) => x.tag === "a").map((a) => a.attrs.href),
    Says: of(c, "gate-says").flatMap((x) => nodes(x, (y) => y.tag === "li").map(words)),
    Buttons: nodes(c, (x) => x.tag === "button").map(words),
  };
})()`

func wantStepCard(t *testing.T, where string, got stepCard) {
	t.Helper()
	if got.Title != "Review the diff of acme/api#9, 3 files changed" {
		t.Errorf("%s: title %q", where, got.Title)
	}
	if len(got.About) != 1 || !strings.Contains(got.About[0], "Batch export") || !strings.Contains(got.About[0], "acme/api#9") {
		t.Errorf("%s: about %q, want the change's title and reference", where, got.About)
	}
	if !slices.Contains(got.Links, "https://forge.example/acme/api/changes/9") {
		t.Errorf("%s: links %q, want the change's link", where, got.Links)
	}
	want := []string{"Looks good: Mark the diff reviewed; posts nothing.", "Dismiss: Close this step without reviewing; the task stays as it is."}
	if !slices.Equal(got.Says, want) {
		t.Errorf("%s: says %q, want %q", where, got.Says, want)
	}
	if len(got.Buttons) != 2 {
		t.Errorf("%s: buttons %q", where, got.Buttons)
	}
}

func TestAStepGatesCardSaysWhatToReviewAndWhatEachAnswerDoes(t *testing.T) {
	var got stepCard
	runGate(t, "const c = card(input.gate);\ndone("+stepCardJS+");", map[string]any{"gate": stepGate()}, &got)
	wantStepCard(t, "detail", got)

	var plain stepCard
	g := questionGate(t, "Which one?", "")
	runGate(t, "const c = card(input.gate);\ndone("+stepCardJS+");", map[string]any{"gate": g}, &plain)
	if len(plain.About) != 0 || len(plain.Says) != 0 {
		t.Errorf("a gate with no units nor summaries: about %q, says %q", plain.About, plain.Says)
	}
}

// gateHarness draws gate cards and draft cards over the cockpit's scripts; card(g) and draft(d) draw one.
const gateHarness = `
const card = (g) => gateCard({ id: "t-1", title: "Retry flaky uploads" }, g);
const draft = (d, e) => draftCard(e || null, d, { scope: "outbox" });
const attr = (n, k) => n.attrs[k];
`

func runGate(t *testing.T, main string, in map[string]any, out any) {
	t.Helper()
	if in["catalog"] == nil {
		in["catalog"] = taskCatalog()
	}
	if in["answers"] == nil {
		in["answers"] = map[string]any{}
	}
	nodeRun(t, cockpitScript(t)+taskPageHarness+gateHarness, nil, "(async () => {\n"+main+"\n})().catch((e) => { console.error(e); process.exit(1); });", in, out)
}

func TestAQuestionGateRendersMarkdownAndOptionCards(t *testing.T) {
	var got struct {
		Labels   []string
		Rec      []string
		Pressed  []string
		Conseq   []string
		Code     []string
		Primary  []string
		Textarea int
	}
	g := questionGate(t, "Use `retry()`", "b", record.Option{ID: "a", Label: "Retry", Consequence: "Runs it again."}, record.Option{ID: "b", Label: "Revert"})
	runGate(t, `
const c = card(input.gate);
const opts = cls(c, "opt");
done({
  Labels: opts.map((o) => cls(o, "lb")[0].textContent),
  Rec: opts.map((o) => cls(o, "rec").map((r) => r.textContent).join("")),
  Pressed: opts.map((o) => attr(o, "aria-pressed")),
  Conseq: opts.map((o) => cls(o, "cs").length + ":" + cls(o, "cs").map((x) => x.textContent).join("|")),
  Code: walk(c, (x) => x.tag === "code").map((x) => x.textContent),
  Primary: cls(c, "primary").map((x) => x.textContent),
  Textarea: walk(c, (x) => x.tag === "textarea").length,
});`, map[string]any{"gate": g}, &got)
	if !slices.Equal(got.Labels, []string{"Revert", "Retry"}) || !slices.Equal(got.Rec, []string{"recommended", ""}) {
		t.Errorf("options %q marked %q, want the recommended Revert first", got.Labels, got.Rec)
	}
	if !slices.Equal(got.Pressed, []string{"true", "false"}) {
		t.Errorf("pressed %q, want the recommended option picked", got.Pressed)
	}
	if !slices.Equal(got.Conseq, []string{"0:", "1:Runs it again."}) {
		t.Errorf("consequences %q, want no line for the option without one", got.Conseq)
	}
	if !slices.Equal(got.Code, []string{"retry()"}) {
		t.Errorf("the message renders code %q, want markdown", got.Code)
	}
	if !slices.Equal(got.Primary, []string{"1 Answer"}) || got.Textarea != 1 {
		t.Errorf("primary %q, %d answer boxes", got.Primary, got.Textarea)
	}
}

// TestAGateRendersAgentProseAsText checks the card-level path of review focus 1: raw HTML stays text, a
// javascript: link is no link, and a 5,000-line message folds behind Read all.
func TestAGateRendersAgentProseAsText(t *testing.T) {
	var lines []string
	for i := range 5000 {
		lines = append(lines, "line "+strings.Repeat("x", i%7))
	}
	text := "<img src=x onerror=alert(1)> <b>hi</b> [go](javascript:alert(1))\n\n" + strings.Join(lines, "\n\n")
	var got struct {
		Tags  []string
		Links []string
		Paras int
		More  []string
	}
	runGate(t, `
const c = card(input.gate);
const md = cls(c, "md")[0];
done({
  Tags: walk(md, (x) => ["img", "b", "script"].includes(x.tag)).map((x) => x.tag),
  Links: walk(md, (x) => x.tag === "a").map((x) => attr(x, "href")),
  Paras: walk(md, (x) => x.tag === "p" && !x.hidden).length,
  More: walk(md, (x) => x.tag === "button").map((x) => x.textContent),
});`, map[string]any{"gate": questionGate(t, text, "")}, &got)
	if len(got.Tags) != 0 || len(got.Links) != 0 {
		t.Errorf("the agent's prose built tags %v and links %v", got.Tags, got.Links)
	}
	if got.Paras > 4 || !slices.Equal(got.More, []string{"Read all"}) {
		t.Errorf("%d paragraphs shown, buttons %q: want the message folded after 4 blocks", got.Paras, got.More)
	}
}

func TestAGateWithoutOptionsShowsOnlyTheAnswerAndIntents(t *testing.T) {
	var got []struct {
		Opts, Pressed, Textarea int
		Primary                 []string
	}
	runGate(t, `
const shape = (g) => {
  const c = card(g);
  return { Opts: cls(c, "opt").length, Pressed: cls(c, "opt").filter((o) => attr(o, "aria-pressed") === "true").length,
    Textarea: walk(c, (x) => x.tag === "textarea").length, Primary: cls(c, "primary").map((x) => x.textContent) };
};
done([shape(input.bare), shape(input.unmarked)]);`, map[string]any{
		"bare":     questionGate(t, "Which one?", ""),
		"unmarked": questionGate(t, "Which one?", "", record.Option{ID: "a", Label: "Retry"}, record.Option{ID: "b", Label: "Revert"}),
	}, &got)
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	if b := got[0]; b.Opts != 0 || b.Textarea != 1 || !slices.Equal(b.Primary, []string{"1 Answer"}) {
		t.Errorf("a gate without options: %+v, want the answer box and the first intent the only primary", b)
	}
	if u := got[1]; u.Opts != 2 || u.Pressed != 0 || !slices.Equal(u.Primary, []string{"1 Answer"}) {
		t.Errorf("options with none recommended: %+v, want none picked", u)
	}
}

func TestPickingAnOptionCardThenAnswerSendsIt(t *testing.T) {
	var got struct {
		Picked, Keyed []any
	}
	g := questionGate(t, "Retry or revert?", "b", record.Option{ID: "a", Label: "Retry"}, record.Option{ID: "b", Label: "Revert"})
	runGate(t, `
let c = card(input.gate);
await focus(c, "gate:3:option:a").on.click();
c = card(input.gate);
focus(c, "gate:3:text").on.input({ target: { value: "typed" } });
await focus(c, "gate:3:answer").on.click();
const picked = posts.splice(0);
c = card(input.gate);
focus(c, "gate:3:text").on.input({ target: { value: "ship it" } });
focus(c, "gate:3:text").on.keydown({ key: "Enter", metaKey: true });
await new Promise((r) => setTimeout(r, 0));
done({ Picked: picked, Keyed: posts });`, map[string]any{"gate": g}, &got)
	want := []any{map[string]any{"path": "/api/gates/3/answer", "body": map[string]any{"source": "cockpit", "args": map[string]any{"answer": "a\ntyped"}}}}
	if mustJS(t, got.Picked) != mustJS(t, want) {
		t.Errorf("picking a then answering posted %v, want %v", got.Picked, want)
	}
	want = []any{map[string]any{"path": "/api/gates/3/answer", "body": map[string]any{"source": "cockpit", "args": map[string]any{"answer": "b\nship it"}}}}
	if mustJS(t, got.Keyed) != mustJS(t, want) {
		t.Errorf("Cmd+Enter in the answer posted %v, want the recommended option and the text %v", got.Keyed, want)
	}
}

// TestAGateShowsItsProfileIntentsAndDigitKeys checks that a gate's card shows its intents in the profile's
// order with the digit that picks each, that the digit resolves the gate the focus is in, and that an answer
// intent with nothing typed or picked says so.
func TestAGateShowsItsProfileIntentsAndDigitKeys(t *testing.T) {
	var got struct {
		Buttons [][]string
		Boxes   []int
		Keyed   []string
		Clicked []any
		Empty   string
	}
	src := strings.Replace(staticJS(t, "keys.js"), `if (typeof document !== "undefined" && typeof state !== "undefined") installKeys(appEnv());`, "", 1)
	raw, _ := json.Marshal(webBindings(t))
	runTaskPage(t, strings.Replace(src, `"use strict";`, "", 1)+`
const box = await page("t-0123456789abcdef");
const gates = cls(box, "d-gate");
const keys = createKeys({
  selected: () => detailState.task,
  questionAt: (el) => gateAt(el, [{ task: detailState.task, gates: detailState.task.gates }]),
  resolveGate: (task, gate, intent) => detailPane.resolve(task, gate, intent),
  verbs: () => [],
});
keys.setBindings(`+string(raw)+`);
const inGate = (g) => ({ tagName: "DIV", closest: (s) => (s === ".d-gate" ? g : null) });
const press = (key, target) => keys.handle({ key, code: "Digit" + key, target, defaultPrevented: false, preventDefault() {} });
await press("2", inGate(gates[0]));
await press("1", inGate(gates[1]));
const empty = notices.slice(-1)[0];
const keyed = posts.splice(0).map((p) => p.path);
focus(box, "gate:9:text").on.input({ target: { value: "Take the timeout case first" } });
await focus(box, "gate:9:answer").on.click();
done({
  Buttons: gates.map((g) => walk(g, (x) => x.tag === "button").map((b) => b.textContent)),
  Boxes: gates.map((g) => walk(g, (x) => x.tag === "textarea").length),
  Keyed: keyed, Clicked: posts, Empty: empty,
});`, pageAnswers(t), &got)
	if want := [][]string{{"1 Run now", "2 Skip"}, {"1 Answer", "2 Dismiss"}}; !slices.EqualFunc(got.Buttons, want, slices.Equal) {
		t.Errorf("gate buttons %q, want %q", got.Buttons, want)
	}
	if !slices.Equal(got.Boxes, []int{0, 1}) {
		t.Errorf("answer boxes %v, want one on the gate whose intent takes an answer", got.Boxes)
	}
	if !slices.Equal(got.Keyed, []string{"/api/gates/7/skip"}) || !strings.HasPrefix(got.Empty, "! Answer") {
		t.Errorf("digits posted %v, then said %q", got.Keyed, got.Empty)
	}
	want := []any{map[string]any{"path": "/api/gates/9/answer", "body": map[string]any{"source": "cockpit", "args": map[string]any{"answer": "Take the timeout case first"}}}}
	if mustJS(t, got.Clicked) != mustJS(t, want) {
		t.Errorf("answering posted %v, want %v", got.Clicked, want)
	}
}

// planGate is the open plan gate 5 of task t-1, which asks to run plan, nil when the plan no longer holds it.
func planGate(plan *api.PlanItem) api.GateView {
	return api.GateView{ID: 5, Task: "t-1", Kind: "plan", Name: "plan", Status: "open", Text: "Run this?", Subject: json.RawMessage(`"fake-close/task/t-1/ab12"`),
		Intents: []api.IntentView{{ID: "run_now", Label: "Run now"}, {ID: "skip", Label: "Skip"}, {ID: "guide", Label: "Guide", Answer: true}}, Plan: plan}
}

func closingPlan(summary string) *api.PlanItem {
	return &api.PlanItem{ID: "fake-close/task/t-1/ab12", Workflow: "fake-close", Summary: summary, Decision: "pending",
		Label: "Retry flaky uploads", Link: "https://chat.example/c1", Steps: []string{"items.resolve"},
		Does: []api.PlanStep{{Use: "items.resolve", Reach: "local"}},
		Why:  api.Why{Trigger: "sweep", Matched: []api.Fact{{Name: "task.category", Value: "active"}, {Name: "task.items_open", Value: "2"}}}}
}

func TestAPlanGateSaysWhatItsPlanDoesOnWhatAndWhy(t *testing.T) {
	type shape struct {
		Title, Summary, On, Link string
		Why                      []string
		Steps                    []string
		Folded                   bool
		Buttons                  []string
	}
	var got []shape
	cat := taskCatalog()
	cat.Facets = []api.FacetSpec{{ID: "category", Label: "Category", Fact: "task.category", Labels: map[string]string{"active": "Active"}}}
	runGate(t, `
const shape = (g) => {
  const c = card(g);
  const on = cls(c, "plan-on")[0];
  const why = cls(c, "plan-why")[0];
  return {
    Title: cls(c, "d-sub")[0].textContent,
    Summary: cls(c, "plan-summary").map((x) => x.textContent).join(""),
    On: on ? on.textContent : "",
    Link: on ? walk(on, (x) => x.tag === "a").map((a) => attr(a, "href")).join("") : "",
    Why: why ? walk(why, (x) => x.tag === "li").map((x) => x.textContent) : [],
    Steps: cls(c, "plan-do").map((x) => attr(cls(x, "plan-do-what")[0], "title")),
    Folded: !!why && why.tag === "details",
    Buttons: walk(c, (x) => x.tag === "button").map((b) => b.textContent),
  };
};
done([shape(input.full), shape(input.bare), shape(input.gone)]);`, map[string]any{
		"catalog": cat,
		"full":    planGate(closingPlan("Close the task once its asker checked the answer; posts nothing")),
		"bare":    planGate(closingPlan("")),
		"gone":    planGate(nil),
	}, &got)
	if len(got) != 3 {
		t.Fatalf("got %+v", got)
	}
	full := got[0]
	if full.Title != "Run this?" || full.Summary != "Close the task once its asker checked the answer; posts nothing" {
		t.Errorf("title %q, summary %q", full.Title, full.Summary)
	}
	if !strings.Contains(full.On, "Retry flaky uploads") || full.Link != "https://chat.example/c1" {
		t.Errorf("on %q linked %q, want the unit's label linked", full.On, full.Link)
	}
	if !full.Folded || !slices.Equal(full.Why, []string{"Category: Active", "task.items_open = 2"}) {
		t.Errorf("why %q folded %v, want the catalog's words for a fact it names, else name = value, folded", full.Why, full.Folded)
	}
	if !slices.Equal(full.Steps, []string{"items.resolve"}) {
		t.Errorf("steps %q, want what applying does beside the summary", full.Steps)
	}
	if bare := got[1]; bare.Summary != "" || !slices.Equal(bare.Steps, []string{"items.resolve"}) {
		t.Errorf("a plan without a summary: %+v, want its steps", bare)
	}
	if gone := got[2]; gone.Title != "Run this?" || gone.Summary != "" || gone.On != "" || len(gone.Why) != 0 ||
		!slices.Equal(gone.Buttons, []string{"1 Run now", "2 Skip", "3 Guide"}) {
		t.Errorf("a gate whose plan left: %+v, want its question and intents", gone)
	}
}

func TestAPlanGateDropsAnUnsafeLinkAndTitlesEachFact(t *testing.T) {
	var got struct {
		Links  []string
		On     string
		Titles []string
	}
	p := closingPlan("Close it")
	p.Link = "javascript:alert(1)"
	runGate(t, `
const c = card(input.gate);
const on = cls(c, "plan-on")[0];
done({ Links: walk(c, (x) => x.tag === "a").map((a) => attr(a, "href")), On: on.textContent,
  Titles: walk(cls(c, "plan-why")[0], (x) => x.tag === "li").map((x) => attr(x, "title")) });`, map[string]any{"gate": planGate(p)}, &got)
	if len(got.Links) != 0 || !strings.Contains(got.On, "Retry flaky uploads") {
		t.Errorf("links %q, target %q: want the label as text and no javascript: link", got.Links, got.On)
	}
	if !slices.Equal(got.Titles, []string{"task.category = active", "task.items_open = 2"}) {
		t.Errorf("fact titles %q, want each fact's name and raw value, which a cut line hides", got.Titles)
	}
}

// pendingDraft is a held nudge on task t-1, from workflow fake-nudge, whose gate 12 approves or discards it.
func pendingDraft() api.Draft {
	return api.Draft{ID: 7, Unit: "github/acme/api#8", Task: "t-1", Kind: "nudge", Purpose: "nudge", Target: "acme/api#8", Where: "acme/api#8",
		Text: "Any news on the retry change?", Why: "No answer since Tuesday", Language: "pt-BR", Effects: []string{"task.nudged"}, State: "held",
		Permalink: "https://example.test/thread", Origin: api.DraftOrigin{Kind: "workflow", Workflow: "fake-nudge"}, Gate: 12,
		Intents: map[string]string{"approved": "approve", "rejected": "discard"}, Rev: 3, Actions: []api.ActionState{}}
}

func TestADraftCardSaysWhyAndWhatSendingDoes(t *testing.T) {
	var got struct {
		Text  string
		Links []string
	}
	cat := taskCatalog()
	cat.Effects = map[string]string{"task.nudged": "The task is marked as nudged."}
	runGate(t, `
const c = draft(input.draft);
done({ Text: c.textContent, Links: walk(c, (x) => x.tag === "a").map((a) => attr(a, "href")) });`, map[string]any{"draft": pendingDraft(), "catalog": cat}, &got)
	for _, s := range []string{"No answer since Tuesday", "The task is marked as nudged.", "Any news on the retry change?"} {
		if !strings.Contains(got.Text, s) {
			t.Errorf("the draft card lacks %q: %s", s, got.Text)
		}
	}
	if len(got.Links) != 2 || !strings.Contains(got.Links[0], "task=t-1") || got.Links[1] != "https://example.test/thread" {
		t.Errorf("links %q, want its task and its unit at the source", got.Links)
	}
}

func TestADraftCardShowsItsLanguage(t *testing.T) {
	var got []string
	d := pendingDraft()
	none := pendingDraft()
	none.Language = ""
	runGate(t, `done([...cls(draft(input.draft), "lang"), ...cls(draft(input.none), "lang")].map((x) => x.textContent));`, map[string]any{"draft": d, "none": none}, &got)
	if !slices.Equal(got, []string{"Brazilian Portuguese"}) {
		t.Errorf("languages %q, want the draft's language by name and none for a draft without one", got)
	}
}

func TestADraftCardOffersOnlyTheActionsServed(t *testing.T) {
	var got struct {
		Without, With []string
		Off           []bool
		Posts         []any
	}
	bare := pendingDraft()
	offered := pendingDraft()
	offered.Actions = []api.ActionState{{ID: "draft.close-silently", Enabled: true}, {ID: "draft.skip-workflow", Enabled: true}, {ID: "draft.edit", Enabled: true}}
	refused := pendingDraft()
	refused.Actions = []api.ActionState{{ID: "draft.close-silently", Reason: "the conversation is open elsewhere"}}
	runGate(t, `
const labels = (c) => walk(c, (x) => x.tag === "button").map((b) => b.textContent);
const c = draft(input.offered);
await focus(c, "outbox:draft.close-silently:draft:7").on.click();
await focus(c, "outbox:draft.skip-workflow:draft:7").on.click();
done({ Without: labels(draft(input.bare)), With: labels(c),
  Off: walk(draft(input.refused), (x) => x.tag === "button" && x.textContent === "Close without a message").map((b) => "disabled" in b.attrs),
  Posts: posts });`, map[string]any{"bare": bare, "offered": offered, "refused": refused}, &got)
	if slices.Contains(got.Without, "Close without a message") || slices.Contains(got.Without, "Don't nudge this conversation") {
		t.Errorf("a draft that serves neither offers %q", got.Without)
	}
	if !slices.Contains(got.With, "Close without a message") || !slices.Contains(got.With, "Don't nudge this conversation") {
		t.Errorf("a draft that serves both offers %q", got.With)
	}
	if !slices.Equal(got.Off, []bool{true}) {
		t.Errorf("a refused offer is disabled %v, want off", got.Off)
	}
	want := []any{
		map[string]any{"path": "/api/drafts/7/close-silently", "body": map[string]any{"rev": 3}},
		map[string]any{"path": "/api/units/github/acme%2Fapi%238/skip/fake-nudge", "body": map[string]any{"source": "cockpit"}},
	}
	if mustJS(t, got.Posts) != mustJS(t, want) {
		t.Errorf("posts %v, want %v", got.Posts, want)
	}
}

func TestADraftCardTogglesAThreadReplysBroadcast(t *testing.T) {
	var got struct {
		On, Plain, Sent []bool
		Boxes           int
		Posts           []any
	}
	on := pendingDraft()
	on.Broadcast = "on"
	plain := pendingDraft()
	sent := pendingDraft()
	sent.Broadcast, sent.State = "off", "sent"
	runGate(t, `
const box = (c) => walk(c, (x) => x.tag === "input" && attr(x, "type") === "checkbox");
const c = draft(input.on);
const checked = (c) => box(c).map((b) => "checked" in b.attrs);
const on = checked(c);
await focus(c, "outbox:broadcast:draft:7").on.change({ target: { checked: false } });
done({ On: on, Plain: checked(draft(input.plain)), Sent: box(draft(input.sent)).map((b) => "disabled" in b.attrs), Boxes: box(c).length, Posts: posts });`,
		map[string]any{"on": on, "plain": plain, "sent": sent}, &got)
	if !slices.Equal(got.On, []bool{true}) || len(got.Plain) != 0 {
		t.Errorf("checkboxes on a broadcast reply %v, on a draft in no thread %v", got.On, got.Plain)
	}
	if !slices.Equal(got.Sent, []bool{true}) {
		t.Errorf("a sent draft's checkbox disabled %v, want off", got.Sent)
	}
	posted := mustJS(t, got.Posts)
	if len(got.Posts) != 1 || !strings.Contains(posted, "/api/tasks/draft:7/edit") || !strings.Contains(posted, `"broadcast":"false"`) || !strings.Contains(posted, `"rev":"3"`) {
		t.Errorf("posts %s, want the draft's edit verb with broadcast false at the rev shown", posted)
	}
}

func TestADraftCardNamesItsMentions(t *testing.T) {
	var got []map[string]string
	d := pendingDraft()
	d.Text = "@{slack:UANA} and @{slack:UBRUNO}, any news? cc @{slack:UCAIO} @{group:channel participants}"
	d.Names = map[string]string{"slack:UANA": "Ana", "slack:UBRUNO": "Bruno", "group:channel participants": "channel participants"}
	runGate(t, `done(cls(draft(input.draft), "mention").map((m) => ({ text: m.textContent, title: attr(m, "title") })));`, map[string]any{"draft": d}, &got)
	want := []map[string]string{{"text": "@Ana", "title": "slack:UANA"}, {"text": "@Bruno", "title": "slack:UBRUNO"}, {"text": "@slack:UCAIO", "title": "slack:UCAIO"},
		{"text": "@channel participants", "title": "group:channel participants"}}
	if mustJS(t, got) != mustJS(t, want) {
		t.Errorf("mentions %v, want %v", got, want)
	}
}

func TestADraftCardNamesItsPurposeAndStateInWords(t *testing.T) {
	var got []string
	failed := pendingDraft()
	failed.Purpose, failed.State = "nudge_once", "failed"
	held := pendingDraft()
	held.Purpose = "close_quietly"
	cat := taskCatalog()
	cat.DraftPurposes = map[string]string{"nudge_once": "Nudge once"}
	cat.DraftStates = map[string]string{"failed": "failed to send", "held": "waiting for you"}
	runGate(t, `done([input.failed, input.held].map((d) => cls(draft(d), "d-sub")[0].textContent));`,
		map[string]any{"failed": failed, "held": held, "catalog": cat}, &got)
	if want := []string{"Draft Nudge once (failed to send)", "Draft close_quietly"}; !slices.Equal(got, want) {
		t.Errorf("draft heads %q, want %q: the profile's words, the purpose as named when it has none, and no state while held", got, want)
	}
}
