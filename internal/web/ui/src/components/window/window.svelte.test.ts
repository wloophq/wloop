import { fireEvent, render } from "@testing-library/svelte";
import { flushSync } from "svelte";
import { beforeEach, describe, expect, test, vi } from "vitest";
import { brand } from "../../brand";
import { I18N } from "../../lib/i18n.svelte";
import type { J } from "../../lib/util";
import { clipboard } from "../../state/actions.svelte";
import { nav } from "../../state/route.svelte";
import { detailState, notice, tasksState } from "../../state/store.svelte";
import { clone, fixtures, settle } from "../../testing/chrome";
import { resetStore } from "../../testing/reset.svelte";
import { served } from "../../state/keycaps.svelte";
import DetailWindowControls from "./DetailWindowControls.svelte";
import { detailWindowInstall, panelInstall } from "./panel";
import TitleBar from "./TitleBar.svelte";
import {
  copyViewLink, deepLink, detachTarget, detachView, detailFocused, followWindowChrome, installDrag, installExternalLinks, insetTitleBar, page, takeLinkNotice, tellLinkNotice,
} from "./window.svelte";

// target is a window or document that keeps its listeners and runs them in order, stopping where a listener
// stops the event.
function target(): J {
  const ls: Record<string, ((e: J) => void)[]> = {};
  return {
    ls,
    addEventListener(type: string, fn: (e: J) => void) {
      (ls[type] ||= []).push(fn);
    },
    dispatch(type: string, e: J) {
      for (const fn of ls[type] || []) {
        fn(e);
        if (e.stopped) break;
      }
    },
  };
}

function event(extra: J = {}): J {
  return {
    defaultPrevented: false, stopped: false,
    preventDefault() {
      this.defaultPrevented = true;
    },
    stopImmediatePropagation() {
      this.stopped = true;
    },
    ...extra,
  };
}

const link = (href: string) => ({ closest: (sel: string) => (sel === 'a[href^="#"]' ? { getAttribute: () => href } : null) });

let posted: J[];
let appSaw: number;
let win: J;
let doc: J;
const nativeClose = () => {};

beforeEach(() => {
  resetStore();
  served.bindings = [];
  posted = [];
  appSaw = 0;
  win = target();
  win.location = { hash: "" };
  win.history = { replaceState: (_: unknown, __: string, h: string) => (win.location.hash = h) };
  win.close = nativeClose;
  doc = target();
});

// start pins the panel as the page boots, then adds the cockpit's own hashchange listener after it.
function start(install: (w: J, d: J, post: (b: J) => void) => boolean, hash: string): boolean {
  win.location.hash = hash;
  const installed = install(win, doc, (b) => posted.push(b));
  win.addEventListener("hashchange", () => appSaw++);
  return installed;
}

describe("the omnibox panel", () => {
  test("hands a link without panel to the main window and stays", () => {
    start(panelInstall, "#panel=omnibox");
    const e = event({ target: link("#task=t-1") });
    doc.dispatch("click", e);
    expect(e.defaultPrevented).toBe(true);
    expect(posted).toEqual([{ action: "open", route: "task=t-1" }]);
    expect(win.location.hash).toBe("#panel=omnibox");
  });

  test("keeps a link with panel", () => {
    start(panelInstall, "#panel=omnibox");
    const e = event({ target: link("#panel=omnibox&q=x") });
    doc.dispatch("click", e);
    expect(e.defaultPrevented).toBe(false);
    expect(posted).toEqual([]);
  });

  test("hands a hash change over, restores its own and the cockpit never sees it", () => {
    start(panelInstall, "#panel=omnibox&q=ab");
    win.location.hash = "#view=needs-me";
    win.dispatch("hashchange", event());
    expect(posted).toEqual([{ action: "open", route: "view=needs-me" }]);
    expect(win.location.hash).toBe("#panel=omnibox&q=ab");
    expect(appSaw).toBe(0);
  });

  test("lets a change within the panel reach the cockpit and restores the latest", () => {
    start(panelInstall, "#panel=omnibox");
    win.location.hash = "#panel=omnibox&q=ab";
    win.dispatch("hashchange", event());
    win.location.hash = "#task=t-2";
    win.dispatch("hashchange", event());
    expect(appSaw).toBe(1);
    expect(win.location.hash).toBe("#panel=omnibox&q=ab");
  });

  test("hides on window.close()", () => {
    start(panelInstall, "#panel=omnibox");
    win.close();
    expect(posted).toEqual([{ action: "hide-panel" }]);
  });

  test("outside it nothing is intercepted", () => {
    const installed = start(panelInstall, "#view=inbox");
    const e = event({ target: link("#task=t-1") });
    doc.dispatch("click", e);
    expect([installed, win.close !== nativeClose, e.defaultPrevented, posted]).toEqual([false, false, false, []]);
  });
});

