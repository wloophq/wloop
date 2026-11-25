import type { J } from "../lib/util";
import { APIError, getJSON } from "./api";
import { state } from "./store.svelte";
import { CONN_CLOSED, DEBOUNCE_MS, timer, type Follow } from "./sync.svelte";

// The floating widget's state: the queue of every decision kind in the daemon's order, the live turns, which item
// is current, and whether the widget shows as a pill, a peek or the open panel. Nothing is drawn here.

export const FLOAT_QUEUE = "/api/queue";
export const FLOAT_TURNS = "/api/turns/live";
// FLOAT_PEEK_MS is how long a peek shows; FLOAT_FOCUS_MS how long the peeked item stays the one the panel opens on.
export const FLOAT_PEEK_MS = 4500;
export const FLOAT_FOCUS_MS = 12000;
// FLOAT_QUIET_MS is how long arrivals must stop before the next one peeks again, so a burst peeks once.
export const FLOAT_QUIET_MS = 10000;
// FLOAT_RETRY_MS is the first wait before a failed read is retried, doubling up to FLOAT_RETRY_MAX_MS.
export const FLOAT_RETRY_MS = 2000;
export const FLOAT_RETRY_MAX_MS = 60000;
export const FLOAT_HOVER_OPEN_MS = 120;
export const FLOAT_HOVER_CLOSE_MS = 400;

export type FloatMode = "pill" | "panel" | "peek";
export const FLOAT_MODES: readonly FloatMode[] = ["pill", "panel", "peek"];

// FloatHide is how long the host hides the widget: until something new arrives, for an hour, or until tomorrow.
export type FloatHide = "quiet" | "hour" | "tomorrow";

// FloatPlace is where the host keeps the pill: docked on a side edge, which stands it upright and opens the panel
// away from that edge, and whether the panel opens upward, as it does from the screen's bottom half.
export interface FloatPlace {
  edge: "" | "left" | "right";
  up: boolean;
}

// floatDeps are what the widget reaches beyond its state: resolving an item's gate, running any other primary,
// and posting a message to the window that hosts it. The page wires them; each is inert until then.
export const floatDeps = {
  resolve: async (_item: J, _intent: string, _option?: string): Promise<unknown> => null,
  run: async (_item: J): Promise<unknown> => null,
  post: (_message: J): void => {},
};

// FloatRead is how the last read went: answered, the daemon unreachable, or the queue refused by a daemon that answers.
export type FloatRead = "ok" | "offline" | "failed";

const fresh = () => ({
  items: [] as J[],
  current: 0,
  title: "",
  empty: "",
  turns: null as J,
  mode: "pill" as FloatMode,
  peeked: null as J,
  pinned: false,
  place: { edge: "", up: false } as FloatPlace,
  read: "ok" as FloatRead,
  turnsFailed: false,
  loaded: false,
  busy: false,
  picked: {} as Record<string, string>,
});

const fl = $state(fresh());

type Timer = ReturnType<typeof setTimeout> | undefined;
interface Reads {
  queue: boolean;
  turns: boolean;
}

const marks = {
  seen: new Set<string>(),
  focus: "",
  burst: false,
  retries: 0,
  timers: { peek: undefined as Timer, focus: undefined as Timer, quiet: undefined as Timer, sync: undefined as Timer, retry: undefined as Timer },
  queued: null as Reads | null,
  chain: Promise.resolve(),
};

// itemKey names an item across reads: its gate, else its task's decision of its kind.
export function itemKey(it: J): string {
  if (it && it.gate) return "gate:" + it.gate.id;
  return ["task", it && it.task && it.task.id, it && it.kind, it && it.situation ? it.situation.kind : ""].join(":");
}

// successor is where the current item at, of prev, lands in next: itself, else the first item after it that next
// still holds, else the nearest one before it.
function successor(prev: J[], at: number, next: J[]): number {
  if (!next.length) return 0;
  const index = new Map(next.map((it, i) => [itemKey(it), i]));
  for (let i = at; i < prev.length; i++) {
    const n = index.get(itemKey(prev[i]));
    if (n !== undefined) return n;
  }
  for (let i = Math.min(at, prev.length) - 1; i >= 0; i--) {
    const n = index.get(itemKey(prev[i]));
    if (n !== undefined) return n;
  }
  return Math.min(at, next.length - 1);
}

function endPeek(): void {
  timer.clear(marks.timers.peek);
  fl.peeked = null;
  if (fl.mode === "peek") fl.mode = "pill";
}

function peek(item: J): void {
  if (!item) return;
  fl.peeked = item;
  marks.focus = itemKey(item);
  if (fl.mode === "pill") fl.mode = "peek";
  timer.clear(marks.timers.peek);
  marks.timers.peek = timer.set(endPeek, FLOAT_PEEK_MS);
  timer.clear(marks.timers.focus);
  marks.timers.focus = timer.set(() => {
    marks.focus = "";
  }, FLOAT_FOCUS_MS);
}

