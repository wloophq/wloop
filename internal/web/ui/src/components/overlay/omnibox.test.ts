import { cleanup, fireEvent, render } from "@testing-library/svelte";
import { flushSync } from "svelte";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import type { J } from "../../lib/util";
import { I18N } from "../../lib/i18n.svelte";
import { net } from "../../state/api";
import { state } from "../../state/store.svelte";
import * as actions from "../../state/actions.svelte";
import * as loaders from "../../state/loaders.svelte";
import { askConfirm, closeOverlays, overlays } from "../../state/overlays.svelte";
import { resetStore } from "../../testing/reset.svelte";
import Omnibox from "./Omnibox.svelte";
import { omniboxAct, omniboxChoices, omniboxEnter, omniboxNew, omniboxTrap, omniboxType, panelFollow, type OmniEnv } from "./omnibox";
import PanelOmnibox from "./PanelOmnibox.svelte";
import { settle } from "../../testing/settle";

vi.mock("../../state/actions.svelte", async (importOriginal) => {
  const m = await importOriginal<typeof import("../../state/actions.svelte")>();
  return { ...m, act: vi.fn(m.act), claimCopy: vi.fn(m.claimCopy) };
});
vi.mock("../../state/loaders.svelte", async (importOriginal) => {
  const m = await importOriginal<typeof import("../../state/loaders.svelte")>();
  return { ...m, refreshSpecs: vi.fn(m.refreshSpecs) };
});

// The omnibox over a fake daemon, as omnibox_test.go's harness: input.search answers GET /api/search by query,
// input.resolve POST /api/omnibox/resolve by text, input.ask the asks in order (a string throws it), input.rows
// a unit's GET by path, input.actions a unit action's POST by path (a string throws it), input.refuse refuses
// the clipboard's write, input.confirm answers a confirm, input.fails are the tasks whose operation a run
// refuses; later runs at once unless held.
interface Input {
  search?: Record<string, J>;
  resolve?: Record<string, J>;
  ask?: (J | string)[];
  rows?: Record<string, J>;
  actions?: Record<string, J | string>;
  refuse?: string;
  confirm?: boolean;
  fails?: string[];
}

function fake(input: Input) {
  const log = { gets: [] as string[], posts: [] as { url: string; body: J }[], opened: [] as string[], acts: [] as string[], actCopies: [] as boolean[], closed: 0, steps: [] as string[] };
  const held: (() => void)[] = [];
  const ctl = { hold: false };
  let asks = 0;
  const env: OmniEnv = {
    get: async (url) => {
      log.gets.push(url);
      if (url.startsWith("/api/units/")) {
        log.steps.push("get");
        return (input.rows || {})[url] || { row: { actions: [] } };
      }
      return (input.search || {})[decodeURIComponent(url.split("q=")[1])] || { groups: [] };
    },
    post: async (url, body: J) => {
      log.posts.push({ url, body: JSON.parse(JSON.stringify(body)) });
      if (url === "/api/omnibox/resolve") return (input.resolve || {})[body.text] || { kind: "search" };
      if (url === "/api/omnibox/ask") {
        const a = (input.ask || [])[asks++];
        if (typeof a === "string") throw new Error(a);
        return a;
      }
      if (url === "/api/omnibox/run") {
        return { results: body.operations.map((op: J) => ((input.fails || []).includes(op.task) ? { ok: false, status: 409, error: "run_conflict" } : { ok: true, status: 200 })) };
      }
      if (url.startsWith("/api/units/")) {
        log.steps.push("post");
        const a = (input.actions || {})[url];
        if (typeof a === "string") throw new Error(a);
        return a || {};
      }
      return {};
    },
    claim: () => {
      log.steps.push("claim");
      let settle!: { ok: () => void; no: (e: unknown) => void };
      const claimed = new Promise<void>((ok, no) => { settle = { ok, no }; });
      claimed.catch(() => {});
      return {
        claimed,
        pending: {
          promise: claimed as J,
          resolve: (copy: J) => { log.steps.push("resolve " + copy.text); if (input.refuse) settle.no(new Error(input.refuse)); else settle.ok(); },
          reject: (err: J) => { log.steps.push("reject " + err.message); settle.no(err); },
        },
      };
    },
    settle: async (name, out, claim) => {
      if (!out.copy) {
        claim.pending.reject(new Error(name + " answered nothing to copy"));
        return false;
      }
      claim.pending.resolve(out.copy);
      await claim.claimed;
      return true;
    },
    confirm: (text) => { log.steps.push("confirm " + text); return input.confirm !== false; },
    later: (fn) => { if (ctl.hold) held.push(fn); else fn(); return 1; },
    clear: () => {},
    now: () => 0,
    open: (href) => { log.opened.push(href); },
    act: async (u, id, copies, row, guidance) => {
      log.acts.push(u.key + " " + id + (row ? " on " + row.title : "") + (guidance === undefined ? "" : " guided " + JSON.stringify(guidance)));
      log.actCopies.push(!!copies);
    },
    close: () => { log.closed++; },
  };
  return { log, env, held, ctl };
}


// mount draws the omnibox over the fake daemon input answers; type types into its field and waits for the
// answer.
function mount(input: Input, opts: { panel?: boolean; items?: J[] } = {}) {
  const f = fake(input);
  const r = render(Omnibox, { panel: !!opts.panel, items: opts.items || [], onclose: () => f.log.closed++, env: f.env });
  const field = r.container.querySelector(".omni-input") as HTMLInputElement;
  const type = async (text: string) => {
    field.value = text;
    await fireEvent.input(field);
    await settle();
  };
  const key = async (k: string, extra: J = {}) => {
    await fireEvent.keyDown(field, { key: k, ...extra });
    await settle();
  };
  const q = (sel: string) => [...r.container.querySelectorAll<HTMLElement>(sel)];
  const button = (label: string) => q("button").find((b) => (b.textContent || "").includes(label)) as HTMLButtonElement;
  const groups = () => q("[data-group]").map((g) => (g.children[0].textContent || "").trim());
  const selected = () => q('[aria-selected="true"], [data-selected="true"]').map((x) => x.dataset.choice);
  return { ...f, ...r, field, type, key, q, button, groups, selected };
}

const res = (id: string, title: string, ...views: string[]) => ({ task: { id, title }, views });
const retriesSearch = (): Record<string, J> => ({
  retries: { query: "retries", total: 2, groups: [
    { id: "you", label: "Needs you", count: 1, results: [res("t-1", "add retries", "needs-me", "focus")] },
    { id: "others", label: "Waiting on others", count: 1, results: [res("t-2", "retries review", "waiting")] }] },
  re: { query: "re", total: 1, groups: [{ id: "done", label: "Done", count: 9, results: [res("t-9", "release")] }] },
});

