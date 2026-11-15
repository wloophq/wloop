import { fireEvent, render } from "@testing-library/svelte";
import { readFileSync } from "node:fs";
import { flushSync } from "svelte";
import { beforeEach, describe, expect, test } from "vitest";
import { I18N } from "../../lib/i18n.svelte";
import { filtersOf, inView, lensOf, passesFilters, qualifierOf, qualifiers, terms } from "../../lib/lens";
import { keyOf, type J } from "../../lib/util";
import { nav, readHash } from "../../state/route.svelte";
import { state } from "../../state/store.svelte";
import { clone, fixtures, followHashes, useCatalog, useRows } from "../../testing/chrome";
import { resetStore } from "../../testing/reset.svelte";
import { charts } from "./chart";
import Dash from "./Dash.svelte";
import {
  dashAgeBoxOption, dashAgingOption, dashAuthorsOption, dashBlockersOption, dashClickQuery, dashCostOption, dashKindOption, dashKpis, dashLift, dashLiftOption,
  dashNextOption, dashOpenedOption, dashPalette, dashPeopleOption, dashReposOption, dashShapeOption, dashTagsOption, dashTheme, dashWaitOption, dashWorkingMs,
} from "./dash";

const now: number = fixtures.dashNow;
const pal = { me: "#000001", author: "#000002", reviewer: "#000003", loop: "#000004", nobody: "#000005", ink: "#000006", muted: "#000007", rule: "#000008",
  panel: "#ffffff", bg: "#000009", sel: "#00000a", late: "#00000b", err: "#00000c", mine: "#00000d" };

function backlog(mod?: (rows: J[]) => void): J[] {
  const all = clone(fixtures.dashRows) as J[];
  if (mod) mod(all);
  useRows(all);
  state.roundGates = new Map(all.filter((r) => r.round_gate).map((r) => [keyOf(r), r.round_gate]));
  return all;
}

function lensOfQ(q: string): J {
  return { view: "dash", q, ts: terms(q), quals: qualifiers(q), filters: filtersOf(qualifiers(q)), tk: "", done: false };
}

const bars = (o: J) => (o ? Object.fromEntries(o.series[0].data.map((d: J) => [d.name, d.value])) : null);

beforeEach(() => {
  resetStore(now);
  followHashes();
  useCatalog();
  state.health = { viewer: "me" };
});

