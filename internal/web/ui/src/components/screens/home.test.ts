import { cleanup, fireEvent, render } from "@testing-library/svelte";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { flushSync } from "svelte";
import { I18N } from "../../lib/i18n.svelte";
import { readRoute } from "../../lib/routes";
import type { J } from "../../lib/util";
import { net } from "../../state/api";
import { drafts, homeState, tasksState } from "../../state/store.svelte";
import { readHash } from "../../state/route.svelte";
import { resetStore } from "../../testing/reset.svelte";
import { fixture, useCatalogs } from "../rows/testdata/fixtures";
import Home from "./Home.svelte";
import { screenLens } from "./screen";

const NOW = Date.parse("2026-11-04T12:00:00Z");

afterEach(cleanup);

function draw(): HTMLElement {
  homeState.home = fixture("home.json");
  return render(Home).container as HTMLElement;
}

const links = (c: Element) => Array.from(c.querySelectorAll("a")).map((a) => ({ href: a.getAttribute("href"), text: a.textContent }));

describe("the home", () => {
  beforeEach(() => {
    resetStore(NOW, "#view=home");
    useCatalogs();
  });

  it("words its briefing with its counters as links", () => {
    const c = draw();
    expect(c.querySelector(".briefing")!.textContent).toBe("The agent has 2 questions for you and the loop handles 3 tasks. ");
    expect(links(c)[0]).toEqual({ href: "#queue=answer", text: "2 questions" });
    expect(links(c)[1]).toEqual({ href: "#view=in-loop", text: "3 tasks" });
  });

  it("shows at most each section's rows, drawn as its view, and links its whole view", () => {
    const c = draw();
    const blocked = c.querySelector('[data-section="blocked"]')!;
    expect(Array.from(blocked.querySelectorAll("[data-row]")).map((n) => n.getAttribute("data-row"))).toEqual(["task:t-1", "task:t-2", "task:t-3"]);
    expect(links(c)).toContainEqual({ href: "#view=home-blocked", text: "See all 5" });
    expect(c.querySelector('[data-section="stalled"]')!.textContent).toContain("Nothing here right now.");
    expect(links(c).some((l) => l.href === "#view=home-stalled")).toBe(false);
    expect(Array.from(c.querySelectorAll(".rows")).map((x) => x.tagName)).not.toContain("OL");
  });

  it("captures a task from its field, keeping the text in the store", async () => {
    const posts: J[] = [];
    net.fetch = async (url: string, init?: RequestInit) => {
      posts.push({ url, body: init && init.body });
      return new Response("{}", { status: 200 });
    };
    const c = draw();
    const field = c.querySelector("input.capture-input") as HTMLInputElement;
    expect(field.placeholder).toBe("Capture what's on your mind");
    await fireEvent.input(field, { target: { value: "  Call Ada " } });
    expect(drafts.capture).toBe("  Call Ada ");
    await fireEvent.keyDown(field, { key: "Enter" });
    await new Promise((r) => setTimeout(r, 0));
    flushSync();
    expect(posts).toEqual([{ url: "/api/tasks", body: JSON.stringify({ title: "Call Ada" }) }]);
    expect(field.value).toBe("");
    await fireEvent.input(field, { target: { value: "   " } });
    await fireEvent.keyDown(field, { key: "Enter" });
    expect(posts.length).toBe(1);
  });

  it("keeps the capture field's node and text across a sync", async () => {
    const c = draw();
    const field = c.querySelector("input.capture-input") as HTMLInputElement;
    await fireEvent.input(field, { target: { value: "half typed" } });
    field.focus();
    const next = fixture("home.json");
    next.sections[0].total = 9;
    Object.assign(homeState.home, next);
    flushSync();
    expect(c.querySelector("input.capture-input")).toBe(field);
    expect(field.value).toBe("half typed");
    expect(document.activeElement).toBe(field);
    expect(links(c)).toContainEqual({ href: "#view=home-blocked", text: "See all 9" });
  });

  it("says every word through the strings", () => {
    I18N.strings = new Proxy({}, { get: (_, k) => (typeof k === "string" ? "«" + k + "»" : undefined), has: () => true });
    const c = draw();
    const chrome = [c.querySelector(".see-all")!.textContent!, c.querySelector(".empty")!.textContent!, c.querySelector("input")!.getAttribute("aria-label")!];
    for (const s of chrome) expect(s.startsWith("«")).toBe(true);
  });

  it("says why it could not load, and that it is loading", () => {
    homeState.error = "Could not load the home: boom";
    expect(render(Home).container.querySelector(".err")!.textContent).toBe("Could not load the home: boom");
    cleanup();
    homeState.error = "";
    homeState.home = null;
    expect(render(Home).container.querySelector(".loading")!.textContent).toBe("Loading the home...");
  });
});

describe("the home's route", () => {
  beforeEach(() => resetStore(NOW, ""));

  it("is the default", () => {
    expect(readRoute("").view).toBe("home");
    expect(readHash().view).toBe("home");
    expect([readRoute("#task=t-1").view, readRoute("#unit=github/acme%2Fapi%238").view, readRoute("#view=inbox").view, readRoute("#queue=answer").view, readRoute("#panel=omnibox").view])
      .toEqual(["", "", "inbox", "queue", ""]);
  });

  it("reads a queue's ids", () => {
    const r = readRoute("#queue=send-draft&q=" + encodeURIComponent("where:general"));
    expect([r.queue, r.q, readRoute("#queue=list&ids=t-1,,t-2").ids, readRoute("#queue=list&ids=" + Array.from({ length: 300 }, (_, i) => "t-" + i).join(",")).ids.length])
      .toEqual(["send-draft", "where:general", ["t-1", "t-2"], 200]);
  });

  it("draws a row no view names as the profile's first view", () => {
    tasksState.catalog = { views: [{ id: "mine", label: "Mine" }, { id: "later", label: "Later" }] };
    expect([screenLens("").view, screenLens("later").view]).toEqual(["mine", "later"]);
  });
});
