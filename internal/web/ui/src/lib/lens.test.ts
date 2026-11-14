import { beforeEach, describe, expect, test } from "vitest";
import { clock } from "../state/clock.svelte";
import { goFixtures, servedCatalog, testdata } from "../testing/fixtures";
import { readHash } from "../state/route.svelte";
import { catalog, state, tasksState } from "../state/store.svelte";
import { resetStore } from "../testing/reset.svelte";
import { applyCatalog } from "./catalog";
import {
  byDefault, copySlot, fieldById, fieldOf, filtersOf, groupField, groupedRows, groupQualifier, itemValues, lensFields, lensOf, markedSpans, matches, multiCopy,
  openSlot, passesFilters, pipeline, pipelineOf, qualifiers, rowByURL, rowParts, rowStrip, rowTip, rowWords, stripColor, stripOf, stripSpec, tagKeys, terms,
  toggledQualifier, viewStrip, waitCat, waitClass, waitingLabel, withoutQualifiers, reasonText, type Lens,
} from "./lens";
import { taskItemOf } from "./tasks";
import { keyOf, tokens, type J } from "./util";

// The lens, ported from board_test.go, tags_test.go, tagcopies_test.go, strip_test.go, catalog_test.go and
// lens_parity_test.go. Rows with the GitHub provider's facets and Go's answers come from cockpit.fixtures.json.

const fx = goFixtures();

beforeEach(() => {
  resetStore(Date.parse(fx.now));
  applyCatalog(servedCatalog(), fieldOf);
  state.health = { viewer: "me" };
  tasksState.catalog = { lens: fx.views.lens };
});

// lensFor is a lens over q as the Go tests build one by hand.
function lensFor(q: string, more: Partial<Lens> = {}): Lens {
  return { view: "needs-me", q, ts: terms(q), quals: qualifiers(q), filters: filtersOf(qualifiers(q)), group: "", sort: "", layout: "", ...more } as Lens;
}

const numbers = (rows: J[]) => rows.map((x) => x.ref.number);

describe("the filter", () => {
  test("folds and marks", () => {
    const cases: [string, string, unknown][] = [
      ["Exportação de dados", "exportacao", [{ mark: "Exportação" }, " de dados"]],
      ["Expórt now", "export", [{ mark: "Expórt" }, " now"]],
      ["Expórt now", "expo", [{ mark: "Expó" }, "rt now"]],
      ["batch export", "batch atch exp", [{ mark: "batch" }, " ", { mark: "exp" }, "ort"]],
      ["batch", "bat ch", [{ mark: "batch" }]],
      ["🚀 launch", "launch", ["🚀 ", { mark: "launch" }]],
      ["🚀 launch", "🚀", [{ mark: "🚀" }, " launch"]],
      ["<b>bold</b> fix", "fix", ["<b>bold</b> ", { mark: "fix" }]],
      ["nothing here", "zzz", ["nothing here"]],
    ];
    for (const [text, q, want] of cases) {
      expect(markedSpans(text, terms(q)).map((s) => (s.hit ? { mark: s.text } : s.text)), `${text} / ${q}`).toEqual(want);
    }
  });

  test("matches every term, and a pasted link finds its row", () => {
    const pull = fx.filterRows.pull;
    const issue = { unit: { provider: "linear", key: "PAY-88" }, label: "PAY-88", title: "Retry budget", link: "https://linear.app/acme/issue/PAY-88" };
    state.rows = [pull, issue];
    expect(terms("  Exportação  ANA ")).toEqual(["exportacao", "ana"]);
    expect(["batch ana", "BATCH zoe", "api#7", "", "batch-x", "exportacao"].map((q) => matches(state.rows[0], terms(q)))).toEqual([true, false, true, true, true, false]);
    const urls = ["https://github.com/acme/api/pull/7/files", " https://github.com/acme/api/pull/7#discussion_r1 ",
      "https://github.com.evil.com/acme/api/pull/7", "https://evil.com/github.com/acme/api/pull/7", "http://github.com/acme/api/pull/7",
      "https://github.com/acme/../pull/7", "https://github.com/acme/api/pull/70", "acme/api#7",
      "https://linear.app/acme/issue/PAY-88/retry-budget", "https://linear.app/acme/issue/PAY-8"];
    expect(urls.map((u) => {
      const r = rowByURL(u);
      return r ? keyOf(r) : "";
    })).toEqual(["github:acme/api#7", "github:acme/api#7", "", "", "", "", "", "", "linear:PAY-88", ""]);
  });

  test("finds kinds by their label", () => {
    const r = { title: "t", label: "a/b#1", unit: { provider: "github", key: "a/b#1" }, kind: "conflicts" };
    expect(["merge conflicts", "conflicts", "can merge"].map((q) => matches(r, terms(q)))).toEqual([true, true, false]);
  });

  test("finds a row's search texts", () => {
    expect(["webhook", "batch delivery", "zebra"].map((q) => matches(fx.filterRows.search, terms(q)))).toEqual([true, true, false]);
  });

  test("finds a reason by its words and by its sentence", () => {
    catalog.reasons = [{ provider: "github", words: { no_reviewer_requested: "nenhum revisor pedido" } }];
    const r = fx.filterRows.reason;
    expect(reasonText(r)).toBe("nenhum revisor pedido");
    expect(["revisor", "reviewer", "zebra"].map((q) => matches(r, terms(q)))).toEqual([true, true, false]);
  });
});