describe("the backlog", () => {
  test("leaves done out unless asked, and holds only pull requests", () => {
    const all = backlog();
    all.push({ unit: { provider: "thread", key: "C1/171.1" }, title: "Deploy window?", views: ["threads"], summary: { closed: false } });
    expect([all.filter((r) => inView(r, "dash", false)).length, all.filter((r) => inView(r, "dash", true)).length, all.filter((r) => inView(r, "all", false)).length]).toEqual([6, 7, 8]);
  });

  test("each panel counts ignoring its own field", () => {
    const all = backlog();
    const lens = lensOfQ("wait:me where:acme/api");
    const rows = all.filter((r) => inView(r, "dash", false));
    const repos = dashReposOption(rows, lens, pal)!.series[0].data.flatMap((o: J) => o.children.map((c: J) => [o.name + "/" + c.name, c.value]));
    expect(bars(dashWaitOption(rows, lens, pal))).toEqual({ "Waiting on me": 2 });
    expect(bars(dashKindOption(rows, lens, pal))).toEqual({ review: 1, "checks failing": 1 });
    expect(Object.fromEntries(repos)).toEqual({ "acme/api": 2, "acme/web": 1 });
    expect(bars(dashAuthorsOption(rows, lens, pal))).toEqual({ ana: 1, me: 1 });
  });

  test("a selected value stays bright and the rest dim", () => {
    const rows = backlog().filter((r) => inView(r, "dash", false));
    const got = Object.fromEntries(dashWaitOption(rows, lensOfQ("wait:me"), pal)!.series[0].data.map((d: J) => [d.q[0][1], d.itemStyle.opacity]));
    expect(got.me).toBe(1);
    expect(got.author).toBeLessThan(1);
    expect(got.loop).toBeLessThan(1);
  });

  test("the KPIs agree with the flags they filter, and count what their tabs show", () => {
    const all = backlog();
    const rows = all.filter((r) => inView(r, "dash", false));
    const want: Record<string, number> = { pending: 3, "needs-me": 2, stalled: 3, "in-loop": 1, approved: 1, due: 2 };
    for (const k of dashKpis(rows, lensOfQ("is:stalled"))) {
      expect(k.n, k.id).toBe(want[k.id]);
      expect(k.on, k.id).toBe(k.id === "stalled");
    }
    const tabs: Record<string, string> = { "needs-me": "needs-me", stalled: "stalled", "in-loop": "loop", approved: "approved" };
    const kpis = dashKpis(rows, lensOfQ("")).filter((k) => tabs[k.id]);
    expect(kpis.length).toBe(4);
    for (const k of kpis) expect(k.n, k.id).toBe(all.filter((r) => inView(r, tabs[k.id])).length);
  });

  test("tag concentration nests values under their key", () => {
    const rows = backlog().filter((r) => inView(r, "dash", false));
    const lens = lensOfQ("tag:lang:go");
    const tree = dashTagsOption(rows, lens, pal, "")!.series[0].data;
    const by = Object.fromEntries(tree.map((k: J) => [k.name, Object.fromEntries(k.children.map((c: J) => [c.name, c.value]))]));
    expect(by.area).toEqual({ auth: 3, ui: 2, billing: 1 });
    expect(bars(dashTagsOption(rows, lens, pal, "lang"))).toEqual({ go: 4, ts: 2 });
    expect(tree.find((k: J) => k.name === "area").q).toEqual([["tag", "area"]]);
  });

  test("tag lift compares the stalled with the backlog", () => {
    const rows = backlog().filter((r) => inView(r, "dash", false));
    const lens = lensOfQ("is:stalled");
    const lift = dashLift(rows, lens);
    expect(lift.map((l: J) => [l.tag, l.n, l.stalled, l.lift])).toEqual([["area:auth", 3, 2, 33], ["lang:go", 4, 2, 0]]);
    expect(dashLiftOption(rows, lens, pal)!.series[0].data[0].q).toEqual([["tag", "area:auth"], ["is", "stalled"]]);
  });

  test("working time leaves weekends out", () => {
    const at = (s: string) => Date.parse(s);
    const h = 3600000;
    expect([
      dashWorkingMs(at("2026-10-14T09:00:00Z"), at("2026-10-14T17:30:00Z")),
      dashWorkingMs(at("2026-10-16T12:00:00Z"), at("2026-10-19T12:00:00Z")),
      dashWorkingMs(at("2026-10-17T03:00:00Z"), at("2026-10-18T22:00:00Z")),
      dashWorkingMs(at("2026-10-09T18:00:00Z"), at("2026-10-21T06:00:00Z")),
      dashWorkingMs(at("2026-10-20T00:00:00-03:00"), at("2026-10-19T00:00:00Z")),
    ]).toEqual([8.5 * h, 24 * h, 0, (7 * 24 + 12) * h, 0]);
  });

  test("aging plots each pull request by stage and wait", () => {
    const rows = backlog().filter((r) => inView(r, "dash", false));
    const o = dashAgingOption(rows, lensOfQ(""), pal, now, 48)!;
    expect(o.xAxis[0].data).toEqual(["agent working", "checks failing", "merge conflicts", "review", "can merge"]);
    const days = Object.fromEntries(o.series[0].data.map((p: J) => [p.pr, p.value[1]]));
    expect(Object.keys(days).length).toBe(6);
    expect(Math.abs(days["github:acme/api#1"] - 0.625)).toBeLessThan(0.02);
    expect(Math.abs(days["github:acme/web#6"] - 5.625)).toBeLessThan(0.02);
    const lines = Object.fromEntries(o.series[0].markLine.data.map((l: J) => [l.name, l.yAxis]));
    expect(lines.stalled).toBe(2);
    expect(Math.abs(lines.p50 - 0.625)).toBeLessThan(0.02);
    expect(lines.p85).toBeGreaterThan(lines.p50);
  });

  test("panels on facts stay quiet without them", () => {
    const rows = backlog((all) => all.forEach((r) => {
      if (r.payload) delete r.payload.facts;
      delete r.round_gate;
      delete r.facets.due;
      delete r.facets.opened;
      r.facets.ci = ["none"];
    })).filter((r) => inView(r, "dash", false));
    state.roundGates = new Map();
    const lens = lensOfQ("");
    expect(dashNextOption(rows, lens, pal, now, null)).toBeNull();
    expect(dashOpenedOption(rows, lens, pal)).toBeNull();
    expect(dashCostOption(rows, lens, pal)).toBeNull();
    expect(dashBlockersOption(rows, lens, pal)!.series[1].data.length).toBe(0);
  });

  test("panels on facts read the contract", () => {
    const rows = backlog().filter((r) => inView(r, "dash", false));
    const lens = lensOfQ("");
    const next = dashNextOption(rows, lens, pal, now, { brief: { paused_until: new Date(now + 3600000).toISOString() } })!;
    const pick = (s: J) => Object.fromEntries(s.data.map((d: J) => [d.name, d.value]));
    expect(Object.fromEntries(next.series[0].data.map((d: J) => [d.name, next.yAxis.data[d.value[1]]]))).toEqual({
      "acme/api#1": "Round sends", "acme/api#2": "Mute ends", "acme/web#6": "Brief retry", "brief quota": "Brief quota resets",
    });
    expect(bars(dashOpenedOption(rows, lens, pal))).toEqual({ "0-1 days": 1, "1-2 weeks": 1, "over 4 weeks": 1 });
    const cost = dashCostOption(rows, lens, pal)!;
    expect(pick(cost.series[0])).toEqual({ "Waiting on me": 12, "Waiting on the author": 3 });
    expect(pick(cost.series[1])).toEqual({ "acme/api#2": 1.25, "acme/web#6": 0.5 });
    const blockers = dashBlockersOption(rows, lens, pal)!;
    expect(pick(blockers.series[0])).toEqual({ "CI failing": 2, "Merge conflicts": 1 });
    expect(pick(blockers.series[1])).toEqual({ lint: 2, unit: 1 });
    const ci = (q: string) => rows.filter((r) => passesFilters(r, filtersOf(qualifiers(q)))).map((r) => r.ref.number);
    expect([...ci("ci:failure"), 0, ...ci("ci:lint"), 0, ...ci("opened:older")]).toEqual([2, 6, 0, 2, 6, 0, 6]);
  });

  test("spend falls back to tokens without a cost", () => {
    const rows = backlog((all) => {
      all[1].payload.facts.turns = { count: 3, tokens: 90000, cost_usd: 0 };
      all[5].payload.facts.turns = { count: 1, tokens: 4000 };
    }).filter((r) => inView(r, "dash", false));
    const cost = dashCostOption(rows, lensOfQ(""), pal)!;
    expect(cost.title[1].text).toBe("Tokens per pull request\nno US$ cost recorded");
    expect(Object.fromEntries(cost.series[1].data.map((d: J) => [d.name, d.value]))).toEqual({ "acme/api#2": 90000, "acme/web#6": 4000 });
  });

  test("shape and age boxes filter by size and risk", () => {
    const rows = backlog().filter((r) => inView(r, "dash", false));
    const lens = lensOfQ("size:s");
    const cells: Record<string, number> = {};
    for (const d of dashShapeOption(rows, lens, pal)!.series[0].data) {
      const key = `${d.q[0][1]}/${d.q[1][1]}`;
      cells[key] = d.value[2];
      expect(d.itemStyle.opacity === 1, key).toBe(key === "s/low");
    }
    expect(cells["s/low"]).toBe(2);
    expect(cells["l/high"]).toBe(1);
    expect(cells["xl/high"]).toBe(1);
    expect(Object.keys(cells).length).toBe(5);
    const boxes = Object.fromEntries(dashAgeBoxOption(rows, lens, pal, now)!.series[0].data.map((d: J) => [d.name, d.value]));
    const s = boxes["size S"];
    expect(s.length).toBe(5);
    expect(s[0]).toBeLessThanOrEqual(s[2]);
    expect(s[2]).toBeLessThanOrEqual(s[4]);
    expect(boxes["high risk"]).toBeUndefined();
  });

  test("tooltips escape titles and tags", () => {
    const rows = backlog((all) => all.forEach((r) => {
      r.title = "<img src=x onerror=alert(1)>";
      if (r.facets.tag) r.facets.tag = r.facets.tag.map((t: string) => (t === "area:auth" ? "area:<b>x</b>" : t));
    })).filter((r) => inView(r, "dash", false));
    const lens = lensOfQ("");
    const tips = [...dashAgingOption(rows, lens, pal, now, 48)!.series[0].data, ...dashLiftOption(rows, lens, pal)!.series[0].data].map((p: J) => p.tooltip.formatter as string);
    for (const tip of tips) expect(tip).not.toMatch(/[<>]/);
    expect(tips.some((tip) => tip.includes("&lt;img src=x onerror=alert(1)&gt;"))).toBe(true);
    expect(tips.some((tip) => tip.includes("area:&lt;b&gt;x&lt;/b&gt;"))).toBe(true);
  });
});

