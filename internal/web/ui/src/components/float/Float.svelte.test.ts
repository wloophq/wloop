import { cleanup, fireEvent, render } from "@testing-library/svelte";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import type { J } from "../../lib/util";
import { nav } from "../../state/route.svelte";
import { floatDeps, floatLoad, floatState, resetFloat } from "../../state/float.svelte";
import { notice, tasksState } from "../../state/store.svelte";
import { draftGate, pendingDraft, reviewDraft, taskCatalog } from "../../testing/gates";
import { FakeDaemon } from "../../testing/fakedaemon";
import { resetStore } from "../../testing/reset.svelte";
import { settle } from "../../testing/settle";
import Agents from "./Agents.svelte";
import Card from "./Card.svelte";
import Float from "./Float.svelte";
import { floatRun, keepRoutes, resetFloatSize } from "./float";
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
