import { cleanup, fireEvent, render } from "@testing-library/svelte";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { flushSync } from "svelte";
import { I18N } from "../../lib/i18n.svelte";
import type { J } from "../../lib/util";
import { net } from "../../state/api";
import { loadReview, loadReviewStep } from "../../state/loaders.svelte";
import { nav } from "../../state/route.svelte";
import { outboxState, reviewState, tasksState } from "../../state/store.svelte";
import { resetStore } from "../../testing/reset.svelte";
import { fixture, task, useCatalogs } from "../rows/testdata/fixtures";
import TaskViewPage from "./TaskViewPage.svelte";
import { reviewKey } from "./review";

const NOW = Date.parse("2026-11-04T12:00:00Z");

afterEach(cleanup);

function reviewFixture(): J {
  const step = (id: string, label: string, view: string, count: number, done: boolean) => ({ id, label, view, query: { view }, count, done });
  return { id: "r-1", started_at: "2026-10-22T12:00:00Z", done: false, steps: [
    step("inbox", "Inbox to zero", "inbox", 2, false), step("outbox", "Outbox", "outbox", 0, true), step("waiting", "Waiting: nudge?", "waiting", 0, true),
    step("projects", "Projects without a next action or past their review", "review", 1, false), step("someday", "Someday", "someday", 0, true), step("focus", "Prune Focus", "focus", 0, true),
  ] };
}

describe("the review", () => {
  beforeEach(() => {
    resetStore(NOW, "#view=review");
    useCatalogs();
  });

  it("walks its steps, marking each done and moving on", async () => {
    const review = reviewFixture();
    const doneInbox = reviewFixture();
    doneInbox.steps[0].done = true;
    const project = task("e-p", "next", (e) => Object.assign(e, { props: { kind: "project" }, title: "Ship the v2 API", review_due: true, review_every: "168h0m0s" }));
    const gets: string[] = [], posts: string[] = [];
    net.fetch = async (url: string, init?: RequestInit) => {
      const ok = (b: J) => new Response(JSON.stringify(b), { status: 200 });
      if (init && init.method === "POST") {
        posts.push(url);
        return ok(doneInbox);
      }
      gets.push(url);
      if (url === "/api/review") return ok(review);
      if (url.includes("view=inbox")) return ok({ tasks: [task("e-1", "inbox"), task("e-2", "inbox")], counters: { views: { inbox: 2 } } });
      return ok({ tasks: [project] });
    };
    await loadReview();
    expect(reviewState.step).toBe("inbox");
    const inboxIDs = tasksState.tasks.map((e: J) => e.id);
    await reviewKey("done");
    expect(posts).toEqual(["/api/review/steps/inbox/done"]);
    expect(reviewState.step).toBe("projects");
    const moves: string[] = [];
    await reviewKey("prev");
    moves.push(reviewState.step);
    await reviewKey("prev");
    moves.push(reviewState.step);
    await reviewKey("done");
    await reviewKey("next");
    moves.push(reviewState.step);
    expect(moves).toEqual(["waiting", "outbox", "waiting"]);
    expect(gets).toEqual(expect.arrayContaining(["/api/tasks?view=inbox", "/api/tasks?view=review"]));
    reviewState.step = "projects";
    await loadReviewStep();
    expect([...tasksState.tasks.map((e: J) => e.id), ...inboxIDs]).toEqual(["e-p", "e-1", "e-2"]);
    const c = render(TaskViewPage).container;
    for (const s of ["Review", "Inbox to zero", "Prune Focus", "Ship the v2 API", "review due", "no next action", "every 7d"]) expect(c.textContent).toContain(s);
    expect(Array.from(c.querySelectorAll('[data-focus^="review:step:"]')).map((n) => n.getAttribute("data-focus"))).toEqual(["review:step:projects"]);
    expect(c.querySelector("#tasks")!.getAttribute("aria-label")).toBe("Review");
    const pick = c.querySelector('[data-focus="review:pick:inbox"]')!;
    await fireEvent.click(pick);
    await new Promise((r) => setTimeout(r, 0));
    flushSync();
    expect(reviewState.step).toBe("inbox");
    expect(c.querySelector('[data-focus="review:pick:inbox"]')).toBe(pick);
    expect(pick.getAttribute("aria-current")).toBe("step");
  });

  it("offers no filter it cannot apply", () => {
    tasksState.catalog = { ...fixture("views.json"), review: ["inbox"] };
    reviewState.review = { steps: [{ id: "inbox", view: "inbox", label: "Inbox to zero", count: 1 }] };
    reviewState.step = "inbox";
    tasksState.key = "review|inbox";
    tasksState.tasks = [task("e-1", "next", (e) => (e.facets = { source: ["slack"], state: ["next"], contexts: ["calls"] }))];
    const c = render(TaskViewPage).container;
    expect(c.querySelectorAll("button.facet, button.th-sort").length).toBe(0);
    expect(c.textContent).toContain("@calls");
  });

  it("forgets its step off its route", () => {
    reviewState.step = "inbox";
    nav.hash = "#view=inbox";
    tasksState.catalog = fixture("views.json");
    render(TaskViewPage);
    flushSync();
    expect(reviewState.step).toBe("");
  });
});

describe("the Review's Outbox step", () => {
  beforeEach(() => {
    resetStore(NOW, "#view=review");
    const views = fixture("views.json");
    views.views = views.views.map((v: J) => (v.id === "outbox" ? { ...v, layout: "outbox" } : v));
    useCatalogs(fixture("catalog.github.json"), views);
    I18N.strings = { "{n} draft": { one: "{n} draft", other: "{n} drafts" } };
    reviewState.review = { steps: [{ id: "outbox", view: "outbox", label: "Outbox", count: 4, done: false }] };
    reviewState.step = "outbox";
  });

  it("reads the Outbox with the step's tasks", async () => {
    const gets: string[] = [];
    net.fetch = async (url: string) => {
      gets.push(url);
      return new Response(JSON.stringify(url.startsWith("/api/outbox") ? fixture("outbox-page.json") : { tasks: [] }), { status: 200 });
    };
    await loadReviewStep();
    expect(gets).toEqual(["/api/tasks?view=outbox", "/api/outbox?view=outbox"]);
    expect(outboxState.page.drafts.map((d: J) => d.id)).toEqual([7, 8, 9, 10]);
  });

  it("draws the Outbox's list, grouped by who proposed each draft", () => {
    tasksState.key = "review|outbox";
    tasksState.tasks = [];
    outboxState.page = fixture("outbox-page.json");
    const c = render(TaskViewPage).container;
    const list = c.querySelector("#tasks-body .outbox")!;
    expect(Array.from(list.querySelectorAll(".ob-title")).map((n) => n.textContent)).toEqual(["Reviews", "Written by hand", "Ask who hasn't answered"]);
    expect(Array.from(list.querySelectorAll("[data-draft]")).map((n) => n.getAttribute("data-draft"))).toEqual(["10", "9", "7", "8"]);
    expect(list.querySelectorAll(".ob-stop button").length).toBe(1);
  });
});
