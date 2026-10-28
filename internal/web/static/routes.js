"use strict";

const ROUTE_PROVIDER = /^[a-z][a-z0-9-]*$/;
const ROUTE_KEY_MAX = 300;

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

// readRoute is the route a hash names; view is "" when it names none.
function readRoute(hash) {
  const p = new URLSearchParams(String(hash || "").replace(/^#/, ""));
  const ctx = (p.get("ctx") || "").split(",").map((c) => c.trim()).filter(Boolean);
  return {
    view: p.get("view") || "",
    unit: routeUnitKey(p.get("unit")),
    task: (p.get("task") || "").slice(0, 200),
    area: (p.get("area") || "").slice(0, 100),
    ctx,
    q: p.get("q") || "",
  };
}
