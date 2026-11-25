import { brand } from "../../brand";
import { conventionalLabel, tally, type Label, type LabelCount } from "../../lib/conventional";
import { t } from "../../lib/i18n.svelte";
import { isSet, safeURL, type J } from "../../lib/util";
import { floatDeps, floatState, openRoute, taskRoute, type FloatMode, type FloatPlace } from "../../state/float.svelte";
import { resolveDraft, resolveGate } from "../gates/gates";
import { queueDeps, queueDraft, queueDraftOf, queueUnit } from "../screens/queue";

// The floating widget's ends: resolving an item through the gates endpoint, and the bridge to the window that
// hosts the page. The host calls window[FLOAT_BRIDGE].setMode; the page posts JSON messages through the Wails
// runtime's message handler, which a plain browser lacks, so there they go nowhere.

// FLOAT_BRIDGE is the window property the host calls: the brand's command followed by Float.
export const FLOAT_BRIDGE = brand.command + "Float";

// toneClass is the class that draws a thing in tone, a design token the daemon serves; "" for none.
export const toneClass = (tone: unknown): string => (typeof tone === "string" && /^[a-z]+$/.test(tone) ? "tone-" + tone : "");

export interface FloatBridge {
  setMode: (m: FloatMode, place?: Partial<FloatPlace>) => void;
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

// floatSize tells the host the size of what the page draws, el, in mode; again only when it changed, unless force.
export function floatSize(el: Element | undefined | null, mode: FloatMode, force = false): void {
  if (!el) return;
  const r = el.getBoundingClientRect();
  const w = Math.ceil(r.width);
  const h = Math.ceil(r.height);
  if (!force && w === sent.w && h === sent.h && mode === sent.mode) return;
  Object.assign(sent, { w, h, mode });
  floatDeps.post({ type: "float:size", w, h, mode });
}

// resetFloatSize forgets the size last told; tests call it between cases.
export function resetFloatSize(): void {
  Object.assign(sent, { w: -1, h: -1, mode: "" });
}

// floatDrag hands a drag of the pill to the host, which moves the window as Wails moves any window it drags, and
// tells it the size again once the pointer is released, so it snaps and keeps the place where it dropped.
export function floatDrag(dropped: () => void, win: J = window): void {
  const h = handlerOf(win);
  if (!h) return;
  h.postMessage("wails:drag");
  const done = () => {
    for (const type of ["pointerup", "mouseup"]) win.removeEventListener(type, done, true);
    dropped();
  };
  for (const type of ["pointerup", "mouseup"]) win.addEventListener(type, done, true);
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

// installFloat exposes the bridge the host calls and wires the widget's ends.
export function installFloat(win: J = window): FloatBridge {
  const f = floatState();
  const bridge: FloatBridge = { setMode: (m, place) => f.setMode(m, place) };
  win[FLOAT_BRIDGE] = bridge;
  floatDeps.resolve = floatResolve;
  floatDeps.run = floatRun;
  floatDeps.post = (m) => floatPost(m, win);
  return bridge;
}

// floatResolve resolves item's gate with intent through the gates endpoint: a draft's at the rev it shows, a
// question's with option when it offers options.
export async function floatResolve(item: J, intent: string, option?: string): Promise<unknown> {
  const g = item.gate;
  if (!g) return null;
  if (queueDraft(g)) return resolveDraft(queueDraftOf(g), intent, (item.primary && item.primary.label) || intent, {});
  return resolveGate(item.task, g, intent, option);
}

// floatRun runs a primary that is no gate's: a unit's catalog action, opening the unit at its source, or the task
// in the cockpit.
export async function floatRun(item: J): Promise<unknown> {
  const p = item.primary || {};
  const u = queueUnit(item);
  if (p.kind === "action" && u) {
    if ((await queueDeps.act(u, p.id)) === false) throw new Error(t("{action} failed", { action: p.label || p.id }));
    return true;
  }
  if (p.kind === "open") {
    const link = sourceLink(item.task, u);
    if (link) return queueDeps.open(link);
  }
  openRoute(taskRoute(item.task && item.task.id));
  return true;
}

function sourceLink(task: J, u: J): string {
  const cards: J[] = (task && task.cards) || [];
  const card = cards.find((c) => c.card && u && c.card.unit && c.card.unit.provider === u.provider && c.card.unit.key === u.key) || cards[0];
  return (card && card.card && safeURL(card.card.link)) || "";
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

// draftSummary reads a draft's payload as its card shows it, by shape alone: the first field a person picks
// among values is its verdict, the first list of records its comments, each with its Markdown value (else its
// last) as the body and its first two other values as where it sits.
export function draftSummary(d: J): DraftSummary {
  const fields: J[] = (d && d.fields) || [];
  const verdict = fields.find((f) => (f.options || []).length && !(f.fields || []).length);
  const list = fields.find((f) => (f.fields || []).length && f.fields.every((x: J) => (x.fields || []).length));
  const comments: DraftComment[] = ((list && list.fields) || []).map((c: J) => {
    const leaves: J[] = (c.fields || []).filter((x: J) => !(x.fields || []).length);
    const body = leaves.find((x) => x.markdown) || leaves[leaves.length - 1];
    const text = body ? String(body.value || "") : "";
    const [file, line] = leaves.filter((x) => x !== body).map((x) => x.shown || x.value);
    const at = line ? t("{file}:{line}", { file, line }) : file || "";
    return { at, body: text, ...(conventionalLabel(text) || {}) };
  });
  return { verdict: verdict ? verdict.shown || verdict.value : "", tone: (verdict && verdict.tone) || "", comments, tally: tally(comments) };
}
