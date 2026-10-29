package web

import (
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/unit"
)

var slackThread = unit.Unit{Provider: "slack", Key: "T1/C1/1700000000.000100"}

const composeHarness = `
detailEnv.postJSON = async (path, body) => { posts.push({ path, body }); return { id: 9, state: body.send ? "approved" : "proposed" }; };
const typeInto = (pane, focus, value) => byFocus(pane, focus).attrs.oninput({ target: { value } });
const click = async (pane, focus) => { await byFocus(pane, focus).attrs.onclick(); };
applyPlugins([{ id: "slack", name: "Slack", enabled: true, reacts: true }, { id: "github", name: "GitHub", enabled: true }]);
`

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
  out[q] = input.tasks.filter((e) => taskMatches(e, filterTerms(q))).map((e) => e.id);
}
process.stdout.write(JSON.stringify(out));`, map[string]any{"tasks": es}, &got)
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
tasksState.catalog = input.cat;
tasksState.key = viewQueryKey("inbox", "", []);
tasksState.tasks = input.tasks;
const out = [];
for (const q of ["rollout", "zzz"]) out.push(text(taskList({ view: "inbox", area: "", ctx: [], q }, "")));
process.stdout.write(JSON.stringify({ Text: out }));`, map[string]any{"tasks": es, "cat": viewsCatalog()}, &got)
	if !strings.Contains(got.Text[0], "Rollout date") || strings.Contains(got.Text[0], "Review the retry") {
		t.Errorf("filtered list: %s", got.Text[0])
	}
	if !strings.Contains(got.Text[1], `Nothing here matches "zzz"`) {
		t.Errorf("no match: %s", got.Text[1])
	}
}

func TestUnitComposerStartsOnTheServedSend(t *testing.T) {
	var got map[string]string
	nodeRun(t, viewsPrelude(t)+detailHarness+composeHarness, nil, `
const out = {};
for (const send of ["now", "draft"]) {
  tasksState.catalog = { verbs: [{ verb: "respond", handler: "reply", label: "Reply", send }] };
  const box = unitExtras({ row: { unit: input.unit }, items: [] });
  out[send] = ["send", "save"].filter((b) => String(byFocus(box, "unit:reply:" + b).attrs.class).includes("primary")).join(",");
}
process.stdout.write(JSON.stringify(out));`, map[string]any{"unit": slackThread}, &got)
	if got["now"] != "send" || got["draft"] != "save" {
		t.Fatalf("primary by served send %v", got)
	}
}
