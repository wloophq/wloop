import { cleanup, fireEvent, render } from "@testing-library/svelte";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { createRawSnippet, flushSync } from "svelte";
import { I18N } from "../../lib/i18n.svelte";
import { groupedRows, lensOf, viewQueryKey, type Lens, type Pipeline } from "../../lib/lens";
import { lensResult } from "../../lib/view";
import type { J } from "../../lib/util";
import { net } from "../../state/api";
import { nav, readHash } from "../../state/route.svelte";
import { outboxState, tasksState } from "../../state/store.svelte";
import { filterPalette, lensItems, listYAML } from "../chrome/toolbar";
import { resetStore } from "../../testing/reset.svelte";
import { fixture, useCatalogs } from "../rows/testdata/fixtures";
import DraftList from "./DraftList.svelte";
import TaskViewPage from "./TaskViewPage.svelte";

const NOW = Date.parse("2026-11-04T12:00:00Z");

afterEach(cleanup);

// outboxView opens the Outbox at hash, the views catalog drawing it in the outbox layout, its page served.
function outboxView(hash: string): void {
  resetStore(NOW, hash);
  const views = fixture("views.json");
  views.views = views.views.map((v: J) => (v.id === "outbox" ? { ...v, layout: "outbox" } : v));
  useCatalogs(fixture("catalog.github.json"), views);
  I18N.strings = { "{n} draft": { one: "{n} draft", other: "{n} drafts" } };
  tasksState.key = viewQueryKey("outbox", "", []);
  tasksState.tasks = [];
  outboxState.page = fixture("outbox-page.json");
}

const body = (c: Element) => c.querySelector("#tasks-body")!;
const heads = (root: Element) => Array.from(root.querySelectorAll(".ob-group")).map((g) => g.querySelector(".ob-title")!.textContent + " " + g.querySelector(".ob-n")!.textContent);
const cards = (root: Element) => Array.from(root.querySelectorAll("[data-draft]")).map((n) => Number(n.getAttribute("data-draft")));

describe("the Outbox through the lens", () => {
  beforeEach(() => outboxView("#view=outbox"));

  it("groups by who proposed each draft, each head in the served words", () => {
    const c = render(TaskViewPage).container;
    expect(heads(body(c))).toEqual(["Reviews 1 draft", "Written by hand 1 draft", "Ask who hasn't answered 2 drafts"]);
    expect(cards(body(c))).toEqual([10, 9, 7, 8]);
    expect(c.querySelector("#tasks-body > .outbox")).not.toBeNull();
  });

  it("regroups by kind, a value without a served head under the facet's word or its own", () => {
    nav.hash = "#view=outbox&g=kind";
    const c = render(TaskViewPage).container;
    expect(heads(body(c))).toEqual(["comment 1 draft", "Nudge 2 drafts", "Review 1 draft"]);
    nav.hash = "#view=outbox&g=source";
    flushSync();
    expect(heads(body(c))).toEqual(["GitHub 4 drafts"]);
  });

  it("filters by where and searches the drafts' texts", () => {
    nav.hash = "#view=outbox&q=" + encodeURIComponent("where:acme/api");
    const c = render(TaskViewPage).container;
    expect(cards(body(c))).toEqual([9, 7]);
    nav.hash = "#view=outbox&q=upload";
    flushSync();
    expect(cards(body(c))).toEqual([8]);
    nav.hash = "#view=outbox&q=acme/web";
    flushSync();
    expect(cards(body(c))).toEqual([10, 8]);
  });

  it("groups and filters by the names of the people the drafts mention", () => {
    nav.hash = "#view=outbox&q=" + encodeURIComponent("people:alice");
    const c = render(TaskViewPage).container;
    expect(cards(body(c))).toEqual([9]);
    nav.hash = "#view=outbox&g=people";
    flushSync();
    expect(heads(body(c))).toContain("alice 1 draft");
  });

  it("says nothing matches a qualifier no facet reads", () => {
    nav.hash = "#view=outbox&q=" + encodeURIComponent("color:blue");
    const c = render(TaskViewPage).container;
    expect(cards(body(c))).toEqual([]);
    expect(body(c).textContent).toContain('Nothing here matches "color:blue"');
    expect(body(c).querySelector(".plan-error")).toBeNull();
  });

  it("keeps the lens when a draft leaves the Outbox", () => {
    const hash = "#view=outbox&q=" + encodeURIComponent("where:acme/api") + "&g=kind&s=age-desc";
    nav.hash = hash;
    const c = render(TaskViewPage).container;
    expect(cards(body(c))).toEqual([9, 7]);
    outboxState.page.drafts = outboxState.page.drafts.filter((d: J) => d.id !== 9);
    flushSync();
    expect(cards(body(c))).toEqual([7]);
    expect(nav.hash).toBe(hash);
    const r = readHash();
    expect([r.q, r.g, r.s]).toEqual(["where:acme/api", "kind", "age-desc"]);
  });

  it("shows the lens bar over the drafts' facets", () => {
    const tools = createRawSnippet((lens: () => Lens, res: () => Pipeline | null) => ({
      render: () => `<i id="seen" data-shown="${res() ? res()!.shown : ""}" data-group="${lens().defaultGroup}" data-offered="${lens().offered.map((f) => f.id).join(",")}"></i>`,
    }));
    const seen = render(TaskViewPage, { tools }).container.querySelector("#seen")!;
    expect(seen.getAttribute("data-shown")).toBe("4");
    expect(seen.getAttribute("data-group")).toBe("proposer");
    expect(seen.getAttribute("data-offered")).toBe("proposer,workflow,kind,source,where,people,state,age");
  });
});

