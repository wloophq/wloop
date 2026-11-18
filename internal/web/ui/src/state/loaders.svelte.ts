import { applyCatalog, applyPlugins, isCoreView, REVIEW, specsStale, viewLabel, viewSpec } from "../lib/catalog";
import { t } from "../lib/i18n.svelte";
import { fieldOf, viewQueryKey } from "../lib/lens";
import { keyUnit, PLAN, QUEUE_LIST, readRoute, RECENT, type Route, unitPath, EXPLORE, HOME, QUEUE } from "../lib/routes";
import { isSet, unitKey, type J } from "../lib/util";
import { APIError, getJSON, postJSON } from "./api";
import { reconcile } from "./reconcile";
import { currentHash, readHash, viewHash, type Hash } from "./route.svelte";
import {
  catalog, detailState, homeState, keptLists, outboxState, queueState, reviewState, state, taskState, tasksState, gateState,
} from "./store.svelte";

// put stores value as obj[key], in place of what obj[key] holds when both are objects or lists.
export function put<T extends object, K extends keyof T>(obj: T, key: K, value: T[K]): void {
  const next = reconcile(obj[key], value);
  if (obj[key] !== next) obj[key] = next;
}

// mergeList applies a page of a list since a position to list: the items removed go, the changed ones keep their
// place, the new ones come last.
function mergeList(list: J[], items: J[], gone: Set<string>, idOf: (x: J) => string): J[] {
  const fresh = new Map(items.map((x) => [idOf(x), x]));
  const kept = list.filter((x) => !gone.has(idOf(x))).map((x) => {
    const next = fresh.get(idOf(x));
    fresh.delete(idOf(x));
    return next || x;
  });
  return [...kept, ...fresh.values()];
}

const rowID = (r: J) => unitKey(r.unit);

// mergeRows applies a page of GET /api/rows to state.rows; false when the page cannot be merged and the caller
// must load everything.
export function mergeRows(page: J): boolean {
  if (page.full) {
    put(state, "rows", page.rows || []);
    state.epoch = page.epoch;
    state.rev = page.rev;
    return true;
  }
  if (!state.loaded || page.epoch !== state.epoch) return false;
  if (page.rev < state.rev) return true;
  const gone = new Set<string>((page.removed || []).map(unitKey));
  put(state, "rows", mergeList(state.rows, page.rows || [], gone, rowID));
  state.rev = page.rev;
  return true;
}

export async function loadSpecs(): Promise<void> {
  const r = await getJSON("/api/actions");
  state.specs = { version: r.version, byId: Object.fromEntries((r.specs || []).map((x: J) => [x.id, x])) };
  state.planSpecs = Object.fromEntries((r.plan || []).map((x: J) => [x.id, x]));
}

export async function refreshSpecs(rows: J[]): Promise<void> {
  if (!specsStale(rows)) return;
  try {
    await loadSpecs();
  } catch (err) {
    state.error = t("Could not load the actions: {error}", { error: (err as Error).message });
  }
}

export async function loadPlugins(): Promise<void> {
  try {
    applyPlugins(await getJSON("/api/plugins"));
  } catch {
    // The views each plugin draws stay as they were.
  }
}

export async function loadCatalog(): Promise<void> {
  applyCatalog(await getJSON("/api/catalog"), fieldOf);
}

// loadRoundGates keeps the open plan gates with a deadline by unit: the automatic rounds a row counts down to.
export async function loadRoundGates(): Promise<void> {
  try {
    const gs = await getJSON("/api/gates?status=open&kind=plan");
    state.roundGates = new Map((gs || []).filter((g: J) => g.kind === "plan" && g.unit && isSet(g.deadline)).map((g: J) => [g.unit, g]));
  } catch {
    // The rows still show their situation; only the countdown waits for the next load.
  }
}

export async function loadPlan(): Promise<void> {
  const view = readHash().view;
  try {
    const res = await getJSON(view === PLAN ? "/api/plan?closed=1" : "/api/plan");
    put(state.plan, "items", res.items || []);
    if (view === PLAN) put(state.plan, "closed", res.closed || []);
    state.plan.error = (res.errors || []).join("; ");
    state.plan.loaded = true;
  } catch (err) {
    state.plan.error = t("Could not load the plan: {error}", { error: (err as Error).message });
    state.plan.loaded = true;
  }
}

