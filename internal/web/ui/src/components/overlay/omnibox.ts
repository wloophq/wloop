// The omnibox: instant search while typing, a pasted address's unit, a known verb's operations to confirm, and
// the read-only agent's answer or proposal. The daemon resolves, answers and runs; this asks it and keeps what
// it says in the omnibox's state, which the component draws. Nothing runs before the person confirms it.

import { t, tn } from "../../lib/i18n.svelte";
import { routeHash, unitPath, type Unit } from "../../lib/routes";
import type { J } from "../../lib/util";
import { act, claimCopy, settleCopy, writeClipboard, type ActExtra, type Claim } from "../../state/actions.svelte";
import { API_VERSION, getJSON, net } from "../../state/api";
import { refreshSpecs } from "../../state/loaders.svelte";
import { askInput, type PaletteItem } from "../../state/overlays.svelte";
import { state } from "../../state/store.svelte";

// OMNI_DEBOUNCE_MS is the pause in typing after which the omnibox asks for search and resolve.
export const OMNI_DEBOUNCE_MS = 120;

export interface OmniOp {
  op: string;
  [field: string]: J;
}

export interface Omni {
  panel: boolean;
  items: PaletteItem[];
  text: string;
  seq: number;
  askSeq: number;
  abort: AbortController | null;
  timer: unknown;
  search: J;
  resolved: J;
  answer: J;
  askError: string;
  asking: boolean;
  follow: { question: string; answer: string }[];
  reply: string;
  ops: OmniOp[];
  checked: boolean[];
  ran: { ops: OmniOp[]; results: J[] } | null;
  runError: string;
  sel: string;
  answered: string;
  pending: Promise<void>;
  took: number;
  acting: boolean;
  acted: { resolved: J; error: string; text: string } | null;
}

// OmniEnv is what the omnibox reaches: the daemon, the timers, the page it opens links in, the unit actions it
// runs and the clipboard; the component's binds it to the page, a test's records what the omnibox asks.
export interface OmniEnv {
  get: (url: string, signal?: AbortSignal) => Promise<J>;
  post: (url: string, body: unknown, signal?: AbortSignal) => Promise<J>;
  later: (fn: () => void, ms: number) => unknown;
  clear: (id: unknown) => void;
  now: () => number;
  open: (href: string) => void;
  act: (u: Unit, id: string, copies: boolean) => Promise<unknown>;
  claim: () => Claim;
  settle: (name: string, out: J, claim: Claim) => Promise<boolean>;
  confirm: (text: string) => Promise<boolean> | boolean;
  close: () => void;
  changed?: () => void;
}

// omniboxNew is an empty omnibox; items are the selection's actions it lists while nothing is typed.
export function omniboxNew(panel = false, items: PaletteItem[] = []): Omni {
  return {
    panel: !!panel, items: items || [], text: "", seq: 0, askSeq: 0, abort: null, timer: null, search: null, resolved: null,
    answer: null, askError: "", asking: false, follow: [], reply: "", ops: [], checked: [], ran: null, runError: "", sel: "",
    answered: "", pending: Promise.resolve(), took: 0, acting: false, acted: null,
  };
}

// omniboxOps makes ops the operations to confirm, every one checked.
function omniboxOps(st: Omni, ops: OmniOp[]): void {
  st.ops = (ops || []).map((op) => ({ ...op }));
  st.checked = st.ops.map(() => true);
}

