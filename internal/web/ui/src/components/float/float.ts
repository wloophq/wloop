import { brand } from "../../brand";
import { conventionalLabel, tally, type Label, type LabelCount } from "../../lib/conventional";
import { t } from "../../lib/i18n.svelte";
import { isSet, safeURL, type J } from "../../lib/util";
import { floatDeps, floatIdleWhileHidden, floatState, itemKey, openRoute, taskRoute, type FloatFilter, type FloatMode, type FloatPlace, type PanelPlace } from "../../state/float.svelte";
import { queueDeps, queueResolve, queueUnit, queueVerb, unitLink } from "../screens/queue";

// The floating widget's ends: resolving an item through the gates endpoint, and the bridge to the window that
// hosts each of its two pages. The host calls window[FLOAT_BRIDGE]: the tab's place, and the panel's show, leave as
// the pointer has left both windows, and blur as its window resigns key; each page posts JSON messages through the
// Wails runtime's message handler, which a plain browser lacks, so there they go nowhere.

// FLOAT_BRIDGE is the window property the host calls: the brand's command followed by Float.
export const FLOAT_BRIDGE = brand.command + "Float";

// FloatRole is which of the widget's two pages a window loads.
export type FloatRole = "tab" | "panel";

// toneClass is the class that draws a thing in tone, a design token the daemon serves; "" for none.
export const toneClass = (tone: unknown): string => (typeof tone === "string" && /^[a-z]+$/.test(tone) ? "tone-" + tone : "");

export interface TabBridge {
  place: (p?: Partial<FloatPlace>) => void;
}

export interface PanelBridge {
  show: (m: FloatMode, p?: Partial<PanelPlace> & { item?: string; filter?: FloatFilter }) => void;
  shown: () => void;
  leave: () => void;
  blur: () => void;
}

interface Handler {
  postMessage: (message: string) => void;
}

const handlerOf = (win: J): Handler | null => (win && win.webkit && win.webkit.messageHandlers && win.webkit.messageHandlers.external) || null;

// floatPost sends message to the host as JSON; nothing in a browser.
export function floatPost(message: J, win: J = window): void {
  const h = handlerOf(win);
  if (h) h.postMessage(JSON.stringify(message));
}

const sent = { w: -1, h: -1, mode: "" };

// floatSize tells the host the size of what the page draws, el, and the panel's mode, "" for the tab; again only
// when it changed, unless force.
export function floatSize(el: Element | undefined | null, mode: FloatMode, force = false): void {
  if (!el) return;
  const r = el.getBoundingClientRect();
  const w = Math.ceil(r.width);
  const h = Math.ceil(r.height);
  if (!force && w === sent.w && h === sent.h && mode === sent.mode) return;
  Object.assign(sent, { w, h, mode });
  floatDeps.post(mode ? { type: "float:size", w, h, mode } : { type: "float:size", w, h });
}

// peekLeaveMS is how long a peek's card takes to leave: the stylesheet's --motion-peek-out, 0 under reduced motion
// or where no stylesheet names it, since then no transition runs to wait for.
export function peekLeaveMS(win: J = window): number {
  if (typeof win.matchMedia === "function" && win.matchMedia("(prefers-reduced-motion: reduce)").matches) return 0;
  const ms = parseFloat(win.getComputedStyle(win.document.documentElement).getPropertyValue("--motion-peek-out"));
  return Number.isFinite(ms) && ms > 0 ? ms : 0;
}

// resetFloatSize forgets the size last told; tests call it between cases.
export function resetFloatSize(): void {
  Object.assign(sent, { w: -1, h: -1, mode: "" });
}

let dragging: (() => void) | null = null;

// floatDrag hands a drag of the tab to the host, which moves the window as Wails moves any window it drags, and
// tells it the size again once the pointer is released, so it snaps and keeps the place where it dropped. The window
// server may keep the release from the page, so the host's next place, or the next drag, ends the drag as well.
export function floatDrag(dropped: () => void, win: J = window): void {
  const h = handlerOf(win);
  if (!h) return;
  floatDragEnd();
  h.postMessage("wails:drag");
  const done = () => {
    for (const type of ["pointerup", "mouseup"]) win.removeEventListener(type, done, true);
    if (dragging === done) dragging = null;
    dropped();
  };
  dragging = done;
  for (const type of ["pointerup", "mouseup"]) win.addEventListener(type, done, true);
}

// floatDragEnd ends a drag still waiting for its release.
export function floatDragEnd(): void {
  if (dragging) dragging();
}

// hovered marks node with data-hover while the pointer is over it: the host's synthetic pointer events reach a float
// that is not key, and they never trigger :hover.
export function hovered(node: HTMLElement): { destroy: () => void } {
  const on = () => node.setAttribute("data-hover", "");
  const off = () => node.removeAttribute("data-hover");
  node.addEventListener("pointerenter", on);
  node.addEventListener("pointerleave", off);
  return {
    destroy: () => {
      node.removeEventListener("pointerenter", on);
      node.removeEventListener("pointerleave", off);
    },
  };
}

