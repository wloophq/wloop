import { cleanup, fireEvent, render } from "@testing-library/svelte";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { flushSync } from "svelte";
import { fieldOf, groupedRows, lensOf, matches, NO_GROUP, pipelineOf, viewQueryKey } from "../../lib/lens";
import { taskItemOf, taskLensItems } from "../../lib/tasks";
import type { J } from "../../lib/util";
import { net } from "../../state/api";
import { applyTasksPage } from "../../state/loaders.svelte";
import { closeOverlays, overlays } from "../../state/overlays.svelte";
import { nav, readHash } from "../../state/route.svelte";
import { catalog, collapsed, notice, outboxState, state, tasksState } from "../../state/store.svelte";
import { resetStore } from "../../testing/reset.svelte";
import { fixture, task, useCatalogs } from "../rows/testdata/fixtures";
import TaskViewPage from "./TaskViewPage.svelte";

const NOW = Date.parse("2026-11-04T12:00:00Z");

afterEach(() => {
  cleanup();
  closeOverlays();
});

// viewsCatalog is GET /api/views with the core views the daemon serves.
function viewsCatalog(): J {
  const views = ["focus", "inbox", "outbox", "fits-now", "waiting", "projects", "someday", "logbook"].map((id) => ({ id, label: id[0].toUpperCase() + id.slice(1).replace("-", " "), rail: true }));
  return { views, areas: ["OSS", "Hiring"], contexts: ["@decide", "@reply"], verbs: [{ verb: "approve", handler: "approve", label: "Approve" }, { verb: "edit", handler: "edit", label: "Edit" },
    { verb: "dismiss", handler: "drop", label: "Discard" }, { verb: "retry", handler: "retry", label: "Retry" }] };
}

function mixed(hash: string, tasks: J[] = fixture("mixed-tasks.json"), cat: J = fixture("views.json")) {
  resetStore(NOW, hash);
  useCatalogs(fixture("catalog.github.json"), cat);
  catalog.fields = [...catalog.fields, ...fixture("slack-facets.json").map(fieldOf)];
  const r = readHash();
  tasksState.key = viewQueryKey(r.view, r.area, r.ctx);
  tasksState.tasks = tasks;
  tasksState.rev++;
}

const page = () => render(TaskViewPage).container;
const body = (c: Element) => c.querySelector("#tasks-body")!;