export async function loadActivity(): Promise<void> {
  if (readHash().view !== RECENT) {
    state.activityError = "";
    return;
  }
  try {
    put(state, "activity", await getJSON("/api/activity?limit=200"));
    state.activityLoaded = true;
    state.activityError = "";
  } catch (err) {
    state.activityError = t("Could not load the recent activity: {error}", { error: (err as Error).message });
    state.activityLoaded = true;
  }
}

// viewOfExplore is the route of the view name of the plugin that serves the Explore view.
export function viewOfExplore(name: string): string {
  const provider = (catalog.views.find((v) => v.id === EXPLORE) || {}).provider || "";
  return `/api/plugins/${encodeURIComponent(provider)}/views/${encodeURIComponent(name)}`;
}

export async function loadExploreRecent(): Promise<void> {
  if (readHash().view !== EXPLORE) return;
  try {
    state.explore.recent = await getJSON(viewOfExplore("explore.recent"));
  } catch {
    // Without the chips the search box still works.
  }
}

export async function loadHome(): Promise<void> {
  if (readHash().view !== HOME) return;
  try {
    put(homeState, "home", await getJSON("/api/home"));
    homeState.error = "";
  } catch (err) {
    homeState.error = t("Could not load the home: {error}", { error: (err as Error).message });
  }
}

// queueKey names a queue route's items: a kind and its lens query, or the tasks a list names.
export function queueKey(route: Pick<Route, "queue" | "ids" | "q">): string {
  return route.queue === QUEUE_LIST ? QUEUE_LIST + ":" + route.ids.join(",") : route.queue + "?" + (route.q || "");
}

// queueURL is the API path of a kind's queue narrowed by its lens query.
export function queueURL(route: Pick<Route, "queue" | "q">): string {
  return "/api/queues/" + encodeURIComponent(route.queue) + (route.q ? "?q=" + encodeURIComponent(route.q) : "");
}

// listQueue is the queue of the tasks ids name, in their order, each opened by its own detail.
export function listQueue(tasks: J[]): J {
  return {
    kind: QUEUE_LIST, title: t("Results"), empty: t("Nothing left in this list."), total: tasks.length,
    items: tasks.map((e) => ({ task: e, primary: { kind: "task", id: e.id, label: t("Open") } })),
  };
}

// queueLoad reads route's queue into queueState, keeping the item at while the same queue reloads; a list queue
// reads each task it names, in order, leaving out the ones gone.
export async function queueLoad(route: Route): Promise<void> {
  const key = queueKey(route);
  const same = queueState.key === key;
  try {
    const queue = route.queue === QUEUE_LIST
      ? listQueue((await Promise.all(route.ids.map((id) => getJSON("/api/tasks/" + encodeURIComponent(id)).catch(() => null)))).filter(Boolean))
      : await getJSON(queueURL(route));
    if (queueKey(readRoute(currentHash())) !== key) return;
    if (!same) Object.assign(queueState, { at: 0, error: "" });
    queueState.key = key;
    if (same) put(queueState, "queue", queue);
    else queueState.queue = queue;
    queueState.loadError = "";
    queueState.at = Math.min(queueState.at, Math.max((queue.items || []).length - 1, 0));
  } catch (err) {
    if (queueKey(readRoute(currentHash())) !== key) return;
    if (!same) Object.assign(queueState, { key, queue: null, at: 0, error: "" });
    queueState.loadError = t("Could not load the queue: {error}", { error: (err as Error).message });
  }
}

// queueShown is the loaded queue when it is route's, else null: a queue loaded for another route never shows nor
// runs.
export function queueShown(route: Pick<Route, "queue" | "ids" | "q">): J {
  return queueState.key === queueKey(route) ? queueState.queue : null;
}

export async function loadQueue(): Promise<void> {
  const route = readRoute(currentHash());
  if (route.view === QUEUE) await queueLoad(route);
}