describe("a click", () => {
  test("writes the query", () => {
    const cases: [string, [string, string][], boolean, string][] = [
      ["", [["wait", "me"]], false, "wait:me"],
      ["wait:me", [["wait", "me"]], false, ""],
      ["wait:me", [["wait", "author"]], false, "wait:me wait:author"],
      ["wait:me", [["wait", "me"]], true, "-wait:me"],
      ["fix", [["waits-on", "team:acme/core"]], false, "fix waits-on:team:acme/core"],
      ["", [["size", "s"], ["risk", "low"]], false, "size:s risk:low"],
      ["size:s", [["size", "s"], ["risk", "low"]], false, "size:s risk:low"],
      ["size:s risk:low where:x", [["size", "s"], ["risk", "low"]], false, "where:x"],
      ["", [["tag", "area:auth"], ["is", "stalled"]], false, "tag:area:auth is:stalled"],
      ["", [["ci", "unit tests"]], false, 'ci:"unit tests"'],
      ["", [["opened", "2w"]], true, "-opened:2w"],
    ];
    for (const [q, pairs, neg, want] of cases) expect(dashClickQuery(q, pairs, neg), `${q} ${JSON.stringify(pairs)}`).toBe(want);
  });

  test("on any datum names a filter field", () => {
    const rows = backlog().filter((r) => inView(r, "dash", false));
    const lens = lensOfQ("");
    const opts = [dashWaitOption, dashPeopleOption, dashAuthorsOption, dashReposOption, dashKindOption, dashShapeOption, dashOpenedOption, dashCostOption, dashBlockersOption,
      dashLiftOption].map((f) => (f as J)(rows, lens, pal, now))
      .concat([dashAgeBoxOption(rows, lens, pal, now), dashTagsOption(rows, lens, pal, ""), dashTagsOption(rows, lens, pal, "area")]);
    const qs: string[] = [];
    const walk = (d: J) => {
      if (d.q) qs.push(...d.q.map((p: string[]) => p.join(":")));
      for (const c of d.children || []) walk(c);
    };
    for (const o of opts) for (const s of o.series) for (const d of s.data) walk(d);
    expect(qs.length).toBeGreaterThanOrEqual(20);
    for (const p of qs) {
      const x = qualifierOf(p.includes(" ") ? p.replace(/:(.*)$/, ':"$1"') : p);
      expect(x && p.startsWith(x.field + ":"), p).toBe(true);
    }
  });
});