// omniboxType takes text typed: after a pause it asks for the search and the resolution together; a reply to
// text older than the newest is dropped, and the request the newer text replaces is aborted.
export function omniboxType(st: Omni, text: string, env: OmniEnv): Promise<void> {
  st.text = text;
  st.sel = "";
  st.answer = null;
  st.askError = "";
  st.ran = null;
  st.askSeq++;
  st.asking = false;
  env.clear(st.timer);
  if (st.abort) st.abort.abort();
  const seq = ++st.seq;
  if (!text.trim()) {
    st.search = null;
    st.resolved = null;
    st.answered = text;
    omniboxOps(st, []);
    st.pending = Promise.resolve();
    return st.pending;
  }
  st.pending = new Promise<void>((done) => {
    st.timer = env.later(async () => {
      const ctl = new AbortController();
      st.abort = ctl;
      const started = env.now();
      const [search, resolved] = await Promise.all([
        env.get("/api/search?q=" + encodeURIComponent(text), ctl.signal).catch(() => null),
        env.post("/api/omnibox/resolve", { text }, ctl.signal).catch(() => null),
      ]);
      if (seq === st.seq) {
        st.search = search;
        st.resolved = resolved;
        st.answered = text;
        st.took = env.now() - started;
        omniboxOps(st, (resolved && resolved.preview && resolved.preview.operations) || []);
      }
      done();
    }, OMNI_DEBOUNCE_MS);
  });
  return st.pending;
}

// omniboxAsk asks the read-only agent about the text, with the questions it asked and their answers; a reply
// to an older ask is dropped. An error shows where the answer would; search stays as it was.
export async function omniboxAsk(st: Omni, env: OmniEnv): Promise<void> {
  if (!st.text.trim() || st.asking) return;
  const seq = ++st.askSeq;
  st.asking = true;
  st.askError = "";
  try {
    const out = await env.post("/api/omnibox/ask", { text: st.text, follow: st.follow });
    if (seq !== st.askSeq) return;
    st.answer = out || {};
    if (st.answer.proposal) omniboxOps(st, st.answer.proposal.operations);
  } catch (err) {
    if (seq === st.askSeq) st.askError = (err as Error).message;
  } finally {
    if (seq === st.askSeq) st.asking = false;
  }
}

// omniboxFollow answers the question the agent asked and asks again with the whole exchange.
export function omniboxFollow(st: Omni, env: OmniEnv): Promise<void> {
  const q = st.answer && st.answer.proposal ? st.answer.proposal.question : "";
  if (!q || !st.reply.trim()) return Promise.resolve();
  st.follow = [...st.follow, { question: q, answer: st.reply.trim() }];
  st.reply = "";
  return omniboxAsk(st, env);
}

// omniboxRun runs the checked operations through the daemon, which replays each as the click it stands for.
export async function omniboxRun(st: Omni, env: OmniEnv, ops?: OmniOp[]): Promise<void> {
  const run = ops || st.ops.filter((_, i) => st.checked[i]);
  if (!run.length) return;
  st.runError = "";
  try {
    const out = await env.post("/api/omnibox/run", { operations: run });
    st.ran = { ops: run, results: (out && out.results) || [] };
    if (env.changed) env.changed();
  } catch (err) {
    st.runError = (err as Error).message;
  }
}

// omniboxUnit is the unit a pasted address resolved to, or null.
export function omniboxUnit(st: Omni): J {
  return st.resolved && st.resolved.kind === "unit" ? st.resolved.unit : null;
}

// omniboxUnitHref is the route of a resolved unit's detail.
export function omniboxUnitHref(unit: J): string {
  return "#" + routeHash("", unit.unit.provider + ":" + unit.unit.key);
}

// omniboxUnitAction runs one of a resolved unit's catalog actions as its detail would, then refreshes. The
// floating panel holds no detail: it runs the action itself, and hands one that asks for an input to the main
// window.
export async function omniboxUnitAction(st: Omni, env: OmniEnv, id: string): Promise<void> {
  const unit = omniboxUnit(st);
  const spec = (unit.actions || []).find((a: J) => a.id === id) || { id };
  if (st.panel) return spec.input ? env.open(omniboxUnitHref(unit)) : omniboxPanelAct(st, env, spec);
  await env.act(unit.unit, id, !!spec.copies);
  if (env.changed) env.changed();
}

