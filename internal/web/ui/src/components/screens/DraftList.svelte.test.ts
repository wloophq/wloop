import { cleanup, fireEvent, render } from "@testing-library/svelte";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { flushSync } from "svelte";
import { I18N } from "../../lib/i18n.svelte";
import { groupedRows, lensFields, lensOf, viewQueryKey } from "../../lib/lens";
import { loadOutbox } from "../../state/loaders.svelte";
import { lensResult } from "../../lib/view";
import type { J } from "../../lib/util";
import { net } from "../../state/api";
import { nav, readHash } from "../../state/route.svelte";
import { outboxState, tasksState } from "../../state/store.svelte";
import { filterPalette, lensChips, lensItems, listYAML } from "../chrome/toolbar";
import { resetStore } from "../../testing/reset.svelte";
import { recordPosts, restoreFetch } from "../../testing/gates";
import { fixture, useCatalogs } from "../rows/testdata/fixtures";
import LensBar from "../chrome/LensBar.svelte";
import DraftList from "./DraftList.svelte";

const NOW = Date.parse("2026-11-04T12:00:00Z");

afterEach(cleanup);

// outboxView opens the Outbox at hash, its page served.
function outboxView(hash: string): void {
  resetStore(NOW, hash);
  useCatalogs(fixture("catalog.github.json"), fixture("views.json"));
  I18N.strings = { "{n} draft": { one: "{n} draft", other: "{n} drafts" } };
  tasksState.key = viewQueryKey("outbox", "", []);
  tasksState.tasks = [];
  outboxState.page = fixture("outbox-page.json");
}

const heads = (root: Element) => Array.from(root.querySelectorAll(".ob-group")).map((g) => g.querySelector(".ob-title")!.textContent + " " + g.querySelector(".ob-n")!.textContent);
const cards = (root: Element) => Array.from(root.querySelectorAll("[data-draft]")).map((n) => Number(n.getAttribute("data-draft")));

