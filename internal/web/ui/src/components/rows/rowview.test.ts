import { cleanup, fireEvent, render } from "@testing-library/svelte";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { flushSync } from "svelte";
import { fmtDate } from "../../lib/i18n.svelte";
import { filtersOf, lensOf, lensWords, passesFilters, pipeline, qualifiers, terms, toggledQualifier, type Lens } from "../../lib/lens";
import { taskItemOf } from "../../lib/tasks";
import { keyOf, type J } from "../../lib/util";
import { applyPlugins } from "../../lib/catalog";
import { resetStore } from "../../testing/reset.svelte";
import { readHash } from "../../state/route.svelte";
import { catalog, state } from "../../state/store.svelte";
import { countdown, pieceLabel, rowModel, rowTip, taskModel, unitTitle, type RowModel } from "./model";
import TestRow from "./testdata/TestRow.svelte";
import TestList from "./testdata/TestList.svelte";
import { fixture, task, useCatalogs } from "./testdata/fixtures";

const NOW = Date.parse("2026-10-12T12:00:00Z");
const SLACK_KEY = "T0001/C0001/1700000000.000100";

afterEach(cleanup);

const lensFor = (view: string): Lens => lensOf({ ...readHash(), view });
const tagRow = (i: number): J => structuredClone(fixture("tag-rows.json")[i]);

function draw(make: () => { model: RowModel; lens: Lens }, card = false): HTMLElement {
  const { container } = render(TestRow, { make, card });
  return container.querySelector(card ? ".card" : ".row") as HTMLElement;
}

const rowOf = (r: J, view: string, pr = "", kind = "row") => draw(() => ({ model: rowModel(r, pr, lensFor(view), "", kind), lens: lensFor(view) }), kind === "card");
const taskOf = (e: J, view: string, card = false) => draw(() => ({ model: taskModel(e, "", "", lensFor(view)), lens: lensFor(view) }), card);

// shown reads back each facet button of a drawn row: the piece it wraps and whether the filter its click adds
// keeps the item it came from.
function shown(row: Element, it: J, lens: Lens) {
  return Array.from(row.querySelectorAll("button.facet")).filter((b) => b.firstElementChild && b.firstElementChild.getAttribute("data-field")).map((b) => {
    const span = b.firstElementChild!;
    const f = span.getAttribute("data-field")!, v = span.getAttribute("data-value")!;
    const quals = qualifiers(toggledQualifier("", f, v, false, lens.fields), lens.fields);
    return { field: f, value: v, label: (span.textContent || span.getAttribute("title") || "").trim(), kept: quals.length === 1 && passesFilters(it, filtersOf(quals, lens.fields)) };
  });
}
const labelsOf = (ps: ReturnType<typeof shown>) => ps.map((p) => p.field + ":" + p.label);

const ROW_SLOTS = ["glyph", "why", "aside", "who", "age", "act"];
function slots(row: Element): string[] {
  const out = new Set<string>();
  for (const n of [row, ...row.querySelectorAll("*")]) for (const c of n.classList) if (ROW_SLOTS.includes(c) && (c !== "who" || n.children.length)) out.add(c);
  return ROW_SLOTS.filter((s) => out.has(s));
}

const slack = (): J => ({
  unit: { provider: "slack", key: SLACK_KEY }, label: "#general", title: "Deploy plan", kind: "waits_on_others",
  views: ["slack"], summary: { waits_on: "Ana", since: "2026-10-12T09:00:00Z" },
  facets: { where: ["general"], "waits-on": ["Ana"], conversation: ["#general"] },
});

