import { readdirSync, readFileSync } from "node:fs";
import { cleanup, render } from "@testing-library/svelte";
import { afterEach, describe, expect, it } from "vitest";
import { lensOf, pipeline, qualifiers, terms, filtersOf, groupQualifier, groupedRows } from "../../lib/lens";
import { unitLabel } from "../../lib/tasks";
import type { J } from "../../lib/util";
import { catalog, state } from "../../state/store.svelte";
import { readHash } from "../../state/route.svelte";
import { resetStore } from "../../testing/reset.svelte";
import PluginList from "./PluginList.svelte";
import { fixture, useCatalogs } from "./testdata/fixtures";

afterEach(cleanup);

describe("the rows' and screens' sources", () => {
  it("say task, never the old words, in their classes", () => {
    for (const dir of [".", "../screens"]) {
      for (const f of readdirSync(new URL(dir + "/", import.meta.url))) {
        if (!f.endsWith(".svelte") && !(f.endsWith(".ts") && !f.endsWith(".test.ts"))) continue;
        const src = readFileSync(new URL(dir + "/" + f, import.meta.url), "utf8");
        expect(src.match(/class(?:=|: )["{][^"}]*\b(gtd|entry|entries)\b/g), f).toBeNull();
      }
    }
  });
});

describe("a plugin view's list", () => {
  it("is the cockpit's row, with its facets, and an empty list says the view's word", () => {
    resetStore(Date.parse("2026-10-12T12:00:00Z"), "#view=mine");
    useCatalogs();
    const pr = { ...fixture("tag-rows.json")[1], views: ["mine"] };
    state.rows = [pr];
    const c = render(PluginList, { rows: state.rows }).container;
    const list = c.firstElementChild!;
    expect(list.tagName).toBe("DIV");
    expect(list.querySelector('[data-row^="row:"]')!.getAttribute("data-row")).toBe("row:github:acme/web#2");
    expect(Array.from(list.querySelectorAll("[data-rove]")).filter((n) => n.className === "facet").length).toBeGreaterThan(0);
    cleanup();
    expect(render(PluginList, { rows: [] }).container.textContent).toBe("Nothing here right now.");
    cleanup();
    expect(render(PluginList, { rows: [], empty: "No pull requests" }).container.textContent).toBe("No pull requests");
  });
});

describe("tag qualifiers", () => {
  it("filter and group by a tag key, a row in two groups listed once", () => {
    resetStore(Date.parse("2026-10-12T12:00:00Z"));
    useCatalogs();
    catalog.views.push({ id: "needs-me", label: "Needs me", provider: "github", rows: true, surfaces: ["web"] });
    const rows = fixture("tag-rows.json");
    const run = (q: string, group = "") => {
      const lens = { ...lensOf({ ...readHash(), view: "needs-me" }), q, ts: terms(q), quals: qualifiers(q), filters: filtersOf(qualifiers(q)), group, sort: "", layout: "" };
      return pipeline(rows, lens);
    };
    const nums = (q: string) => groupedRows(run(q).groups).map((x: J) => x.ref.number);
    expect(nums("tag:lang:go")).toEqual([2, 1]);
    expect(nums("tag:lang")).toEqual([2, 1, 5]);
    expect(nums("-tag:lang:go")).toEqual([4, 3, 5]);
    expect(nums("-tag:lang")).toEqual([4, 3]);
    expect(nums("tag:lang:go -tag:change:fix")).toEqual([2]);
    expect(nums("billing")).toEqual([4]);
    expect(nums("TAG:Lang:Go")).toEqual([2, 1]);
    const by = run("", "tag:lang");
    expect(by.groups.map((g) => `${g.key}(${g.label})=${g.rows.map((x: J) => x.ref.number).join(",")}`)).toEqual(["lang:go(lang:go)=2,1", "lang:python(lang:python)=5", "lang:sql(lang:sql)=2", "none(no lang)=4,3"]);
    expect(groupedRows(by.groups).map((x: J) => x.ref.number)).toEqual([2, 1, 5, 4, 3]);
    expect([groupQualifier(by.groups[0]), groupQualifier(by.groups[3])]).toEqual([{ field: "tag", raw: "lang:go", neg: false }, { field: "tag", raw: "lang", neg: true }]);
  });

  it("name a unit by its provider and key when it carries no label", () => {
    expect([unitLabel({ provider: "github", key: "acme/api#7" }), unitLabel({ provider: "linear", key: "PAY-88" }), unitLabel(null)]).toEqual(["github:acme/api#7", "linear:PAY-88", ""]);
  });
});
