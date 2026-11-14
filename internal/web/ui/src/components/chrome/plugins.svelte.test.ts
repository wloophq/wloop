import { render } from "@testing-library/svelte";
import { readFileSync } from "node:fs";
import { flushSync } from "svelte";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { applyCatalog, applyPlugins } from "../../lib/catalog";
import { fieldOf } from "../../lib/lens";
import type { J } from "../../lib/util";
import { nav } from "../../state/route.svelte";
import { state } from "../../state/store.svelte";
import { clone, fixtures, followHashes, settle, useCatalog } from "../../testing/chrome";
import { resetStore } from "../../testing/reset.svelte";
import { cockpitHost, importer, pluginKeyDown, pluginTheme, publishPlugins, showPluginView, unmountPluginView, type Host } from "./plugins.svelte";
import PluginView from "./PluginView.svelte";

const enabled = [{ id: "github", enabled: true }, { id: "fake", enabled: true, ui: { web: { module: "view.js", views: ["fake-board"] } } }];
const disabled = [{ id: "github", enabled: true }, { id: "fake", enabled: false }];

let log: string[];
let ctxKeys: string[];

function fakeView(): J {
  return {
    mount(el: HTMLElement, ctx: J) {
      log.push("mount");
      ctxKeys = Object.keys(ctx).sort().concat(Object.keys(ctx.keys).map((k) => "keys." + k));
      ctx.subscribe((rows: J[]) => log.push("rows:" + rows.length));
      ctx.keys.register("x", "Do x", () => log.push("key:x"));
      ctx.select({ unit: { provider: "fake", key: "T1/C1" } });
      ctx.act({ provider: "fake", key: "T1/C1" }, "nudge", { guidance: "hi" }).then((ok: boolean) => log.push("acted:" + ok));
      ctx.list(el, [{ label: "r" }], { empty: "none" });
      ctx.detail(el, { provider: "fake", key: "T1/C1" });
      log.push("theme:" + typeof ctx.theme);
    },
    unmount() {
      log.push("unmount");
    },
  };
}

beforeEach(() => {
  resetStore();
  followHashes();
  log = [];
});

afterEach(() => unmountPluginView());