// arrived peeks at item unless a burst is on; every arrival keeps the burst on until FLOAT_QUIET_MS pass without one.
function arrived(item: J, collapsed: boolean): void {
  const quiet = !marks.burst;
  marks.burst = true;
  timer.clear(marks.timers.quiet);
  marks.timers.quiet = timer.set(() => {
    marks.burst = false;
  }, FLOAT_QUIET_MS);
  if (quiet && collapsed) peek(item);
}

function applyQueue(q: J): void {
  const items: J[] = (q && q.items) || [];
  const keys = items.map(itemKey);
  const arrivals = fl.loaded ? items.filter((_, i) => !marks.seen.has(keys[i])) : [];
  fl.current = successor(fl.items, fl.current, items);
  fl.items = items;
  fl.title = (q && q.title) || "";
  fl.empty = (q && q.empty) || "";
  fl.loaded = true;
  marks.seen = new Set(keys);
  if (marks.focus && !marks.seen.has(marks.focus)) marks.focus = "";
  if (fl.peeked && !marks.seen.has(itemKey(fl.peeked))) endPeek();
  if (arrivals.length) arrived(arrivals[0], fl.mode !== "panel");
}

// lose forgets what the daemon served, so no count outlives it; what was seen stays, so a recovery peeks only at
// what is new.
function lose(read: FloatRead): void {
  Object.assign(fl, { read, items: [], current: 0, turns: null, turnsFailed: false });
  endPeek();
}

function retry(): void {
  timer.clear(marks.timers.retry);
  const wait = Math.min(FLOAT_RETRY_MAX_MS, FLOAT_RETRY_MS * 2 ** marks.retries++);
  marks.timers.retry = timer.set(() => void floatLoad(), wait);
}

async function read(asked: Reads): Promise<void> {
  const what = fl.read === "ok" ? asked : { queue: true, turns: true };
  const [q, tr] = await Promise.allSettled([what.queue ? getJSON(FLOAT_QUEUE) : null, what.turns ? getJSON(FLOAT_TURNS) : null]);
  if (q.status === "rejected") {
    lose(q.reason instanceof APIError && q.reason.status > 0 ? "failed" : "offline");
    return retry();
  }
  fl.read = "ok";
  if (what.queue) applyQueue(q.value);
  if (tr.status === "rejected") {
    Object.assign(fl, { turns: null, turnsFailed: true });
    return retry();
  }
  if (what.turns) Object.assign(fl, { turns: tr.value, turnsFailed: false });
  timer.clear(marks.timers.retry);
  marks.retries = 0;
}

// floatLoad reads what reads names, after every read already started.
export function floatLoad(what: Reads = { queue: true, turns: true }): Promise<void> {
  marks.chain = marks.chain.then(() => read(what)).catch(() => lose("offline"));
  return marks.chain;
}

function schedule(what: Reads): void {
  const q = marks.queued || { queue: false, turns: false };
  marks.queued = { queue: q.queue || what.queue, turns: q.turns || what.turns };
  timer.clear(marks.timers.sync);
  marks.timers.sync = timer.set(() => {
    const next = marks.queued as Reads;
    marks.queued = null;
    void floatLoad(next);
  }, DEBOUNCE_MS);
}

// floatChange reads one event of the stream: the queue moves with a gate or a task, the turns only when they say so;
// after a failed read any event reads everything again.
export function floatChange(e: { data: string }): void {
  let c: J;
  try {
    c = JSON.parse(e.data);
  } catch {
    return schedule({ queue: true, turns: true });
  }
  if (fl.read !== "ok") return schedule({ queue: true, turns: true });
  const what = { queue: !!(c.all || c.gates || c.counters), turns: !!(c.all || c.turns) };
  if (what.queue || what.turns) schedule(what);
}

// floatFollow is how the widget follows the event stream: a stream that drops shows offline at once, and every
// open while offline reads everything, which alone brings the widget back.
export const floatFollow: Follow = {
  change: floatChange,
  opened: () => {
    if (fl.read !== "ok") schedule({ queue: true, turns: true });
  },
  reopened: () => schedule({ queue: true, turns: true }),
  reconnecting: () => lose("offline"),
  lost: () => lose("offline"),
};

function setPlace(p: Partial<FloatPlace> | undefined): void {
  if (!p || typeof p !== "object") return;
  fl.place = { edge: p.edge === "left" || p.edge === "right" ? p.edge : "", up: !!p.up };
}

