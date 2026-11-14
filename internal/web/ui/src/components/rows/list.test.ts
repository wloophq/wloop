import { cleanup, fireEvent, render } from "@testing-library/svelte";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { flushSync } from "svelte";
import { fieldOf, lensOf, multiCopy, pipeline, type Lens } from "../../lib/lens";
import type { J } from "../../lib/util";
import { applyPlugins } from "../../lib/catalog";
import { resetStore } from "../../testing/reset.svelte";
import { readHash, nav } from "../../state/route.svelte";
import { catalog, collapsed, state } from "../../state/store.svelte";
import { openSlot, reveal, revealOpenRow, rowModel } from "./model";
import TestList from "./testdata/TestList.svelte";
import UnitList from "./UnitList.svelte";
import { fixture, useCatalogs } from "./testdata/fixtures";

const NOW = Date.parse("2026-10-12T12:00:00Z");

afterEach(cleanup);

const lensFor = (view: string, extra: Partial<Lens> = {}): Lens => ({ ...lensOf({ ...readHash(), view }), ...extra });

function mineRows(): J[] {
  return fixture("tag-rows.json").map((r: J) => ({ ...r, views: ["mine"] }));
}

describe("a list of rows", () => {
  beforeEach(() => {
    resetStore(NOW, "#view=mine");
    useCatalogs();
    state.rows = mineRows();
    state.loaded = true;
  });

  it("keeps each row's node, its group and its collapse across a sync", async () => {
    const { container } = render(UnitList);
    const row = container.querySelector('[data-row="row:github:acme/web#2"]')!;
    const title = row.querySelector(".t")!;
    expect(title.textContent).toBe("t2");
    state.rows[1].title = "acme/web#2: Fix the export";
    state.rev++;
    flushSync();
    expect(container.querySelector('[data-row="row:github:acme/web#2"]')).toBe(row);
    expect(row.querySelector(".t")).toBe(title);
    expect(title.textContent).toBe("Fix the export");

    nav.hash = "#view=mine&g=size";
    flushSync();
    const toggle = container.querySelector('[data-focus^="group:size:l"]') as HTMLButtonElement;
    await fireEvent.click(toggle);
    expect(toggle.getAttribute("aria-expanded")).toBe("false");
    expect(toggle.textContent!.startsWith("▸")).toBe(true);
    const shut = container.querySelector('[data-row="row:github:acme/web#2"]') as HTMLElement;
    expect(shut.hidden).toBe(true);
    state.rows[1].summary = { ...state.rows[1].summary, reason: "checks fail" };
    state.rows = [...state.rows];
    state.rev++;
    flushSync();
    expect(container.querySelector('[data-focus^="group:size:l"]')).toBe(toggle);
    expect(toggle.getAttribute("aria-expanded")).toBe("false");
    expect((container.querySelector('[data-row="row:github:acme/web#2"]') as HTMLElement).hidden).toBe(true);
    expect(collapsed.has("size:l")).toBe(true);

    nav.hash = "#view=mine&g=risk";
    flushSync();
    expect(collapsed.size).toBe(0);
  });

  it("counts what it shows of its view, as the view names its unit", () => {
    const { container } = render(UnitList);
    expect(container.querySelector("#list-sum")!.textContent).toMatch(/^Pull request: 5 of 5$/);
  });

  it("heads each group with its count, a toggle of its section and its only-this filter", () => {
    nav.hash = "#view=mine&g=size";
    const { container } = render(UnitList);
    const groups = Array.from(container.querySelectorAll("#table .rows > section.group"));
    expect(groups.length).toBeGreaterThan(1);
    for (const s of groups) {
      const toggle = s.querySelector(".g-head .g-toggle")!;
      expect(toggle.getAttribute("aria-controls")).toBe(s.id);
      expect(s.querySelector(".g-head .count")!.textContent).toBe(String(s.querySelectorAll(".row").length));
      expect(s.querySelector(".g-head .facet.only")).not.toBeNull();
    }
  });

  it("draws a board that keeps its scroll across a sync, and hides it when empty", () => {
    nav.hash = "#view=mine&layout=board";
    const { container } = render(UnitList);
    const board = container.querySelector("#board") as HTMLElement;
    expect(board.hidden).toBe(false);
    expect((container.querySelector("#table") as HTMLElement).hidden).toBe(true);
    expect(board.querySelectorAll(".lane").length).toBeGreaterThan(0);
    board.scrollLeft = 120;
    board.scrollTop = 40;
    const card = board.querySelector('[data-row="card:github:acme/web#2"]')!;
    state.rows[1].title = "acme/web#2: Fix the export";
    state.rev++;
    flushSync();
    expect(container.querySelector("#board")).toBe(board);
    expect([board.scrollLeft, board.scrollTop]).toEqual([120, 40]);
    expect(board.querySelector('[data-row="card:github:acme/web#2"]')).toBe(card);
    nav.hash = "#view=mine&layout=board&q=zzz";
    flushSync();
    expect(board.hidden).toBe(true);
  });

  it("lays each lane under its head with its count and its cards", () => {
    nav.hash = "#view=mine&layout=board&g=size";
    const { container } = render(UnitList);
    for (const lane of container.querySelectorAll("#board .lane")) {
      expect(lane.querySelector(".lane-head .count")!.textContent).toBe(String(lane.querySelectorAll("li.card").length));
    }
  });

  it("says what an empty list lacks", async () => {
    nav.hash = "#view=mine&q=zzz";
    const { container } = render(UnitList);
    const empty = container.querySelector("#empty") as HTMLElement;
    expect(empty.hidden).toBe(false);
    expect(empty.textContent).toBe('Nothing in My PRs matches "zzz".');
    nav.hash = "#view=mine&q=" + encodeURIComponent("waits-on:bob zzz");
    flushSync();
    expect(empty.textContent).toBe('Nothing in My PRs matches "waits-on:bob zzz". Clear filters');
    applyPlugins([{ id: "github", name: "GitHub", enabled: true }]);
    nav.hash = "#view=mine&q=" + encodeURIComponent("https://github.com/acme/web/pull/3");
    flushSync();
    expect(empty.textContent).toBe("acme/web#3 is not in the inventory. Explore it Open it on GitHub");
    expect(empty.querySelector("a")!.getAttribute("href")).toBe("https://github.com/acme/web/pull/3");
    state.rows = [];
    nav.hash = "#view=mine";
    flushSync();
    expect(empty.textContent).toBe("Nothing in My PRs right now.");
    state.loaded = false;
    flushSync();
    expect(empty.textContent).toBe("Loading the inventory.");
  });

  it("hides on a view that lists no rows of its own", () => {
    nav.hash = "#view=plan";
    const { container } = render(UnitList);
    expect((container.querySelector("#list-box") as HTMLElement).hidden).toBe(true);
    expect((container.querySelector("#table") as HTMLElement).hidden).toBe(true);
    expect((container.querySelector("#board") as HTMLElement).hidden).toBe(true);
  });
});

