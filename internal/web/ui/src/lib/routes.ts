export interface Unit {
  provider: string;
  key: string;
}

const ROUTE_PROVIDER = /^[a-z][a-z0-9-]*$/;
const ROUTE_KEY_MAX = 300;
// ROUTE_LAYOUTS are a list's board, its list (over a view the profile draws as a board) and a window that shows
// one item's detail alone.
const ROUTE_LAYOUTS = ["board", "list", "detail"];

// routeUnitKey reads a unit= value, "<provider>/<key>" once decoded, as the cockpit's unit key
// "<provider>:<key>"; "" when it is not one.
export function routeUnitKey(raw: string | null | undefined): string {
  const s = String(raw || "");
  const at = s.indexOf("/");
  if (at < 1) return "";
  const provider = s.slice(0, at);
  const key = s.slice(at + 1);
  if (!ROUTE_PROVIDER.test(provider) || !key || key.length > ROUTE_KEY_MAX || /\s/.test(key)) return "";
  return provider + ":" + key;
}

// routeUnit is the unit= value of a unit key, its key escaped.
export function routeUnit(key: string | null | undefined): string {
  const k = String(key || "");
  const at = k.indexOf(":");
  if (at < 1) return "";
  return k.slice(0, at) + "/" + encodeURIComponent(k.slice(at + 1));
}

// keyUnit is the unit a unit key "<provider>:<key>" names.
export const keyUnit = (k: string): Unit => ({ provider: k.slice(0, k.indexOf(":")), key: k.slice(k.indexOf(":") + 1) });
// unitPath is the API path of u's detail, followed by the segments of rest.
export const unitPath = (u: Unit, ...rest: string[]): string => "/api/units/" + [u.provider, u.key, ...rest].map(encodeURIComponent).join("/");

// routeHash is a hash (no "#") with view first, then unit, then rest, a URLSearchParams of the other
// parameters.
export function routeHash(view: string, unitKey: string, rest?: URLSearchParams): string {
  const parts: string[] = [];
  if (view) parts.push("view=" + encodeURIComponent(view));
  const u = routeUnit(unitKey);
  if (u) parts.push("unit=" + u);
  const tail = rest ? rest.toString() : "";
  if (tail) parts.push(tail);
  return parts.join("&");
}

// HOME is the cockpit's home route, which no view of the profile may be named; QUEUE the view of a decision
// queue's route; PLAN the core's screen of every workflow's plan and RECENT its feed of the newest events of
// every unit the loop follows, which no view may be named either.
export const HOME = "home";
export const QUEUE = "queue";
export const PLAN = "plan";
export const RECENT = "recent";
export const EXPLORE = "explore";
export const DASH = "dash";
// QUEUE_LIST is the queue of the tasks a route's ids name, in their order; QUEUE_IDS_MAX the most it takes.
export const QUEUE_LIST = "list";
export const QUEUE_IDS_MAX = 200;

// routeView is the view a route's parameters name: its view, the queue's for a queue, none for an item opened
// alone, else the home.
function routeView(p: URLSearchParams): string {
  if (p.get("view")) return p.get("view") as string;
  if (p.get("queue")) return QUEUE;
  return p.get("task") || p.get("unit") || p.get("panel") ? "" : HOME;
}

// routeParams are hash's parameters; a path such as "task/draft:12" or "unit/github/acme%2Fapi%237" is that one
// parameter, as "task=draft:12" would be.
export function routeParams(hash: string): URLSearchParams {
  const raw = String(hash || "").replace(/^#/, "");
  const path = raw.match(/^(task|unit|view|queue)\/(.+)$/);
  if (!path) return new URLSearchParams(raw);
  let value = path[2];
  try {
    value = decodeURIComponent(value);
  } catch {
    // a malformed escape stays as typed
  }
  return new URLSearchParams([[path[1], value]]);
}

export interface Route {
  view: string;
  queue: string;
  ids: string[];
  unit: string;
  task: string;
  area: string;
  ctx: string[];
  q: string;
  panel: string;
  layout: string;
  link: string;
}

// readRoute is the route a hash names; view is "" when it names none.
export function readRoute(hash: string): Route {
  const p = routeParams(hash);
  const ctx = (p.get("ctx") || "").split(",").map((c) => c.trim()).filter(Boolean);
  const layout = p.get("layout") || "";
  return {
    view: routeView(p),
    queue: (p.get("queue") || "").slice(0, 100),
    ids: (p.get("ids") || "").split(",").map((x) => x.trim()).filter(Boolean).slice(0, QUEUE_IDS_MAX),
    unit: routeUnitKey(p.get("unit")),
    task: (p.get("task") || "").slice(0, 200),
    area: (p.get("area") || "").slice(0, 100),
    ctx,
    q: p.get("q") || "",
    panel: p.get("panel") || "",
    layout: ROUTE_LAYOUTS.includes(layout) ? layout : "",
    link: p.get("link") || "",
  };
}

// routeItem is the item a route's detail shows, the task before the unit, as the window controller reads it;
// "" when it names none.
export const routeItem = (r: Route): string => (r.task ? "task:" + r.task : r.unit ? "unit:" + r.unit : "");
