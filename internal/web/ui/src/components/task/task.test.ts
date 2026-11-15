import { cleanup, fireEvent, render } from "@testing-library/svelte";
import { readdirSync, readFileSync } from "node:fs";
import { flushSync } from "svelte";
import { afterEach, beforeEach, expect, test } from "vitest";
import type { J } from "../../lib/util";
import { I18N } from "../../lib/i18n.svelte";
import { results } from "../../state/actions.svelte";
import { net } from "../../state/api";
import { loadDetailPane } from "../../state/loaders.svelte";
import { overlays } from "../../state/overlays.svelte";
import { catalog, detailState, notice, state, taskState, tasksState } from "../../state/store.svelte";
import { resetStore } from "../../testing/reset.svelte";
import { served } from "../../state/keycaps.svelte";
import { askedArgs, detailPane, propRows, runVerb, shownTabs, selectedTab, taskGates } from "./task";
import TaskDetail from "./TaskDetail.svelte";
import TaskVerbs from "./TaskVerbs.svelte";
import TaskProps from "./TaskProps.svelte";
import { settle } from "../../testing/settle";

const ID = "t-0123456789abcdef";
const prUnit = { provider: "github", key: "acme/api#8" };

function taskCatalog(): J {
  return {
    views: [{ id: "triage", label: "Triage" }, { id: "parked", label: "Parked" }, { id: "walk", label: "Weekly walk" }],
    verbs: [{ verb: "finish", label: "Finish", summary: "Close it" }, { verb: "reply", handler: "reply", label: "Reply", summary: "Hold a reply for approval" }],
    states: [{ name: "triage", label: "To sort", category: "inbox" }, { name: "doing", label: "Doing", category: "active" },
      { name: "parked", label: "Parked", category: "deferred" }, { name: "later", label: "Later", category: "deferred" }, { name: "shipped", label: "Shipped", category: "done" }],
    props: [{ name: "size", type: "enum", values: ["s", "m", "l"] }, { name: "owner", type: "actor" }],
  };
}

function task(id: string, st: string, mod: (e: J) => void = () => {}): J {
  const e: J = { id, state: st, title: "Task " + id, units: [], verbs: [] };
  mod(e);
  return e;
}

function pageTask(): J {
  return task(ID, "doing", (e) => {
    Object.assign(e, { category: "active", title: "Ship the retry change", owed_by: "me", units: [prUnit] });
    e.cards = [{ card: { unit: prUnit, label: "acme/api#8", link: "https://github.com/acme/api/pull/8", title: "Retry flaky uploads" } }];
    e.props = { size: "m", owner: { key: "github:ana", name: "Ana" } };
    e.asks = [{ item: "slack:1", summary: "Ship it before Friday", asker: "Bruno", permalink: "https://chat.example/p/1",
      quotes: ["can we get the retry out before the freeze?", "Friday at the latest"] }];
    e.brief = { kind: "brief", at: "2026-10-27T14:00:00Z", data: { goal: "Retry uploads that fail on a timeout", risk: "medium" } };
    e.gates = [
      { id: 7, task: ID, kind: "plan", name: "plan", status: "open", text: "Run this?", intents: [{ id: "run", label: "Run now" }, { id: "skip", label: "Skip" }] },
      { id: 9, task: ID, kind: "question", name: "question", status: "open", text: "The agent asks", intents: [{ id: "answer", label: "Answer", answer: true }, { id: "dismiss", label: "Dismiss" }] },
    ];
    e.runs = [
      { seq: 3, intent: "start", state: "running", manager: "orca", worktree: "/work/acme-api-retry", session: "s-1", session_state: "live", actions: ["open", "enter", "transcript"] },
      { seq: 2, intent: "brief", state: "done", actions: ["transcript"] },
    ];
    e.verbs = [{ verb: "finish", enabled: true }, { verb: "reply", enabled: true }];
    e.children = ["t-00000000000000c1"];
  });
}

function timelineNodes(): J[] {
  return [
    { label: "message", at: "2026-10-27T15:02:00Z", actor: "agent", events: [{ seq: 12, kind: "message", actor: "agent", at: "2026-10-27T15:02:00Z", data: { text: "The retry change is ready for review." } }] },
    { label: "verb", at: "2026-10-27T15:01:00Z", actor: "person", events: [{ seq: 11, kind: "verb", sub: "start", actor: "person", at: "2026-10-27T15:01:00Z" }] },
    { label: "message", at: "2026-10-27T15:00:00Z", actor: "person", events: [{ seq: 10, kind: "message", actor: "person", at: "2026-10-27T15:00:00Z", data: { text: "Take the timeout case first." } }] },
  ];
}

const nodesPath = (id: string, kinds = "") => `/api/tasks/${id}/timeline?as=nodes` + (kinds ? "&kinds=" + kinds : "");

function pageAnswers(e: J = pageTask()): Record<string, J> {
  return { ["/api/tasks/" + e.id]: e, [nodesPath(e.id)]: timelineNodes() };
}

// The daemon as the page reads it: gets answer from answers by URL (a 404 for any other), posts are recorded and
// answered by post, which may hold the answer back.
interface Net {
  gets: string[];
  posts: { path: string; body: J }[];
  answers: Record<string, J>;
  post: (path: string) => J | Promise<J>;
}

let daemon: Net;
const realFetch = net.fetch;

function serve(answers: Record<string, J>): Net {
  daemon = { gets: [], posts: [], answers, post: () => ({}) };
  net.fetch = async (input: string, init?: RequestInit) => {
    if (init && init.method === "POST") {
      daemon.posts.push({ path: input, body: JSON.parse(String(init.body)) });
      return new Response(JSON.stringify(await daemon.post(input)), { status: 200 });
    }
    daemon.gets.push(input);
    if (!(input in daemon.answers)) return new Response(JSON.stringify({ error: "404 " + input }), { status: 404 });
    return new Response(JSON.stringify(daemon.answers[input]), { status: 200 });
  };
  return daemon;
}


