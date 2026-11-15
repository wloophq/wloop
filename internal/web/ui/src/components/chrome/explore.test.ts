import { fireEvent, render } from "@testing-library/svelte";
import { flushSync } from "svelte";
import { beforeEach, describe, expect, test } from "vitest";
import { brand } from "../../brand";
import { exploreSearch } from "../../lib/catalog";
import { I18N } from "../../lib/i18n.svelte";
import { keyOf, type J } from "../../lib/util";
import { clipboard, scheduler } from "../../state/actions.svelte";
import { nav, readHash } from "../../state/route.svelte";
import { drafts, notice, state } from "../../state/store.svelte";
import { answer, clone, fixtures, followHashes, settle, useCatalog, useSpecs } from "../../testing/chrome";
import { resetStore } from "../../testing/reset.svelte";
import Explore from "./Explore.svelte";
import { actOnCandidate, candidatesYAML, candidateWait, exploreEnv, hasTerm, hasWindow, querySort, toggledQuery, withSort, withWindow } from "./explore";
import { listYAML } from "./toolbar";

const now = Date.parse("2026-10-15T12:00:00Z");

beforeEach(() => {
  resetStore(now);
  followHashes();
  scheduler.load = () => {};
});

describe("the Explore query's chips", () => {
  test("toggle a term", () => {
    expect([
      toggledQuery("org:acme", "draft:false"),
      toggledQuery("org:acme  draft:false label:x", "draft:false"),
      toggledQuery("", "-reviewed-by:@me"),
      hasTerm("org:acme draft:false", "draft:false"),
      hasTerm("org:acme draft:falsey", "draft:false"),
    ]).toEqual(["org:acme draft:false", "org:acme label:x", "-reviewed-by:@me", true, false]);
  });

  test("pick one sort", () => {
    useCatalog();
    expect([
      withSort("org:acme", "created-asc"),
      withSort("org:acme  sort:created-asc label:x", "updated-desc"),
      withSort("sort:updated-desc @zoe sort:created-asc", "updated-asc"),
      withSort("", "updated-asc"),
      querySort("org:acme"),
      querySort("org:acme sort:created-asc"),
      querySort("sort:created-asc org:acme sort:updated-desc"),
      querySort("label:resort:x"),
    ]).toEqual(["org:acme sort:created-asc", "org:acme label:x sort:updated-desc", "@zoe sort:updated-asc", "sort:updated-asc", "updated-asc", "created-asc", "updated-desc", "updated-asc"]);
  });

  test("put an activity window days back, or take it out", () => {
    const today = new Date(2026, 9, 15, 23, 30);
    expect([
      withWindow("org:acme", today, 30),
      withWindow("org:acme updated:>=2026-01-01 label:x", today, 30),
      withWindow("org:acme  updated:>=2026-01-01 sort:created-asc", today, 0),
      withWindow("", new Date(2026, 2, 10), 30),
      withWindow("updated:>2026-01-01", today, 30),
      hasWindow("org:acme updated:>=2026-09-15"),
      hasWindow("org:acme updated:>2026-09-15"),
      hasWindow("org:acme"),
    ]).toEqual(["org:acme updated:>=2026-09-15", "org:acme label:x updated:>=2026-09-15", "org:acme sort:created-asc", "updated:>=2026-02-08", "updated:>2026-01-01 updated:>=2026-09-15", true, false, false]);
  });

  test("the presets ask for me and for my teams in one click", async () => {
    useCatalog();
    expect(exploreSearch().presets).toEqual([
      { query: "user-review-requested:@me", label: "Asked of me" },
      { query: "review-requested:@me -user-review-requested:@me", label: "Asked of my teams" },
    ]);
    const calls = answer(() => ({ candidates: [], total: 0, query: "x" }));
    nav.hash = "#view=explore";
    const { container } = render(Explore);
    await fireEvent.click(container.querySelector('[data-focus="explore:preset:review-requested:@me -user-review-requested:@me"]')!);
    await settle();
    expect(calls.map((c) => [c.url, c.body])).toEqual([["/api/plugins/github/views/explore", { query: "review-requested:@me -user-review-requested:@me" }]]);
    expect(readHash().x).toBe("review-requested:@me -user-review-requested:@me");
  });

  test("follow the text: a chip presses as its term enters the query, and a click writes it", async () => {
    useCatalog();
    nav.hash = "#view=explore&x=org%3Aacme";
    const { container } = render(Explore);
    const q = container.querySelector("#explore-q") as HTMLInputElement;
    const ready = container.querySelector('[data-focus="explore:filter:draft:false"]') as HTMLElement;
    const sort = container.querySelector('[data-focus="explore:sort:created-asc"]') as HTMLElement;
    expect(q.value).toBe("org:acme");
    expect(ready.getAttribute("aria-pressed")).toBe("false");
    await fireEvent.click(ready);
    expect(readHash().x).toBe("org:acme draft:false");
    expect(q.value).toBe("org:acme draft:false");
    expect(ready.getAttribute("aria-pressed")).toBe("true");
    await fireEvent.click(sort);
    expect(readHash().x).toBe("org:acme draft:false sort:created-asc");
    expect(sort.getAttribute("aria-pressed")).toBe("true");
    await fireEvent.click(container.querySelector('[data-focus="explore:filter:window"]')!);
    expect(readHash().x).toBe("org:acme draft:false sort:created-asc updated:>=2026-09-15");
  });
});