describe("the Outbox through the lens", () => {
  beforeEach(() => outboxView("#view=outbox"));

  // drafts draws the Outbox's drafts through the lens of hash, as the review's Outbox step does.
  const drafts = (hash: string) => {
    cleanup();
    nav.hash = hash;
    return render(DraftList, { page: outboxState.page, lens: lensOf(readHash()) }).container;
  };

  it("groups by who proposed each draft, each head in the served words", () => {
    const c = drafts("#view=outbox");
    expect(heads(c)).toEqual(["Reviews 1 draft", "Written by hand 1 draft", "Ask who hasn't answered 2 drafts"]);
    expect(cards(c)).toEqual([10, 9, 7, 8]);
  });

  it("regroups by kind, a value without a served head under the facet's word or its own", () => {
    expect(heads(drafts("#view=outbox&g=kind"))).toEqual(["comment 1 draft", "Nudge 2 drafts", "Review 1 draft"]);
    expect(heads(drafts("#view=outbox&g=source"))).toEqual(["GitHub 4 drafts"]);
  });

  it("filters by where and searches the drafts' texts", () => {
    expect(cards(drafts("#view=outbox&q=" + encodeURIComponent("where:acme/api")))).toEqual([9, 7]);
    expect(cards(drafts("#view=outbox&q=upload"))).toEqual([8]);
    expect(cards(drafts("#view=outbox&q=acme/web"))).toEqual([10, 8]);
  });

  it("groups and filters by the names of the people the drafts mention", () => {
    expect(cards(drafts("#view=outbox&q=" + encodeURIComponent("people:alice")))).toEqual([9]);
    expect(heads(drafts("#view=outbox&g=people"))).toContain("alice 1 draft");
  });

  it("keeps the lens when a draft leaves the Outbox", () => {
    const hash = "#view=outbox&q=" + encodeURIComponent("where:acme/api") + "&g=kind&s=age-desc";
    const c = drafts(hash);
    expect(cards(c)).toEqual([9, 7]);
    outboxState.page.drafts = outboxState.page.drafts.filter((d: J) => d.id !== 9);
    flushSync();
    expect(cards(c)).toEqual([7]);
    expect(nav.hash).toBe(hash);
    const r = readHash();
    expect([r.q, r.g, r.s]).toEqual(["where:acme/api", "kind", "age-desc"]);
  });

  it("offers no list/board switch, the Outbox drawing only its list", () => {
    const bar = (hash: string) => {
      nav.hash = hash;
      return render(LensBar, { lens: lensOf(readHash()), res: null }).container;
    };
    expect(bar("#view=outbox").querySelector('[data-focus="lens:board"]')).toBeNull();
    cleanup();
    expect(bar("#view=needs-me").querySelector('[data-focus="lens:board"]')).not.toBeNull();
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

  it("words a chip's value as the palette does", () => {
    nav.hash = "#view=outbox&q=" + encodeURIComponent("proposer:intent:review kind:nudge");
    expect(lensChips(lensOf(readHash())).map((c) => c.text)).toEqual(["Proposed by: Reviews ×", "Kind: Nudge ×"]);
  });

  it("words the drafts' kinds by the plugin that posts them", () => {
    const lens = lensOf(readHash());
    const palettes: J[] = [];
    filterPalette(lens, lensItems(lens), (p) => palettes.push(p));
    palettes[0].items.find((it: J) => it.label === "Kind").run();
    expect(palettes[1].items.map((it: J) => it.label).sort()).toEqual(["Nudge", "Review", "comment"]);
  });
});

describe("reloading the Outbox", () => {
  beforeEach(() => outboxView("#view=outbox"));

  it("reads the words of the facets the reload serves", async () => {
    const where = () => lensFields("outbox").find((f) => f.id === "where")!;
    expect(where().show).toBeUndefined();
    const next = fixture("outbox-page.json");
    next.facets.find((f: J) => f.id === "where").labels = { "acme/api": "The API" };
    net.fetch = async () => new Response(JSON.stringify(next), { status: 200 });
    await loadOutbox("outbox");
    expect(where().show!("acme/api")).toBe("The API");
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

describe("a question's drafts in the Outbox", () => {
  beforeEach(() => outboxView("#view=outbox"));
  afterEach(restoreFetch);

  // pinnedPage is the Outbox page whose drafts 7 and 8 an open question pins in its options, with its gate.
  function pinnedPage(): J {
    const page = fixture("outbox-page.json");
    const pinned = page.drafts.filter((d: J) => d.id === 7 || d.id === 8).map((d: J) => ({ ...d, pinned_by: 5 }));
    page.drafts = page.drafts.map((d: J) => pinned.find((p: J) => p.id === d.id) || d);
    page.questions = [{
      id: 55, task: "t-1", kind: "question", name: "question", status: "open", intents: [{ id: "answer", label: "Answer", answer: true }],
      subject: { id: 5, kind: "question", text: "Nudge whom?", recommend: "b", pinned,
        options: [{ id: "a", label: "Nudge Dana", draft: { id: 7, rev: 3 } }, { id: "b", label: "Nudge Eli", draft: { id: 8, rev: 3 } }, { id: "c", label: "Wait" }] },
    }];
    return page;
  }

  it("shows the drafts with the question that pins them, its options side by side, the recommended one marked", () => {
    outboxState.page = pinnedPage();
    const c = render(DraftList, { page: outboxState.page, lens: lensOf(readHash()) }).container;
    const asks = c.querySelectorAll(".ob-question");
    expect(asks).toHaveLength(1);
    const q = asks[0];
    expect(q.querySelector(".bd")!.textContent).toContain("Nudge whom?");
    expect(q.querySelector(".ob-about")!.textContent).toContain("An open question decides these drafts");
    expect(Array.from(q.querySelectorAll(".d-pin[data-draft]")).map((n) => n.getAttribute("data-draft"))).toEqual(["8", "7"]);
    expect(Array.from(q.querySelectorAll(".opt .rec")).map((n) => n.closest(".opt")!.getAttribute("data-pin"))).toEqual(["8@3"]);
    expect(cards(c).filter((id) => id === 7 || id === 8)).toEqual([8, 7]);
    expect(heads(c)).toContain("Ask who hasn't answered 2 drafts");
  });

  it("answers the question through its gate when an option is picked there", async () => {
    outboxState.page = pinnedPage();
    const posts = recordPosts();
    const c = render(DraftList, { page: outboxState.page, lens: lensOf(readHash()) }).container;
    await fireEvent.click(c.querySelector('[data-focus="gate:55:option:b"]')!);
    await fireEvent.click(Array.from(c.querySelectorAll(".ob-question button.act")).find((b) => b.textContent!.includes("Answer"))!);
    await Promise.resolve();
    expect(posts.map((p) => p.path)).toEqual(["/api/gates/55/answer"]);
    expect(posts[0].body.args).toMatchObject({ answer: "b", rev: "3" });
  });

  it("falls back to the draft's own card when the page serves no question for it", () => {
    const page = pinnedPage();
    page.questions = [];
    outboxState.page = page;
    const c = render(DraftList, { page: outboxState.page, lens: lensOf(readHash()) }).container;
    expect(c.querySelectorAll(".ob-question")).toHaveLength(0);
    expect(cards(c)).toEqual([10, 9, 7, 8]);
  });
});
