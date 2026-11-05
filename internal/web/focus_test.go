package web

import (
	"encoding/json"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
)

// treeDOMJS gives the shim what focus needs: parents, data-* in dataset, closest and the page's queries over
// the board, the table and the tasks, and focus as the active element.
const treeDOMJS = `
const shimAppend = El.prototype.append;
El.prototype.append = function (...c) { shimAppend.apply(this, c); for (const x of this.children) if (x instanceof El) x.parent = this; };
El.prototype.replaceChildren = function (...c) { for (const x of this.children) if (x instanceof El) x.parent = null; this.children = []; this.append(...c); };
const shimSet = El.prototype.setAttribute;
El.prototype.setAttribute = function (k, v) { shimSet.call(this, k, v); if (k.startsWith("data-")) this.dataset[k.slice(5).replace(/-(\w)/g, (_, c) => c.toUpperCase())] = v; };
function selMatch(n, sel) {
  return sel.split(",").some((one) => {
    const m = /^\s*\[([\w-]+)(?:(\^?=)"(.*)")?\]\s*$/.exec(one);
    if (!m || !n.attrs) return false;
    const v = n.attrs[m[1]];
    if (v === undefined) return false;
    return !m[2] || (m[2] === "=" ? v === m[3] : String(v).startsWith(m[3]));
  });
}
El.prototype.closest = function (sel) { for (let n = this; n; n = n.parent) if (selMatch(n, sel)) return n; return null; };
const pageRoots = ["board", "table", "tasks"].map((id) => document.getElementById(id));
Object.defineProperty(El.prototype, "isConnected", { get() { let n = this; while (n.parent) n = n.parent; return pageRoots.includes(n); } });
El.prototype.focus = function () { document.activeElement = this; };
function under(root, sel) { const out = []; const w = (n) => { if (!(n instanceof El)) return; if (selMatch(n, sel)) out.push(n); n.children.forEach(w); }; w(root); return out; }
document.querySelectorAll = (sel) => { const at = /^#(\S+)\s+(.*)$/.exec(sel); return at ? under(document.getElementById(at[1]), at[2]) : pageRoots.flatMap((r) => under(r, sel)); };
document.querySelector = (sel) => document.querySelectorAll(sel)[0] || null;
const slotOf = (n) => (n && n.attrs && n.attrs["data-focus"]) || "";
function focusOn(n) { n.focus(); lastFocus = slotOf(n); }
`

// focusRun runs main over the cockpit, keys.js and the tree DOM.
func focusRun(t *testing.T, main string, in map[string]any, out any) {
	t.Helper()
	keys := strings.Replace(staticJS(t, "keys.js"), "if (typeof document !== \"undefined\" && typeof state !== \"undefined\") installKeys(appEnv());", "", 1)
	in["views"] = servedViews(t)
	nodeRun(t, cockpitScript(t)+keys+treeDOMJS, nil, `
Date.now = () => input.now;
applyCatalog(input.catalog);
tasksState.catalog = input.views;
state.health = { viewer: "me" };
state.specs = { version: "", byId: {} };
state.busy = new Set();
`+main, in, out)
}

// TestTheFocusOutlivesARender focuses a card's facet, a row's facet whose value then changes, and a draft row's
// title link, renders each list again, and restores the focus: the same control when it is still there, else
// its row.
func TestTheFocusOutlivesARender(t *testing.T) {
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	pr := tagRows()[1]
	pr["views"] = []string{"mine"}
	var got struct{ Card, CardAfter, Row, RowAfter, Link, LinkAfter string }
	focusRun(t, `state.rows = [input.pr];
const lens = lensOf({ ...readHash(), view: "mine" });
const board = () => renderBoard(pipeline(state.rows, lens).groups, "", lens);
const table = () => renderTable(pipeline(state.rows, lens), "", lens);
const drafts = () => { const tl = lensOf({ ...readHash(), view: "outbox" }); tasksState.tasks = input.drafts; document.getElementById("tasks").replaceChildren(taskTable(pipelineOf(taskLensItems(), tl, null), tl, "", taskLensItems())); };
const again = (draw) => { draw(); document.activeElement = document.body; restoreFocus(); return slotOf(document.activeElement); };
board();
focusOn(under(document.getElementById("board"), '[data-focus^="facet:card:"]')[0]);
const out = { Card: lastFocus, CardAfter: again(board) };
table();
focusOn(under(document.getElementById("table"), '[data-focus^="facet:row:"]').find((n) => n.attrs["data-focus"].includes(":waits-on:")));
out.Row = lastFocus;
state.rows = [{ ...input.pr, facets: { ...input.pr.facets, "waits-on": ["zed"] } }];
out.RowAfter = again(table);
drafts();
focusOn(under(document.getElementById("tasks"), '[data-focus^="task:"]').find((n) => n.attrs["data-focus"].endsWith(":title")));
out.Link = lastFocus;
out.LinkAfter = again(drafts);
process.stdout.write(JSON.stringify(out));`,
		map[string]any{"pr": wireRow("me", now, pr), "drafts": outboxTasks(), "catalog": json.RawMessage(servedCatalog(t)), "now": now.UnixMilli()}, &got)
	if got.Card == "" || got.CardAfter != got.Card {
		t.Errorf("a card's facet %q, after a render %q", got.Card, got.CardAfter)
	}
	if !strings.HasPrefix(got.Row, "facet:row:") || got.RowAfter != "row:github:acme/web#2" {
		t.Errorf("a row's facet %q whose value changed restores to %q, want its row", got.Row, got.RowAfter)
	}
	if !strings.HasSuffix(got.Link, ":title") || got.LinkAfter != got.Link {
		t.Errorf("a draft's title link %q, after a render %q", got.Link, got.LinkAfter)
	}
}