describe("a row's pieces", () => {
  beforeEach(() => resetStore(NOW));

  it("are each a facet that keeps its row", () => {
    const cat = fixture("catalog.github.json");
    cat.facets.push({ id: "where", label: "Where", provider: "slack", group: true, sort: "text", match: "suffix", menu: true, show: "ref", label_prefix: "#" },
      { id: "waits-on", label: "Waits on (person)", provider: "slack", group: true, match: "team", menu: true, show: "who" });
    cat.views.push({ id: "slack", label: "Slack", provider: "slack", rows: true, surfaces: ["web"], unit: "Conversation" });
    useCatalogs(cat);
    const pr = tagRow(1);
    pr.payload.override = { hold: true };
    pr.summary.reason = "changes requested by bia";
    pr.views = ["mine", "stalled"];
    pr.facets.is = ["stalled", "paused"];
    const e = task("e-1", "next", (e) => {
      e.title = "Answer the deploy question";
      e.units = [{ provider: "slack", key: SLACK_KEY }];
      e.unit_labels = ["#general thread"];
      e.facets = { source: ["slack"], state: ["next"], "waits-on": ["bia"], contexts: ["calls"], turn: ["others"], moved: ["2026-10-12T10:30:00Z"], where: ["general"], risk: ["none"] };
    });
    const s = slack();
    state.rows = [pr, s];
    const prRow = rowOf(pr, "mine"), slackRow = rowOf(s, "slack"), taskRow = taskOf(e, "inbox");
    const got = { pr: shown(prRow, pr, lensFor("mine")), slack: shown(slackRow, s, lensFor("slack")), task: shown(taskRow, taskItemOf(e), lensFor("inbox")) };
    for (const ps of Object.values(got)) {
      expect(ps.length).toBeGreaterThan(0);
      for (const p of ps) {
        expect(p.kept, `${p.field}:${p.value}`).toBe(true);
        expect(p.value).not.toBe("none");
        expect(p.label).not.toBe("none");
      }
    }
    expect(labelsOf(got.pr)).toEqual(expect.arrayContaining(["where:acme/web", "author:by me", "size:L", "risk:risk high", "tag:lang:go", "tag:lang:sql", "is:paused", "kind:changes asked"]));
    expect(Array.from(prRow.classList)).toEqual(expect.arrayContaining(["mine", "late"]));
    expect(slots(prRow)).toEqual(["why", "aside", "who", "age"]);
    expect(labelsOf(got.slack)).toEqual(["where:#general", "waits-on:Ana"]);
    expect((slackRow.textContent || "").split("general").length - 1).toBe(1);
    expect(labelsOf(got.task)).toEqual(expect.arrayContaining(["source:Slack", "contexts:@calls", "waits-on:bia", "state:Next"]));
    expect(labelsOf(got.task).some((l) => l.startsWith("moved:moved ") && !l.includes("{{"))).toBe(true);
    expect(got.task.some((p) => p.field === "where")).toBe(false);
    expect(slots(taskRow)).toEqual(["glyph", "who", "age"]);
  });

  it("render with no GitHub facet", () => {
    useCatalogs({
      views: [{ id: "slack", label: "Slack", provider: "slack", rows: true, surfaces: ["web"], unit: "Conversation" }],
      facets: [{ id: "where", label: "Where", provider: "slack", group: true, match: "suffix", menu: true, show: "ref" },
        { id: "waits-on", label: "Waits on (person)", provider: "slack", group: true, match: "team", menu: true, show: "who" }],
      situations: [{ provider: "slack", kind: "new", label: "new" }],
    });
    const s = { ...slack(), kind: "new", summary: { waits_on: "Ana" }, facets: { where: ["general"], "waits-on": ["Ana"] }, actions: [] };
    state.rows = [s];
    expect(labelsOf(shown(rowOf(s, "slack"), s, lensFor("slack")))).toEqual(["where:general", "waits-on:Ana"]);
    expect(unitTitle("slack", lensFor("slack"))).toBe("Conversation");
  });

  it("speak their own plugin's words", () => {
    const cat = fixture("catalog.github.json");
    cat.lens = [{ provider: "github", words: { filter_label: "GitHub filter {{.value}}", countdown_title: "GitHub sends at {{.time}}" } }];
    useCatalogs(cat);
    const pr = tagRow(0);
    pr.views = ["all"];
    pr.actions = [{ id: "open", enabled: true }];
    const s = { unit: { provider: "slack", key: SLACK_KEY }, label: "#general", title: "Deploy plan", kind: "new", views: ["all"], summary: { waits_on: "Ana" }, facets: { author: ["me"], "waits-on": ["Ana"] } };
    state.rows = [pr, s];
    state.roundGates.set(keyOf(pr), { deadline: new Date(NOW + 600000).toISOString() });
    const countdownTitle = countdown(pr).title;
    state.roundGates.clear();
    state.specs = { version: "", byId: { open: { id: "open", label: "Open", group: "control", surfaces: ["row"] } } };
    const key = keyOf(pr);
    const prRow = rowOf(pr, "all", key), slackRow = rowOf(s, "all", key);
    const labels = (row: Element) => Array.from(row.querySelectorAll("button.facet")).map((b) => b.getAttribute("aria-label") || "");
    expect(prRow.classList.contains("sel")).toBe(true);
    expect(prRow.getAttribute("aria-current")).toBe("true");
    expect(Array.from(prRow.querySelectorAll(".act")).map((b) => b.textContent)).toEqual(["Open"]);
    expect(prRow.querySelector('[data-field="age"]')!.getAttribute("title")).toBeTruthy();
    expect(labels(prRow)[0]).toMatch(/^GitHub filter /);
    expect(labels(slackRow).some((l) => l.startsWith("GitHub filter "))).toBe(false);
    expect(slackRow.classList.contains("mine")).toBe(false);
    expect(countdownTitle).toMatch(/^GitHub sends at /);
  });

  it("name a value by its word", () => {
    const cat = fixture("catalog.github.json");
    cat.facets.find((f: J) => f.id === "risk").labels = { medium: "médio" };
    useCatalogs(cat);
    const pr = tagRow(0);
    pr.views = ["all"];
    pr.facets.risk = ["medium"];
    state.rows = [pr];
    const buttons = Array.from(rowOf(pr, "all").querySelectorAll("button.facet")).filter((b) => (b.getAttribute("data-focus") || "").includes(":risk:"));
    expect(buttons.length).toBe(1);
    expect(buttons[0].getAttribute("aria-label")).toContain("médio");
    expect(buttons[0].getAttribute("aria-label")).not.toContain("medium");
    expect(buttons[0].getAttribute("title")).not.toContain("medium");
  });
});

