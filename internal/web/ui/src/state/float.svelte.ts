import type { J } from "../lib/util";
import { getJSON } from "./api";
import { state } from "./store.svelte";
import { CONN_CLOSED, DEBOUNCE_MS, timer, type Follow } from "./sync.svelte";

// The floating widget's state: the queue of every decision kind in the daemon's order, the live turns, which item
// is current, and whether the widget shows as a pill, a peek or the open panel. Nothing is drawn here.

export const FLOAT_QUEUE = "/api/queue";
export const FLOAT_TURNS = "/api/turns/live";
// FLOAT_PEEK_MS is how long a peek shows; FLOAT_FOCUS_MS how long the peeked item stays the one the panel opens
// on, and how long after a peek no arrival peeks again, so a burst peeks once.
export const FLOAT_PEEK_MS = 4500;
export const FLOAT_FOCUS_MS = 12000;
export const FLOAT_HOVER_OPEN_MS = 120;
export const FLOAT_HOVER_CLOSE_MS = 400;

export type FloatMode = "pill" | "panel" | "peek";
export const FLOAT_MODES: readonly FloatMode[] = ["pill", "panel", "peek"];

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
  online: true,
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
  cooling: false,
  timers: { peek: undefined as Timer, focus: undefined as Timer, cool: undefined as Timer, sync: undefined as Timer },
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

function peekArrival(item: J): void {
  if (marks.cooling) return;
  marks.cooling = true;
  marks.timers.cool = timer.set(() => {
    marks.cooling = false;
  }, FLOAT_FOCUS_MS);
  peek(item);
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
  if (arrivals.length && fl.mode !== "panel") peekArrival(arrivals[0]);
}

// offline forgets what the daemon served, so no count outlives it; what was seen stays, so a recovery peeks only
// at what is new.
function offline(): void {
  Object.assign(fl, { online: false, items: [], current: 0, turns: null });
  endPeek();
}

async function read(what: Reads): Promise<void> {
  const [q, tr] = await Promise.allSettled([what.queue ? getJSON(FLOAT_QUEUE) : null, what.turns ? getJSON(FLOAT_TURNS) : null]);
  if (q.status === "rejected" || tr.status === "rejected") return offline();
  fl.online = true;
  if (what.queue) applyQueue(q.value);
  if (what.turns) fl.turns = tr.value;
}

// floatLoad reads what reads names, after every read already started.
export function floatLoad(what: Reads = { queue: true, turns: true }): Promise<void> {
  marks.chain = marks.chain.then(() => read(what)).catch(() => offline());
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

// floatChange reads one event of the stream: the queue moves with a gate or a task, the turns only when they say so.
export function floatChange(e: { data: string }): void {
  let c: J;
  try {
    c = JSON.parse(e.data);
  } catch {
    return schedule({ queue: true, turns: true });
  }
  const what = { queue: !!(c.all || c.gates || c.counters), turns: !!(c.all || c.turns) };
  if (what.queue || what.turns) schedule(what);
}

// floatFollow is how the widget follows the event stream: a lost stream shows offline, a reopened one reads all.
export const floatFollow: Follow = {
  change: floatChange,
  reopened: () => schedule({ queue: true, turns: true }),
  lost: offline,
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
// other primary; then the queue reads again, so the item leaves. An item no longer served is never acted on.
async function act(intent?: string): Promise<void> {
  const item = fl.items[fl.current];
  if (!item || fl.busy) return;
  const p = item.primary || {};
  const id = intent || p.id;
  fl.busy = true;
  try {
    if (item.gate && (p.kind === "gate" || intent)) await floatDeps.resolve(item, id, (item.options || []).length ? optionOf(item) : undefined);
    else await floatDeps.run(item);
  } finally {
    fl.busy = false;
  }
  if (item.gate || p.kind === "action") await floatLoad({ queue: true, turns: false });
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
    // online is false once a read failed or the stream is lost, until the next read answers.
    get online(): boolean {
      return fl.online && state.conn !== "reconnecting" && state.conn !== CONN_CLOSED;
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
  };
}

export type FloatState = ReturnType<typeof floatState>;

// resetFloat puts the widget back as the page starts; tests call it between cases.
export function resetFloat(): void {
  for (const id of Object.values(marks.timers)) timer.clear(id);
  Object.assign(marks, { seen: new Set<string>(), focus: "", cooling: false, queued: null, chain: Promise.resolve() });
  marks.timers = { peek: undefined, focus: undefined, cool: undefined, sync: undefined };
  Object.assign(fl, fresh());
}
