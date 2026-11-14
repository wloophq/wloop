import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import type { J } from "../lib/util";
import type { PaletteRequest } from "../state/overlays.svelte";
import { createKeys, keyContextOf, type Binding, type HelpGroupOf, type KeyEvent, type Keys, type KeysEnv } from "../components/keys/keys";

// The keys' test harness, run from internal/web/ui: the dispatcher over a recording environment, the bindings the daemon serves
// (testdata/keys.web.json, which internal/daemon keeps current) and the verbs the bundled profile serves.

export const BINDINGS: Binding[] = JSON.parse(readFileSync(resolve(process.cwd(), "../testdata/keys.web.json"), "utf8")).bindings;

// TASK_PICKS mirrors builtin.TaskPicks: the handlers that pick another task, by the tasks they pick from.
const TASK_PICKS: Record<string, string> = { merge: "open", relate: "open", split: "merged", unrelate: "related" };

// profileVerbs reads the [verbs.<name>] tables of the bundled tasks.toml: each verb's handler, label and input.
function profileVerbs(): Record<string, Record<string, string>> {
  const src = readFileSync(resolve(process.cwd(), "../../../profiles/default/tasks.toml"), "utf8");
  const out: Record<string, Record<string, string>> = {};
  let at: Record<string, string> | null = null;
  for (const line of src.split("\n")) {
    const table = /^\[([^\]]+)\]\s*$/.exec(line);
    if (table) {
      const m = /^verbs\.([a-z_-]+)$/.exec(table[1]);
      at = m ? (out[m[1]] = {}) : null;
      continue;
    }
    const kv = /^(handler|label|input)\s*=\s*"((?:[^"\\]|\\.)*)"/.exec(line);
    if (at && kv) at[kv[1]] = JSON.parse(`"${kv[2]}"`);
  }
  return out;
}

const verbs = profileVerbs();
export const VERBS: string[] = Object.keys(verbs).sort();
export const VERB_SPECS: J[] = VERBS.map((v) => {
  const handler = verbs[v].handler || "";
  const spec: J = { verb: v, handler, label: verbs[v].label || "" };
  if (verbs[v].input) spec.input = verbs[v].input;
  if (TASK_PICKS[handler]) spec.pick = TASK_PICKS[handler];
  return spec;
});

export interface Posted {
  url: string;
  body: J;
  api: string;
}

export interface Log {
  posts: Posted[];
  gets: string[];
  nav: string[];
  area: string[];
  opened: string[];
  moves: number[];
  unit: J[];
  plan: J[];
  asked: string[];
  left: number;
  filter: number;
  closed: number;
  detail: number;
  detached: number;
  copied: number;
  palette: PaletteRequest | null;
  help: HelpGroupOf[] | null;
  hints: string[];
  timers: (() => void)[];
}

export interface Options {
  selected?: J;
  rows?: J[];
  specs?: Record<string, J>;
  unitAction?: KeysEnv["unitAction"];
  step?: J;
  planAction?: KeysEnv["planAction"];
  view?: string;
  reviewStep?: KeysEnv["step"];
  question?: J;
  views?: J[];
  providers?: string[];
  areas?: string[];
  answers?: (string | null)[];
  overlay?: boolean;
  pluginKey?: KeysEnv["pluginKey"];
  act?: KeysEnv["act"];
  verbs?: string[];
  verbSpecs?: J[];
  addFilter?: KeysEnv["addFilter"];
  queue?: KeysEnv["queue"];
  onNav?: (v: string) => void;
  get?: (url: string) => J;
}

export interface PressOptions {
  code?: string;
  shift?: boolean;
  meta?: boolean;
  ctrl?: boolean;
  alt?: boolean;
  target?: J;
}

export interface Harness {
  keys: Keys;
  log: Log;
  press: (key: string, x?: PressOptions) => Promise<boolean>;
  tick: (ms: number) => void;
}

