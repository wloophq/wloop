import { t } from "../../lib/i18n.svelte";
import { PLAN, QUEUE } from "../../lib/routes";
import { safeURL, type J } from "../../lib/util";
import { API_VERSION } from "../../state/api";
import { servedAsk, type Ask } from "../../state/verbs";
import type { PaletteItem, PaletteRequest } from "../../state/overlays.svelte";
import { matchPath, PICK_ARG, taskPicker } from "../../lib/palette";

export const CHORD_MS = 1000;
export const KEY_CONTEXTS = ["detail", "question", "queue", "plan", "review", "list", "global"];
const CORE_ACTIONS = /^(move:|detail:|nav:|area:|answer:|step:|link:|tab:|palette$|help$|filter$|filter:add$|open-source$|capture$|quit$)/;
const NAMED_KEYS: Record<string, string> = { Enter: "Enter", Escape: "Esc", Esc: "Esc", Backspace: "Backspace", Tab: "Tab" };

export interface Binding {
  key: string;
  action: string;
  context: string;
  owner?: string;
  help?: string;
  summary?: string;
  served?: boolean;
}

export interface KeyEvent {
  key: string;
  code?: string;
  shiftKey?: boolean;
  metaKey?: boolean;
  ctrlKey?: boolean;
  altKey?: boolean;
  isComposing?: boolean;
  target: unknown;
  defaultPrevented: boolean;
  preventDefault(): void;
}

// eventKey is the canonical name of a key event, as the served bindings spell it; "" when no binding can
// match. Shift+digit reads the physical key, so layouts such as ABNT2, where Shift+6 types "¨", still pick
// areas.
export function eventKey(e: KeyEvent): string {
  if (e.isComposing) return "";
  const altGr = e.ctrlKey && e.altKey;
  const letter = typeof e.key === "string" && /^[a-z]$/i.test(e.key);
  if (e.metaKey) return letter ? "Cmd+" + e.key.toUpperCase() : "";
  if (e.ctrlKey && !altGr) return letter ? "Ctrl+" + e.key.toUpperCase() : "";
  if (e.altKey && !altGr) return "";
  if (NAMED_KEYS[e.key]) return NAMED_KEYS[e.key];
  const digit = /^Digit([1-9])$/.exec(e.code || "");
  if (digit) return e.shiftKey ? "Shift+" + digit[1] : digit[1];
  if (letter) return e.shiftKey ? e.key.toUpperCase() : e.key.toLowerCase();
  return typeof e.key === "string" && [...e.key].length === 1 && e.key !== " " ? e.key : "";
}

interface Target {
  tagName?: unknown;
  type?: string;
  isContentEditable?: boolean;
  getAttribute?: (name: string) => string | null;
}

export function typingTarget(el: unknown): boolean {
  const n = el as Target | null;
  if (!n || typeof n.tagName !== "string") return false;
  const tag = n.tagName.toUpperCase();
  if (tag === "INPUT") return !["checkbox", "radio", "button", "submit", "reset"].includes(String(n.type || "text").toLowerCase());
  return tag === "TEXTAREA" || tag === "SELECT" || !!n.isContentEditable;
}

// pressable elements take Enter themselves.
export function pressable(el: unknown): boolean {
  const n = el as Target | null;
  if (!n || typeof n.tagName !== "string") return false;
  const tag = n.tagName.toUpperCase();
  const role = n.getAttribute ? n.getAttribute("role") : null;
  return ["BUTTON", "A", "SUMMARY"].includes(tag) || role === "button" || role === "link";
}

export const isTask = (sel: J): boolean => !!sel && typeof sel.id === "string" && Array.isArray(sel.units);
const unitsOf = (sel: J): J[] => (!sel ? [] : isTask(sel) ? sel.units : sel.unit ? [sel.unit] : []);
export const sameUnit = (a: J, b: J): boolean => !!a && !!b && a.provider === b.provider && a.key === b.key;

// sourceLink is the link of the selection's first unit, only when it is https.
export function sourceLink(sel: J): string {
  if (!sel) return "";
  if (!isTask(sel)) return safeURL(sel.link) || "";
  const u = sel.units[0];
  const c = (sel.cards || []).find((x: J) => x.card && sameUnit(x.card.unit, u));
  return (c && safeURL(c.card.link)) || "";
}

// questionOptions are a question's answers in the order the card shows them: yes and no for a confirmation,
// the recommended option first.
export function questionOptions(q: J): Record<string, string | boolean>[] {
  if (!q) return [];
  if (q.kind === "confirm") return [{ yes: true }, { no: true }];
  const opts: J[] = q.options || [];
  return [...opts.filter((o) => o.id === q.recommend), ...opts.filter((o) => o.id !== q.recommend)].map((o) => ({ option_id: o.id }));
}

