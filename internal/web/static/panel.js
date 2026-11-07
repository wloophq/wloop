"use strict";

// In the omnibox panel (#panel=...), a route without panel= opens in the main
// window and the panel keeps its own; window.close() hides the panel, since
// the webview ignores it on a page no script opened.
function panelInstall(win, doc, post) {
  if (!readRoute(win.location.hash).panel) return false;
  let own = win.location.hash;
  const handOver = (hash) => post({ action: "open", route: String(hash).replace(/^#/, "") });
  doc.addEventListener("click", (e) => {
    const a = e.target && e.target.closest ? e.target.closest('a[href^="#"]') : null;
    const href = a ? a.getAttribute("href") : "";
    if (!href || readRoute(href).panel) return;
    e.preventDefault();
    e.stopImmediatePropagation();
    handOver(href);
  }, true);
  win.addEventListener("hashchange", (e) => {
    const hash = win.location.hash;
    if (readRoute(hash).panel) {
      own = hash;
      return;
    }
    e.stopImmediatePropagation();
    win.history.replaceState(null, "", own);
    handOver(hash);
  });
  win.close = () => post({ action: "hide-panel" });
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