describe("filtering the Outbox", () => {
  beforeEach(() => outboxView("#view=outbox"));

  it("words the values of who proposed the drafts as their group heads do", () => {
    const lens = lensOf(readHash());
    const items = lensItems(lens);
    const palettes: J[] = [];
    filterPalette(lens, items, (p) => palettes.push(p));
    palettes[0].items.find((it: J) => it.label === "Proposed by").run();
    expect(palettes[1].items.map((it: J) => it.label).sort()).toEqual(["Ask who hasn't answered", "Reviews", "Written by hand"]);
  });

  it("words the drafts' kinds by the plugin that posts them", () => {
    const lens = lensOf(readHash());
    const palettes: J[] = [];
    filterPalette(lens, lensItems(lens), (p) => palettes.push(p));
    palettes[0].items.find((it: J) => it.label === "Kind").run();
    expect(palettes[1].items.map((it: J) => it.label).sort()).toEqual(["Nudge", "Review", "comment"]);
  });
});

describe("Copy list on the Outbox", () => {
  beforeEach(() => outboxView("#view=outbox&q=upload"));

  it("names each draft by its id, its text and its unit", () => {
    const lens = lensOf(readHash());
    expect(listYAML({ view: "outbox", filter: lens.q, at: "2026-11-04T12:00:00.000Z" }, groupedRows(lensResult(lens).groups))).toBe(
      'view: "outbox"\nfilter: "upload"\ncopied_at: "2026-11-04T12:00:00.000Z"\ncount: 1\nitems:\n  - ref: "draft:8"\n    url: "https://example.test/thread"\n' +
        '    title: "Still waiting on the upload fix?"\n    units: ["acme/web#5"]\n    age: ["2026-11-04T10:15:00Z"]\n    kind: ["nudge"]\n    proposer: ["workflow:fake-nudge"]\n' +
        '    source: ["github"]\n    state: ["held"]\n    where: ["acme/web"]\n    workflow: ["fake-nudge"]\n',
    );
    expect(lensItems(lens).length).toBe(4);
  });
});

describe("a workflow's head", () => {
  beforeEach(() => outboxView("#view=outbox"));

  it("says why its drafts were written and stops the workflow once confirmed", async () => {
    const posts: J[] = [];
    net.fetch = async (url: string, init?: RequestInit) => {
      posts.push({ path: url, body: JSON.parse(String(init && init.body)) });
      return new Response("{}", { status: 200 });
    };
    const c = render(DraftList, { page: outboxState.page, lens: lensOf(readHash()) }).container;
    const marks = ["ob-title", "ob-n", "ob-about", "ob-workflow", "d-draft"];
    const order = Array.from(c.querySelectorAll("*")).filter((x) => marks.some((m) => x.classList.contains(m)))
      .map((x) => (x.getAttribute("data-draft") ? "card " + x.getAttribute("data-draft") : x.textContent));
    expect(order).toEqual(["Reviews", "1 draft", "Drafted by a review round on someone else's pull request.", "card 10", "Written by hand", "1 draft", "card 9",
      "Ask who hasn't answered", "2 drafts", "fake-nudge", "Drafted when a conversation where you asked something goes quiet.", "card 7", "card 8"]);
    const buttons = () => Array.from(c.querySelectorAll(".ob-stop button")).map((b) => b.getAttribute("data-focus"));
    expect(buttons()).toEqual(["outbox:stop:fake-nudge"]);
    await fireEvent.click(c.querySelector('[data-focus="outbox:stop:fake-nudge"]')!);
    expect(buttons()).toEqual(["outbox:stop-confirm:fake-nudge", "outbox:stop-cancel:fake-nudge"]);
    await fireEvent.click(c.querySelector('[data-focus="outbox:stop-cancel:fake-nudge"]')!);
    expect(buttons()).toEqual(["outbox:stop:fake-nudge"]);
    expect(posts).toEqual([]);
    await fireEvent.click(c.querySelector('[data-focus="outbox:stop:fake-nudge"]')!);
    await fireEvent.click(c.querySelector('[data-focus="outbox:stop-confirm:fake-nudge"]')!);
    await new Promise((r) => setTimeout(r, 0));
    expect(posts).toEqual([{ path: "/api/workflows/fake-nudge/disable", body: { source: "cockpit" } }]);
    expect(outboxState.confirming).toBe("");
  });

  it("offers the stop on the workflow grouping too", () => {
    nav.hash = "#view=outbox&g=workflow";
    const c = render(DraftList, { page: outboxState.page, lens: lensOf(readHash()) }).container;
    expect(heads(c)).toEqual(["Ask who hasn't answered 2 drafts", "no workflow 2 drafts"]);
    expect(Array.from(c.querySelectorAll(".ob-stop button")).map((b) => b.getAttribute("data-focus"))).toEqual(["outbox:stop:fake-nudge"]);
  });

  it("keeps each card's node across a reload", () => {
    const c = render(DraftList, { page: outboxState.page, lens: lensOf(readHash()) }).container;
    const card = c.querySelector('[data-draft="8"]');
    const next = fixture("outbox-page.json");
    next.drafts[1].text = "Still waiting?";
    outboxState.page.drafts = next.drafts;
    flushSync();
    expect(card).not.toBeNull();
    expect(c.querySelector('[data-draft="8"]')).toBe(card);
  });
});
