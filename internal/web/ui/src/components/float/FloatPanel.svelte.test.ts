import { readFileSync } from "node:fs";
import { cleanup, fireEvent, render } from "@testing-library/svelte";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import type { J } from "../../lib/util";
import { nav } from "../../state/route.svelte";
import { FLOAT_ARM_MS, FLOAT_PEEK_LINGER_MS, FLOAT_PEEK_MS, floatDeps, floatLoad, floatState, itemKey, resetFloat } from "../../state/float.svelte";
import { flushSync } from "svelte";
import { notice, tasksState } from "../../state/store.svelte";
import { draftGate, pendingDraft, reviewDraft, taskCatalog } from "../../testing/gates";
import { FakeDaemon } from "../../testing/fakedaemon";
import { resetStore } from "../../testing/reset.svelte";
import { settle } from "../../testing/settle";
import Agents from "./Agents.svelte";
import Card from "./Card.svelte";
import FloatPanel from "./FloatPanel.svelte";
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
const closes = () => posted.filter((m) => m.type === "float:close").length;

test("the page says it is closed as it loads, then reports its size with the mode it draws and none while closed", async () => {
  await floatLoad();
  render(FloatPanel);
  await settle();
  expect([closes(), sizes()]).toEqual([1, []]);
  floatState().show("panel");
  await settle();
  expect(sizes().at(-1)).toMatchObject({ type: "float:size", mode: "panel" });
  const before = sizes().length;
  floatState().show("", {});
  await settle();
  expect([sizes().length, closes()]).toEqual([before, 1]);
  floatState().show("panel");
  await settle();
  expect(sizes().length).toBe(before + 1);
});

test("the panel draws a task's two units waiting the same way as two items and moves between them", async () => {
  const thread = (n: number): J => ({
    kind: "answer", word: "thread", task: { id: "t-1", title: "Answer the team", units: [] },
    situation: { provider: "slack", kind: "waits_on_viewer", label: "Waits on you", unit: "slack:C1/" + n },
    primary: { kind: "open", label: "Open" },
  });
  served = { title: "Needs you", items: [thread(1), thread(2)] };
  await floatLoad();
  const c = render(FloatPanel).container;
  floatState().show("panel");
  await settle();
  expect(c.querySelectorAll(".fl-queue > li")).toHaveLength(2);
  expect(c.querySelector(".fl-queue > li:nth-child(1) .fl-hand")).not.toBeNull();
  floatState().move(1);
  await settle();
  expect(floatState().current).toBe(1);
  expect(c.querySelector(".fl-queue > li:nth-child(2) .fl-hand")).not.toBeNull();
  expect(c.querySelector(".fl-queue > li:nth-child(1) .fl-line")).not.toBeNull();
});

test("the hide menu asks the host to hide the widget and closes the panel", async () => {
  await floatLoad();
  const c = render(FloatPanel).container;
  floatState().setPinned(true);
  await settle();
  await fireEvent.click(c.querySelector('[aria-haspopup="menu"]') as HTMLElement);
  const modes = [...c.querySelectorAll("[data-hide]")].map((b) => b.getAttribute("data-hide"));
  expect(modes).toEqual(["quiet", "hour", "tomorrow"]);
  await fireEvent.click(c.querySelector('[data-hide="hour"]') as HTMLElement);
  expect(posted.filter((m) => m.type === "float:hide")).toEqual([{ type: "float:hide", mode: "hour" }]);
  expect([floatState().mode, closes()]).toEqual(["", 2]);
});

test("a link a card draws opens in the cockpit and leaves the float window on its route", async () => {
  nav.hash = "#/float/panel";
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
  const c = render(FloatPanel).container;
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
  const c = render(FloatPanel).container;
  floatState().setPinned(true);
  await settle();
  expect([floatState().read, floatState().items.length]).toEqual(["ok", 1]);
  expect(c.querySelector(".fl-agents-off")).not.toBeNull();
  expect(c.querySelector(".fl-off")).toBeNull();
});