describe("a query", () => {
  test("holds free terms and qualifiers", () => {
    const qs = [
      `where:acme/widgets wait:me fix`,
      `status:"needs me" -risk:high "two words"`,
      `fix: https://github.com/acme/api/pull/7 is:pr author:@me AUTHOR:Ana`,
      `Where:Widgets -waits-on:team:acme/search unknown:x`,
    ];
    expect(qs.map((q) => terms(q))).toEqual([["fix"], ["two words"], ["fix:", "https://github.com/acme/api/pull/7"], ["unknown:x"]]);
    const q = (field: string, value: string, raw: string, neg: boolean) => ({ field, value, raw, neg });
    expect(qs.map((s) => qualifiers(s))).toEqual([
      [q("where", "acme/widgets", "acme/widgets", false), q("wait", "me", "me", false)],
      [q("status", "needs me", "needs me", false), q("risk", "high", "high", true)],
      [q("is", "pr", "pr", false), q("author", "me", "@me", false), q("author", "ana", "Ana", false)],
      [q("where", "widgets", "Widgets", false), q("waits-on", "team:acme/search", "team:acme/search", true)],
    ]);
    expect(tokens(qs[1])).toEqual([`status:"needs me"`, "-risk:high", `"two words"`]);
  });

  test("a facet toggles its qualifier", () => {
    expect([
      toggledQualifier("fix", "where", "acme/widgets", false),
      toggledQualifier("fix where:acme/widgets", "where", "Acme/Widgets", false),
      toggledQualifier("where:a", "where", "b", false),
      toggledQualifier("where:a", "where", "a", true),
      toggledQualifier("-where:a", "where", "a", false),
      toggledQualifier("author:@me", "author", "me", false),
      toggledQualifier("", "status", "needs me", false),
      toggledQualifier('status:"needs me"', "status", "needs me", false),
      withoutQualifiers("where:a fix -where:b where:c wait:me", "where", false),
    ]).toEqual(["fix where:acme/widgets", "fix", "where:a where:b", "-where:a", "where:a", "", `status:"needs me"`, "", "fix -where:b wait:me"]);
  });
});

