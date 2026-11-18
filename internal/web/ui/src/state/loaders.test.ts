import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { actionStates } from "../lib/actions";
import { lensOf, pipelineOf } from "../lib/lens";
import { taskLensItems } from "../lib/tasks";
import { resetStore } from "../testing/reset.svelte";
import { FakeDaemon } from "../testing/fakedaemon";
import { servedCatalog, task, viewsCatalog } from "../testing/fixtures";
import { applyCatalog } from "../lib/catalog";
import { fieldOf, viewQueryKey } from "../lib/lens";
import { FILTER_COUNT_DELAY_MS, forgetTasksPage, loadFiltered, loadRoundGates, loadTasks, refreshSpecs, viewCounters } from "./loaders.svelte";
import type { J } from "../lib/util";
import { readHash } from "./route.svelte";
import { state, tasksState } from "./store.svelte";
import { load, resetSync } from "./sync.svelte";

let daemon: FakeDaemon;

beforeEach(() => {
  resetStore();
  resetSync();
  daemon = new FakeDaemon();
});

afterEach(() => {
  resetSync();
  vi.useRealTimers();
});

describe("the task lists", () => {
  test("the first load resolves a core view once its catalog is in", async () => {
    resetStore(undefined, "#view=focus");
    daemon.pages["/api/views"] = viewsCatalog();
    daemon.fallback = () => ({ epoch: "e", rev: 1, full: true, tasks: [], removed: [], counters: { views: {} } });
    await loadTasks();
    expect(daemon.urls).toEqual(["/api/views", "/api/tasks?view=focus"]);
  });

  test("a views version change rereads the views catalog", async () => {
    const page = (rev: number, specs: string) => ({ epoch: "ep", rev, full: rev === 1, tasks: [], removed: [], counters: { views: {} }, specs });
    const pages = [page(1, "v1"), page(2, "v2"), page(3, "v2")];
    const cats = [{ ...viewsCatalog(), version: "v1" }, { ...viewsCatalog(), version: "v2" }];
    daemon.fallback = (c) => (c.url === "/api/views" ? cats.shift() : pages.shift());
    const route = { ...readHash(), view: "inbox", area: "", ctx: [] };
    await loadTasks(route);
    await loadTasks(route);
    await loadTasks(route);
    expect(daemon.urls).toEqual(["/api/views", "/api/tasks?view=inbox", "/api/tasks?view=inbox&since=ep%3A1", "/api/views", "/api/tasks?view=inbox&since=ep%3A2"]);
    expect(tasksState.catalog.version).toBe("v2");
  });

  test("a tasks page since names the units until it is read", async () => {
    let fail = false;
    daemon.fallback = (c) => {
      if (c.url === "/api/views") return viewsCatalog();
      if (fail) throw new Error("down");
      return { epoch: "ep", rev: 1, full: !c.url.includes("since"), tasks: [], removed: [], counters: { views: {} } };
    };
    const route = { ...readHash(), view: "inbox", area: "", ctx: [] };
    await loadTasks(route);
    fail = true;
    await loadTasks(route, ["github:a/b#1"]);
    fail = false;
    await loadTasks(route, ["slack:T1/C1/1"]);
    await loadTasks(route);
    const held = "/api/tasks?view=inbox&since=ep%3A1&units=github%3Aa%2Fb%231&units=slack%3AT1%2FC1%2F1";
    expect(daemon.urls).toEqual(["/api/views", "/api/tasks?view=inbox", "/api/tasks?view=inbox&since=ep%3A1&units=github%3Aa%2Fb%231", held, "/api/tasks?view=inbox&since=ep%3A1"]);
  });

  const full = (view: string, ...tasks: J[]) => ({ epoch: "ep", rev: 1, full: true, tasks, removed: [], counters: { views: {} }, view });
  const routeOf = (view: string) => ({ ...readHash(), view, area: "", ctx: [] });

  test("coming back to a view shows its list at once and reads only what changed since", async () => {
    daemon.pages["/api/views"] = viewsCatalog();
    daemon.pages["/api/tasks?view=inbox"] = full("inbox", task("i1", "inbox"));
    daemon.pages["/api/tasks?view=focus"] = full("focus", task("f1", "next"));
    await loadTasks(routeOf("inbox"));
    await loadTasks(routeOf("focus"));
    let shown: string[] = [];
    daemon.pages["/api/tasks?view=inbox&since=ep%3A1"] = () => {
      shown = [tasksState.key, ...tasksState.tasks.map((t) => t.id)];
      return { epoch: "ep", rev: 2, full: false, tasks: [task("i2", "inbox")], removed: [], counters: { views: {} } };
    };
    await loadTasks(routeOf("inbox"));
    expect(shown).toEqual([viewQueryKey("inbox", "", []), "i1"]);
    expect(tasksState.tasks.map((t) => t.id)).toEqual(["i1", "i2"]);
    await loadTasks(routeOf("focus"));
    expect(daemon.urls[daemon.urls.length - 1]).toBe("/api/tasks?view=focus&since=ep%3A1");
    expect(tasksState.tasks.map((t) => t.id)).toEqual(["f1"]);
  });

  test("a list that arrives after its view was left does not replace the one shown", async () => {
    daemon.pages["/api/views"] = viewsCatalog();
    let release = (_: J) => {};
    daemon.pages["/api/tasks?view=inbox"] = () => new Promise((r) => (release = r));
    daemon.pages["/api/tasks?view=focus"] = full("focus", task("f1", "next"));
    const slow = loadTasks(routeOf("inbox"));
    await loadTasks(routeOf("focus"));
    release(full("inbox", task("i1", "inbox")));
    await slow;
    expect([tasksState.key, ...tasksState.tasks.map((t) => t.id)]).toEqual([viewQueryKey("focus", "", []), "f1"]);
  });

  test("the Outbox reads its drafts beside its list, not after it", async () => {
    daemon.pages["/api/views"] = { ...viewsCatalog(), views: viewsCatalog().views.map((v: J) => (v.id === "outbox" ? { ...v, layout: "outbox" } : v)) };
    let release = (_: J) => {};
    daemon.pages["/api/tasks?view=outbox"] = () => new Promise((r) => (release = r));
    daemon.pages["/api/outbox"] = { drafts: [] };
    const read = loadTasks(routeOf("outbox"));
    await vi.waitFor(() => expect(daemon.urls).toContain("/api/outbox"));
    release(full("outbox"));
    await read;
  });

  test("a reconnect forgets every list it kept", async () => {
    daemon.pages["/api/views"] = viewsCatalog();
    daemon.pages["/api/tasks?view=inbox"] = full("inbox", task("i1", "inbox"));
    daemon.pages["/api/tasks?view=focus"] = full("focus", task("f1", "next"));
    await loadTasks(routeOf("inbox"));
    await loadTasks(routeOf("focus"));
    forgetTasksPage();
    await loadTasks(routeOf("inbox"));
    expect(daemon.urls[daemon.urls.length - 1]).toBe("/api/tasks?view=inbox");
  });
});

