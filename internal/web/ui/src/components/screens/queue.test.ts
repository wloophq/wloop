import { cleanup, fireEvent, render } from "@testing-library/svelte";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { flushSync } from "svelte";
import { readRoute, unitPath, type Unit } from "../../lib/routes";
import type { J } from "../../lib/util";
import { net } from "../../state/api";
import { queueLoad } from "../../state/loaders.svelte";
import { nav } from "../../state/route.svelte";
import { notice, queueState } from "../../state/store.svelte";
import { resetStore } from "../../testing/reset.svelte";
import { fixture, useCatalogs } from "../rows/testdata/fixtures";
import Queue from "./Queue.svelte";
import { queueAct, queueDeps, queueKeyAction } from "./queue";

const NOW = Date.parse("2026-11-04T12:00:00Z");

const log = { gets: [] as string[], posts: [] as string[], bodies: [] as J[], opened: [] as string[], acts: [] as string[], nav: [] as string[] };
let input: { queues?: Record<string, J>; tasks?: Record<string, J>; refuse?: string[] } = {};

const saved = { ...queueDeps };

afterEach(() => {
  cleanup();
  Object.assign(queueDeps, saved);
});

beforeEach(() => {
  resetStore(NOW);
  useCatalogs(fixture("catalog.github.json"), fixture("views.json"));
  Object.assign(log, { gets: [], posts: [], bodies: [], opened: [], acts: [], nav: [] });
  input = {};
  net.fetch = async (url: string, init?: RequestInit) => {
    const ok = (body: J) => new Response(JSON.stringify(body), { status: 200 });
    if (init && init.method === "POST") {
      log.posts.push(url);
      log.bodies.push(JSON.parse(String(init.body)));
      return (input.refuse || []).includes(url) ? new Response(JSON.stringify({ error: "it changed" }), { status: 409 }) : ok({});
    }
    log.gets.push(url);
    if (url.startsWith("/api/tasks/")) {
      const e = (input.tasks || {})[decodeURIComponent(url.slice("/api/tasks/".length))];
      return e ? ok(e) : new Response("{}", { status: 404 });
    }
    const q = (input.queues || {})[decodeURIComponent(url.split("/")[3].split("?")[0])];
    return q ? ok(structuredClone(q)) : new Response("{}", { status: 404 });
  };
  queueDeps.open = (u: string) => log.opened.push(u);
  queueDeps.act = async (u: Unit, id: string) => {
    log.acts.push(unitPath(u, id));
    if ((input.refuse || []).includes(unitPath(u, id))) throw new Error("it changed");
    return true;
  };
  queueDeps.keyOf = (a: string) => ({ "queue:primary": "Enter", "queue:edit": "e", "queue:discard": "x", "queue:close-silently": "m", "queue:skip-workflow": "n", "queue:skip": "j", "queue:back": "k" } as Record<string, string>)[a] || "";
  queueDeps.navigate = (v: string) => log.nav.push(v);
});

function situationItem(id: string, title: string): J {
  return { task: { id, title, units: [{ provider: "alpha", key: "acme/api#" + id }], cards: [{ card: { unit: { provider: "alpha", key: "acme/api#" + id }, link: "https://alpha.test/" + id } }] },
    situation: { provider: "alpha", kind: "merge", label: "can merge" }, primary: { kind: "open", label: "Open at source" } };
}

function threeQueue(): J {
  return { kind: "last-step", title: "Last steps", empty: "Nothing waits.", total: 3, items: [situationItem("t-1", "one"), situationItem("t-2", "two"), situationItem("t-3", "three")] };
}

async function open(hash: string) {
  nav.hash = hash;
  await queueLoad(readRoute(nav.hash));
  return render(Queue).container as HTMLElement;
}

const tick = () => new Promise((r) => setTimeout(r, 5));
const rows = (c: Element) => Array.from(c.querySelectorAll("[data-row]")).map((n) => n.getAttribute("data-row"));
const progress = (c: Element) => c.querySelector(".progress")!.textContent;

