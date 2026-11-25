import { readFileSync } from "node:fs";
import { cleanup, fireEvent, render } from "@testing-library/svelte";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import type { J } from "../../lib/util";
import { nav } from "../../state/route.svelte";
import { FLOAT_HOVER_CLOSE_MS, FLOAT_PEEK_MS, floatDeps, floatLoad, floatState, resetFloat } from "../../state/float.svelte";
import { flushSync } from "svelte";
import { notice, tasksState } from "../../state/store.svelte";
import { draftGate, pendingDraft, reviewDraft, taskCatalog } from "../../testing/gates";
import { FakeDaemon } from "../../testing/fakedaemon";
import { resetStore } from "../../testing/reset.svelte";
import { settle } from "../../testing/settle";
import Agents from "./Agents.svelte";
import Card from "./Card.svelte";
import Float from "./Float.svelte";
import { floatOpenUnit, floatRun, keepRoutes, resetFloatSize } from "./float";
import ReviewCard from "./ReviewCard.svelte";
import { queueDeps } from "../screens/queue";

const saved = { ...floatDeps };
const savedOpen = queueDeps.open;
const savedMatchMedia = window.matchMedia;
let opened: string[] = [];
let posted: J[] = [];
let raw: string[] = [];
let served: J;
const turns = (running: J[] = []) => ({ running, done: [], asked: 0, usual: {} });

const item = (id: number, extra: J = {}): J => ({
  kind: "review-step", word: "step", tone: "me", task: { id: "t-" + id, title: "Ana's change " + id, units: [] },
  gate: { id, kind: "step", status: "open", text: "Review it", intents: [{ id: "approve", label: "Looks good" }] },
  primary: { kind: "gate", id: "approve", label: "Looks good" }, ...extra,
});

beforeEach(() => {
  resetStore();
  resetFloat();
  resetFloatSize();
  tasksState.catalog = taskCatalog();
  posted = [];
  raw = [];
  opened = [];
  queueDeps.open = (url) => opened.push(url);
  floatDeps.openUnit = floatOpenUnit;
  floatDeps.post = (m) => posted.push(m);
  (window as J).webkit = { messageHandlers: { external: { postMessage: (m: string) => raw.push(m) } } };
  served = { title: "Needs you", items: [item(1)] };
  new FakeDaemon({ "/api/queue": () => served, "/api/turns/live": () => turns() });
});

afterEach(() => {
  vi.useRealTimers();
  window.matchMedia = savedMatchMedia;
  document.documentElement.style.removeProperty("--motion-peek-out");
  vi.restoreAllMocks();
  cleanup();
  Object.assign(floatDeps, saved);
  queueDeps.open = savedOpen;
  delete (window as J).webkit;
  resetFloat();
});

const sizes = () => posted.filter((m) => m.type === "float:size");
const pill = (c: Element) => c.querySelector(".fl-pill") as HTMLElement;

test("the size the page reports names the mode it draws, and a drop reports it again", async () => {
  await floatLoad();
  const c = render(Float).container;
  await settle();
  expect(sizes().at(-1)).toMatchObject({ type: "float:size", mode: "pill" });
  floatState().setMode("panel");
  await settle();
  expect(sizes().at(-1)!.mode).toBe("panel");
  floatState().setMode("pill");
  await settle();
  const before = sizes().length;
  await fireEvent.pointerDown(pill(c), { button: 0, clientX: 0, clientY: 0 });
  await fireEvent.pointerMove(pill(c), { clientX: 12, clientY: 0 });
  expect(raw).toEqual(["wails:drag"]);
  expect(sizes().length).toBe(before);
  await fireEvent.pointerUp(window);
  expect(sizes().length).toBe(before + 1);
  expect(sizes().at(-1)!.mode).toBe("pill");
});

test("the panel draws a task's two units waiting the same way as two items and moves between them", async () => {
  const thread = (n: number): J => ({
    kind: "answer", word: "thread", task: { id: "t-1", title: "Answer the team", units: [] },
    situation: { provider: "slack", kind: "waits_on_viewer", label: "Waits on you", unit: "slack:C1/" + n },
    primary: { kind: "open", label: "Open" },
  });
  served = { title: "Needs you", items: [thread(1), thread(2)] };
  await floatLoad();
  const c = render(Float).container;
  floatState().setMode("panel");
  await settle();
  expect(c.querySelectorAll(".fl-queue > li")).toHaveLength(2);
  expect(c.querySelector(".fl-queue > li:nth-child(1) .fl-hand")).not.toBeNull();
  floatState().move(1);
  await settle();
  expect(floatState().current).toBe(1);
  expect(c.querySelector(".fl-queue > li:nth-child(2) .fl-hand")).not.toBeNull();
  expect(c.querySelector(".fl-queue > li:nth-child(1) .fl-line")).not.toBeNull();
});

