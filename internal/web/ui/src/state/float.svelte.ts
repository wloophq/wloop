import type { J } from "../lib/util";
import { APIError, getJSON } from "./api";
import { state } from "./store.svelte";
import { CONN_CLOSED, DEBOUNCE_MS, timer, type Follow } from "./sync.svelte";

// The floating widget's state, which each of its two pages keeps on its own: the queue of every decision kind in the
// daemon's order and the live turns, which both read; where the tab sits; and, in the panel's page, which item is
// current and whether it shows a peek, the full panel or nothing. Nothing is drawn here.

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
// FLOAT_HOVER_OPEN_MS is how long the pointer rests on the tab before it asks for the panel; the host closes it.
export const FLOAT_HOVER_OPEN_MS = 120;

// FloatMode is what the panel's page shows: nothing while its window is hidden, a peek, or the full panel.
export type FloatMode = "" | "peek" | "panel";
export const FLOAT_MODES: readonly FloatMode[] = ["", "peek", "panel"];

// FloatHide is how long the host hides the widget: until something new arrives, for an hour, or until tomorrow.
export type FloatHide = "quiet" | "hour" | "tomorrow";

// FloatPlace is where the host keeps the tab: docked on a side edge, which stands it upright, or free.
export interface FloatPlace {
  edge: "" | "left" | "right";
}

// PanelPlace is where the host puts the panel's window: on which side of the tab, and the room it may fill there,
// 0 until the host names one.
export interface PanelPlace {
  side: "" | "left" | "right" | "above" | "below";
  maxW: number;
  maxH: number;
}

// floatDeps are what the widget reaches beyond its state: resolving an item's gate, running any other primary,
// opening an item's unit at its source, posting a message to the window that hosts it, and asking for a peek at an
// item that arrived, which only the tab's page does. The page wires them; each is inert until then.
export const floatDeps = {
  resolve: async (_item: J, _intent: string, _option?: string): Promise<unknown> => null,
  run: async (_item: J): Promise<unknown> => null,
  openUnit: (_item: J): void => {},
  post: (_message: J): void => {},
  peek: (_item: J): void => {},
};

// FloatRead is how the last read went: answered, the daemon unreachable, or the queue refused by a daemon that answers.
export type FloatRead = "ok" | "offline" | "failed";