describe("a plugin view", () => {
  test("mounts once, gets the rows as they change and its keys, and unmounts clean; a view left during its import never mounts", async () => {
    const urls: string[] = [];
    importer.load = async (u) => {
      urls.push(u);
      return fakeView();
    };
    const el = document.createElement("section");
    el.append(document.createElement("i"));
    let rows: J[] = [{ id: 1 }];
    const host: Host = {
      rows: () => rows,
      select: (x) => log.push("select:" + x.unit.key),
      act: async (u, a, i) => {
        log.push(`act:${u.key}:${a}:${i.guidance}`);
        return true;
      },
      list: (_, rs, o) => log.push(`list:${rs.length}:${o.empty}`),
      detail: (_, u) => log.push("detail:" + u.key),
    };
    applyPlugins(enabled);
    await showPluginView("fake-board", el, host);
    await showPluginView("fake-board", el, host);
    await settle();
    publishPlugins((rows = [{ id: 1 }, { id: 2 }]));
    nav.hash = "#view=inbox";
    const elsewhere = pluginKeyDown(new KeyboardEvent("keydown", { key: "x" }));
    nav.hash = "#view=fake-board";
    const handled = pluginKeyDown(new KeyboardEvent("keydown", { key: "x" }));
    unmountPluginView();
    expect(el.childNodes.length).toBe(0);
    const before = log.length;
    publishPlugins([{ id: 1 }, { id: 2 }, { id: 3 }]);
    expect(pluginKeyDown(new KeyboardEvent("keydown", { key: "x" }))).toBe(false);
    expect(log.slice(before)).toEqual([]);
    const racing = showPluginView("fake-board", el, host);
    unmountPluginView();
    await racing;
    expect(log.slice(before)).toEqual([]);
    applyPlugins(disabled);
    const loads = urls.length;
    await showPluginView("fake-board", el, host);
    expect(urls.slice(loads)).toEqual([]);

    expect(elsewhere).toBe(false);
    expect(handled).toBe(true);
    expect(log.filter((x) => x !== "acted:true")).toEqual(["mount", "rows:1", "select:T1/C1", "act:T1/C1:nudge:hi", "list:1:none", "detail:T1/C1", "theme:function", "rows:2", "key:x", "unmount"]);
    expect(log).toContain("acted:true");
    expect(ctxKeys).toEqual(["act", "detail", "keys", "list", "select", "subscribe", "theme", "keys.register"]);
    expect(urls).toEqual(["/plugins/fake/view.js", "/plugins/fake/view.js"]);
  });

  test("says why its module did not load", async () => {
    importer.load = async () => {
      throw new Error("404");
    };
    applyPlugins(enabled);
    const el = document.createElement("section");
    await showPluginView("fake-board", el, cockpitHost);
    expect(el.textContent).toBe("Could not load the fake view: 404");
  });

  test("the section mounts the view the route names and unmounts it when the route leaves", async () => {
    importer.load = async () => fakeView();
    applyPlugins(enabled);
    state.loaded = true;
    state.rows = [{ unit: { provider: "fake", key: "T1/C1" } }];
    const props = $state({ view: "fake-board" });
    const host: Host = { ...cockpitHost, select: () => {}, act: async () => true, list: () => {}, detail: () => {} };
    const { container } = render(PluginView, { get view() { return props.view; }, host });
    await settle();
    flushSync();
    const box = container.querySelector("#plugin-view") as HTMLElement;
    expect(box.hidden).toBe(false);
    expect(log.slice(0, 2)).toEqual(["mount", "rows:1"]);
    state.rows.push({ unit: { provider: "fake", key: "T1/C2" } });
    flushSync();
    expect(log).toContain("rows:2");
    props.view = "inbox";
    flushSync();
    expect(box.hidden).toBe(true);
    expect(log.at(-1)).toBe("unmount");
  });
});

describe("what a plugin view draws with the cockpit", () => {
  beforeEach(() => {
    useCatalog();
    applyPlugins([{ id: "github", name: "GitHub" }]);
    state.health = { viewer: "me" };
  });

  const row = (extra: J = {}): J => ({
    unit: { provider: "github", key: "acme/api#8" }, link: "https://github.com/acme/api/pull/8", label: "acme/api#8", title: "Retry flaky uploads", kind: "review",
    summary: { waits_on: "me", reason: "your review is requested" }, views: ["needs-me"], facets: {}, actions: [], ...extra,
  });

  test("a detail links the service out and keeps the label in, and says why in its plugin's words", () => {
    const cat = clone(fixtures.catalog);
    cat.reasons = [{ provider: "github", words: { review_requested: "revisão pedida" } }];
    applyCatalog(cat, fieldOf);
    state.rows = [row({ summary: { waits_on: "me", reason: "review requested", reason_key: "review_requested" } })];
    const el = document.createElement("div");
    cockpitHost.detail(el, state.rows[0].unit);
    const ext = el.querySelector("a.ext") as HTMLAnchorElement;
    expect([ext.getAttribute("href"), ext.textContent, ext.getAttribute("target")]).toEqual(["https://github.com/acme/api/pull/8", "GitHub", "_blank"]);
    expect(el.querySelector(".ref")!.tagName).toBe("SPAN");
    expect(el.textContent).not.toContain("https://");
    expect(el.querySelector("h2")!.textContent).toBe("Retry flaky uploads");
    expect(el.querySelector(".plugin-detail strong")!.textContent).toBe("me");
    expect(el.textContent).toContain("revisão pedida");
    expect(el.textContent).not.toContain("review requested");
  });

  test("a detail of a unit not in the rows says so; a list of none says the view's empty word", () => {
    const el = document.createElement("div");
    cockpitHost.detail(el, { provider: "github", key: "acme/api#9" });
    expect(el.textContent).toBe("acme/api#9 is not in the rows.");
    const list = document.createElement("div");
    cockpitHost.list(list, [], { empty: "Nothing here" });
    expect(list.textContent).toBe("Nothing here");
  });

  test("a list draws the cockpit's rows, and a second hand-over updates them in place", () => {
    nav.hash = "#view=fake-board";
    const r = row();
    state.rows = [r];
    const el = document.createElement("div");
    cockpitHost.list(el, [r], {});
    const box = el.querySelector(".rows.plugin-list") as HTMLElement;
    expect(box).not.toBeNull();
    const first = box.firstElementChild;
    expect(first).not.toBeNull();
    cockpitHost.list(el, [r, row({ unit: { provider: "github", key: "acme/api#9" }, label: "acme/api#9" })], {});
    flushSync();
    expect(el.querySelector(".rows.plugin-list")).toBe(box);
    expect(box.firstElementChild).toBe(first);
    expect(box.children.length).toBe(2);
  });
});