// page loads task id into the store as a sync would and draws the detail once.
async function page(id: string, answers: Record<string, J> = pageAnswers()): Promise<HTMLElement> {
  serve(answers);
  await loadDetailPane({ task: id, unit: "" });
  const { container } = render(TaskDetail);
  flushSync();
  return container;
}

const $ = <T extends Element = HTMLElement>(box: Element, sel: string) => box.querySelector<T & HTMLElement>(sel) as T & HTMLElement;
const $$ = (box: Element, sel: string) => [...box.querySelectorAll<HTMLElement>(sel)];
const focus = (box: Element, f: string) => $<HTMLButtonElement>(box, `[data-focus="${f}"]`);
const text = (n: Element | null) => (n ? n.textContent || "" : "");
const said = () => [notice.text, ...[...results.values()].map((r) => r.text)];

async function click(el: HTMLElement): Promise<void> {
  await fireEvent.click(el);
  await settle();
}

async function type(box: Element, value: string): Promise<void> {
  const ta = focus(box, "task:compose:text") as unknown as HTMLTextAreaElement;
  await fireEvent.input(ta, { target: { value } });
  flushSync();
}

beforeEach(() => {
  resetStore();
  tasksState.catalog = taskCatalog();
});

afterEach(() => {
  cleanup();
  net.fetch = realFetch;
});

test("the task page draws its sections in order, with its tabs, head, props and links (TestTaskPageSections)", async () => {
  const box = await page(ID);
  expect([...box.children].map((c) => c.getAttribute("class"))).toEqual(["d-head", "props d-props", "d-links", "d-gates", "verbs d-actions", "d-tabs", "compose d-compose"]);
  expect($$(box, '.tabs [role="tab"]').map((b) => b.dataset.focus)).toEqual(["task:tab:brief", "task:tab:timeline", "task:tab:asks", "task:tab:related", "task:tab:runs"]);
  const head = $(box, ".d-head");
  expect(text($(head, ".state"))).toBe("Doing");
  expect($(head, ".state").dataset.category).toBe("active");
  for (const s of ["Ship the retry change", "owed by me", "acme/api#8"]) expect(text(head)).toContain(s);
  expect($(head, ".ref").getAttribute("href")).toBe("#view=all&unit=github/acme%2Fapi%238");
  expect($(head, ".ext").getAttribute("href")).toBe("https://github.com/acme/api/pull/8");
  expect(Object.fromEntries($$(box, ".props dd").map((d) => [d.dataset.prop, text(d)]))).toMatchObject({ size: "m", owner: "Ana" });
  taskState.tab = "related";
  flushSync();
  expect(focus(box, "child:t-00000000000000c1").getAttribute("href")).toContain("task=t-00000000000000c1");
});

test("an ask shows who asked, its source and its quotes (TestAsksShowTheirSourceQuotes)", async () => {
  taskState.tab = "asks";
  const box = await page(ID);
  const asks = $(box, ".d-asks");
  expect($$(asks, ".quote").map(text)).toEqual(["can we get the retry out before the freeze?", "Friday at the latest"]);
  expect(text($(asks, ".ask-item"))).toContain("Bruno: Ship it before Friday");
  expect($(asks, ".ext").getAttribute("href")).toBe("https://chat.example/p/1");
});

test("a run offers open, enter and its transcript, each as the run offers it (TestARunOffersOpenEnterTranscript)", async () => {
  taskState.tab = "runs";
  const answers = pageAnswers();
  answers["/api/runs/3/transcript"] = [{ role: "user", text: "Take the timeout case first." }, { role: "assistant", text: "Done; tests pass." }];
  const box = await page(ID, answers);
  await click(focus(box, "run:3:open"));
  await click(focus(box, "run:3:enter"));
  await click(focus(box, "run:3:transcript"));
  const runs = $$(box, ".d-run");
  expect($$(runs[0], "button").map(text)).toEqual(["Open workspace", "Enter session", "Transcript"]);
  expect($$(runs[1], "button").map((b) => (b as HTMLButtonElement).disabled)).toEqual([true, true, false]);
  expect(daemon.posts.map((p) => p.path)).toEqual(["/api/runs/3/open", "/api/runs/3/enter"]);
  expect(daemon.gets.at(-1)).toBe("/api/runs/3/transcript");
  expect($$(box, ".transcript li").map((li) => text(li).trim())).toEqual(["user: Take the timeout case first.", "assistant: Done; tests pass."]);
});

test("the timeline narrows to the kinds picked (TestTheTimelineFiltersByKind)", async () => {
  taskState.tab = "timeline";
  const answers = pageAnswers();
  answers[nodesPath(ID, "message")] = [timelineNodes()[0], timelineNodes()[2]];
  const box = await page(ID, answers);
  const before = $$(box, ".ev").map(text);
  expect(before).toHaveLength(3);
  expect(before[0]).toContain("ready for review");
  expect(before[2]).toContain("Take the timeout case first.");
  expect($$(box, ".kinds button").map(text)).toEqual(["all", "message", "verb"]);
  await click(focus(box, "timeline:kind:message"));
  expect(daemon.gets.at(-1)).toBe(nodesPath(ID, "message"));
  expect($$(box, ".ev")).toHaveLength(2);
});