export const OUTBOX_PATH = "/api/outbox";
// OUTBOX_LAYOUT is the served layout of a view the Outbox draws, whatever the view's id.
export const OUTBOX_LAYOUT = "outbox";

export async function loadOutbox(): Promise<void> {
  try {
    put(outboxState, "page", await getJSON(OUTBOX_PATH));
    outboxState.error = "";
  } catch (err) {
    outboxState.error = t("Could not load the Outbox: {error}", { error: (err as Error).message });
  }
}

// tasksURL lists view, narrowed to area; a page since a position also holds the tasks of units, as
// provider:key.
export function tasksURL(view: string, area: string, ctx: string[], since: string, units?: string[]): string {
  const p = new URLSearchParams({ view });
  if (area) p.set("area", area);
  if (ctx && ctx.length) p.set("context", ctx.join(","));
  if (since) p.set("since", since);
  for (const u of (since && units) || []) p.append("units", u);
  return "/api/tasks?" + p.toString();
}

// FILTER_COUNT_DELAY_MS lets a burst of keystrokes ask for the counts once.
export const FILTER_COUNT_DELAY_MS = 250;

// countersURL counts area, every area when "", narrowed to the tasks the lens query q keeps.
export function countersURL(area: string, q?: string): string {
  const p = new URLSearchParams();
  if (area) p.set("area", area);
  if (q) p.set("q", q);
  const query = p.toString();
  return "/api/counters" + (query ? "?" + query : "");
}

// filterQuery is the filter as the daemon reads it, qualifiers and all.
export function filterQuery(route: Pick<Hash, "q">): string {
  return String(route.q || "").trim();
}

// viewCounters are the counts the rail, the area picker and the context chips show: with a filter, those of the
// tasks it keeps, the last read while a newer one is on its way; null before the first.
export function viewCounters(route: Pick<Hash, "q" | "area">): J {
  if (!filterQuery(route)) return tasksState.counters;
  const f = tasksState.filtered;
  return f && f.area === (route.area || "") ? f.counters : null;
}

function filteredKey(route: Pick<Hash, "q" | "area">): string {
  return [route.area || "", filterQuery(route)].join("\u0000");
}

// timers lets tests run the counters' delay without the wall clock.
export const timers = { wait: (ms: number) => new Promise<void>((done) => setTimeout(done, ms)) };

// loadFiltered reads the counters the filter narrows, once the query stops changing for FILTER_COUNT_DELAY_MS;
// only the latest read is kept, and a failed one leaves the rail without counts.
export async function loadFiltered(route: Pick<Hash, "q" | "area">): Promise<void> {
  const q = filterQuery(route);
  const seq = ++tasksState.filterSeq;
  if (!q) {
    tasksState.filtered = null;
    return;
  }
  const key = filteredKey(route);
  if (!tasksState.filtered || tasksState.filtered.key !== key) await timers.wait(FILTER_COUNT_DELAY_MS);
  if (seq !== tasksState.filterSeq) return;
  try {
    const c = await getJSON(countersURL(route.area, q));
    if (seq === tasksState.filterSeq) tasksState.filtered = { key, area: route.area || "", counters: c };
  } catch {
    if (seq === tasksState.filterSeq) tasksState.filtered = null;
  }
}

// applyTasksPage folds a page into the list; a page of another query, epoch or a full page replaces it, an older
// one is dropped. A changed task keeps its place and a new one comes last.
export function applyTasksPage(key: string, page: J): void {
  const cur = tasksState.page;
  const same = tasksState.key === key && cur && cur.epoch === page.epoch;
  if (same && !page.full && page.rev < cur.rev) return;
  if (tasksState.key !== key) {
    keepList();
    tasksState.tasks = page.tasks || [];
    tasksState.gates = page.gates || [];
  } else if (!same || page.full) {
    put(tasksState, "tasks", page.tasks || []);
    put(tasksState, "gates", page.gates || []);
  } else {
    const gone = new Set<string>(page.removed || []);
    put(tasksState, "tasks", mergeList(tasksState.tasks, page.tasks || [], gone, (e) => e.id));
    if (page.gates) put(tasksState, "gates", page.gates);
  }
  tasksState.key = key;
  tasksState.loading = page.loading || [];
  tasksState.page = { epoch: page.epoch || "", rev: page.rev || 0 };
  tasksState.rev++;
  if (page.counters) put(tasksState, "counters", page.counters);
}