describe("a decision queue", () => {
  it("shows one item with its progress, its primary and the next one up", async () => {
    input.queues = { "last-step": threeQueue() };
    const c = await open("#queue=last-step");
    expect(progress(c)).toBe("1 of 3");
    expect(rows(c)).toEqual(["task:t-1", "task:t-2"]);
    expect(c.querySelector(".next-up")!.textContent).toContain("+1 more");
    expect(c.querySelector(".queue-primary")!.textContent).toContain("Open at source");
  });

  it("runs a gate's primary once on Enter, then reloads", async () => {
    const gateItem = { task: { id: "t-1", title: "ship", units: [] }, gate: { id: 7, kind: "question", text: "Ship it?", intents: [{ id: "approve", label: "Approve" }, { id: "dismiss", label: "Dismiss" }] },
      primary: { kind: "gate", id: "approve", label: "Approve" } };
    input.queues = { answer: { kind: "answer", title: "Agent questions", total: 1, items: [gateItem] } };
    await open("#queue=answer");
    queueKeyAction("queue:primary");
    queueKeyAction("queue:primary");
    await tick();
    expect(log.posts).toEqual(["/api/gates/7/approve"]);
    expect(log.gets.length).toBe(2);
  });

  it("opens an item at its source", async () => {
    input.queues = { "last-step": threeQueue() };
    await open("#queue=last-step");
    await queueAct(queueState.queue.items[0]);
    expect(log.opened).toEqual(["https://alpha.test/t-1"]);
    expect(log.posts).toEqual([]);
  });

  it("skips and goes back, staying inside the queue, its buttons off at either end", async () => {
    input.queues = { "last-step": threeQueue() };
    const c = await open("#queue=last-step");
    const back = c.querySelector('[data-focus="queue:back"]') as HTMLButtonElement;
    const skip = c.querySelector('[data-focus="queue:skip"]') as HTMLButtonElement;
    expect([back.disabled, skip.disabled]).toEqual([true, false]);
    expect(back.textContent).toContain("k");
    expect(skip.textContent).toContain("j");
    await fireEvent.click(skip);
    expect(progress(c)).toBe("2 of 3");
    queueKeyAction("queue:back");
    flushSync();
    expect(progress(c)).toBe("1 of 3");
    for (let i = 0; i < 5; i++) queueKeyAction("queue:skip");
    flushSync();
    expect(progress(c)).toBe("3 of 3");
    expect([back.disabled, skip.disabled]).toEqual([false, true]);
  });

  it("shows why a stale item's action was refused, reloading, running nothing twice", async () => {
    const item = { task: { id: "t-1", title: "ship", units: [{ provider: "alpha", key: "acme/api#1" }] }, situation: { provider: "alpha", kind: "no_reviewer", label: "no reviewer" },
      primary: { kind: "action", id: "request-review", label: "Request a review" } };
    input.queues = { "pick-someone": { kind: "pick-someone", title: "Pick someone", total: 1, items: [item] } };
    input.refuse = ["/api/units/alpha/acme%2Fapi%231/request-review"];
    const c = await open("#queue=pick-someone");
    await queueAct(queueState.queue.items[0]);
    flushSync();
    expect(log.acts.length).toBe(1);
    expect(log.posts).toEqual([]);
    expect(log.gets.length).toBe(2);
    expect(c.querySelector(".err")!.textContent).toContain("it changed");
  });

  it("runs an action primary through the cockpit's action path", async () => {
    const item = { task: { id: "t-1", title: "ship", units: [{ provider: "alpha", key: "acme/api#1" }] }, situation: { provider: "alpha", kind: "stalled", label: "stalled" },
      primary: { kind: "action", id: "take_over", label: "Take over" } };
    input.queues = { "last-step": { kind: "last-step", title: "Last steps", total: 1, items: [item] } };
    await open("#queue=last-step");
    queueKeyAction("queue:primary");
    await tick();
    expect(log.acts).toEqual(["/api/units/alpha/acme%2Fapi%231/take_over"]);
    expect(log.posts).toEqual([]);
  });

  it("approves the draft revision shown on Enter, with one primary on screen", async () => {
    const draft = { id: 4, gate: 7, rev: 3, text: "Thanks, merged.", delivery: "reply", unit: "alpha:acme/api#1", intents: { approved: "approve" } };
    const item = { task: { id: "t-1", title: "ship", units: [] }, gate: { id: 7, kind: "draft", delivery: "held", subject: draft, intents: [{ id: "approve", label: "Send" }] },
      primary: { kind: "gate", id: "approve", label: "Send" } };
    input.queues = { "send-draft": { kind: "send-draft", title: "Send drafts", total: 1, items: [item] } };
    const c = await open("#queue=send-draft");
    expect(Array.from(c.querySelectorAll("kbd")).filter((k) => k.textContent === "↵").length).toBe(1);
    expect(c.querySelectorAll("button.primary").length).toBe(1);
    queueKeyAction("queue:primary");
    await tick();
    expect(log.posts).toEqual(["/api/gates/7/approve"]);
    expect(log.bodies[0].args).toEqual({ rev: "3" });
  });

  it("runs nothing and says why when a queued draft's primary is off", async () => {
    const d = { id: 4, gate: 7, rev: 3, text: "Thanks.", state: "held", intents: { approved: "approve" } };
    const e = { id: "draft:4", title: "reply", units: [], gates: [{ id: 7, kind: "draft", subject: d }], verbs: [{ verb: "approve", enabled: false, reason: "the conversation is closed" }] };
    input.queues = { "send-draft": { kind: "send-draft", title: "Send drafts", total: 1, items: [{ task: e, gate: { id: 7, kind: "draft", delivery: "held", subject: d, intents: [{ id: "approve", label: "Send" }] },
      primary: { kind: "gate", id: "approve", label: "Send" } }] } };
    await open("#queue=send-draft");
    queueKeyAction("queue:primary");
    await tick();
    expect(log.posts).toEqual([]);
    expect(notice.text).toContain("the conversation is closed");
  });

  it("says what an empty queue says, with the way home", () => {
    nav.hash = "#queue=answer";
    queueState.key = "answer?";
    queueState.queue = { kind: "answer", title: "Agent questions", empty: "No question waits for you.", total: 0, items: [] };
    const c = render(Queue).container;
    expect(c.textContent).toContain("No question waits for you.");
    expect(Array.from(c.querySelectorAll("a")).map((a) => a.getAttribute("href"))).toContain("#view=home");
  });

  it("walks a list of tasks, leaving out the ones gone, each opening its detail", async () => {
    input.tasks = { "t-1": { id: "t-1", title: "one" }, "t-2": { id: "t-2", title: "two" } };
    const c = await open("#queue=list&ids=t-2,t-gone,t-1");
    expect(log.gets).toEqual(["/api/tasks/t-2", "/api/tasks/t-gone", "/api/tasks/t-1"]);
    expect(rows(c)).toEqual(["task:t-2", "task:t-1"]);
    queueKeyAction("queue:primary");
    await tick();
    expect(location.hash).toContain("task=t-2");
  });

  it("never shows nor runs another route's queue, and drops its error once it loads", async () => {
    const gateItem = { task: { id: "t-9", title: "ship", units: [] }, gate: { id: 7, kind: "question", text: "Ship it?", intents: [{ id: "approve", label: "Approve" }] }, primary: { kind: "gate", id: "approve", label: "Approve" } };
    input.queues = { answer: { kind: "answer", title: "Agent questions", total: 1, items: [gateItem] }, "last-step": threeQueue() };
    const c = await open("#queue=answer");
    nav.hash = "#queue=send-draft";
    flushSync();
    expect(rows(c)).toEqual([]);
    expect(c.textContent).not.toContain("Agent questions");
    expect(queueKeyAction("queue:primary")).toBe(false);
    await queueLoad(readRoute(nav.hash));
    flushSync();
    expect(rows(c)).toEqual([]);
    expect(c.querySelector(".err")!.textContent).toContain("Could not load the queue");
    nav.hash = "#queue=last-step";
    await queueLoad(readRoute(nav.hash));
    flushSync();
    expect(rows(c)).toEqual(["task:t-1", "task:t-2"]);
    expect(c.querySelector(".err")).toBeNull();
    expect(log.posts).toEqual([]);
  });

  it("draws its rows as its served view's", async () => {
    const q = threeQueue();
    q.view = "needs-me";
    input.queues = { "last-step": q };
    const c = await open("#queue=last-step");
    expect(rows(c).length).toBe(2);
  });

  it("shows the row of the task a queued draft or gate is about, once", async () => {
    const gate = (id: number) => ({ task: { id: "t-1", title: "ship", units: [] }, gate: { id, kind: "question", text: "Which?", intents: [{ id: "approve", label: "Approve" }] }, primary: { kind: "gate", id: "approve", label: "Approve" } });
    input.queues = { answer: { kind: "answer", title: "Agent questions", total: 2, items: [gate(1), gate(2)] } };
    const c = await open("#queue=answer");
    expect(Array.from(c.querySelector(".queue-item")!.querySelectorAll("[data-row]")).map((n) => n.getAttribute("data-row"))).toEqual(["task:t-1"]);
    expect(rows(c)).toEqual(["task:t-1"]);
  });

  it("goes home on its key", () => {
    expect(queueKeyAction("queue:home")).toBe(true);
    expect(log.nav).toEqual(["home"]);
  });

  it("keeps its item's row node across a reload of the same queue", async () => {
    input.queues = { "last-step": threeQueue() };
    const c = await open("#queue=last-step");
    const row = c.querySelector('.queue-item [data-row="task:t-1"]');
    input.queues["last-step"].items[0].task.title = "one, renamed";
    await queueLoad(readRoute(nav.hash));
    flushSync();
    expect(c.querySelector('.queue-item [data-row="task:t-1"]')).toBe(row);
    expect(row!.textContent).toContain("one, renamed");
  });
});