describe("a row's why", () => {
  beforeEach(() => resetStore(NOW));

  it("reads in its plugin's words", () => {
    const cat = fixture("catalog.github.json");
    cat.reasons = [{ provider: "github", words: { changes_requested: "mudanças pedidas por {{.actor}}" } }];
    useCatalogs(cat);
    const pr = tagRow(0);
    pr.views = ["all"];
    Object.assign(pr.summary, { reason: "changes requested by bia", reason_key: "changes_requested", reason_params: { actor: "bia" } });
    const other = tagRow(1);
    other.views = ["all"];
    other.summary.reason = "round soon: review";
    state.rows = [pr, other];
    const m = rowModel(pr, "", lensFor("all"), "");
    expect(m.why).toBe("mudanças pedidas por bia");
    expect(m.tip).toContain("mudanças pedidas por bia");
    expect(rowModel(other, "", lensFor("all"), "").why).toBe("round soon: review");
  });
});

describe("a board card", () => {
  beforeEach(() => resetStore(NOW));

  function cardShown(card: HTMLElement, row: HTMLElement, aside: J[] = []) {
    const open = card.querySelector(".open") as HTMLElement | null;
    const facets = Array.from(card.querySelectorAll("button.facet"));
    const pieceOf = (b: Element) => b.firstElementChild!.getAttribute("data-field") + ":" + b.firstElementChild!.getAttribute("data-value");
    const asideOf = aside.map((p) => p.field + ":" + p.value);
    return {
      tag: card.tagName, cls: Array.from(card.classList), row: card.getAttribute("data-row") || "", tip: open ? open.getAttribute("title") || "" : "", rowTip: row.getAttribute("title") || "",
      openTag: open ? open.tagName : "", openFocus: open ? open.getAttribute("data-focus") : "", openTab: open ? open.getAttribute("tabindex") || "" : "none",
      openCurrent: open ? open.getAttribute("aria-current") || "" : "", openHref: open ? open.getAttribute("href") || "" : "",
      outside: facets.every((f) => !open || !open.contains(f)), rove: facets.every((f) => f.getAttribute("tabindex") === "-1" && f.hasAttribute("data-rove")),
      titled: Array.from(card.querySelectorAll("[title]")).filter((n) => !n.classList.contains("open") && !(open && open.contains(n))).map((n) => n.tagName.toLowerCase() + "." + n.className),
      pieces: facets.filter((f) => f.firstElementChild && f.firstElementChild.hasAttribute("data-field")).map(pieceOf),
      rowPieces: Array.from(row.querySelectorAll("button.facet")).filter((f) => f.firstElementChild && f.firstElementChild.hasAttribute("data-field")).map(pieceOf).filter((p) => !asideOf.includes(p)),
    };
  }

  it("is its row's component", async () => {
    useCatalogs(fixture("catalog.bundled.json"));
    const pr = tagRow(1);
    pr.views = ["mine"];
    pr.payload.workspace = { status: "reviewing" };
    pr.facets.status = ["reviewing"];
    const s = { ...slack(), facets: { where: ["general"], "waits-on": ["Ana"], turn: ["others"], situation: ["waits_on_others"], age: ["2026-10-12T09:00:00Z"] } };
    const e = task("e-1", "next", (e) => (e.facets = { state: ["next"], "waits-on": ["me"], turn: ["me"], moved: ["2026-10-12T10:30:00Z"] }));
    const draft = fixture("outbox-tasks.json")[0];
    state.rows = [pr, s];
    const key = keyOf(pr);
    const unit = (r: J, view: string, sel: string) => {
      const m = rowModel(r, sel, lensFor(view), "", "card");
      return cardShown(rowOf(r, view, sel, "card"), rowOf(r, view, sel), m.parts.aside);
    };
    const taskShown = (t: J, view: string) => cardShown(taskOf(t, view, true), taskOf(t, view), taskModel(t, "", "", lensFor(view)).parts.aside);
    const got: Record<string, ReturnType<typeof cardShown>> = { pr: unit(pr, "mine", key), slack: unit(s, "slack", ""), task: taskShown(e, "inbox"), draft: taskShown(draft, "outbox") };
    for (const [name, c] of Object.entries(got)) {
      expect(c.tag, name).toBe("LI");
      expect(c.cls).toContain("card");
      expect(c.row).not.toBe("");
      expect(c.openFocus).toBe(c.row);
      expect(c.openTab).toBe("");
      if (name !== "draft") expect(c.pieces.length, name).toBeGreaterThan(0);
      expect(c.outside && c.rove, name).toBe(true);
      expect(c.pieces, name).toEqual(c.rowPieces);
      expect(c.titled).not.toContain("li.card");
      expect(c.tip, name).toBe(c.rowTip);
    }
    expect([got.pr.openTag, got.pr.openCurrent, got.slack.openCurrent]).toEqual(["BUTTON", "true", ""]);
    expect(got.pr.row.startsWith("card:")).toBe(true);
    expect(got.draft.openTag).toBe("A");
    expect(got.draft.openHref).toContain("task=draft");
    const card = rowOf(pr, "mine", "", "card");
    await fireEvent.click(card.querySelector(".open")!);
    expect(state.openSlot.startsWith("card:")).toBe(true);
    expect(location.hash).toContain("acme");
  });

  it("keeps its raised pieces outside its title, so they stay under a lane's head", () => {
    useCatalogs(fixture("catalog.bundled.json"));
    const pr = tagRow(1);
    pr.views = ["mine"];
    state.rows = [pr];
    const card = rowOf(pr, "mine", "", "card");
    expect(card.querySelector(".open button.facet")).toBeNull();
  });
});

