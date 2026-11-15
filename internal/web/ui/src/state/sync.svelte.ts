import { applyCatalog } from "../lib/catalog";
import { followStrings, t } from "../lib/i18n.svelte";
import { fieldOf } from "../lib/lens";
import { unitKey, type J } from "../lib/util";
import { API_VERSION, getJSON, net } from "./api";
import { forgetAnswers, scheduler, type Change } from "./actions.svelte";
import { checkBuild } from "./build.svelte";
import {
  forgetTasksPage, loadActivity, loadCatalog, loadDetail, loadExploreRecent, loadHome, loadPlan, loadPlugins, loadQueue,
  loadRoundGates, loadTasks, mergeRows, put, refreshSpecs,
} from "./loaders.svelte";
import { readHash } from "./route.svelte";
import { catalog, state } from "./store.svelte";

// The page's sync: the daemon's event stream names what changed, a debounce gathers a burst of changes into one
// read of what they name, and every read writes into the store. Nothing is drawn here; the views follow the store.

export const DEBOUNCE_MS = 300;
export const RECONNECT_MAX_MS = 30000;

// timer is how the sync waits; tests run it under fake timers.
export const timer = {
  set: (fn: () => void, ms: number): ReturnType<typeof setTimeout> => setTimeout(fn, ms),
  clear: (id: ReturnType<typeof setTimeout> | undefined) => clearTimeout(id),
};

// afterSync are what runs once a read has applied, such as letting go of a decision the daemon has answered.
export const afterSync = new Set<() => void>();

function afterLoad(): void {
  forgetAnswers([...state.rows, ...(state.detail ? [state.detail.row] : [])]);
  for (const fn of afterSync) fn();
}

function applyHealth(h: J): void {
  if (state.health && h && typeof h === "object") put(state, "health", h);
  else state.health = h;
  checkBuild(state.health);
  followStrings(state.health && state.health.strings, getJSON);
}

// load reads everything the page shows.
export async function load(): Promise<void> {
  const plugins = loadPlugins();
  const [cat, rows, health] = await Promise.allSettled([getJSON("/api/catalog"), getJSON("/api/rows"), getJSON("/api/health")]);
  await plugins;
  if (cat.status === "fulfilled") applyCatalog(cat.value, fieldOf);
  if (rows.status === "fulfilled") {
    const page = rows.value;
    if (!(page.epoch && page.epoch === state.epoch && page.rev < state.rev)) {
      put(state, "rows", page.rows || []);
      state.epoch = page.epoch || "";
      state.rev = page.rev || 0;
    }
    state.loaded = true;
  }
  if (health.status === "fulfilled") applyHealth(health.value);
  const failed = [cat, rows, health].filter((r): r is PromiseRejectedResult => r.status === "rejected");
  // A daemon that cannot open its store says why in its health's warnings.
  const storeClosed = health.status === "fulfilled" && health.value.store && health.value.store.state !== "ok";
  if (failed.length && !storeClosed) {
    state.error = t("Could not reach the daemon: {error}", { error: [...new Set(failed.map((r) => (r.reason as Error).message))].join("; ") });
  } else {
    state.error = "";
    state.authLost = false;
  }
  forgetTasksPage();
  await Promise.all([loadActivity(), loadDetail(), loadExploreRecent(), loadPlan(), loadRoundGates(), loadTasks(), loadHome(), loadQueue()]);
  await refreshSpecs([...state.rows, ...(state.detail ? [state.detail.row] : [])]);
  afterLoad();
}