describe("the theme a plugin view gets", () => {
  test("follows the cockpit's tokens, light and dark", () => {
    const css = readFileSync("../static/app.css", "utf8");
    const token = /--([\w-]+):\s*([^;]+);/g;
    const [lightAll, rest] = css.split("@media (prefers-color-scheme: dark)");
    const lightCSS = lightAll.split(":root {")[1].split("\n}\n")[0];
    const darkCSS = rest.split("\n}\n")[0];
    const want: Record<string, Record<string, string>> = { light: {}, dark: {} };
    for (const m of lightCSS.matchAll(token)) want.light[m[1]] = want.dark[m[1]] = m[2];
    for (const m of darkCSS.matchAll(token)) want.dark[m[1]] = m[2];
    expect(Object.keys(want.light).length).toBeGreaterThanOrEqual(20);
    for (const mode of ["light", "dark"]) {
      vi.stubGlobal("matchMedia", (q: string) => ({ matches: q === "(prefers-color-scheme: dark)" && mode === "dark" }));
      vi.stubGlobal("getComputedStyle", () => ({ getPropertyValue: (n: string) => " " + (want[mode][n.slice(2)] || "") }));
      const got = pluginTheme();
      expect(got.mode).toBe(mode);
      for (const [name, v] of Object.entries(want[mode])) if (name in got.tokens) expect(got.tokens[name], name).toBe(v);
      for (const name of Object.keys(got.tokens)) expect(want[mode], name).toHaveProperty(name);
    }
    vi.unstubAllGlobals();
  });
});

describe("what a plugin view leaves behind", () => {
  test("a key or a subscription the view asks for after it was left takes nothing", async () => {
    let ctx: J = null;
    let release!: () => void;
    importer.load = async () => ({ mount: async (_el: HTMLElement, c: J) => { ctx = c; await new Promise<void>((r) => (release = r)); } });
    applyPlugins(enabled);
    nav.hash = "#view=fake-board";
    const el = document.createElement("section");
    const host: Host = { rows: () => [{ id: 1 }], select: () => {}, act: async () => true, list: () => {}, detail: () => {} };
    const mounting = showPluginView("fake-board", el, host);
    await settle();
    unmountPluginView();
    let ran = 0;
    let fed = 0;
    ctx.keys.register("r", "Refresh", () => ran++);
    ctx.subscribe(() => fed++);
    release();
    await mounting;
    nav.hash = "#view=fake-board";
    expect(pluginKeyDown(new KeyboardEvent("keydown", { key: "r" }))).toBe(false);
    publishPlugins([{ id: 2 }]);
    expect([ran, fed]).toEqual([0, 0]);
  });

  test("a subscriber gets a copy of the rows, never the store's own", () => {
    state.loaded = true;
    state.rows = [{ unit: { provider: "acme", key: "web#1" }, title: "One" }];
    const rows = cockpitHost.rows() as J[];
    rows[0].title = "Changed by the plugin";
    rows.reverse();
    expect(state.rows[0].title).toBe("One");
  });
});
