import { beforeEach, describe, expect, test } from "vitest";
import { readHash } from "../state/route.svelte";
import { tasksState } from "../state/store.svelte";
import { goFixtures, servedCatalog } from "../testing/fixtures";
import { resetStore } from "../testing/reset.svelte";
import { applyCatalog } from "./catalog";
import { fieldOf, lensOf, matches, passesFilters } from "./lens";
import { taskItemOf } from "./tasks";
import type { J } from "./util";

// Ported from views_filter_test.go: the cockpit's task list and the daemon's counts keep the same tasks for a
// query; the daemon's answers are Go's lens over the same specs, in cockpit.fixtures.json.

const fx = goFixtures();

beforeEach(() => {
  resetStore(Date.parse(fx.now));
  applyCatalog(servedCatalog(), fieldOf);
  tasksState.catalog = fx.views;
});

describe("the task filter", () => {
  test("the cockpit and the daemon match the same tasks", () => {
    const { tasks, queries, want } = fx.taskMatch as { tasks: J[]; queries: string[]; want: boolean[][] };
    const items = tasks.map(taskItemOf);
    queries.forEach((q, i) => {
      const lens = lensOf({ ...readHash(), view: "inbox", area: "", ctx: [], q });
      const got = items.map((it) => matches(it, lens.ts) && passesFilters(it, lens.filters));
      expect(got, q).toEqual(want[i]);
    });
    for (const q of ["general", "failure thread"]) expect(want[queries.indexOf(q)][tasks.length - 1], q).toBe(true);
  });
});