describe("a task view", () => {
  it("groups by source and filters", () => {
    mixed("#view=needs-me&q=" + encodeURIComponent("where:general") + "&g=source");
    const c = page();
    const groups = Array.from(body(c).querySelectorAll(".rows > section.group"));
    expect(groups.length).toBe(1);
    expect(groups[0].querySelector(".g-toggle")!.textContent).toBe("▾ Slack");
    expect(body(c).textContent).toContain("Answer the deploy question");
    expect(body(c).textContent).not.toContain("general settings");
    expect(Array.from(body(c).querySelectorAll("[data-row]")).map((n) => n.getAttribute("data-row"))).toEqual(["task:s-1"]);
  });

  it("heads each group with its count, and shows a task's reason on its row alone", () => {
    mixed("#view=needs-me&g=source", fixture("mixed-tasks.json").map((e: J) => (e.id === "g-1" ? { ...e, reason: "checks failing" } : e)));
    const c = page();
    const groups = Array.from(body(c).querySelectorAll(".rows > section.group"));
    expect(groups.length).toBeGreaterThanOrEqual(2);
    for (const s of groups) {
      const toggle = s.querySelector(".g-toggle")!;
      expect(toggle.textContent).not.toBe("");
      expect(s.querySelector(".count")!.textContent).toBe(String(s.querySelectorAll(".row").length));
      expect(toggle.getAttribute("aria-controls")).toBe(s.id);
    }
    expect(Array.from(body(c).querySelectorAll(".why")).map((w) => w.textContent)).toEqual(["checks failing"]);
  });

  it("draws a lane per group on a board, each card's title its focus", () => {
    mixed("#view=needs-me&g=source&layout=board");
    const lanes = Array.from(body(page()).querySelectorAll(".board .lane")).map((l) => ({
      label: l.querySelector(".lane-head")!.firstElementChild!.textContent,
      focus: Array.from(l.querySelectorAll("li")).map((li) => (li.querySelector(".open")!.getAttribute("data-focus") === li.getAttribute("data-row") ? li.getAttribute("data-row") : "")),
    }));
    expect(lanes).toEqual([{ label: "GitHub", focus: ["task:g-1", "task:g-2"] }, { label: "Slack", focus: ["task:s-1", "task:s-2"] }]);
  });

  it("groups by its view's own group, and by nothing when asked", () => {
    mixed("#view=waiting");
    const c = page();
    expect(Array.from(body(c).querySelectorAll(".g-toggle")).map((b) => b.textContent!.slice(2))).toEqual(["bia", "me"]);
    nav.hash = "#view=waiting&g=" + NO_GROUP;
    flushSync();
    expect(body(c).querySelectorAll(".g-toggle").length).toBe(0);
  });

  it("draws an ungrouped board as one lane titled by its view", () => {
    mixed("#view=needs-me&layout=board&g=" + NO_GROUP);
    expect(Array.from(body(page()).querySelectorAll(".lane-head")).map((h) => h.firstElementChild!.textContent)).toEqual(["Needs me"]);
  });

  it("groups its board by the profile's strip facet, and not at all without it", () => {
    mixed("#view=needs-me&layout=board");
    const c = page();
    expect(Array.from(body(c).querySelectorAll(".lane")).length).toBeGreaterThan(0);
    expect(body(c).querySelector(".lane-head")!.className).toMatch(/w-/);
    tasksState.tasks = fixture("mixed-tasks.json").map((e: J) => ({ ...e, facets: { source: e.facets.source } }));
    tasksState.rev++;
    flushSync();
    expect(Array.from(body(c).querySelectorAll(".lane-head")).map((h) => h.firstElementChild!.textContent)).toEqual(["Needs me"]);
  });

  it("gives a task in two groups a focus slot per copy", () => {
    mixed("#view=needs-me&g=tag:lang", fixture("mixed-tasks.json").map((e: J) => (e.id === "g-1" ? { ...e, facets: { ...e.facets, tag: ["lang:go", "lang:rust"] } } : e)));
    const focus = Array.from(body(page()).querySelectorAll("[data-row]")).map((n) => n.getAttribute("data-focus"));
    expect(focus).toEqual(expect.arrayContaining(["task:g-1@lang:go", "task:g-1@lang:rust"]));
  });

  it("groups states and contexts as the bundled profile declares", () => {
    const mk = (id: string, st: string, ctx: string) => task(id, st, (e) => {
      e.title = id;
      e.facets = { state: [st], ...(ctx ? { contexts: [ctx] } : {}) };
    });
    mixed("#view=needs-me&g=state", [mk("a", "done", ""), mk("b", "inbox", "computer"), mk("c", "next", "calls"), mk("d", "scheduled", "")]);
    const c = page();
    expect(Array.from(body(c).querySelectorAll(".g-toggle")).map((b) => b.textContent!.slice(2))).toEqual(["Inbox", "Next", "Scheduled", "Done"]);
    nav.hash = "#view=needs-me&g=contexts";
    flushSync();
    expect(Array.from(body(c).querySelectorAll(".g-toggle")).map((b) => b.textContent!.slice(2))).toEqual(["calls", "computer", "no context"]);
  });

  it("lists the tasks the daemon would count for the same query", () => {
    const want: Record<string, string[]> = { "where:general": ["s-1"], "source:github ci:failure": ["g-1"], "-source:slack": ["g-1", "g-2"], "tag:lang": ["g-1"], "conversation:thread": ["s-1"], "nothing:here": [] };
    for (const [q, ids] of Object.entries(want)) {
      mixed("#view=needs-me&q=" + encodeURIComponent(q));
      expect(groupedRows(pipelineOf(taskLensItems(), lensOf(readHash()), null).groups).map((it: J) => it.task.id).sort(), q).toEqual(ids);
    }
  });

  it("keeps a row's node and a collapsed group across a sync, and its board's scroll", async () => {
    mixed("#view=needs-me&g=source");
    const c = page();
    const row = body(c).querySelector('[data-row="task:g-1"]')!;
    const toggle = body(c).querySelector('[data-focus="group:source:slack"]') as HTMLButtonElement;
    await fireEvent.click(toggle);
    applyTasksPage(viewQueryKey("needs-me", "", []), { epoch: "e", rev: 2, full: true, tasks: fixture("mixed-tasks.json").map((e: J) => (e.id === "g-1" ? { ...e, title: "Fix the upload test" } : e)) });
    flushSync();
    expect(body(c).querySelector('[data-row="task:g-1"]')).toBe(row);
    expect(row.querySelector(".t")!.textContent).toBe("Fix the upload test");
    expect(toggle.getAttribute("aria-expanded")).toBe("false");
    expect(collapsed.has("source:slack")).toBe(true);
    nav.hash = "#view=needs-me&g=source&layout=board";
    flushSync();
    const board = body(c).querySelector(".board") as HTMLElement;
    board.scrollLeft = 200;
    board.scrollTop = 30;
    applyTasksPage(viewQueryKey("needs-me", "", []), { epoch: "e", rev: 3, full: true, tasks: fixture("mixed-tasks.json") });
    flushSync();
    expect(body(c).querySelector(".board")).toBe(board);
    expect([board.scrollLeft, board.scrollTop]).toEqual([200, 30]);
  });
});