// keepRoutes hands every same-origin route a plain click on a link would follow to the cockpit's main window, so
// the float window never leaves its own route; a modified or other-button click keeps its default.
export function keepRoutes(doc: J): void {
  doc.addEventListener(
    "click",
    (e: MouseEvent) => {
      if (e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
      const t = e.target as Element | null;
      const a = t && t.closest ? t.closest('a[href^="#"]') : null;
      const href = a ? a.getAttribute("href") || "" : "";
      if (!href) return;
      e.preventDefault();
      e.stopImmediatePropagation();
      openRoute(href.replace(/^#/, ""));
    },
    true,
  );
}

// installFloat exposes the bridge the host calls on role's page and wires the widget's ends; the tab's page asks
// for the peeks.
export function installFloat(win: J, role: "tab"): TabBridge;
export function installFloat(win: J, role: "panel"): PanelBridge;
export function installFloat(win: J, role: FloatRole): TabBridge | PanelBridge {
  const f = floatState();
  const bridge: TabBridge | PanelBridge =
    role === "tab"
      ? {
          place: (p) => {
            floatDragEnd();
            f.setPlace(p);
          },
        }
      : { show: (m, p) => f.show(m, p), shown: () => f.shown(), leave: () => f.leave(), blur: () => f.blur() };
  win[FLOAT_BRIDGE] = bridge;
  floatDeps.resolve = floatResolve;
  floatDeps.run = floatRun;
  floatDeps.openUnit = floatOpenUnit;
  floatDeps.post = (m) => floatPost(m, win);
  floatDeps.focused = () => !!win.document && win.document.hasFocus();
  if (role === "tab") floatDeps.peek = (item) => floatDeps.post({ type: "float:peek", item: itemKey(item) });
  else floatIdleWhileHidden();
  return bridge;
}

// floatResolve resolves item's gate with intent as the cockpit's queue does (queueResolve).
export async function floatResolve(item: J, intent: string, option?: string): Promise<unknown> {
  return item.gate ? queueResolve(item, intent, option) : null;
}

// floatRun runs a primary that is no gate's: a unit's catalog action, a task verb (a failed delivery's retry),
// opening the unit at its source, or the task in the cockpit.
export async function floatRun(item: J): Promise<unknown> {
  const p = item.primary || {};
  const u = queueUnit(item);
  if (p.kind === "action" && u) {
    if ((await queueDeps.act(u, p.id)) === false) throw new Error(t("{action} failed", { action: p.label || p.id }));
    return true;
  }
  if (p.kind === "verb") {
    await queueVerb(item);
    return true;
  }
  if (p.kind === "open") {
    const link = itemLink(item);
    if (link) return queueDeps.open(link);
  }
  openRoute(taskRoute(item.task && item.task.id));
  return true;
}

// itemLink is the link of item's unit at its source, empty when no card of its task links it.
export const itemLink = (item: J): string => unitLink(item.task, queueUnit(item));

// floatOpenUnit opens item's unit at its source, doing nothing when no card links it.
export function floatOpenUnit(item: J): void {
  const link = itemLink(item);
  if (link) queueDeps.open(link);
}

// LATE_MS is the age past which an item's age reads as late.
export const LATE_MS = 24 * 3600 * 1000;

// itemRef is how a person finds item's unit: its task's first unit label.
export const itemRef = (item: J): string => ((item && item.task && item.task.unit_labels) || [])[0] || "";

// itemTitle is what item is about: its task's title, else its gate's text.
export const itemTitle = (item: J): string => (item && item.task && item.task.title) || (item && item.gate && item.gate.text) || "";

// itemText is what item asks: its gate's text, else its situation.
export const itemText = (item: J): string => (item && item.gate && item.gate.text) || (item && item.situation && item.situation.label) || "";

// sinceMS is when item's decision began, in ms; NaN when the daemon did not say.
export const sinceMS = (item: J): number => (item && isSet(item.since) ? Date.parse(item.since) : NaN);

// oldest is the earliest start of items' decisions, in ms; NaN when none says.
export function oldest(items: J[]): number {
  const all = items.map(sinceMS).filter((n) => !Number.isNaN(n));
  return all.length ? Math.min(...all) : NaN;
}

// duration is ms in the compact words of a figure: seconds under a minute, minutes under an hour, then hours.
export function duration(ms: number): string {
  const s = Math.max(0, Math.floor(ms / 1000));
  if (s < 60) return t("{seconds}s", { seconds: s });
  const m = Math.floor(s / 60);
  if (m < 60) return t("{minutes}m", { minutes: m });
  const h = Math.floor(m / 60);
  return m % 60 ? t("{hours}h {minutes}m", { hours: h, minutes: m % 60 }) : t("{hours}h", { hours: h });
}

// progress is how far elapsed is along usual, from 0 to 1; 0 when no usual duration is known.
export const progress = (elapsed: number, usual: number): number => (usual > 0 ? Math.min(1, Math.max(0, elapsed / usual)) : 0);

export interface DraftComment extends Partial<Label> {
  at: string;
  body: string;
}

export interface DraftSummary {
  verdict: string;
  tone: string;
  comments: DraftComment[];
  tally: LabelCount[];
}

// fieldAt is the field of fields, at any depth, at pointer.
function fieldAt(fields: J[], pointer: string): J {
  for (const f of fields || []) {
    if (f.pointer === pointer) return f;
    const g = fieldAt(f.fields, pointer);
    if (g) return g;
  }
  return null;
}

// draftSummary reads a draft as its card shows it: the verdict and its tone the daemon serves, and the comments of
// the list its plugin names, each with its Markdown value (else its last) as the body and its first two other
// values as where it sits.
export function draftSummary(d: J): DraftSummary {
  const list = d && d.comments_at ? fieldAt(d.fields, d.comments_at) : null;
  const comments: DraftComment[] = ((list && list.fields) || []).map((c: J) => {
    const leaves: J[] = (c.fields || []).filter((x: J) => !(x.fields || []).length);
    const body = leaves.find((x) => x.markdown) || leaves[leaves.length - 1];
    const text = body ? String(body.value || "") : "";
    const [file, line] = leaves.filter((x) => x !== body).map((x) => x.shown || x.value);
    const at = line ? t("{file}:{line}", { file, line }) : file || "";
    return { at, body: text, ...(conventionalLabel(text) || {}) };
  });
  return { verdict: (d && d.verdict) || "", tone: (d && d.verdict_tone) || "", comments, tally: tally(comments) };
}
