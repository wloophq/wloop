package web

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// reviewFixture is GET /api/review with the Inbox and the projects steps open.
func reviewFixture() map[string]any {
	step := func(id, label, view string, count int, done bool) map[string]any {
		return map[string]any{"id": id, "label": label, "view": view, "query": map[string]string{"view": view}, "count": count, "done": done}
	}
	return map[string]any{"id": "r-1", "started_at": "2026-10-22T12:00:00Z", "done": false, "steps": []any{
		step("inbox", "Inbox to zero", "inbox", 2, false),
		step("outbox", "Outbox", "outbox", 0, true),
		step("waiting", "Waiting: nudge?", "waiting", 0, true),
		step("projects", "Projects without a next action or past their review", "review", 1, false),
		step("someday", "Someday", "someday", 0, true),
		step("focus", "Prune Focus", "focus", 0, true),
	}}
}

func TestReviewKeysWeb(t *testing.T) {
	var got struct {
		Steps   []string `json:"steps"`
		Nav     []string `json:"nav"`
		Posts   int      `json:"posts"`
		Outside []string `json:"outside"`
		Button  []string `json:"button"`
		Context string   `json:"context"`
		Wired   []string `json:"wired"`
	}
	keysRun(t, "", `
const steps = [];
const o = { view: "review", reviewStep: (a) => { steps.push(a); return true; } };
o.onNav = (v) => { o.view = v; };
const k = harness(o);
await k.press("Enter");
await k.press("]");
await k.press("[");
const button = steps.length;
await k.press("Enter", { target: { tagName: "BUTTON" } });
const buttonSteps = steps.slice(button);
o.view = "inbox";
const before = steps.length;
await k.press("]");
await k.press("[");
const outside = steps.slice(before);
o.view = "";
await k.press("g"); await k.press("r");
const wired = [];
globalThis.readHash = () => ({ view: "review" });
globalThis.reviewKey = (a) => { wired.push(a); return true; };
const env = appEnv();
env.step("next");
return { steps: steps.slice(0, button), nav: k.log.nav, posts: k.log.posts.length, outside, button: buttonSteps, context: env.viewContext(), wired };`, nil, &got)
	if !slices.Equal(got.Steps, []string{"done", "next", "prev"}) {
		t.Errorf("Enter, ] and [ in the review ran %v", got.Steps)
	}
	if len(got.Button) != 0 || len(got.Outside) != 0 || got.Posts != 0 {
		t.Errorf("a button's Enter or ] [ outside the review ran a step: %+v", got)
	}
	if !slices.Equal(got.Nav, []string{"review"}) {
		t.Errorf("g r navigated to %v", got.Nav)
	}
	if got.Context != "review" || !slices.Equal(got.Wired, []string{"next"}) {
		t.Errorf("appEnv: context %q, step wired to %v", got.Context, got.Wired)
	}
}

