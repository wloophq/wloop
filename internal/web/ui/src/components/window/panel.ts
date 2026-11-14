import { readRoute, routeItem } from "../../lib/routes";
import type { J } from "../../lib/util";
import { API_VERSION, net } from "../../state/api";

export type Post = (body: J) => unknown;

export interface WinLike {
  location: { hash: string; search?: string; origin?: string };
  history: { replaceState: (data: unknown, unused: string, url: string) => void };
  addEventListener: (type: string, fn: (e: J) => void, capture?: boolean) => void;
  close?: () => void;
}

export interface DocLike {
  addEventListener: (type: string, fn: (e: J) => void, capture?: boolean) => void;
}

// pinRoutes keeps win on the routes keeps accepts and hands every other one, followed as a link or set as the
// hash, to the main window. Its listeners must be added before the cockpit's hashchange listener, which they
// stop for a route handed over.
export function pinRoutes(win: WinLike, doc: DocLike, keeps: (hash: string) => boolean, post: Post): void {
  let own = win.location.hash;
  const handOver = (hash: string) => post({ action: "open", route: String(hash).replace(/^#/, "") });
  doc.addEventListener(
    "click",
    (e) => {
      const a = e.target && e.target.closest ? e.target.closest('a[href^="#"]') : null;
      const href = a ? a.getAttribute("href") : "";
      if (!href || keeps(href)) return;
      e.preventDefault();
      e.stopImmediatePropagation();
      handOver(href);
    },
    true,
  );
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

// panelInstall pins the omnibox panel (#panel=...): a route without panel= opens in the main window and the panel
// keeps its own; window.close() hides the panel, since the webview ignores it on a page no script opened.
export function panelInstall(win: WinLike, doc: DocLike, post: Post): boolean {
  if (!readRoute(win.location.hash).panel) return false;
  pinRoutes(win, doc, (hash) => !!readRoute(hash).panel, post);
  win.close = () => post({ action: "hide-panel" });
  return true;
}

// detailWindowInstall pins a detached window (#...&layout=detail): the routes of its item stay and any other
// route opens in the main window.
export function detailWindowInstall(win: WinLike, doc: DocLike, post: Post): boolean {
  const r = readRoute(win.location.hash);
  const item = routeItem(r);
  if (r.layout !== "detail" || !item) return false;
  pinRoutes(
    win,
    doc,
    (hash) => {
      const next = readRoute(hash);
      return next.layout === "detail" && routeItem(next) === item;
    },
    post,
  );
  return true;
}

export function postWindow(body: J): Promise<unknown> {
  return net
    .fetch("/api/window", {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json", Accept: "application/json", "X-Loop-API": API_VERSION },
      body: JSON.stringify(body),
    })
    .catch(() => {});
}