test("the hide menu and an Option-click ask the host to hide the widget", async () => {
  await floatLoad();
  const c = render(Float).container;
  floatState().setPinned(true);
  await settle();
  await fireEvent.click(c.querySelector('[aria-haspopup="menu"]') as HTMLElement);
  const modes = [...c.querySelectorAll("[data-hide]")].map((b) => b.getAttribute("data-hide"));
  expect(modes).toEqual(["quiet", "hour", "tomorrow"]);
  await fireEvent.click(c.querySelector('[data-hide="hour"]') as HTMLElement);
  expect(posted.filter((m) => m.type === "float:hide")).toEqual([{ type: "float:hide", mode: "hour" }]);
  expect(floatState().mode).toBe("pill");
  await settle();
  await fireEvent.pointerDown(pill(c), { button: 0, clientX: 0, clientY: 0 });
  await fireEvent.pointerUp(pill(c), { altKey: true });
  expect(posted.filter((m) => m.type === "float:hide").at(-1)).toEqual({ type: "float:hide", mode: "quiet" });
  expect(floatState().mode).toBe("pill");
});

test("with nothing waiting and nothing running the pill is a dot that still reports its size", async () => {
  served = { title: "Needs you", items: [] };
  await floatLoad();
  const c = render(Float).container;
  await settle();
  expect(pill(c).classList.contains("dot")).toBe(true);
  expect(pill(c).querySelector(".fl-dot.tone-muted")).not.toBeNull();
  expect(sizes().length).toBeGreaterThan(0);
  served = { title: "Needs you", items: [item(2)] };
  await floatLoad();
  await settle();
  expect(pill(c).classList.contains("dot")).toBe(false);
});

test("a link a card draws opens in the cockpit and leaves the float window on its route", async () => {
  nav.hash = "#/float";
  keepRoutes(document);
  const d = pendingDraft();
  const c = render(Card, { props: { f: floatState(), item: { ...item(9), gate: draftGate(d) }, age: "", late: false } }).container;
  const a = [...c.querySelectorAll("a")].find((x) => (x.getAttribute("href") || "").startsWith("#")) as HTMLAnchorElement;
  expect(a).toBeDefined();
  await fireEvent.click(a);
  expect(posted).toEqual([{ type: "float:open", route: a.getAttribute("href")!.slice(1) }]);
  expect(window.location.hash).not.toContain("task=");
});

test("items and the verdict draw in the tones the daemon serves", async () => {
  const d = { ...reviewDraft(), verdict: "Request changes", verdict_tone: "err" };
  const it = { ...item(3, { kind: "answer", tone: "late" }), options: [{ id: "a", label: "Request changes", recommended: true, draft: { id: 12, kind: "draft", subject: d } }] };
  const c = render(ReviewCard, { props: { f: floatState(), item: it, age: "", late: false } }).container;
  expect(c.querySelector(".fl-hand")!.classList.contains("tone-late")).toBe(true);
  expect(c.querySelector(".fl-verdict")!.classList.contains("tone-err")).toBe(true);
  expect(c.querySelector(".act.primary")!.textContent).toContain("Looks good");
});

test("an agent row names its intent by the label the daemon serves", async () => {
  new FakeDaemon({ "/api/queue": () => served, "/api/turns/live": () => turns([{ seq: 1, intent: "review", label: "Review the change", unit: "acme/widgets#12", started: "2026-11-07T11:55:00Z" }]) });
  await floatLoad();
  const c = render(Float).container;
  floatState().setPinned(true);
  await settle();
  expect(c.querySelector(".fl-what b")!.textContent).toBe("Review the change");
});

vi.setConfig({ testTimeout: 30000 });

test("the primary button says what picking the selected option posts, as the daemon words each one", async () => {
  const it = { ...item(4, { kind: "answer" }), options: [
    { id: "a", label: "Request changes", primary: "Post Request changes" },
    { id: "b", label: "Comment only", recommended: true, primary: "Post Comment" },
  ] };
  const c = render(ReviewCard, { props: { f: floatState(), item: it, age: "", late: false } }).container;
  const label = () => c.querySelector(".act.primary")!.textContent!.trim();
  expect(label()).toBe("↵ Post Comment");
  await fireEvent.click(c.querySelectorAll('[role="tab"]')[0]);
  expect(label()).toBe("↵ Post Request changes");
});

