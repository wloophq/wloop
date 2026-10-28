package web

import (
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/unit"
)

var slackThread = unit.Unit{Provider: "slack", Key: "T1/C1/1700000000.000100"}

func threadTask(verbs ...api.VerbState) api.TaskView {
	return task("e-s", "inbox", func(e *api.TaskView) {
		e.Title, e.Units = "Ana asks for the rollout date", []unit.Unit{slackThread}
		e.UnitLabels = []string{"#ops"}
		e.Verbs = verbs
	})
}

const composeHarness = `
detailEnv.postJSON = async (path, body) => { posts.push({ path, body }); return { id: 9, state: body.send ? "approved" : "proposed" }; };
const typeInto = (pane, focus, value) => byFocus(pane, focus).attrs.oninput({ target: { value } });
const click = async (pane, focus) => { await byFocus(pane, focus).attrs.onclick(); };
applyPlugins([{ id: "slack", name: "Slack", enabled: true, reacts: true }, { id: "github", name: "GitHub", enabled: true }]);
`

func TestDelegateBoxSendsGuidanceAndMode(t *testing.T) {
	e := threadTask(api.VerbState{Verb: "delegate", Enabled: true}, api.VerbState{Verb: "reply", Enabled: true})
	var got struct {
		Text    string
		Buttons []string
		Posts   []map[string]any
	}
	nodeRun(t, viewsPrelude(t)+detailHarness+composeHarness, nil, `
(async () => {
  await loadDetailPane({ entry: "e-s" });
  let pane = entryPane(detailState.entry);
  typeInto(pane, "detail:guidance", "say we ship Friday");
  await click(pane, "detail:headless:delegate:e-s");
  pane = entryPane(detailState.entry);
  await click(pane, "detail:session:delegate:e-s");
  process.stdout.write(JSON.stringify({ Text: text(pane), Buttons: buttonLabels(pane), Posts: posts.filter((p) => !p.path.endsWith("/seen")) }));
})();`, map[string]any{"answers": map[string]any{"/api/entries/e-s": e, "/api/drafts?entry=e-s&state=proposed%2Capproved%2Csending%2Csent%2Cfailed": api.DraftsResponse{Drafts: []api.Draft{}}}}, &got)
	for _, b := range []string{"Draft for me", "Hand to an agent", "Save to Outbox", "Send now"} {
		if !slices.Contains(got.Buttons, b) {
			t.Errorf("no %q button: %v", b, got.Buttons)
		}
	}
	if slices.Contains(got.Buttons, "Delegate") || slices.Contains(got.Buttons, "Reply") {
		t.Errorf("a plain verb button stands beside the boxes: %v", got.Buttons)
	}
	if len(got.Posts) != 2 {
		t.Fatalf("posts %+v", got.Posts)
	}
	first, second := got.Posts[0], got.Posts[1]
	args := func(p map[string]any) map[string]any {
		a, _ := p["body"].(map[string]any)["args"].(map[string]any)
		return a
	}
	if first["path"] != "/api/entries/e-s/delegate" || args(first)["mode"] != "headless" || args(first)["guidance"] != "say we ship Friday" {
		t.Errorf("Draft for me posted %+v", first)
	}
	if args(second)["mode"] != "session" || args(second)["guidance"] != nil {
		t.Errorf("Hand to an agent posted %+v, want session with the guidance cleared", second)
	}
}

func TestDelegateBoxShowsWhyItIsOff(t *testing.T) {
	e := threadTask(api.VerbState{Verb: "delegate", Reason: "slack:T1 offers no action to delegate"})
	var got struct {
		Text     string
		Disabled bool
	}
	nodeRun(t, viewsPrelude(t)+detailHarness+composeHarness, nil, `
(async () => {
  await loadDetailPane({ entry: "e-s" });
  const pane = entryPane(detailState.entry);
  process.stdout.write(JSON.stringify({ Text: text(pane), Disabled: !!byFocus(pane, "detail:headless:delegate:e-s").attrs.disabled }));
})();`, map[string]any{"answers": map[string]any{"/api/entries/e-s": e}}, &got)
	if !got.Disabled || !strings.Contains(got.Text, "offers no action to delegate") || !strings.Contains(got.Text, "Could not load the drafts") {
		t.Errorf("%+v", got)
	}
}

func TestHandToAnAgentIsOffWithThePlacementReason(t *testing.T) {
	e := threadTask(api.VerbState{Verb: "delegate", Enabled: true, Runners: []api.RunnerState{
		{Runner: "headless", Enabled: true}, {Runner: "session", Reason: "no workspace manager is enabled"}}})
	var got struct {
		Text              string
		Headless, Session bool
		Title             string
	}
	nodeRun(t, viewsPrelude(t)+detailHarness+composeHarness, nil, `
(async () => {
  await loadDetailPane({ entry: "e-s" });
  const pane = entryPane(detailState.entry);
  const s = byFocus(pane, "detail:session:delegate:e-s").attrs;
  process.stdout.write(JSON.stringify({ Text: text(pane), Headless: !!byFocus(pane, "detail:headless:delegate:e-s").attrs.disabled,
    Session: !!s.disabled, Title: s.title || "" }));
})();`, map[string]any{"answers": map[string]any{"/api/entries/e-s": e, "/api/drafts?entry=e-s&state=proposed%2Capproved%2Csending%2Csent%2Cfailed": api.DraftsResponse{Drafts: []api.Draft{}}}}, &got)
	if got.Headless || !got.Session || got.Title != "no workspace manager is enabled" || !strings.Contains(got.Text, "Hand to an agent: no workspace manager is enabled") {
		t.Errorf("%+v", got)
	}
}

