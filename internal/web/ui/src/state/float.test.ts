import { afterEach, beforeEach, expect, test, vi } from "vitest";
import type { J } from "../lib/util";
import { floatResolve, floatRun } from "../components/float/float";
import { FakeDaemon } from "../testing/fakedaemon";
import { resetStore } from "../testing/reset.svelte";
import { Reply } from "../testing/fakedaemon";
import { state } from "./store.svelte";
import { FLOAT_ARM_MS, FLOAT_PEEK_MS, FLOAT_QUIET_MS, FLOAT_RETRY_MS, floatChange, itemKey, floatDeps, floatFollow, floatIdleWhileHidden, floatLoad, floatState, resetFloat } from "./float.svelte";
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
  floatDeps.focused = () => true;
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

// peeks records the peeks the page asks for, by task.
const peeks = (): string[] => {
  const asked: string[] = [];
  floatDeps.peek = (it) => asked.push(it.task.id);
  return asked;
};

test("a burst of arrivals asks for one peek, at the first of them, and the first read asks for none", async () => {
  const asked = peeks();
  served = queue(gateItem(1, "Ana's retry change"));
  await floatLoad();
  expect(asked).toEqual([]);
  for (let i = 2; i <= 21; i++) {
    served = queue(...served.items, gateItem(i, "Arrival " + i));
    await floatLoad();
    await vi.advanceTimersByTimeAsync(400);
  }
  expect(asked).toEqual(["t-2"]);
});

test("a peek the host shows lasts its while, then ends, and the item stays the one the panel opens on", async () => {
  served = queue(gateItem(1, "Ana's retry change"), gateItem(2, "Bo's export fix"));
  await floatLoad();
  const f = floatState();
  f.show("peek", { item: itemKey(served.items[1]), side: "left", maxW: 1400, maxH: 900 });
  expect([f.mode, f.peeking, f.peeked.task.id, f.room]).toEqual(["peek", true, "t-2", { side: "left", maxW: 1400, maxH: 900 }]);
  await vi.advanceTimersByTimeAsync(FLOAT_PEEK_MS);
  expect([f.mode, f.peeking]).toEqual(["peek", false]);
  f.show("panel");
  f.shown();
  expect([f.mode, f.items[f.current].task.id]).toEqual(["panel", "t-2"]);
});

test("the host opens the panel on the item it names, and a peek over the open panel changes nothing", async () => {
  await floatLoad();
  const f = floatState();
  f.show("panel", { item: itemKey(served.items[2]) });
  expect(f.items[f.current].task.id).toBe("t-3");
  f.show("peek", { item: itemKey(served.items[0]) });
  expect([f.mode, f.peekKey, f.items[f.current].task.id]).toEqual(["panel", "", "t-3"]);
  f.show("", {});
  expect([f.mode, f.pinned]).toEqual(["", false]);
});

