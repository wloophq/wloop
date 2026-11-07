package web

import (
	"io/fs"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/record"
	intentload "github.com/wloophq/wloop/internal/intent"
)

func TestEveryNewKeyIsInTheHelp(t *testing.T) {
	help, problems := intentload.BundledKeys(os.DirFS("../../profiles/default"))
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	pt, err := fs.ReadFile(os.DirFS("../../profiles/default"), "locales/pt-BR/keys.toml")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []struct{ key, action, context string }{
		{"]", "tab:next", "detail"},
		{"[", "tab:prev", "detail"},
		{"+", "filter:add", "list"},
	} {
		label := help.Help(want.context, want.action)
		if label == "" {
			t.Errorf("keys.toml has no help for %s", want.action)
		}
		if !strings.Contains(string(pt), `"`+want.action+`" =`) {
			t.Errorf("the pt-BR keys.toml has no help for %s", want.action)
		}
		i := slices.IndexFunc(webBindings(t), func(v api.KeyView) bool { return v.Key == want.key && v.Action == want.action })
		if i < 0 {
			t.Errorf("keys.web.json has no %q bound to %s", want.key, want.action)
			continue
		}
		if got := webBindings(t)[i]; got.Context != want.context || got.Help != label {
			t.Errorf("%q in keys.web.json is %s with help %q, want %s with the profile's %q", want.key, got.Context, got.Help, want.context, label)
		}
	}
}

// unfocusableJS is unfocusable(tree): every node of tree, over the tree h or the DOM shim, that answers a
// click and that no key reaches: neither a button nor a link with an href, nor a node with a tabindex of 0.
const unfocusableJS = `
function unfocusable(n, out = []) {
  if (Array.isArray(n)) { n.forEach((c) => unfocusable(c, out)); return out; }
  if (!n || typeof n !== "object") return out;
  const attrs = n.attrs || {};
  if (n.tag && (typeof attrs.onclick === "function" || (n.on && n.on.click))) {
    const ok = n.tag === "button" || (n.tag === "a" && attrs.href) || String(attrs.tabindex) === "0";
    if (!ok) out.push(n.tag + "." + (attrs.class || "") + "[" + (attrs["data-focus"] || "") + "]");
  }
  unfocusable(n.children, out);
  unfocusable(n.kids, out);
  return out;
}
`

func TestEveryControlOfTheNewSurfacesIsFocusable(t *testing.T) {
	check := func(t *testing.T, got map[string][]string, surfaces ...string) {
		t.Helper()
		for _, s := range surfaces {
			if _, ok := got[s]; !ok {
				t.Errorf("%s was not drawn", s)
			}
		}
		for s, nodes := range got {
			if len(nodes) > 0 {
				t.Errorf("%s: controls no key reaches: %q", s, nodes)
			}
		}
	}
	t.Run("gate, draft and Outbox", func(t *testing.T) {
		var got map[string][]string
		g := questionGate(t, "Use `retry()`", "a", record.Option{ID: "a", Label: "Retry", Consequence: "Runs it again."}, record.Option{ID: "b", Label: "Revert"})
		runGate(t, unfocusableJS+outboxHarness+`
done({ gateCard: unfocusable(card(input.gate)), draftCard: unfocusable(draft(input.page.groups[0].drafts[0])), outboxPage: unfocusable(box()) });`,
			map[string]any{"gate": g, "page": outboxFixture()}, &got)
		check(t, got, "gateCard", "draftCard", "outboxPage")
	})
	t.Run("task page and timeline", func(t *testing.T) {
		var got map[string][]string
		d := pendingDraft()
		d.Task = "t-0123456789abcdef"
		e := pageTask(t)
		e.Drafts = []int64{d.ID}
		answers := pageAnswers(t)
		answers["/api/tasks/"+e.ID] = e
		answers["/api/drafts?task="+e.ID] = api.DraftsResponse{Drafts: []api.Draft{d}}
		runTaskPage(t, unfocusableJS+`
const out = {};
let box = await page("t-0123456789abcdef");
const tabs = walk(box, (x) => String(x.attrs["data-focus"] || "").startsWith("task:tab:")).map((x) => x.attrs["data-focus"].slice(9));
for (const tab of tabs) {
  taskState.tab = tab;
  box = await page("t-0123456789abcdef");
  out["task:" + tab] = unfocusable(box);
}
out.timelineNodes = unfocusable(timelineNodes(input.answers[Object.keys(input.answers).find((k) => k.includes("/timeline"))] || []));
done(out);`, answers, &got)
		check(t, got, "task:timeline", "task:brief", "task:runs", "timelineNodes")
	})
	t.Run("rows and toolbar", func(t *testing.T) {
		var got map[string][]string
		nodeRun(t, toolbarPrelude(t)+unfocusableJS, nil, `
const lens = lensOf(route({ view: "mine", q: "wait:me risk:high", g: "risk" }));
process.stdout.write(JSON.stringify({ rowView: unfocusable(input.tasks.map((e) => taskRow(e))), lensToolbar: unfocusable(lensToolbar(lens, pipeline(state.rows, lens))) }));`,
			mixedInput(t, map[string]any{"rows": mineRows()}), &got)
		check(t, got, "rowView", "lensToolbar")
	})
	t.Run("rail", func(t *testing.T) {
		var got map[string][]string
		nodeRun(t, viewsPrelude(t)+railDoc+unfocusableJS, nil, `
tasksState.catalog = input.cat;
const nav = viewNav(input.cat, [], [], input.counters);
renderRail(nav, railRoute("inbox"));
const closed = unfocusable(railEls.views.kids);
byFocus(railEls.views.kids, "rail:empties").attrs.onclick();
process.stdout.write(JSON.stringify({ renderRail: closed, "renderRail open": unfocusable(railEls.views.kids) }));`,
			map[string]any{"cat": viewsCatalog(), "counters": api.Counters{Views: map[string]int{"inbox": 3, "someday": 0}}}, &got)
		check(t, got, "renderRail", "renderRail open")
	})
}

