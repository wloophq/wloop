import { readFileSync } from "node:fs";
import { cleanup, fireEvent, render } from "@testing-library/svelte";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import type { J } from "../../lib/util";
import { FLOAT_HOVER_OPEN_MS, floatDeps, floatLoad, floatState, resetFloat } from "../../state/float.svelte";
import { FakeDaemon } from "../../testing/fakedaemon";
import { resetStore } from "../../testing/reset.svelte";
import { settle } from "../../testing/settle";
import FloatTab from "./FloatTab.svelte";
import { resetFloatSize } from "./float";

const saved = { ...floatDeps };
let posted: J[] = [];
let raw: string[] = [];
let served: J;
let live: J[] = [];
const turns = (running: J[] = []) => ({ running, done: [], asked: 0, usual: {} });
const item = (id: number): J => ({
  kind: "review-step", word: "step", tone: "me", task: { id: "t-" + id, title: "Ana's change " + id, units: [] },
  gate: { id, kind: "step", status: "open", text: "Review it", intents: [{ id: "approve", label: "Looks good" }] },
  primary: { kind: "gate", id: "approve", label: "Looks good" },
});

beforeEach(() => {
  resetStore();
  resetFloat();
  resetFloatSize();
  posted = [];
  raw = [];
  live = [];
  floatDeps.post = (m) => posted.push(m);
  (window as J).webkit = { messageHandlers: { external: { postMessage: (m: string) => raw.push(m) } } };
  served = { title: "Needs you", items: [item(1)] };
  new FakeDaemon({ "/api/queue": () => served, "/api/turns/live": () => turns(live) });
});

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
  cleanup();
  Object.assign(floatDeps, saved);
  delete (window as J).webkit;
  resetFloat();
});

const sizes = () => posted.filter((m) => m.type === "float:size");
const asks = () => posted.filter((m) => m.type === "float:panel");
const pill = (c: Element) => c.querySelector(".fl-pill") as HTMLElement;
const click = async (el: Element, extra: J = {}) => {
  await fireEvent.pointerDown(el, { button: 0, clientX: 0, clientY: 0 });
  await fireEvent.pointerUp(el, { button: 0, clientX: 0, clientY: 0, ...extra });
};

// drawsAt makes the tab report w by h for what it draws, as a browser would.
const drawsAt = (w: number, h: number) =>
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockReturnValue({ width: w, height: h, x: 0, y: 0, top: 0, left: 0, right: w, bottom: h, toJSON: () => ({}) } as DOMRect);

test("the tab reports its size with no mode, again only as it changes, and once more on a drop", async () => {
  const draws = drawsAt(128, 48);
  await floatLoad();
  const c = render(FloatTab).container;
  await settle();
  expect(sizes()).toEqual([{ type: "float:size", w: 128, h: 48 }]);
  floatState().setPlace({ edge: "right" });
  await settle();
  expect(sizes()).toHaveLength(1);
  draws.mockReturnValue({ width: 50, height: 100, x: 0, y: 0, top: 0, left: 0, right: 50, bottom: 100, toJSON: () => ({}) } as DOMRect);
  floatState().setPlace({ edge: "" });
  await settle();
  expect(sizes().at(-1)).toEqual({ type: "float:size", w: 50, h: 100 });
  await fireEvent.pointerDown(pill(c), { button: 0, clientX: 0, clientY: 0 });
  await fireEvent.pointerMove(pill(c), { clientX: 12, clientY: 0 });
  expect(raw).toEqual(["wails:drag"]);
  const before = sizes().length;
  await fireEvent.pointerUp(window);
  expect(sizes()).toHaveLength(before + 1);
});

test("docked, the tab stands upright on its edge", async () => {
  await floatLoad();
  const c = render(FloatTab).container;
  floatState().setPlace({ edge: "left" });
  await settle();
  expect((c.querySelector(".fl") as HTMLElement).dataset.edge).toBe("left");
});

test("hovering the tab asks for the panel after the hover delay; leaving before then asks nothing", async () => {
  await floatLoad();
  const c = render(FloatTab).container;
  await settle();
  vi.useFakeTimers();
  await fireEvent.pointerEnter(pill(c));
  vi.advanceTimersByTime(FLOAT_HOVER_OPEN_MS - 1);
  await fireEvent.pointerLeave(pill(c));
  vi.advanceTimersByTime(FLOAT_HOVER_OPEN_MS);
  expect(asks()).toEqual([]);
  await fireEvent.pointerEnter(pill(c));
  vi.advanceTimersByTime(FLOAT_HOVER_OPEN_MS);
  expect(asks()).toEqual([{ type: "float:panel" }]);
});