describe("a view's head", () => {
  it("picks an area with its counts and shows its context chips", async () => {
    resetStore(NOW, "#view=inbox&area=OSS");
    tasksState.catalog = viewsCatalog();
    tasksState.counters = { areas: { OSS: 3 }, contexts: { "@decide": 2 } };
    const c = page();
    const label = c.querySelector(".lens-pick")!;
    expect(label.textContent!.startsWith("Area")).toBe(true);
    expect(Array.from(label.querySelectorAll("option")).map((o) => [o.textContent, o.selected])).toEqual([["All areas", false], ["OSS (3)", true], ["Hiring", false]]);
    expect(Array.from(c.querySelectorAll(".chip")).map((b) => b.textContent)).toEqual(["@decide 2", "@reply"]);
    await fireEvent.click(c.querySelector(".chip")!);
    expect(decodeURIComponent(location.hash)).toContain("ctx=@decide");
  });

  it("shows no context chip on a view of gates", () => {
    resetStore(NOW, "#view=approvals");
    tasksState.catalog = { views: [{ id: "triage", label: "Triage" }, { id: "approvals", label: "Approvals", over: "gates" }], contexts: ["@decide", "@reply"] };
    expect(page().querySelectorAll(".chip").length).toBe(0);
    cleanup();
    nav.hash = "#view=triage";
    expect(Array.from(page().querySelectorAll(".chip")).map((b) => b.textContent)).toEqual(["@decide", "@reply"]);
  });

  it("explains itself, and an empty view says what its profile says", () => {
    resetStore(NOW, "#view=waiting");
    tasksState.catalog = { views: [
      { id: "waiting", label: "Waiting", summary: "What others owe you.", empty: "Nobody owes you anything.", when: "task.owed_by != 'me'", group: "task.owed_by", sort: "-task.since_movement" },
      { id: "outbox", label: "Outbox", over: "gates", summary: "Drafts to approve.", empty: "No drafts wait on you." },
      { id: "review", label: "Review", summary: "The weekly review." },
    ] };
    tasksState.key = viewQueryKey("waiting", "", []);
    const c = page();
    expect(c.querySelector(".view-summary")!.textContent).toBe("What others owe you.");
    expect(c.querySelector(".view-rule summary")!.textContent).toBe("Why these tasks?");
    for (const want of ["task.owed_by != 'me'", "-task.since_movement", "task.owed_by"]) expect(c.querySelector(".view-rule")!.textContent).toContain(want);
    expect(body(c).querySelector(".empty")!.textContent).toBe("Nobody owes you anything.");
    tasksState.tasks = [{ id: "t-1", title: "Call the bank", state: "waiting", units: [], verbs: [] }];
    nav.hash = "#view=waiting&q=zzz";
    flushSync();
    expect(body(c).querySelector(".empty")!.textContent).toBe('Nothing here matches "zzz".');
    cleanup();
    nav.hash = "#view=outbox";
    tasksState.key = viewQueryKey("outbox", "", []);
    expect(body(page()).querySelector(".empty")!.textContent).toBe("No drafts wait on you.");
  });

  it("says which plugins are still loading", () => {
    resetStore(NOW, "#view=inbox");
    tasksState.catalog = { views: [{ id: "inbox", label: "Inbox" }] };
    applyTasksPage(viewQueryKey("inbox", "", []), { epoch: "e", rev: 1, full: true, tasks: [], loading: [{ plugin: "slack", reason: "importing threads" }] });
    expect(Array.from(page().querySelectorAll(".loading")).map((p) => p.textContent)).toEqual(["Loading slack: importing threads"]);
  });

  it("says it is loading until its list is read", () => {
    resetStore(NOW, "#view=inbox");
    tasksState.catalog = viewsCatalog();
    expect(body(page()).textContent!.trim()).toBe("Loading.");
  });

  it("offers Remove all clean on a view that asks, and says what it removed and kept", async () => {
    resetStore(NOW, "#view=inbox");
    tasksState.catalog = { views: [{ id: "inbox", label: "Inbox", remove_clean: true }] };
    tasksState.key = viewQueryKey("inbox", "", []);
    tasksState.tasks = [task("t-1", "inbox")];
    const posts: J[] = [];
    net.fetch = async (url: string, init?: RequestInit) => {
      posts.push({ url, body: init && init.body });
      return new Response(JSON.stringify(url.includes("remove-clean") ? { removed: [{ label: "acme/api#1" }], kept: [{ label: "acme/api#2", reason: "dirty", detail: "a.txt" }] } : {}), { status: 200 });
    };
    const c = page();
    const button = c.querySelector('[data-focus="tasks:remove-clean"]') as HTMLButtonElement;
    expect(button.textContent).toBe("Remove all clean");
    expect(button.disabled).toBe(false);
    await fireEvent.click(button);
    const ask = overlays.stack[0];
    expect(ask.kind).toBe("confirm");
    if (ask.kind === "confirm") ask.finish(true);
    await new Promise((r) => setTimeout(r, 0));
    await new Promise((r) => setTimeout(r, 0));
    expect(posts[0]).toEqual({ url: "/api/workspaces/remove-clean", body: JSON.stringify({ view: "inbox" }) });
    expect(notice.text).toBe("Removed 1: acme/api#1. Kept 1: acme/api#2 (dirty).");
    expect(state.actionDetail).toBe("acme/api#2: a.txt");
  });
});