test("arrow keys typed in a text field of the open panel stay in the field", async () => {
  served = { title: "Needs you", items: [item(1), item(2)] };
  await floatLoad();
  const c = render(FloatPanel).container;
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
  const c = render(FloatPanel).container;
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
  const c = render(FloatPanel).container;
  floatDeps.focused = () => true;
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

test("the panel is bounded by the room the host names beside the tab, never by its window", async () => {
  await floatLoad();
  const c = render(FloatPanel).container;
  floatState().show("panel", { side: "left", maxW: 1400, maxH: 900 });
  await settle();
  expect(flRoot(c).style.getPropertyValue("--fl-max-w")).toBe("1400px");
  expect(flRoot(c).style.getPropertyValue("--fl-max-h")).toBe("900px");
  expect(flRoot(c).dataset.side).toBe("left");
});

test("before the host names a room the panel is bounded by the screen the page reads", async () => {
  vi.spyOn(window.screen, "availWidth", "get").mockReturnValue(1280);
  vi.spyOn(window.screen, "availHeight", "get").mockReturnValue(760);
  await floatLoad();
  const c = render(FloatPanel).container;
  floatState().show("panel");
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
  expect(panel).toContain("calc(var(--fl-max-w) - 16px)");
  expect(panel).toContain("calc(var(--fl-max-h) - 16px)");
});

test("the host's leave closes a panel that is not pinned, as does the window losing focus", async () => {
  await floatLoad();
  render(FloatPanel);
  floatState().show("panel");
  await settle();
  floatState().leave();
  expect([floatState().mode, closes()]).toEqual(["", 2]);
  floatState().show("panel");
  await fireEvent.blur(window);
  expect([floatState().mode, closes()]).toEqual(["", 3]);
});

test("a pinned panel stays open when the host says leave and when the window loses focus, and Esc closes it", async () => {
  await floatLoad();
  const c = render(FloatPanel).container;
  floatState().show("panel");
  await settle();
  await fireEvent.click(c.querySelector(".fl-quiet[aria-pressed]")!);
  expect(floatState().pinned).toBe(true);
  floatState().leave();
  await fireEvent.blur(window);
  expect(floatState().mode).toBe("panel");
  await fireEvent.keyDown(window, { key: "Escape" });
  expect([floatState().mode, floatState().pinned, closes()]).toEqual(["", false, 2]);
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
  const c = render(FloatPanel).container;
  floatState().setPinned(true);
  floatState().show("panel");
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

// drawsWide makes the page report a wide size while it draws the peek's card, as a browser would.
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
const peekAt = async (side: "left" | "right" | "above" | "below" = "left") => {
  drawsWide();
  served = { title: "Needs you", items: [item(1), linked()] };
  await floatLoad();
  const c = render(FloatPanel).container;
  await settle();
  vi.useFakeTimers();
  floatState().show("peek", { item: itemKey(linked()), side, maxW: 1400, maxH: 900 });
  await tick();
  return c;
};

test.each(["left", "right", "above", "below"] as const)("beside the tab on its %s, a peek shows what arrived, then leaves and closes the window once its card has left", async (side) => {
  motion();
  const c = await peekAt(side);
  const card = flyout(c)!;
  expect(card.dataset.state).toBe("shown");
  expect(flRoot(c).dataset.side).toBe(side);
  expect(card.querySelector("b.tone-me")!.textContent).toBe("step");
  expect(card.textContent).toBe("step · acme/widgets#7 · Review it");
  expect(sizes().at(-1)).toMatchObject({ w: 380, mode: "peek" });
  await vi.advanceTimersByTimeAsync(FLOAT_PEEK_MS);
  await tick();
  expect(flyout(c)!.dataset.state).toBe("leaving");
  await vi.advanceTimersByTimeAsync(LEAVE_MS - 1);
  await tick();
  expect([flyout(c) !== null, closes()]).toEqual([true, 1]);
  await vi.advanceTimersByTimeAsync(1);
  await tick();
  expect([flyout(c), floatState().mode, closes()]).toEqual([null, "", 2]);
});

test("the pointer on the peek's card keeps it shown, and it leaves FLOAT_PEEK_LINGER_MS after the pointer does", async () => {
  motion();
  const c = await peekAt();
  await vi.advanceTimersByTimeAsync(FLOAT_PEEK_MS - 1);
  await fireEvent.pointerEnter(flyout(c)!);
  await vi.advanceTimersByTimeAsync(FLOAT_PEEK_MS * 3);
  await tick();
  expect([flyout(c)!.dataset.state, closes()]).toEqual(["shown", 1]);
  await fireEvent.pointerLeave(flyout(c)!);
  await vi.advanceTimersByTimeAsync(FLOAT_PEEK_LINGER_MS - 1);
  await tick();
  expect(flyout(c)!.dataset.state).toBe("shown");
  await vi.advanceTimersByTimeAsync(1);
  await tick();
  expect(flyout(c)!.dataset.state).toBe("leaving");
});

test("the pointer reaching a leaving peek's card brings it back", async () => {
  motion();
  const c = await peekAt();
  await vi.advanceTimersByTimeAsync(FLOAT_PEEK_MS);
  await tick();
  expect(flyout(c)!.dataset.state).toBe("leaving");
  await fireEvent.pointerEnter(flyout(c)!);
  await tick();
  await vi.advanceTimersByTimeAsync(LEAVE_MS * 2);
  await tick();
  expect([flyout(c)!.dataset.state, closes()]).toEqual(["shown", 1]);
});

test("a peek the host asks for while the last one leaves keeps the card", async () => {
  motion();
  const c = await peekAt();
  await vi.advanceTimersByTimeAsync(FLOAT_PEEK_MS);
  await tick();
  expect(flyout(c)!.dataset.state).toBe("leaving");
  floatState().show("peek", { item: itemKey(item(1)) });
  await tick();
  expect(flyout(c)!.dataset.state).toBe("shown");
  await vi.advanceTimersByTimeAsync(LEAVE_MS);
  await tick();
  expect([flyout(c)!.dataset.state, closes()]).toEqual(["shown", 1]);
});

test("a click on the card, leaving or not, turns it into the panel on what arrived, unpinned", async () => {
  motion();
  const c = await peekAt();
  await vi.advanceTimersByTimeAsync(FLOAT_PEEK_MS);
  await tick();
  await fireEvent.pointerEnter(flyout(c)!);
  expect(flyout(c)!.hasAttribute("data-hover")).toBe(true);
  await fireEvent.click(flyout(c)!);
  await tick();
  expect([floatState().mode, floatState().current, floatState().pinned]).toEqual(["panel", 1, false]);
  expect(flyout(c)).toBeNull();
  await vi.advanceTimersByTimeAsync(LEAVE_MS);
  await tick();
  expect([floatState().mode, closes()]).toEqual(["panel", 1]);
  expect(sizes().at(-1)!.mode).toBe("panel");
});

test("under reduced motion the card leaves and the window closes as the peek ends", async () => {
  motion(true);
  const c = await peekAt();
  await vi.advanceTimersByTimeAsync(FLOAT_PEEK_MS);
  await tick();
  expect([flyout(c), closes()]).toEqual([null, 2]);
});

test("a peek at an item the page has not read yet reports no size until it has", async () => {
  await floatLoad();
  const c = render(FloatPanel).container;
  await settle();
  floatState().show("peek", { item: itemKey(item(2)) });
  await settle();
  expect([flyout(c), sizes()]).toEqual([null, []]);
  served = { title: "Needs you", items: [item(1), item(2)] };
  await floatLoad();
  await settle();
  expect(flyout(c)).not.toBeNull();
  expect(sizes().at(-1)!.mode).toBe("peek");
});

test("a click on an action of a panel the pointer just opened does nothing, until the panel has shown FLOAT_ARM_MS, though the click made its window key", async () => {
  let now = 1_000_000;
  vi.spyOn(Date, "now").mockImplementation(() => now);
  floatDeps.focused = () => true;
  const daemon = new FakeDaemon({ "/api/queue": () => served, "/api/turns/live": () => turns() });
  daemon.fallback = () => ({});
  const resolved = () => daemon.calls.filter((x) => x.init && x.init.method === "POST").map((x) => x.url);
  await floatLoad();
  const c = render(FloatPanel).container;
  floatState().show("panel");
  await settle();
  const primary = () => c.querySelector<HTMLButtonElement>(".fl-panel .act.primary")!;
  await fireEvent.click(primary());
  await settle();
  expect(resolved()).toEqual([]);
  now += FLOAT_ARM_MS;
  await fireEvent.click(primary());
  await settle();
  expect(resolved()).toEqual(["/api/gates/1/approve"]);
});