// readViews reads the catalog of the task views; false when it failed.
export async function readViews(): Promise<boolean> {
  try {
    const next = await getJSON("/api/views");
    if (JSON.stringify(tasksState.catalog) !== JSON.stringify(next)) tasksState.catalog = next;
    tasksState.catalogError = "";
    return true;
  } catch (err) {
    tasksState.catalogError = (err as Error).message;
    return false;
  }
}

// forgetTasksPage makes the next list read whole, and forgets the lists of the views left.
export function forgetTasksPage(): void {
  tasksState.page = null;
  keptLists.clear();
}

// KEPT_LISTS bounds the views whose list is kept: each area and context a view is narrowed to is a view of its own.
const KEPT_LISTS = 8;

// keepList keeps the list shown, as the list of its view, before another takes its place.
function keepList(): void {
  const { key, page } = tasksState;
  if (!key || !page) return;
  keptLists.delete(key);
  keptLists.set(key, { tasks: tasksState.tasks, gates: tasksState.gates, loading: tasksState.loading, page });
  if (keptLists.size > KEPT_LISTS) keptLists.delete(keptLists.keys().next().value!);
}

// showKept shows the list kept for key, if any, in place of the one shown.
function showKept(key: string): void {
  const kept = keptLists.get(key);
  if (!kept || tasksState.key === key) return;
  keepList();
  keptLists.delete(key);
  Object.assign(tasksState, kept, { key });
  tasksState.rev++;
}

// listReads counts the list reads, so a page read for a view left since is not shown.
let listReads = 0;

// loadTasks loads the catalog once, then the list of route (the hash's when not given, read once the catalog can
// resolve it) with its counters, or the counters alone on a plugin view, and the counters the filter narrows.
// units are those a change named, whose tasks the next page since the list's position holds.
export async function loadTasks(given?: Hash, units?: string[]): Promise<void> {
  if (units) tasksState.units = [...new Set([...tasksState.units, ...units])];
  if (!tasksState.catalog && !(await readViews())) return;
  const route = given || readHash();
  await Promise.all([loadTaskList(route), loadFiltered(route)]);
}

async function loadTaskList(route: Hash): Promise<void> {
  if (route.view === REVIEW) return loadReview();
  if (!isCoreView(route.view)) {
    try {
      put(tasksState, "counters", await getJSON(countersURL(route.area)));
    } catch {
      // The rail keeps its last counts.
    }
    return;
  }
  const key = viewQueryKey(route.view, route.area, route.ctx);
  showKept(key);
  const read = ++listReads;
  const cur = tasksState.page;
  const since = tasksState.key === key && cur && cur.epoch ? `${cur.epoch}:${cur.rev}` : "";
  const units = tasksState.units;
  const outbox = (viewSpec(route.view) || {}).layout === OUTBOX_LAYOUT ? loadOutbox() : null;
  let page: J;
  try {
    page = await getJSON(tasksURL(route.view, route.area, route.ctx, since, units));
    if (read !== listReads) return;
    applyTasksPage(key, page);
    tasksState.units = tasksState.units.filter((u) => !units.includes(u));
    tasksState.error = "";
  } catch (err) {
    if (read === listReads) tasksState.error = t("Could not load the {view} list: {error}", { view: route.view, error: (err as Error).message });
    return;
  } finally {
    await outbox;
  }
  if (page.specs && page.specs !== (tasksState.catalog || {}).version) await readViews();
}

// reviewSteps are the views the catalog's review walks, in its order, each with the label, count and done mark of
// the Review's step that shows it.
export function reviewSteps(): J[] {
  const served = (reviewState.review && reviewState.review.steps) || [];
  const views = (tasksState.catalog && tasksState.catalog.review) || [];
  if (!views.length) return served;
  return views.map((v: string) => {
    const s = served.find((x: J) => x.view === v || x.id === v) || {};
    return { id: s.id || v, view: v, label: s.label || viewLabel(v), count: s.count !== undefined ? s.count : ((tasksState.counters || {}).views || {})[v] || 0, done: !!s.done };
  });
}