describe("a detached window", () => {
  test("hands another item to the main window", () => {
    const installed = start(detailWindowInstall, "#task=t-1&layout=detail");
    const e = event({ target: link("#task=t-2&layout=detail") });
    doc.dispatch("click", e);
    win.location.hash = "#unit=github/acme%2Fapi%238&layout=detail";
    win.dispatch("hashchange", event());
    win.location.hash = "#view=inbox";
    win.dispatch("hashchange", event());
    expect(installed).toBe(true);
    expect(e.defaultPrevented).toBe(true);
    expect(posted).toEqual([
      { action: "open", route: "task=t-2&layout=detail" },
      { action: "open", route: "unit=github/acme%2Fapi%238&layout=detail" },
      { action: "open", route: "view=inbox" },
    ]);
    expect(win.location.hash).toBe("#task=t-1&layout=detail");
    expect(appSaw).toBe(0);
  });

  test("keeps moves within its item", () => {
    start(detailWindowInstall, "#task=t-1&layout=detail");
    const e = event({ target: link("#task=t-1&layout=detail&ctx=home") });
    doc.dispatch("click", e);
    win.location.hash = "#task=t-1&layout=detail&ctx=home";
    win.dispatch("hashchange", event());
    expect([e.defaultPrevented, posted, appSaw]).toEqual([false, [], 1]);
  });

  test("is only a detail layout route with an item", () => {
    for (const h of ["#task=t-1", "#view=inbox&layout=detail", "#view=inbox&layout=board"]) {
      win.location.hash = h;
      expect(detailWindowInstall(win, target(), () => {}), h).toBe(false);
    }
  });
});

