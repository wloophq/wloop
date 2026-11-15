import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { resetStore } from "../testing/reset.svelte";
import type { J } from "../lib/util";
import { API_VERSION, getJSON, postJSON, REQUEST_TIMEOUT_MS } from "./api";
import { FakeDaemon } from "../testing/fakedaemon";
import { nav } from "./route.svelte";
import { state, tasksState } from "./store.svelte";
import { connect, DEBOUNCE_MS, load, onChange, resetSync, scheduleLoad, settled, stream, type EventSourceLike } from "./sync.svelte";

// The sync's contract, ported from deltas_test.go: a debounce gathers the stream's changes into one read, each
// change reads only what it names, a rows page merges into the rows the page holds, and a reconnect reloads.

const unit = (k: string) => ({ provider: "github", key: k });
const row = (k: string, title: string) => ({ unit: unit(k), label: k, title });
const since = (epoch: string, rev: number) => "/api/rows?since=" + encodeURIComponent(epoch + ":" + rev);
const full = () => ({ epoch: "e1", rev: 1, full: true, rows: [row("a/b#1", "one"), row("a/b#2", "two")], removed: [] });

let daemon: FakeDaemon;
let sources: (EventSourceLike & { url: string })[];

// named is a call as deltas_test.go's stubs record it: the plan, the round gates, the activity and a detail by
// name, the reads of the rows, the health, the catalog and the plugins by URL; the rest left out.
function named(urls: string[]): string[] {
  const out: string[] = [];
  for (const u of urls) {
    if (u.startsWith("/api/plan")) out.push("plan");
    else if (u === "/api/gates?status=open&kind=plan") out.push("gates");
    else if (u.startsWith("/api/activity")) out.push("activity");
    else if (/^\/api\/units\/[^/]+\/[^/]+$/.test(u) || /^\/api\/tasks\/[^/?]+$/.test(u)) out.push("detail");
    else if (u.startsWith("/api/rows") || ["/api/health", "/api/catalog", "/api/plugins"].includes(u)) out.push(u);
  }
  return out;
}

const fetched = (urls: string[]) => named(urls).filter((u) => u === "plan" || u === "gates" || u === "/api/health");
const message = (c: J) => sources[0].onmessage?.({ data: JSON.stringify({ type: "changed", epoch: "e1", ...c }) } as MessageEvent);
const titles = () => state.rows.map((r) => r.title);

async function flush(): Promise<void> {
  await vi.advanceTimersByTimeAsync(DEBOUNCE_MS);
  await settled();
}

beforeEach(() => {
  vi.useFakeTimers();
  resetStore(Date.parse("2026-11-07T12:00:00Z"), "#view=needs-me");
  resetSync();
  sources = [];
  stream.open = (url: string) => {
    const es = { url, onmessage: null, onopen: null, onerror: null, readyState: 1, close() {} } as EventSourceLike & { url: string };
    sources.push(es);
    return es;
  };
  daemon = new FakeDaemon({
    "/api/plugins": [],
    "/api/catalog": { version: "c1", views: [], facets: [], situations: [] },
    "/api/rows": full(),
    "/api/health": { viewer: "me" },
    "/api/plan": { items: [] },
    "/api/gates?status=open&kind=plan": [],
    "/api/actions": { version: "v1", specs: [] },
    "/api/views": { views: [{ id: "needs-me", label: "Needs me" }] },
  });
  daemon.fallback = (c) => {
    if (c.url.startsWith("/api/tasks?")) return { epoch: "t1", rev: 1, full: !c.url.includes("since"), tasks: [], removed: [], counters: { views: {} } };
    if (/^\/api\/units\/[^/]+\/[^/]+\/tasks$/.test(c.url)) return [];
    if (/^\/api\/units\/[^/]+\/[^/]+$/.test(c.url)) return { row: { unit: unit("a/b#1"), kind: "review" }, events: [] };
    if (/^\/api\/tasks\/[^/?]+\/timeline/.test(c.url)) return [];
    if (/^\/api\/tasks\/[^/?]+$/.test(c.url)) return { id: decodeURIComponent(c.url.slice("/api/tasks/".length)), category: "next", gates: [] };
    throw new Error("unexpected");
  };
});