describe("the strip", () => {
  test("waitCat is the strip and agrees with the label", () => {
    const cases = fx.waitCases as J[];
    for (const c of cases.slice(0, -1)) {
      const wait = (c.facets || {}).wait || [];
      const want = wait[0] || "";
      expect([waitCat(c), waitClass(c)], JSON.stringify(c.summary) + c.kind).toEqual([want, want ? "w-" + want : ""]);
      const loop = c.kind === "loop_working" || c.kind === "loop_waiting";
      if (!loop) expect(waitCat(c) === "me", JSON.stringify(c)).toBe(waitingLabel(c).split(", ").includes("me"));
    }
    expect(waitingLabel(cases[cases.length - 1])).toBe("ana");
  });

  test("a row's tooltip says whom it waits on, its kind and why", () => {
    const rows = fx.tipRows as J[];
    const lens = lensOf({ ...readHash(), view: "mine" });
    const tip = (r: J) => {
      const w = rowWords(r.unit.provider);
      const item = { facets: r.facets || {}, provider: r.unit.provider };
      const kind = (rowParts(item, lens.specs, w).kind || []).map((x) => x.label).join(", ");
      return rowTip(stripOf(item, lens.specs), waitingLabel(r), kind, reasonText(r), w);
    };
    expect(rows.map(tip)).toEqual([
      "Waiting on me: changes asked. changes requested by bia",
      "Waiting on the author (ana): changes asked. changes requested by bia",
      "Waiting on a reviewer (bia, team:acme/core): review",
      "In loop: agent working. a review round runs",
      "Waiting on nobody: draft",
    ]);
    expect(rows.map((r) => rowStrip(r)?.color)).toEqual(["me", "author", "reviewer", "loop", "nobody"]);
  });

  test("each item's strip is its own provider's, and its value filters it", () => {
    applyCatalog(fx.bundled, fieldOf);
    tasksState.catalog = fx.views;
    const [, , pr] = fx.loopRows as J[];
    const slack = { unit: { provider: "slack", key: "T1/C1/1" }, label: "#general", title: "Deploy plan", kind: "waits_on_others", views: ["slack"],
      summary: { waits_on: "Ana", since: "2026-10-12T09:00:00Z" },
      facets: { where: ["general"], "waits-on": ["Ana"], turn: ["others"], situation: ["waits_on_others"], age: ["2026-10-12T09:00:00Z"] } };
    const other = { unit: { provider: "thread", key: "C1/171.1" }, label: "C1", title: "A thread", kind: "new", views: ["slack"], summary: { waits_on: "Ana" },
      facets: { "waits-on": ["Ana"], turn: ["others"] } };
    const shown = (it: J, lens: Lens) => {
      const s = stripOf(it, lens.specs);
      if (!s) return null;
      const quals = qualifiers(toggledQualifier("", s.spec.id, s.value, false, lens.fields), lens.fields);
      const f = lens.strip ? fieldById(lens.strip, lens.fields) : null;
      return { field: s.spec.id, value: s.value, color: s.color, kept: quals.length === 1 && passesFilters(it, filtersOf(quals, lens.fields)), sort: f && f.sort ? f.id : "" };
    };
    const row = (r: J, view: string) => shown({ ...r, facets: r.facets || {}, provider: r.unit.provider }, lensOf({ ...readHash(), view }));
    const taskOf = (e: J) => shown(taskItemOf(e), lensOf({ ...readHash(), view: "inbox" }));
    expect(row(pr, "mine")).toMatchObject({ field: "wait", kept: true, sort: "wait" });
    expect(row(pr, "mine")?.color).toBe(row(pr, "mine")?.value);
    expect(row(slack, "slack")).toEqual({ field: "turn", value: "others", color: "author", kept: true, sort: "turn" });
    expect(row(other, "slack")).toBeNull();
    expect(taskOf({ id: "e-1", state: "next", facets: { state: ["next"], "waits-on": ["me"], turn: ["me"] } })).toMatchObject({ field: "turn", value: "me", color: "me", kept: true });
    expect(taskOf({ id: "e-2", state: "waiting", facets: { state: ["waiting"], "waits-on": ["ana"], turn: ["others"] } })).toMatchObject({ field: "turn", value: "others", color: "author", kept: true });
    const slackLens = lensOf({ ...readHash(), view: "slack" });
    const w = rowWords("slack");
    const item = { facets: slack.facets, provider: "slack" };
    const kind = (rowParts(item, slackLens.specs, w).kind || []).map((x) => x.label).join(", ");
    expect(rowTip(stripOf(item, slackLens.specs), waitingLabel(slack), kind, reasonText(slack), w).startsWith("Others' turn (Ana): waits on others")).toBe(true);
    expect(Object.values(rowParts(item, slackLens.specs, w)).flat().map((p) => p.field + ":" + p.label)).toContain("situation:waits on others");
  });

  test("reads only what its provider declares", () => {
    applyCatalog(fx.bundled, fieldOf);
    tasksState.catalog = fx.views;
    const ana = { id: "waits-on", label: "Waits on", colors: { Ana: "author" } };
    const merged = [{ id: "turn", label: "Turn", provider: "", strip: true, colors: { me: "me" }, displays: { a: {}, b: { strip: true, colors: { me: "me" } } } }];
    catalog.views.push({ id: "mixed", label: "Mixed", provider: "", rows: true, surfaces: ["web"] });
    expect([stripColor(ana, "ana"), stripColor(ana, "ANA")]).toEqual(["author", "author"]);
    expect(!!stripSpec(merged, "b")).toBe(true);
    expect(!!stripSpec(merged, "a") || !!stripSpec(merged, "c")).toBe(false);
    expect(viewStrip("mixed")).toBeNull();
    const w = fx.views.lens;
    expect(rowTip({ label: "In loop" }, "ana", "agent working", "", w)).toBe("In loop (ana): agent working");
    expect(rowTip({ label: "Waiting on me" }, "me", "review", "", w)).toBe("Waiting on me: review");
  });

  test("a loop row's tooltip names nobody the loop waits on", () => {
    applyCatalog(fx.bundled, fieldOf);
    const lens = lensOf({ ...readHash(), view: "mine" });
    for (const r of (fx.loopRows as J[]).slice(0, 2)) {
      const w = rowWords(r.unit.provider);
      const item = { facets: r.facets, provider: r.unit.provider };
      const kind = (rowParts(item, lens.specs, w).kind || []).map((x) => x.label).join(", ");
      const strip = stripOf(item, lens.specs);
      const tip = rowTip(strip, waitingLabel(r), kind, reasonText(r), w);
      expect(strip?.value).toBe("loop");
      expect(tip.startsWith("In loop: ")).toBe(true);
      expect(tip).not.toContain("(");
    }
  });

  test("a task view's menus name each field apart", () => {
    applyCatalog(fx.bundled, fieldOf);
    tasksState.catalog = fx.views;
    const seen = new Map<string, string>();
    for (const f of lensFields("inbox")) {
      expect(seen.get(f.label) ?? f.id, f.label).toBe(f.id);
      seen.set(f.label, f.id);
    }
  });
});