const fresh = () => ({
  items: [] as J[],
  current: 0,
  title: "",
  empty: "",
  turns: null as J,
  mode: "" as FloatMode,
  peekKey: "",
  peeking: false,
  pinned: false,
  place: { edge: "" } as FloatPlace,
  room: { side: "", maxW: 0, maxH: 0 } as PanelPlace,
  read: "ok" as FloatRead,
  turnsFailed: false,
  loaded: false,
  busy: false,
  failed: { key: "", text: "" },
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

// itemKey names an item across reads, as the menu bar does: its gate, else its task's decision of its kind and
// its situation's provider, kind and unit.
export function itemKey(it: J): string {
  if (it && it.gate) return "gate:" + it.gate.id;
  const s = (it && it.situation) || {};
  return ["task", it && it.task && it.task.id, it && it.kind, s.provider || "", s.kind || "", s.unit || ""].join(":");
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

// endPeek stops a peek showing; the page lets its card leave, then closes.
function endPeek(): void {
  timer.clear(marks.timers.peek);
  fl.peeking = false;
}

// peekAt shows the peek of the item keyed key for FLOAT_PEEK_MS, and makes it the one the panel opens on for
// FLOAT_FOCUS_MS.
function peekAt(key: string): void {
  if (!key || fl.mode === "panel") return;
  Object.assign(fl, { mode: "peek", peekKey: key, peeking: true });
  marks.focus = key;
  timer.clear(marks.timers.peek);
  marks.timers.peek = timer.set(endPeek, FLOAT_PEEK_MS);
  timer.clear(marks.timers.focus);
  marks.timers.focus = timer.set(() => {
    marks.focus = "";
  }, FLOAT_FOCUS_MS);
}

// arrived asks for a peek at item unless a burst is on; every arrival keeps the burst on until FLOAT_QUIET_MS pass
// without one.
function arrived(item: J): void {
  const quiet = !marks.burst;
  marks.burst = true;
  timer.clear(marks.timers.quiet);
  marks.timers.quiet = timer.set(() => {
    marks.burst = false;
  }, FLOAT_QUIET_MS);
  if (quiet) floatDeps.peek(item);
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
  if (fl.failed.key && !marks.seen.has(fl.failed.key)) fl.failed = { key: "", text: "" };
  if (arrivals.length) arrived(arrivals[0]);
}

// lose forgets what the daemon served, so no count outlives it; what was seen stays, so a recovery peeks only at
// what is new.
function lose(read: FloatRead): void {
  Object.assign(fl, { read, items: [], current: 0, turns: null, turnsFailed: false });
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
  marks.timers.sync = timer.set(() => void flush(), DEBOUNCE_MS);
}

// flush starts the read an event queued now, instead of after the debounce.
function flush(): Promise<void> {
  timer.clear(marks.timers.sync);
  const next = marks.queued;
  marks.queued = null;
  return next ? floatLoad(next) : marks.chain;
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

// setPlace keeps where the host says the tab sits.
function setPlace(p: Partial<FloatPlace> | undefined): void {
  if (!p || typeof p !== "object") return;
  fl.place = { edge: p.edge === "left" || p.edge === "right" ? p.edge : "" };
}

const SIDES: readonly PanelPlace["side"][] = ["left", "right", "above", "below"];

// show shows m in the panel's page, at the place the host gives: the full panel on item's key, else on the item
// last peeked at, a peek at item, or nothing. The same mode again with no item only takes the new place.
function show(m: FloatMode, p?: Partial<PanelPlace> & { item?: string }): void {
  if (p && typeof p === "object") {
    const size = (n: unknown) => (typeof n === "number" && n > 0 ? n : 0);
    fl.room = { side: SIDES.includes(p.side as PanelPlace["side"]) ? (p.side as PanelPlace["side"]) : "", maxW: size(p.maxW), maxH: size(p.maxH) };
  }
  const item = p && typeof p.item === "string" ? p.item : "";
  if (!FLOAT_MODES.includes(m) || (m === fl.mode && !item)) return;
  if (m === "panel") return openPanel(item);
  if (m === "peek") return peekAt(item);
  shut();
}

// openPanel shows the full panel on the item keyed key, else on the one last peeked at.
function openPanel(key = ""): void {
  const at = key || marks.focus;
  const i = at ? fl.items.findIndex((it) => itemKey(it) === at) : -1;
  if (i >= 0) fl.current = i;
  endPeek();
  Object.assign(fl, { mode: "panel", peekKey: "" });
}

// shut shows nothing, unpinned, as the host hid the window.
function shut(): void {
  endPeek();
  Object.assign(fl, { mode: "", peekKey: "", pinned: false });
}

// close shows nothing and asks the host to hide the panel's window.
function close(): void {
  shut();
  floatDeps.post({ type: "float:close" });
}

// leave closes a full panel that is not pinned, as the pointer has left both windows or the window lost the keyboard.
function leave(): void {
  if (fl.mode === "panel" && !fl.pinned) close();
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
// other primary; then the queue reads again, so the item leaves. It waits for the reads under way, and for one an
// event queued, first and sends nothing when the item is no longer served. A primary that fails says so in failed
// until the item leaves or the next act.
async function act(intent?: string): Promise<void> {
  const at = fl.items[fl.current];
  if (!at || fl.busy) return;
  const key = itemKey(at);
  fl.busy = true;
  fl.failed = { key: "", text: "" };
  try {
    await flush();
    const item = fl.items.find((it) => itemKey(it) === key);
    if (!item) return;
    const p = item.primary || {};
    if (item.gate && (p.kind === "gate" || intent)) await floatDeps.resolve(item, intent || p.id, (item.options || []).length ? optionOf(item) : undefined);
    else await floatDeps.run(item);
    if (!item.gate && p.kind !== "action" && p.kind !== "verb") return;
  } catch (err) {
    fl.failed = { key, text: (err as Error).message };
    return;
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

function openUnit(): void {
  const item = fl.items[fl.current];
  if (item) floatDeps.openUnit(item);
}

// hide asks the host to hide the widget for mode, closing the panel.
function hide(mode: FloatHide): void {
  if (fl.mode) close();
  floatDeps.post({ type: "float:hide", mode });
}

function setPinned(on: boolean): void {
  fl.pinned = on;
  if (on && fl.mode !== "panel") openPanel();
}

// askPanel asks the host for the full panel beside the tab, or, on a click, to close it when it shows.
function askPanel(toggle = false): void {
  floatDeps.post(toggle ? { type: "float:panel", toggle } : { type: "float:panel" });
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
    // peeked is the item the peek shows, null until the queue holds it.
    get peeked(): J {
      return fl.peekKey ? fl.items.find((it) => itemKey(it) === fl.peekKey) || null : null;
    },
    get peekKey(): string {
      return fl.peekKey;
    },
    // peeking says the peek still shows; once it ends the page lets its card leave, then closes.
    get peeking(): boolean {
      return fl.peeking;
    },
    get pinned(): boolean {
      return fl.pinned;
    },
    get place(): FloatPlace {
      return fl.place;
    },
    get room(): PanelPlace {
      return fl.room;
    },
    get busy(): boolean {
      return fl.busy;
    },
    // failed is how the last primary run from the widget failed, "" when it did not.
    get failed(): string {
      return fl.failed.text;
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
    setPlace,
    show,
    openPanel,
    close,
    leave,
    blur: leave,
    askPanel,
    setPinned,
    move,
    select: (i: number) => {
      if (i >= 0 && i < fl.items.length) fl.current = i;
    },
    pick,
    act,
    open,
    openUnit,
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