describe("links and detach", () => {
  test("a link carries the route without what only a window shows", () => {
    expect([
      deepLink("loop", "#view=inbox&task=t-1"), deepLink("loop", "#task=t-1&layout=detail"), deepLink("loop", "#view=inbox&layout=board"),
      deepLink("loop", "#panel=omnibox&q=ab"), deepLink("loop", "#link=malformed"), deepLink("loop", ""),
    ]).toEqual(["loop://open/#view=inbox&task=t-1", "loop://open/#task=t-1", "loop://open/#view=inbox&layout=board", "loop://open/#q=ab", "loop://open/#", "loop://open/#"]);
  });

  test("copying a link uses the brand's scheme and says so", async () => {
    const copied: string[] = [];
    clipboard.writeText = async (s) => {
      copied.push(s);
    };
    page.hash = () => "#view=inbox&task=t-1&layout=detail";
    copyViewLink();
    await settle();
    expect(copied).toEqual([`${brand.scheme}://open/#view=inbox&task=t-1`]);
    expect(notice.text).toBe("Copied a link to this view");
  });

  test("detach asks the native window, or opens a tab in a browser", () => {
    expect([detachTarget("#view=inbox&task=t-1", true), detachTarget("#view=inbox&task=t-1", false), detachTarget("#task=t-1&layout=detail", false)]).toEqual([
      { post: { action: "detach", route: "view=inbox&task=t-1" } }, { open: "#view=inbox&task=t-1&layout=detail" }, { open: "#task=t-1&layout=detail" },
    ]);
    const posts: J[] = [];
    const opened: string[] = [];
    page.post = (b) => posts.push(b);
    page.open = (u) => opened.push(u);
    page.hash = () => "#view=inbox&task=t-1";
    nav.hash = "#view=inbox";
    expect(detachView()).toBe(false);
    nav.hash = "#view=inbox&task=t-1";
    page.search = () => "?titlebar=inset";
    expect(detachView()).toBe(true);
    page.search = () => "";
    detachView();
    expect(posts).toEqual([{ action: "detach", route: "view=inbox&task=t-1" }]);
    expect(opened).toEqual(["#view=inbox&task=t-1&layout=detail"]);
  });

  test("only the host marks the inset title bar", () => {
    expect([insetTitleBar("?titlebar=inset"), insetTitleBar("?x=1&titlebar=inset"), insetTitleBar(""), insetTitleBar("?titlebar=other")]).toEqual([true, true, false, false]);
  });

  test("a malformed link's notice is taken once, and told in the strings", () => {
    win.location.hash = "#link=malformed&view=inbox";
    expect(takeLinkNotice(win)).toBe(true);
    expect(takeLinkNotice(win)).toBe(false);
    expect(win.location.hash).toBe("#view=inbox");
    expect(nav.hash).toBe("#view=inbox");
    I18N.strings = { "That link could not be read, so the default view opened": "Esse link não pôde ser lido" };
    win.location.hash = "#link=malformed";
    tellLinkNotice(win);
    expect([notice.text, notice.err]).toEqual(["Esse link não pôde ser lido", true]);
  });

  test("the detail's key context is the shown detail, or the whole of a window that shows it alone", () => {
    const inside = document.createElement("aside");
    inside.id = "detail";
    const b = document.createElement("button");
    inside.append(b);
    const out = document.createElement("button");
    nav.hash = "#view=inbox";
    expect(detailFocused(b)).toBe(false);
    nav.hash = "#view=inbox&task=t-1";
    expect([detailFocused(b), detailFocused(out)]).toEqual([true, false]);
    nav.hash = "#task=t-1&layout=detail";
    expect(detailFocused(out)).toBe(true);
  });
});

describe("the native window", () => {
  const sent: string[] = [];
  const el = (drag: string) => ({ drag });
  const host = () => ({
    webkit: { messageHandlers: { external: { postMessage: (m: string) => sent.push(m) } } },
    getComputedStyle: (n: J) => ({ getPropertyValue: (p: string) => (p === "--wails-draggable" ? n.drag || "" : "") }),
  });

  test("the title bar drags the window and its controls do not", () => {
    sent.length = 0;
    const page = target();
    expect(installDrag(page, host())).toBe(true);
    page.dispatch("mousedown", event({ button: 0, detail: 1, target: el("no-drag") }));
    page.dispatch("mousemove", event({ buttons: 1, target: el("no-drag") }));
    page.dispatch("mousedown", event({ button: 0, detail: 1, target: el("drag") }));
    page.dispatch("mousemove", event({ buttons: 1, target: el("drag") }));
    page.dispatch("mousemove", event({ buttons: 1, target: el("drag") }));
    page.dispatch("mousedown", event({ button: 2, detail: 1, target: el("drag") }));
    page.dispatch("mousemove", event({ buttons: 2, target: el("drag") }));
    page.dispatch("dblclick", event({ target: el("drag") }));
    page.dispatch("dblclick", event({ target: el("") }));
    expect(sent).toEqual(["wails:drag", "wails:drag:doubleclick"]);
  });

  test("without the bridge nothing drags", () => {
    expect(installDrag(target(), { getComputedStyle: host().getComputedStyle })).toBe(false);
  });

  test("an outside link, or a window.open of one, opens in the browser", () => {
    const out: J[] = [];
    const opened: string[] = [];
    const anchor = (href: string, tgt = "") => ({ closest: (sel: string) => (sel === "a[href]" ? { href, target: tgt } : null) });
    const pg = target();
    const h: J = { webkit: { messageHandlers: { external: { postMessage() {} } } }, location: { origin: "http://127.0.0.1:5000" }, open: (u: string) => (opened.push(u), null) };
    expect(installExternalLinks(pg, h, (b) => out.push(b))).toBe(true);
    const clicks = [anchor("https://github.com/acme/api/pull/7", "_blank"), anchor("http://127.0.0.1:5000/#task=t-1", "_blank"), anchor("javascript:alert(1)", "_blank"), anchor("https://example.com/doc")];
    const prevented = clicks.map((a) => {
      const e = event({ target: a, button: 0 });
      pg.dispatch("click", e);
      return e.defaultPrevented;
    });
    h.open("https://slack.com/archives/C1", "_blank", "noopener");
    h.open("#view=inbox", "_blank");
    expect(installExternalLinks(target(), { location: { origin: "http://127.0.0.1:5000" } }, () => {})).toBe(false);
    expect(out).toEqual([
      { action: "external", url: "https://github.com/acme/api/pull/7" },
      { action: "external", url: "https://example.com/doc" },
      { action: "external", url: "https://slack.com/archives/C1" },
    ]);
    expect(prevented).toEqual([true, false, false, true]);
    expect(opened).toEqual(["#view=inbox"]);
  });
});

