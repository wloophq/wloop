package web

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/card"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/unit"
)

var prUnit = unit.Unit{Provider: "github", Key: "acme/api#8"}

func questionEntry() api.EntryView {
	return entry("e-1", "inbox", func(e *api.EntryView) {
		e.Title, e.Contexts, e.Changed = "Pick a fix", []string{"@decide"}, true
		e.Units = []unit.Unit{prUnit}
		e.Items = []api.ItemView{{Unit: prUnit, ID: "gh:review", Summary: "Review the retry change", State: "open", Kind: "action"}}
		e.Message = &api.Message{ID: 3, EntryID: "e-1", Unit: prUnit, Kind: "question", Text: "Retry or revert?", State: "open", Recommend: "b",
			Options: []record.Option{{ID: "a", Label: "Retry"}, {ID: "b", Label: "Revert"}}}
		e.Cards = []api.CardView{{Card: card.Card{Unit: prUnit, Title: "Retry flaky uploads", State: "open", Why: "your review is requested",
			Link: "https://github.com/acme/api/pull/8", Signals: []card.Signal{{Key: "checks", Value: "failing", Tone: "bad"}},
			WaitsOn: []items.Person{{Key: "github:me", Name: "me"}}}}}
		e.Verbs = []api.VerbState{{Verb: "next", Enabled: true}, {Verb: "done", Enabled: true}, {Verb: "answer", Enabled: true}, {Verb: "seen", Enabled: true}, {Verb: "dismiss", Enabled: true}}
	})
}

// detailHarness records the pane's requests: getJSON answers from
// input.answers by URL, posts land in posts.
const detailHarness = `
const posts = [], asked = [];
detailEnv.getJSON = async (u) => { asked.push(u); if (!(u in input.answers)) throw new Error("404 " + u); return input.answers[u]; };
detailEnv.post = async (path, body) => { posts.push({ path, body }); return { entry: {} }; };
detailEnv.notice = () => {}; detailEnv.changed = () => {}; detailEnv.render = () => {};
`

func TestEntryDetailSections(t *testing.T) {
	e := questionEntry()
	var got struct {
		Before, After string
		Questions     int
		Cards         int
		LogBefore     bool
		LogAfter      bool
		Asked         []string
	}
	nodeRun(t, gtdPrelude(t)+detailHarness, nil, `
(async () => {
  await loadDetailPane({ entry: "e-1" });
  const before = entryPane(detailState.entry);
  await byFocus(before, "detail:agent-log").attrs.onclick();
  const after = entryPane(detailState.entry);
  process.stdout.write(JSON.stringify({
    Before: text(before), After: text(after), Questions: byClass(before, "d-question").length, Cards: byClass(before, "d-card").length,
    LogBefore: byClass(before, "timeline").length > 0, LogAfter: byClass(after, "timeline").length > 0, Asked: asked,
  }));
})();`, map[string]any{"answers": map[string]any{
		"/api/entries/e-1":                 e,
		"/api/units/github/acme%2Fapi%238": map[string]any{"events": []map[string]any{{"kind": "turn_started", "at": "2026-10-20T10:00:00Z"}}},
	}}, &got)
	for _, s := range []string{"Pick a fix", "inbox", "@decide", "Review the retry change", "Retry or revert?", "Revert (recommended)", "Retry flaky uploads", "your review is requested", "checks failing", "Agent log"} {
		if !strings.Contains(got.Before, s) {
			t.Errorf("the detail lacks %q: %s", s, got.Before)
		}
	}
	if got.Questions != 1 || got.Cards != 1 {
		t.Errorf("%d question cards and %d unit cards, want 1 and 1", got.Questions, got.Cards)
	}
	if got.LogBefore || strings.Contains(got.Before, "turn started") {
		t.Error("the turn log shows before Agent log is clicked")
	}
	if !got.LogAfter || !strings.Contains(got.After, "turn started") {
		t.Errorf("Agent log did not open the turn log: %s", got.After)
	}
	if want := []string{"/api/entries/e-1", "/api/drafts?entry=e-1&state=proposed%2Capproved%2Csending%2Csent%2Cfailed", "/api/units/github/acme%2Fapi%238"}; !slices.Equal(got.Asked, want) {
		t.Errorf("asked %v, want %v", got.Asked, want)
	}
}