test("the timeline tab folds each node's raw events and counts them (TestTheTimelineTabFoldsRawEventsInEachNode)", async () => {
  resetStore(Date.parse("2026-10-28T12:00:00Z"));
  tasksState.catalog = taskCatalog();
  I18N.strings["{n} event"] = { one: "{n} event", other: "{n} events" };
  taskState.tab = "timeline";
  const at = Date.parse("2026-10-27T09:39:00Z");
  const iso = (ms: number) => new Date(ms).toISOString();
  const round = Array.from({ length: 11 }, (_, i) => ({ seq: i + 1, kind: "turn_started", actor: "agent", at: iso(at + i * 60000) as string, data: undefined as J }));
  round[10].data = { text: "Checked the **four** threads." };
  const answers = pageAnswers();
  answers[nodesPath(ID)] = [
    { rule: "review", label: "dana reviewed: approved", tone: "people", at: iso(at + 26 * 3600000), actor: "dana", events: [{ seq: 20, kind: "review", actor: "dana", at: iso(at + 26 * 3600000) }] },
    { rule: "round", label: "Agent round, 10m", tone: "loop", at: iso(at), end: iso(at + 600000), events: round },
  ];
  const box = await page(ID, answers);
  const evs = $$(box, ".tl .ev");
  expect(evs.map((n) => text($(n, ".lb")))).toEqual(["dana reviewed: approved", "Agent round, 10m"]);
  expect($$(box, ".tl summary").map(text)).toEqual(["11 events"]);
  expect($$(box, ".tl details").map((d) => d.querySelectorAll("li").length)).toEqual([11]);
  expect(evs.map((n) => n.className)).toEqual(["ev people", "ev loop"]);
  expect(evs.map((n) => $(n, ".tm").tagName)).toEqual(["TIME", "TIME"]);
  expect(text($(evs[1], ".dim")).trim()).toBe("Checked the four threads.");
  expect(text($(focus(box, "task:tab:timeline"), ".n"))).toBe("12");
});

test("the composer says its message is on its way and takes no second send (TestComposerShowsItsMessageOnItsWay)", async () => {
  const box = await page(ID);
  let release!: (v: J) => void;
  daemon.post = () => new Promise((r) => (release = r));
  await type(box, "Take the timeout case first.");
  fireEvent.click(focus(box, "task:compose:send"));
  fireEvent.click(focus(box, "task:compose:send"));
  await settle();
  expect(said()).toContain("Sending to Ship the retry change…");
  expect($(box, ".d-compose").getAttribute("aria-busy")).toBe("true");
  release({});
  await settle();
  expect(daemon.posts).toHaveLength(1);
  expect(said()).toContain("Sent to Ship the retry change");
});

test("Send posts to the chat and Save as draft holds a reply; an empty composer says so (TestComposerPostsToChat)", async () => {
  const box = await page(ID);
  const compose = $(box, ".d-compose");
  expect($$(compose, "button").map(text)).toEqual(["Send", "Save as draft", "Send reply"]);
  expect(text($(compose, ".primary"))).toBe("Send");
  await click(focus(box, "task:compose:send"));
  expect(results.get("task:compose:send")).toEqual({ text: "Write a message first", err: true });
  expect(text($(compose, ".act-result"))).toBe("Write a message first");
  await type(box, "Take the timeout case first.");
  await click(focus(box, "task:compose:send"));
  await type(box, "We ship Friday.");
  await click(focus(box, "task:compose:draft"));
  expect(daemon.posts).toEqual([
    { path: `/api/tasks/${ID}/chat`, body: { text: "Take the timeout case first." } },
    { path: `/api/tasks/${ID}/reply`, body: { source: "cockpit", args: { text: "We ship Friday.", send: "draft" } } },
  ]);
});

for (const [served, checked, sent] of [
  ["on", [true, false], ["true", "false"]],
  ["off", [false, true], ["false", "true"]],
  ["", [], [undefined, undefined]],
] as [string, boolean[], (string | undefined)[]][]) {
  test(`the composer offers to also send a thread reply to the conversation, served "${served}" (TestComposerOffersToAlsoSendAThreadReplyToTheConversation)`, async () => {
    const e = pageTask();
    e.reply_broadcast = served;
    const box = await page(ID, pageAnswers(e));
    const toggle = () => focus(box, "task:compose:broadcast") as unknown as HTMLInputElement | null;
    const seen: boolean[] = [];
    if (served) expect(text($(box, ".broadcast")).trim()).toBe("Also send to the conversation");
    else expect($(box, ".broadcast")).toBeNull();
    if (toggle()) seen.push(toggle()!.checked);
    await type(box, "We ship Friday.");
    await click(focus(box, "task:compose:draft"));
    if (toggle()) {
      await fireEvent.click(toggle()!);
      flushSync();
      seen.push(toggle()!.checked);
    }
    await type(box, "Merged.");
    await click(focus(box, "task:compose:reply"));
    expect(seen).toEqual(checked);
    expect(daemon.posts.map((p) => p.body.args)).toEqual([
      { text: "We ship Friday.", send: "draft", ...(sent[0] ? { broadcast: sent[0] } : {}) },
      { text: "Merged.", send: "now", ...(sent[1] ? { broadcast: sent[1] } : {}) },
    ]);
  });
}

test("the composer's broadcast starts as served on each task (TestTheComposersBroadcastStartsAsServedOnEachTask)", async () => {
  const e = pageTask();
  e.reply_broadcast = "on";
  const other = { ...pageTask(), id: "t-00000000000000b2", reply_broadcast: "on" };
  const answers = { ...pageAnswers(e), ...pageAnswers(other) };
  const box = await page(ID, answers);
  const toggle = () => focus(box, "task:compose:broadcast") as unknown as HTMLInputElement;
  await fireEvent.click(toggle());
  flushSync();
  const out = [toggle().checked];
  await loadDetailPane({ task: other.id, unit: "" });
  flushSync();
  out.push(toggle().checked);
  await loadDetailPane({ task: ID, unit: "" });
  flushSync();
  out.push(toggle().checked);
  expect(out).toEqual([false, true, true]);
});