// reviewFirstOpen is the first step not done after index from (wrapping), the last step when every one is done.
export function reviewFirstOpen(steps: J[], from = -1): string {
  for (let i = 1; i <= steps.length; i++) {
    const s = steps[(from + i) % steps.length];
    if (!s.done) return s.id;
  }
  return steps.length ? steps[steps.length - 1].id : "";
}

export function reviewCurrent(): J {
  return reviewSteps().find((s) => s.id === reviewState.step) || null;
}

// loadReview reads the Review and the tasks of the step shown, the first open step when none is.
export async function loadReview(): Promise<void> {
  try {
    put(reviewState, "review", await getJSON("/api/review"));
    reviewState.error = "";
  } catch (err) {
    reviewState.error = t("Could not load the review: {error}", { error: (err as Error).message });
    return;
  }
  if (!reviewCurrent()) reviewState.step = reviewFirstOpen(reviewSteps());
  await loadReviewStep();
}

export async function loadReviewStep(): Promise<void> {
  const step = reviewCurrent();
  if (!step) return;
  try {
    const page = await getJSON(tasksURL(step.view, "", [], ""));
    const key = "review|" + step.id;
    if (tasksState.key === key) put(tasksState, "tasks", page.tasks || []);
    else {
      keepList();
      tasksState.tasks = page.tasks || [];
    }
    tasksState.key = key;
    tasksState.page = null;
    tasksState.rev++;
    if (page.counters) put(tasksState, "counters", page.counters);
    tasksState.error = "";
  } catch (err) {
    tasksState.error = t("Could not load the {step} step: {error}", { step: step.label, error: (err as Error).message });
  }
}

export function taskPath(id: string, ...rest: string[]): string {
  return "/api/tasks/" + [id, ...rest].map((s) => encodeURIComponent(s).replace(/%3A/gi, ":")).join("/");
}

// handlerVerb is the catalog's verb whose handler is handler, its own name when it names none; "" when none is.
export function handlerVerb(handler: string): string {
  const v = ((tasksState.catalog && tasksState.catalog.verbs) || []).find((x: J) => (x.handler || x.verb) === handler);
  return v ? v.verb : "";
}

// verbFor is the verb that runs handler, handler itself before the catalog was read.
export function verbFor(handler: string): string {
  return handlerVerb(handler) || handler;
}

function resetTaskPage(id: string): void {
  Object.assign(taskState, { id, timeline: null, timelineError: "", kinds: [], kindsSeen: [], text: "", broadcast: null, transcript: null, drafts: [] });
  gateState.answers.clear();
  gateState.picked.clear();
}

// timelineURL is task id's timeline folded into facts, narrowed to kinds when any.
export function timelineURL(id: string, kinds: string[]): string {
  const q = new URLSearchParams({ as: "nodes" });
  if (kinds.length) q.set("kinds", kinds.join(","));
  return taskPath(id, "timeline") + "?" + q.toString();
}

// loadTaskPage reads the timeline of e, narrowed to the kinds picked, and the drafts that wait on e.
export async function loadTaskPage(e: J): Promise<void> {
  if (taskState.id !== e.id) resetTaskPage(e.id);
  await Promise.all([loadTimeline(e), loadTaskDrafts(e)]);
}

export async function loadTimeline(e: J): Promise<void> {
  try {
    const nodes = await getJSON(timelineURL(e.id, taskState.kinds));
    if (taskState.id !== e.id) return;
    put(taskState, "timeline", nodes || []);
    taskState.timelineError = "";
    for (const n of taskState.timeline) for (const ev of n.events || []) if (!taskState.kindsSeen.includes(ev.kind)) taskState.kindsSeen.push(ev.kind);
  } catch (err) {
    if (taskState.id === e.id) taskState.timelineError = t("Could not load the timeline: {error}", { error: (err as Error).message });
  }
}