afterEach(() => {
  resetSync();
  vi.useRealTimers();
  vi.restoreAllMocks();
});

// boot loads the page, opens the stream and forgets what the boot read, as runDelta does.
async function boot(): Promise<void> {
  await load();
  connect();
  sources[0].onopen?.();
  daemon.clear();
}

describe("the sync", () => {
  test("the boot reads the rows once", async () => {
    await load();
    connect();
    sources[0].onopen?.();
    await flush();
    expect(daemon.urls.filter((u) => u.startsWith("/api/rows"))).toEqual(["/api/rows"]);
  });

  test("a change is read once the stream has been quiet for the debounce", async () => {
    await boot();
    daemon.pages[since("e1", 1)] = { epoch: "e1", rev: 2, full: false, rows: [row("a/b#2", "two again")], removed: [] };
    message({ rev: 2, units: [unit("a/b#2")] });
    await vi.advanceTimersByTimeAsync(DEBOUNCE_MS - 1);
    expect(daemon.urls).toEqual([]);
    message({ rev: 2, units: [unit("a/b#2")] });
    await vi.advanceTimersByTimeAsync(DEBOUNCE_MS - 1);
    expect(daemon.urls).toEqual([]);
    await vi.advanceTimersByTimeAsync(1);
    await settled();
    expect(named(daemon.urls)).toEqual([since("e1", 1)]);
  });

  test("merges a delta", async () => {
    await boot();
    daemon.pages[since("e1", 1)] = { epoch: "e1", rev: 3, full: false, rows: [row("a/b#2", "two again"), row("a/b#3", "three")], removed: [unit("a/b#1")] };
    message({ rev: 3, units: [unit("a/b#1"), unit("a/b#2"), unit("a/b#3")] });
    await flush();
    expect(named(daemon.urls)).toEqual(["/api/rows?since=e1%3A1"]);
    expect(titles()).toEqual(["two again", "three"]);
    expect(state.rev).toBe(3);
  });

  test("rereads the detail only for its unit, whatever the case", async () => {
    nav.hash = "#view=needs-me&unit=github/a%2Fb%231";
    await boot();
    daemon.pages[since("e1", 1)] = { epoch: "e1", rev: 2, full: false, rows: [row("a/b#2", "two")], removed: [] };
    message({ rev: 2, units: [unit("a/b#2")] });
    await flush();
    daemon.pages[since("e1", 2)] = { epoch: "e1", rev: 3, full: false, rows: [row("a/b#1", "one")], removed: [] };
    message({ rev: 3, units: [unit("a/b#1")] });
    await flush();
    message({ rev: 3, health: true });
    await flush();
    expect(named(daemon.urls)).toEqual(["/api/rows?since=e1%3A1", "/api/rows?since=e1%3A2", "detail", "/api/health"]);

    nav.hash = "#view=needs-me&unit=github/A%2FB%231";
    daemon.clear();
    daemon.pages[since("e1", 3)] = { epoch: "e1", rev: 4, full: false, rows: [row("a/b#1", "one")], removed: [] };
    message({ rev: 4, units: [unit("a/b#1")] });
    await flush();
    expect(named(daemon.urls)).toContain("detail");
  });

  test("a change that moves the gates rereads the open task", async () => {
    nav.hash = "#view=needs-me&task=draft:1626";
    await boot();
    daemon.pages[since("e1", 1)] = { epoch: "e1", rev: 1, full: false, rows: [], removed: [] };
    message({ rev: 1, gates: true, units: [unit("a/b#1")] });
    await flush();
    expect(named(daemon.urls)).toContain("detail");
  });

  test("an all message reloads whole", async () => {
    await boot();
    daemon.pages["/api/rows"] = { epoch: "e2", rev: 1, full: true, rows: [row("a/b#9", "nine")], removed: [] };
    message({ epoch: "e2", rev: 1, all: true });
    await flush();
    expect(daemon.urls).toContain("/api/rows");
    expect(daemon.urls.some((u) => u.includes("since"))).toBe(false);
    expect(titles()).toEqual(["nine"]);
  });

  test("falls back to a full load when a page cannot merge", async () => {
    await boot();
    daemon.pages["/api/rows"] = { epoch: "e2", rev: 1, full: true, rows: [row("a/b#9", "nine")], removed: [] };
    daemon.pages[since("e1", 1)] = { epoch: "e2", rev: 2, full: false, rows: [row("a/b#1", "stale")], removed: [] };
    message({ rev: 2, units: [unit("a/b#1")] });
    await flush();
    message({ epoch: "e2", rev: 3, units: [unit("a/b#9")] });
    await flush();
    const load = ["/api/plugins", "/api/catalog", "/api/rows", "/api/health"];
    expect(named(daemon.urls).filter((u) => u.startsWith("/api/"))).toEqual([since("e1", 1), ...load, since("e2", 1), ...load]);
    expect(titles()).toEqual(["nine"]);
  });

  test("a reopened stream reads everything the page holds from the rev", async () => {
    await boot();
    daemon.pages[since("e1", 1)] = { epoch: "e1", rev: 1, full: false, rows: [], removed: [] };
    sources[0].onopen?.();
    await flush();
    expect(named(daemon.urls)).toEqual([since("e1", 1), "/api/health", "plan", "gates"]);
  });

  test.each([
    ["health", (_: unknown) => message({ rev: 1, health: true }), ["/api/health"]],
    ["rows", () => {
      daemon.pages[since("e1", 1)] = { epoch: "e1", rev: 2, full: false, rows: [row("a/b#2", "two")], removed: [] };
      message({ rev: 2, units: [unit("a/b#2")] });
    }, []],
    ["plan", () => message({ rev: 1, plan: true }), ["plan"]],
    ["gates", () => message({ rev: 1, gates: true }), ["gates"]],
    ["notify", () => sources[0].onmessage?.({ data: JSON.stringify({ type: "notify", epoch: "e1", rev: 1, notices: [{ id: "n1", text: "hi" }] }) } as MessageEvent), []],
    ["a row the plan holds", () => {
      state.plan.items = [{ id: "i1", unit: unit("a/b#2") }];
      daemon.pages[since("e1", 1)] = { epoch: "e1", rev: 2, full: false, rows: [row("a/b#2", "two")], removed: [] };
      message({ rev: 2, units: [unit("a/b#2")] });
    }, ["plan"]],
  ] as [string, (x?: unknown) => void, string[]][])("a %s change reads only what it names", async (_, send, want) => {
    await boot();
    send();
    await flush();
    expect(fetched(daemon.urls)).toEqual(want);
  });

  test("coalesces messages into one pull and drops an older page", async () => {
    await boot();
    daemon.pages[since("e1", 1)] = { epoch: "e1", rev: 3, full: false, rows: [row("a/b#1", "uno"), row("a/b#2", "dos")], removed: [] };
    message({ rev: 2, units: [unit("a/b#1")] });
    message({ rev: 3, units: [unit("a/b#2")] });
    await flush();
    daemon.pages[since("e1", 3)] = { epoch: "e1", rev: 2, full: false, rows: [row("a/b#1", "older")], removed: [] };
    message({ rev: 2, units: [unit("a/b#1")] });
    await flush();
    expect(daemon.urls.filter((u) => u.startsWith("/api/rows"))).toHaveLength(2);
    expect(titles()).toEqual(["uno", "dos"]);
    expect(state.rev).toBe(3);
  });

  test("a delta that succeeds clears the banner", async () => {
    await boot();
    state.error = "Could not reach the daemon: boom";
    daemon.pages[since("e1", 1)] = { epoch: "e1", rev: 1, full: false, rows: [], removed: [] };
    sources[0].onopen?.();
    await flush();
    expect(state.error).toBe("");
  });

  test("a load keeps newer rows from an older page of the same epoch", async () => {
    await boot();
    state.rev = 5;
    daemon.pages["/api/rows"] = { epoch: "e1", rev: 3, full: true, rows: [row("a/b#9", "older")], removed: [] };
    await load();
    expect(titles().join(",") + "@" + state.rev).toBe("one,two@5");
    daemon.pages["/api/rows"] = { epoch: "e2", rev: 1, full: true, rows: [row("a/b#9", "other epoch")], removed: [] };
    await load();
    expect(titles().join(",") + "@" + state.rev + "/" + state.epoch).toBe("other epoch@1/e2");
  });

  test("a change with counters rereads the tasks of its units", async () => {
    await boot();
    daemon.pages[since("e1", 1)] = { epoch: "e1", rev: 2, full: false, rows: [row("a/b#1", "one")], removed: [] };
    message({ rev: 2, units: [unit("a/b#1")], counters: { views: {} } });
    await flush();
    expect(daemon.urls.filter((u) => u.startsWith("/api/tasks?"))).toEqual(["/api/tasks?view=needs-me&since=t1%3A1&units=github%3Aa%2Fb%231"]);
  });

  test("an all change and a reconnect read the tasks whole", async () => {
    await boot();
    message({ rev: 2, all: true, counters: { views: {} } });
    await flush();
    expect(daemon.urls.filter((u) => u.startsWith("/api/tasks?"))).toEqual(["/api/tasks?view=needs-me"]);
    daemon.clear();
    daemon.pages[since("e1", 1)] = { epoch: "e1", rev: 1, full: false, rows: [], removed: [] };
    sources[0].onopen?.();
    await flush();
    expect(daemon.urls.filter((u) => u.startsWith("/api/tasks?"))).toEqual(["/api/tasks?view=needs-me"]);
  });

  test("a rows page of another catalog rereads the catalog", async () => {
    await boot();
    daemon.pages[since("e1", 1)] = { epoch: "e1", rev: 2, full: false, rows: [], removed: [], catalog: "c1" };
    message({ rev: 2, units: [unit("a/b#1")] });
    await flush();
    daemon.pages[since("e1", 2)] = { epoch: "e1", rev: 3, full: false, rows: [], removed: [], catalog: "c2" };
    message({ rev: 3, units: [unit("a/b#1")] });
    await flush();
    expect(named(daemon.urls)).toEqual([since("e1", 1), since("e1", 2), "/api/catalog"]);
  });

  test("a message that is no JSON reloads whole, one that names nothing reads nothing", async () => {
    await boot();
    onChange({ data: JSON.stringify({ type: "changed", rev: 1 }) });
    await flush();
    expect(daemon.urls).toEqual([]);
    onChange({ data: "{" });
    await flush();
    expect(daemon.urls).toContain("/api/rows");
  });

  test("scheduleLoad with no change reloads whole", async () => {
    await boot();
    scheduleLoad();
    await flush();
    expect(daemon.urls).toContain("/api/rows");
  });

  test("the stream asks for the page's API version and reconnects with a backoff", async () => {
    await boot();
    expect(sources[0].url).toBe(`/api/events?api=${API_VERSION}`);
    expect(state.conn).toBe("open");
    sources[0].onerror?.();
    expect(state.conn).toBe("reconnecting");
    expect(sources).toHaveLength(1);
    const waits: number[] = [];
    for (let i = 0; i < 7; i++) {
      const es = sources[sources.length - 1];
      es.readyState = stream.CLOSED;
      es.onerror?.();
      expect(state.conn).toBe("closed");
      const before = sources.length;
      let waited = 0;
      while (sources.length === before) {
        await vi.advanceTimersByTimeAsync(1000);
        waited += 1000;
      }
      waits.push(waited);
    }
    expect(waits).toEqual([1000, 2000, 4000, 8000, 16000, 30000, 30000]);
    sources[sources.length - 1].onopen?.();
    expect(state.conn).toBe("open");
    sources[sources.length - 1].readyState = stream.CLOSED;
    sources[sources.length - 1].onerror?.();
    const before = sources.length;
    await vi.advanceTimersByTimeAsync(1000);
    expect(sources.length).toBe(before + 1);
  });
});