test("a modified or middle click on a card link is left to the page", async () => {
  keepRoutes(document);
  const d = pendingDraft();
  const c = render(Card, { props: { f: floatState(), item: { ...item(9), gate: draftGate(d) }, age: "", late: false } }).container;
  const a = [...c.querySelectorAll("a")].find((x) => (x.getAttribute("href") || "").startsWith("#")) as HTMLAnchorElement;
  let prevented = 0;
  document.addEventListener("click", (e) => { prevented += e.defaultPrevented ? 1 : 0; e.preventDefault(); });
  await fireEvent.click(a, { metaKey: true });
  await fireEvent.click(a, { shiftKey: true });
  await fireEvent.click(a, { button: 1 });
  expect(posted).toEqual([]);
  expect(prevented).toBe(0);
});

test("agents the daemon cannot read say so, and the queue stays as read", async () => {
  new FakeDaemon({ "/api/queue": () => served, "/api/turns/live": () => { throw new Error("busy"); } });
  await floatLoad();
  const c = render(Float).container;
  floatState().setPinned(true);
  await settle();
  expect([floatState().read, floatState().items.length]).toEqual(["ok", 1]);
  expect(c.querySelector(".fl-agents-off")).not.toBeNull();
  expect(c.querySelector(".fl-off")).toBeNull();
});

test("arrow keys typed in a text field of the open panel stay in the field", async () => {
  served = { title: "Needs you", items: [item(1), item(2)] };
  await floatLoad();
  const c = render(Float).container;
  floatState().setPinned(true);
  await settle();
  const field = document.createElement("textarea");
  c.querySelector(".fl-panel")!.appendChild(field);
  field.focus();
  for (const key of ["ArrowDown", "ArrowUp", "j"]) {
    const e = new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true });
    field.dispatchEvent(e);
    expect([key, e.defaultPrevented]).toEqual([key, false]);
  }
  expect(floatState().current).toBe(0);
});

test("a queue line and a hide choice mark the pointer over them, as the host's pointer events reach a float that is not key", async () => {
  served = { title: "Needs you", items: [item(1), item(2)] };
  await floatLoad();
  const c = render(Float).container;
  floatState().setPinned(true);
  await settle();
  await fireEvent.click(c.querySelector('[aria-haspopup="menu"]') as HTMLElement);
  for (const el of [c.querySelector(".fl-line"), c.querySelector("[data-hide]")] as HTMLElement[]) {
    await fireEvent.pointerEnter(el);
    expect(el.hasAttribute("data-hover")).toBe(true);
    await fireEvent.pointerLeave(el);
    expect(el.hasAttribute("data-hover")).toBe(false);
  }
});

test("a retry the daemon refuses says so on the float, and the refusal goes no further", async () => {
  floatDeps.run = floatRun;
  served = { title: "Needs you", items: [item(5, {
    kind: "send-draft", gate: undefined,
    primary: { kind: "verb", id: "retry", task: "draft:5", args: { rev: "3" }, label: "Retry" },
  })] };
  await floatLoad();
  const c = render(Float).container;
  floatState().setPinned(true);
  await settle();
  await expect(floatState().act()).resolves.toBeUndefined();
  notice.err = false;
  notice.text = "";
  await settle();
  expect(c.querySelector(".fl-err")!.textContent).toBe("Retry failed");
  served = { title: "Needs you", items: [item(6)] };
  await floatLoad();
  await settle();
  expect(c.querySelector(".fl-err")).toBeNull();
});

test("the agents count every turn done today, beyond the ones listed", async () => {
  const done = [1, 2, 3].map((seq) => ({ seq, intent: "review", label: "Review", unit: "acme/widgets#" + seq, ended: "2026-11-07T11:00:00Z", duration_ms: 60000 }));
  const c = render(Agents, { props: { turns: { running: [], done, done_today: 25, asked: 0, usual: {} } } }).container;
  expect(c.querySelector(".fl-sum")!.textContent).toBe("25 done today");
});

const flRoot = (c: Element) => c.querySelector(".fl") as HTMLElement;