for (const send of ["now", "draft"]) {
  test(`Send is the composer's default and Ctrl+Enter runs it; the reply's served send "${send}" orders the replies (TestComposerSendIsTheDefault)`, async () => {
    const cat = taskCatalog();
    cat.verbs = cat.verbs.map((v: J) => (v.handler === "reply" ? { ...v, verb: "respond", send } : v));
    tasksState.catalog = cat;
    const e = pageTask();
    e.verbs = [{ verb: "finish", enabled: true }, { verb: "respond", enabled: true }];
    const box = await page(ID, pageAnswers(e));
    const compose = $(box, ".d-compose");
    expect(text($(compose, ".primary"))).toBe("Send");
    const want = send === "now" ? ["Send reply", "Save as draft"] : ["Save as draft", "Send reply"];
    expect($$(compose, "button").slice(1).map(text)).toEqual(want);
    await type(box, "Take the timeout case first.");
    await fireEvent.keyDown(focus(box, "task:compose:text"), { key: "Enter", ctrlKey: true });
    await settle();
    await type(box, "Plain enter is a newline.");
    await fireEvent.keyDown(focus(box, "task:compose:text"), { key: "Enter" });
    await type(box, "We ship Friday.");
    const def = $(compose, "button.default");
    expect(text(def)).toBe(want[0]);
    await click(def);
    expect(daemon.posts).toEqual([
      { path: `/api/tasks/${ID}/chat`, body: { text: "Take the timeout case first." } },
      { path: `/api/tasks/${ID}/respond`, body: { source: "cockpit", args: { text: "We ship Friday.", send } } },
    ]);
  });
}

test("a run's states and the brief's risk read in the locale, a transcript line as markdown (TestARunAndABriefReadInTheLocale)", async () => {
  I18N.strings["Risk: {risk}"] = "Risco: {risk}";
  tasksState.catalog = { ...taskCatalog(), facets: [{ id: "risk", fact: "brief.risk", label: "Risco", labels: { high: "alto" } }],
    run_states: { started: "iniciado" }, session_states: { dormant: "dormente" } };
  const e = task(ID, "doing", (x) => {
    x.brief = { data: { risk: "high" } };
    x.runs = [{ seq: 3, intent: "start", state: "started", session: "s-1", session_state: "dormant", actions: [] }];
  });
  const box = await page(ID, pageAnswers(e));
  expect(text($(box, ".d-brief"))).toContain("Risco: alto");
  taskState.transcript = { seq: 3, lines: ["assistant: **Done**; tests pass."] };
  taskState.tab = "runs";
  flushSync();
  const meta = text($(box, ".d-run .meta"));
  expect(meta).toContain("iniciado");
  expect(meta).toContain("(dormente)");
  const line = $(box, ".transcript");
  expect(line.querySelectorAll("strong")).toHaveLength(1);
  expect(text(line)).not.toContain("**");
});

test("a task page offers the actions its units offer on it (TestATaskPageOffersTheActionsItsUnitsOfferOnIt)", async () => {
  state.specs.byId.copy_review_request = { id: "copy_review_request", label: "Copy review request", group: "brief", surfaces: ["detail", "task"] };
  state.specs.byId.pause = { id: "pause", label: "Pause", group: "control", surfaces: ["row", "detail"] };
  const e = pageTask();
  e.unit_actions = [{ unit: prUnit, label: "acme/api#8", actions: [{ id: "copy_review_request", enabled: true }, { id: "pause", enabled: true }] }];
  const box = await page(ID, pageAnswers(e));
  const buttons = $$(box, ".verbs button").filter((b) => (b.dataset.focus || "").startsWith("task:github:"));
  expect(buttons.map(text)).toEqual(["Copy review request"]);
  await click(buttons[0]);
  expect(daemon.posts.map((p) => p.path)).toEqual(["/api/units/github/acme%2Fapi%238/copy_review_request"]);
});

test("the detail lists the profile's primary verbs and the rest in the palette (TestTheDetailListsTheProfilesPrimaryVerbsAndTheRestInThePalette)", async () => {
  tasksState.catalog = { ...taskCatalog(), verbs: [{ verb: "next", label: "Next", key: "x" }, { verb: "done", label: "Done", key: "e" }, { verb: "waiting", label: "Waiting", key: "w" },
    { verb: "focus", label: "Focus", key: "f" }], detail_verbs: ["done", "waiting"] };
  served.bindings = [{ action: "palette", key: "Cmd+K" }];
  const e = task("t-1", "next", (x) => (x.verbs = ["next", "done", "waiting", "focus"].map((verb) => ({ verb, enabled: true }))));
  serve({});
  const { container } = render(TaskVerbs, { e });
  const buttons = $$(container, "button");
  expect(buttons.map((b) => text($(b, ".lb")))).toEqual(["Done", "Waiting", "More 2"]);
  expect(buttons.map((b) => $$(b, "kbd").map(text).join(""))).toEqual(["e", "w", "⌘K"]);
  expect(buttons[2].getAttribute("aria-keyshortcuts")).toBe("Meta+K");
  await click(buttons[2]);
  const top = overlays.stack.at(-1) as J;
  expect(top.palette.items.map((i: J) => i.label + " " + i.key)).toEqual(["Next x", "Focus f"]);
  await top.palette.items[0].run();
  expect(daemon.posts.map((p) => p.path)).toEqual(["/api/tasks/t-1/next"]);
});

test("a verb button says what it does, and why it is off (TestAVerbButtonSaysWhatItDoes)", () => {
  tasksState.catalog = { verbs: [{ verb: "done", label: "Finish", summary: "Close it" }, { verb: "someday", label: "Someday", summary: "Not now" }], detail_verbs: ["done", "someday"] };
  const e = task("e-2", "next", (x) => (x.verbs = [{ verb: "done", enabled: true }, { verb: "someday", reason: "not on a someday task" }]));
  const { container } = render(TaskVerbs, { e });
  expect(focus(container, "detail:done:e-2").title).toBe("Close it");
  expect(focus(container, "detail:someday:e-2").title).toBe("Not now\nnot on a someday task");
});