const ADDRESS = "https://example.test/acme/api/pull/8";
function pastedUnit(...tasks: J[]): Record<string, J> {
  return { [ADDRESS]: { kind: "unit", unit: {
    unit: { provider: "alpha", key: "acme/api#8" }, label: "acme/api#8", held: true, tasks,
    actions: [{ id: "pause", label: "Pause" }, { id: "remove", label: "Remove" }] } } };
}
function unitWith(action: J): Record<string, J> {
  const r = pastedUnit();
  r[ADDRESS].unit.actions.push(action);
  return r;
}
const copyingUnit = () => unitWith({ id: "copy_review_request", label: "Copy review request", copies: true });
const COPY = "/api/units/alpha/acme%2Fapi%238/copy_review_request";
const copied = { [COPY]: { ok: true, copy: { text: "review, pls?" } } };
const verbResolve = { "done t-1": { kind: "verb", preview: { operations: [{ op: "verb", verb: "done", task: "t-1" }] } } };

beforeEach(() => resetStore());
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("omnibox_test.go", () => {
  test("TestOmniboxSearchGroups", async () => {
    const o = mount({ search: retriesSearch() });
    await o.type("retries");
    expect(o.groups()).toEqual(["Needs you 1", "Waiting on others 1"]);
    expect(o.q('[role="option"]').map((x) => x.dataset.href)).toEqual(["#task=t-1", "#task=t-2"]);
    expect(o.q(".views")[0].textContent).toContain("needs-me · focus");
    expect(o.q(".omni-badge")[0].textContent).toBe("instant");
  });

  test("TestOmniboxStaleReplyDropped", async () => {
    const f = fake({ search: retriesSearch() });
    const st = omniboxNew(false, []);
    f.ctl.hold = true;
    const first = omniboxType(st, "re", f.env);
    const second = omniboxType(st, "retries", f.env);
    const [a, b] = f.held;
    await b();
    await a();
    await Promise.all([first, second]);
    expect(st.search.groups.map((g: J) => g.id)).toEqual(["you", "others"]);
  });

  test("TestOmniboxVerbPreviewRunsChecked", async () => {
    const o = mount({ resolve: { "revise acme/api#8 and acme/api#9": { kind: "verb", preview: { operations: [
      { op: "action", unit: { provider: "alpha", key: "acme/api#8" }, action: "review" },
      { op: "action", unit: { provider: "alpha", key: "acme/api#9" }, action: "review" }] } } } });
    await o.type("revise acme/api#8 and acme/api#9");
    await fireEvent.click(o.q('input[type="checkbox"]')[1]);
    await o.key("Enter");
    const ran = o.log.posts.filter((p) => p.url === "/api/omnibox/run").flatMap((p) => p.body.operations);
    expect(ran.map((op: J) => op.unit.key)).toEqual(["acme/api#8"]);
    expect(o.q("li.ok")).toHaveLength(1);
  });

  test("an operation that ran leaves the checklist, one refused or left unchecked stays", async () => {
    const ops = ["t-1", "t-2", "t-3"].map((task) => ({ op: "verb", verb: "done", task }));
    const o = mount({ resolve: { "done t-1 t-2 t-3": { kind: "verb", preview: { operations: ops } } }, fails: ["t-2"] });
    await o.type("done t-1 t-2 t-3");
    await fireEvent.click(o.q('input[type="checkbox"]')[2]);
    await o.key("Enter");
    expect(o.q(".omni-ran li").map((li) => (li.textContent || "").slice(0, 1))).toEqual(["✓", "✗"]);
    expect(o.q(".omni-op").map((li) => (li.textContent || "").trim())).toEqual(["done task t-2", "done task t-3"]);
    expect(o.q('input[type="checkbox"]').map((c) => (c as HTMLInputElement).checked)).toEqual([true, false]);
  });

  test("a checklist whose operations all ran shows only what ran", async () => {
    const o = mount({ resolve: verbResolve });
    await o.type("done t-1");
    await o.key("Enter");
    expect(o.q(".omni-ran li.ok")).toHaveLength(1);
    expect(o.q(".omni-ops")).toHaveLength(0);
    expect(o.button("Run")).toBeUndefined();
    await o.key("Enter");
    expect(o.log.posts.filter((p) => p.url === "/api/omnibox/run")).toHaveLength(1);
  });

  test("Enter after every operation ran opens no search result", async () => {
    const o = mount({ resolve: verbResolve, search: { "done t-1": retriesSearch().re } });
    await o.type("done t-1");
    await o.key("Enter");
    await o.key("Enter");
    expect(o.log.opened).toEqual([]);
    expect(o.q(".omni-ran li.ok")).toHaveLength(1);
    await o.key("ArrowDown");
    await o.key("Enter");
    expect(o.log.opened).toEqual(["#task=t-9"]);
  });

  test("a clicked Run that empties the checklist hands the focus back to the field", async () => {
    const o = mount({ resolve: verbResolve });
    await o.type("done t-1");
    const run = o.button("Run");
    run.focus();
    await fireEvent.click(run);
    await settle();
    expect(o.q(".omni-ops")).toHaveLength(0);
    expect(document.activeElement).toBe(o.field);
  });

  test("an operation shows what its row asks before it runs", async () => {
    const o = mount({ resolve: { "adopt acme/api#8": { kind: "verb", preview: { operations: [
      { op: "action", unit: { provider: "alpha", key: "acme/api#8" }, action: "take_over", confirm: "It becomes yours to develop" }] } } } });
    await o.type("adopt acme/api#8");
    expect(o.q(".omni-op .why").map((x) => x.textContent)).toEqual(["It becomes yours to develop"]);
  });

  test("search results show before a slow resolution answers", async () => {
    const f = fake({ search: retriesSearch() });
    let answer!: (v: J) => void;
    const post = f.env.post;
    f.env.post = (url, body, signal) => (url === "/api/omnibox/resolve" ? new Promise<J>((ok) => { answer = ok; }) : post(url, body, signal));
    const st = omniboxNew(false, []);
    const typed = omniboxType(st, "retries", f.env);
    await settle();
    expect(st.search.groups.map((g: J) => g.id)).toEqual(["you", "others"]);
    expect(st.answered).toBe("");
    answer({ kind: "search" });
    await typed;
    expect(st.answered).toBe("retries");
  });

  test("a pasted address shows its unit and operations before a slow search answers", async () => {
    const r = pastedUnit();
    r[ADDRESS].preview = { operations: [{ op: "action", unit: r[ADDRESS].unit.unit, action: "track" }] };
    const f = fake({ resolve: r });
    let answer!: (v: J) => void;
    const get = f.env.get;
    f.env.get = (url, signal) => (url.startsWith("/api/search") ? new Promise<J>((ok) => { answer = ok; }) : get(url, signal));
    const st = omniboxNew(false, []);
    const typed = omniboxType(st, ADDRESS, f.env);
    await settle();
    expect(st.answered).toBe(ADDRESS);
    expect(st.resolved.kind).toBe("unit");
    expect(st.ops.map((o) => o.action)).toEqual(["track"]);
    expect(st.search).toBeNull();
    answer({ groups: [] });
    await typed;
    expect(st.search).toEqual({ groups: [] });
  });

  test("Enter on a pasted address runs its choice without waiting for search", async () => {
    const r = pastedUnit({ id: "t-4", title: "Review the export" });
    const f = fake({ resolve: r });
    f.env.get = () => new Promise<J>(() => {});
    const st = omniboxNew(false, []);
    void omniboxType(st, ADDRESS, f.env);
    await omniboxEnter(st, f.env, false);
    expect(f.log.opened).toEqual(["#task=t-4"]);
  });

  test("Enter waits for search when resolve offers no choice of its own", async () => {
    const f = fake({ search: retriesSearch() });
    let answer!: (v: J) => void;
    const get = f.env.get;
    f.env.get = (url, signal) => (url.startsWith("/api/search") ? new Promise<J>((ok) => { answer = ok; }) : get(url, signal));
    const st = omniboxNew(false, []);
    void omniboxType(st, "retries", f.env);
    const entered = omniboxEnter(st, f.env, false);
    await settle();
    expect(f.log.opened).toEqual([]);
    answer(retriesSearch().retries);
    await entered;
    expect(f.log.opened).toEqual(["#task=t-1"]);
  });

  test("a resolution for newer text drops the older text's search", async () => {
    const r = pastedUnit();
    r[ADDRESS].preview = { operations: [{ op: "action", unit: r[ADDRESS].unit.unit, action: "track" }] };
    const f = fake({ search: retriesSearch(), resolve: r });
    const st = omniboxNew(false, []);
    await omniboxType(st, "retries", f.env);
    expect(st.search.groups.length).toBe(2);
    const get = f.env.get;
    f.env.get = (url, signal) => (url.startsWith("/api/search") ? new Promise<J>(() => {}) : get(url, signal));
    void omniboxType(st, ADDRESS, f.env);
    await settle();
    expect(st.answered).toBe(ADDRESS);
    expect(st.search).toBeNull();
    expect(omniboxChoices(st, f.env).map((c) => c.id)).toEqual(["ops", "unit"]);
  });

  test("Enter on a result the newer search no longer lists runs nothing", async () => {
    const r = pastedUnit();
    r[ADDRESS].preview = { operations: [{ op: "action", unit: r[ADDRESS].unit.unit, action: "track" }] };
    const f = fake({ search: retriesSearch(), resolve: r });
    const st = omniboxNew(false, []);
    await omniboxType(st, "retries", f.env);
    let answer!: (v: J) => void;
    const get = f.env.get;
    f.env.get = (url, signal) => (url.startsWith("/api/search") ? new Promise<J>((ok) => { answer = ok; }) : get(url, signal));
    const typed = omniboxType(st, ADDRESS, f.env);
    st.sel = "task:t-1";
    const entered = omniboxEnter(st, f.env, false);
    await settle();
    answer({ groups: [] });
    await typed;
    await entered;
    expect(f.log.posts.filter((p) => p.url === "/api/omnibox/run")).toEqual([]);
    expect(f.log.opened).toEqual([]);
  });

  test("the hint to ask again stays drawn after the agent failed", async () => {
    const o = mount({ resolve: { "what waits": { kind: "agent" } }, ask: ["the agent is away"] });
    await o.type("what waits");
    expect(o.q("output")[0].classList.contains("said")).toBe(true);
    await o.key("Enter");
    expect(o.q(".omni-ask-error")).toHaveLength(1);
    expect(o.q(".omni-hint")).toHaveLength(0);
    expect(o.q("output")[0].textContent).toContain("ask the agent");
    expect(o.q("output")[0].classList.contains("said")).toBe(false);
  });

  test("a unit that offers no action says why, and Enter opens no detail the loop does not serve", async () => {
    const r = pastedUnit();
    Object.assign(r[ADDRESS].unit, { held: false, actions: [], why: "unit_unreadable", params: { unit: "acme/api#8" } });
    const o = mount({ resolve: r });
    await o.type(ADDRESS);
    expect(o.q(".omni-unit .omni-why").map((x) => x.textContent)).toEqual(["unit_unreadable"]);
    await o.key("Enter");
    expect(o.log.opened).toEqual([]);
  });

  test("an action on an unheld unit resolves the text again", async () => {
    const r = unitWith({ id: "track", label: "Track" });
    Object.assign(r[ADDRESS].unit, { held: false, row: { unit: r[ADDRESS].unit.unit, title: "Retry the export", actions: [{ id: "track", enabled: true }] } });
    const o = mount({ resolve: r });
    await o.type(ADDRESS);
    Object.assign(r[ADDRESS].unit, { held: true, row: undefined, actions: [{ id: "pause", label: "Pause" }] });
    await fireEvent.click(o.button("Track"));
    await settle();
    expect(o.button("Track")).toBeUndefined();
    expect(o.button("Pause")).toBeDefined();
    await o.key("Enter");
    expect(o.log.opened).toHaveLength(1);
  });

  test("an unheld unit's action runs on the row the resolution carries, with no detail read", async () => {
    const unheld = (): Record<string, J> => {
      const r = unitWith({ id: "open", label: "Open" });
      r[ADDRESS].unit.held = false;
      r[ADDRESS].unit.row = { unit: r[ADDRESS].unit.unit, title: "Retry the export", actions: [{ id: "open", enabled: true, basis: "b1" }] };
      return r;
    };
    const o = mount({ resolve: unheld() });
    await o.type(ADDRESS);
    await fireEvent.click(o.button("Open"));
    await settle();
    expect(o.log.acts).toEqual(["acme/api#8 open on Retry the export"]);
    await o.key("Enter");
    expect(o.selected()).toEqual(["unit"]);
    expect(o.log.opened).toEqual([]);
    const p = mount({ resolve: unheld() }, { panel: true });
    await p.type(ADDRESS);
    await fireEvent.click(p.button("Open"));
    await settle();
    expect(p.log.steps).toEqual(["post"]);
    expect(p.log.posts.filter((x) => x.url.startsWith("/api/units/")).map((x) => x.body)).toEqual([{ source: "cockpit", basis: "b1" }]);
  });

  test("omniboxAct acts on a row it is given without reading the unit's detail", async () => {
    const gets: string[] = [];
    net.fetch = async (url: string) => {
      gets.push(url);
      return new Response("{}", { status: 404 });
    };
    vi.mocked(loaders.refreshSpecs).mockResolvedValue();
    const acts: string[] = [];
    vi.mocked(actions.act).mockImplementation(async (row: J, id: string) => { acts.push(row.title + " " + id); return true; });
    state.specs = { version: "", byId: {} };
    await omniboxAct({ provider: "alpha", key: "acme/api#8" }, "review", false, { title: "Retry the export", actions: [] });
    expect(gets).toEqual([]);
    expect(acts).toEqual(["Retry the export review"]);
  });

  test("TestOmniboxAgentProposalEditAndConfirm", async () => {
    const o = mount({ ask: [{ proposal: { summary: "Start the retries work", operations: [{ op: "start", brief: "add retries to the sender", repo: "acme/api", reason: "it is new work" }] } }] });
    await o.type("start the retries work");
    await o.key("Enter", { metaKey: true });
    expect(o.q(".omni-badge")[0].textContent).toBe("agent");
    const repo = o.q('input[aria-label="Repository"]')[0] as HTMLInputElement;
    repo.value = "acme/web";
    await fireEvent.input(repo);
    await o.key("Enter");
    const ran = o.log.posts.filter((p) => p.url === "/api/omnibox/run").flatMap((p) => p.body.operations);
    expect(ran.map((op: J) => op.repo)).toEqual(["acme/web"]);
  });

  test("TestOmniboxAgentErrorKeepsSearch", async () => {
    const o = mount({ search: retriesSearch(), ask: ["The agent could not answer: boom"] });
    await o.type("retries");
    await o.key("Enter", { metaKey: true });
    expect(o.groups()).toHaveLength(2);
    expect(o.q(".omni-ask-error")[0].textContent).toBe("The agent could not answer: boom");
    expect(o.log.posts.filter((p) => p.url === "/api/omnibox/run")).toHaveLength(0);
  });

  test("TestOmniboxFollowUpSendsHistory", async () => {
    const o = mount({ ask: [{ proposal: { summary: "Which one?", question: "The sender or the receiver?", operations: [] } }, { answer: { text: "ok", tasks: [] } }] });
    await o.type("retries for the webhook");
    await o.key("Enter", { metaKey: true });
    const reply = o.q('input[aria-label="Your answer"]')[0] as HTMLInputElement;
    reply.value = "both";
    await fireEvent.input(reply);
    await fireEvent.keyDown(reply, { key: "Enter" });
    await settle();
    const asks = o.log.posts.filter((p) => p.url === "/api/omnibox/ask");
    expect(asks[1].body.follow).toEqual([{ question: "The sender or the receiver?", answer: "both" }]);
  });

  test("TestOmniboxAnswerOpensAsQueue", async () => {
    const o = mount({ ask: [{ answer: { text: "Two wait on Ada.", tasks: [{ id: "t-1", title: "a" }, { id: "t-2", title: "b" }] } }] });
    await o.type("what waits on Ada");
    await o.key("Enter", { metaKey: true });
    expect(o.q(".omni-answer a").map((a) => a.getAttribute("href"))).toEqual(["#task=t-1", "#task=t-2", "#queue=list&ids=t-1,t-2"]);
  });

  test("TestOmniboxStringsGoThroughT", async () => {
    I18N.strings = new Proxy({}, { get: (_, k) => (typeof k === "string" ? "«" + k + "»" : undefined), has: () => true });
    const o = mount({ resolve: {
      "https://elsewhere.example/x": { kind: "search", why: "unclaimed_address", params: { address: "https://elsewhere.example/x" } },
      "api#5": { kind: "agent", why: "ambiguous_ref", params: { ref: "api#5" } } } });
    await o.type("https://elsewhere.example/x");
    const why = o.q(".omni-why")[0].textContent || "";
    const badge = o.q(".omni-badge")[0].textContent || "";
    await o.type("api#5");
    const hint = o.q(".omni-hint")[0].textContent || "";
    for (const s of [why, badge, hint]) expect(s.startsWith("«")).toBe(true);
  });

  test("TestOmniboxEmptyListsSelectionActions", async () => {
    const ran: string[] = [];
    const o = mount({}, { items: [{ label: "to Next", key: "x", run: () => ran.push("next") }, { label: "done", key: "e", run: () => ran.push("done") }] });
    const opts = o.q('[role="option"]');
    expect(opts.map((x) => x.textContent)).toEqual(["to Nextx", "donee"]);
    await fireEvent.click(opts[1]);
    expect(ran).toEqual(["done"]);
  });

  test("TestOmniboxUnitActionUsesTheActionPath", async () => {
    const o = mount({ resolve: pastedUnit() });
    await o.type(ADDRESS);
    await fireEvent.click(o.button("Pause"));
    await settle();
    expect(o.log.acts).toEqual(["acme/api#8 pause"]);
    expect(o.log.posts.filter((p) => p.url === "/api/omnibox/run")).toHaveLength(0);
  });

  test("TestOmniboxPanelRunsAUnitsActionInPlace", async () => {
    const o = mount({ resolve: unitWith({ id: "open", label: "Open" }) }, { panel: true });
    await o.type(ADDRESS);
    await fireEvent.click(o.button("Open"));
    await settle();
    expect(o.log.steps).toEqual(["get", "post"]);
    expect(o.log.posts.filter((p) => p.url.startsWith("/api/units/"))).toHaveLength(1);
    expect(o.log.closed).toBe(1);
    expect(o.log.opened).toEqual([]);
  });

  test("TestOmniboxPanelHandsAnActionThatNeedsAnInputToTheWindow", async () => {
    for (const input of [{ kind: "duration", required: true }, { kind: "answer" }]) {
      const o = mount({ resolve: unitWith({ id: "guide", label: "Guide", input }) }, { panel: true });
      await o.type(ADDRESS);
      await fireEvent.click(o.button("Guide"));
      await settle();
      expect(o.log.opened).toEqual(["#unit=alpha/acme%2Fapi%238"]);
      expect(o.log.steps).toEqual([]);
      cleanup();
    }
  });

  const REVIEW = { id: "review", label: "Review", input: { kind: "text", label: "Guidance", placeholder: "what the round should look at", max: 40 } };
  const unitPosts = (o: ReturnType<typeof mount>) => o.log.posts.filter((p) => p.url.startsWith("/api/units/")).map((p) => [p.url, p.body]);

  test("an action that takes guidance asks it in the unit's card, as its spec words it, before it runs", async () => {
    for (const panel of [false, true]) {
      const o = mount({ resolve: unitWith(REVIEW) }, { panel });
      await o.type(ADDRESS);
      await fireEvent.click(o.button("Review"));
      await settle();
      const field = o.q(".omni-guide input")[0] as HTMLInputElement;
      expect(field.getAttribute("aria-label")).toBe("Guidance");
      expect(field.placeholder).toBe("what the round should look at");
      expect(field.maxLength).toBe(40);
      expect(document.activeElement).toBe(field);
      expect(o.log.acts).toEqual([]);
      expect(unitPosts(o)).toEqual([]);
      cleanup();
    }
  });

  test("the main window runs the action with the guidance typed in the card, or without it when empty", async () => {
    for (const [typed, want] of [["  every TODO needs an issue ", "every TODO needs an issue"], ["", ""]]) {
      const o = mount({ resolve: unitWith(REVIEW) });
      await o.type(ADDRESS);
      await fireEvent.click(o.button("Review"));
      await settle();
      const field = o.q(".omni-guide input")[0] as HTMLInputElement;
      field.value = typed;
      await fireEvent.input(field);
      await fireEvent.keyDown(field, { key: "Enter" });
      await settle();
      expect(o.log.acts).toEqual(["acme/api#8 review guided " + JSON.stringify(want)]);
      expect(o.q(".omni-guide")).toEqual([]);
      cleanup();
    }
  });

  test("the panel posts the guidance typed in the card, and none when it is empty", async () => {
    for (const [typed, body] of [["focus on the migration", { source: "cockpit", guidance: "focus on the migration" }], ["", { source: "cockpit" }]] as const) {
      const o = mount({ resolve: unitWith(REVIEW) }, { panel: true });
      await o.type(ADDRESS);
      await fireEvent.click(o.button("Review"));
      await settle();
      const field = o.q(".omni-guide input")[0] as HTMLInputElement;
      field.value = typed;
      await fireEvent.input(field);
      await fireEvent.click(o.q(".omni-guide button")[0]);
      await settle();
      expect(o.log.opened).toEqual([]);
      expect(unitPosts(o)).toEqual([["/api/units/alpha/acme%2Fapi%238/review", body]]);
      expect(o.log.closed).toBe(1);
      cleanup();
    }
  });

  test("a required guidance runs only once given, in the panel too", async () => {
    const o = mount({ resolve: unitWith({ id: "guide", label: "Guide", input: { kind: "text", required: true, label: "Brief" } }) }, { panel: true });
    await o.type(ADDRESS);
    await fireEvent.click(o.button("Guide"));
    await settle();
    const run = o.q(".omni-guide button")[0] as HTMLButtonElement;
    expect(run.disabled).toBe(true);
    const field = o.q(".omni-guide input")[0] as HTMLInputElement;
    await fireEvent.keyDown(field, { key: "Enter" });
    await settle();
    expect(unitPosts(o)).toEqual([]);
    field.value = "the brief";
    await fireEvent.input(field);
    expect(run.disabled).toBe(false);
    await fireEvent.click(run);
    await settle();
    expect(unitPosts(o)).toEqual([["/api/units/alpha/acme%2Fapi%238/guide", { source: "cockpit", guidance: "the brief" }]]);
    expect(o.log.opened).toEqual([]);
  });

  test("the main window's own act runs the card's guidance and asks for nothing more", async () => {
    net.fetch = async () => new Response(JSON.stringify({ row: { unit: { provider: "alpha", key: "acme/api#8" }, actions: [{ id: "review", enabled: true }] } }));
    vi.mocked(loaders.refreshSpecs).mockResolvedValue();
    const extras: J[] = [];
    vi.mocked(actions.act).mockImplementation(async (_row: J, _id: string, extra: J) => { extras.push(extra); return true; });
    state.specs = { version: "", byId: { review: REVIEW } };
    const f = fake({ resolve: unitWith(REVIEW) });
    const env: Partial<OmniEnv> = { ...f.env };
    delete env.act;
    const r = render(Omnibox, { onclose: () => {}, env });
    const field = r.container.querySelector(".omni-input") as HTMLInputElement;
    field.value = ADDRESS;
    await fireEvent.input(field);
    await settle();
    const review = [...r.container.querySelectorAll("button")].find((b) => b.textContent === "Review")!;
    await fireEvent.click(review);
    await settle();
    const guide = r.container.querySelector(".omni-guide input") as HTMLInputElement;
    guide.value = "focus on X";
    await fireEvent.input(guide);
    await fireEvent.keyDown(guide, { key: "Enter" });
    await settle();
    expect(extras).toEqual([{ guidance: "focus on X" }]);
    expect(overlays.stack).toEqual([]);
  });

  test("running the card's guidance gives the focus back to the omnibox's field", async () => {
    for (const panel of [false, true]) {
      const o = mount({ resolve: unitWith(REVIEW) }, { panel });
      await o.type(ADDRESS);
      await fireEvent.click(o.button("Review"));
      await settle();
      await fireEvent.keyDown(o.q(".omni-guide input")[0], { key: "Enter" });
      await settle();
      expect(document.activeElement).toBe(o.field);
      cleanup();
    }
  });

  test("clicking the action again keeps what the card's guidance holds", async () => {
    const o = mount({ resolve: unitWith(REVIEW) });
    await o.type(ADDRESS);
    await fireEvent.click(o.button("Review"));
    await settle();
    const field = o.q(".omni-guide input")[0] as HTMLInputElement;
    field.value = "keep me";
    await fireEvent.input(field);
    await fireEvent.click(o.button("Review"));
    await settle();
    expect((o.q(".omni-guide input")[0] as HTMLInputElement).value).toBe("keep me");
    expect(o.log.acts).toEqual([]);
  });

  test("the panel keeps the card's guidance while an action is still running", async () => {
    const o = mount({ resolve: unitWith(REVIEW), actions: { "/api/units/alpha/acme%2Fapi%238/review": new Promise(() => {}) } }, { panel: true });
    await o.type(ADDRESS);
    await fireEvent.click(o.button("Review"));
    await settle();
    await fireEvent.keyDown(o.q(".omni-guide input")[0], { key: "Enter" });
    await settle();
    await fireEvent.click(o.button("Review"));
    await settle();
    const field = o.q(".omni-guide input")[0] as HTMLInputElement;
    field.value = "later";
    await fireEvent.input(field);
    await fireEvent.keyDown(field, { key: "Enter" });
    await settle();
    expect((o.q(".omni-guide input")[0] as HTMLInputElement).value).toBe("later");
  });

  test("Esc in the card's guidance puts it away and keeps the omnibox open", async () => {
    const o = mount({ resolve: unitWith(REVIEW) }, { panel: true });
    await o.type(ADDRESS);
    await fireEvent.click(o.button("Review"));
    await settle();
    await fireEvent.keyDown(o.q(".omni-guide input")[0], { key: "Escape" });
    await settle();
    expect(o.q(".omni-guide")).toEqual([]);
    expect(o.log.closed).toBe(0);
    expect(document.activeElement).toBe(o.field);
  });

  test("new text puts the card's guidance away", async () => {
    const o = mount({ resolve: unitWith(REVIEW) });
    await o.type(ADDRESS);
    await fireEvent.click(o.button("Review"));
    await settle();
    await o.type(ADDRESS + " ");
    expect(o.q(".omni-guide")).toEqual([]);
  });

  test("an operation to confirm that takes guidance shows it, editable, and runs with it", async () => {
    const op = { op: "action", unit: { provider: "alpha", key: "acme/api#8" }, action: "review", guidance: "focus on X", input: REVIEW.input };
    const o = mount({ resolve: { "review acme/api#8 focus on X": { kind: "verb", preview: { operations: [op] } } } });
    await o.type("review acme/api#8 focus on X");
    const field = o.q(".omni-op input.omni-edit")[0] as HTMLInputElement;
    expect(field.value).toBe("focus on X");
    expect(field.getAttribute("aria-label")).toBe("Guidance");
    expect(field.placeholder).toBe("what the round should look at");
    expect(field.maxLength).toBe(40);
    field.value = "focus on Y";
    await fireEvent.input(field);
    await fireEvent.click(o.button("Run"));
    await settle();
    const run = o.log.posts.filter((p) => p.url === "/api/omnibox/run");
    expect(run.map((p) => p.body.operations[0].guidance)).toEqual(["focus on Y"]);
  });

  test("an operation that takes no guidance shows no field for it", async () => {
    const o = mount({ resolve: { "review acme/api#8": { kind: "verb", preview: { operations: [{ op: "action", unit: { provider: "alpha", key: "acme/api#8" }, action: "track" }] } } } });
    await o.type("review acme/api#8");
    expect(o.q(".omni-op input.omni-edit")).toEqual([]);
  });

  test("TestOmniboxPanelRunsAnActionThatCopiesInTheClick", async () => {
    const o = mount({ resolve: copyingUnit(), actions: copied }, { panel: true });
    await o.type(ADDRESS);
    await fireEvent.click(o.button("Copy review request"));
    await settle();
    expect(o.log.steps).toEqual(["claim", "get", "post", "resolve review, pls?"]);
    expect(o.log.closed).toBe(1);
    expect(o.log.opened).toEqual([]);
    expect(o.log.acts).toEqual([]);
  });

  test("TestOmniboxPanelCopyAsksTheRowsConfirmAndSendsItsBasis", async () => {
    const rows = { "/api/units/alpha/acme%2Fapi%238": { row: { actions: [{ id: "copy_review_request", enabled: true, basis: "open@3", confirm: "Copy it?" }] } } };
    const yes = mount({ resolve: copyingUnit(), rows, confirm: true, actions: copied }, { panel: true });
    await yes.type(ADDRESS);
    await fireEvent.click(yes.button("Copy review request"));
    await settle();
    expect(yes.log.steps).toEqual(["claim", "get", "confirm Copy it?", "post", "resolve review, pls?"]);
    expect(yes.log.posts.filter((p) => p.url.startsWith("/api/units/")).map((p) => p.body.basis)).toEqual(["open@3"]);
    expect(yes.log.closed).toBe(1);
    yes.unmount();
    const no = mount({ resolve: copyingUnit(), rows, confirm: false, actions: copied }, { panel: true });
    await no.type(ADDRESS);
    await fireEvent.click(no.button("Copy review request"));
    await settle();
    expect(no.log.posts.filter((p) => p.url.startsWith("/api/units/"))).toHaveLength(0);
    expect(no.log.closed).toBe(0);
    expect(no.log.steps.at(-1)).toBe("reject cancelled");
  });

  test("TestOmniboxPanelCopyRunsOncePerClickInFlight", async () => {
    const o = mount({ resolve: copyingUnit(), actions: copied }, { panel: true });
    await o.type(ADDRESS);
    const b = o.button("Copy review request");
    b.click();
    b.click();
    await settle();
    expect(o.log.steps.filter((s) => s === "post")).toHaveLength(1);
    expect(o.log.steps.filter((s) => s === "claim")).toHaveLength(1);
  });

  test("TestOmniboxPanelRetryClearsTheEarlierFailure", async () => {
    const input: Input = { resolve: copyingUnit(), actions: copied, refuse: "denied" };
    const o = mount(input, { panel: true });
    await o.type(ADDRESS);
    await fireEvent.click(o.button("Copy review request"));
    await settle();
    expect(o.q('[class*="omni-copy"]').length).toBeGreaterThan(0);
    input.refuse = "";
    await fireEvent.click(o.button("Copy review request"));
    await settle();
    expect(o.q('[class*="omni-copy"]')).toEqual([]);
  });

  test("TestOmniboxPanelShowsTheCopyTheClipboardRefused", async () => {
    const o = mount({ resolve: copyingUnit(), actions: copied, refuse: "denied" }, { panel: true });
    await o.type(ADDRESS);
    await fireEvent.click(o.button("Copy review request"));
    await settle();
    expect(o.log.closed).toBe(0);
    expect(o.q('[class*="omni-copy"]').map((x) => (x as HTMLTextAreaElement).value || x.textContent)).toEqual(["Could not copy: denied", "review, pls?"]);
  });

  test("TestOmniboxPanelCopyThatFailsReleasesTheClaim", async () => {
    const o = mount({ resolve: copyingUnit(), actions: { [COPY]: "not offered" } }, { panel: true });
    await o.type(ADDRESS);
    await fireEvent.click(o.button("Copy review request"));
    await settle();
    expect(o.log.steps).toEqual(["claim", "get", "post", "reject not offered"]);
    expect(o.log.closed).toBe(0);
    expect(o.q('[class*="omni-copy"]').map((x) => x.textContent)).toEqual(["Copy review request failed: not offered"]);
  });

  test("TestOmniboxUnitActionTakesCopiesFromTheUnit", async () => {
    const o = mount({ resolve: copyingUnit() });
    await o.type(ADDRESS);
    for (const label of ["Pause", "Copy review request"]) {
      await fireEvent.click(o.button(label));
      await settle();
    }
    expect(o.log.actCopies).toEqual([false, true]);
  });

  test("omniboxAct runs with the guidance given and asks for none", async () => {
    net.fetch = async () => new Response(JSON.stringify({ row: { unit: { provider: "alpha", key: "acme/api#8" }, actions: [{ id: "review", enabled: true }] } }));
    vi.mocked(loaders.refreshSpecs).mockResolvedValue();
    const extras: J[] = [];
    vi.mocked(actions.act).mockImplementation(async (_row: J, _id: string, extra: J) => { extras.push(extra); return true; });
    state.specs = { version: "", byId: { review: { id: "review", input: { kind: "text" } } } };
    await omniboxAct({ provider: "alpha", key: "acme/api#8" }, "review", false, undefined, "focus on X");
    expect(extras).toEqual([{ guidance: "focus on X" }]);
    expect(overlays.stack).toEqual([]);
  });

  test("the panel drops a question it asked when it shows again", async () => {
    net.fetch = async () => new Response(JSON.stringify({ strings: "", build: "" }));
    render(PanelOmnibox, { hide: () => {} });
    const asked = askConfirm("Take it over?");
    flushSync();
    window.dispatchEvent(new Event("blur"));
    window.dispatchEvent(new Event("focus"));
    await settle();
    expect(await asked).toBe(false);
    expect(overlays.stack).toEqual([]);
  });

  test("the panel asks a row's confirm in its own page", async () => {
    net.fetch = async () => new Response("{}");
    const { container } = render(PanelOmnibox, { hide: () => {} });
    const asked = askConfirm("Starts a new round?");
    flushSync();
    expect((container.ownerDocument.body.textContent || "").includes("Starts a new round?")).toBe(true);
    closeOverlays();
    expect(await asked).toBe(false);
  });

  test("TestOmniboxActRunsTheServedRowsAction", async () => {
    const gets: string[] = [];
    net.fetch = async (url: string) => {
      gets.push(url);
      return new Response(JSON.stringify({ row: { unit: { provider: "alpha", key: "acme/api#8" }, actions: [{ id: "pause", enabled: true, confirm: "Pause it?" }] } }));
    };
    vi.mocked(loaders.refreshSpecs).mockResolvedValue();
    const acts: string[] = [];
    vi.mocked(actions.act).mockImplementation(async (row: J, id: string, extra: J) => { acts.push(row.actions[0].confirm + " " + id + " " + JSON.stringify(extra)); return true; });
    state.specs = { version: "", byId: {} };
    await omniboxAct({ provider: "alpha", key: "acme/api#8" }, "pause");
    expect(gets).toEqual(["/api/units/alpha/acme%2Fapi%238"]);
    expect(acts).toEqual(["Pause it? pause {}"]);
  });

  test("TestOmniboxClaimsTheClipboardBeforeItFetches", async () => {
    const order: string[] = [];
    const extra: boolean[] = [];
    vi.mocked(actions.claimCopy).mockImplementation(() => { order.push("claim"); return { pending: { promise: Promise.resolve() as J, resolve() {}, reject() {} }, claimed: null }; });
    net.fetch = async () => { order.push("fetch"); return new Response(JSON.stringify({ row: { unit: { provider: "alpha", key: "acme/api#8" }, actions: [] } })); };
    vi.mocked(loaders.refreshSpecs).mockResolvedValue();
    vi.mocked(actions.act).mockImplementation(async (_row: J, _id: string, x: J) => { order.push("act"); extra.push(!!x.claim); return true; });
    await omniboxAct({ provider: "alpha", key: "acme/api#8" }, "copy_review_request", true);
    expect(order).toEqual(["claim", "fetch", "act"]);
    expect(extra).toEqual([true]);
  });

  test("TestOmniboxEnterOnAUnitRunsNothing", async () => {
    const input = { resolve: pastedUnit({ id: "t-8", title: "review 8" }) };
    const o = mount(input);
    await o.type(ADDRESS);
    await o.key("Enter");
    o.unmount();
    const bare = mount({ resolve: pastedUnit() });
    await bare.type(ADDRESS);
    await bare.key("Enter");
    expect([...o.log.opened, ...bare.log.opened]).toEqual(["#task=t-8", "#unit=alpha/acme%2Fapi%238"]);
    expect([...o.log.acts, ...bare.log.acts]).toEqual([]);
  });

  test("TestOmniboxAskAgainAfterTypingDuringAnAsk", async () => {
    const f = fake({ ask: [{ answer: { text: "old", tasks: [] } }, { answer: { text: "new", tasks: [] } }] });
    const st = omniboxNew(false, []);
    await omniboxType(st, "who waits", f.env);
    const first = omniboxEnter(st, f.env, true);
    await omniboxType(st, "who waits on Ada", f.env);
    await first;
    await omniboxEnter(st, f.env, true);
    expect(f.log.posts.filter((p) => p.url === "/api/omnibox/ask")).toHaveLength(2);
    expect(st.asking).toBe(false);
    expect(st.answer.answer.text).toBe("new");
  });

  test("TestOmniboxPanelMode: the omnibox alone, modal, no backdrop, Esc anywhere in it hides the panel", async () => {
    let hidden = 0;
    const { container } = render(PanelOmnibox, { hide: () => hidden++ });
    const box = container.querySelector(".pal") as HTMLElement;
    expect(box.className).toContain("omni-panel");
    expect(container.querySelector(".pal-back")).toBeNull();
    expect(box.getAttribute("aria-modal")).toBe("true");
    expect([...box.children].filter((c) => c.getAttribute("aria-live"))).toHaveLength(0);
    expect(document.body.classList.contains("panel-omnibox")).toBe(true);
    await fireEvent.keyDown(box.querySelector(".omni-keys") as HTMLElement, { key: "Escape" });
    expect(hidden).toBe(1);
  });

  test("TestOmniboxNeverOffersMerge: the omnibox lists only what the daemon serves", async () => {
    const o = mount({ search: retriesSearch() });
    await o.type("retries");
    expect(o.container.textContent).not.toContain("merge");
  });

  test("TestOmniboxArrowsMoveTheSelection", async () => {
    const o = mount({ search: retriesSearch() });
    await o.type("retries");
    expect(o.selected()).toEqual(["task:t-1"]);
    expect(o.q('[role="option"]')).toHaveLength(2);
    const down = new KeyboardEvent("keydown", { key: "ArrowDown", cancelable: true, bubbles: true });
    o.field.dispatchEvent(down);
    await settle();
    expect(down.defaultPrevented).toBe(true);
    expect(o.selected()).toEqual(["task:t-2"]);
    await o.key("ArrowDown");
    expect(o.selected()).toEqual(["task:t-2"]);
    await o.key("ArrowUp");
    expect(o.selected()).toEqual(["task:t-1"]);
    await o.key("n", { ctrlKey: true });
    expect(o.selected()).toEqual(["task:t-2"]);
    await o.key("p", { ctrlKey: true });
    expect(o.selected()).toEqual(["task:t-1"]);
  });

  test("TestOmniboxEnterOpensTheSelectedResult", async () => {
    const o = mount({ search: retriesSearch(), ask: [{ answer: { text: "none" } }] });
    await o.type("retries");
    await o.key("ArrowDown");
    await o.key("Enter");
    await o.key("Enter", { metaKey: true });
    expect(o.log.opened).toEqual(["#task=t-2"]);
    expect(o.log.posts.filter((p) => p.url === "/api/omnibox/ask")).toHaveLength(1);
  });

  test("TestOmniboxSelectionResetsOnANewQueryAndKeepsAcrossRedraws", async () => {
    const f = fake({ search: retriesSearch() });
    const r = render(Omnibox, { onclose: () => {}, env: f.env });
    const field = r.container.querySelector(".omni-input") as HTMLInputElement;
    const selected = () => [...r.container.querySelectorAll<HTMLElement>('[aria-selected="true"]')].map((x) => x.dataset.choice);
    field.value = "retries";
    await fireEvent.input(field);
    await settle();
    await fireEvent.keyDown(field, { key: "ArrowDown" });
    await settle();
    expect(selected()).toEqual(["task:t-2"]);
    const kept = r.container.querySelector('[data-choice="task:t-2"]');
    field.value = "re";
    await fireEvent.input(field);
    await settle();
    expect(selected()).toEqual(["task:t-9"]);
    expect(kept && kept.isConnected).toBe(false);
  });

  test("TestOmniboxEnterRunsTheSelectedUnit", async () => {
    const o = mount({ resolve: pastedUnit() });
    await o.type(ADDRESS);
    expect(o.selected()).toEqual(["unit"]);
    await o.key("Enter");
    expect(o.log.opened).toHaveLength(1);
  });

  test("TestOmniboxKeysHintIsWorded", () => {
    I18N.strings = { "to navigate": "para navegar", "to open": "para abrir", "to ask the agent": "para perguntar ao agente", "to close": "para fechar" };
    const o = mount({});
    const hint = o.q(".omni-keys")[0].textContent || "";
    for (const w of ["para navegar", "para abrir", "para perguntar ao agente", "para fechar"]) expect(hint).toContain(w);
  });

  test("TestOmniboxTabStaysInside", () => {
    const stops = [{ name: "input" }, { name: "first" }, { name: "last" }];
    const at = (el: J, shift = false) => (omniboxTrap({ key: "Tab", shiftKey: shift }, stops, el) || { name: "" }).name;
    expect([at(stops[2]), at(stops[0], true), at(stops[1])]).toEqual(["input", "last", ""]);
  });

  test("TestOmniboxTabStaysInside: Tab from the last control goes back to the field", async () => {
    const o = mount({ resolve: pastedUnit() });
    await o.type(ADDRESS);
    const last = o.button("Remove");
    last.focus();
    await fireEvent.keyDown(last, { key: "Tab" });
    expect(document.activeElement).toBe(o.field);
  });

  test("TestOmniboxAnswerRendersMarkdown", async () => {
    const o = mount({ ask: [{ answer: { text: "Two **tasks** wait", tasks: [] } }] });
    await o.type("what waits");
    await o.key("Enter", { metaKey: true });
    expect(o.q(".omni-answer strong")).toHaveLength(1);
    expect(o.q(".omni-answer")[0].textContent).not.toContain("**");
  });

  test("TestOmniboxRedrawKeepsTheFocusAndPointsAtTheSelection: no node is rebuilt, the field points at the selection", async () => {
    const o = mount({ resolve: verbResolve, search: { "done t-1": retriesSearch().retries } });
    await o.type("done t-1");
    const box = o.q('input[type="checkbox"]')[0] as HTMLInputElement;
    box.focus();
    await o.type("done t-1");
    expect(o.q('input[type="checkbox"]')[0]).toBe(box);
    expect(document.activeElement).toBe(box);
    await o.key("ArrowDown");
    expect(o.field.getAttribute("aria-activedescendant")).toBe("omni-opt-1");
    expect(document.getElementById("omni-opt-1")?.getAttribute("role")).toBe("option");
  });

  test("TestOmniboxEnterActsOnlyOnTheAnswerForTheCurrentText", async () => {
    const f = fake({ search: retriesSearch() });
    const st = omniboxNew(false, []);
    await omniboxType(st, "re", f.env);
    f.ctl.hold = true;
    const typed = omniboxType(st, "retries", f.env);
    const entered = omniboxEnter(st, f.env, false);
    await settle();
    expect(f.log.opened).toEqual([]);
    f.ctl.hold = false;
    f.held.splice(0).forEach((fn) => fn());
    await typed;
    await entered;
    expect(f.log.opened).toEqual(["#task=t-1"]);
  });

  test("TestTheOmniboxListboxHoldsOnlyOptions", async () => {
    const o = mount({ resolve: verbResolve, search: { "done t-1": retriesSearch().retries } });
    await o.type("done t-1");
    const lists = o.q('[role="listbox"]');
    expect(lists).toHaveLength(1);
    const inList = new Set(lists.flatMap((l) => [...l.querySelectorAll('[role="option"]')]));
    expect(o.q('[role="option"]').filter((x) => !inList.has(x))).toHaveLength(0);
    expect(lists[0].querySelectorAll("button, input, a, textarea")).toHaveLength(0);
    await o.key("ArrowDown");
    expect(o.q("output")[0].textContent).toBe("2 results");
    await fireEvent.click(lists[0].querySelectorAll('[role="option"]')[1]);
    expect(o.log.opened).toEqual(["#task=t-2"]);
  });

  test("TestTheOmniboxPointsOnlyAtAnOptionAndSaysAnyOtherSelection", async () => {
    const o = mount({ resolve: verbResolve, search: { "done t-1": retriesSearch().retries } });
    await o.type("done t-1");
    expect(o.field.hasAttribute("aria-activedescendant")).toBe(false);
    expect(o.q("output")[0].textContent).toContain("Run 1");
    expect(o.q("output")[0].classList.contains("said")).toBe(true);
    await o.key("ArrowDown");
    expect(o.q("output")[0].classList.contains("said")).toBe(false);
    const id = o.field.getAttribute("aria-activedescendant") as string;
    expect(id).not.toBe("");
    expect(document.getElementById(id)?.getAttribute("role")).toBe("option");
  });
});