func TestUnitDetailRoute(t *testing.T) {
	mine := entry("e-1", "next", func(e *api.EntryView) { e.Units = []unit.Unit{prUnit} })
	other := entry("e-2", "next", func(e *api.EntryView) { e.Units = []unit.Unit{{Provider: "github", Key: "acme/api#80"}} })
	waiting := entry("e-3", "waiting", func(e *api.EntryView) { e.Units = []unit.Unit{prUnit} })
	page := func(es ...api.EntryView) api.EntriesPage {
		return api.EntriesPage{Full: true, Entries: es, Removed: []string{}}
	}
	detail := map[string]any{"row": map[string]any{"unit": prUnit}, "events": []any{}, "items": []any{},
		"card": card.Snapshot{Card: card.Card{Unit: prUnit, Title: "Retry flaky uploads", State: "open"}}}
	var got struct {
		Text    string
		Entries []string
		Route   map[string]any
	}
	nodeRun(t, gtdPrelude(t)+detailHarness, nil, `
function hashString(x) { return routeHash(x.view, x.pr, new URLSearchParams(x.entry ? { entry: x.entry } : {})); }
function readHash() { return { view: "next" }; }
(async () => {
  const route = readRoute("#unit=github/acme%2Fapi%238");
  await loadDetailPane(route);
  const box = unitExtras(input.detail);
  process.stdout.write(JSON.stringify({ Text: text(box), Entries: detailState.unitEntries.map((e) => e.id), Route: route }));
})();`, map[string]any{"detail": detail, "answers": map[string]any{
		"/api/entries?view=inbox":   page(),
		"/api/entries?view=next":    page(mine, other),
		"/api/entries?view=waiting": page(waiting),
		"/api/entries?view=someday": page(),
	}}, &got)
	if got.Route["unit"] != "github:acme/api#8" {
		t.Fatalf("route %v", got.Route)
	}
	if !slices.Equal(got.Entries, []string{"e-1", "e-3"}) {
		t.Errorf("the unit's entries %v, want e-1 and e-3", got.Entries)
	}
	for _, s := range []string{"Retry flaky uploads", "Entries", "Entry e-1", "Entry e-3"} {
		if !strings.Contains(got.Text, s) {
			t.Errorf("the unit detail lacks %q: %s", s, got.Text)
		}
	}
	if strings.Contains(got.Text, "Entry e-2") {
		t.Errorf("another unit's entry shows: %s", got.Text)
	}
}

func TestOpeningClearsChanged(t *testing.T) {
	next := entry("e-2", "next", func(e *api.EntryView) { e.Changed = true })
	var got struct{ Paths []string }
	nodeRun(t, gtdPrelude(t)+detailHarness, nil, `
(async () => {
  await loadDetailPane({ entry: "e-1" });
  await loadDetailPane({ entry: "e-1" });
  await loadDetailPane({ entry: "e-2" });
  await loadDetailPane({ entry: "" });
  await loadDetailPane({ entry: "e-1" });
  process.stdout.write(JSON.stringify({ Paths: posts.map((p) => p.path + " " + JSON.stringify(p.body)) }));
})();`, map[string]any{"answers": map[string]any{"/api/entries/e-1": questionEntry(), "/api/entries/e-2": next}}, &got)
	want := []string{`/api/entries/e-1/seen {"source":"cockpit"}`, `/api/entries/e-1/seen {"source":"cockpit"}`}
	if !slices.Equal(got.Paths, want) {
		t.Errorf("posts %v, want seen once per opening of the Inbox entry: %v", got.Paths, want)
	}
}