test("a verb asks the profile's question first (TestAVerbAsksTheProfilesQuestion)", async () => {
  tasksState.catalog = { verbs: [{ verb: "waiting", handler: "waiting", label: "Waiting", summary: "Wait", input: "On whom?" }], detail_verbs: ["waiting"] };
  const e = task("e-3", "next", (x) => (x.verbs = [{ verb: "waiting", enabled: true }]));
  serve({});
  const { container } = render(TaskVerbs, { e });
  await click(focus(container, "detail:waiting:e-3"));
  const ask = overlays.stack.at(-1) as J;
  expect(ask.kind).toBe("input");
  expect(ask.label).toBe("On whom?");
  ask.finish("ana");
  await settle();
  expect(daemon.posts).toEqual([{ path: "/api/tasks/e-3/waiting", body: { source: "cockpit", args: { on: "ana" } } }]);
});

test("a renamed verb asks by its handler (TestARenamedVerbAsksByItsHandler)", async () => {
  tasksState.catalog = { verbs: [{ verb: "park", handler: "waiting", label: "Park", input: "Parked on whom?" }] };
  const args = askedArgs("park");
  const ask = overlays.stack.at(-1) as J;
  expect(ask.label).toBe("Parked on whom?");
  ask.finish("ana");
  expect(await args).toEqual({ on: "ana" });
});

test("a verb the task does not offer has no button (TestAVerbTheTaskDoesNotOfferHasNoButton)", async () => {
  tasksState.catalog = { detail_verbs: ["approve", "dismiss", "done"] };
  const box = await page("e-4", { "/api/tasks/e-4": task("e-4", "inbox", (x) => (x.verbs = [{ verb: "done", enabled: true }])) });
  expect(focus(box, "detail:approve:e-4")).toBeNull();
  expect(focus(box, "detail:dismiss:e-4")).toBeNull();
  expect(focus(box, "detail:done:e-4")).not.toBeNull();
});

test("a task offers the intents that serve it (TestATaskOffersTheIntentsThatServeIt)", async () => {
  const e = task("e-3f9a", "inbox", (x) => {
    Object.assign(x, { title: "Add retries to the webhook sender", props: { kind: "capture" } });
    x.actions = [{ id: "start", label: "Begin work", enabled: true, note: "Starts an agent session on the task's brief" }];
  });
  const box = await page("e-3f9a", { "/api/tasks/e-3f9a": e });
  expect(text(box)).toContain("Begin work");
  await click(focus(box, "detail:start:e-3f9a"));
  expect(daemon.posts.at(-1)).toEqual({ path: "/api/units/task/e-3f9a/start", body: { source: "cockpit" } });
});

test("opening an Inbox task marks it seen once per opening (TestOpeningClearsChanged)", async () => {
  const inbox = task("e-1", "inbox", (x) => Object.assign(x, { category: "inbox", changed: true }));
  serve({ "/api/tasks/e-1": inbox, "/api/tasks/e-2": task("e-2", "next", (x) => (x.changed = true)) });
  for (const id of ["e-1", "e-1", "e-2", "", "e-1"]) await loadDetailPane({ task: id, unit: "" });
  expect(daemon.posts.map((p) => p.path + " " + JSON.stringify(p.body))).toEqual(['/api/tasks/e-1/seen {"source":"cockpit"}', '/api/tasks/e-1/seen {"source":"cockpit"}']);
});

function tabsOf(e: J, picked: string): { tabs: string[]; selected: string } {
  taskState.tab = picked;
  return { tabs: shownTabs(e).map((x) => x.id), selected: selectedTab(e) };
}

test("tabs with nothing in them do not show (TestEmptyTabsDoNotShow)", () => {
  expect(tabsOf(task("t-1", "next"), "")).toEqual({ tabs: ["timeline"], selected: "timeline" });
  expect(tabsOf(pageTask(), "").tabs).toEqual(["brief", "timeline", "asks", "related", "runs"]);
});

test("a vanished tab falls back to the first; the strip selects what the page does (TestAVanishedTabFallsBackToTheFirst)", async () => {
  const e = pageTask();
  e.runs = [];
  expect(tabsOf(e, "runs")).toEqual({ tabs: ["brief", "timeline", "asks", "related"], selected: "brief" });
  expect(tabsOf(e, "asks").selected).toBe("asks");
  const box = await page(ID, pageAnswers(e));
  expect($$(box, '[role="tab"][aria-selected="true"]').map((x) => x.dataset.focus)).toEqual(["task:tab:asks"]);
});

test("props show the risk with its reason, the cards' signals in their tone and the tags, whose rest opens the Brief tab (TestPropsShowRiskWithItsReasonAndTheCardsSignals)", async () => {
  tasksState.catalog = { ...taskCatalog(), facets: [{ id: "tag", fact: "brief.tags", show_max: 2 }] };
  const e = pageTask();
  e.brief = { kind: "brief", data: { risk: "high", risk_why: "Data loss", tags: ["lang:go", "area:api", "team:core"] } };
  e.cards[0].card.signals = [{ key: "checks", value: "failing", tone: "bad" }];
  const { container } = render(TaskProps, { e });
  const dts = $$(container, "dt"), dds = $$(container, "dd");
  const rows = Object.fromEntries(dts.map((d, i) => [text(d), dds[i]]));
  expect(text(rows.Risk)).toContain("high");
  expect(text(rows.Risk)).toContain("Data loss");
  expect(text(rows.checks)).toBe("failing");
  expect(rows.checks.className).toBe("tone-bad");
  expect(text(rows.Tags)).toBe("lang:goarea:api+1");
  await click($(container, ".more"));
  expect(taskState.tab).toBe("brief");
});