test("the panel is bounded by the visible frame of the screen the host names, never by the float window", async () => {
  await floatLoad();
  const c = render(Float).container;
  floatState().setMode("panel", { edge: "right", up: false, maxW: 1400, maxH: 900 });
  await settle();
  expect(flRoot(c).style.getPropertyValue("--fl-max-w")).toBe("1400px");
  expect(flRoot(c).style.getPropertyValue("--fl-max-h")).toBe("900px");
});

test("a float the host says opens leftward marks its root, so the pill keeps to the right where its anchor is", async () => {
  await floatLoad();
  const c = render(Float).container;
  floatState().setMode("pill", { edge: "", up: false, leftward: true, maxW: 1200, maxH: 800 });
  await settle();
  expect(flRoot(c).classList.contains("leftward")).toBe(true);
  floatState().setMode("pill", { edge: "", up: false, leftward: false, maxW: 1200, maxH: 800 });
  await settle();
  expect(flRoot(c).classList.contains("leftward")).toBe(false);
});

test("before the host names a screen the panel is bounded by the one the page reads", async () => {
  vi.spyOn(window.screen, "availWidth", "get").mockReturnValue(1280);
  vi.spyOn(window.screen, "availHeight", "get").mockReturnValue(760);
  await floatLoad();
  const c = render(Float).container;
  floatState().setMode("panel");
  await settle();
  expect(flRoot(c).style.getPropertyValue("--fl-max-w")).toBe("1280px");
  expect(flRoot(c).style.getPropertyValue("--fl-max-h")).toBe("760px");
});

test("no style of the float reads the viewport, which is the float window sized to its content", () => {
  const css = readFileSync("../static/app.css", "utf8");
  const rules = css.match(/[^{}]*(?:\.fl\b|\.fl-|float-page)[^{}]*\{[^}]*\}/g) || [];
  expect(rules.length).toBeGreaterThan(10);
  for (const rule of rules) expect(rule).not.toMatch(/\d(?:vh|vw|dvh|dvw|svh|svw|lvh|lvw)\b/);
  const panel = rules.find((r) => /\.fl-panel\s*\{/.test(r)) || "";
  expect(panel).toContain("calc(var(--fl-max-w) - 24px)");
  expect(panel).toContain("calc(var(--fl-max-h) - 48px)");
});

const click = async (el: Element) => {
  await fireEvent.pointerDown(el, { button: 0, clientX: 0, clientY: 0 });
  await fireEvent.pointerUp(el, { button: 0, clientX: 0, clientY: 0 });
};

test("a click on the pill opens the panel without pinning it, and another closes it", async () => {
  await floatLoad();
  const c = render(Float).container;
  await settle();
  await click(pill(c));
  expect([floatState().mode, floatState().pinned]).toEqual(["panel", false]);
  await click(pill(c));
  expect(floatState().mode).toBe("pill");
});

test("a panel a click opened closes once the pointer has been gone for the hover delay", async () => {
  await floatLoad();
  const c = render(Float).container;
  await settle();
  await click(pill(c));
  vi.useFakeTimers();
  await fireEvent.pointerLeave(flRoot(c));
  vi.advanceTimersByTime(FLOAT_HOVER_CLOSE_MS - 1);
  expect(floatState().mode).toBe("panel");
  vi.advanceTimersByTime(1);
  flushSync();
  expect(floatState().mode).toBe("pill");
});

test("a panel a click opened closes at once when the float window loses focus", async () => {
  await floatLoad();
  const c = render(Float).container;
  await settle();
  await click(pill(c));
  await fireEvent.blur(window);
  expect(floatState().mode).toBe("pill");
});

test("a pinned panel stays open when the pointer leaves and when the window loses focus, and Esc closes it", async () => {
  await floatLoad();
  const c = render(Float).container;
  await settle();
  await click(pill(c));
  await fireEvent.click(c.querySelector(".fl-quiet[aria-pressed]")!);
  expect(floatState().pinned).toBe(true);
  vi.useFakeTimers();
  await fireEvent.pointerLeave(flRoot(c));
  vi.advanceTimersByTime(FLOAT_HOVER_CLOSE_MS * 2);
  await fireEvent.blur(window);
  expect(floatState().mode).toBe("panel");
  await fireEvent.keyDown(window, { key: "Escape" });
  expect([floatState().mode, floatState().pinned]).toEqual(["pill", false]);
});

const PR = { provider: "github", key: "acme/widgets#7" };
const LINK = "https://github.com/acme/widgets/pull/7";
const linked = (extra: J = {}, cards: J[] = [{ card: { unit: PR, link: LINK } }]): J =>
  item(7, { task: { id: "t 7", title: "Ana's change 7", units: [PR], unit_labels: ["acme/widgets#7"], cards }, ...extra });