// TestKeysOnATaskCardsFacetActOnThatCard focuses a facet of the second of three task cards while another task
// is open: the verbs' selection is that card's task, and j moves to the third card.
func TestKeysOnATaskCardsFacetActOnThatCard(t *testing.T) {
	tasks := []api.TaskView{}
	for _, id := range []string{"e-1", "e-2", "e-3"} {
		tasks = append(tasks, task(id, "next", func(e *api.TaskView) {
			e.Facets = map[string][]string{"state": {"next"}, "waits-on": {"me"}, "turn": {"me"}}
		}))
	}
	var got struct{ Selected, Moved string }
	focusRun(t, `location.hash = "#view=inbox";
tasksState.tasks = input.tasks;
detailState.task = input.tasks[0];
const lens = lensOf({ ...readHash(), view: "inbox", layout: "board" });
document.getElementById("tasks").replaceChildren(taskBoard(pipelineOf(taskLensItems(), lens, null), lens, ""));
focusOn(under(document.getElementById("tasks"), '[data-focus^="facet:task:e-2"]')[0]);
const selected = (detailPane.selected() || {}).id;
appEnv().move(1);
process.stdout.write(JSON.stringify({ Selected: selected, Moved: slotOf(document.activeElement) }));`,
		map[string]any{"tasks": tasks, "catalog": json.RawMessage(servedCatalog(t)), "now": 0}, &got)
	if got.Selected != "e-2" || got.Moved != "task:e-3" {
		t.Errorf("from e-2's facet the verbs act on %q and j moves to %q, want e-2 and task:e-3", got.Selected, got.Moved)
	}
}

// TestACardKeepsItsRaisedPiecesUnderTheLaneHead pins that a card is its own stacking context, so the pieces
// raised over its stretched title stay under a board's sticky lane head.
func TestACardKeepsItsRaisedPiecesUnderTheLaneHead(t *testing.T) {
	raw, err := fs.ReadFile(files, "static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(raw)
	at := strings.Index(css, "\n.card { ")
	if at < 0 || !strings.Contains(css[at:at+strings.Index(css[at+1:], "\n")+1], "isolation: isolate") {
		t.Error(".card does not isolate its stacking context")
	}
}

// TestAPluginViewsListIsTheRow renders a plugin view's list with the host's list: its rows are the cockpit's
// rows, with facet buttons and its plugin's words, and an empty list says the profile's word.
func TestAPluginViewsListIsTheRow(t *testing.T) {
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	pr := tagRows()[1]
	pr["views"] = []string{"mine"}
	var got struct {
		Tag, Row, Empty, Given string
		Facets                 int
	}
	focusRun(t, `location.hash = "#view=mine";
state.rows = [input.pr];
const el = h("div", {}), empty = h("div", {}), given = h("div", {});
pluginList(el, state.rows, {});
pluginList(empty, [], {});
pluginList(given, [], { empty: "No pull requests" });
const table = el.children[0];
const tr = under(table, '[data-row^="row:"]')[0];
process.stdout.write(JSON.stringify({ Tag: table.tag, Row: tr ? tr.attrs["data-row"] : "", Facets: under(table, '[data-rove]').filter((n) => n.attrs.class === "facet").length,
  Empty: empty.textContent, Given: given.textContent }));`,
		map[string]any{"pr": wireRow("me", now, pr), "catalog": json.RawMessage(servedCatalog(t)), "now": now.UnixMilli()}, &got)
	if got.Tag != "table" || got.Row != "row:github:acme/web#2" || got.Facets == 0 {
		t.Errorf("a plugin view's list %+v, want the cockpit's row with its facet buttons", got)
	}
	if got.Empty != "Nothing here right now." || got.Given != "No pull requests" {
		t.Errorf("empty lists read %q and %q", got.Empty, got.Given)
	}
}