test("props use the profile's words (TestPropsUseTheProfilesWords)", () => {
  tasksState.catalog = { ...taskCatalog(),
    props: [{ name: "kind", type: "enum", values: ["start", "capture"] }, { name: "priority", type: "enum", values: ["low", "high"] }, { name: "owner", type: "actor" }],
    facets: [
      { id: "prop.kind", fact: "task.prop.kind", label: "Tipo", labels: { capture: "captura" } },
      { id: "prop.priority", fact: "task.prop.priority", label: "Prioridade" },
      { id: "brief-risk", fact: "brief.risk", label: "Risco", labels: { high: "alto" } },
      { id: "brief-size", fact: "brief.size", label: "Tamanho", labels: { m: "M" } },
    ] };
  const e = pageTask();
  e.props = { kind: "capture", priority: "high", owner: { key: "github:ana", name: "Ana" } };
  e.brief = { kind: "brief", data: { risk: "high", risk_why: "Perda de dados", size: "m" } };
  const { container } = render(TaskProps, { e });
  const dds = $$(container, "dd");
  expect($$(container, "dt").map((d, i) => [text(d), text(dds[i])])).toEqual([["Tipo", "captura"], ["Prioridade", "high"], ["owner", "Ana"], ["Risco", "alto · Perda de dados"], ["Tamanho", "M"]]);
  expect(propRows(e).map((r) => r.key)).toEqual(["prop:kind", "prop:priority", "prop:owner", "brief:risk", "brief:size"]);
});

test("the detail shows each signal once, in the props (TestTheDetailShowsEachSignalOnce)", async () => {
  const e = pageTask();
  e.cards[0].card.signals = [{ key: "checks", value: "failing", tone: "bad" }];
  const box = await page(ID, pageAnswers(e));
  const holders = $$(box, "*").filter((n) => [...n.childNodes].some((c) => c.nodeType === 3 && (c.textContent || "").includes("failing")));
  expect(holders.map((n) => n.tagName)).toEqual(["DD"]);
});

test("a task with a pending draft reads its drafts and shows its card in place of the draft's gate (TestATaskWithAPendingDraftShowsItsCard)", async () => {
  const d = { id: 7, unit: "github/acme/api#8", task: ID, kind: "nudge", text: "Any news on the retry change?", state: "held", gate: 12, rev: 3, actions: [] };
  const e = pageTask();
  e.drafts = [7];
  e.gates = [...e.gates, { id: 12, task: ID, kind: "draft", name: "draft", status: "open", subject: d, intents: [{ id: "approve", label: "Approve" }] }];
  const answers = pageAnswers(e);
  answers[`/api/drafts?task=${ID}`] = { drafts: [d] };
  await page(ID, answers);
  expect(daemon.gets).toContain(`/api/drafts?task=${ID}`);
  const g = taskGates(detailState.task)!;
  expect(g.drafts.map((x) => x.id)).toEqual([7]);
  expect(g.gates.map((x) => x.id)).toEqual([7, 9]);
});

test("a task without drafts asks for none (TestATaskWithoutDraftsAsksForNone)", async () => {
  await page(ID);
  expect(daemon.gets.filter((u) => u.startsWith("/api/drafts"))).toEqual([]);
});

test("the arrow keys move along the tabs, and the detail's tab key steps back (TestArrowKeysMoveAlongTheTabs)", async () => {
  const box = await page(ID);
  const strip = $(box, ".tabs");
  const seen: string[] = [];
  for (const key of ["ArrowRight", "ArrowRight", "ArrowLeft", "ArrowLeft", "ArrowLeft"]) {
    await fireEvent.keyDown(strip, { key });
    flushSync();
    seen.push(taskState.tab);
  }
  detailPane.tab("prev");
  flushSync();
  seen.push(taskState.tab);
  expect(seen).toEqual(["timeline", "asks", "timeline", "brief", "runs", "related"]);
  expect(document.activeElement).toBe(focus(box, "task:tab:related"));
});

test("the tab strip names the served tab keys", async () => {
  served.bindings = [{ action: "tab:prev", key: "[" }, { action: "tab:next", key: "]" }];
  const box = await page(ID);
  expect($(box, ".tabs").getAttribute("aria-keyshortcuts")).toBe("[ ]");
  expect($$(box, ".tabs .keyhint kbd").map(text)).toEqual(["[", "]"]);
});

