import { cleanup, fireEvent, render } from "@testing-library/svelte";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import type { J } from "../../lib/util";
import { nav } from "../../state/route.svelte";
import { floatDeps, floatLoad, floatState, resetFloat } from "../../state/float.svelte";
import { tasksState } from "../../state/store.svelte";
import { draftGate, pendingDraft, reviewDraft, taskCatalog } from "../../testing/gates";
import { FakeDaemon } from "../../testing/fakedaemon";
import { resetStore } from "../../testing/reset.svelte";
import { settle } from "../../testing/settle";
import Card from "./Card.svelte";
import Float from "./Float.svelte";
import { keepRoutes, resetFloatSize } from "./float";
import ReviewCard from "./ReviewCard.svelte";

const saved = { ...floatDeps };
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
  floatDeps.post = (m) => posted.push(m);
  (window as J).webkit = { messageHandlers: { external: { postMessage: (m: string) => raw.push(m) } } };
  served = { title: "Needs you", items: [item(1)] };
  new FakeDaemon({ "/api/queue": () => served, "/api/turns/live": () => turns() });
});

afterEach(() => {
  cleanup();
  Object.assign(floatDeps, saved);
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
  const d = reviewDraft();
  d.fields[0].tone = "err";
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
