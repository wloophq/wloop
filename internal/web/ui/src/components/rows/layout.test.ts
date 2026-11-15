import { render } from "@testing-library/svelte";
import { existsSync, readFileSync, writeFileSync } from "node:fs";
import { beforeEach, expect, test } from "vitest";
import { applyCatalog } from "../../lib/catalog";
import { fieldOf, lensOf } from "../../lib/lens";
import { clock } from "../../state/clock.svelte";
import { readHash } from "../../state/route.svelte";
import { state, tasksState } from "../../state/store.svelte";
import { fixtures } from "../../testing/chrome";
import { resetStore } from "../../testing/reset.svelte";
import { rowModel } from "./model";
import TestRow from "./testdata/TestRow.svelte";

// The user's row as the cockpit draws it is the page the Go layout tests lay out in a headless Chrome
// (internal/web/layout_test.go); UPDATE_LAYOUT=1 rewrites it.
const GOLDEN = "../testdata/layout/users-row.html";

beforeEach(() => resetStore());

test("the user's row is what the layout tests lay out", () => {
  const { row, specs, catalog, now } = fixtures.usersRow;
  applyCatalog(catalog, fieldOf);
  tasksState.catalog = fixtures.views;
  state.health = { viewer: "me" };
  state.rows = [row];
  state.specs = { version: "", byId: specs };
  state.loaded = true;
  clock.now = now;
  const c = render(TestRow, { make: () => {
    const lens = lensOf({ ...readHash(), view: "mine" });
    return { model: rowModel(state.rows[0], "", lens, ""), lens };
  } }).container;
  const html = (c.querySelector(".row") as HTMLElement).outerHTML.replace(/<!---->/g, "");
  if (process.env.UPDATE_LAYOUT || !existsSync(GOLDEN)) writeFileSync(GOLDEN, html + "\n");
  expect(html + "\n").toBe(readFileSync(GOLDEN, "utf8"));
});