func TestTaskComposerSavesSendsAndReacts(t *testing.T) {
	e := threadTask(api.VerbState{Verb: "reply", Enabled: true})
	var got struct {
		Args  []map[string]any
		Empty string
	}
	nodeRun(t, viewsPrelude(t)+detailHarness+composeHarness, nil, `
(async () => {
  let notices = [];
  detailEnv.notice = (t, err) => notices.push((err ? "! " : "") + t);
  await loadDetailPane({ entry: "e-s" });
  let pane = entryPane(detailState.entry);
  await click(pane, "detail:reply:save");
  typeInto(pane, "detail:reply:text", "Friday, after the freeze");
  await click(pane, "detail:reply:save");
  pane = entryPane(detailState.entry);
  typeInto(pane, "detail:reply:text", "Shipped");
  await click(pane, "detail:reply:send");
  pane = entryPane(detailState.entry);
  typeInto(pane, "detail:reply:reaction", ":eyes:");
  await click(pane, "detail:reply:save");
  const replies = posts.filter((p) => p.path.endsWith("/reply")).map((p) => p.body.args);
  process.stdout.write(JSON.stringify({ Args: replies, Empty: notices[0] }));
})();`, map[string]any{"answers": map[string]any{"/api/entries/e-s": e, "/api/drafts?entry=e-s&state=proposed%2Capproved%2Csending%2Csent%2Cfailed": api.DraftsResponse{Drafts: []api.Draft{}}}}, &got)
	want := []map[string]any{
		{"kind": "reply", "text": "Friday, after the freeze"},
		{"kind": "reply", "text": "Shipped", "send": "now"},
		{"kind": "react", "reaction": "eyes"},
	}
	if len(got.Args) != len(want) {
		t.Fatalf("replies %+v", got.Args)
	}
	for i := range want {
		for k, v := range want[i] {
			if got.Args[i][k] != v {
				t.Errorf("reply %d: %v, want %v", i, got.Args[i], want[i])
			}
		}
		if len(got.Args[i]) != len(want[i]) {
			t.Errorf("reply %d: %v, want %v", i, got.Args[i], want[i])
		}
	}
	if !strings.HasPrefix(got.Empty, "! Write a reply") {
		t.Errorf("an empty composer said %q", got.Empty)
	}
}

func TestTaskDetailListsItsDrafts(t *testing.T) {
	e := threadTask(api.VerbState{Verb: "reply", Enabled: true})
	drafts := api.DraftsResponse{Drafts: []api.Draft{
		{ID: 4, Unit: "slack/" + slackThread.Key, Task: "e-s", Kind: "reply", Text: "Friday works", State: "proposed", Rev: 2, Where: "#ops in the thread",
			Actions: []api.ActionState{{ID: "draft.approve", Enabled: true}, {ID: "draft.edit", Enabled: true}, {ID: "draft.discard", Enabled: true}, {ID: "draft.retry", Reason: "the draft is proposed"}}},
		{ID: 3, Unit: "slack/" + slackThread.Key, Task: "e-s", Kind: "react", Reaction: "eyes", State: "sent",
			Actions: []api.ActionState{{ID: "draft.approve", Reason: "the draft is sent"}, {ID: "draft.edit", Reason: "the draft is sent"}, {ID: "draft.discard", Reason: "the draft is sent"}}},
	}}
	var got struct {
		Text   string
		Drafts int
		Post   map[string]any
	}
	nodeRun(t, viewsPrelude(t)+detailHarness+composeHarness, nil, `
(async () => {
  await loadDetailPane({ entry: "e-s" });
  const pane = entryPane(detailState.entry);
  await click(pane, "drafts:approve:draft:4");
  process.stdout.write(JSON.stringify({ Text: text(pane), Drafts: byClass(pane, "d-draft").length, Post: posts.find((p) => p.path.includes("draft")) }));
})();`, map[string]any{"answers": map[string]any{"/api/entries/e-s": e, "/api/drafts?entry=e-s&state=proposed%2Capproved%2Csending%2Csent%2Cfailed": drafts}}, &got)
	if got.Drafts != 2 || !strings.Contains(got.Text, "Friday works") || !strings.Contains(got.Text, ":eyes:") || !strings.Contains(got.Text, "#ops in the thread") {
		t.Fatalf("%+v", got)
	}
	body, _ := got.Post["body"].(map[string]any)
	if got.Post["path"] != "/api/entries/draft:4/approve" || body["args"].(map[string]any)["rev"] != "2" {
		t.Fatalf("approve posted %+v", got.Post)
	}
}

