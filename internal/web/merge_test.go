package web

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	dtask "github.com/wloophq/wloop/internal/domain/task"
)

// openTasks answer GET /api/tasks?match=, by the daemon's rule: the open tasks whose title holds the text,
// case and spaces around it aside.
const openTasks = `
const openTasks = [
  { id: "e-1", title: "Reply to the review", state: "inbox" },
  { id: "t-2", title: "Book the launch venue", state: "next" },
  { id: "t-3", title: "Order the badges", state: "someday" },
];
const matching = (url) => {
  const q = decodeURIComponent(url.split("match=")[1] || "").trim().toLowerCase();
  return { tasks: openTasks.filter((e) => e.title.toLowerCase().includes(q)) };
};
`

func TestTheCockpitPicksTheOtherTaskOfAMergeFromTheOpenTasks(t *testing.T) {
	type item struct {
		Label string `json:"label"`
		Key   string `json:"key"`
	}
	var got struct {
		Labels   []string `json:"labels"`
		Title    string   `json:"title"`
		First    []item   `json:"first"`
		Narrowed []string `json:"narrowed"`
		Gets     []string `json:"gets"`
		Posts    []posted `json:"posts"`
		Split    []string `json:"split"`
		Labels2  []string `json:"labels2"`
	}
	e := taskFixture()
	for _, v := range []string{"merge", "relate", "split", "unrelate"} {
		e["verbs"] = append(e["verbs"].([]map[string]any), map[string]any{"verb": v, "enabled": true})
	}
	keysRun(t, openTasks, `
const strip = (xs) => xs.map(({ label, key }) => ({ label, key }));
const k = harness({ selected: input, get: matching });
await k.press("k", { meta: true });
const labels = k.log.palette.items.map((i) => i.label);
await k.log.palette.items.find((i) => i.label === "Merge").run();
const pal = k.log.palette;
const narrowed = await pal.source(" VENUE");
await narrowed[0].run();
const merged = { ...input, merged_from: [{ id: "t-9", title: "Reply to the review, again", state: "inbox" }] };
const k2 = harness({ selected: merged, get: matching });
await k2.press("k", { meta: true });
const labels2 = k2.log.palette.items.map((i) => i.label);
await k2.log.palette.items.find((i) => i.label === "Split").run();
const split = k2.log.palette.items.map((i) => i.label);
await k2.log.palette.items[0].run();
return { labels, title: pal.title, first: strip(pal.items), narrowed: narrowed.map((i) => i.label), gets: k.log.gets,
  posts: [...k.log.posts, ...k2.log.posts], split, labels2 };`, e, &got)

	if !slices.Contains(got.Labels, "Merge") || !slices.Contains(got.Labels, "Relate") {
		t.Errorf("the palette lacks the profile's Merge and Relate: %v", got.Labels)
	}
	if slices.Contains(got.Labels, "Split") || slices.Contains(got.Labels, "Unrelate") {
		t.Errorf("the palette offers Split or Unrelate on a task with nothing merged or related: %v", got.Labels)
	}
	if got.Title != "Merge with which task" {
		t.Errorf("the picker is titled %q, want the verb's input", got.Title)
	}
	if want := []item{{"Book the launch venue", "next"}, {"Order the badges", "someday"}}; !reflect.DeepEqual(got.First, want) {
		t.Errorf("the picker opens on %+v, want the open tasks but the task itself %+v", got.First, want)
	}
	if !reflect.DeepEqual(got.Narrowed, []string{"Book the launch venue"}) {
		t.Errorf("typing venue narrows to %v", got.Narrowed)
	}
	if want := []string{"/api/tasks?" + api.TasksMatch + "=", "/api/tasks?" + api.TasksMatch + "=%20VENUE"}; !reflect.DeepEqual(got.Gets, want) {
		t.Errorf("the picker read %v, want the daemon's match of each text %v", got.Gets, want)
	}
	if !slices.Contains(got.Labels2, "Split") {
		t.Errorf("the palette lacks Split on a task with a task merged into it: %v", got.Labels2)
	}
	if !reflect.DeepEqual(got.Split, []string{"Reply to the review, again"}) {
		t.Errorf("Split picks from %v, want the tasks merged into it", got.Split)
	}
	want := []posted{
		{URL: "/api/tasks/e-1/merge", Body: map[string]any{"source": "cockpit", "args": map[string]any{dtask.ArgTask: "t-2"}}, API: "1"},
		{URL: "/api/tasks/e-1/split", Body: map[string]any{"source": "cockpit", "args": map[string]any{dtask.ArgTask: "t-9"}}, API: "1"},
	}
	for i := range got.Posts {
		got.Posts[i].API = "1"
	}
	if !reflect.DeepEqual(got.Posts, want) {
		t.Errorf("posted %+v, want %+v", got.Posts, want)
	}
}