func TestQuestionCardAnswers(t *testing.T) {
	confirm := questionEntry()
	confirm.ID = "e-9"
	confirm.Message = &api.Message{ID: 9, EntryID: "e-9", Kind: "question", Text: "Merge it?", Confirm: true, State: "open"}
	var got struct {
		Bodies  []string
		Classes []bool
		Digit   []string
	}
	nodeRun(t, gtdPrelude(t)+detailHarness, nil, `
(async () => {
  const card = questionCard(input.q);
  await byFocus(card, "question:a").attrs.onclick();
  await byFocus(card, "question:b").attrs.onclick();
  const yes = questionCard(input.c);
  await byFocus(yes, "question:yes").attrs.onclick();
  detailState.answer = "only the uploads";
  await byFocus(card, "question:a").attrs.onclick();
  detailState.entry = input.q;
  const el = { closest: (s) => (s === ".d-question" ? {} : null) };
  const q = detailPane.questionAt(el);
  process.stdout.write(JSON.stringify({
    Bodies: posts.map((p) => p.path + " " + JSON.stringify(p.body)),
    Classes: [byClass(card, "d-question").length === 1, card.attrs.class === "d-question"],
    Digit: [q.entry.id, q.question.recommend, ...q.question.options.map((o) => o.id)],
  }));
})();`, map[string]any{"q": questionEntry(), "c": confirm, "answers": map[string]any{}}, &got)
	want := []string{
		`/api/entries/e-1/answer {"source":"cockpit","args":{"option_id":"a"}}`,
		`/api/entries/e-1/answer {"source":"cockpit","args":{"option_id":"b"}}`,
		`/api/entries/e-9/answer {"source":"cockpit","args":{"yes":"true"}}`,
		`/api/entries/e-1/answer {"source":"cockpit","args":{"option_id":"a","guidance":"only the uploads"}}`,
	}
	if !slices.Equal(got.Bodies, want) {
		t.Errorf("posts\n%v\nwant\n%v", got.Bodies, want)
	}
	if !got.Classes[1] {
		t.Error("the question card lacks .d-question, so digit keys cannot answer it")
	}
	if !slices.Equal(got.Digit, []string{"e-1", "b", "a", "b"}) {
		t.Errorf("questionAt %v", got.Digit)
	}
}

func TestProposalShowsItsDraft(t *testing.T) {
	e := entry("e-5", "inbox", func(e *api.EntryView) {
		e.Kind, e.Title = "message", "Nudge the reviewer?"
		e.Units = []unit.Unit{prUnit}
		e.Message = &api.Message{ID: 5, EntryID: "e-5", Kind: "proposal", Text: "The review waits for 3 days; nudge?", State: "open",
			Options: []record.Option{{ID: "accept", Label: "Accept"}, {ID: "reject", Label: "Reject"}}}
		e.Draft = &api.Draft{ID: 11, Unit: "github/acme/api#8", Kind: "nudge", Target: "acme/api#8", Text: "Could you take a look?", State: "proposed", Rev: 2}
		e.Verbs = []api.VerbState{{Verb: "approve", Enabled: true}, {Verb: "dismiss", Enabled: true}, {Verb: "answer", Enabled: true}}
	})
	var got struct {
		Text   string
		Kinds  []int
		Posted []string
		Edited []string
		Keyed  []string
	}
	nodeRun(t, gtdPrelude(t)+detailHarness, nil, `
(async () => {
  await loadDetailPane({ entry: "e-5" });
  const pane = entryPane(detailState.entry);
  posts.length = 0;
  await byFocus(pane, "detail:approve:e-5").attrs.onclick();
  const posted = posts.map((p) => p.path);
  const show = (p) => p.path + " " + JSON.stringify(p.body);
  posts.length = 0;
  byFocus(pane, "detail:open:approve:e-5").attrs.onclick();
  const editing = entryPane(detailState.entry);
  byFocus(editing, "detail:draft-text:e-5").attrs.oninput({ target: { value: "Could you review the retry change today?" } });
  await byFocus(entryPane(detailState.entry), "detail:approve:e-5").attrs.onclick();
  const edited = posts.map(show);
  posts.length = 0;
  byFocus(entryPane(detailState.entry), "detail:open:approve:e-5").attrs.onclick();
  byFocus(entryPane(detailState.entry), "detail:draft-text:e-5").attrs.oninput({ target: { value: "Ping on the retry change" } });
  await detailPane.act("approve", {});
  const keyed = posts.map(show);
  process.stdout.write(JSON.stringify({ Text: text(pane), Kinds: [byClass(pane, "d-question").length, byClass(pane, "d-draft").length], Posted: posted, Edited: edited, Keyed: keyed }));
})();`, map[string]any{"answers": map[string]any{"/api/entries/e-5": e}}, &got)
	if !slices.Equal(got.Kinds, []int{1, 1}) {
		t.Errorf("question and draft cards %v, want one of each", got.Kinds)
	}
	for _, s := range []string{"The review waits for 3 days; nudge?", "Could you take a look?", "acme/api#8"} {
		if !strings.Contains(got.Text, s) {
			t.Errorf("the detail lacks %q", s)
		}
	}
	if !slices.Equal(got.Posted, []string{"/api/entries/e-5/approve"}) {
		t.Errorf("approving the draft posted %v, want the entry's approve", got.Posted)
	}
	if want := []string{`/api/entries/draft:11/approve {"source":"cockpit","args":{"text":"Could you review the retry change today?","rev":"2"}}`}; !slices.Equal(got.Edited, want) {
		t.Errorf("approving the edited draft posted %v, want %v", got.Edited, want)
	}
	if want := []string{`/api/entries/draft:11/approve {"source":"cockpit","args":{"text":"Ping on the retry change","rev":"2"}}`}; !slices.Equal(got.Keyed, want) {
		t.Errorf("the approve key during an edit posted %v, want %v", got.Keyed, want)
	}
}

