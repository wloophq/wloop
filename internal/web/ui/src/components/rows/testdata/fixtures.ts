import { readFileSync } from "node:fs";
import { applyCatalog } from "../../../lib/catalog";
import { fieldOf } from "../../../lib/lens";
import type { J } from "../../../lib/util";
import { fixtures as chrome } from "../../../testing/chrome";
import { goFixtures } from "../../../testing/fixtures";
import { state, tasksState } from "../../../state/store.svelte";

// served are the fixtures the Go side keeps current (internal/web/testdata/cockpit.fixtures.json and
// cockpit.chrome.json), by the name the row tests read them under; the rest are this directory's own.
const served: Record<string, () => J> = {
  "catalog.bundled.json": () => goFixtures().bundled,
  "catalog.github.json": () => chrome.catalog,
  "lens-rows.json": () => goFixtures().lensRows,
  "mixed-tasks.json": () => chrome.mixedTasks,
  "slack-facets.json": () => chrome.slackFacets,
  "tag-rows.json": () => goFixtures().tagRows,
  "views.json": () => goFixtures().views,
};

export function fixture(name: string): J {
  if (served[name]) return structuredClone(served[name]());
  return JSON.parse(readFileSync(new URL("./" + name, import.meta.url), "utf8"));
}

// useCatalogs applies a units catalog (GET /api/catalog) and the task views' catalog (GET /api/views).
export function useCatalogs(cat: J = fixture("catalog.github.json"), views: J = fixture("views.json")): void {
  applyCatalog(cat, fieldOf);
  tasksState.catalog = views;
  state.health = { viewer: "me" };
  state.specs = { version: "", byId: {} };
}

export function task(id: string, st: string, mod?: (e: J) => void): J {
  const e: J = { id, state: st, title: id, units: [], verbs: [], created: "2026-10-01T09:00:00Z", updated: "2026-10-01T09:00:00Z" };
  if (mod) mod(e);
  return e;
}

// childClasses are the first class of each child element of n, a facet button read as the piece it wraps.
export function childClasses(n: Element | null): string[] {
  const cls = (c: Element): string => (c.classList.contains("facet") && c.firstElementChild ? cls(c.firstElementChild) : c.classList[0] || c.tagName.toLowerCase());
  return n ? Array.from(n.children).map(cls) : [];
}
