import { beforeEach, expect, test } from "vitest";
import { resetStore } from "../testing/reset.svelte";
import { catalog, plugins } from "../state/store.svelte";
import { applyCatalog, applyPlugins } from "./catalog";
import { fieldOf } from "./lens";

beforeEach(() => resetStore());

const served = () => ({
  version: "v1", views: [{ id: "all", order: 1, surfaces: ["web"] }], facets: [{ id: "where", label: "Where" }],
  situations: [{ provider: "acme", kind: "review", label: "Review", rank: 1 }], events: [{ kind: "commented", label: "commented", tone: "loop" }],
});

test("a catalog read that changed nothing keeps every value the page derives from", () => {
  applyCatalog(served(), fieldOf);
  const before = { views: catalog.views, fields: catalog.fields, kinds: catalog.kinds, events: catalog.events };
  applyCatalog(served(), fieldOf);
  expect(catalog.views).toBe(before.views);
  expect(catalog.fields).toBe(before.fields);
  expect(catalog.kinds).toBe(before.kinds);
  expect(catalog.events).toBe(before.events);
  applyCatalog({ ...served(), facets: [{ id: "who", label: "Who" }] }, fieldOf);
  expect(catalog.fields.map((f) => f.id)).toEqual(["who"]);
  expect(catalog.views).toBe(before.views);
});

test("a plugins read that changed nothing keeps the vocabulary, and a change replaces it", () => {
  const list = [{ id: "acme", name: "Acme", enabled: true, reacts: true }];
  applyPlugins(list);
  const before = { names: plugins.names, reacting: plugins.reacting };
  applyPlugins(list);
  expect(plugins.names).toBe(before.names);
  expect(plugins.reacting).toBe(before.reacting);
  applyPlugins([{ id: "acme", name: "Acme", enabled: true }]);
  expect(plugins.reacting.has("acme")).toBe(false);
});