// harness is the dispatcher over a recording environment that o answers; selected is read on each call, so a
// test may move it.
export function harness(o: Options = {}): Harness {
  const log: Log = {
    posts: [], gets: [], nav: [], area: [], opened: [], moves: [], unit: [], plan: [], asked: [], left: 0, filter: 0, closed: 0, detail: 0,
    detached: 0, copied: 0, palette: null, help: null, hints: [], timers: [],
  };
  let now = 0;
  const env: KeysEnv = {
    now: () => now,
    fetch: async (url, init = {}) => {
      if (!init.method) {
        log.gets.push(url);
        return { ok: true, status: 200, json: async () => (o.get ? o.get(url) : {}) };
      }
      log.posts.push({ url, body: JSON.parse(String(init.body)), api: (init.headers as Record<string, string>)["X-Loop-API"] });
      return { ok: true, status: 200, json: async () => ({}) };
    },
    selected: () => o.selected || null,
    rowOf: (u) => (o.rows || []).find((r) => r.unit.provider === u.provider && r.unit.key === u.key) || null,
    spec: (id) => (o.specs || {})[id],
    unitAction: o.unitAction || (async (row, id, extra) => { log.unit.push({ unit: row.unit, id, extra }); }),
    planStep: () => o.step || null,
    planAction: o.planAction || (async (it, id) => { log.plan.push({ id: it.id, action: id }); }),
    viewContext: () => keyContextOf(o.view || ""),
    step: o.reviewStep,
    questionAt: (el) => ((el as J) && (el as J).inQuestion ? o.question || null : null),
    move: (d) => { log.moves.push(d); },
    open: () => { log.detail++; },
    close: () => { log.closed++; },
    navigate: (v) => { log.nav.push(v); if (o.onNav) o.onNav(v); },
    views: () => o.views || [],
    providers: () => o.providers || [],
    areas: async () => o.areas || [],
    setArea: (a) => { log.area.push(a); },
    filter: () => { log.filter++; },
    openURL: (u) => { log.opened.push(u); },
    leave: () => { log.left++; },
    ask: async (q) => { log.asked.push(q.label); return (o.answers || []).length ? (o.answers as (string | null)[]).shift() : null; },
    palette: (p) => { log.palette = p; },
    help: (g) => { log.help = g; },
    overlay: () => !!o.overlay,
    pluginKey: o.pluginKey,
    hint: (h) => { log.hints.push(h); },
    later: (fn) => { log.timers.push(fn); },
    act: o.act,
    verbs: () => o.verbs || VERBS,
    verbSpecs: () => o.verbSpecs || VERB_SPECS,
    detailAt: (el) => !!((el as J) && (el as J).inDetail),
    detach: () => { log.detached++; },
    copyLink: () => { log.copied++; },
    addFilter: o.addFilter,
    queue: o.queue,
  };
  const keys = createKeys(env);
  keys.setBindings(BINDINGS);
  const press = (key: string, x: PressOptions = {}) => {
    const e: KeyEvent = {
      key, code: x.code || "", shiftKey: !!x.shift, metaKey: !!x.meta, ctrlKey: !!x.ctrl, altKey: !!x.alt, isComposing: false,
      target: x.target || { tagName: "BODY" }, defaultPrevented: false, preventDefault() { this.defaultPrevented = true; },
    };
    return keys.handle(e);
  };
  return { keys, log, press, tick: (ms) => { now += ms; } };
}

// taskFixture is a task in the inbox on one pull request, offering the bundled verbs the keys run.
export function taskFixture(): J {
  const verbs = ["next", "waiting", "schedule", "someday", "reference", "focus", "project", "approve", "delegate", "done", "dismiss", "seen", "reply"];
  return {
    id: "e-1", kind: "action", state: "inbox", title: "Reply to the review",
    units: [{ provider: "github", key: "acme/api#8" }],
    cards: [{ card: { unit: { provider: "github", key: "acme/api#8" }, link: "https://github.com/acme/api/pull/8" } }],
    verbs: verbs.map((verb) => ({ verb, enabled: true })),
  };
}

export const viewsFixture: J[] = [
  { id: "explore", provider: "github", order: 9 },
  { id: "needs-me", provider: "github", order: 1 },
  { id: "plan", provider: "github", order: 5 },
  { id: "threads", provider: "slack", order: 0 },
];