describe("the round gates", () => {
  test("the countdowns ask for the open plan gates only", async () => {
    daemon.pages["/api/gates?status=open&kind=plan"] = [
      { id: 1, kind: "plan", unit: "github:acme/api#1", deadline: "2026-10-30T12:00:00Z" },
      { id: 2, kind: "plan", unit: "github:acme/api#2", deadline: "0001-01-01T00:00:00Z" },
      { id: 3, kind: "plan", unit: "", deadline: "2026-10-30T12:00:00Z" },
    ];
    await loadRoundGates();
    expect(daemon.urls).toEqual(["/api/gates?status=open&kind=plan"]);
    expect([...state.roundGates.keys()]).toEqual(["github:acme/api#1"]);
  });
});

describe("the filtered counters", () => {
  const whole = { views: { focus: 1, inbox: 3, outbox: 0, "fits-now": 3, waiting: 0, projects: 0, someday: 0, logbook: 4 }, areas: { OSS: 2, Hiring: 1 }, contexts: { "@decide": 1, "@reply": 2 } };
  const narrowed = { views: { focus: 0, inbox: 1, outbox: 0, "fits-now": 2, waiting: 0, projects: 0, someday: 0, logbook: 1 }, areas: { OSS: 2 }, contexts: { "@reply": 2 } };

  test("the filter narrows the view counts, the latest read wins and an area's counts are its own", async () => {
    vi.useFakeTimers();
    let answer: unknown = null;
    daemon.fallback = () => answer;
    tasksState.catalog = viewsCatalog();
    tasksState.counters = whole;
    const route = { view: "fits-now", area: "", ctx: [] as string[], task: "", q: "invoice" };
    const settle = async (p: Promise<void>) => {
      await vi.advanceTimersByTimeAsync(FILTER_COUNT_DELAY_MS);
      await p;
    };
    expect(viewCounters(route)).toBeNull();
    answer = narrowed;
    await settle(loadFiltered(route));
    expect(viewCounters(route)).toEqual(narrowed);
    answer = whole;
    const slow = loadFiltered({ ...route, q: "invoic" });
    answer = narrowed;
    await loadFiltered(route);
    await settle(slow);
    expect(viewCounters(route)).toEqual(narrowed);
    expect(viewCounters({ ...route, q: "" })).toEqual(whole);
    await settle(loadFiltered({ ...route, area: "OSS", q: 'invoice "the client"' }));
    expect(daemon.urls[0]).toBe("/api/counters?q=invoice");
    expect(daemon.urls[daemon.urls.length - 1]).toBe("/api/counters?area=OSS&q=invoice+%22the+client%22");
    expect(tasksState.filtered.area).toBe("OSS");
    expect(viewCounters(route)).toBeNull();
  });

  test("the filter keeps the tasks the daemon counts", () => {
    applyCatalog(servedCatalog(), fieldOf);
    tasksState.catalog = viewsCatalog();
    tasksState.key = viewQueryKey("fits-now", "", []);
    tasksState.tasks = [
      task("n1", "next", { title: "Invoice the client", props: { area: "OSS", contexts: ["@reply"] } }),
      task("n2", "next", { title: "Review invoice terms", props: { area: "OSS", contexts: ["@reply"] } }),
      task("n3", "next", { title: "Call Bob", props: { contexts: ["@decide"] } }),
    ];
    const lens = lensOf({ ...readHash(), view: "fits-now", q: "invoice" });
    const res = pipelineOf(taskLensItems(), lens, null);
    expect([res.shown, res.total]).toEqual([2, 3]);
    expect(res.group).toBe("");
  });

  test("a failed read leaves the rail without counts", async () => {
    vi.useFakeTimers();
    let fail = false;
    daemon.fallback = () => {
      if (fail) throw new Error("HTTP 500");
      return { views: { "fits-now": 2 } };
    };
    tasksState.catalog = viewsCatalog();
    const read = async (q: string) => {
      const p = loadFiltered({ area: "", q });
      await vi.advanceTimersByTimeAsync(FILTER_COUNT_DELAY_MS);
      await p;
    };
    await read("invoice");
    expect(viewCounters({ area: "", q: "invoice" }).views["fits-now"]).toBe(2);
    fail = true;
    await read("invoices");
    expect(viewCounters({ area: "", q: "invoices" })).toBeNull();
  });
});