describe("a candidate", () => {
  test("says how long it waits", () => {
    const ago = (h: number) => new Date(now - h * 3600000).toISOString();
    const w = (text: string, late: boolean) => ({ text, late });
    expect([
      candidateWait({ updated_at: ago(0.5) }, null, 48),
      candidateWait({ updated_at: ago(5) }, null, 48),
      candidateWait({ updated_at: ago(47) }, null, 48),
      candidateWait({ updated_at: ago(49) }, null, 48),
      candidateWait({ updated_at: ago(80) }, null, 72),
      candidateWait({ updated_at: ago(30) }, null, 0),
      candidateWait({ updated_at: ago(80) }, { summary: { since: ago(3), stalled: false } }, 48),
      candidateWait({ updated_at: ago(1) }, { summary: { since: ago(30), stalled: true } }, 48),
      candidateWait({ updated_at: ago(60) }, { summary: { since: "0001-01-01T00:00:00Z" } }, 48),
      candidateWait({ updated_at: "0001-01-01T00:00:00Z" }, null, 48),
    ]).toEqual([w("waiting 30m", false), w("waiting 5h", false), w("waiting 47h", false), w("waiting 2d", true), w("waiting 3d", true),
      w("waiting 30h", false), w("waiting 3h", false), w("waiting 30h", true), w("waiting 2d", true), null]);
  });

  test("uses its served actions on the explore surface, the off ones off", async () => {
    useSpecs(fixtures.specs);
    for (const name of ["explore_candidate_mine", "untracked_open_not_mine"]) {
      const c = clone(fixtures.candidates[name]);
      const ran: string[] = [];
      exploreEnv.act = async (_row: J, id: string) => {
        ran.push(id);
        return true;
      };
      exploreEnv.load = async () => {};
      state.rows = [c];
      state.explore.result = { candidates: [c], total: 1, query: "x" };
      state.explore.sel = keyOf(c);
      const { container, unmount } = render(Explore);
      const buttons = [...container.querySelectorAll("#explore-act-buttons button")].filter((b) => b.getAttribute("data-focus") !== "explore:detail") as HTMLButtonElement[];
      const want = (c.actions as J[]).filter((st) => state.specs.byId[st.id] && state.specs.byId[st.id].surfaces.includes("explore"));
      expect(buttons.map((b) => b.textContent), name).toEqual(want.map((st) => st.label || state.specs.byId[st.id].label));
      expect(buttons.map((b) => b.disabled), name).toEqual(want.map((st) => !st.enabled));
      for (const b of buttons) if (!b.disabled) await fireEvent.click(b);
      await settle();
      expect(ran, name).toEqual(want.filter((st) => st.enabled).map((st) => st.id));
      unmount();
    }
  });

  test("an action brings the pull request in first and says how it went", async () => {
    const calls: string[] = [];
    exploreEnv.act = async (_row: J, name: string) => {
      calls.push(name);
      return true;
    };
    exploreEnv.load = async () => {
      if (calls.includes("track")) state.rows = [{ label: "acme/api#7", unit: { provider: "github", key: "acme/api#7" }, reasons: ["tracked"] }];
    };
    const ok = await actOnCandidate({ label: "acme/api#7", unit: { provider: "github", key: "acme/api#7" } }, "review", {});
    expect(ok).toBe(true);
    expect(calls).toEqual(["track", "review"]);
    expect(state.explore.status["github:acme/api#7"]).toMatchObject({ text: `review started; it is in ${brand.name} now`, done: true });
  });

  test("an action the row refuses takes the track back", async () => {
    const calls: string[] = [];
    exploreEnv.act = async (_row: J, name: string) => {
      calls.push(name);
      return true;
    };
    exploreEnv.load = async () => {
      state.rows = calls.includes("track") && !calls.includes("untrack")
        ? [{ label: "acme/api#7", unit: { provider: "github", key: "acme/api#7" }, actions: [{ id: "nudge", enabled: false, reason: "it waits on the review loop" }] }]
        : [];
    };
    const ok = await actOnCandidate({ label: "acme/api#7", unit: { provider: "github", key: "acme/api#7" } }, "nudge", {});
    expect(ok).toBe(false);
    expect(calls).toEqual(["track", "untrack"]);
    expect(state.explore.status["github:acme/api#7"]).toMatchObject({ text: "nudge acme/api#7: it waits on the review loop; untracked it again", error: true });
  });

  test("its guidance lives in the store and the box stays the same node as results and statuses change", async () => {
    useCatalog();
    useSpecs(fixtures.specs);
    const c = clone(fixtures.candidates.explore_candidate_mine);
    state.explore.result = { candidates: [c], total: 1, query: "x" };
    state.explore.sel = keyOf(c);
    const { container } = render(Explore);
    const box = container.querySelector("#explore-guidance") as HTMLTextAreaElement;
    box.focus();
    box.value = "look at the retry";
    await fireEvent.input(box);
    expect(drafts.guidance[keyOf(c)]).toBe("look at the retry");
    state.explore.status[keyOf(c)] = { text: "review running…" };
    state.explore.result = { candidates: [{ ...c, title: "Renamed" }], total: 1, query: "x" };
    flushSync();
    expect(container.querySelector("#explore-guidance")).toBe(box);
    expect(box.value).toBe("look at the retry");
    expect(document.activeElement).toBe(box);
    expect(container.querySelector(".cand-status")!.textContent!.trim()).toBe("review running…");
  });

  test("the status says what the search spends, and rewords with the strings", () => {
    useCatalog();
    const { container } = render(Explore);
    const status = () => container.querySelector("#explore-status")!.textContent!.trim();
    expect(status()).toBe("A search runs only when you press Enter or Search; each one spends about 1 point of the hourly API budget of github.");
    I18N.locale = "pt-BR";
    I18N.strings = clone(fixtures.stringsPtBR);
    flushSync();
    expect(status()).toBe("A busca só roda quando você aperta Enter ou Buscar; cada uma gasta cerca de 1 ponto da cota por hora do github.");
  });
});