const unitItem = (cards?: J[]) => linked({ kind: "unit", gate: undefined, primary: { kind: "open", id: "open", label: "Open" } }, cards);
const reviewItem = () => linked({ options: [{ id: "a", label: "Option A", primary: "Post A" }], gate: { id: 7, kind: "question", status: "open", text: "Which?", intents: [{ id: "approve", label: "Looks good" }] } });
const shown = async (it: J) => {
  served = { title: "Needs you", items: [it] };
  await floatLoad();
  const c = render(Float).container;
  floatState().setPinned(true);
  floatState().setMode("panel");
  await settle();
  return c;
};

test.each([["a gate", () => linked()], ["a unit", () => unitItem()], ["a question with drafts", () => reviewItem()]])(
  "the title of %s opens its task in the cockpit and its ref opens the unit at its source",
  async (_name, make) => {
    const c = await shown(make());
    await fireEvent.click(c.querySelector(".fl-card-title button") as HTMLElement);
    expect(posted.filter((m) => m.type === "float:open")).toEqual([{ type: "float:open", route: "task=t%207" }]);
    expect(opened).toEqual([]);
    const ref = c.querySelector(".fl-hand .fl-ref") as HTMLElement;
    expect(ref.tagName).toBe("BUTTON");
    expect(ref.textContent).toBe("acme/widgets#7");
    await fireEvent.click(ref);
    expect(opened).toEqual([LINK]);
  },
);

test("o opens the current task in the cockpit and u its unit at its source", async () => {
  await shown(linked());
  await fireEvent.keyDown(window, { key: "o" });
  expect(posted.filter((m) => m.type === "float:open")).toEqual([{ type: "float:open", route: "task=t%207" }]);
  expect(opened).toEqual([]);
  await fireEvent.keyDown(window, { key: "u" });
  expect(opened).toEqual([LINK]);
});

test("a unit no card links draws its ref as text, and u opens nothing", async () => {
  const c = await shown(unitItem([]));
  const ref = c.querySelector(".fl-hand .fl-ref") as HTMLElement;
  expect([ref.tagName, ref.textContent]).toEqual(["SPAN", "acme/widgets#7"]);
  await fireEvent.keyDown(window, { key: "u" });
  expect(opened).toEqual([]);
  expect(posted.filter((m) => m.type === "float:open")).toEqual([]);
});

test("u typed in a text field stays in the field", async () => {
  const c = await shown(linked());
  const field = document.createElement("input");
  c.appendChild(field);
  field.focus();
  field.dispatchEvent(new KeyboardEvent("keydown", { key: "u", bubbles: true, cancelable: true }));
  expect(opened).toEqual([]);
});

// drawsWide makes the float report a wide size while it draws the docked peek's card, as a browser would.
const drawsWide = () =>
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(function (this: HTMLElement) {
    const w = this.querySelector(".fl-flyout") ? 380 : 50;
    return { width: w, height: 100, x: 0, y: 0, top: 0, left: 0, right: w, bottom: 100, toJSON: () => ({}) } as DOMRect;
  });
const flyout = (c: Element) => c.querySelector(".fl-flyout") as HTMLElement | null;
const tick = async () => {
  await vi.advanceTimersByTimeAsync(0);
  flushSync();
};

// LEAVE_MS is the peek's leave as the stylesheet's token names it; jsdom loads no stylesheet, so a test sets it.
const LEAVE_MS = 140;
const motion = (reduced = false) => {
  document.documentElement.style.setProperty("--motion-peek-out", LEAVE_MS + "ms");
  window.matchMedia = (q: string) =>
    ({ matches: reduced && q.includes("prefers-reduced-motion: reduce"), media: q, addEventListener() {}, removeEventListener() {} }) as unknown as MediaQueryList;
};
const peekDocked = async (edge: "left" | "right" = "right") => {
  drawsWide();
  served = { title: "Needs you", items: [linked()] };
  await floatLoad();
  const c = render(Float).container;
  await settle();
  vi.useFakeTimers();
  floatState().setMode("peek", { edge, up: false, maxW: 1400, maxH: 900 });
  await tick();
  return c;
};