describe("the chart theme", () => {
  test("comes from the CSS tokens, light and dark", () => {
    const css = readFileSync("../static/app.css", "utf8");
    const token = /--([\w-]+):\s*([^;]+);/g;
    const [lightCSS, rest] = css.split("@media (prefers-color-scheme: dark)");
    const darkCSS = rest.split("\n}\n")[0];
    const light: Record<string, string> = {};
    for (const m of lightCSS.matchAll(token)) light[m[1]] = m[2];
    const dark = { ...light };
    for (const m of darkCSS.matchAll(token)) dark[m[1]] = m[2];
    for (const tokens of [light, dark]) {
      const p = dashPalette((n) => " " + (tokens[n.slice(2)] || "") + " ");
      for (const v of Object.values(p)) expect(v).toMatch(/^#[0-9a-fA-F]{6}$/);
      expect(p.me).toBe(tokens.me);
      expect(p.nobody).toBe(tokens.none);
      expect(dashTheme(p).color.length).toBeGreaterThanOrEqual(5);
    }
  });
});

describe("the dashboard", () => {
  interface Fake {
    el: HTMLElement;
    theme: string;
    renderer: string;
    options: J[];
    cleared: number;
    disposed: boolean;
    click?: (p: J) => void;
  }
  let made: Fake[];
  let themes: string[];

  beforeEach(() => {
    made = [];
    themes = [];
    const lib = {
      registerTheme: (name: string) => themes.push(name),
      init: (el: HTMLElement, theme: string, opts: J) => {
        const f: Fake = { el, theme, renderer: opts.renderer, options: [], cleared: 0, disposed: false };
        made.push(f);
        return {
          on: (_: string, fn: (p: J) => void) => (f.click = fn),
          setOption: (o: J) => f.options.push(o),
          clear: () => f.cleared++,
          dispose: () => (f.disposed = true),
          resize: () => {},
        };
      },
    };
    charts.lib = () => lib;
    charts.dark = () => false;
  });

  function show(hash = "#view=dash") {
    nav.hash = hash;
    const props = $state({ lens: lensOf(readHash()) });
    const r = render(Dash, props);
    return { ...r, props };
  }

  test("says so when the charts library did not load", () => {
    charts.lib = () => undefined;
    const { container } = show();
    expect(container.querySelector("#dash .empty")!.textContent).toBe("The charts library did not load; reload the page.");
  });

  test("draws each panel as an SVG chart and sets a new option in place as the rows change", () => {
    backlog();
    const { container, props } = show();
    expect(themes).toEqual(["loop-light"]);
    const wait = made.find((f) => f.el.id === "dash-wait")!;
    expect(wait.renderer).toBe("svg");
    expect(wait.options.length).toBe(1);
    const node = container.querySelector("#dash-wait");
    const kpi = container.querySelector('[data-focus="dash:kpi:stalled"]') as HTMLElement;
    kpi.focus();
    useRows(fixtures.dashRows.slice(1));
    props.lens = lensOf(readHash());
    flushSync();
    expect(container.querySelector("#dash-wait")).toBe(node);
    expect(made.filter((f) => f.el.id === "dash-wait").length).toBe(1);
    expect(wait.options.length).toBe(2);
    expect(container.querySelector('[data-focus="dash:kpi:stalled"]')).toBe(kpi);
    expect(document.activeElement).toBe(kpi);
  });

  test("a click on a datum filters by it, Alt+click leaves it out, a pull request's opens it", async () => {
    backlog();
    show();
    const wait = made.find((f) => f.el.id === "dash-wait")!;
    wait.click!({ data: { q: [["wait", "me"]] }, event: { event: { altKey: false } } });
    expect(readHash().q).toBe("wait:me");
    wait.click!({ data: { q: [["wait", "author"]] }, event: { event: { altKey: true } } });
    expect(readHash().q).toBe("wait:me -wait:author");
    wait.click!({ data: { pr: "github:acme/api#1" } });
    expect(readHash().pr).toBe("github:acme/api#1");
  });

  test("a KPI filters by its flag and Include done takes the done in", async () => {
    backlog();
    const { container, props } = show();
    await fireEvent.click(container.querySelector('[data-focus="dash:kpi:stalled"]')!);
    expect(readHash().q).toBe("is:stalled");
    await fireEvent.click(container.querySelector("#dash-done")!);
    expect(readHash().done).toBe(true);
    props.lens = lensOf(readHash());
    flushSync();
    expect(container.querySelector("#dash-done")!.getAttribute("aria-pressed")).toBe("true");
    expect(container.querySelector('[data-focus="dash:kpi:stalled"]')!.getAttribute("aria-pressed")).toBe("true");
  });

  test("a panel with no data says so and clears its chart; the charts go with the dashboard", () => {
    backlog((all) => all.forEach((r) => r.payload && delete r.payload.facts));
    state.roundGates = new Map();
    const { container, unmount } = show();
    expect((container.querySelector("#dash-cost") as HTMLElement).hidden).toBe(true);
    expect((container.querySelector("#dash-cost-none") as HTMLElement).hidden).toBe(false);
    expect(made.find((f) => f.el.id === "dash-cost")!.cleared).toBe(1);
    unmount();
    expect(made.every((f) => f.disposed)).toBe(true);
  });

  test("rewords its panels with the strings, the charts in place", () => {
    backlog();
    const { container } = show();
    const head = container.querySelector(".panel-head span")!;
    I18N.strings = { "Waiting on whom": "Esperando por quem", "Waiting on me": "Esperando por mim" };
    flushSync();
    expect(container.querySelector(".panel-head span")).toBe(head);
    expect(head.textContent).toBe("Esperando por quem");
    expect(made.filter((f) => f.el.id === "dash-wait").length).toBe(1);
  });
});