describe("the action specs", () => {
  test("are read again when the version changes", async () => {
    let served = { version: "v1", specs: [{ id: "run_now", label: "Run now" }] };
    daemon.fallback = (c) => {
      if (c.url !== "/api/actions") throw new Error(c.url);
      return served;
    };
    const versions: string[] = [];
    await refreshSpecs([{ specs_version: "v1" }, { specs_version: "v1" }]);
    versions.push(state.specs.version);
    await refreshSpecs([{ specs_version: "v1" }]);
    versions.push(state.specs.version);
    served = { version: "v2", specs: [{ id: "run_now", label: "Go now" }] };
    await refreshSpecs([{ specs_version: "v2" }]);
    versions.push(state.specs.version);
    await refreshSpecs([]);
    expect(daemon.urls).toHaveLength(2);
    expect(versions).toEqual(["v1", "v1", "v2"]);
    expect(state.specs.byId.run_now.label).toBe("Go now");
  });

  const boot = (listed: () => string, actionsFail: () => boolean, rows: () => unknown[]) => {
    daemon.fallback = (c) => {
      if (c.url === "/api/rows") return { rows: rows() };
      if (c.url === "/api/health") return {};
      if (c.url === "/api/actions") {
        if (actionsFail()) throw new Error("boom");
        return { version: listed(), specs: [{ id: "open", label: "Open", surfaces: ["row"] }] };
      }
      if (c.url === "/api/plugins") return [];
      if (c.url === "/api/catalog") return { views: [], facets: [], situations: [] };
      if (c.url === "/api/views") return { views: [] };
      if (c.url.startsWith("/api/plan")) return { items: [] };
      if (c.url.startsWith("/api/gates")) return [];
      if (c.url.startsWith("/api/counters")) return { views: {} };
      throw new Error(c.url);
    };
  };

  test("a load refreshes them and keeps them when the read fails", async () => {
    let listed = "v1";
    let fail = false;
    const row = (v: string) => ({ label: "a/b#1", unit: { provider: "github", key: "a/b#1" }, specs_version: v, actions: [{ id: "open", enabled: true }] });
    boot(() => listed, () => fail, () => [row(listed)]);
    const versions: string[] = [];
    await load();
    versions.push(state.specs.version);
    listed = "v2";
    fail = true;
    await load();
    versions.push(state.specs.version);
    expect(daemon.urls.filter((u) => u === "/api/actions")).toHaveLength(2);
    expect(versions).toEqual(["v1", "v1"]);
    expect(actionStates(state.rows[0], "row").map(([st, sp]) => st.label || sp.label)).toEqual(["Open"]);
    expect(state.error).toContain("Could not load the actions: boom");
  });

  test("load with an empty inventory still reads them, once", async () => {
    boot(() => "v1", () => false, () => []);
    await load();
    await load();
    expect(daemon.urls.filter((u) => u === "/api/actions")).toHaveLength(1);
    expect(state.specs.version).toBe("v1");
  });
});