describe("a row's roving focus", () => {
  beforeEach(() => resetStore(NOW));

  it("moves with the arrows inside a row and a card", async () => {
    useCatalogs(fixture("catalog.bundled.json"));
    const pr = tagRow(1);
    pr.views = ["mine"];
    pr.actions = [{ id: "open", enabled: true }, { id: "hold", enabled: false }];
    state.rows = [pr];
    state.specs = { version: "", byId: { open: { id: "open", label: "Open", surfaces: ["row"] }, hold: { id: "hold", label: "Pause", surfaces: ["row"] } } };
    const row = rowOf(pr, "mine");
    const card = rowOf(pr, "mine", "", "card");
    const { rowStops } = await import("./rove");
    const name = (n: Element) => n.getAttribute("data-focus") || n.className || n.tagName;
    const stops = rowStops(row), cardStops = rowStops(card);
    const names = stops.map(name);
    expect(names.length).toBeGreaterThanOrEqual(4);
    expect(names[0].startsWith("row:")).toBe(true);
    expect(names.some((s) => s.endsWith(":link"))).toBe(true);
    expect(names.some((s) => s.startsWith("facet:"))).toBe(true);
    expect(names.some((s) => s.endsWith(":open"))).toBe(true);
    expect(names.some((s) => s.endsWith(":hold"))).toBe(false);
    expect(name(cardStops[0]).startsWith("card:")).toBe(true);
    const tabbable = (root: Element) => [root, ...root.querySelectorAll("*")].filter((n) => (["BUTTON", "A"].includes(n.tagName) || n.getAttribute("tabindex") === "0") && n.getAttribute("tabindex") !== "-1" && !n.hasAttribute("disabled")).map(name);
    expect(tabbable(row)).toEqual([names[0]]);
    expect(tabbable(card)).toEqual([name(cardStops[0])]);
    const press = (root: HTMLElement, target: Element, key: string, mods: KeyboardEventInit = {}) => {
      (document.activeElement as HTMLElement | null)?.blur();
      const e = new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true, ...mods });
      target.dispatchEvent(e);
      return { took: e.defaultPrevented, to: document.activeElement && root.contains(document.activeElement) ? name(document.activeElement) : "" };
    };
    expect(press(row, row, "ArrowRight").to).toBe(names[1]);
    expect(press(row, stops[1], "ArrowLeft").to).toBe(names[0]);
    expect(press(row, row, "End").to).toBe(names[names.length - 1]);
    expect(press(row, stops[stops.length - 1], "Home").to).toBe(names[0]);
    expect(press(card, cardStops[0], "ArrowRight").to).toBe(name(cardStops[1]));
    expect(press(row, row, "ArrowRight", { shiftKey: true }).took).toBe(false);
  });
});