describe("a row in several groups", () => {
  beforeEach(() => {
    resetStore(NOW, "#view=mine");
    useCatalogs();
  });

  it("gives each copy its own focus slots and remembers the copy last opened", () => {
    const r = { ...fixture("tag-rows.json")[1], views: ["mine"], actions: [{ id: "open", enabled: true }, { id: "hold", enabled: true }, { id: "mute", enabled: true }] };
    r.payload.workspace = { id: "w1", status: "reviewing" };
    state.rows = [r];
    state.specs = { version: "v", byId: Object.fromEntries(["open", "hold", "mute"].map((id) => [id, { id, label: id, group: "control", surfaces: ["row"] }])) };
    const lens = lensFor("mine", { group: "tag:lang" });
    const slotsOf = (copy: string, kind = "row") => {
      const { container } = render(TestList, { make: () => ({ res: { group: "", groups: [{ field: "", key: "", raw: "", label: "", rows: [r] }], total: 1, shown: 1 }, lens, model: () => rowModel(r, "", lens, copy, kind) }) });
      return Array.from(container.querySelectorAll("[data-focus]")).map((n) => n.getAttribute("data-focus")!);
    };
    expect([multiCopy("tag:lang"), multiCopy("where")]).toEqual([true, false]);
    const copies = ["lang:go", "lang:sql"].map((c) => slotsOf(c));
    expect(copies[0].length).toBeGreaterThanOrEqual(5);
    copies.forEach((slots, i) => {
      for (const s of slots) {
        expect(s).toContain("row:github:acme/web#2@" + ["lang:go", "lang:sql"][i]);
        expect(copies[1 - i]).not.toContain(s);
      }
    });
    const plain = slotsOf("");
    expect(plain).toEqual(expect.arrayContaining(["row:github:acme/web#2", "row:github:acme/web#2:open"]));
    state.openSlot = copies[1][0];
    expect([openSlot("github:acme/web#2"), openSlot("github:acme/web#20"), openSlot("github:acme/api#2")]).toEqual(["row:github:acme/web#2@lang:sql", "", ""]);
  });

  it("is drawn once per group it sits in, each copy keyed apart", () => {
    const rows = mineRows();
    state.rows = rows;
    state.loaded = true;
    nav.hash = "#view=mine&g=tag:lang";
    const { container } = render(UnitList);
    const slots = Array.from(container.querySelectorAll("#table [data-row]")).map((n) => n.getAttribute("data-row"));
    expect(slots).toEqual(expect.arrayContaining(["row:github:acme/web#2@lang:go", "row:github:acme/web#2@lang:sql"]));
    expect(new Set(slots).size).toBe(slots.length);
  });
});