// setMode shows the widget as m, at place when the host gives one.
function setMode(m: FloatMode, place?: Partial<FloatPlace>): void {
  setPlace(place);
  if (!FLOAT_MODES.includes(m)) return;
  if (m === "panel") {
    const at = marks.focus ? fl.items.findIndex((it) => itemKey(it) === marks.focus) : -1;
    if (at >= 0) fl.current = at;
    endPeek();
  } else {
    fl.pinned = false;
  }
  if (m === "peek") {
    if (!fl.items.length) return;
    fl.mode = "pill";
    return peek(fl.items[fl.current] || fl.items[0]);
  }
  if (m === "pill") endPeek();
  fl.mode = m;
}

function move(delta: number): void {
  const n = fl.items.length;
  if (!n) return;
  fl.current = (((fl.current + delta) % n) + n) % n;
}

// optionOf is the option item's card has selected: the person's pick, else the recommended one, else the first.
export function optionOf(item: J): string {
  const opts: J[] = (item && item.options) || [];
  const picked = fl.picked[itemKey(item)];
  if (picked && opts.some((o) => o.id === picked)) return picked;
  return (opts.find((o) => o.recommended) || opts[0] || { id: "" }).id;
}

function pick(item: J, option: string): void {
  fl.picked[itemKey(item)] = option;
}

// act runs the current item's primary: its gate's intent, with the selected option when it offers options, or any
// other primary; then the queue reads again, so the item leaves. It waits for the reads under way first and sends
// nothing when the item is no longer served.
async function act(intent?: string): Promise<void> {
  const at = fl.items[fl.current];
  if (!at || fl.busy) return;
  const key = itemKey(at);
  fl.busy = true;
  try {
    await marks.chain;
    const item = fl.items.find((it) => itemKey(it) === key);
    if (!item) return;
    const p = item.primary || {};
    if (item.gate && (p.kind === "gate" || intent)) await floatDeps.resolve(item, intent || p.id, (item.options || []).length ? optionOf(item) : undefined);
    else await floatDeps.run(item);
    if (!item.gate && p.kind !== "action") return;
  } finally {
    fl.busy = false;
  }
  await floatLoad({ queue: true, turns: false });
}

// openRoute asks the host to show route in the cockpit's main window.
export function openRoute(route: string): void {
  if (route) floatDeps.post({ type: "float:open", route });
}

// taskRoute is the cockpit's route of task id.
export const taskRoute = (id: string): string => (id ? "task=" + encodeURIComponent(id) : "");

function open(): void {
  const item = fl.items[fl.current];
  if (item && item.task) openRoute(taskRoute(item.task.id));
}

// hide asks the host to hide the widget for mode; the page collapses to the pill it comes back as.
function hide(mode: FloatHide): void {
  setMode("pill");
  floatDeps.post({ type: "float:hide", mode });
}

function setPinned(on: boolean): void {
  fl.pinned = on;
  if (on) setMode("panel");
}

// floatState is the widget's state and what moves it.
export function floatState() {
  return {
    get items(): J[] {
      return fl.items;
    },
    get current(): number {
      return fl.current;
    },
    get turns(): J {
      return fl.turns;
    },
    // turnsFailed says the daemon answers but refused the live turns, which the queue does not wait on.
    get turnsFailed(): boolean {
      return fl.turnsFailed;
    },
    get title(): string {
      return fl.title;
    },
    get empty(): string {
      return fl.empty;
    },
    get mode(): FloatMode {
      return fl.mode;
    },
    get peeked(): J {
      return fl.peeked;
    },
    get pinned(): boolean {
      return fl.pinned;
    },
    get place(): FloatPlace {
      return fl.place;
    },
    get busy(): boolean {
      return fl.busy;
    },
    get loaded(): boolean {
      return fl.loaded;
    },
    // read is "offline" while the daemon or its stream is down, "failed" while it answers but refuses a read.
    get read(): FloatRead {
      return fl.read === "ok" && (state.conn === "reconnecting" || state.conn === CONN_CLOSED) ? "offline" : fl.read;
    },
    get online(): boolean {
      return this.read === "ok";
    },
    peek,
    setMode,
    setPinned,
    move,
    select: (i: number) => {
      if (i >= 0 && i < fl.items.length) fl.current = i;
    },
    pick,
    act,
    open,
    hide,
  };
}

export type FloatState = ReturnType<typeof floatState>;

// resetFloat puts the widget back as the page starts; tests call it between cases.
export function resetFloat(): void {
  for (const id of Object.values(marks.timers)) timer.clear(id);
  Object.assign(marks, { seen: new Set<string>(), focus: "", burst: false, retries: 0, queued: null, chain: Promise.resolve() });
  marks.timers = { peek: undefined, focus: undefined, quiet: undefined, sync: undefined, retry: undefined };
  Object.assign(fl, fresh());
}