// applyChange brings the page up to a change: the rows that moved, and only what the change names of the rest:
// the health, the plan (also when a row it shows moved), the round gates, the detail when its unit moved.
export async function applyChange(c: Change): Promise<void> {
  if (c.all || !state.loaded) return load();
  try {
    if (c.rows || c.units.length) {
      const page = await getJSON(`/api/rows?since=${encodeURIComponent(`${state.epoch}:${state.rev}`)}`);
      if (!mergeRows(page)) return load();
      if (page.catalog && page.catalog !== catalog.version) await loadCatalog();
    }
    if (c.health) applyHealth(await getJSON("/api/health"));
  } catch {
    return load();
  }
  state.error = "";
  state.authLost = false;
  const route = readHash();
  const { view, pr } = route;
  const planned = new Set((state.plan.items || []).map((it: J) => unitKey(it.unit)));
  const tasksMoved = c.tasks || c.gates;
  await Promise.all([
    c.units.length && view === "recent" ? loadActivity() : null,
    (pr && c.units.some((u) => u.toLowerCase() === pr.toLowerCase())) || (tasksMoved && (route.task || pr)) ? loadDetail() : null,
    c.plan || c.units.some((u) => planned.has(u)) ? loadPlan() : null,
    c.gates ? loadRoundGates() : null,
    tasksMoved ? loadTasks(route, c.units) : null,
    c.tasks || c.all || c.units.length ? loadHome() : null,
    c.tasks || c.all || c.units.length ? loadQueue() : null,
  ]);
  await refreshSpecs([...state.rows, ...(state.detail ? [state.detail.row] : [])]);
  afterLoad();
}

const sync = { pending: undefined as ReturnType<typeof setTimeout> | undefined, queued: null as Change | null, chain: Promise.resolve() };

// scheduleLoad gathers c into the change the next read applies, DEBOUNCE_MS after the last one.
export function scheduleLoad(c: Partial<Change> = { all: true }): void {
  const q = sync.queued || { units: [], all: false, rows: false, health: false, tasks: false, plan: false, gates: false };
  q.units = [...new Set([...q.units, ...(c.units || [])])];
  for (const k of ["all", "rows", "health", "tasks", "plan", "gates"] as const) q[k] = q[k] || !!c[k];
  sync.queued = q;
  timer.clear(sync.pending);
  sync.pending = timer.set(() => {
    const next = sync.queued as Change;
    sync.queued = null;
    sync.chain = sync.chain.then(() => applyChange(next)).catch(() => {});
  }, DEBOUNCE_MS);
}

// settled resolves once every read already started has applied; tests wait on it.
export function settled(): Promise<void> {
  return sync.chain;
}

scheduler.load = scheduleLoad;

// onChange reads one event of the stream: the units it names and the parts of the page it moved.
export function onChange(e: { data: string }): void {
  let c: J;
  try {
    c = JSON.parse(e.data);
  } catch {
    return scheduleLoad();
  }
  const change: Change = { units: (c.units || []).map(unitKey), all: !!c.all, rows: false, health: !!c.health, tasks: !!c.counters, plan: !!c.plan, gates: !!c.gates };
  if (!change.units.length && !Object.values(change).some((v) => v === true)) return;
  scheduleLoad(change);
}

export interface EventSourceLike {
  onmessage: ((e: MessageEvent) => void) | null;
  onopen: (() => void) | null;
  onerror: (() => void) | null;
  readyState: number;
  close(): void;
}

// stream is how the page opens the daemon's event stream; tests replace it.
export const stream = {
  open: (url: string): EventSourceLike => new EventSource(url) as unknown as EventSourceLike,
  CLOSED: 2,
};

const conn = { retries: 0, opened: false };

// connect follows the daemon's event stream, reconnecting with a backoff up to RECONNECT_MAX_MS, and loads what a
// reconnect may have missed.
export function connect(): void {
  const es = stream.open(`/api/events?api=${API_VERSION}`);
  es.onmessage = onChange;
  es.onopen = () => {
    conn.retries = 0;
    state.conn = "open";
    state.authLost = false;
    if (conn.opened) {
      forgetTasksPage();
      scheduleLoad({ rows: true, health: true, tasks: true, plan: true, gates: true });
    }
    conn.opened = true;
  };
  es.onerror = () => {
    if (es.readyState !== stream.CLOSED) {
      state.conn = "reconnecting";
      return;
    }
    es.close();
    state.conn = "closed";
    load();
    timer.set(connect, Math.min(RECONNECT_MAX_MS, 1000 * 2 ** conn.retries++));
  };
}

// resetSync forgets the pending change, the reads on their way and the stream's history, as a page that just
// started; tests call it between cases.
export function resetSync(): void {
  timer.clear(sync.pending);
  Object.assign(sync, { pending: undefined, queued: null, chain: Promise.resolve() });
  Object.assign(conn, { retries: 0, opened: false });
}

net.authLost = () => {
  state.authLost = true;
};
