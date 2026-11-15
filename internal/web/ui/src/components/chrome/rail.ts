import { coreViews, EXPLORE, HOME, PLAN, providerName, RECENT } from "../../lib/catalog";
import { t } from "../../lib/i18n.svelte";
import { normValue, passes, toggledQualifier, type Lens } from "../../lib/lens";
import type { J } from "../../lib/util";
import { viewCounters } from "../../state/loaders.svelte";
import { hashString, readHash, type Hash } from "../../state/route.svelte";
import { catalog, plugins, state, tasksState } from "../../state/store.svelte";
import { waitingWho } from "./facts";
import { planCount } from "./plan";

export interface NavEntry {
  id: string;
  label: string;
  summary?: string;
  provider?: string;
  count: number | "";
  core: boolean;
  offRail?: boolean;
}

// viewNav is the rail: the core views with their counters, the plan and Recent, then each plugin view (count left
// to the caller).
export function viewNav(cat: J, served: J[], plugin: string[], counters: J): NavEntry[] {
  const views = (counters && counters.views) || {};
  const out: NavEntry[] = coreViews(cat).map((v: J) => ({
    id: v.id, label: v.label, summary: v.summary || "", count: views[v.id] === undefined ? "" : views[v.id], core: true, offRail: v.rail === false,
  }));
  out.push({ id: PLAN, label: t("Plan"), summary: t("What the workflows will do, and what waits for you to apply it"), count: "", core: true });
  out.push({ id: RECENT, label: t("Recent"), summary: t("The newest events of every unit the loop follows"), count: "", core: true });
  const taken = new Set(out.map((v) => v.id));
  for (const v of served) {
    if (taken.has(v.id)) continue;
    taken.add(v.id);
    out.push({ id: v.id, label: v.label, provider: v.provider || "", count: "", core: false });
  }
  for (const id of plugin) {
    if (!taken.has(id)) out.push({ id, label: id, provider: (plugins.views.get(id) || { plugin: "" }).plugin, count: "", core: false });
  }
  return out;
}

export interface RailSection {
  title: string;
  views: NavEntry[];
}

// railSections splits viewNav's list: the core views untitled, then one section per provider, titled by
// name(provider), in the order they first appear.
export function railSections(nav: NavEntry[], name: (provider: string) => string): RailSection[] {
  const out: RailSection[] = [{ title: "", views: nav.filter((v) => v.core) }];
  const by = new Map<string, RailSection>();
  for (const v of nav.filter((x) => !x.core)) {
    const p = v.provider || "";
    if (!by.has(p)) {
      const sec = { title: p ? name(p) : t("Plugins"), views: [] as NavEntry[] };
      by.set(p, sec);
      out.push(sec);
    }
    (by.get(p) as RailSection).views.push(v);
  }
  return out.filter((sec) => sec.views.length);
}

// lensHash is route's hash with change over it, keeping its lens.
export function lensHash(change: Partial<Hash>, route: Hash = readHash()): string {
  const { q, g, s, layout, view, area } = route;
  return hashString({ view, q, g, s, layout, area, ...change });
}

// railCount is what the rail counts of the entry v under lens: a core view's counter, the plan's steps, the rows
// of a row view the lens keeps; "" for a view it does not count.
export function railCount(v: NavEntry, lens: Lens): number | "" {
  if (v.id === PLAN) return planCount();
  if (v.core) return v.count;
  const rows = catalog.views.some((c) => c.id === v.id) && v.id !== EXPLORE;
  return rows ? state.rows.filter((r) => passes(r, v.id, lens)).length : "";
}

// railNav is viewNav's list for lens, each entry with what the rail counts of it.
export function railNav(lens: Lens, route: Hash = readHash()): NavEntry[] {
  return viewNav(tasksState.catalog, catalog.views, [...plugins.views.keys()], viewCounters(route)).map((v) => ({ ...v, count: railCount(v, lens) }));
}

// railFolds says whether the rail folds the entry v into its Empty entry: it counts nothing and is not the view
// shown.
export const railFolds = (v: NavEntry, current: string): boolean => v.count === 0 && v.id !== current;

export interface RailItem {
  id: string;
  label: string;
  count: number | string;
  on: boolean;
  hash: string;
  cls: string;
  tip?: string;
}

export function railItem(v: NavEntry, route: Hash): RailItem {
  return {
    id: v.id, label: v.label, count: v.count, on: v.id === route.view, hash: lensHash({ view: v.id, task: "", pr: "" }, route),
    cls: (v.id === "stalled" ? "late-count " : "") + (v.core ? "core" : "plugin"), tip: v.summary,
  };
}

export function homeItem(route: Hash): RailItem {
  return { id: HOME, label: t("Home"), count: "", on: route.view === HOME, hash: "view=" + HOME, cls: "core", tip: t("What waits on you, and what stalled the longest") };
}

// railLayout are the rail's entries: each section's views under its provider's name, but the empty ones, which
// one Empty entry counts and lists in place when opened, and the views the profile keeps off the rail, unless one
// is the view shown.
export function railLayout(all: NavEntry[], route: Hash): { sections: { title: string; items: RailItem[] }[]; empties: RailItem[] } {
  const nav = all.filter((v) => !v.offRail || v.id === route.view);
  const empties = nav.filter((v) => railFolds(v, route.view)).map((v) => railItem(v, route));
  const sections = railSections(nav.filter((v) => !railFolds(v, route.view)), providerName).map((sec) => ({ title: sec.title, items: sec.views.map((v) => railItem(v, route)) }));
  return { sections, empties };
}

// railPeople are the people the rows of lens wait on, most rows first, each a link that filters by them.
export function railPeople(lens: Lens, route: Hash): RailItem[] {
  const counts = new Map<string, number>();
  for (const r of state.rows) {
    if (!passes(r, lens.view, lens, "waits-on")) continue;
    for (const w of waitingWho(r)) counts.set(w, (counts.get(w) || 0) + 1);
  }
  const people = [...counts.entries()].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]));
  return people.map(([who, n]) => ({
    id: who, label: who, count: n, cls: "",
    on: lens.quals.some((x) => x.field === "waits-on" && !x.neg && x.value === normValue("waits-on", who)),
    hash: lensHash({ q: toggledQualifier(lens.q, "waits-on", who, false) }, route),
  }));
}

// railTipID is the id of the tooltip of the entry under hash.
export const railTipID = (hash: string): string => "rail-tip-" + hash.replace(/[^A-Za-z0-9_-]/g, "-");