// omniboxActed is what the panel's last action on the resolved unit left to show: why it failed, and the text
// to copy by hand when the clipboard refused its copy.
export function omniboxActed(st: Omni): { error: string; text: string } | null {
  const c = st.acted;
  return c && c.resolved === st.resolved ? { error: c.error, text: c.text } : null;
}

// omniboxPanelAct runs a unit's action from the floating panel: it asks the confirm of the row the unit serves,
// posts the action on the basis that row rests on and hides the panel. An action that copies claims the
// clipboard while the click lasts and puts its copy there. A failed action says why, and a refused write shows
// the text to copy by hand.
async function omniboxPanelAct(st: Omni, env: OmniEnv, spec: J): Promise<void> {
  if (st.acting) return;
  st.acting = true;
  st.acted = null;
  const claim = spec.copies ? env.claim() : null;
  const resolved = st.resolved;
  const u = resolved.unit.unit;
  const action = spec.label || spec.id;
  const show = (error: string, text: string) => {
    st.acted = { resolved, error, text };
  };
  try {
    let out: J;
    try {
      const d = await env.get(unitPath(u));
      const offered = ((d.row && d.row.actions) || []).find((a: J) => a.id === spec.id) || {};
      if (offered.confirm && !(await env.confirm(offered.confirm))) {
        if (claim) claim.pending.reject(new Error("cancelled"));
        return;
      }
      out = await env.post(unitPath(u, spec.id), offered.basis ? { source: "cockpit", basis: offered.basis } : { source: "cockpit" });
      if (out.error) throw new Error(out.error);
    } catch (err) {
      if (claim) claim.pending.reject(err);
      return show(t("{action} failed: {error}", { action, error: (err as Error).message }), "");
    }
    try {
      if (claim && !(await env.settle(spec.id, out, claim))) return show(t("{action} answered nothing to copy", { action }), "");
    } catch (err) {
      return show(t("Could not copy: {error}", { error: (err as Error).message }), out.copy.text);
    }
    env.close();
  } finally {
    st.acting = false;
  }
}

// omniboxAct runs action id on u as the unit's detail does: through act, on the row the detail serves, which
// asks for the action's confirm and input. An action that copies claims the clipboard before the first await.
export async function omniboxAct(u: Unit, id: string, copies: boolean = !!(state.specs.byId[id] || {}).copies): Promise<boolean | undefined> {
  const claim = copies ? claimCopy() : null;
  try {
    const d = await getJSON(unitPath(u));
    await refreshSpecs([d.row]);
    const input = (state.specs.byId[id] || {}).input;
    const extra: ActExtra = claim ? { claim } : {};
    if (input && (input.kind === "text" || input.kind === "answer")) {
      const v = await askInput({ label: input.label || id, placeholder: input.placeholder });
      if (v === null || v === undefined) return undefined;
      extra.guidance = String(v).trim();
    }
    return await act(d.row, id, extra);
  } finally {
    if (claim) claim.pending.reject(new Error(t("{action} answered nothing to copy", { action: id })));
  }
}

export { settleCopy };

export interface Choice {
  id: string;
  run: () => unknown;
}

// omniboxChoices are what Enter may run, in the order the arrows walk them: the operations to confirm, a
// pasted address's unit, the agent where only it would help, each search result, and the selection's actions
// while nothing is typed.
export function omniboxChoices(st: Omni, env: OmniEnv): Choice[] {
  if (!st.text.trim()) return st.items.map((it, i) => ({ id: "item:" + i, run: () => { env.close(); it.run(); } }));
  const out: Choice[] = [];
  if (st.ops.some((_, i) => st.checked[i])) out.push({ id: "ops", run: () => omniboxRun(st, env) });
  const unit = omniboxUnit(st);
  if (unit) {
    const first = (unit.tasks || [])[0];
    out.push({ id: "unit", run: () => env.open(first ? "#task=" + encodeURIComponent(first.id) : omniboxUnitHref(unit)) });
  }
  if (st.resolved && st.resolved.kind === "agent" && !st.answer) out.push({ id: "ask", run: () => omniboxAsk(st, env) });
  for (const g of (st.search && st.search.groups) || []) {
    for (const r of g.results || []) out.push({ id: "task:" + r.task.id, run: () => env.open("#task=" + encodeURIComponent(r.task.id)) });
  }
  return out;
}