describe("a copied list", () => {
  test("is YAML an agent can read", () => {
    useCatalog({ views: false });
    state.health = { viewer: "carol" };
    state.rows = [{ unit: { provider: "slack", key: "T1/C1/1" }, link: "https://acme.slack.com/archives/C1/p1" }];
    const got = listYAML({ view: "needs-me", filter: 'widgets "x"', at: "2026-10-15T13:00:00Z" }, [clone(fixtures.copyRow), { task: clone(fixtures.copyTask) }]);
    for (const want of [
      'view: "needs-me"', 'filter: "widgets \\"x\\""', 'copied_at: "2026-10-15T13:00:00Z"', "count: 2", "items:",
      '  - ref: "acme/widgets#4870"', '    url: "https://github.com/acme/widgets/pull/4870"',
      '    title: "perf: one \\"conn\\"\\nper batch"', '    author: ["me", "carol"]', '    kind: ["conflicts"]', '    risk: ["high"]',
      '    size: ["m"]', '    status: ["in-progress"]', '    wait: ["me"]',
      '    since: "2026-10-13T13:00:00Z"', '    reason: "merge conflicts with develop"',
      '  - ref: "t-1"', '    url: "https://acme.slack.com/archives/C1/p1"', '    title: "Answer the deploy question"', '    units: ["#general thread"]', '    source: ["slack"]', '    where: ["general"]',
    ]) expect(got).toContain(want + "\n");
    expect(got).not.toContain("asked-by");
    expect(got.indexOf("    author:")).toBeLessThan(got.indexOf("    wait:"));
  });

  test("says how it was grouped and sorted", () => {
    const got = listYAML({ view: "needs-me", filter: "where:widgets", group_by: "where", sort: "risk descending", at: "x" }, []);
    for (const want of ['filter: "where:widgets"', 'group_by: "where"', 'sort: "risk descending"', "count: 0", "items:"]) expect(got).toContain(want + "\n");
  });

  test("of candidates says what the cards say", async () => {
    useCatalog();
    const c = {
      label: "acme/widgets#5068", unit: { provider: "github", key: "acme/widgets#5068" }, url: "https://github.com/acme/widgets/pull/5068", title: "fix(cron): flags", author: "ana",
      additions: 6, deletions: 0, changed_files: 2, review_decision: "APPROVED", checks: "FAILURE", review_requests: ["bia", "team:acme/web"], draft: false,
      updated_at: "2026-10-14T13:00:00Z", created_at: "2026-10-10T13:00:00Z", brief: { brief: { size: "s", risk: "low", goal: "Add flags.", open_questions: ["why?"] } },
    };
    const got = candidatesYAML({ query: "widgets", read: 'read "widgets" as repo:acme/widgets', at: "2026-10-15T13:00:00Z" }, [c]);
    for (const want of [
      'query: "widgets"', 'read_as: "read \\"widgets\\" as repo:acme/widgets"', "count: 1", "pull_requests:",
      '  - ref: "acme/widgets#5068"', '    url: "https://github.com/acme/widgets/pull/5068"', '    author: "ana"',
      '    size: "+6 -0, 2 files"', '    review: "approved"', '    checks: "failure"', '    asks: ["bia", "acme/web"]',
      '    updated_at: "2026-10-14T13:00:00Z"', "    in_loop: false", '    brief: {size: "s", risk: "low", open_questions: 1, goal: "Add flags."}',
    ]) expect(got).toContain(want + "\n");
    const copied: string[] = [];
    clipboard.writeText = async (s: string) => {
      copied.push(s);
    };
    state.explore.result = { candidates: [c], query: "widgets", total: 1 };
    nav.hash = "#view=explore&x=widgets";
    const { container } = render(Explore);
    await fireEvent.click(container.querySelector('[data-focus="explore:copy"]')!);
    await settle();
    expect(copied[0]).toContain('query: "widgets"');
    expect(notice.text).toBe("Copied 1 candidates as YAML.");
  });
});
