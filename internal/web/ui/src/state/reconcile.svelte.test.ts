import { flushSync } from "svelte";
import { describe, expect, test } from "vitest";
import { reconcile } from "./reconcile";

describe("reconcile", () => {
  test("keeps the items it finds again by id or unit, in the source's order", () => {
    const a = { id: "e-1", title: "Old" };
    const b = { unit: { provider: "acme", key: "web#7" }, title: "Row" };
    const list = [a, b];
    const out = reconcile(list, [
      { unit: { provider: "acme", key: "web#7" }, title: "Row moved" },
      { id: "e-2", title: "New" },
      { id: "e-1", title: "Old" },
    ]);
    expect(out).toBe(list);
    expect(out[0]).toBe(b);
    expect(out[2]).toBe(a);
    expect(b.title).toBe("Row moved");
    expect(out.map((x: { title: string }) => x.title)).toEqual(["Row moved", "New", "Old"]);
  });

  test("drops the keys the source no longer holds and keeps nested objects", () => {
    const facets = { who: ["me"] };
    const row: Record<string, unknown> = { id: "e-1", facets, stale: true };
    reconcile(row, { id: "e-1", facets: { who: ["you"] } });
    expect(row.facets).toBe(facets);
    expect(facets.who).toEqual(["you"]);
    expect("stale" in row).toBe(false);
  });

  test("returns the source when the shapes differ", () => {
    expect(reconcile<unknown>({ a: 1 }, [1])).toEqual([1]);
    expect(reconcile<unknown>(null, { a: 1 })).toEqual({ a: 1 });
  });

  test("writes only what changed into reactive state", () => {
    const store = $state({ rows: [{ id: "e-1", title: "One", n: 1 }, { id: "e-2", title: "Two", n: 2 }] });
    const first = store.rows[0];
    let titles = 0;
    let counts = 0;
    const stop = $effect.root(() => {
      $effect(() => {
        void store.rows[0].title;
        titles++;
      });
      $effect(() => {
        void store.rows[1].n;
        counts++;
      });
    });
    flushSync();
    reconcile(store.rows, [{ id: "e-1", title: "One", n: 1 }, { id: "e-2", title: "Two", n: 3 }]);
    flushSync();
    expect(store.rows[0]).toBe(first);
    expect(titles).toBe(1);
    expect(counts).toBe(2);
    stop();
  });
});
