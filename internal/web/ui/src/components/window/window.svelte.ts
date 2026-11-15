import { brand } from "../../brand";
import { HOME, PLAN, RECENT, servedView, viewLabel } from "../../lib/catalog";
import { t } from "../../lib/i18n.svelte";
import { rowByKey } from "../../lib/lens";
import { readRoute, routeItem } from "../../lib/routes";
import { labelOf, type J } from "../../lib/util";
import { clipboard, setNotice } from "../../state/actions.svelte";
import { nav, readHash } from "../../state/route.svelte";
import { detailState, state } from "../../state/store.svelte";
import { detailWindowInstall, postWindow, type DocLike, type Post, type WinLike } from "./panel";

const DETAIL_LAYOUT = "layout=detail";

// insetTitleBar says the native window draws its controls over the page, which then draws the title bar around
// them.
export function insetTitleBar(search: string): boolean {
  return new URLSearchParams(String(search || "")).get("titlebar") === "inset";
}

export const detailOnly = (hash: string): boolean => readRoute(hash).layout === "detail";

// linkRoute is the route of hash without what only one window shows: the detail layout, the panel and a link's
// notice.
export function linkRoute(hash: string): string {
  return String(hash || "")
    .replace(/^#/, "")
    .split("&")
    .filter((p) => p && p !== DETAIL_LAYOUT && !/^(panel|link)=/.test(p))
    .join("&");
}

// deepLink opens the route of hash in the app; it carries no port and no token.
export function deepLink(scheme: string, hash: string): string {
  return scheme + "://open/#" + linkRoute(hash);
}

// detachTarget is how the item hash shows opens alone: a request to the native window, or, in a browser, a tab on
// the detail layout.
export function detachTarget(hash: string, native: boolean): { post?: J; open?: string } {
  const route = linkRoute(hash);
  return native ? { post: { action: "detach", route } } : { open: "#" + (route ? route + "&" : "") + DETAIL_LAYOUT };
}

// takeLinkNotice drops the mark of a link that could not be read from win's route; true when there was one to
// tell about.
export function takeLinkNotice(win: WinLike): boolean {
  const parts = String(win.location.hash || "").replace(/^#/, "").split("&");
  if (!parts.some((p) => p.startsWith("link="))) return false;
  const hash = "#" + parts.filter((p) => !p.startsWith("link=")).join("&");
  win.history.replaceState(null, "", hash);
  nav.hash = hash;
  return true;
}

const bridgeOf = (win: J) => win.webkit && win.webkit.messageHandlers && win.webkit.messageHandlers.external;

// installDrag moves the native window from any element whose --wails-draggable is drag, as Wails' own runtime
// does, through the bridge the native window injects; false in a browser.
export function installDrag(doc: DocLike, win: J): boolean {
  const bridge = bridgeOf(win);
  if (!bridge) return false;
  const drags = (el: J) => !!el && win.getComputedStyle(el).getPropertyValue("--wails-draggable").trim() === "drag";
  let armed = false;
  doc.addEventListener("mousedown", (e) => {
    armed = e.button === 0 && e.detail === 1 && drags(e.target);
  });
  doc.addEventListener("mousemove", (e) => {
    if (!armed) return;
    armed = false;
    if (e.buttons === 1) bridge.postMessage("wails:drag");
  });
  doc.addEventListener("mouseup", () => {
    armed = false;
  });
  doc.addEventListener("dblclick", (e) => {
    if (drags(e.target)) bridge.postMessage("wails:drag:doubleclick");
  });
  return true;
}

// installExternalLinks hands a link to another site, or a window.open of one, to post, which opens it in the
// default browser: the native window's web view drops both. False, and nothing installed, in a browser.
export function installExternalLinks(doc: DocLike, win: J, post: Post): boolean {
  if (!bridgeOf(win)) return false;
  const outside = (href: unknown) => {
    try {
      const u = new URL(String(href), win.location.origin);
      return (u.protocol === "http:" || u.protocol === "https:") && u.origin !== win.location.origin ? u.href : "";
    } catch {
      return "";
    }
  };
  doc.addEventListener(
    "click",
    (e) => {
      const a = e.button === 0 && e.target && e.target.closest ? e.target.closest("a[href]") : null;
      const url = a ? outside(a.href) : "";
      if (!url) return;
      e.preventDefault();
      post({ action: "external", url });
    },
    true,
  );
  const open = win.open;
  win.open = (href: string, ...rest: unknown[]) => {
    const url = outside(href);
    if (!url) return open.call(win, href, ...rest);
    post({ action: "external", url });
    return null;
  };
  return true;
}

// page is how the window controls reach the page; tests replace it.
export const page = {
  hash: (): string => (typeof location === "undefined" ? "" : location.hash),
  search: (): string => (typeof location === "undefined" ? "" : location.search),
  open: (url: string): unknown => window.open(url, "_blank", "noopener"),
  post: postWindow as Post,
  back: () => history.back(),
  forward: () => history.forward(),
  // canGo is whether the window's history goes back and forward; null where the Navigation API is missing.
  canGo: (): { back: boolean; forward: boolean } | null => {
    const n = typeof window !== "undefined" ? (window as J).navigation : null;
    return n && "canGoBack" in n ? { back: !!n.canGoBack, forward: !!n.canGoForward } : null;
  },
};

// detailShown says the route names an item whose detail the page shows.
export const detailShown = (hash: string = nav.hash): boolean => routeItem(readRoute(hash)) !== "";

export function copyViewLink(): boolean {
  clipboard.writeText(deepLink(brand.scheme, page.hash())).then(
    () => setNotice(t("Copied a link to this view")),
    (err: Error) => setNotice(t("Could not copy: {error}", { error: err.message }), true),
  );
  return true;
}

export function detachView(): boolean {
  if (!detailShown()) return false;
  const to = detachTarget(page.hash(), insetTitleBar(page.search()));
  if (to.post) page.post(to.post);
  else page.open(to.open as string);
  return true;
}

// detailFocused says el is in the detail's key context: inside the shown detail, or anywhere in a window that
// shows a detail alone.
export function detailFocused(el: Element | null): boolean {
  return detailShown() && (detailOnly(nav.hash) || (!!el && !!el.closest && !!el.closest("#detail")));
}

// windowTitle names what the window shows: the item of the shown detail, or the current view.
export function windowTitle(): string {
  const r = readHash();
  if (r.task) return (detailState.task && detailState.task.title) || "";
  if (r.pr) {
    const row = (state.detail && state.detail.row) || rowByKey(r.pr);
    if (row) return row.title || labelOf(row);
  }
  if (r.view === HOME) return t("Home");
  if (r.view === PLAN) return t("Plan");
  if (r.view === RECENT) return t("Recent");
  const served = servedView(r.view);
  return served ? served.label : viewLabel(r.view);
}

export function tellLinkNotice(win: WinLike): void {
  if (takeLinkNotice(win)) setNotice(t("That link could not be read, so the default view opened"), true);
}

// installWindow installs the window chrome's listeners: a detached window's pin, the native drag and the
// outside links. It runs before the page follows its hash, so a detached window's pin sees each route first;
// true when the native window draws its controls over the page.
export function installWindow(win: J, doc: J, post: Post = postWindow): boolean {
  const inset = insetTitleBar(win.location.search);
  if (inset) detailWindowInstall(win, doc, post);
  installDrag(doc, win);
  installExternalLinks(doc, win, post);
  return inset;
}

// followWindowChrome keeps body's classes on the route: titlebar when the page draws the title bar, layout-detail
// when it shows a detail alone. It returns the stop.
export function followWindowChrome(body: HTMLElement, inset: boolean): () => void {
  return $effect.root(() => {
    $effect(() => {
      body.classList.toggle("titlebar", inset);
      body.classList.toggle("layout-detail", detailOnly(nav.hash));
    });
  });
}