func TestUnitComposerPostsADraft(t *testing.T) {
	var got struct {
		Posts  []map[string]any
		Text   string
		Reacts []bool
	}
	nodeRun(t, viewsPrelude(t)+detailHarness+composeHarness, nil, `
(async () => {
  const d = { row: { unit: input.unit }, items: [] };
  let box = unitExtras(d);
  typeInto(box, "unit:reply:text", "Thanks, merged");
  await click(box, "unit:reply:send");
  const reacts = (u) => !!byFocus(unitExtras({ row: { unit: u }, items: [] }), "unit:reply:reaction");
  box = unitExtras({ row: { unit: { provider: "github", key: "acme/api#8" } }, items: [] });
  applyPlugins([{ id: "slack", name: "Slack", enabled: true, reacts: true }, { id: "github", name: "GitHub", enabled: true }, { id: "linear", name: "Linear", enabled: true, reacts: true }]);
  process.stdout.write(JSON.stringify({ Posts: posts, Text: text(box) + (byFocus(box, "unit:reply:reaction") ? " reacts" : ""),
    Reacts: [reacts(input.unit), reacts({ provider: "linear", key: "PAY-88" }), reacts({ provider: "wiki", key: "Home" })] }));
})();`, map[string]any{"unit": slackThread}, &got)
	if len(got.Posts) != 1 || got.Posts[0]["path"] != "/api/drafts" {
		t.Fatalf("posts %+v", got.Posts)
	}
	b := got.Posts[0]["body"].(map[string]any)
	if b["text"] != "Thanks, merged" || b["send"] != true || b["kind"] != "reply" || b["source"] != "cockpit" || b["unit"].(map[string]any)["provider"] != "slack" {
		t.Fatalf("body %+v", b)
	}
	if strings.Contains(got.Text, "reacts") {
		t.Errorf("a pull request's composer offers a reaction: %s", got.Text)
	}
	if !slices.Equal(got.Reacts, []bool{true, true, false}) {
		t.Errorf("reactions offered for slack, a reacting plugin, a plugin that does not react: %v", got.Reacts)
	}
}

func TestTheViewFilterMatchesTasksByTheirWords(t *testing.T) {
	es := []api.TaskView{
		task("e-1", "next", func(e *api.TaskView) {
			e.Title, e.UnitLabels, e.Units = "Rollout date", []string{"#ops"}, []unit.Unit{slackThread}
		}),
		task("e-2", "next", func(e *api.TaskView) {
			e.Title, e.Waiting = "Review the retry", &api.WaitingView{On: []api.WaitingPerson{{Name: "Dana Souza"}}}
		}),
		task("e-3", "next", func(e *api.TaskView) {
			e.Title, e.UnitLabels, e.Units = "Fix flaky uploads", []string{"acme/api#8"}, []unit.Unit{prUnit}
		}),
	}
	var got map[string][]string
	nodeRun(t, viewsPrelude(t), nil, `
const out = {};
for (const q of ["", "#ops", "dana", "souza review", "ACME/API", "\"retry\"", "nothing"]) {
  out[q] = input.entries.filter((e) => entryMatches(e, gtdTerms(q))).map((e) => e.id);
}
process.stdout.write(JSON.stringify(out));`, map[string]any{"entries": es}, &got)
	want := map[string][]string{"": {"e-1", "e-2", "e-3"}, "#ops": {"e-1"}, "dana": {"e-2"}, "souza review": {"e-2"}, "ACME/API": {"e-3"}, `"retry"`: {"e-2"}, "nothing": {}}
	for q, ids := range want {
		if !slices.Equal(got[q], ids) && !(len(ids) == 0 && len(got[q]) == 0) {
			t.Errorf("%q matched %v, want %v", q, got[q], ids)
		}
	}
}

func TestAViewListShowsOnlyMatchingTasks(t *testing.T) {
	es := []api.TaskView{
		task("e-1", "inbox", func(e *api.TaskView) { e.Title = "Rollout date" }),
		task("e-2", "inbox", func(e *api.TaskView) { e.Title = "Review the retry" }),
	}
	var got struct{ Text []string }
	nodeRun(t, viewsPrelude(t), nil, `
function writeHash() {}
gtdState.catalog = input.cat;
gtdState.key = gtdQueryKey("inbox", "", []);
gtdState.entries = input.entries;
const out = [];
for (const q of ["rollout", "zzz"]) out.push(text(gtdList({ view: "inbox", area: "", ctx: [], q }, "")));
process.stdout.write(JSON.stringify({ Text: out }));`, map[string]any{"entries": es, "cat": viewsCatalog()}, &got)
	if !strings.Contains(got.Text[0], "Rollout date") || strings.Contains(got.Text[0], "Review the retry") {
		t.Errorf("filtered list: %s", got.Text[0])
	}
	if !strings.Contains(got.Text[1], `Nothing here matches "zzz"`) {
		t.Errorf("no match: %s", got.Text[1])
	}
}
