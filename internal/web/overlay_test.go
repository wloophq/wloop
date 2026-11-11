package web

import (
	"strings"
	"testing"
)

// overlayDOM is a tree that removes, focuses and bubbles as a page does, for the overlays' own behaviour.
const overlayDOM = `
class Node { constructor() { this.parent = null; }
  remove() { if (!this.parent) return; this.parent.children = this.parent.children.filter((c) => c !== this); this.parent = null; } }
class Text extends Node { constructor(t) { super(); this.data = String(t); } get textContent() { return this.data; } }
class El extends Node {
  constructor(tag) { super(); this.tag = tag; this.children = []; this.attrs = {}; this.listeners = {}; this.value = ""; }
  append(...c) { for (const x of c) { const n = x instanceof Node ? x : new Text(x); n.remove(); n.parent = this; this.children.push(n); } }
  replaceChildren(...c) { for (const x of [...this.children]) x.remove(); this.append(...c); }
  get isConnected() { let n = this; while (n.parent) n = n.parent; return n === document.body || n === document.head; }
  setAttribute(k, v) { this.attrs[k] = v; }
  getAttribute(k) { return this.attrs[k]; }
  removeAttribute(k) { delete this.attrs[k]; }
  get className() { return this.attrs.class || ""; }
  set className(v) { this.attrs.class = v; }
  get textContent() { return this.children.map((c) => c.textContent).join(""); }
  addEventListener(type, fn) { (this.listeners[type] ||= []).push(fn); }
  focus() { const was = document.activeElement; if (was === this) return; document.activeElement = this; if (was && was.listeners) for (const fn of was.listeners.blur || []) fn({}); }
}
function walk(n, pred, out = []) { if (n instanceof El) { if (pred(n)) out.push(n); n.children.forEach((c) => walk(c, pred, out)); } return out; }
const document = { createElement: (t) => new El(t), createTextNode: (t) => new Text(t), head: new El("head"), body: new El("body"),
  getElementById: (id) => walk(document.head, (n) => n.attrs.id === id)[0] || walk(document.body, (n) => n.attrs.id === id)[0] || null,
  querySelectorAll: (sel) => walk(document.body, (n) => n.className.split(" ").includes(sel.slice(1))) };
document.activeElement = document.body;
// fire bubbles an event of type from el, then does what the page would by default: Enter on a button clicks it.
function fire(el, type, props = {}) {
  const e = { type, target: el, defaultPrevented: false, stopped: false, ...props, preventDefault() { this.defaultPrevented = true; }, stopPropagation() { this.stopped = true; } };
  for (let n = el; n && !e.stopped; n = n.parent) for (const fn of n.listeners[type] || []) fn(e);
  if (type === "keydown" && e.key === "Enter" && !e.defaultPrevented && el.tag === "button") fire(el, "click");
  return e;
}
const shades = () => walk(document.body, (n) => n.className === "pal-back");
const buttons = (shade) => walk(shade, (n) => n.tag === "button");
const tick = () => new Promise((r) => setTimeout(r, 0));
// track follows a promise: what it settled to, or "open".
function track(p) { const s = { v: "open" }; p.then((v) => { s.v = v; }); return s; }
// underneath opens an overlay as the omnibox does, with a button focused in it.
function underneath() {
  const btn = document.createElement("button");
  const box = document.createElement("div");
  box.append(btn);
  const o = { closed: 0, btn };
  o.close = palOverlay(box, () => { o.closed++; });
  btn.focus();
  return o;
}
`

func overlayRun(t *testing.T, main string, out any) {
	t.Helper()
	src := strings.Replace(staticJS(t, "omnibox.js"), `"use strict";`, "", 1)
	nodeScript(t, overlayDOM+src+"\n(async () => {\n"+main+"\n})().catch((e) => { console.error(e); process.exit(1); });", nil, out)
}

type overlayOutcome struct {
	Answer  any  `json:"answer"`
	Shades  int  `json:"shades"`
	Closed  int  `json:"closed"`
	Focused bool `json:"focused"`
}