describe("a countdown and a tooltip", () => {
  beforeEach(() => resetStore(NOW));

  it("speak the row words", async () => {
    useCatalogs(fixture("catalog.bundled.json"));
    catalog.lens = [{ provider: "slack", words: { countdown_seconds: "{{.s}}s to go", tip_kind: "{{.tip}} | {{.kind}}", tip_why: "{{.tip}} | {{.why}}", tip_named: "{{.strip}} [{{.who}}]" } }];
    const { left } = await import("./model");
    const { rowWords } = await import("../../lib/lens");
    const at = (s: number) => new Date(NOW + s * 1000).toISOString();
    const w = rowWords(""), sl = rowWords("slack");
    const strip = { label: "Others' turn" };
    expect([left(at(5), w, NOW), left(at(125), w, NOW), left(at(-1), w, NOW), left(at(5), sl, NOW),
      rowTip(strip, "Ana", "waits on others", "she asked", w), rowTip(strip, "Ana", "waits on others", "she asked", sl), rowTip(null, "", "", "she asked", w)])
      .toEqual(["in 5 s", "in 2m 05s", "sending", "5s to go", "Others' turn (Ana): waits on others. she asked", "Others' turn [Ana] | waits on others | she asked", "she asked"]);
  });

  it("counts down on its node as the clock moves", async () => {
    useCatalogs();
    const pr = tagRow(0);
    pr.views = ["all"];
    state.rows = [pr];
    state.roundGates.set(keyOf(pr), { deadline: new Date(NOW + 5000).toISOString() });
    const row = rowOf(pr, "all");
    const el = row.querySelector(".countdown")!;
    expect(el.textContent).toBe("in 5 s");
    const { clock } = await import("../../state/clock.svelte");
    clock.now = NOW + 2000;
    flushSync();
    expect(row.querySelector(".countdown")).toBe(el);
    expect(el.textContent).toBe("in 3 s");
  });

  it("names nobody the loop waits on", () => {
    useCatalogs(fixture("catalog.bundled.json"));
    const base = fixture("lens-rows.json")[4];
    for (const kind of ["loop_working", "loop_waiting"]) {
      const r = { ...structuredClone(base), kind, views: ["mine"], summary: { ...base.summary, waits_on: "me" }, facets: { ...base.facets, kind: [kind], wait: ["loop"], "waits-on": ["me"] } };
      state.rows = [r];
      const row = rowOf(r, "mine");
      expect(row.querySelector(".dot")!.getAttribute("data-value")).toBe("loop");
      expect(row.getAttribute("title")).toMatch(/^In loop: /);
      expect(row.getAttribute("title")).not.toContain("(");
    }
  });
});

