"use strict";

// The cockpit's window chrome: the title bar it draws when the native window
// marks its URL, the link that copies the current view, and the detach of an
// item's detail into a window of its own.

const DETAIL_LAYOUT = "layout=detail";

// insetTitleBar says the native window draws its controls over the page,
// which then draws the title bar around them.
function insetTitleBar(search) {
  return new URLSearchParams(String(search || "")).get("titlebar") === "inset";
}

const detailOnly = (hash) => readRoute(hash).layout === "detail";

// linkRoute is the route of hash without what only one window shows: the
// detail layout, the panel and a link's notice.
function linkRoute(hash) {
  return String(hash || "").replace(/^#/, "").split("&")
    .filter((p) => p && p !== DETAIL_LAYOUT && !/^(panel|link)=/.test(p)).join("&");
}

// deepLink opens the route of hash in the app; it carries no port and no token.
function deepLink(scheme, hash) {
  return scheme + "://open/#" + linkRoute(hash);
}

// detachTarget is how the item hash shows opens alone: a request to the
// native window, or, in a browser, a tab on the detail layout.
function detachTarget(hash, native) {
  const route = linkRoute(hash);
  return native ? { post: { action: "detach", route } } : { open: "#" + (route ? route + "&" : "") + DETAIL_LAYOUT };
}

// takeLinkNotice drops the mark of a link that could not be read from win's
// route; true when there was one to tell about.
function takeLinkNotice(win) {
  const parts = String(win.location.hash || "").replace(/^#/, "").split("&");
  if (!parts.some((p) => p.startsWith("link="))) return false;
  win.history.replaceState(null, "", "#" + parts.filter((p) => !p.startsWith("link=")).join("&"));
  return true;
}

// installDrag moves the native window from any element whose
// --wails-draggable is drag, as Wails' own runtime does, through the bridge
// the native window injects; false in a browser.
function installDrag(doc, win) {
  const bridge = win.webkit && win.webkit.messageHandlers && win.webkit.messageHandlers.external;
  if (!bridge) return false;
  const drags = (el) => !!el && win.getComputedStyle(el).getPropertyValue("--wails-draggable").trim() === "drag";
  let armed = false;
  doc.addEventListener("mousedown", (e) => { armed = e.button === 0 && e.detail === 1 && drags(e.target); });
  doc.addEventListener("mousemove", (e) => {
    if (!armed) return;
    armed = false;
    if (e.buttons === 1) bridge.postMessage("wails:drag");
  });
  doc.addEventListener("mouseup", () => { armed = false; });
  doc.addEventListener("dblclick", (e) => {
    if (drags(e.target)) bridge.postMessage("wails:drag:doubleclick");
  });
  return true;
}

// keyHint is label with the key the served bindings give action.
function keyHint(label, action) {
  const keys = typeof cockpitKeys !== "undefined" ? cockpitKeys : null;
  const b = action && keys ? keys.bindings().find((x) => x.action === action) : null;
  return b ? t("{action} ({key})", { action: label, key: b.key }) : label;
}

// iconButton draws the icon app.css names by data-icon.
function iconButton(name, label, focus, run, action) {
  return h("button", { type: "button", class: "icon-btn", "data-icon": name, title: keyHint(label, action), "aria-label": label, "data-focus": focus, "data-key-action": action, onclick: run });
}

const linkScheme = () => (document.querySelector('meta[name="link-scheme"]') || {}).content || "";
const detailShown = () => {
  const box = document.getElementById("detail");
  return !!box && !box.hidden;
};

function copyViewLink() {
  navigator.clipboard.writeText(deepLink(linkScheme(), location.hash)).then(
    () => setNotice(t("Copied a link to this view")),
    (err) => setNotice(t("Could not copy: {error}", { error: err.message }), true));
  return true;
}

function detachView() {
  if (!detailShown()) return false;
  const to = detachTarget(location.hash, insetTitleBar(location.search));
  if (to.post) postWindow(to.post);
  else window.open(to.open, "_blank", "noopener");
  return true;
}

// detailFocused says el is in the detail's key context: inside the shown
// detail, or anywhere in a window that shows a detail alone.
function detailFocused(el) {
  return detailShown() && (detailOnly(location.hash) || (!!el && !!el.closest && !!el.closest("#detail")));
}

// pageHash is the page's route; a detail drawn with no page, as the tests
// draw it, has none.
const pageHash = () => (typeof location === "undefined" ? "" : location.hash);

// detailWindowControls are the detail's own window controls: detach, unless
// it is alone already, and copy its link.
function detailWindowControls() {
  return h("span", { class: "d-window" },
    detailOnly(pageHash()) ? null : iconButton("detach", t("Open in a window of its own"), "detail:detach", () => detachView(), "detail:detach"),
    iconButton("link", t("Copy a link to this view"), "detail:copy-link", () => copyViewLink(), "link:copy"));
}

// windowTitle names what the window shows: the item of the shown detail, or
// the current view.
function windowTitle(doc) {
  const box = doc.getElementById("detail");
  const head = box && !box.hidden ? box.querySelector(".d-head h1") : null;
  const text = head ? head.textContent.replace(/^★\s*/, "").trim() : "";
  if (text) return text;
  const view = doc.querySelector('#rail [aria-current="page"] .label');
  return view ? view.textContent.trim() : "";
}

function installTitleBar(doc) {
  const top = doc.getElementById("top");
  if (!top) return;
  doc.body.classList.add("titlebar");
  top.prepend(
    h("span", { class: "tb-nav" },
      iconButton("back", t("Back"), "top:back", () => history.back()),
      iconButton("forward", t("Forward"), "top:forward", () => history.forward())),
    h("span", { id: "window-title", class: "tb-title" }));
  top.append(h("span", { class: "tb-tools" },
    iconButton("link", t("Copy a link to this view"), "top:copy-link", () => copyViewLink(), "link:copy"),
    iconButton("search", t("Open the omnibox"), "top:omnibox", () => openOmnibox())));
}

// syncWindowChrome follows the route and the rendered page: the detail
// layout, the title, the history buttons and the key hints.
function syncWindowChrome() {
  document.body.classList.toggle("layout-detail", detailOnly(location.hash));
  const title = document.getElementById("window-title");
  if (!title) return;
  title.textContent = windowTitle(document);
  const nav = window.navigation;
  if (nav && "canGoBack" in nav) {
    document.querySelector('[data-focus="top:back"]').disabled = !nav.canGoBack;
    document.querySelector('[data-focus="top:forward"]').disabled = !nav.canGoForward;
  }
  for (const b of document.querySelectorAll("#top [data-key-action]")) b.title = keyHint(b.getAttribute("aria-label"), b.dataset.keyAction);
}

function tellLinkNotice() {
  if (takeLinkNotice(window)) setNotice(t("That link could not be read, so the default view opened"), true);
}

// The chrome installs while the page parses, before the cockpit's own
// listeners, so a detached window's guard sees each route first.
if (typeof document !== "undefined" && document.readyState === "loading") {
  if (insetTitleBar(location.search)) {
    detailWindowInstall(window, document, postWindow);
    installDrag(document, window);
  }
  window.addEventListener("hashchange", tellLinkNotice);
  document.addEventListener("DOMContentLoaded", () => {
    if (insetTitleBar(location.search)) installTitleBar(document);
    tellLinkNotice();
    syncWindowChrome();
  });
}