test.each(["left", "right"] as const)("docked %s, a peek shows beside the tab what arrived, and the window grows for it until the card has left", async (edge) => {
  motion();
  const c = await peekDocked(edge);
  const card = flyout(c)!;
  expect(card).not.toBeNull();
  expect(card.dataset.state).toBe("shown");
  expect(pill(c).nextElementSibling).toBe(card);
  expect(card.querySelector("b.tone-me")!.textContent).toBe("step");
  expect(card.textContent).toBe("step · acme/widgets#7 · Review it");
  expect(pill(c).querySelector(".fl-peek")).toBeNull();
  expect(sizes().at(-1)).toMatchObject({ w: 380, mode: "peek" });
  await vi.advanceTimersByTimeAsync(FLOAT_PEEK_MS);
  await tick();
  expect(flyout(c)!.dataset.state).toBe("leaving");
  expect(sizes().at(-1)).toMatchObject({ w: 380 });
  await vi.advanceTimersByTimeAsync(LEAVE_MS - 1);
  await tick();
  expect(flyout(c)).not.toBeNull();
  await vi.advanceTimersByTimeAsync(1);
  await tick();
  expect(flyout(c)).toBeNull();
  expect(sizes().at(-1)).toMatchObject({ w: 50, mode: "pill" });
});

test("a peek that arrives while the last one leaves keeps the card", async () => {
  motion();
  const c = await peekDocked();
  await vi.advanceTimersByTimeAsync(FLOAT_PEEK_MS);
  await tick();
  expect(flyout(c)!.dataset.state).toBe("leaving");
  floatState().setMode("peek");
  await tick();
  expect(flyout(c)!.dataset.state).toBe("shown");
  await vi.advanceTimersByTimeAsync(LEAVE_MS);
  await tick();
  expect(flyout(c)!.dataset.state).toBe("shown");
  expect(sizes().at(-1)).toMatchObject({ w: 380 });
});

test("a click on the leaving card opens the panel on what arrived", async () => {
  motion();
  const c = await peekDocked();
  await vi.advanceTimersByTimeAsync(FLOAT_PEEK_MS);
  await tick();
  await fireEvent.click(flyout(c)!);
  await tick();
  expect([floatState().mode, floatState().current]).toEqual(["panel", 0]);
  expect(flyout(c)).toBeNull();
  await vi.advanceTimersByTimeAsync(LEAVE_MS);
  floatState().setMode("pill");
  await tick();
  expect(flyout(c)).toBeNull();
});

test("under reduced motion the card leaves as the peek ends", async () => {
  motion(true);
  const c = await peekDocked();
  await vi.advanceTimersByTimeAsync(FLOAT_PEEK_MS);
  await tick();
  expect(flyout(c)).toBeNull();
  expect(sizes().at(-1)).toMatchObject({ w: 50, mode: "pill" });
});

test("undocked, the peek's text leaves the pill the same way, and a click while it leaves opens the panel", async () => {
  motion();
  await floatLoad();
  const c = render(Float).container;
  await settle();
  vi.useFakeTimers();
  floatState().setMode("peek");
  await tick();
  expect((pill(c).querySelector(".fl-peek") as HTMLElement).dataset.state).toBe("shown");
  await vi.advanceTimersByTimeAsync(FLOAT_PEEK_MS);
  await tick();
  expect((pill(c).querySelector(".fl-peek") as HTMLElement).dataset.state).toBe("leaving");
  await fireEvent.pointerDown(pill(c), { button: 0, clientX: 0, clientY: 0 });
  await fireEvent.pointerUp(pill(c));
  expect(floatState().mode).toBe("panel");
});

test("a click on the docked peek opens the panel on what arrived", async () => {
  await floatLoad();
  const c = render(Float).container;
  floatState().setMode("pill", { edge: "right", up: false, maxW: 1400, maxH: 900 });
  await settle();
  served = { title: "Needs you", items: [item(1), item(2)] };
  await floatLoad();
  await settle();
  expect(floatState().mode).toBe("peek");
  const card = flyout(c)!;
  await fireEvent.pointerEnter(card);
  expect(card.hasAttribute("data-hover")).toBe(true);
  await fireEvent.click(card);
  expect([floatState().mode, floatState().current, floatState().pinned]).toEqual(["panel", 1, false]);
  expect(flyout(c)).toBeNull();
});

test("undocked, a peek shows in the pill and draws no card beside it", async () => {
  await floatLoad();
  const c = render(Float).container;
  floatState().setMode("peek");
  await settle();
  expect(pill(c).querySelector(".fl-peek")!.textContent).toBe("step · Review it");
  expect(flyout(c)).toBeNull();
});