test("the task page shows related tasks and a merged-from trace with split (TestTheCockpitTaskPageShowsRelatedTasksAndAMergedFromTraceWithSplit)", async () => {
  const cat = taskCatalog();
  cat.verbs.push(
    { verb: "merge", handler: "merge", label: "Merge", input: "Merge with which task", pick: "open" },
    { verb: "split", handler: "split", label: "Split", input: "Split which task out", pick: "merged" },
    { verb: "relate", handler: "relate", label: "Relate", input: "Relate to which task", pick: "open" },
    { verb: "unrelate", handler: "unrelate", label: "Unrelate", input: "Unrelate which task", pick: "related" },
  );
  cat.props.push({ name: "related", type: "string[]", role: "related" }, { name: "merged", type: "string[]", role: "merged" });
  tasksState.catalog = cat;
  tasksState.tasks = [{ id: "t-2", title: "Book the launch hall", state: "doing", units: [] }, { id: "t-4", title: "Stale copy", state: "doing", units: [] }];
  const e = pageTask();
  e.children = [];
  e.verbs.push(...["merge", "relate", "split", "unrelate"].map((verb) => ({ verb, enabled: true })));
  e.props.related = ["t-2", "t-4"];
  e.props.merged = ["t-9"];
  e.related = [{ id: "t-2", title: "Book the launch venue", state: "later" }, { id: "t-4", title: "Order the badges", state: "parked", merged: true }];
  e.merged_from = [{ id: "t-9", title: "Ship the retry fix", state: "triage", within: [{ id: "t-8", title: "Retry the uploads", state: "triage" }] }];
  const answers = pageAnswers(e);
  answers[`/api/tasks?match=&except=${ID}`] = { tasks: [{ id: "t-5", title: "Write the runbook", state: "parked" }] };
  taskState.tab = "related";
  const box = await page(ID, answers);
  const panel = $(box, ".tabpanel");
  expect([...panel.children].map((c) => c.getAttribute("class"))).toEqual(["d-related", "d-merged", "d-actions"]);
  const head = $$(panel, "button").map(text);
  expect(head).toContain("Merge");
  expect(head).toContain("Relate");
  await click(focus(box, `detail:more:${ID}`));
  const more = (overlays.stack.at(-1) as J).palette.items.map((i: J) => i.label);
  expect(more).toEqual(expect.arrayContaining(["Merge", "Relate"]));
  expect(more).not.toContain("Split");
  expect(more).not.toContain("Unrelate");
  (overlays.stack.at(-1) as J).finish();
  expect($$(box, ".props dt").map(text)).not.toContain("related");
  expect($$(box, ".props dt").map(text)).not.toContain("merged");
  const lis = (n: Element) => $$(n, "li").map((li) => text(li).replace(/\s+/g, " ").trim());
  expect(lis($(box, ".d-related"))).toEqual(["Book the launch hall Doing Unrelate", "Order the badges Parked Unrelate"]);
  const merged = lis($(box, ".d-merged"));
  expect(merged).toHaveLength(2);
  expect(merged[0].startsWith("Ship the retry fix To sort Split")).toBe(true);
  expect(merged[1]).toBe("Retry the uploads To sort");
  const rowButtons = $$(box, "button").filter((b) => ["Split", "Unrelate"].includes(text(b)));
  expect(rowButtons.map((b) => b.dataset.focus)).toEqual([`related:t-2:unrelate:${ID}`, `related:t-4:unrelate:${ID}`, `merged:t-9:split:${ID}`]);
  expect(rowButtons.map((b) => b.getAttribute("aria-label"))).toEqual(["Unrelate Book the launch hall", "Unrelate Order the badges", "Split Ship the retry fix"]);
  await click($$(panel, "button").find((b) => text(b) === "Merge")!);
  const picker = (overlays.stack.at(-1) as J).palette;
  expect(picker.title).toBe("Merge with which task");
  expect(picker.items.map((i: J) => i.label + " " + i.meta)).toEqual(["Write the runbook Parked"]);
  await picker.items[0].run();
  (overlays.stack.at(-1) as J).finish();
  await click(focus(box, `related:t-2:unrelate:${ID}`));
  await click(focus(box, `merged:t-9:split:${ID}`));
  expect(daemon.posts.map((p) => [p.path, p.body.args])).toEqual([
    [`/api/tasks/${ID}/merge`, { task: "t-5" }],
    [`/api/tasks/${ID}/unrelate`, { task: "t-2" }],
    [`/api/tasks/${ID}/split`, { task: "t-9" }],
  ]);
});

test("a verb that takes another task picks from the open tasks, or from the tasks merged into it (TestTheCockpitPicksTheOtherTaskOfAMergeFromTheOpenTasks, its picker)", async () => {
  tasksState.catalog = { verbs: [{ verb: "merge", handler: "merge", label: "Merge", input: "Merge with which task", pick: "open" },
    { verb: "split", handler: "split", label: "Split", input: "Split which task out", pick: "merged" }] };
  const open = [{ id: "e-1", title: "Reply to the review", state: "inbox" }, { id: "t-2", title: "Book the launch venue", state: "next" }, { id: "t-3", title: "Order the badges", state: "someday" }];
  serve({});
  const base = net.fetch;
  net.fetch = async (input: string, init?: RequestInit) => {
    if (!input.startsWith("/api/tasks?match=")) return base(input, init);
    daemon.gets.push(input);
    const p = new URLSearchParams(input.split("?")[1]);
    const q = (p.get("match") || "").trim().toLowerCase();
    return new Response(JSON.stringify({ tasks: open.filter((x) => x.id !== p.get("except") && x.title.toLowerCase().includes(q)) }));
  };
  const e = task("e-1", "inbox", (x) => (x.verbs = [{ verb: "merge", enabled: true }, { verb: "split", enabled: true }]));
  await runVerb(e, "merge");
  const pal = (overlays.stack.at(-1) as J).palette;
  expect(pal.title).toBe("Merge with which task");
  expect(pal.items.map((i: J) => ({ label: i.label, key: i.meta }))).toEqual([{ label: "Book the launch venue", key: "next" }, { label: "Order the badges", key: "someday" }]);
  const narrowed = await pal.source(" VENUE");
  expect(narrowed.map((i: J) => i.label)).toEqual(["Book the launch venue"]);
  await narrowed[0].run();
  expect(daemon.gets).toEqual(["/api/tasks?match=&except=e-1", "/api/tasks?match=%20VENUE&except=e-1"]);
  await runVerb({ ...e, merged_from: [{ id: "t-9", title: "Reply to the review, again", state: "inbox" }] }, "split");
  const split = (overlays.stack.at(-1) as J).palette;
  expect(split.items.map((i: J) => i.label)).toEqual(["Reply to the review, again"]);
  await split.items[0].run();
  expect(daemon.posts).toEqual([
    { path: "/api/tasks/e-1/merge", body: { source: "cockpit", args: { task: "t-2" } } },
    { path: "/api/tasks/e-1/split", body: { source: "cockpit", args: { task: "t-9" } } },
  ]);
});

