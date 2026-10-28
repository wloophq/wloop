package web

import (
	"reflect"
	"testing"
)

type paletteItem struct {
	Label string `json:"label"`
	Key   string `json:"key"`
}

func TestPaletteShowsEveryKey(t *testing.T) {
	row := map[string]any{
		"unit": map[string]string{"provider": "github", "key": "acme/api#8"}, "label": "acme/api#8",
		"actions": []map[string]any{{"id": "hold", "enabled": true, "label": "Hold"}, {"id": "release", "enabled": false}, {"id": "dismiss", "enabled": true, "label": "Dismiss the turn"}, {"id": "act", "enabled": true, "label": "Act"}},
	}
	var got struct {
		Title string        `json:"title"`
		Items []paletteItem `json:"items"`
		Help  []struct {
			Context  string `json:"context"`
			Bindings []struct {
				Key    string `json:"key"`
				Action string `json:"action"`
			} `json:"bindings"`
		} `json:"help"`
		Bindings int           `json:"bindings"`
		Areas    []paletteItem `json:"areas"`
		Picked   []string      `json:"picked"`
		Unit     []any         `json:"unit"`
		Narrowed []paletteItem `json:"narrowed"`
	}
	keysRun(t, "", `
const k = harness({ selected: input.task, rows: [input.row], areas: ["OSS", "Hiring"] });
await k.press("k", { meta: true });
const pal = k.log.palette;
await k.press("?");
const strip = (xs) => xs.map(({ label, key }) => ({ label, key }));
await pal.items.find((i) => i.label === "Dismiss the turn").run();
await pal.items.find((i) => i.label === "Act").run();
await pal.items.find((i) => i.label === "Area...").run();
const areas = k.log.palette;
areas.items[2].run();
return { title: pal.title, items: strip(pal.items), help: k.log.help, bindings: BINDINGS.length, areas: strip(areas.items), picked: k.log.area, unit: k.log.unit,
  narrowed: strip(paletteMatch(pal.items, "TO next")) };`, map[string]any{"task": taskFixture(), "row": row}, &got)

	has := func(label, key string) bool {
		for _, it := range got.Items {
			if it.Label == label && it.Key == key {
				return true
			}
		}
		return false
	}
	for _, want := range []paletteItem{
		{"to Next", "x"}, {"to Waiting", "w"}, {"schedule or snooze", "s"}, {"to Someday", "y"}, {"to Reference", "l"},
		{"toggle Focus", "f"}, {"make a project or attach to one", "p"}, {"approve the draft or proposal", "a"},
		{"delegate to an agent", "d"}, {"done", "e"}, {"dismiss", "Backspace"},
		{"Hold", "H"}, {"Dismiss the turn", "X"}, {"Act", ""}, {"open the source", "o"},
		{"capture", "n"}, {"Inbox", "g i"}, {"GitHub", "g g"}, {"filter", "/"}, {"help", "?"}, {"Area...", ""},
	} {
		if !has(want.Label, want.Key) {
			t.Errorf("the palette lacks %q on %q: %+v", want.Label, want.Key, got.Items)
		}
	}
	if has("Release", "H") || has("seen", "") {
		t.Errorf("the palette offers what the selection does not: %+v", got.Items)
	}
	if len(got.Unit) != 2 {
		t.Errorf("palette items run their unit actions: %v", got.Unit)
	}
	if !reflect.DeepEqual(got.Areas, []paletteItem{{"No area", ""}, {"OSS", "Shift+1"}, {"Hiring", "Shift+2"}}) || !reflect.DeepEqual(got.Picked, []string{"Hiring"}) {
		t.Errorf("area picker %+v picked %v", got.Areas, got.Picked)
	}
	if !reflect.DeepEqual(got.Narrowed, []paletteItem{{"to Next", "x"}}) {
		t.Errorf("narrowed %+v", got.Narrowed)
	}

	var order []string
	n := 0
	for _, g := range got.Help {
		order = append(order, g.Context)
		n += len(g.Bindings)
	}
	if !reflect.DeepEqual(order, []string{"global", "list", "question", "plan", "review"}) || n != got.Bindings {
		t.Errorf("help groups %v hold %d of %d bindings", order, n, got.Bindings)
	}
}