// A question asked from an overlay, such as an action's confirm from the omnibox, opens over it and gives it
// back: the person who cancels lands where they were.
func TestAQuestionAskedFromAnOverlayLeavesItOpen(t *testing.T) {
	var got map[string]overlayOutcome
	overlayRun(t, `
const out = {};
for (const [name, ask, answer] of [
  ["cancel", () => askConfirm("Remove acme/api#8?"), (s) => fire(walk(s, (n) => n.attrs.role === "alertdialog")[0], "keydown", { key: "Escape" })],
  ["confirm", () => askConfirm("Remove acme/api#8?"), (s) => fire(buttons(s)[1], "click")],
  ["input", () => askInput({ label: "Snooze for" }), (s) => { const i = walk(s, (n) => n.tag === "input")[0]; i.value = "2d"; fire(i, "keydown", { key: "Enter" }); }],
]) {
  const o = underneath();
  const asked = track(ask());
  const open = shades().length;
  answer(shades()[shades().length - 1]);
  await tick();
  out[name] = { answer: asked.v, shades: open * 10 + shades().length, closed: o.closed, focused: document.activeElement === o.btn };
  o.close();
}
process.stdout.write(JSON.stringify(out));`, &got)

	want := map[string]any{"cancel": false, "confirm": true, "input": "2d"}
	for name, o := range got {
		if o.Answer != want[name] {
			t.Errorf("%s answered %v, want %v", name, o.Answer, want[name])
		}
		if o.Shades != 21 || o.Closed != 0 || !o.Focused {
			t.Errorf("%s: shades %d (open, then after; want 21), the overlay under it closed %d times, focus back on it %v", name, o.Shades, o.Closed, o.Focused)
		}
	}
	if len(got) != 3 {
		t.Errorf("ran %d cases, want 3", len(got))
	}
}

// A question whose overlay goes away without an answer, because another overlay replaces it or the panel
// redraws its page, answers as a cancel, so whoever awaits it is not left waiting.
func TestAQuestionWhoseOverlayGoesAwayAnswersCancel(t *testing.T) {
	var got map[string]overlayOutcome
	overlayRun(t, `
const out = {};
for (const [name, ask, away] of [
  ["confirm replaced", () => askConfirm("Remove acme/api#8?"), () => palOverlay(document.createElement("div"))],
  ["input replaced", () => askInput({ label: "Snooze for" }), () => palOverlay(document.createElement("div"))],
  ["confirm under a new page", () => askConfirm("Remove acme/api#8?"), () => palPage(document.createElement("div"))],
  ["under its overlay closed", () => askConfirm("Remove acme/api#8?"), null],
]) {
  const o = underneath();
  const asked = track(ask());
  if (away) away(); else o.close();
  await tick();
  out[name] = { answer: asked.v, shades: shades().length, closed: o.closed };
  palDropAll();
}
process.stdout.write(JSON.stringify(out));`, &got)

	for name, o := range got {
		if o.Answer != false && o.Answer != nil {
			t.Errorf("%s: the question answered %v, want a cancel", name, o.Answer)
		}
		if o.Closed != 1 {
			t.Errorf("%s: the overlay under it closed %d times, want once", name, o.Closed)
		}
	}
	if got["confirm replaced"].Shades != 1 || got["confirm under a new page"].Shades != 0 || got["under its overlay closed"].Shades != 0 {
		t.Errorf("overlays left on the page: %+v", got)
	}
	if len(got) != 4 {
		t.Errorf("ran %d cases, want 4", len(got))
	}
}

// The confirm keeps Tab inside it, so Enter and Esc keep reaching it; Enter on Cancel cancels, and the second
// press of a double click, which lands on the backdrop the first opened, does not dismiss it.
func TestTheConfirmKeepsTheKeysAndTheSecondClick(t *testing.T) {
	var got struct {
		FromLast  string `json:"fromLast"`
		FromFirst string `json:"fromFirst"`
		Prevented bool   `json:"prevented"`
		Double    any    `json:"double"`
		Single    any    `json:"single"`
		OnCancel  any    `json:"onCancel"`
	}
	overlayRun(t, `
const asked = track(askConfirm("Remove acme/api#8?"));
const [no, yes] = buttons(shades()[0]);
const e = fire(yes, "keydown", { key: "Tab" });
const fromLast = document.activeElement === no ? "cancel" : "elsewhere";
fire(no, "keydown", { key: "Tab", shiftKey: true });
const fromFirst = document.activeElement === yes ? "confirm" : "elsewhere";
const shade = shades()[0];
fire(shade, "mousedown", { detail: 2 });
await tick();
const double = asked.v;
fire(shade, "mousedown", { detail: 1 });
await tick();
const single = asked.v;
const again = track(askConfirm("Remove acme/api#8?"));
const cancel = buttons(shades()[0])[0];
cancel.focus();
fire(cancel, "keydown", { key: "Enter" });
await tick();
process.stdout.write(JSON.stringify({ fromLast, fromFirst, prevented: e.defaultPrevented, double, single, onCancel: again.v }));`, &got)

	if got.FromLast != "cancel" || got.FromFirst != "confirm" || !got.Prevented {
		t.Errorf("Tab leaves the confirm: from Confirm to %s, Shift+Tab from Cancel to %s, prevented %v", got.FromLast, got.FromFirst, got.Prevented)
	}
	if got.Double != "open" || got.Single != false {
		t.Errorf("a double click's second press answered %v (want open), a single click outside %v (want false)", got.Double, got.Single)
	}
	if got.OnCancel != false {
		t.Errorf("Enter on Cancel answered %v, want false", got.OnCancel)
	}
}