describe("the open row", () => {
  beforeEach(() => resetStore(NOW));

  it("scrolls into view once each time it opens", () => {
    reveal.pr = "";
    let scrolls = 0;
    const blocks: string[] = [];
    let row: J = null;
    const root = { querySelector: () => row } as unknown as ParentNode;
    revealOpenRow("o/r/1", root);
    expect(scrolls).toBe(0);
    row = { scrollIntoView: (o: ScrollIntoViewOptions) => { scrolls++; blocks.push(String(o.block)); } };
    revealOpenRow("o/r/1", root);
    revealOpenRow("o/r/1", root);
    expect(scrolls).toBe(1);
    revealOpenRow("", root);
    revealOpenRow("o/r/1", root);
    expect(scrolls).toBe(2);
    expect(blocks).toEqual(["nearest", "nearest"]);
  });

  it("asks for the copy last opened", () => {
    reveal.pr = "";
    state.openSlot = "row:github:acme/web#2@lang:sql";
    const asked: string[] = [];
    const root = { querySelector: (sel: string) => { asked.push(sel); return { scrollIntoView() {} }; } } as unknown as ParentNode;
    revealOpenRow("github:acme/web#2", root);
    expect(asked).toEqual([`[data-focus="${CSS.escape("row:github:acme/web#2@lang:sql")}"]`]);
  });
});

describe("tags", () => {
  beforeEach(() => {
    resetStore(NOW);
    useCatalogs();
  });

  it("group a value named none apart from the rows with no value", () => {
    const rows = fixture("lens-rows.json").slice(0, 3).map((r: J) => ({ ...r }));
    rows[0].facets = { ...rows[0].facets, tag: ["lang:none"] };
    rows[1].facets = { ...rows[1].facets, tag: ["lang:go"] };
    rows[2].facets = { ...rows[2].facets, tag: [] };
    const lens = lensFor("needs-me", { view: "needs-me", group: "tag:lang", sort: "", layout: "" });
    catalog.views.push({ id: "needs-me", label: "Needs me", provider: "github", rows: true, surfaces: ["web"] });
    const res = pipeline(rows, lens);
    const { container } = render(TestList, { make: () => ({ res, lens, model: (r: J, copy: string) => rowModel(r, "", lens, copy) }) });
    const heads = Array.from(container.querySelectorAll(".g-toggle")).map((b) => b.textContent);
    expect(heads).toEqual(["▾ lang:go", "▾ lang:none", "▾ no lang"]);
    const only = Array.from(container.querySelectorAll(".facet.only")).map((b) => b.getAttribute("aria-label"));
    expect(only).toEqual(["Only lang:go", "Only lang:none", "Only no lang"]);
  });

  it("keeps fieldOf's own grouping for a tag key", () => {
    expect(fieldOf({ id: "tag", label: "Tag", match: "prefix" }).groupBy!("lang:go")).toBe("tag:lang");
  });
});
