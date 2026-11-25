import { afterEach, beforeEach, expect, test, vi } from "vitest";
import type { J } from "../lib/util";
import { floatResolve, floatRun } from "../components/float/float";
import { FakeDaemon } from "../testing/fakedaemon";
import { resetStore } from "../testing/reset.svelte";
import { Reply } from "../testing/fakedaemon";
import { state } from "./store.svelte";
import { FLOAT_PEEK_MS, FLOAT_QUIET_MS, FLOAT_RETRY_MS, floatChange, itemKey, floatDeps, floatFollow, floatLoad, floatState, resetFloat } from "./float.svelte";
import { connect, DEBOUNCE_MS, stream, type EventSourceLike } from "./sync.svelte";

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

test("act runs a failed delivery's retry through the draft's own verb, never the approved gate", async () => {
  floatDeps.run = floatRun;
  served = queue({
    kind: "send-draft",
    task: { id: "t-7", title: "thank the reviewer", units: [] },
    gate: { id: 9, kind: "draft", name: "draft", status: "approved", delivery: "failed", subject: "5", intents: [{ id: "approve", label: "Post" }, { id: "discard", label: "Discard" }] },
    primary: { kind: "verb", id: "retry", task: "draft:5", args: { rev: "3" }, label: "Retry" },
  });
  await floatLoad();
  const f = floatState();
  await f.act();
  expect(posts()).toEqual(["/api/tasks/draft:5/retry"]);
  const call = daemon.calls.find((c) => c.url === "/api/tasks/draft:5/retry");
  expect(JSON.parse(String(call && call.init && call.init.body)).args).toEqual({ rev: "3" });
  expect(daemon.calls.filter((c) => c.url === "/api/queue").length).toBe(2);
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

test("a trickle of arrivals for 30 s peeks once, and the next arrival after a quiet spell peeks again", async () => {
  served = queue(gateItem(1, "Ana's retry change"));
  await floatLoad();
  const f = floatState();
  const peeks: string[] = [];
  let last = "";
  for (let i = 2; i <= 16; i++) {
    served = queue(...served.items, gateItem(i, "Arrival " + i));
    await floatLoad();
    const now = f.peeked ? f.peeked.task.id : "";
    if (now && now !== last) peeks.push(now);
    last = now;
    await vi.advanceTimersByTimeAsync(2000);
  }
  expect(peeks).toEqual(["t-2"]);
  await vi.advanceTimersByTimeAsync(FLOAT_QUIET_MS);
  served = queue(...served.items, gateItem(40, "After the quiet"));
  await floatLoad();
  expect(f.peeked.task.id).toBe("t-40");
});

test("act waits for the read under way and sends nothing when it took the item away", async () => {
  await floatLoad();
  const f = floatState();
  let answer: (v: J) => void = () => {};
  daemon.pages["/api/queue"] = () => new Promise((r) => (answer = r));
  const reading = floatLoad();
  const acting = f.act();
  await vi.waitFor(() => expect(daemon.urls.filter((u) => u === "/api/queue")).toHaveLength(2));
  answer(queue(gateItem(2, "Bo's export fix"), gateItem(3, "Cy's docs pass")));
  await reading;
  await acting;
  expect(posts()).toEqual([]);
});

test("a stream that drops shows offline at once, and only a read after it reopens brings the counts back", async () => {
  const opened: EventSourceLike[] = [];
  const saved = stream.open;
  stream.open = () => {
    const es = { onmessage: null, onopen: null, onerror: null, readyState: 1, close: () => {} } as EventSourceLike;
    opened.push(es);
    return es;
  };
  try {
    await floatLoad();
    connect(floatFollow);
    const es = opened[0];
    es.onopen!();
    const f = floatState();
    expect([f.online, f.items.length]).toEqual([true, 3]);
    es.readyState = 0;
    es.onerror!();
    expect([f.online, f.read, f.items.length]).toEqual([false, "offline", 0]);
    es.readyState = 1;
    es.onopen!();
    expect(state.conn).toBe("open");
    expect(f.online).toBe(false);
    await vi.advanceTimersByTimeAsync(DEBOUNCE_MS);
    expect([f.online, f.items.length]).toEqual([true, 3]);
  } finally {
    stream.open = saved;
  }
});

test("a queue the daemon refuses shows its own state and is read again on a backoff and on the next event", async () => {
  await floatLoad();
  const f = floatState();
  daemon.pages["/api/queue"] = () => new Reply(503, { error: "busy" });
  await floatLoad();
  expect([f.read, f.items.length]).toEqual(["failed", 0]);
  daemon.clear();
  await vi.advanceTimersByTimeAsync(FLOAT_RETRY_MS);
  expect(daemon.urls).toEqual(["/api/queue", "/api/turns/live"]);
  expect(f.read).toBe("failed");
  daemon.clear();
  floatChange({ data: JSON.stringify({ type: "changed", health: true }) });
  await vi.advanceTimersByTimeAsync(DEBOUNCE_MS);
  expect(daemon.urls).toEqual(["/api/queue", "/api/turns/live"]);
  daemon.pages["/api/queue"] = () => structuredClone(served);
  await vi.advanceTimersByTimeAsync(8 * FLOAT_RETRY_MS);
  expect([f.read, f.items.length]).toEqual(["ok", 3]);
});

test("an open of the stream while the widget is offline reads everything", async () => {
  daemon.pages["/api/queue"] = () => {
    throw new Error("down");
  };
  await floatLoad();
  daemon.pages["/api/queue"] = () => structuredClone(served);
  daemon.clear();
  floatFollow.opened!();
  await vi.advanceTimersByTimeAsync(DEBOUNCE_MS);
  expect(daemon.urls.slice(0, 2)).toEqual(["/api/queue", "/api/turns/live"]);
  expect(floatState().online).toBe(true);
});

test("act after a gate event waits for the read it queued and sends nothing against a gate closed elsewhere", async () => {
  served = queue(gateItem(1, "Ana's retry change"), gateItem(2, "Bo's export fix"));
  await floatLoad();
  const f = floatState();
  served = queue(gateItem(2, "Bo's export fix"));
  floatChange({ data: JSON.stringify({ type: "changed", gates: true }) });
  await f.act();
  expect(posts()).toEqual([]);
  expect(f.items[f.current].task.id).toBe("t-2");
});

test("an item is known by its gate, else its task, kind and situation's provider, kind and unit, as the menu bar knows it", () => {
  const stuck = { kind: "fix-checks", task: { id: "t-1" }, situation: { provider: "github", kind: "checks-failed", label: "Checks failed", unit: "github:acme/api#1" } };
  expect(itemKey({ ...stuck, situation: { ...stuck.situation, label: "As verificações falharam" } })).toBe("task:t-1:fix-checks:github:checks-failed:github:acme/api#1");
  expect(itemKey({ ...stuck, situation: { ...stuck.situation, unit: "github:acme/api#2" } })).not.toBe(itemKey(stuck));
  expect(itemKey({ kind: "send-draft", task: { id: "t-1" }, gate: { id: 41 } })).toBe("gate:41");
});
