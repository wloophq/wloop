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
import Outbox from "./Outbox.svelte";
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

describe("the Outbox", () => {
  beforeEach(() => {
    resetStore(NOW, "#view=outbox");
    useCatalogs();
    I18N.strings = { "{n} draft": { one: "{n} draft", other: "{n} drafts" } };
  });

  it("draws one card per draft under its group", () => {
    const c = render(Outbox, { page: fixture("outbox-page.json") }).container;
    const marks = ["ob-title", "ob-n", "ob-about", "ob-workflow", "d-draft"];
    const order = Array.from(c.querySelectorAll("*")).filter((x) => marks.some((m) => x.classList.contains(m)))
      .map((x) => (x.getAttribute("data-draft") ? "card " + x.getAttribute("data-draft") : x.textContent));
    expect(order).toEqual(["Ask who hasn't answered", "2 drafts", "fake-nudge", "Drafted when a conversation where you asked something goes quiet.", "card 7", "card 8", "Written by hand", "1 draft", "card 9"]);
    expect(Array.from(c.querySelectorAll(".d-draft")).map((d) => d.querySelectorAll(".d-context").length)).toEqual([1, 0, 0]);
  });

  it("stops drafting a workflow once its inline confirm is pressed", async () => {
    const posts: J[] = [];
    net.fetch = async (url: string, init?: RequestInit) => {
      posts.push({ path: url, body: JSON.parse(String(init && init.body)) });
      return new Response("{}", { status: 200 });
    };
    const c = render(Outbox, { page: fixture("outbox-page.json") }).container;
    const buttons = () => Array.from(c.querySelectorAll(".ob-stop button")).map((b) => b.getAttribute("data-focus"));
    expect(buttons()).toEqual(["outbox:stop:fake-nudge"]);
    await fireEvent.click(c.querySelector('[data-focus="outbox:stop:fake-nudge"]')!);
    expect(buttons()).toEqual(["outbox:stop-confirm:fake-nudge", "outbox:stop-cancel:fake-nudge"]);
    expect(posts).toEqual([]);
    await fireEvent.click(c.querySelector('[data-focus="outbox:stop-cancel:fake-nudge"]')!);
    expect(buttons()).toEqual(["outbox:stop:fake-nudge"]);
    await fireEvent.click(c.querySelector('[data-focus="outbox:stop:fake-nudge"]')!);
    await fireEvent.click(c.querySelector('[data-focus="outbox:stop-confirm:fake-nudge"]')!);
    await new Promise((r) => setTimeout(r, 0));
    expect(posts).toEqual([{ path: "/api/workflows/fake-nudge/disable", body: { source: "cockpit" } }]);
    expect(outboxState.confirming).toBe("");
  });

  it("keeps each card's node across a reload", () => {
    outboxState.page = fixture("outbox-page.json");
    const c = render(Outbox, { page: outboxState.page }).container;
    const card = c.querySelector('[data-draft="8"]');
    const next = fixture("outbox-page.json");
    next.groups[0].drafts[1].text = "Still waiting?";
    outboxState.page.groups = next.groups;
    flushSync();
    expect(card).not.toBeNull();
    expect(c.querySelector('[data-draft="8"]')).toBe(card);
  });
});
