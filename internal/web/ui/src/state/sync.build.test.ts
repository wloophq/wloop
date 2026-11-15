import { afterEach, beforeEach, expect, test } from "vitest";
import { FakeDaemon } from "../testing/fakedaemon";
import { resetStore } from "../testing/reset.svelte";
import { busy, state } from "./store.svelte";
import { page, update } from "./build.svelte";
import { applyChange, load, resetSync } from "./sync.svelte";

let reloads = 0;
let served = "b1";
const saved = { ...page };

beforeEach(() => {
  resetStore();
  resetSync();
  reloads = 0;
  served = "b1";
  update.shown = false;
  Object.assign(page, { build: () => "b1", reloadedFor: () => "", remember: () => {}, reload: () => reloads++ });
  const d = new FakeDaemon({ "/api/health": () => ({ build: served }), "/api/rows": { epoch: "e", rev: 1, rows: [] }, "/api/catalog": {} });
  d.fallback = () => ({});
});

afterEach(() => Object.assign(page, saved));

test("every health the page reads checks the build: the first load's and a change's", async () => {
  await load();
  expect(state.loaded).toBe(true);
  expect(update.shown).toBe(false);
  busy.add("acme:web#1");
  served = "b2";
  await applyChange({ units: [], all: false, rows: false, health: true, tasks: false, plan: false, gates: false });
  expect(update.shown).toBe(true);
  busy.clear();
  served = "b3";
  await applyChange({ units: [], all: false, rows: false, health: true, tasks: false, plan: false, gates: false });
  expect(reloads).toBe(1);
});