test("a click toggles the panel, and an Option-click hides the widget until something new", async () => {
  await floatLoad();
  const c = render(FloatTab).container;
  await settle();
  await click(pill(c));
  await click(pill(c), { altKey: true });
  expect(posted.filter((m) => m.type !== "float:size")).toEqual([{ type: "float:panel", toggle: true }, { type: "float:hide", mode: "quiet" }]);
});

test("a drag asks for no panel", async () => {
  await floatLoad();
  const c = render(FloatTab).container;
  await settle();
  vi.useFakeTimers();
  await fireEvent.pointerEnter(pill(c));
  await fireEvent.pointerDown(pill(c), { button: 0, clientX: 0, clientY: 0 });
  await fireEvent.pointerMove(pill(c), { clientX: 12, clientY: 0 });
  vi.advanceTimersByTime(FLOAT_HOVER_OPEN_MS);
  await fireEvent.pointerUp(window);
  expect(asks()).toEqual([]);
});

test("with nothing waiting and nothing running the tab is a dot, whatever the panel shows", async () => {
  served = { title: "Needs you", items: [] };
  await floatLoad();
  const c = render(FloatTab).container;
  await settle();
  expect(pill(c).classList.contains("dot")).toBe(true);
  expect(pill(c).querySelector(".fl-dot.tone-muted")).not.toBeNull();
  served = { title: "Needs you", items: [item(2)] };
  await floatLoad();
  await settle();
  expect(pill(c).classList.contains("dot")).toBe(false);
});

test("an arrival asks the host for a peek at it, and the tab only counts it", async () => {
  floatDeps.peek = (it) => posted.push({ type: "float:peek", item: it.task.id });
  await floatLoad();
  const c = render(FloatTab).container;
  await settle();
  served = { title: "Needs you", items: [item(1), item(2)] };
  await floatLoad();
  await settle();
  expect(posted.filter((m) => m.type === "float:peek")).toEqual([{ type: "float:peek", item: "t-2" }]);
  expect(pill(c).querySelector(".fl-count")!.textContent).toBe("2");
});

test("the tab shows the count and the running turns alone: no mark, no dot beside the count, no age", async () => {
  await floatLoad();
  const c = render(FloatTab).container;
  await settle();
  expect(pill(c).querySelector(".fl-count")!.textContent).toBe("1");
  expect(pill(c).querySelector(".fl-mark")).toBeNull();
  expect(pill(c).querySelector(".fl-dot")).toBeNull();
  expect(pill(c).querySelector(".fl-age")).toBeNull();
});

test("the tab is one circle with the count, and two side by side while turns run, the second a ring with their count", async () => {
  await floatLoad();
  const c = render(FloatTab).container;
  await settle();
  expect(pill(c).querySelectorAll(".fl-slot").length).toBe(1);
  expect(pill(c).querySelector(".fl-count")!.textContent).toBe("1");
  live = [{ intent: "review", started: new Date().toISOString() }, { intent: "address", started: new Date().toISOString() }];
  await floatLoad();
  await settle();
  const slots = pill(c).querySelectorAll(".fl-slot");
  expect(slots.length).toBe(2);
  expect(slots[1].querySelector(".fl-ring")).not.toBeNull();
  expect(slots[1].textContent!.trim()).toBe("2");
});

test("the tab is a flat circle: round, as wide as tall per slot, with no outline, shadow or backdrop blur", () => {
  const css = readFileSync("../static/app.css", "utf8");
  const rule = (sel: string) => (css.match(new RegExp("\\n" + sel.replace(/[.[\]]/g, "\\$&") + "\\s*\\{([^}]*)\\}")) || ["", ""])[1];
  const pillRule = rule(".fl-pill");
  expect(pillRule).toMatch(/border-radius:\s*var\(--fl-slot\)/);
  expect(pillRule).not.toMatch(/(^|[;\s])border:\s*(?!0)/);
  expect(pillRule).not.toMatch(/box-shadow|backdrop-filter/);
  const slot = rule(".fl-slot");
  expect(slot).toMatch(/width:\s*var\(--fl-slot\)/);
  expect(slot).toMatch(/height:\s*var\(--fl-slot\)/);
});