describe("the window chrome", () => {
  test("the title bar follows the locale, its buttons in place", () => {
    const start = render(TitleBar, { part: "start", omnibox: () => {} });
    const end = render(TitleBar, { part: "end", omnibox: () => {} });
    const buttons = () => [...start.container.querySelectorAll("button"), ...end.container.querySelectorAll("button")];
    const before = buttons();
    served.bindings = [{ action: "link:copy", key: "Cmd+Shift+C" }];
    I18N.locale = "pt-BR";
    I18N.strings = { Back: "Voltar", Forward: "Avançar", "Copy a link to this view": "Copiar um link para esta visão", "Open the omnibox": "Abrir a omnibox" };
    flushSync();
    expect(buttons()).toEqual(before);
    const got = Object.fromEntries(before.map((b) => [b.dataset.focus, [b.getAttribute("aria-label"), b.getAttribute("title")]]));
    expect(got).toEqual({
      "top:back": ["Voltar", "Voltar"], "top:forward": ["Avançar", "Avançar"],
      "top:copy-link": ["Copiar um link para esta visão", "Copiar um link para esta visão (Cmd+Shift+C)"], "top:omnibox": ["Abrir a omnibox", "Abrir a omnibox"],
    });
  });

  test("the title names the shown task, else the view, and the history buttons follow the route", async () => {
    let can = { back: false, forward: false };
    page.canGo = () => can;
    const back = vi.fn();
    page.back = back;
    tasksState.catalog = clone(fixtures.railViews);
    nav.hash = "#view=inbox";
    const { container } = render(TitleBar, { part: "start", omnibox: () => {} });
    const title = container.querySelector("#window-title")!;
    const b = container.querySelector('[data-focus="top:back"]') as HTMLButtonElement;
    expect(title.textContent).toBe("Inbox");
    expect(b.disabled).toBe(true);
    can = { back: true, forward: false };
    detailState.task = { id: "t-1", title: "Answer Ana" };
    nav.hash = "#view=inbox&task=t-1";
    flushSync();
    expect(container.querySelector("#window-title")).toBe(title);
    expect(title.textContent).toBe("Answer Ana");
    expect(b.disabled).toBe(false);
    await fireEvent.click(b);
    expect(back).toHaveBeenCalledOnce();
  });

  test("the detail's controls detach unless the detail is alone, and copy its link", () => {
    nav.hash = "#view=inbox&task=t-1";
    const { container } = render(DetailWindowControls);
    const slots = () => [...container.querySelectorAll(".d-window button")].map((b) => (b as HTMLElement).dataset.focus);
    expect(slots()).toEqual(["detail:detach", "detail:copy-link"]);
    nav.hash = "#task=t-1&layout=detail";
    flushSync();
    expect(slots()).toEqual(["detail:copy-link"]);
  });

  test("the body's classes follow the route", () => {
    const body = document.createElement("body");
    nav.hash = "#view=inbox";
    const stop = followWindowChrome(body, true);
    flushSync();
    expect(body.className).toBe("titlebar");
    nav.hash = "#task=t-1&layout=detail";
    flushSync();
    expect(body.classList.contains("layout-detail")).toBe(true);
    stop();
  });
});