describe("the pipeline", () => {
  test("where filters by suffix", () => {
    const res = pipeline(fx.lensRows, lensFor("where:widgets"));
    expect(numbers(res.groups.flatMap((g) => g.rows)).sort()).toEqual([3, 4]);
  });

  test("filters, sorts and groups", () => {
    catalog.views.push({ id: "needs-me", label: "Needs me", provider: "github", rows: true, surfaces: ["web"] });
    const lenses: Partial<Lens>[] = [
      { q: "" }, { q: "where:widgets" }, { q: "where:widgets where:acme/web" }, { q: "where:acme/api wait:loop" }, { q: "author:me" },
      { q: "-risk:high risk:none risk:low" }, { q: "waits-on:me" }, { q: "is:stalled" }, { q: "", sort: "risk" }, { q: "", sort: "risk-desc" },
      { q: "", sort: "age-desc" }, { q: "", group: "wait" }, { q: "", group: "where", sort: "age" }, { q: "", layout: "board" }, { q: "", group: "size" },
    ];
    const got = lenses.map((l) => pipeline(fx.lensRows, lensFor(l.q || "", l)));
    const flat = [[2, 4, 3, 1, 5], [4, 3], [2, 4, 3], [5], [2], [3, 1], [2, 4, 5], [2, 4], [2, 5, 4, 1, 3], [1, 4, 2, 5, 3], [4, 5, 1, 2, 3]];
    flat.forEach((want, i) => expect(numbers(got[i].groups.flatMap((g) => g.rows)), JSON.stringify(lenses[i])).toEqual(want));
    expect([got[0].total, got[1].shown, got[1].total]).toEqual([5, 2, 5]);
    const keys = (i: number) => got[i].groups.map((g) => g.key + "=" + JSON.stringify(numbers(g.rows)));
    expect(keys(11)).toEqual(["me=[2,4]", "loop=[5]", "reviewer=[1]", "author=[3]"]);
    expect(keys(12)).toEqual(["acme/api=[1,5]", "acme/web=[2]", "acme/widgets=[3,4]"]);
    expect(keys(13)).toEqual(["me=[2,4]", "loop=[5]", "reviewer=[1]", "author=[3]"]);
    expect(keys(14)).toEqual(["xs=[5]", "s=[1]", "m=[4]", "l=[2]", "none=[3]"]);
    expect(got[14].groups[4].label).toBe("no brief");
    expect(got[11].groups[0].label).toBe("Waiting on me");
  });

  test("the default order breaks ties by provider then key", () => {
    const rows = [["github", "b/c#2"], ["gitlab", "a/a#1"], ["github", "a/b#10"]].map(([p, k]) => ({ unit: { provider: p, key: k }, label: k, summary: { stalled: false, since: "2026-10-01T00:00:00Z" } }));
    expect(rows.sort(byDefault).map(keyOf)).toEqual(["github:a/b#10", "github:b/c#2", "gitlab:a/a#1"]);
  });

  test("the tag qualifier and grouping by a tag key", () => {
    const rows = fx.tagRows;
    const run = (q: string, group = "") => {
      const r = pipeline(rows, lensFor(q, { group }));
      return { group: r.group, unique: numbers(groupedRows(r.groups)), groups: r.groups.map((g) => ({ key: g.key, label: g.label, rows: numbers(g.rows), only: g.field ? groupQualifier(g) : null })) };
    };
    const want: [string, number[]][] = [
      ["tag:lang:go", [2, 1]], ["tag:lang", [2, 1, 5]], ["-tag:lang:go", [4, 3, 5]], ["-tag:lang", [4, 3]], ["tag:lang:go tag:lang:python", [2, 1, 5]],
      ["tag:lang:go -tag:change:fix", [2]], ["billing", [4]], ["TAG:Lang:Go", [2, 1]], ["tag:lang where:acme/api", [1, 5]],
    ];
    for (const [q, rowsWant] of want) expect(run(q).unique, q).toEqual(rowsWant);
    const byLang = run("", "tag:lang");
    expect(byLang.groups.map((g) => `${g.key}(${g.label})=${JSON.stringify(g.rows)}`)).toEqual(["lang:go(lang:go)=[2,1]", "lang:python(lang:python)=[5]", "lang:sql(lang:sql)=[2]", "none(no lang)=[4,3]"]);
    expect(byLang.group).toBe("tag:lang");
    expect(byLang.unique).toEqual([2, 1, 5, 4, 3]);
    expect(run("", "tag:domain").groups.map((g) => `${g.key}(${g.label})=${JSON.stringify(g.rows)}`)).toEqual(["domain:billing(domain:billing)=[4]", "none(no domain)=[2,3,1,5]"]);
    expect([byLang.groups[0].only, byLang.groups[3].only]).toEqual([{ field: "tag", raw: "lang:go", neg: false }, { field: "tag", raw: "lang", neg: true }]);
  });

  test("tag keys list the keys present and a tag chip toggles its qualifier", () => {
    tasksState.catalog = { lens: { tag_key: "{{.label}} {{.key}}", tag_key_empty: "no {{.key}}" } };
    expect(tagKeys(fx.tagRows)).toEqual(["change", "domain", "lang"]);
    expect(["tag:lang", "tag", "tag:Lang", "tag:", "where"].map((id) => {
      const f = groupField(id);
      return f ? [f.id, f.label, !!f.group] : null;
    })).toEqual([["tag:lang", "Tag lang", true], ["tag", "Tag", false], null, null, ["where", "Where", true]]);
    expect(fieldById("tag")?.groupBy?.("lang:go")).toBe("tag:lang");
    expect([toggledQualifier("fix", "tag", "lang:go", false), toggledQualifier("tag:lang:go", "tag", "lang:go", false), toggledQualifier("tag:lang:go", "tag", "lang:go", true)])
      .toEqual(["fix tag:lang:go", "", "-tag:lang:go"]);
  });

  test("the type qualifier filters and groups by the brief kind", () => {
    const run = (q: string, group = "") => pipeline(fx.typeRows, lensFor(q, { group }));
    const sorted = (q: string) => numbers(run(q).groups.flatMap((g) => g.rows)).sort();
    expect(sorted("type:feature")).toEqual([1, 5]);
    expect(sorted("-type:feature")).toEqual([2, 3, 4]);
    expect(sorted("type:fix type:chore")).toEqual([2, 4]);
    const labels = run("", "type").groups.map((g) => g.label);
    expect(labels).toContain("feature");
    expect(labels).toContain("no brief");
  });

  test("a tag value named none is not the empty group", () => {
    const rows = fx.noneTagRows;
    const r = pipeline(rows, lensFor("", { group: "tag:lang" }));
    expect(r.groups.map((g) => g.key + "(" + g.label + ")=" + numbers(g.rows).join(","))).toEqual(["lang:go(lang:go)=2", "lang:none(lang:none)=1", "none(no lang)=3"]);
    expect(r.groups.map((g) => groupQualifier(g))).toEqual([
      { field: "tag", raw: "lang:go", neg: false }, { field: "tag", raw: "lang:none", neg: false }, { field: "tag", raw: "lang", neg: true },
    ]);
    expect(numbers(pipeline(rows, lensFor("tag:lang:none")).groups[0].rows)).toEqual([1]);
  });

  test("each copy of a row has its own slot, and the open slot is the copy last opened", () => {
    expect([multiCopy("tag:lang"), multiCopy("where")]).toEqual([true, false]);
    expect(["lang:go", "lang:sql", ""].map((c) => copySlot("row", "github:acme/web#2", c))).toEqual(["row:github:acme/web#2@lang:go", "row:github:acme/web#2@lang:sql", "row:github:acme/web#2"]);
    expect(copySlot("card", "github:acme/web#2", "lang:go")).toBe("card:github:acme/web#2@lang:go");
    state.openSlot = "row:github:acme/web#2@lang:sql";
    expect([openSlot("github:acme/web#2"), openSlot("github:acme/web#20"), openSlot("github:acme/api#2")]).toEqual(["row:github:acme/web#2@lang:sql", "", ""]);
  });
});