// keyContextOf is the key context a view adds before the list's.
export const keyContextOf = (view: string): string => (view === PLAN || view === "review" || view === QUEUE ? view : "");

const CONTEXT_HEADS: Record<string, () => string> = {
  global: () => t("Everywhere"),
  list: () => t("On the selection"),
  question: () => t("In a question"),
  plan: () => t("In the plan"),
  review: () => t("In the review"),
  detail: () => t("In the detail"),
  queue: () => t("In a queue"),
};

const HELP_ORDER = ["global", "list", "detail", "question", "queue", "plan", "review"];

export interface HelpGroupOf {
  context: string;
  head: string;
  bindings: Binding[];
}

// helpGroups are every binding by context.
export function helpGroups(bindings: Binding[]): HelpGroupOf[] {
  const order = (c: string) => (HELP_ORDER.includes(c) ? HELP_ORDER.indexOf(c) : HELP_ORDER.length);
  const contexts = [...new Set(bindings.map((b) => b.context))].sort((a, b) => order(a) - order(b) || a.localeCompare(b));
  return contexts.map((c) => ({ context: c, head: CONTEXT_HEADS[c] ? CONTEXT_HEADS[c]() : c, bindings: bindings.filter((b) => b.context === c) }));
}

export interface Question {
  task?: J;
  row?: J;
  gate?: J;
  question?: J;
}

// KeysEnv is what the dispatcher reads of the page and runs through; the page's binds it to the cockpit, a test's
// records what the keys ask.
export interface KeysEnv {
  fetch?: (url: string, init?: RequestInit) => Promise<Response | { ok: boolean; status: number; json: () => Promise<J> }>;
  now?: () => number;
  later?: (fn: () => void, ms: number) => unknown;
  selected: () => J;
  act?: (verb: string, args: Record<string, string | boolean>, task: J) => Promise<unknown>;
  verbs?: () => string[];
  verbSpecs?: () => J[];
  resolveGate?: (task: J, gate: J, intent: string) => Promise<unknown>;
  rowOf?: (u: J) => J;
  spec?: (id: string) => J;
  unitAction: (row: J, id: string, extra: J) => Promise<unknown>;
  planStep?: () => J;
  planAction?: (step: J, id: string) => Promise<unknown>;
  overlay?: () => boolean;
  pluginKey?: (e: KeyEvent) => boolean;
  viewContext?: () => string;
  queue?: (action: string) => boolean;
  step?: (action: string) => unknown;
  detailAt?: (el: unknown) => boolean;
  detach?: () => unknown;
  copyLink?: () => unknown;
  tab?: (dir: string) => unknown;
  questionAt?: (el: unknown) => Question | null;
  move: (delta: number) => unknown;
  open: () => unknown;
  close: () => unknown;
  navigate: (view: string) => void;
  views?: () => J[];
  providers?: () => string[];
  areas: () => Promise<string[]>;
  setArea: (a: string) => void;
  filter: () => unknown;
  addFilter?: () => unknown;
  openURL: (u: string) => void;
  leave?: (el: unknown) => void;
  ask: (o: { label: string; placeholder?: string }) => Promise<string | null | undefined>;
  palette: (p: PaletteRequest) => unknown;
  omnibox?: (items: PaletteItem[]) => unknown;
  stateLabel?: (name: string) => string;
  help: (groups: HelpGroupOf[]) => unknown;
  notice?: (text: string, err?: boolean) => void;
  hint?: (text: string) => void;
  changed?: () => void;
  extraItems?: () => PaletteItem[];
}

export interface Keys {
  handle: (e: KeyEvent) => Promise<boolean>;
  paletteItems: () => PaletteItem[];
  openPalette: () => void;
  setBindings: (list: Binding[] | null | undefined) => void;
  bindings: () => Binding[];
  keyOf: (action: string) => string;
}