// cssRules are the rules of css as selector and body, the rules nested in an at-rule among them.
func cssRules(css string) [][2]string {
	css = regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(css, "")
	var out [][2]string
	for _, m := range regexp.MustCompile(`([^{}]+)\{([^{}]*)\}`).FindAllStringSubmatch(css, -1) {
		out = append(out, [2]string{strings.TrimSpace(m[1][strings.LastIndex(m[1], ";")+1:]), m[2]})
	}
	return out
}

// motionCSS is app.css and the palette's own stylesheet.
func motionCSS(t *testing.T) string {
	t.Helper()
	app, err := fs.ReadFile(files, "static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	pal := regexp.MustCompile("(?s)const PALETTE_CSS = `(.*?)`").FindStringSubmatch(staticJS(t, "palette.js"))
	if pal == nil {
		t.Fatal("palette.js has no PALETTE_CSS")
	}
	return string(app) + "\n" + pal[1]
}

func TestMotionStopsUnderReducedMotion(t *testing.T) {
	css := motionCSS(t)
	if !regexp.MustCompile(`@media \(prefers-reduced-motion: reduce\)\s*\{\s*:root\s*\{\s*--motion-fast:\s*0ms;\s*\}`).MatchString(css) {
		t.Error("the token region does not set --motion-fast to 0ms under prefers-reduced-motion: reduce")
	}
	if strings.Contains(css, "@keyframes") || regexp.MustCompile(`(^|[;{\s])animation(-[a-z-]+)?\s*:`).MatchString(css) {
		t.Error("a stylesheet declares an animation; motion is a transition on the motion token, in answer to an action")
	}
	decl := regexp.MustCompile(`(?:^|;)\s*transition(-[a-z-]+)?\s*:([^;]*)`)
	moving := map[string]bool{}
	for _, r := range cssRules(css) {
		for _, m := range decl.FindAllStringSubmatch(r[1], -1) {
			for _, part := range strings.Split(m[2], ",") {
				if m[1] != "" || !strings.Contains(part, "var(--motion-fast)") || !strings.Contains(part, "var(--motion-ease)") {
					t.Errorf("%s: transition%s %q does not read var(--motion-fast) var(--motion-ease)", r[0], m[1], strings.TrimSpace(part))
				}
			}
			moving[r[0]] = true
		}
	}
	// The rules that move answer an action: opening the detail or the palette, resolving a decision card,
	// and hover, focus or press on a control. A class live data toggles moves nothing.
	want := []string{"#detail", ".pal-back", ".d-gate, .d-draft", ".outbox .d-draft", ".find", ".views .nav", ".opt", ".tab", ".seg button", ".tools .pill"}
	var got []string
	for s := range moving {
		got = append(got, s)
	}
	for _, s := range want {
		if !moving[s] {
			t.Errorf("%s has no transition on the motion token", s)
		}
	}
	for _, s := range got {
		if !slices.Contains(want, s) && !strings.HasPrefix(s, ".act") {
			t.Errorf("%s moves: only an action's answer moves, add it here once it answers one", s)
		}
	}
	for _, s := range []string{"#detail", ".pal-back"} {
		if !regexp.MustCompile(`@starting-style\s*\{\s*` + regexp.QuoteMeta(s) + `\s*\{`).MatchString(css) {
			t.Errorf("%s has no @starting-style to open from", s)
		}
	}
}

func TestTheTabsShowTheKeysThatStepThem(t *testing.T) {
	var got struct{ Hint, Shortcuts string }
	runTaskPage(t, `
const strip = cls(taskTabs(input.answers.task).body, "tabs")[0];
done({ Hint: cls(strip, "keyhint").map((x) => x.textContent).join("|"), Shortcuts: strip.attrs["aria-keyshortcuts"] || "" });`,
		map[string]any{"task": pageTask(t)}, &got)
	if got.Hint != "[ ]" || got.Shortcuts != "[ ]" {
		t.Errorf("the tab strip hints %q with shortcuts %q, want the keys of tab:prev and tab:next", got.Hint, got.Shortcuts)
	}
}

func TestResolvingACardDimsItUntilTheAnswer(t *testing.T) {
	var got []string
	g := questionGate(t, "Retry?", "")
	runGate(t, `
const marks = [];
const drawn = { classList: { toggle: (c, on) => marks.push(c + ":" + on) }, setAttribute: (k, v) => marks.push(k + "=" + v) };
document.querySelector = (s) => (s === '.d-gate[data-gate="3"]' || s === '.d-draft[data-draft="7"]' ? drawn : null);
detailEnv.post = async () => { marks.push("posted"); return {}; };
await resolveGate({ id: "t-1" }, input.gate, "dismiss");
detailEnv.post = async () => { throw new Error("gone"); };
await resolveDraft({ id: 7, gate: 12, rev: 1 }, "approve", "Approve", {});
done(marks);`, map[string]any{"gate": g}, &got)
	want := []string{"resolving:true", "aria-busy=true", "posted", "resolving:true", "aria-busy=true", "resolving:false", "aria-busy=false"}
	if !slices.Equal(got, want) {
		t.Errorf("resolving marked %q, want %q: dimmed while posting, back when it fails", got, want)
	}
}
