"use strict";

const ROUTE_PROVIDER = /^[a-z][a-z0-9-]*$/;
const ROUTE_KEY_MAX = 300;
// ROUTE_LAYOUTS are a list's board, its list (over a view the profile draws as a board) and a window that shows
// one item's detail alone.
const ROUTE_LAYOUTS = ["board", "list", "detail"];

// routeUnitKey reads a unit= value, "<provider>/<key>" once decoded, as the
// cockpit's unit key "<provider>:<key>"; "" when it is not one.
function routeUnitKey(raw) {
  const s = String(raw || "");
  const at = s.indexOf("/");
  if (at < 1) return "";
  const provider = s.slice(0, at);
  const key = s.slice(at + 1);
  if (!ROUTE_PROVIDER.test(provider) || !key || key.length > ROUTE_KEY_MAX || /\s/.test(key)) return "";
  return provider + ":" + key;
}

// routeUnit is the unit= value of a unit key, its key escaped.
function routeUnit(key) {
  const at = String(key || "").indexOf(":");
  if (at < 1) return "";
  return key.slice(0, at) + "/" + encodeURIComponent(key.slice(at + 1));
}

// keyUnit is the unit a unit key "<provider>:<key>" names.
const keyUnit = (k) => ({ provider: k.slice(0, k.indexOf(":")), key: k.slice(k.indexOf(":") + 1) });
// unitPath is the API path of u's detail, followed by the segments of rest.
const unitPath = (u, ...rest) => "/api/units/" + [u.provider, u.key, ...rest].map(encodeURIComponent).join("/");

// routeHash is a hash (no "#") with view first, then unit, then rest, a
// URLSearchParams of the other parameters.
function routeHash(view, unitKey, rest) {
  const parts = [];
  if (view) parts.push("view=" + encodeURIComponent(view));
  const u = routeUnit(unitKey);
  if (u) parts.push("unit=" + u);
  const tail = rest ? rest.toString() : "";
  if (tail) parts.push(tail);
  return parts.join("&");
}

// HOME is the cockpit's home route, which no view of the profile may be named; QUEUE the view of a decision
// queue's route; PLAN the core's screen of every workflow's plan, which no view may be named either.
const HOME = "home";
const QUEUE = "queue";
const PLAN = "plan";
// QUEUE_LIST is the queue of the tasks a route's ids name, in their order; QUEUE_IDS_MAX the most it takes.
const QUEUE_LIST = "list";
const QUEUE_IDS_MAX = 200;

// routeView is the view a route's parameters name: its view, the queue's for a queue, none for an item opened
// alone, else the home.
function routeView(p) {
  if (p.get("view")) return p.get("view");
  if (p.get("queue")) return QUEUE;
  return p.get("task") || p.get("unit") || p.get("panel") ? "" : HOME;
}

// readRoute is the route a hash names; view is "" when it names none.
function readRoute(hash) {
  const p = new URLSearchParams(String(hash || "").replace(/^#/, ""));
  const ctx = (p.get("ctx") || "").split(",").map((c) => c.trim()).filter(Boolean);
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
    layout: ROUTE_LAYOUTS.includes(p.get("layout")) ? p.get("layout") : "",
    link: p.get("link") || "",
  };
}