describe("the requests", () => {
  test("carry the API version", async () => {
    daemon.pages["/x"] = { ok: true };
    daemon.pages["/y"] = { ok: true };
    await getJSON("/x");
    await postJSON("/y", { a: 1 });
    for (const c of daemon.calls) expect((c.init?.headers as Record<string, string>)["X-Loop-API"]).toBe(API_VERSION);
    expect(daemon.calls[1].init?.method).toBe("POST");
    expect(daemon.calls[1].body).toEqual({ a: 1 });
  });

  test("a read gives up after the request timeout", async () => {
    const signal = new AbortController().signal;
    const timeout = vi.spyOn(AbortSignal, "timeout").mockReturnValue(signal);
    daemon.pages["/x"] = {};
    await getJSON("/x");
    expect(timeout).toHaveBeenCalledWith(REQUEST_TIMEOUT_MS);
    expect(daemon.calls[0].init?.signal).toBe(signal);
  });

  test("a hung read is aborted", async () => {
    const ctl = new AbortController();
    vi.spyOn(AbortSignal, "timeout").mockReturnValue(ctl.signal);
    const { net } = await import("./api");
    net.fetch = (_url, init) => new Promise((_, reject) => init?.signal?.addEventListener("abort", () => reject(new Error("aborted"))));
    const read = getJSON("/x");
    ctl.abort();
    await expect(read).rejects.toThrow("aborted");
  });

  test("a 401 tells the page its authorization is lost", async () => {
    daemon.pages["/x"] = () => {
      throw new Error("x");
    };
    const { Reply } = await import("../testing/fakedaemon");
    daemon.pages["/x"] = new Reply(401, {});
    await expect(getJSON("/x")).rejects.toThrow();
    expect(state.authLost).toBe(true);
  });
});