describe("a task row", () => {
  it("reads its properties by role", () => {
    const cat = { ...fixture("views.json"), props: [{ name: "pin", type: "enum", values: ["yes", "no"], role: "star" }, { name: "by", type: "time", role: "deadline" }, { name: "focus", type: "enum", values: ["on", "off"] }] };
    mixed("#view=inbox", [task("e-1", "next", (e) => {
      e.props = { pin: "yes", by: "2026-11-20T00:00:00Z", focus: "on" };
      e.facets = { contexts: ["calls"], "prop.area": ["OSS"] };
    })], cat);
    const text = body(page()).querySelector(".row")!.textContent!;
    for (const want of ["★", "@calls", "OSS", "due 2026-11-20"]) expect(text).toContain(want);
    expect(text).not.toContain("focus");
  });

  it("filters by the where value its unit label names", () => {
    mixed("#view=inbox", [task("e-1", "next", (e) => {
      e.units = [{ provider: "slack", key: "T1/C1/seg/1" }, { provider: "github", key: "acme/api#7" }, { provider: "session", key: "s-1" }];
      e.unit_labels = ["#team-alpha", "acme/api#7", "a session"];
      e.facets = { where: ["team-alpha", "acme/api"] };
    })]);
    const units = Array.from(body(page()).querySelectorAll(".unit")).map((u) => ({ label: u.textContent, slot: u.parentElement!.matches("button") ? u.parentElement!.getAttribute("data-focus") : "" }));
    expect(units).toEqual([{ label: "#team-alpha", slot: "facet:task:e-1:where:team-alpha" }, { label: "acme/api#7", slot: "facet:task:e-1:where:acme/api" }, { label: "a session", slot: "" }]);
  });

  it("shows a project's next action and the local day it was reviewed", () => {
    process.env.TZ = "America/Sao_Paulo";
    mixed("#view=inbox", [task("e-p", "next", (e) => Object.assign(e, { props: { kind: "project" }, title: "Ship", children: ["e-a", "e-b"], review_every: "168h0m0s", reviewed_at: "2026-10-23T02:30:00Z" })),
      task("e-q", "next", (e) => Object.assign(e, { props: { kind: "project" }, title: "Idle" }))]);
    const rows = Array.from(body(page()).querySelectorAll(".row")).map((r) => r.textContent!);
    for (const want of ["2 open", "every 7d"]) expect(rows[0]).toContain(want);
    expect(rows[0]).toMatch(/reviewed 2026-10-2[23]/);
    expect(rows[1]).toContain("no next action");
    expect(rows[0]).not.toContain("no next action");
  });

  it("matches the filter by its words", () => {
    resetStore(NOW);
    const es = [
      task("e-1", "next", (e) => Object.assign(e, { title: "Rollout date", unit_labels: ["#ops"], units: [{ provider: "slack", key: "T1/C1/1.1" }] })),
      task("e-2", "next", (e) => Object.assign(e, { title: "Review the retry", owed_by: "Dana Souza" })),
      task("e-3", "next", (e) => Object.assign(e, { title: "Fix flaky uploads", unit_labels: ["acme/api#8"], units: [{ provider: "github", key: "acme/api#8" }] })),
    ];
    const want: Record<string, string[]> = { "": ["e-1", "e-2", "e-3"], "#ops": ["e-1"], dana: ["e-2"], "souza review": ["e-2"], "ACME/API": ["e-3"], '"retry"': ["e-2"], nothing: [] };
    for (const [q, ids] of Object.entries(want)) {
      const lens = lensOf({ ...readHash(), view: "", q });
      expect(es.filter((e) => matches(taskItemOf(e), lens.ts)).map((e) => e.id), q).toEqual(ids);
    }
  });

  it("lists only the tasks that match", () => {
    resetStore(NOW, "#view=inbox&q=rollout");
    tasksState.catalog = viewsCatalog();
    tasksState.key = viewQueryKey("inbox", "", []);
    tasksState.tasks = [task("e-1", "inbox", (e) => (e.title = "Rollout date")), task("e-2", "inbox", (e) => (e.title = "Review the retry"))];
    const c = page();
    expect(body(c).textContent).toContain("Rollout date");
    expect(body(c).textContent).not.toContain("Review the retry");
    nav.hash = "#view=inbox&q=zzz";
    flushSync();
    expect(body(c).textContent).toContain('Nothing here matches "zzz"');
  });
});

describe("a view's layout", () => {
  it("picks the Outbox, the board or the list", () => {
    const cat = fixture("views.json");
    cat.views = cat.views.map((v: J) => (v.id === "needs-me" ? { ...v, layout: "board" } : v));
    cat.views.push({ id: "send-later", label: "Send later", over: "gates", layout: "outbox" });
    mixed("#view=send-later", fixture("mixed-tasks.json"), cat);
    outboxState.page = fixture("outbox-page.json");
    const classes = (c: Element) => Array.from(body(c).children).map((n) => n.className);
    expect(classes(page())).toEqual(["outbox"]);
    cleanup();
    nav.hash = "#view=needs-me";
    tasksState.key = viewQueryKey("needs-me", "", []);
    const board = classes(page());
    expect(board.length).toBe(1);
    expect(board[0]).toContain("board");
    nav.hash = "#view=needs-me&layout=list";
    flushSync();
    const c = document.body;
    expect(classes(c)).toEqual(["rows task-list"]);
  });
});
