import { isPluginView, resolveView } from "../lib/catalog";
import { DASH, EXPLORE, QUEUE, readRoute, routeHash } from "../lib/routes";
import { catalog, tasksState } from "./store.svelte";

const UNIT_KEY = /^[a-z][a-z0-9-]*:\S{1,300}$/;
export const QUERY_MAX = 200;
export const EXPLORE_MAX = 256;

// nav is the page's hash, the route every view derives from: a hashchange, from a link, the keys or the native
// host's location.hash, moves it, and so does writeHash.
export const nav = $state({ hash: typeof location === "undefined" ? "" : location.hash });

// win is how the router reaches the window; tests replace it.
export const win = {
  location: () => (typeof location === "undefined" ? ({ hash: "" } as Location) : location),
  replaceState: (hash: string) => history.replaceState(null, "", hash),
};

export interface Hash {
  view: string;
  queue: string;
  ids: string[];
  pr: string;
  task: string;
  area: string;
  ctx: string[];
  layout: string;
  q: string;
  g: string;
  s: string;
  x: string;
  tk: string;
  done: boolean;
}

// written is the hash a writeHash set that no hashchange has reported yet, and over the route nav held then.
const written = { hash: "", over: "" };

// currentHash is the route as it is now: nav's, or a write the browser has not reported yet while the window still
// holds it (a panel that hands a route over puts its own back); read through nav, so what derives from the route
// follows each move.
export function currentHash(): string {
  const h = nav.hash;
  return written.hash && written.over === h && win.location().hash === written.hash ? written.hash : h;
}

export function readHash(hash: string = currentHash()): Hash {
  const p = new URLSearchParams(hash.replace(/^#/, ""));
  const route = readRoute(hash);
  const view = resolveView(route.view, tasksState.catalog, catalog.views, isPluginView);
  return {
    view,
    queue: view === QUEUE ? route.queue : "",
    ids: view === QUEUE ? route.ids : [],
    pr: UNIT_KEY.test(route.unit) ? route.unit : "",
    task: route.task,
    area: route.area,
    ctx: route.ctx,
    layout: route.layout,
    q: (p.get("q") || "").slice(0, QUERY_MAX),
    g: (p.get("g") || "").slice(0, QUERY_MAX),
    s: (p.get("s") || "").slice(0, QUERY_MAX),
    x: view === EXPLORE ? (p.get("x") || "").slice(0, EXPLORE_MAX) : "",
    tk: view === DASH && /^[a-z-]+$/.test(p.get("tk") || "") ? (p.get("tk") as string) : "",
    done: view === DASH && p.get("done") === "1",
  };
}

export function hashString(next: Partial<Hash> & { view?: string }): string {
  const p = new URLSearchParams();
  const queue = next.view === QUEUE && next.queue;
  if (queue) p.set("queue", next.queue as string);
  if (queue && next.ids && next.ids.length) p.set("ids", next.ids.join(","));
  if (next.task) p.set("task", next.task);
  if (next.area) p.set("area", next.area);
  if (next.ctx && next.ctx.length) p.set("ctx", ([] as string[]).concat(next.ctx).join(","));
  if (next.layout) p.set("layout", next.layout);
  if (next.q) p.set("q", next.q);
  if (next.g) p.set("g", next.g);
  if (next.s) p.set("s", next.s);
  if (next.x && next.view === EXPLORE) p.set("x", next.x);
  if (next.tk && next.view === DASH) p.set("tk", next.tk);
  if (next.done && next.view === DASH) p.set("done", "1");
  return routeHash(queue ? "" : next.view || "", next.pr || "", p);
}

// viewHash is the hash of the current route with change applied.
export function viewHash(change: Partial<Hash> & { ctx?: string[] | string }): string {
  return hashString({ ...readHash(), ...(change as Partial<Hash>) });
}

// writeHash moves the route by change: a new history entry, or with replace the current one, which no
// hashchange reports.
export function writeHash(change: Partial<Hash> & { ctx?: string[] | string }, replace = false): void {
  const hash = viewHash(change);
  if (!replace) {
    const over = nav.hash;
    win.location().hash = hash;
    Object.assign(written, { hash: win.location().hash, over });
    return;
  }
  written.hash = "";
  win.replaceState("#" + hash);
  nav.hash = "#" + hash;
}

// followHash keeps nav on the window's hash; listeners added before it, such as a panel's, run first and may
// stop a hashchange from reaching it.
export function followHash(target: Window): () => void {
  const on = () => {
    written.hash = "";
    nav.hash = target.location.hash;
  };
  target.addEventListener("hashchange", on);
  nav.hash = target.location.hash;
  return () => target.removeEventListener("hashchange", on);
}