describe("each row's strip", () => {
  beforeEach(() => resetStore(NOW));

  it("is its own provider's", () => {
    useCatalogs(fixture("catalog.bundled.json"));
    const pr = tagRow(1);
    pr.views = ["mine"];
    const s = { ...slack(), facets: { where: ["general"], "waits-on": ["Ana"], turn: ["others"], situation: ["waits_on_others"], age: ["2026-10-12T09:00:00Z"] } };
    const other = { unit: { provider: "thread", key: "C1/171.1" }, label: "C1", title: "A thread", kind: "new", views: ["slack"], summary: { waits_on: "Ana" }, facets: { "waits-on": ["Ana"], turn: ["others"] } };
    const e = task("e-1", "next", (e) => (e.facets = { state: ["next"], "waits-on": ["me"], turn: ["me"] }));
    const theirs = task("e-2", "waiting", (e) => (e.facets = { state: ["waiting"], "waits-on": ["ana"], turn: ["others"] }));
    state.rows = [pr, s, other];
    const strip = (row: HTMLElement, it: J, view: string) => {
      const lens = lensFor(view);
      const dot = row.querySelector(".dot");
      const field = dot ? dot.getAttribute("data-field")! : "", value = dot ? dot.getAttribute("data-value")! : "";
      const quals = dot ? qualifiers(toggledQualifier("", field, value, false, lens.fields), lens.fields) : [];
      return { field, value, button: !!(dot && dot.parentElement!.matches("button.facet")), kept: quals.length === 1 && passesFilters(it, filtersOf(quals, lens.fields)),
        strip: Array.from(row.classList).filter((c) => c.startsWith("w-")), sort: lens.strip, tip: row.getAttribute("title") || "" };
    };
    const got = { pr: strip(rowOf(pr, "mine"), pr, "mine"), slack: strip(rowOf(s, "slack"), s, "slack"), other: strip(rowOf(other, "slack"), other, "slack"),
      task: strip(taskOf(e, "inbox"), taskItemOf(e), "inbox"), theirs: strip(taskOf(theirs, "inbox"), taskItemOf(theirs), "inbox") };
    expect(got.pr).toMatchObject({ field: "wait", button: true, kept: true, strip: ["w-" + got.pr.value], sort: "wait" });
    expect(got.slack).toMatchObject({ field: "turn", value: "others", kept: true, strip: ["w-author"], sort: "turn" });
    expect(got.slack.tip.startsWith("Others' turn (Ana): waits on others")).toBe(true);
    const slackModel = rowModel(s, "", lensFor("slack"), "");
    const pieces = Object.values(slackModel.parts).flat().map((p) => p.field + ":" + pieceLabel(p));
    expect(pieces).toEqual(expect.arrayContaining(["situation:waits on others", "age:since " + fmtDate("2026-10-12T09:00:00Z", "datetime")]));
    expect(got.other.field).toBe("");
    expect(got.other.strip).toEqual([]);
    expect(got.task).toMatchObject({ field: "turn", value: "me", kept: true, strip: ["w-me"] });
    expect(got.theirs).toMatchObject({ field: "turn", value: "others", kept: true, strip: ["w-author"] });
    const lens = lensFor("slack");
    const { container } = render(TestList, { make: () => ({ res: { group: "turn", groups: [{ field: "turn", key: "others", raw: "others", label: "Others' turn", rows: [s] }], total: 1, shown: 1 }, lens, model: (r: J, copy: string) => rowModel(r, "", lens, copy) }) });
    expect(container.querySelector(".g-head")!.classList.contains("w-author")).toBe(true);
  });

  it("reads only what its provider declares in its tooltip", () => {
    useCatalogs(fixture("catalog.bundled.json"));
    expect(rowTip({ label: "In loop" }, "ana", "agent working", "", lensWords())).toBe("In loop (ana): agent working");
    expect(rowTip({ label: "Waiting on me" }, "me", "review", "", lensWords())).toBe("Waiting on me: review");
  });

  it("explains itself in each row's tooltip and stands out on my pull requests", () => {
    useCatalogs();
    const rows = fixture("tag-rows.json").slice(0, 1).map((r: J) => ({ ...r, views: ["mine"] }));
    state.rows = rows;
    const row = rowOf(rows[0], "mine");
    const card = rowOf(rows[0], "mine", "", "card");
    expect(card.querySelector(".open")!.getAttribute("title")).toBe(row.getAttribute("title"));
  });
});