test("a Slack task's detail reads by name (TestSlackTaskDetailReadsByName)", async () => {
  const slackUnit = { provider: "slack", key: "C0001/1700.1" };
  const e = task("e-9", "waiting", (x) => {
    x.units = [slackUnit];
    x.unit_labels = ["#general"];
    x.asks = [{ item: "e1", summary: "Ana owes the doc", asker: "Ana" }];
    x.cards = [{ card: { unit: slackUnit, label: "#general", title: "Deploy plan", state: "waits_on_others",
      signals: [{ key: "last activity", value: "2026-10-23 14:15 UTC", tone: "info", at: "2026-10-23T14:15:00Z" }],
      waits_on: [{ key: "group:S0001", name: "@team-a" }] } }];
  });
  taskState.tab = "asks";
  tasksState.catalog = { ...taskCatalog(), states: [...taskCatalog().states, { name: "waiting", label: "waiting", category: "waiting" }] };
  const box = await page("e-9", { "/api/tasks/e-9": e, [nodesPath("e-9")]: [] });
  const all = text(box);
  expect(all).not.toContain("[object ");
  expect($$($(box, ".d-head"), ".pill").filter((p) => text(p) === "waiting")).toHaveLength(1);
  for (const s of ["#general", "Ana owes the doc", "@team-a", "last activity"]) expect(all).toContain(s);
  expect(text($$(box, ".props dd")[0])).toContain("10/23");
  for (const s of ["C0001", "S0001", "UTC"]) expect(all).not.toContain(s);
});

test("a store update that changes the task keeps the composer's node, text, size, scroll and focus, and the tab picked", async () => {
  catalog.events = new Map();
  taskState.tab = "asks";
  const box = await page(ID);
  const ta = focus(box, "task:compose:text") as unknown as HTMLTextAreaElement;
  await type(box, "Half a thought");
  ta.focus();
  ta.setSelectionRange(4, 4);
  ta.style.height = "240px";
  ta.scrollTop = 30;
  const head = $(box, ".d-head h1");

  serve({
    ...pageAnswers({ ...pageTask(), title: "Ship the retry change now", verbs: [...pageTask().verbs, { verb: "park", enabled: true }],
      asks: [...pageTask().asks, { item: "slack:2", summary: "And the docs", asker: "Ana" }] }),
    [nodesPath(ID)]: [{ label: "message", at: "2026-10-27T15:03:00Z", events: [{ seq: 13, kind: "message", at: "2026-10-27T15:03:00Z" }] }, ...timelineNodes()],
  });
  await loadDetailPane({ task: ID, unit: "" });
  flushSync();

  expect(focus(box, "task:compose:text")).toBe(ta);
  expect(ta.value).toBe("Half a thought");
  expect(ta.style.height).toBe("240px");
  expect(ta.scrollTop).toBe(30);
  expect(ta.selectionStart).toBe(4);
  expect(document.activeElement).toBe(ta);
  expect($(box, ".d-head h1")).toBe(head);
  expect(text(head)).toBe("Ship the retry change now");
  expect(text($(focus(box, "task:tab:timeline"), ".n"))).toBe("4");
  expect($$(box, ".ask-item")).toHaveLength(2);
  expect(taskState.tab).toBe("asks");
  expect($(box, '[role="tab"][aria-selected="true"]').dataset.focus).toBe("task:tab:asks");
});

test("a timeline node's raw events left open stay open across a store update", async () => {
  taskState.tab = "timeline";
  const nodes = [{ rule: "round", label: "Agent round", at: "2026-10-27T15:00:00Z", events: [{ seq: 1, kind: "a", at: "2026-10-27T15:00:00Z" }, { seq: 2, kind: "a", at: "2026-10-27T15:01:00Z" }] }];
  const box = await page(ID, { ...pageAnswers(), [nodesPath(ID)]: nodes });
  const details = $<HTMLDetailsElement>(box, ".tl details");
  details.open = true;
  serve({ ...pageAnswers(), [nodesPath(ID)]: [{ ...nodes[0], events: [...nodes[0].events, { seq: 3, kind: "a", at: "2026-10-27T15:02:00Z" }] }] });
  await loadDetailPane({ task: ID, unit: "" });
  flushSync();
  expect($(box, ".tl details")).toBe(details);
  expect(details.open).toBe(true);
  expect(details.querySelectorAll("li")).toHaveLength(3);
});

test("while the task loads, or when it cannot, the detail says so under its close button", async () => {
  serve({});
  detailState.id = "t-x";
  const { container } = render(TaskDetail);
  expect(text($(container, ".d-head .empty"))).toBe("Loading the task.");
  await loadDetailPane({ task: "t-x", unit: "" });
  flushSync();
  expect(text($(container, ".d-head .plan-error"))).toContain("Could not load the task:");
  expect(focus(container, "detail:close")).not.toBeNull();
});

test("the task detail's sources name no bundled view id as logic (TestTaskScriptsNameNoViewAsLogic)", () => {
  const ids = ["inbox", "needs-me", "waiting", "snoozed", "someday", "focus", "projects", "logbook", "outbox"].join("|");
  const logic = new RegExp(`[!=]==?\\s*"(${ids})"|"(${ids})"\\s*[!=]==?|\\(\\s*"(${ids})"\\s*[,)]|[\\[,]\\s*"(${ids})"\\s*[,\\]]|case\\s+"(${ids})"`, "g");
  const dir = process.cwd() + "/src/components/task/";
  const found = readdirSync(dir).filter((f) => !f.endsWith(".test.ts") && (f.endsWith(".ts") || f.endsWith(".svelte")))
    .flatMap((f) => (readFileSync(dir + f, "utf8").match(logic) || []).map((m) => `${f}: ${m}`));
  expect(found).toEqual([]);
});