// omniboxSelected is the id of the choice selected: the one picked while it is still offered, else the first.
export function omniboxSelected(st: Omni, choices: Choice[]): string {
  return (choices.find((c) => c.id === st.sel) || choices[0] || { id: "" }).id;
}

// omniboxMove moves the selection by delta choices, stopping at either end.
export function omniboxMove(st: Omni, delta: number, env: OmniEnv): void {
  const choices = omniboxChoices(st, env);
  if (!choices.length) return;
  const at = choices.findIndex((c) => c.id === omniboxSelected(st, choices));
  st.sel = choices[Math.min(Math.max(at + delta, 0), choices.length - 1)].id;
}

// omniboxEnter is Enter in the field: run the selected choice once the answer to the text typed is in; with
// Cmd it asks the agent.
export async function omniboxEnter(st: Omni, env: OmniEnv, ask: boolean): Promise<unknown> {
  if (ask) return omniboxAsk(st, env);
  const text = st.text;
  if (st.answered !== text) await st.pending;
  if (st.answered !== text || st.text !== text) return undefined;
  const choices = omniboxChoices(st, env);
  const pick = choices.find((c) => c.id === omniboxSelected(st, choices));
  return pick ? pick.run() : undefined;
}

export interface OmniKey {
  key: string;
  ctrlKey?: boolean;
  metaKey?: boolean;
  altKey?: boolean;
  isComposing?: boolean;
  preventDefault(): void;
}

// omniboxKey is a key in the omnibox's field: the arrows (and Ctrl+N, Ctrl+P) move the selection, Enter runs
// it, Cmd+Enter asks the agent, Escape closes; true when the omnibox took the key.
export function omniboxKey(st: Omni, env: OmniEnv, e: OmniKey, close: () => void): boolean {
  const ctrl = e.ctrlKey && !e.metaKey && !e.altKey;
  if (e.key === "Escape") close();
  else if (e.key === "Enter" && !e.isComposing) void omniboxEnter(st, env, !!(e.metaKey || e.ctrlKey));
  else if (e.key === "ArrowDown" || (ctrl && e.key === "n")) omniboxMove(st, 1, env);
  else if (e.key === "ArrowUp" || (ctrl && e.key === "p")) omniboxMove(st, -1, env);
  else return false;
  e.preventDefault();
  return true;
}

// OMNI_STOPS are the controls Tab moves through inside the omnibox.
export const OMNI_STOPS = ["input", "button", "a[href]", '[tabindex]:not([tabindex="-1"])'].join(",");

// omniboxTrap is where Tab from active goes so the focus stays among stops: the other end past either end,
// else null, leaving the move to the page.
export function omniboxTrap<T>(e: { key: string; shiftKey?: boolean }, stops: T[], active: unknown): T | null {
  if (e.key !== "Tab" || !stops.length) return null;
  const first = stops[0];
  const last = stops[stops.length - 1];
  if (e.shiftKey && active === first) return last;
  if (!e.shiftKey && active === last) return first;
  return null;
}

// omniboxOpLabel is an operation in words.
export function omniboxOpLabel(op: OmniOp): string {
  const unit = op.unit ? op.unit.key : "";
  switch (op.op) {
    case "action": return t("{action} on {unit}", { action: op.reason || op.action, unit });
    case "verb": return t("{verb} task {task}", { verb: op.verb, task: op.task });
    case "capture": return t("Capture: {title}", { title: op.title });
    case "start": return op.repo ? t("Start in {repo}: {brief}", { repo: op.repo, brief: op.brief }) : t("Start: {brief}", { brief: op.brief });
  }
  return op.op;
}