func TestCardRefOpensTheUnitInItsPluginView(t *testing.T) {
	type link struct{ Href, Text, Target string }
	var got map[string]struct{ Ref, Ext []link }
	nodeRun(t, gtdPrelude(t), nil, `
const catalog = { views: [
  { id: "needs-me", provider: "github", rows: true }, { id: "dash", provider: "github", rows: false },
  { id: "all", provider: "github", rows: true }, { id: "slack", provider: "slack", rows: true },
] };
applyPlugins([{ id: "github", name: "GitHub" }, { id: "slack", name: "Slack" }]);
const links = (n, c) => byClass(n, c).map((a) => ({ Href: a.attrs.href, Text: text(a), Target: a.attrs.target || "" }));
const out = {};
for (const c of input) {
  const card = unitCard({ card: c });
  out[c.unit.provider] = { Ref: links(card, "ref"), Ext: links(card, "ext") };
}
process.stdout.write(JSON.stringify(out));`, []map[string]any{
		{"unit": map[string]string{"provider": "github", "key": "acme/api#8"}, "label": "acme/api#8", "link": "https://github.com/acme/api/pull/8"},
		{"unit": map[string]string{"provider": "slack", "key": "T1/C1/1700.1"}, "label": "#eng thread", "link": "slack://channel?id=C1"},
	}, &got)
	want := map[string]struct{ Ref, Ext []link }{
		"github": {Ref: []link{{Href: "#view=all&unit=github/acme%2Fapi%238", Text: "acme/api#8"}}, Ext: []link{{Href: "https://github.com/acme/api/pull/8", Text: "GitHub", Target: "_blank"}}},
		"slack":  {Ref: []link{{Href: "#view=slack&unit=slack/T1%2FC1%2F1700.1", Text: "#eng thread"}}, Ext: []link{{Href: "slack://channel?id=C1", Text: "Slack", Target: "_blank"}}},
	}
	for p, w := range want {
		g := got[p]
		if !slices.Equal(g.Ref, w.Ref) || !slices.Equal(g.Ext, w.Ext) {
			t.Errorf("%s card: ref %+v ext %+v, want ref %+v ext %+v", p, g.Ref, g.Ext, w.Ref, w.Ext)
		}
	}
}

func TestAVerbButtonSaysWhatItDoes(t *testing.T) {
	var got struct {
		On, Off string
	}
	e := entry("e-2", "next", func(e *api.EntryView) {
		e.Verbs = []api.VerbState{{Verb: "done", Enabled: true}, {Verb: "someday", Reason: "not on a someday entry"}}
	})
	cat := api.GTDCatalog{Verbs: []api.GTDVerb{{Verb: "done", Label: "Finish", Summary: "Close it"}, {Verb: "someday", Label: "Someday", Summary: "Not now"}}}
	nodeRun(t, gtdPrelude(t), nil, `
gtdState.catalog = input.cat;
const pane = h("div", {}, ...verbButtons(input.entry));
process.stdout.write(JSON.stringify({
  On: byFocus(pane, "detail:done:e-2").attrs.title,
  Off: byFocus(pane, "detail:someday:e-2").attrs.title,
}));`, map[string]any{"cat": cat, "entry": e}, &got)
	if got.On != "Close it" || got.Off != "Not now\nnot on a someday entry" {
		t.Errorf("titles %q and %q", got.On, got.Off)
	}
}