describe("a unit row's link", () => {
  beforeEach(() => resetStore(NOW));

  it("names its provider", () => {
    useCatalogs(fixture("catalog.bundled.json"));
    applyPlugins([{ id: "github", name: "GitHub", enabled: true }, { id: "slack", name: "Slack", enabled: true }]);
    const s = { ...slack(), link: "https://app.slack.com/client/T0001/C0001" };
    const pr = { ...tagRow(0), link: "https://github.com/acme/api/pull/8", views: ["mine"] };
    state.rows = [s, pr];
    const link = (row: Element) => row.querySelector("a.ext")!;
    expect(link(rowOf(s, "slack")).textContent).toBe("Slack");
    expect(link(rowOf(pr, "mine")).textContent).toBe("GitHub");
  });

  it("filters to its row from every who chip", () => {
    const cat = fixture("catalog.github.json");
    cat.facets.push({ id: "waits-on", label: "Waits on (person)", provider: "slack", group: true, match: "team", menu: true, show: "who" });
    useCatalogs(cat);
    const row = {
      unit: { provider: "slack", key: SLACK_KEY }, label: "#general", title: "Deploy plan", kind: "waits_on_others", views: ["slack", "all"],
      summary: { waits_on: "@team-a, Ana, channel participants" }, facets: { conversation: ["#general"], "waits-on": ["@team-a", "Ana", "channel participants"] },
      payload: { waits_on: [{ key: "group:S0001", name: "@Team-A" }, { key: "slack:U0002", name: "Ana" }, { key: "group:channel participants", name: "channel participants" }] },
    };
    const other = { unit: { provider: "slack", key: "T0001/C0002/1700000000.000200" }, label: "#random", kind: "waits_on_others", views: ["slack", "all"], summary: { waits_on: "Bo" }, facets: { "waits-on": ["Bo"] } };
    const chips = Array.from(rowOf(row, "all").querySelectorAll(".who button.facet"));
    expect(chips.map((b) => b.getAttribute("aria-label"))).toEqual(["Filter by waits on (group) @team-a", "Filter by waits on (person) Ana", "Filter by waits on (group) channel participants"]);
    for (const b of chips) {
      const q = toggledQualifier("", "waits-on", b.textContent || "", false);
      const lens = { ...lensFor("all"), q, ts: terms(q), quals: qualifiers(q), filters: filtersOf(qualifiers(q)), group: "", sort: "", layout: "", done: false };
      expect(pipeline([row, other], lens).groups.flatMap((g) => g.rows.map((r) => r.label))).toEqual(["#general"]);
    }
  });

  it("titles the unit column by the view's unit", () => {
    useCatalogs();
    catalog.views = [{ id: "needs-me", provider: "github", rows: true, unit: "Pull request" }, { id: "all", provider: "github", rows: true }, { id: "slack", provider: "slack", rows: true, unit: "Conversation" }];
    expect(["needs-me", "slack", "all"].map((v) => unitTitle(v, { words: { col_unit: "Item" } }))).toEqual(["Pull request", "Conversation", "Item"]);
  });

  it("renders a fake provider's row without a pull request's fields", () => {
    useCatalogs();
    const r = fixture("fake-row.json");
    const lens = { ...lensFor("all"), view: "all" };
    const tr = draw(() => ({ model: rowModel(r, "", lens, ""), lens }));
    const c = draw(() => ({ model: rowModel(r, "", lens, "", "card"), lens }), true);
    for (const s of [tr.textContent || "", c.textContent || ""]) {
      expect(s).toContain("Can someone look at the deploy?");
      expect(s).toContain("#general thread");
      expect(s).not.toContain("undefined");
    }
  });
});