describe("reconcile through the sync", () => {
  test("after a delta the rows that did not change keep their object, the changed one too", async () => {
    await boot();
    const [one, two] = state.rows;
    daemon.pages[since("e1", 1)] = { epoch: "e1", rev: 2, full: false, rows: [row("a/b#2", "two again"), row("a/b#3", "three")], removed: [] };
    message({ rev: 2, units: [unit("a/b#2"), unit("a/b#3")] });
    await flush();
    expect(titles()).toEqual(["one", "two again", "three"]);
    expect(state.rows[0]).toBe(one);
    expect(state.rows[1]).toBe(two);
  });

  test("a full load keeps the object of every row it serves again", async () => {
    await boot();
    const [one, two] = state.rows;
    daemon.pages["/api/rows"] = { epoch: "e1", rev: 2, full: true, rows: [row("a/b#2", "two"), row("a/b#1", "one, renamed")], removed: [] };
    await load();
    expect(state.rows[0]).toBe(two);
    expect(state.rows[1]).toBe(one);
    expect(one.title).toBe("one, renamed");
  });

  test("a tasks page keeps the tasks it does not change", async () => {
    await boot();
    tasksState.page = null;
    daemon.fallback = ((prev) => (c) => {
      if (c.url === "/api/tasks?view=needs-me") return { epoch: "t1", rev: 1, full: true, tasks: [{ id: "e-1", title: "One" }, { id: "e-2", title: "Two" }], removed: [], counters: { views: {} } };
      if (c.url.startsWith("/api/tasks?view=needs-me&since=t1%3A1")) return { epoch: "t1", rev: 2, full: false, tasks: [{ id: "e-2", title: "Two again" }], removed: [], counters: { views: {} } };
      return prev(c);
    })(daemon.fallback);
    message({ rev: 1, all: true });
    await flush();
    const [a, b] = tasksState.tasks;
    message({ rev: 1, counters: { views: {} } });
    await flush();
    expect(tasksState.tasks.map((e) => e.title)).toEqual(["One", "Two again"]);
    expect(tasksState.tasks[0]).toBe(a);
    expect(tasksState.tasks[1]).toBe(b);
  });
});