// omniboxEdits are the fields of a proposed operation a person may change before running it.
export function omniboxEdits(op: OmniOp): { name: string; label: string }[] {
  if (op.op === "start") return [{ name: "brief", label: t("Brief") }, { name: "repo", label: t("Repository") }];
  if (op.op === "capture") return [{ name: "title", label: t("Title") }];
  return [];
}

// omniboxOptionID is the element id of the choice at i.
export const omniboxOptionID = (i: number): string => "omni-opt-" + i;

// omniboxIsOption says whether choice id is an option of the results' listbox: a result or a selection's
// action; the operations to run, a pasted unit and the agent keep their own controls.
export function omniboxIsOption(id: string): boolean {
  return id.startsWith("task:") || id.startsWith("item:");
}

// omniboxActiveID is the element id of the selected choice, "" when there is none or it is no option.
export function omniboxActiveID(st: Omni, env: OmniEnv): string {
  const choices = omniboxChoices(st, env);
  const i = choices.findIndex((c) => c.id === omniboxSelected(st, choices));
  return i < 0 || !omniboxIsOption(choices[i].id) ? "" : omniboxOptionID(i);
}

// omniboxSelectionWords are the words of a selected choice that is no option, which the status says.
export function omniboxSelectionWords(st: Omni, id: string): string {
  if (id === "ops") return tn("Run {n}", st.checked.filter(Boolean).length);
  if (id === "unit") return (omniboxUnit(st) || {}).label || "";
  if (id === "ask") return t("Press ⌘↵ to ask the agent");
  return "";
}

// omniboxStatus is what the polite status says: the selected choice when it is no option of the list, else
// how many results the search found.
export function omniboxStatus(st: Omni, env: OmniEnv): string {
  const choices = omniboxChoices(st, env);
  const sel = omniboxSelected(st, choices);
  if (sel && !omniboxIsOption(sel)) return omniboxSelectionWords(st, sel);
  const n = ((st.search && st.search.groups) || []).reduce((sum: number, g: J) => sum + (g.results || []).length, 0);
  return st.search ? tn("{n} results", n) : "";
}

// omniFetch is a JSON request to the daemon that signal may abort; a refusal throws the daemon's error.
export async function omniFetch(url: string, body: unknown, signal?: AbortSignal): Promise<J> {
  const res = await net.fetch(url, {
    method: body === undefined ? "GET" : "POST", credentials: "same-origin", signal,
    headers: { "Content-Type": "application/json", Accept: "application/json", "X-Loop-API": API_VERSION },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const out = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(out.error || `${url}: HTTP ${res.status}`);
  return out;
}

export interface PanelEnv {
  reset: () => void;
  health: () => Promise<J>;
  strings: (stamp: string) => unknown;
  build: (health: J) => void;
}

// panelFollow keeps the floating panel current each time it shows again after it was left: an empty omnibox,
// then the daemon's strings and build as its health names them. The returned stop removes its listeners.
export function panelFollow(win: Pick<Window, "addEventListener" | "removeEventListener">, doc: Pick<Document, "addEventListener" | "removeEventListener" | "visibilityState">, env: PanelEnv): () => void {
  let away = false;
  const leave = () => {
    away = true;
  };
  const back = async () => {
    if (!away || doc.visibilityState === "hidden") return;
    away = false;
    env.reset();
    try {
      const health = await env.health();
      env.strings(health.strings);
      env.build(health);
    } catch {
      // The panel's next show asks again.
    }
  };
  const visibility = () => (doc.visibilityState === "hidden" ? leave() : back());
  win.addEventListener("blur", leave);
  win.addEventListener("focus", back);
  doc.addEventListener("visibilitychange", visibility);
  return () => {
    win.removeEventListener("blur", leave);
    win.removeEventListener("focus", back);
    doc.removeEventListener("visibilitychange", visibility);
  };
}
