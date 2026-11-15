import { fireEvent, render } from "@testing-library/svelte";
import { flushSync } from "svelte";
import { beforeEach, describe, expect, test, vi } from "vitest";
import { applyPlugins, resolveView } from "../../lib/catalog";
import { I18N } from "../../lib/i18n.svelte";
import { lensOf } from "../../lib/lens";
import type { J } from "../../lib/util";
import { nav, readHash } from "../../state/route.svelte";
import { catalog, state, tasksState } from "../../state/store.svelte";
import { clone, fixtures, useCatalog } from "../../testing/chrome";
import { resetStore } from "../../testing/reset.svelte";
import { served } from "../../state/keycaps.svelte";
import { railCount, railSections, viewNav } from "./rail";
import Rail from "./Rail.svelte";

const coreViewIDs = ["focus", "inbox", "outbox", "fits-now", "waiting", "projects", "someday", "logbook"];
const coreRoutes = ["plan", "recent"];

beforeEach(() => {
  resetStore();
  served.bindings = [];
});

function railCat(mod?: (views: J[]) => void): J {
  const cat = clone(fixtures.railViews);
  if (mod) mod(cat.views);
  return cat;
}

function show(view: string, omnibox = vi.fn()) {
  nav.hash = "#view=" + view;
  const r = render(Rail, { omnibox });
  const links = () => [...r.container.querySelectorAll("#views a")].map((a) => (a.querySelector(".label") as HTMLElement).textContent);
  const focus = (slot: string) => r.container.querySelector(`[data-focus="${slot}"]`) as HTMLElement;
  return { ...r, links, focus };
}

describe("the rail", () => {
  test("folds the empty views into one entry, keeps the view shown, and opens it in place", async () => {
    tasksState.catalog = railCat();
    tasksState.counters = { views: { inbox: 3, someday: 0, logbook: 0 } };
    served.bindings = [{ action: "palette", key: "Cmd+K" }];
    const { links, focus, container } = show("someday");
    expect(links()).toEqual(["Home", "Focus", "Inbox", "Outbox", "Fits now", "Waiting", "Projects", "Someday", "Plan", "Recent"]);
    const folder = focus("rail:empties");
    expect(folder.querySelector(".label")!.textContent).toBe("Empty");
    expect(folder.querySelector(".count")!.textContent).toBe("1");
    expect(folder.getAttribute("aria-expanded")).toBe("false");
    await fireEvent.click(folder);
    expect(focus("rail:empties")).toBe(folder);
    expect(folder.getAttribute("aria-expanded")).toBe("true");
    expect(links().at(-1)).toBe("Logbook");
    expect(container.querySelector("#omnibox-entry")!.textContent).toBe("Search or ask⌘K");
  });

  test("lists Home first, marks the view shown and leaves rail = false out", () => {
    tasksState.catalog = railCat((vs) => vs.push({ id: "home-blocked", label: "Blocked by you", rail: false }));
    tasksState.counters = { views: Object.fromEntries(coreViewIDs.map((id) => [id, 1])) };
    const { links, container } = show("home");
    const home = () => container.querySelector("#views a") as HTMLAnchorElement;
    expect(links()[0]).toBe("Home");
    expect(home().getAttribute("href")).toBe("#view=home");
    expect(links()).not.toContain("Blocked by you");
    expect(home().getAttribute("aria-current")).toBe("page");
    nav.hash = "#view=inbox";
    flushSync();
    expect(home().hasAttribute("aria-current")).toBe(false);
  });

  test("leaves out the views off the rail, unless one is the view shown", () => {
    tasksState.catalog = railCat((vs) => vs.forEach((v) => (v.rail = v.id !== "someday" && v.id !== "logbook")));
    tasksState.counters = { views: { inbox: 3, someday: 2, logbook: 0 } };
    const { links, focus } = show("inbox");
    expect(focus("rail:empties")).toBeNull();
    expect(links()).not.toContain("Someday");
    expect(links()).not.toContain("Logbook");
    nav.hash = "#view=someday";
    flushSync();
    expect(links()).toContain("Someday");
    expect(links()).not.toContain("Logbook");
  });

  test("heads each plugin's section with its service's name", () => {
    tasksState.catalog = railCat();
    catalog.views = [{ id: "needs-me", label: "Needs me", provider: "github" }, { id: "slack", label: "Conversations", provider: "slack" }, { id: "all", label: "All", provider: "github" }];
    applyPlugins([{ id: "github", name: "GitHub", enabled: true }, { id: "slack", name: "Slack", enabled: true }]);
    state.rows = [{ unit: { provider: "github", key: "acme/api#1" }, views: ["needs-me", "slack"], facets: {} }];
    state.loaded = true;
    const { links, container } = show("inbox");
    expect([...container.querySelectorAll("#views .rail-head")].map((x) => x.textContent)).toEqual(["GitHub", "Slack"]);
    expect(links().slice(-3)).toEqual(["Needs me", "All", "Conversations"]);
  });

  test("counts again in place: a new counter rewords the link it had", () => {
    tasksState.catalog = railCat();
    tasksState.counters = { views: { inbox: 3 } };
    const { focus } = show("inbox");
    const inbox = focus("rail:view=inbox");
    inbox.focus();
    tasksState.counters = { views: { inbox: 4 } };
    flushSync();
    expect(focus("rail:view=inbox")).toBe(inbox);
    expect(inbox.querySelector(".count")!.textContent).toBe("4");
    expect(document.activeElement).toBe(inbox);
  });

  test("rewords itself when the strings change, without drawing its entries again", () => {
    tasksState.catalog = railCat();
    const { focus, container } = show("inbox");
    const plan = focus("rail:view=plan");
    I18N.locale = "pt-BR";
    I18N.strings = { Plan: "Plano", "Search or ask": "Buscar ou perguntar", "Waiting on": "Esperando por" };
    flushSync();
    expect(focus("rail:view=plan")).toBe(plan);
    expect(plan.querySelector(".label")!.textContent).toBe("Plano");
    expect(container.querySelector("#omnibox-entry .label")!.textContent).toBe("Buscar ou perguntar");
    expect(container.querySelector("h2.rail-head")!.textContent).toBe("Esperando por");
  });

  test("the omnibox entry opens the omnibox", async () => {
    const omnibox = vi.fn();
    const { container } = show("inbox", omnibox);
    await fireEvent.click(container.querySelector("#omnibox-entry")!);
    expect(omnibox).toHaveBeenCalledOnce();
  });

  test("the omnibox entry shows the served palette key, none when nothing is bound", () => {
    const { container } = show("inbox");
    expect(container.querySelector("#omnibox-entry kbd")).toBeNull();
    served.bindings = [{ action: "palette", key: "Ctrl+P" }];
    flushSync();
    expect(container.querySelector("#omnibox-entry kbd")!.textContent).toBe("⌃P");
  });
});