describe("panel_test.go", () => {
  test("TestThePanelFreshensEachTimeItShowsAgain", async () => {
    const steps: string[] = [];
    const win = new EventTarget();
    const doc = Object.assign(new EventTarget(), { visibilityState: "visible" as DocumentVisibilityState });
    panelFollow(win as J, doc as J, {
      reset: () => steps.push("reset"),
      health: async () => ({ strings: "pt-2", build: "b-2" }),
      strings: (stamp) => steps.push("strings " + stamp),
      build: (h) => { steps.push("build " + h.build); },
    });
    win.dispatchEvent(new Event("focus"));
    await settle();
    steps.push("|");
    win.dispatchEvent(new Event("blur"));
    win.dispatchEvent(new Event("focus"));
    doc.dispatchEvent(new Event("visibilitychange"));
    await settle();
    steps.push("|");
    doc.visibilityState = "hidden";
    doc.dispatchEvent(new Event("visibilitychange"));
    doc.visibilityState = "visible";
    doc.dispatchEvent(new Event("visibilitychange"));
    await settle();
    expect(steps).toEqual(["|", "reset", "strings pt-2", "build b-2", "|", "reset", "strings pt-2", "build b-2"]);
  });

  test("TestThePanelRendersItsOwnPage: new strings reword the panel's omnibox and keep its text and field", async () => {
    net.fetch = async () => new Response("{}");
    const { container } = render(PanelOmnibox, { hide: () => {} });
    const field = container.querySelector(".omni-input") as HTMLInputElement;
    field.value = "retr";
    I18N.locale = "pt-BR";
    I18N.strings = { "Search, paste a link, or ask": "Busque, cole um link ou pergunte" };
    flushSync();
    const again = container.querySelector(".omni-input") as HTMLInputElement;
    expect(again).toBe(field);
    expect(again.value).toBe("retr");
    expect(again.placeholder).toBe("Busque, cole um link ou pergunte");
  });

  test("the panel starts an empty omnibox each time it shows again", async () => {
    net.fetch = async () => new Response(JSON.stringify({ strings: "", build: "" }));
    const { container } = render(PanelOmnibox, { hide: () => {} });
    const field = container.querySelector(".omni-input") as HTMLInputElement;
    field.value = "retr";
    window.dispatchEvent(new Event("blur"));
    window.dispatchEvent(new Event("focus"));
    await settle();
    const fresh = container.querySelector(".omni-input") as HTMLInputElement;
    expect(fresh).not.toBe(field);
    expect(fresh.value).toBe("");
  });
});