// createKeys is the key dispatcher over env; table holds the bindings, reactive state where the page's hints
// read them.
export function createKeys(env: KeysEnv, table: { bindings: Binding[] } = { bindings: [] }): Keys {
  let chord: { prefix: string; at: number } | null = null;
  const now = env.now || (() => performance.now());
  const taskVerbs = () => new Set(env.verbs ? env.verbs() : []);
  const keyVerb = (name: string) => taskVerbs().has(name);
  // A served action's key is one uppercase letter, or its plan context's own key.
  const servedAction = (b: Binding) => !!b.served || /^[A-Z]$/.test(b.key) || (b.context === "plan" && !CORE_ACTIONS.test(b.action) && !keyVerb(b.action));
  const all = () => table.bindings;

  const isPrefix = (key: string) => all().some((b) => b.key.startsWith(key + " "));
  // reserved mirrors keys.Reserved: a core key, a chord prefix, a lowercase letter or a digit.
  const reserved = (key: string) => /^[a-z0-9]$/.test(key) || isPrefix(key) || all().some((b) => b.owner === "core" && !servedAction(b) && b.key === key);
  const bindingOf = (action: string, context?: string) => all().find((b) => b.action === action && (!context || b.context === context));

  const request = (url: string, init: RequestInit) => (env.fetch ? env.fetch(url, init) : fetch(url, init));

  async function postJSON(path: string, body: unknown): Promise<J> {
    const res = await request(path, {
      method: "POST",
      headers: { "Content-Type": "application/json", Accept: "application/json", "X-Loop-API": API_VERSION },
      body: JSON.stringify(body),
    });
    const out = await res.json().catch(() => ({}));
    if (!res.ok || out.error) throw new Error(out.error || `HTTP ${res.status}`);
    return out;
  }

  const say = (text: string, err?: boolean) => env.notice && env.notice(text, err);

  // match are the open tasks but except whose title holds q, as the daemon lists them.
  async function match(q: string, except: string): Promise<J[]> {
    try {
      const res = await request(matchPath(q, except), { headers: { Accept: "application/json", "X-Loop-API": API_VERSION } });
      const out = await res.json().catch(() => ({}));
      if (!res.ok || out.error) throw new Error(out.error || `HTTP ${res.status}`);
      return out.tasks || [];
    } catch (err) {
      say(t("Could not list the tasks: {error}", { error: (err as Error).message }), true);
      return [];
    }
  }

  async function taskVerb(task: J, verb: string, args: Record<string, string | boolean>): Promise<unknown> {
    if (env.act) return env.act(verb, args, task);
    const body: J = { source: "cockpit" };
    if (args && Object.keys(args).length) body.args = Object.fromEntries(Object.entries(args).map(([k, v]) => [k, String(v)]));
    try {
      const out = await postJSON(`/api/tasks/${encodeURIComponent(task.id)}/${encodeURIComponent(verb)}`, body);
      say(`${verb}: ${task.title || task.id}`);
      if (env.changed) env.changed();
      return out;
    } catch (err) {
      say(t("{verb} {task} failed: {error}", { verb, task: task.title || task.id, error: (err as Error).message }), true);
    }
  }

  async function verb(name: string): Promise<boolean> {
    const picked = env.selected();
    const task = isTask(picked) ? picked : null;
    if (!task) return false;
    const st = (task.verbs || []).find((v: J) => v.verb === name);
    if (!st || !st.enabled) {
      say(name + ": " + ((st && st.reason) || t("not offered here")), true);
      return true;
    }
    let args: Record<string, string> | null = {};
    const ask = servedAsk(name, env.verbSpecs ? env.verbSpecs() : []);
    if (ask && ask.pick) {
      await taskPicker({ task, ask: { label: ask.label, pick: ask.pick }, match, palette: env.palette, stateLabel: env.stateLabel, run: (id) => taskVerb(task, name, { [PICK_ARG]: id }) });
      return true;
    }
    if (ask && ask.args) {
      const v = await env.ask({ label: ask.label });
      if (v === null || v === undefined) return true;
      args = ask.args(String(v).trim());
      if (!args) return true;
    }
    await taskVerb(task, name, args);
    return true;
  }

  // unitRows are the rows whose served actions the selection's unit offers.
  function unitRows(sel: J): J[] {
    if (sel && !isTask(sel) && sel.unit) return [sel];
    return unitsOf(sel).map((u) => env.rowOf && env.rowOf(u)).filter(Boolean);
  }

  async function unitAction(id: string): Promise<boolean> {
    const sel = env.selected();
    for (const row of unitRows(sel)) {
      const st = (row.actions || []).find((a: J) => a.id === id && a.enabled);
      if (!st) continue;
      const spec = (env.spec && env.spec(id)) || {};
      const extra: J = {};
      const input = spec.input;
      if (input && (input.kind === "text" || input.kind === "answer")) {
        const v = await env.ask({ label: input.label || st.label || id, placeholder: input.placeholder });
        if (v === null || v === undefined) return true;
        extra.guidance = String(v).trim();
      }
      await env.unitAction(row, id, extra);
      return true;
    }
    return false;
  }

  async function answer(n: number, target: unknown): Promise<boolean> {
    const q = env.questionAt && env.questionAt(target);
    if (!q) return false;
    if (q.gate) {
      const intent = (q.gate.intents || [])[n - 1];
      if (intent && env.resolveGate) await env.resolveGate(q.task, q.gate, intent.id);
      return true;
    }
    const pick = questionOptions(q.question)[n - 1];
    if (!pick) return true;
    if (q.task) await taskVerb(q.task, "answer", pick);
    else if (q.row) await env.unitAction(q.row, "answer", pick);
    return true;
  }

  // planAction runs id on the focused plan step. With no step focused, Enter focuses the first one and every
  // other plan key does nothing.
  async function planAction(id: string, key: string): Promise<boolean> {
    const step = env.planStep && env.planStep();
    if (!step) {
      if (key === "Enter") env.move(1);
      return true;
    }
    if (!(step.actions || []).some((a: J) => a.id === id && a.enabled)) return false;
    if (env.planAction) await env.planAction(step, id);
    return true;
  }

  // navTarget is the view nav:<to> opens: the first view of the plugin to names, "" for a plugin with none
  // served, else the view whose id is to.
  function navTarget(to: string): string {
    const vs = (env.views ? env.views() : []).filter((v) => v.provider === to);
    vs.sort((a, b) => (a.order || 0) - (b.order || 0));
    if (vs.length) return vs[0].id;
    return env.providers && env.providers().includes(to) ? "" : to;
  }

  async function areaList(): Promise<string[] | null> {
    try {
      return (await env.areas()) || [];
    } catch (err) {
      say(t("Could not load the areas: {error}", { error: (err as Error).message }), true);
      return null;
    }
  }

  async function pickArea(n: number): Promise<boolean> {
    const areas = await areaList();
    if (!areas) return true;
    const a = areas[n - 1];
    if (!a) return false;
    env.setArea(a);
    return true;
  }

  async function chooseArea(): Promise<boolean> {
    const areas = await areaList();
    if (!areas) return true;
    const items: PaletteItem[] = [
      { label: t("No area"), key: "", run: () => env.setArea("") },
      ...areas.map((a, i) => ({ label: a, key: i < 9 ? `Shift+${i + 1}` : "", run: () => env.setArea(a) })),
    ];
    env.palette({ title: t("Area"), items });
    return true;
  }

  async function capture(): Promise<boolean> {
    const title = await env.ask({ label: t("Capture") });
    if (!title || !String(title).trim()) return true;
    try {
      await postJSON("/api/tasks", { title: String(title).trim() });
      say(t("captured: {title}", { title: String(title).trim() }));
      if (env.changed) env.changed();
    } catch (err) {
      say(t("capture failed: {error}", { error: (err as Error).message }), true);
    }
    return true;
  }

  // paletteItems are every action of the selection with its key, the global actions, then "Area...".
  function paletteItems(): PaletteItem[] {
    const sel = env.selected();
    const items: PaletteItem[] = [];
    const item = (b: Binding, label?: string): PaletteItem => ({ label: label || b.help || b.action, key: b.key || "", run: () => run(b, {}) });
    const listBinding = (action: string, served: boolean) => all().find((b) => b.action === action && b.context === "list" && servedAction(b) === served);
    if (isTask(sel)) {
      const specs = env.verbSpecs ? env.verbSpecs() : [];
      for (const v of (sel.verbs || []).filter((x: J) => x.enabled && keyVerb(x.verb) && (listBinding(x.verb, false) || servedAsk(x.verb, specs)))) {
        const b: Binding = listBinding(v.verb, false) || { key: "", action: v.verb, context: "list" };
        const spec = specs.find((x) => x.verb === v.verb);
        items.push(item(b, b.help || (spec && spec.label) || v.verb));
      }
    }
    for (const row of unitRows(sel)) {
      for (const st of (row.actions || []).filter((a: J) => a.enabled)) {
        const b: Binding = listBinding(st.id, true) || { key: "", action: st.id, context: "list", served: true };
        items.push(item(b, st.label || b.help || st.id));
      }
    }
    if (sourceLink(sel)) items.push(item(bindingOf("open-source", "list") || { key: "", action: "open-source", context: "list" }));
    for (const b of all().filter((x) => x.context === "global" && !/^(palette|area:)/.test(x.action))) items.push(item(b));
    items.push({ label: t("Area..."), key: "", run: () => chooseArea() });
    for (const extra of (env.extraItems && env.extraItems()) || []) items.push(extra);
    return items;
  }

  // openPalette opens the omnibox over the selection's actions, or the actions palette where none is wired.
  function openPalette(): void {
    if (env.omnibox) env.omnibox(paletteItems());
    else env.palette({ title: t("Actions"), items: paletteItems() });
  }

  async function run(b: Binding, ctx: { target?: unknown }): Promise<boolean> {
    const a = b.action;
    if (b.context === "queue") return env.queue ? env.queue(a) !== false : false;
    if (b.context === "plan") return servedAction(b) ? planAction(a, b.key) : a === "approve" ? planAction("plan_apply", b.key) : false;
    if (servedAction(b)) return b.context === "list" ? unitAction(a) : false;
    if (a === "move:down" || a === "move:up") return env.move(a === "move:down" ? 1 : -1) !== false;
    if (a === "detail:open") return env.open() !== false;
    if (a === "detail:close") return env.close() !== false;
    if (a.startsWith("nav:")) {
      const view = navTarget(a.slice(4));
      if (!view) return false;
      env.navigate(view);
      return true;
    }
    if (a.startsWith("area:")) return pickArea(Number(a.slice(5)));
    if (a.startsWith("answer:")) return answer(Number(a.slice(7)), ctx.target);
    if (a.startsWith("step:")) return env.step ? env.step(a.slice(5)) !== false : false;
    if (a === "detail:detach") return env.detach ? env.detach() !== false : false;
    if (a === "link:copy") return env.copyLink ? env.copyLink() !== false : false;
    if (a.startsWith("tab:")) return env.tab ? env.tab(a.slice(4)) !== false : false;
    if (a === "palette") {
      openPalette();
      return true;
    }
    if (a === "help") {
      env.help(helpGroups(all()));
      return true;
    }
    if (a === "filter") return env.filter() !== false;
    if (a === "filter:add") return env.addFilter ? env.addFilter() !== false : false;
    if (a === "capture") return capture();
    if (a === "open-source") {
      const link = sourceLink(env.selected());
      if (!link) return false;
      env.openURL(link);
      return true;
    }
    if (keyVerb(a)) return verb(a);
    return false;
  }

  // handle dispatches e; true when a binding took it.
  async function handle(e: KeyEvent): Promise<boolean> {
    if (e.defaultPrevented) return false;
    const key = eventKey(e);
    if (!key) return false;
    if (env.overlay && env.overlay() && key !== "Esc") return false;
    if (typingTarget(e.target)) {
      if (key !== "Esc") return false;
      e.preventDefault();
      if (env.leave) env.leave(e.target);
      return true;
    }
    if (chord && now() - chord.at <= CHORD_MS) {
      const full = `${chord.prefix} ${key}`;
      chord = null;
      if (env.hint) env.hint("");
      e.preventDefault();
      const b = all().find((x) => x.key === full);
      if (b) await run(b, { target: e.target });
      return true;
    }
    chord = null;
    if (env.pluginKey && !reserved(key) && e.key === key && env.pluginKey(e)) {
      e.preventDefault();
      return true;
    }
    if (key === "Enter" && pressable(e.target)) return false;
    const contexts = ["list", "global"];
    if (env.questionAt && env.questionAt(e.target)) contexts.unshift("question");
    const view = env.viewContext ? env.viewContext() : "";
    if (view) contexts.unshift(view);
    if (env.detailAt && env.detailAt(e.target)) contexts.unshift("detail");
    const candidates = all()
      .filter((b) => b.key === key && contexts.includes(b.context))
      .sort((x, y) => contexts.indexOf(x.context) - contexts.indexOf(y.context) || Number(servedAction(y)) - Number(servedAction(x)));
    if (!candidates.length && isPrefix(key)) {
      e.preventDefault();
      const c = { prefix: key, at: now() };
      chord = c;
      if (env.hint) {
        env.hint(`${key} …`);
        (env.later || setTimeout)(() => {
          if (chord !== c) return;
          chord = null;
          if (env.hint) env.hint("");
        }, CHORD_MS);
      }
      return true;
    }
    if (!candidates.length) return false;
    e.preventDefault();
    for (const b of candidates) {
      if (await run(b, { target: e.target })) return true;
    }
    return true;
  }

  return {
    handle,
    paletteItems,
    openPalette,
    setBindings(list) {
      table.bindings = (list || []).map((b) => ({ ...b }));
    },
    bindings: () => table.bindings,
    keyOf: (action) => (table.bindings.find((b) => b.action === action) || { key: "" }).key || "",
  };
}