describe("the rail's entries", () => {
  test("list the core views, the plan and Recent, then the plugin views", () => {
    useCatalog({ views: false });
    const counters = { views: { focus: 1, inbox: 4, outbox: 2, "fits-now": 7, waiting: 3, projects: 0, someday: 5, logbook: 9 } as Record<string, number> };
    const all = viewNav(railCat(), catalog.views, ["fake-board"], counters);
    expect(all.map((v) => v.id)).toEqual([...coreViewIDs, ...coreRoutes, "mine", "dash", "all", "explore", "fake-board"]);
    all.forEach((v, i) => expect(v.core).toBe(i < coreViewIDs.length + coreRoutes.length));
    coreViewIDs.forEach((id, i) => expect(all[i].count).toBe(counters.views[id]));
  });

  test("drop a disabled plugin's views", () => {
    useCatalog({ views: false });
    const cat = railCat();
    expect(viewNav(cat, [], [], null).map((v) => v.id)).toEqual([...coreViewIDs, ...coreRoutes]);
    expect(resolveView("mine", cat, [], () => false)).toBe("focus");
    expect(resolveView("mine", null, [], () => false)).toBe("mine");
    expect(resolveView("mine", cat, catalog.views, () => false)).toBe("mine");
    expect(resolveView("waiting", cat, [], () => false)).toBe("waiting");
  });

  test("group the plugin views by plugin", () => {
    const servedViews = [{ id: "needs-me", label: "Needs me", provider: "github" }, { id: "slack", label: "Slack", provider: "slack" }, { id: "all", label: "All", provider: "github" }];
    const names: Record<string, string> = { github: "GitHub", slack: "Slack" };
    const secs = railSections(viewNav(railCat(), servedViews, [], null), (p) => names[p]).map((s) => ({ title: s.title, views: s.views.map((v) => v.id) }));
    expect(secs).toEqual([
      { title: "", views: [...coreViewIDs, ...coreRoutes] },
      { title: "GitHub", views: ["needs-me", "all"] },
      { title: "Slack", views: ["slack"] },
    ]);
  });

  test("hold the plan among the core views, counted, whatever plugin runs", () => {
    const cat = railCat();
    const all = viewNav(cat, [], [], null);
    const plan = all.find((v) => v.id === "plan")!;
    state.plan.items = [{ decision: "pending" }, { decision: "blocked" }, { decision: "run" }];
    state.plan.loaded = true;
    tasksState.catalog = cat;
    expect(plan.core).toBe(true);
    expect(plan.label).toBe("Plan");
    expect(resolveView("plan", cat, [], () => false)).toBe("plan");
    expect(railCount(plan, lensOf(readHash()))).toBe(2);
    expect(railSections(all, (p) => p).find((s) => s.views.some((v) => v.id === "plan"))!.title).toBe("");
  });

  test("hold Recent among the core views, uncounted, whatever plugin runs", () => {
    useCatalog({ views: false });
    const cat = railCat();
    const all = viewNav(cat, [], [], null);
    const recent = all.find((v) => v.id === "recent")!;
    tasksState.catalog = cat;
    expect(recent.core).toBe(true);
    expect(recent.label).toBe("Recent");
    expect(resolveView("recent", cat, [], () => false)).toBe("recent");
    expect(railCount(recent, lensOf(readHash()))).toBe("");
    expect(railSections(all, (p) => p).find((s) => s.views.some((v) => v.id === "recent"))!.title).toBe("");
    expect(catalog.views.some((v) => v.id === "recent")).toBe(false);
  });
});