// loadTaskDrafts reads the drafts e's pending draft ids name; none when it names none or they cannot be read.
export async function loadTaskDrafts(e: J): Promise<void> {
  if (!(e.drafts || []).length) {
    taskState.drafts = [];
    return;
  }
  const out = await getJSON("/api/drafts?" + new URLSearchParams({ task: e.id }).toString()).catch(() => null);
  if (taskState.id === e.id) put(taskState, "drafts", (out && out.drafts) || []);
}

// loadUnitTasks reads the open tasks that hold the unit key, "<provider>:<key>", and the held and failed drafts
// that wait on it but those an open question pins, which are that question's to decide; no drafts when they
// cannot be read.
export async function loadUnitTasks(key: string): Promise<void> {
  detailState.unitKey = key || "";
  if (!key) {
    detailState.unitTasks = [];
    detailState.unitDrafts = [];
    return;
  }
  const u = keyUnit(key);
  const [tasks, drafts] = await Promise.all([
    getJSON(unitPath(u, "tasks")).catch(() => []),
    getJSON("/api/drafts?" + new URLSearchParams({ unit: u.provider + "/" + u.key, state: "held,failed" }).toString()).catch(() => null),
  ]);
  if (detailState.unitKey !== key) return;
  put(detailState, "unitTasks", tasks);
  put(detailState, "unitDrafts", ((drafts && drafts.drafts) || []).filter((d: J) => !d.pinned_by));
}

// loadDetailPane reads the task route names, marking an inbox task seen once, or the open tasks of the unit it
// names.
export async function loadDetailPane(route: { task: string; unit: string }): Promise<void> {
  if (!route.task) {
    Object.assign(detailState, { id: "", task: null, error: "", seenFor: "" });
    return loadUnitTasks(route.unit);
  }
  const id = route.task;
  if (detailState.id !== id) Object.assign(detailState, { id, task: null, error: "", seenFor: "" });
  try {
    const e = await getJSON(taskPath(id));
    if (detailState.id !== id) return;
    put(detailState, "task", e);
    detailState.error = "";
    await loadTaskPage(e);
    if (e.category === "inbox" && detailState.seenFor !== id) {
      detailState.seenFor = id;
      postJSON(taskPath(id, verbFor("seen")), { source: "cockpit" }).catch(() => {});
    }
  } catch (err) {
    if (detailState.id !== id) return;
    detailState.task = null;
    detailState.error = (err as Error).message;
  }
}

// unitFallback is the route a unit with no detail opens instead: the first open task that holds it; "" when none
// does or the read failed otherwise.
export function unitFallback(err: unknown, tasks: J[]): string {
  return err instanceof APIError && err.status === 404 && tasks && tasks.length ? "#" + viewHash({ task: tasks[0].id, pr: "" }) : "";
}

const checksLoading = new Set<string>();

// loadChecks runs beside the rest: naming the failed steps can take a few calls, and the rows must not wait for
// them.
export async function loadChecks(pr: string): Promise<void> {
  if (checksLoading.has(pr)) return;
  checksLoading.add(pr);
  try {
    const list = await getJSON(unitPath(keyUnit(pr), "checks"));
    state.checks[pr] = list;
  } catch {
    // The row's reason already names the checks; the panel goes without links.
  } finally {
    checksLoading.delete(pr);
  }
}

// page moves the page to a hash without a history entry, as a unit with no detail falls back to its task; tests
// replace it.
export const page = { replace: (hash: string) => location.replace(hash) };

export async function loadDetail(): Promise<void> {
  const { pr, task } = readHash();
  await loadDetailPane({ task, unit: pr });
  if (!pr || task) {
    state.detail = null;
    state.detailError = "";
    return;
  }
  try {
    const d = await getJSON(unitPath(keyUnit(pr)));
    if (readHash().pr !== pr) return;
    if (state.detail && unitKey(state.detail.row.unit) === unitKey(d.row.unit)) put(state, "detail", d);
    else state.detail = d;
    state.detailError = "";
    if (d.row.kind === "checks") loadChecks(pr);
  } catch (err) {
    if (readHash().pr !== pr) return;
    state.detail = null;
    const instead = unitFallback(err, detailState.unitTasks);
    state.detailError = instead ? "" : (err as Error).message;
    if (instead) page.replace(instead);
  }
}

