"use strict";

// pinRoutes keeps win on the routes keeps accepts and hands every other one,
// followed as a link or set as the hash, to the main window.
function pinRoutes(win, doc, keeps, post) {
  let own = win.location.hash;
  const handOver = (hash) => post({ action: "open", route: String(hash).replace(/^#/, "") });
  doc.addEventListener("click", (e) => {
    const a = e.target && e.target.closest ? e.target.closest('a[href^="#"]') : null;
    const href = a ? a.getAttribute("href") : "";
    if (!href || keeps(href)) return;
    e.preventDefault();
    e.stopImmediatePropagation();
    handOver(href);
  }, true);
  win.addEventListener("hashchange", (e) => {
    const hash = win.location.hash;
    if (keeps(hash)) {
      own = hash;
      return;
    }
    e.stopImmediatePropagation();
    win.history.replaceState(null, "", own);
    handOver(hash);
  });
}

// In the omnibox panel (#panel=...), a route without panel= opens in the main
// window and the panel keeps its own; window.close() hides the panel, since
// the webview ignores it on a page no script opened.
function panelInstall(win, doc, post) {
  if (!readRoute(win.location.hash).panel) return false;
  pinRoutes(win, doc, (hash) => !!readRoute(hash).panel, post);
  win.close = () => post({ action: "hide-panel" });
  return true;
}

// routeItem is the item a route's detail shows, the task before the unit, as
// the window controller reads it; "" when it names none.
const routeItem = (r) => (r.task ? "task:" + r.task : r.unit ? "unit:" + r.unit : "");

// In a detached window (#...&layout=detail), the routes of its item stay and
// any other route opens in the main window.
function detailWindowInstall(win, doc, post) {
  const r = readRoute(win.location.hash);
  const item = routeItem(r);
  if (r.layout !== "detail" || !item) return false;
  pinRoutes(win, doc, (hash) => {
    const next = readRoute(hash);
    return next.layout === "detail" && routeItem(next) === item;
  }, post);
  return true;
}

function postWindow(body) {
  return fetch("/api/window", {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json", Accept: "application/json", "X-Loop-API": API_VERSION },
    body: JSON.stringify(body),
  }).catch(() => {});
}

if (typeof window !== "undefined") panelInstall(window, document, postWindow);