describe("the served facets", () => {
  test("time facets bucket on the client as the clock moves", () => {
    const t0 = Date.parse("2026-10-14T15:00:00Z");
    const iso = (ms: number) => new Date(ms).toISOString().replace(/\.\d+Z$/, "Z");
    const h = 3600000;
    const r = { unit: { provider: "github", key: "acme/api#1" }, kind: "review", facets: { opened: [iso(t0 - 20 * h)], age: [iso(t0 - 2 * h)], due: [iso(t0 + 30 * h)], is: ["needs-me"] } };
    const at = (ms: number, ids: string[]) => {
      clock.now = ms;
      return Object.fromEntries(ids.map((id) => [id, itemValues(fieldById(id)!, r)]));
    };
    const ids = ["opened", "age", "due", "is"];
    expect(at(t0, ids)).toEqual({ opened: ["1d"], age: ["1d"], due: ["later"], is: ["needs-me"] });
    expect(at(t0 + 2 * 86400000, ids)).toEqual({ opened: ["3d"], age: ["3d"], due: ["past"], is: ["needs-me"] });
    r.facets.due = [iso(t0 + 30 * h)];
    r.facets.due = [iso(t0 + 24 * h + 6 * h)];
    state.rev++;
    expect(at(t0, ["due", "is"])).toEqual({ due: ["later"], is: ["needs-me"] });
    expect(at(t0 + 86400000, ["due", "is"])).toEqual({ due: ["due"], is: ["needs-me", "due"] });
  });

  test("the lens over the served facets matches the golden", () => {
    const ops = testdata("web/testdata/lens.golden.json") as J[];
    const rows = testdata("readmodel/prrow/testdata/rows.json") as J[];
    state.health = { viewer: "me-login" };
    for (const o of ops) {
      const q = o.q || "";
      const r = pipeline(rows, lensFor(q, { view: o.view || "all", group: o.group || "", sort: o.sort || "", layout: o.layout || "", done: false }));
      const got = { group: r.group, total: r.total, shown: r.shown, groups: r.groups.map((g) => ({ key: g.key, label: g.label, rows: g.rows.map((x) => x.label) })) };
      expect(got, JSON.stringify({ ...o, result: undefined })).toEqual(o.result);
    }
  });

  test("a thread row of a provider the catalog adds stands in its view", () => {
    const c = servedCatalog();
    c.situations.push({ provider: "thread", kind: "waiting", label: "waits for a reply", rank: 0 });
    c.facets.push({ id: "waits-on", label: "Waits on", provider: "thread", show: "who" });
    applyCatalog(c, fieldOf);
    tasksState.catalog = fx.views;
    const r = { unit: { provider: "thread", key: "c1" }, link: "https://chat.example/c1", label: "#general c1", title: "Deploy question", kind: "waiting",
      summary: { waits_on: "ana", since: "2026-10-14T12:00:00Z", reason: "asked about the deploy" }, views: ["mine"], facets: { "waits-on": ["ana"] }, actions: [] };
    state.rows = [r];
    expect(pipeline(state.rows, lensOf({ ...readHash(), view: "mine" })).shown).toBe(1);
    expect(Object.values(rowParts({ facets: r.facets, provider: "thread" }, catalog.specs, rowWords("thread"))).flat().map((p) => p.label)).toEqual(["ana"]);
  });
});