func TestTheCockpitTaskPageShowsRelatedTasksAndAMergedFromTraceWithSplit(t *testing.T) {
	cat := taskCatalog()
	words := servedVerbSpecs(t)
	for _, v := range words {
		if v.Pick != "" {
			cat.Verbs = append(cat.Verbs, v)
		}
	}
	cat.Props = append(cat.Props, api.PropSpec{Name: "related", Type: "string[]", Role: string(dtask.RoleRelated)},
		api.PropSpec{Name: "merged", Type: "string[]", Role: string(dtask.RoleMerged)})
	e := pageTask(t)
	e.Children = nil
	for _, v := range []string{"merge", "relate", "split", "unrelate"} {
		e.Verbs = append(e.Verbs, api.VerbState{Verb: v, Enabled: true})
	}
	e.Props["related"] = raw(t, []string{"t-2", "t-4"})
	e.Props["merged"] = raw(t, []string{"t-9"})
	e.Related = []api.TaskRef{{ID: "t-2", Title: "Book the launch venue", State: "later"}, {ID: "t-4", Title: "Order the badges", State: "parked", Merged: true}}
	e.MergedFrom = []api.TaskRef{{ID: "t-9", Title: "Ship the retry fix", State: "triage", Within: []api.TaskRef{{ID: "t-8", Title: "Retry the uploads", State: "triage"}}}}
	answers := pageAnswers(t)
	answers["/api/tasks/"+e.ID] = e
	answers["/api/tasks?"+api.TasksMatch+"="] = map[string]any{"tasks": []map[string]string{{"id": e.ID, "title": e.Title, "state": "doing"}, {"id": "t-5", "title": "Write the runbook", "state": "parked"}}}
	var got struct {
		Sections []string
		Head     []string
		Props    []string
		Related  []string
		Merged   []string
		Buttons  []string
		Picker   struct {
			Title string
			Items []string
		}
		Posts []struct {
			Path string
			Body map[string]any
		}
	}
	nodeRun(t, cockpitScript(t)+strings.Replace(staticJS(t, "palette.js"), `"use strict";`, "", 1)+taskPageHarness, nil, "(async () => {\n"+`
tasksState.tasks = [{ id: "t-2", title: "Book the launch hall", state: "doing", units: [] }, { id: "t-4", title: "Stale copy", state: "doing", units: [] }];
let picker = null;
detailEnv.palette = (o) => { picker = o; };
const box = await page("t-0123456789abcdef");
const head = box.children[0];
const text = (n) => n.textContent;
const related = cls(box, "d-related")[0], merged = cls(box, "d-merged")[0];
const lis = (n) => walk(n, (x) => x.tag === "li").map(text);
await walk(head, (x) => x.tag === "button" && x.textContent === "Merge")[0].on.click();
await picker.items[0].run();
await focus(box, "related:t-2:unrelate:t-0123456789abcdef").on.click();
await focus(box, "merged:t-9:split:t-0123456789abcdef").on.click();
done({
  Sections: box.children.map((c) => c.attrs.class),
  Head: walk(head, (x) => x.tag === "button").map(text),
  Props: walk(head, (x) => x.tag === "dt").map(text),
  Related: lis(related), Merged: lis(merged),
  Buttons: walk(box, (x) => x.tag === "button" && ["Split", "Unrelate"].includes(x.textContent)).map((b) => b.attrs["data-focus"]),
  Picker: { Title: picker.title, Items: picker.items.map((i) => i.label + " " + i.key) },
  Posts: posts,
});`+"\n})().catch((e) => { console.error(e); process.exit(1); });", map[string]any{"catalog": cat, "answers": answers}, &got)

	if n := len(got.Sections); n < 2 || got.Sections[n-2] != "d-related" || got.Sections[n-1] != "d-merged" {
		t.Errorf("sections %v end without the related and merged-from sections", got.Sections)
	}
	if !slices.Contains(got.Head, "Merge") || !slices.Contains(got.Head, "Relate") || slices.Contains(got.Head, "Split") || slices.Contains(got.Head, "Unrelate") {
		t.Errorf("the header's verbs %v: want Merge and Relate there, Split and Unrelate on their rows", got.Head)
	}
	if slices.Contains(got.Props, "related") || slices.Contains(got.Props, "merged") {
		t.Errorf("the header lists the related or merged property as text: %v", got.Props)
	}
	if want := []string{"Book the launch hall Doing Unrelate", "Order the badges Parked Unrelate"}; !reflect.DeepEqual(got.Related, want) {
		t.Errorf("related rows %q, want the page's own copy of t-2 and the ref of a merged t-4 %q", got.Related, want)
	}
	if len(got.Merged) != 2 || !strings.HasPrefix(got.Merged[0], "Ship the retry fix To sort Split") || got.Merged[1] != "Retry the uploads To sort" {
		t.Errorf("merged-from rows %q: want t-9 with Split and t-8 under it without", got.Merged)
	}
	if want := []string{"related:t-2:unrelate:t-0123456789abcdef", "related:t-4:unrelate:t-0123456789abcdef", "merged:t-9:split:t-0123456789abcdef"}; !reflect.DeepEqual(got.Buttons, want) {
		t.Errorf("row buttons %v, want %v", got.Buttons, want)
	}
	if got.Picker.Title != "Merge with which task" || !reflect.DeepEqual(got.Picker.Items, []string{"Write the runbook Parked"}) {
		t.Errorf("the Merge picker %+v", got.Picker)
	}
	var paths []string
	var args []any
	for _, p := range got.Posts {
		paths = append(paths, p.Path)
		args = append(args, p.Body["args"])
	}
	wantArgs := func(id string) any { return map[string]any{dtask.ArgTask: id} }
	if want := []string{"/api/tasks/t-0123456789abcdef/merge", "/api/tasks/t-0123456789abcdef/unrelate", "/api/tasks/t-0123456789abcdef/split"}; !reflect.DeepEqual(paths, want) ||
		!reflect.DeepEqual(args, []any{wantArgs("t-5"), wantArgs("t-2"), wantArgs("t-9")}) {
		raw, _ := json.Marshal(got.Posts)
		t.Errorf("posted %s", raw)
	}
}
