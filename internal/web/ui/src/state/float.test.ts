import { afterEach, beforeEach, expect, test, vi } from "vitest";
import type { J } from "../lib/util";
import { floatResolve } from "../components/float/float";
import { FakeDaemon } from "../testing/fakedaemon";
import { resetStore } from "../testing/reset.svelte";
import { FLOAT_PEEK_MS, floatChange, floatDeps, floatLoad, floatState, resetFloat } from "./float.svelte";
import { DEBOUNCE_MS } from "./sync.svelte";

function gateItem(id: number, title: string): J {
  return {
    kind: "review-step",
    task: { id: "t-" + id, title, units: [] },
    gate: { id, kind: "step", name: "review-diff", status: "open", text: title, intents: [{ id: "approve", label: "Looks good" }, { id: "dismiss", label: "Dismiss" }] },
    primary: { kind: "gate", id: "approve", label: "Looks good" },
  };
}

const queue = (...items: J[]) => ({ kind: "", title: "Needs you", empty: "Nothing needs you.", total: items.length, items });
const turns = { running: [], done: [], asked: 0, usual: {} };

let daemon: FakeDaemon;
let served: J;
const saved = { ...floatDeps };

beforeEach(() => {
  vi.useFakeTimers();
  resetStore();
  resetFloat();
  floatDeps.resolve = floatResolve;
  served = queue(gateItem(1, "Ana's retry change"), gateItem(2, "Bo's export fix"), gateItem(3, "Cy's docs pass"));
  daemon = new FakeDaemon({ "/api/queue": () => structuredClone(served), "/api/turns/live": () => turns });
  daemon.fallback = (c) => {
    if (c.init && c.init.method === "POST") return {};
    throw new Error("unexpected");
  };
});

afterEach(() => {
  Object.assign(floatDeps, saved);
  resetFloat();
  vi.useRealTimers();
});

const posts = () => daemon.calls.filter((c) => c.init && c.init.method === "POST").map((c) => c.url);

test("move wraps at both ends of the queue", async () => {
  await floatLoad();
  const f = floatState();
  expect(f.current).toBe(0);
  f.move(-1);
  expect(f.current).toBe(2);
  f.move(1);
  expect(f.current).toBe(0);
  f.move(1);
  f.move(1);
  expect(f.items[f.current].task.id).toBe("t-3");
});

test("act resolves the current gate with its primary intent through the gates endpoint", async () => {
  await floatLoad();
  const f = floatState();
  f.move(1);
  await f.act();
  expect(posts()).toEqual(["/api/gates/2/approve"]);
});

test("an item that leaves on a refetch hands the focus to its successor, and act never reaches the closed gate", async () => {
  await floatLoad();
  const f = floatState();
  f.move(1);
  served = queue(gateItem(1, "Ana's retry change"), gateItem(3, "Cy's docs pass"));
  await floatLoad();
  expect(f.items[f.current].task.id).toBe("t-3");
  served = queue(gateItem(1, "Ana's retry change"));
  await floatLoad();
  expect(f.items[f.current].task.id).toBe("t-1");
  await f.act();
  expect(posts()).toEqual(["/api/gates/1/approve"]);
});

test("a burst of arrivals while collapsed peeks once, at the first of them", async () => {
  served = queue(gateItem(1, "Ana's retry change"));
  await floatLoad();
  const f = floatState();
  expect(f.peeked).toBeNull();
  const seen: string[] = [];
  for (let i = 2; i <= 21; i++) {
    served = queue(...served.items, gateItem(i, "Arrival " + i));
    await floatLoad();
    if (f.peeked) seen.push(f.peeked.task.id);
    await vi.advanceTimersByTimeAsync(400);
  }
  expect(new Set(seen)).toEqual(new Set(["t-2"]));
  await vi.advanceTimersByTimeAsync(FLOAT_PEEK_MS);
  expect(f.peeked).toBeNull();
  expect(f.mode).toBe("pill");
});

test("an arrival while the panel is open does not peek", async () => {
  served = queue(gateItem(1, "Ana's retry change"));
  await floatLoad();
  const f = floatState();
  f.setMode("panel");
  served = queue(gateItem(1, "Ana's retry change"), gateItem(2, "Bo's export fix"));
  await floatLoad();
  expect(f.peeked).toBeNull();
});

test("opening the panel while a peek shows makes the peeked item current", async () => {
  served = queue(gateItem(1, "Ana's retry change"));
  await floatLoad();
  served = queue(gateItem(1, "Ana's retry change"), gateItem(2, "Bo's export fix"));
  await floatLoad();
  const f = floatState();
  f.setMode("panel");
  expect(f.items[f.current].task.id).toBe("t-2");
});

test("a change reads the queue on a gate or task move and the turns only when they moved", async () => {
  await floatLoad();
  daemon.clear();
  floatChange({ data: JSON.stringify({ type: "changed", gates: true }) });
  await vi.advanceTimersByTimeAsync(DEBOUNCE_MS);
  expect(daemon.urls).toEqual(["/api/queue"]);
  daemon.clear();
  floatChange({ data: JSON.stringify({ type: "changed", turns: true }) });
  await vi.advanceTimersByTimeAsync(DEBOUNCE_MS);
  expect(daemon.urls).toEqual(["/api/turns/live"]);
  daemon.clear();
  floatChange({ data: JSON.stringify({ type: "changed", health: true }) });
  await vi.advanceTimersByTimeAsync(DEBOUNCE_MS);
  expect(daemon.urls).toEqual([]);
});

test("a daemon that stops answering leaves no stale count", async () => {
  await floatLoad();
  const f = floatState();
  expect(f.items.length).toBe(3);
  daemon.pages["/api/queue"] = () => {
    throw new Error("down");
  };
  await floatLoad();
  expect([f.online, f.items.length]).toEqual([false, 0]);
});