describe("the Go lens", () => {
  const table = testdata("api/lens/testdata/cases.json");

  test("the cockpit's lens matches the Go lens", () => {
    clock.now = Date.parse(table.now);
    tasksState.catalog = { lens: table.words };
    catalog.fields = table.specs.map(fieldOf);
    for (const c of table.cases) {
      const quals = qualifiers(c.q);
      const lens = { q: c.q, ts: terms(c.q), quals, filters: filtersOf(quals), group: "", sort: c.sort ? c.sort + (c.desc ? "-desc" : "") : "", layout: "" } as unknown as Lens;
      const at = (rows: J[]) => rows.map((x) => table.items.indexOf(x));
      const out: J = { shown: at(pipelineOf(table.items, lens, null).groups[0].rows) };
      if (c.group) out.groups = pipelineOf(table.items, { ...lens, group: c.group }, null).groups.map((g) => ({ key: g.key, label: g.label, items: at(g.rows) }));
      expect(out, JSON.stringify(c)).toEqual(c.want);
    }
  });

  test("the cockpit places pieces as the Go lens", () => {
    const now = Date.parse(table.now);
    for (const c of table.parts.cases) expect(rowParts(c.item, table.parts.specs, table.parts.words, now), JSON.stringify(c.item)).toEqual(c.want);
  });
});
