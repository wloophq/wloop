import { render } from "@testing-library/svelte";
import { beforeEach, describe, expect, it } from "vitest";
import { flushSync } from "svelte";
import { fmtDate } from "../../lib/i18n.svelte";
import { lensOf } from "../../lib/lens";
import type { J } from "../../lib/util";
import { resetStore } from "../../testing/reset.svelte";
import { tasksState } from "../../state/store.svelte";
import { taskModel } from "./model";
import { rowStops } from "./rove";
import TestRow from "./testdata/TestRow.svelte";
import { childClasses, fixture, task, useCatalogs } from "./testdata/fixtures";

const NOW = Date.parse("2026-10-12T12:00:00Z");
const T1 = "2026-10-12T09:30:00Z";
const T2 = "2026-10-11T16:45:00Z";

function rowTask(mod?: (e: J) => void): J {
  const e = structuredClone(fixture("row-task.json").task);
  if (mod) mod(e);
  return e;
}

function setup() {
  resetStore(NOW);
  useCatalogs();
  const views = fixture("views.json");
  tasksState.catalog = { ...views, states: [{ name: "next", label: "Next", category: "ready" }], facets: [...views.facets, { id: "ask", label: "Ask", group: true, show: "kind" }] };
}

const inbox = () => lensOf({ view: "inbox", q: "", g: "", s: "", layout: "", area: "", ctx: [], tk: "", done: false });

function rowOf(e: J, card = false, open = "") {
  const { container } = render(TestRow, { make: () => ({ model: taskModel(e, open, "", inbox()), lens: inbox() }), card });
  return container.querySelector(card ? ".card" : ".row") as HTMLElement;
}

const slot = (row: Element, name: string) => row.querySelector("." + name);
const metaTexts = (row: Element) => Array.from(slot(row, "pieces")!.children).map((c) => (c.textContent || "").trim());

describe("a task's row", () => {
  beforeEach(setup);

  it("has two lines in their order", () => {
    const row = rowOf(rowTask());
    expect(childClasses(row)).toEqual(["glyph", "l1", "meta", "l2", "r2"]);
    expect(childClasses(slot(row, "l1"))).toEqual(["t", "ch"]);
    expect(metaTexts(row)[0]).toBe("Review");
    expect(metaTexts(row)).toContain("risk high");
    expect(childClasses(slot(row, "l2"))).toEqual(["ref", "why"]);
    expect(childClasses(slot(row, "when"))).toEqual(["dot", "who", "age"]);
    expect(slot(row, "t")!.textContent).toBe("Retry flaky uploads");
  });

  it("draws its state as a glyph that filters by it", () => {
    const row = rowOf(rowTask());
    const g = slot(row, "glyph")!;
    expect([g.getAttribute("data-category"), g.getAttribute("title"), g.getAttribute("data-field"), g.getAttribute("data-value")]).toEqual(["ready", "Next", "state", "next"]);
    expect(row.children[0].matches("button.facet")).toBe(true);
    expect(Array.from(row.querySelectorAll(".meta .verb")).map((x) => x.textContent)).not.toContain("Next");
  });

  it("dates by the time rule", () => {
    const mine = slot(rowOf(rowTask()), "age")!.textContent;
    const theirs = slot(rowOf(rowTask((e) => (e.facets.turn = ["others"]))), "age")!.textContent;
    expect(mine).toBe("since " + fmtDate(T1, "datetime"));
    expect(theirs).toBe("moved " + fmtDate(T2, "datetime"));
  });

  it("shows two tags and the rest, the rest named in its tooltip", () => {
    tasksState.catalog = { ...tasksState.catalog, facets: tasksState.catalog.facets.map((f: J) => (f.id === "tag" ? { ...f, labels: { c: "Cê" } } : f)) };
    const row = rowOf(rowTask());
    expect(metaTexts(row).slice(-3)).toEqual(["a", "b", "+1"]);
    expect(row.querySelector("[data-more]")!.getAttribute("title")).toBe("Cê");
  });

  it("marks pending drafts", () => {
    const marks = rowOf(rowTask((e) => (e.drafts = [7]))).querySelectorAll(".draft-mark");
    expect(marks.length).toBe(1);
    expect(marks[0].getAttribute("title")).toBe("1 draft waits for you");
    expect(rowOf(rowTask()).querySelectorAll(".draft-mark").length).toBe(0);
  });

  it("lays its board card on the same slots", () => {
    const row = rowOf(rowTask());
    const card = rowOf(rowTask(), true);
    expect(card.tagName).toBe("LI");
    expect(card.classList.contains("card")).toBe(true);
    expect(childClasses(slot(card, "l1"))).toEqual(expect.arrayContaining(["open", "ch"]));
    expect(slot(card, "t")!.textContent).toBe(slot(row, "t")!.textContent);
    expect(childClasses(slot(card, "when"))).toEqual(childClasses(slot(row, "when")));
    expect(metaTexts(card)).toEqual(metaTexts(row));
  });

  it("keeps who and its date on a long row", () => {
    const row = rowOf(rowTask((e) => {
      e.title = "Retry the flaky uploads ".repeat(9).slice(0, 200);
      e.reason = "the upload test fails when the bucket is slow ".repeat(10);
      e.facets.tag = Array.from({ length: 20 }, (_, i) => "t" + String.fromCharCode(97 + i));
    }));
    const meta = metaTexts(row);
    expect(meta.filter((m) => m.length === 2 && m[0] === "t").length).toBe(2);
    expect(meta[meta.length - 1]).toBe("+18");
    expect(childClasses(slot(row, "when"))).toEqual(expect.arrayContaining(["who", "age"]));
    expect(slot(row, "age")!.textContent).not.toBe("");
  });

  it("marks the open task as current", () => {
    const open = rowOf(rowTask(), false, "e-1");
    const other = rowOf(rowTask(), false, "e-2");
    expect([open.getAttribute("aria-current"), open.getAttribute("aria-selected")]).toEqual(["true", null]);
    expect([other.getAttribute("aria-current"), other.getAttribute("aria-selected")]).toEqual([null, null]);
  });

  it("keeps its node, and the node of each piece, when its data changes", () => {
    tasksState.tasks = [rowTask()];
    const { container } = render(TestRow, { make: () => ({ model: taskModel(tasksState.tasks[0], "", "", inbox()), lens: inbox() }) });
    const row = container.querySelector(".row")!;
    const title = row.querySelector(".t")!;
    const risk = row.querySelector('[data-field="risk"]')!;
    Object.assign(tasksState.tasks[0], { title: "acme/api#8: Retry the uploads", reason: "checks pass now" });
    flushSync();
    expect(container.querySelector(".row")).toBe(row);
    expect(row.querySelector(".t")).toBe(title);
    expect(row.querySelector('[data-field="risk"]')).toBe(risk);
    expect(title.textContent).toBe("Retry the uploads");
    expect(row.querySelector(".why")!.textContent).toBe("checks pass now");
  });
});

describe("a row's arrows", () => {
  it("skip what it hides", () => {
    const row = document.createElement("div");
    row.setAttribute("data-rove", "");
    row.innerHTML = '<div class="l1"><span data-rove="">title</span></div><div class="meta"><span class="acts"><button data-rove="">Review</button><button data-rove="">Remover</button></span></div>';
    (row.querySelectorAll("button")[1] as HTMLElement).inert = true;
    const names = rowStops(row).map((n) => n.textContent);
    expect(names).toContain("Review");
    expect(names).not.toContain("Remover");
  });
});