test("leaving closes the open panel and asks the host to hide its window, unless pinned; closing unpins", async () => {
  const posted: J[] = [];
  floatDeps.post = (m) => posted.push(m);
  await floatLoad();
  const f = floatState();
  f.show("panel");
  f.shown();
  f.setPinned(true);
  f.leave();
  f.blur();
  expect([f.mode, posted]).toEqual(["panel", []]);
  f.close();
  expect([f.mode, f.pinned, posted]).toEqual(["", false, [{ type: "float:close" }]]);
  f.show("panel");
  f.shown();
  f.leave();
  expect([f.mode, posted.length]).toEqual(["", 2]);
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
  const asked = peeks();
  served = queue(gateItem(1, "Ana's retry change"));
  await floatLoad();
  for (let i = 2; i <= 16; i++) {
    served = queue(...served.items, gateItem(i, "Arrival " + i));
    await floatLoad();
    await vi.advanceTimersByTimeAsync(2000);
  }
  expect(asked).toEqual(["t-2"]);
  await vi.advanceTimersByTimeAsync(FLOAT_QUIET_MS);
  served = queue(...served.items, gateItem(40, "After the quiet"));
  await floatLoad();
  expect(asked).toEqual(["t-2", "t-40"]);
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

test("act runs the item its card drew, and nothing once a read took that item away", async () => {
  await floatLoad();
  const f = floatState();
  f.move(1);
  const drawn = itemKey(f.items[f.current]);
  served = queue(gateItem(1, "Ana's retry change"), gateItem(3, "Cy's docs pass"));
  await floatLoad();
  await f.act(drawn);
  expect(posts()).toEqual([]);
  await f.act(itemKey(f.items[f.current]));
  expect(posts()).toEqual(["/api/gates/3/approve"]);
});

test("a panel shown less than FLOAT_ARM_MS ago runs no primary unless its window has the keyboard", async () => {
  await floatLoad();
  const f = floatState();
  let focused = false;
  floatDeps.focused = () => focused;
  f.show("panel");
  f.shown();
  await f.act();
  expect(posts()).toEqual([]);
  focused = true;
  await f.act();
  expect(posts()).toEqual(["/api/gates/1/approve"]);
  focused = false;
  f.close();
  f.show("panel");
  f.shown();
  await vi.advanceTimersByTimeAsync(FLOAT_ARM_MS - 1);
  await f.act();
  expect(posts()).toEqual(["/api/gates/1/approve"]);
  await vi.advanceTimersByTimeAsync(1);
  expect(f.armed).toBe(true);
});

test("a stale draft's primary is refused as the cockpit's card refuses it, and nothing posts", async () => {
  const d = { id: 4, gate: 7, rev: 3, text: "Thanks, merged.", delivery: "reply", unit: "alpha:acme/api#1", intents: { approved: "approve" }, state: "held", stale: true,
    notes: ["The thread moved since the draft was written."] };
  served = queue({ kind: "send-draft", task: { id: "t-1", title: "ship", units: [] }, gate: { id: 7, kind: "draft", delivery: "held", subject: d, intents: [{ id: "approve", label: "Send" }] },
    primary: { kind: "gate", id: "approve", label: "Send" } });
  await floatLoad();
  const f = floatState();
  await f.act();
  expect(posts()).toEqual([]);
  expect(f.failed).toContain("The thread moved since the draft was written.");
});

test("a hidden panel page reads nothing on events, and reads what it missed as it shows", async () => {
  floatIdleWhileHidden();
  await floatLoad();
  const f = floatState();
  daemon.clear();
  served = queue(gateItem(1, "Ana's retry change"), gateItem(4, "Di's new review"));
  floatChange({ data: JSON.stringify({ type: "changed", gates: true, turns: true }) });
  await vi.advanceTimersByTimeAsync(DEBOUNCE_MS * 4);
  expect(daemon.urls).toEqual([]);
  f.show("peek", { item: "gate:4" });
  await vi.advanceTimersByTimeAsync(0);
  expect(daemon.urls).toEqual(["/api/queue", "/api/turns/live"]);
  expect(f.peeked && f.peeked.task.id).toBe("t-4");
  daemon.clear();
  floatChange({ data: JSON.stringify({ type: "changed", gates: true }) });
  await vi.advanceTimersByTimeAsync(DEBOUNCE_MS);
  expect(daemon.urls).toEqual(["/api/queue"]);
});

test("the arming clock starts as the host says it showed the panel, not as the page asks for it", async () => {
  await floatLoad();
  const f = floatState();
  f.show("panel");
  await vi.advanceTimersByTimeAsync(FLOAT_ARM_MS);
  expect(f.clickArmed).toBe(false);
  f.shown();
  await vi.advanceTimersByTimeAsync(FLOAT_ARM_MS - 1);
  expect(f.clickArmed).toBe(false);
  await vi.advanceTimersByTimeAsync(1);
  expect(f.clickArmed).toBe(true);
  f.close();
  f.show("panel");
  expect(f.clickArmed).toBe(false);
});