func TestReviewStepsWeb(t *testing.T) {
	var got struct {
		First   string   `json:"first"`
		Gets    []string `json:"gets"`
		Posts   []string `json:"posts"`
		After   string   `json:"after"`
		Moves   []string `json:"moves"`
		Text    string   `json:"text"`
		Tasks   []string `json:"tasks"`
		Heads   []string `json:"heads"`
		Focused []string `json:"focused"`
	}
	review := reviewFixture()
	doneInbox := reviewFixture()
	doneInbox["steps"].([]any)[0].(map[string]any)["done"] = true
	project := task("e-p", "next", nil)
	project.Props, project.Title = map[string]json.RawMessage{"kind": json.RawMessage(`"project"`)}, "Ship the v2 API"
	project.ReviewDue, project.ReviewEvery = true, "168h0m0s"
	nodeRun(t, viewsPrelude(t), nil, `
const gets = [], posts = [], focused = [];
reviewEnv.getJSON = async (url) => {
  gets.push(url);
  if (url === "/api/review") return input.review;
  if (url.includes("view=inbox")) return { tasks: input.inbox, counters: { views: { inbox: 2 } } };
  return { tasks: input.projects };
};
reviewEnv.post = async (path, body) => { posts.push(path); return input.doneInbox; };
reviewEnv.notice = () => {};
reviewEnv.render = () => {};
reviewEnv.focusStep = (id) => focused.push(id);
(async () => {
  await loadReview();
  const first = reviewState.step;
  const inboxIDs = tasksState.tasks.map((e) => e.id);
  await reviewKey("done");
  const after = reviewState.step;
  const moves = [];
  await reviewKey("prev"); moves.push(reviewState.step);
  await reviewKey("prev"); moves.push(reviewState.step);
  await reviewKey("done");
  await reviewKey("next"); moves.push(reviewState.step);
  reviewState.step = "projects";
  await loadReviewStep();
  const panel = reviewPanel("");
  process.stdout.write(JSON.stringify({ first, gets, posts, after, moves, text: text(panel), tasks: tasksState.tasks.map((e) => e.id).concat(inboxIDs),
    heads: all(panel, (x) => String(x.attrs["data-focus"] || "").startsWith("review:step:")).map((x) => x.attrs["data-focus"]), focused }));
})();`, map[string]any{"review": review, "doneInbox": doneInbox, "inbox": []any{task("e-1", "inbox", nil), task("e-2", "inbox", nil)}, "projects": []any{project}}, &got)
	if got.First != "inbox" {
		t.Errorf("the review opened on %q, want the first open step", got.First)
	}
	if !slices.Equal(got.Posts, []string{"/api/review/steps/inbox/done"}) || got.After != "projects" {
		t.Errorf("Enter posted %v and moved to %q, want inbox done then projects", got.Posts, got.After)
	}
	if !slices.Equal(got.Moves, []string{"waiting", "outbox", "waiting"}) {
		t.Errorf("[ [ ] moved through %v", got.Moves)
	}
	if !slices.Contains(got.Gets, "/api/tasks?view=inbox") || !slices.Contains(got.Gets, "/api/tasks?view=review") {
		t.Errorf("step queries %v", got.Gets)
	}
	if !reflect.DeepEqual(got.Tasks, []string{"e-p", "e-1", "e-2"}) {
		t.Errorf("tasks %v", got.Tasks)
	}
	for _, s := range []string{"Review", "Inbox to zero", "Prune Focus", "Ship the v2 API", "review due", "no next action", "every 7d"} {
		if !strings.Contains(got.Text, s) {
			t.Errorf("the review lacks %q: %s", s, got.Text)
		}
	}
	if !slices.Equal(got.Heads, []string{"review:step:projects"}) || !slices.Contains(got.Focused, "projects") {
		t.Errorf("step header %v, focused %v", got.Heads, got.Focused)
	}
}

func TestProjectFactsShowTheNextActionAndTheLocalDay(t *testing.T) {
	var got struct {
		With    string `json:"with"`
		Without string `json:"without"`
	}
	p := task("e-p", "next", nil)
	p.Props, p.Title, p.Children, p.ReviewEvery = map[string]json.RawMessage{"kind": json.RawMessage(`"project"`)}, "Ship", []string{"e-a", "e-b"}, "168h0m0s"
	p.ReviewedAt = time.Date(2026, 10, 23, 2, 30, 0, 0, time.UTC)
	q := task("e-q", "next", nil)
	q.Props, q.Title = map[string]json.RawMessage{"kind": json.RawMessage(`"project"`)}, "Idle"
	nodeRun(t, viewsPrelude(t), nil, `
process.env.TZ = "America/Sao_Paulo";
process.stdout.write(JSON.stringify({ with: text(taskItem(input.p, "")), without: text(taskItem(input.q, "")) }));`, map[string]any{"p": p, "q": q}, &got)
	for _, s := range []string{"2 open", "every 7d", "reviewed 2026-10-22"} {
		if !strings.Contains(got.With, s) {
			t.Errorf("the project lacks %q: %s", s, got.With)
		}
	}
	if !strings.Contains(got.Without, "no next action") || strings.Contains(got.With, "no next action") {
		t.Errorf("no next action marks: %q / %q", got.With, got.Without)
	}
}