func TestAVerbAsksTheProfilesQuestion(t *testing.T) {
	var got struct{ Asked []string }
	e := entry("e-3", "next", func(e *api.EntryView) { e.Verbs = []api.VerbState{{Verb: "waiting", Enabled: true}} })
	cat := api.GTDCatalog{Verbs: []api.GTDVerb{{Verb: "waiting", Label: "Waiting", Summary: "Wait", Input: "On whom?"}}}
	nodeRun(t, gtdPrelude(t)+strings.Replace(staticJS(t, "keys.js"), `"use strict";`, "", 1)+detailHarness, nil, `
gtdState.catalog = input.cat;
detailEnv.ask = (label) => { asked.push(label); return "ana"; };
const pane = h("div", {}, ...verbButtons(input.entry));
(async () => {
  await byFocus(pane, "detail:waiting:e-3").attrs.onclick();
  process.stdout.write(JSON.stringify({ Asked: asked }));
})();`, map[string]any{"cat": cat, "entry": e}, &got)
	if len(got.Asked) != 1 || got.Asked[0] != "On whom?" {
		t.Errorf("asked %v", got.Asked)
	}
}

func TestAQuestionCardShowsItsAnswersAndWhetherTheAgentReadThem(t *testing.T) {
	q := questionEntry()
	read := time.Date(2026, 10, 22, 12, 5, 0, 0, time.UTC)
	q.Message.State = "answered"
	q.Message.Answers = []api.TurnAnswer{
		{ID: 1, OptionID: "b", Text: "and say why", Message: 3, ReadAt: &read},
		{ID: 2, Text: "one more thing", Message: 3},
		{ID: 3, Yes: true, Message: 3},
	}
	var got []string
	nodeRun(t, gtdPrelude(t)+detailHarness, nil, `
const card = questionCard(input.q);
process.stdout.write(JSON.stringify(byClass(card, "d-answer").map(text)));`, map[string]any{"q": q, "answers": map[string]any{}}, &got)
	want := []string{
		"Revert, and say why: read by the agent",
		"one more thing: not read by the agent yet",
		"yes: not read by the agent yet",
	}
	if !slices.Equal(got, want) {
		t.Errorf("answers\n%q\nwant\n%q", got, want)
	}
}

func TestAVerbTheEntryDoesNotOfferHasNoButton(t *testing.T) {
	var got struct{ Approve, Dismiss, Done int }
	e := entry("e-4", "inbox", func(e *api.EntryView) { e.Verbs = []api.VerbState{{Verb: "done", Enabled: true}} })
	nodeRun(t, gtdPrelude(t)+detailHarness, nil, `
const pane = h("div", {}, ...entryPane(input.entry));
const n = (verb) => byFocus(pane, "detail:" + verb + ":e-4") ? 1 : 0;
process.stdout.write(JSON.stringify({ Approve: n("approve"), Dismiss: n("dismiss"), Done: n("done") }));`, map[string]any{"entry": e, "answers": map[string]any{}}, &got)
	if got.Approve != 0 || got.Dismiss != 0 || got.Done != 1 {
		t.Errorf("buttons %+v", got)
	}
}

func TestAnEntryOffersTheIntentsThatServeIt(t *testing.T) {
	e := entry("e-3f9a", "inbox", func(e *api.EntryView) {
		e.Title, e.Kind = "Add retries to the webhook sender", "capture"
		e.Actions = []api.ActionState{{ID: "start", Label: "Begin work", Enabled: true, Note: "Starts an agent session on the entry's brief"}}
	})
	var got struct {
		Text  string
		Posts []struct {
			Path string
			Body map[string]any
		}
	}
	nodeRun(t, gtdPrelude(t)+detailHarness, nil, `
(async () => {
  await loadDetailPane({ entry: "e-3f9a" });
  const pane = entryPane(detailState.entry);
  await byFocus(pane, "detail:start:e-3f9a").attrs.onclick();
  process.stdout.write(JSON.stringify({ Text: text(pane), Posts: posts }));
})();`, map[string]any{"answers": map[string]any{"/api/entries/e-3f9a": e}}, &got)
	if !strings.Contains(got.Text, "Begin work") {
		t.Errorf("the detail lacks the action's label: %s", got.Text)
	}
	last := len(got.Posts) - 1
	if last < 0 || got.Posts[last].Path != "/api/units/entry/e-3f9a/start" || got.Posts[last].Body["source"] != "cockpit" {
		t.Fatalf("posts %+v", got.Posts)
	}
}
