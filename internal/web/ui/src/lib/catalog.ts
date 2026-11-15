import { catalog, plugins, state, tasksState, type Field } from "../state/store.svelte";
import { DASH, EXPLORE, HOME, PLAN, QUEUE, RECENT } from "./routes";
import { unitOf, type J } from "./util";

// REVIEW is the route and the key context of the review, a mode of the cockpit that walks the catalog's review
// views.
export const REVIEW = "review";

// MATCH is how a facet's match compares a row's value v with a query q.
export const MATCH: Record<string, (v: string, q: string) => boolean> = {
  suffix: (v, q) => v === q || v.endsWith("/" + q),
  prefix: (v, q) => v === q || v.startsWith(q + ":"),
  team: (v, q) => v === q || v === "team:" + q,
};

// changed says whether next differs from what the store holds as cur, by value.
function changed(cur: unknown, next: unknown): boolean {
  const plain = (v: unknown) => (v instanceof Map || v instanceof Set ? [...v] : v);
  return JSON.stringify(plain(cur)) !== JSON.stringify(plain(next));
}

// keep stores next in obj[key] only when it differs from what obj[key] holds, so a read that changed nothing leaves
// every value, and whatever derives from it, as it was.
function keep<T extends object, K extends keyof T>(obj: T, key: K, next: T[K]): void {
  if (changed(obj[key], next)) obj[key] = next;
}

export function applyCatalog(c: J, fieldOf: (spec: J) => Field): void {
  catalog.version = c.version || "";
  keep(catalog, "views", (c.views || []).filter((v: J) => (v.surfaces || []).includes("web")).sort((a: J, b: J) => a.order - b.order));
  const sits = [...(c.situations || [])].sort((a: J, b: J) => a.rank - b.rank);
  keep(catalog, "kinds", new Map(sits.map((x: J) => [`${x.provider}:${x.kind}`, x.label])));
  keep(catalog, "kindOrder", [...new Set(sits.map((x: J) => x.kind as string))]);
  if (changed(catalog.specs, c.facets || []) || catalog.fields.length !== (c.facets || []).length) {
    catalog.specs = c.facets || [];
    catalog.fields = catalog.specs.map(fieldOf);
  }
  keep(catalog, "lens", c.lens || []);
  keep(catalog, "reasons", c.reasons || []);
  keep(catalog, "cardSignals", c.card_signals || []);
  keep(catalog, "events", new Map((c.events || []).map((e: J) => [e.kind, e.label])));
  keep(catalog, "eventData", new Map((c.events || []).filter((e: J) => e.data).map((e: J) => [e.kind, e.data])));
  keep(catalog, "eventTones", new Map((c.events || []).filter((e: J) => e.tone).map((e: J) => [e.kind, e.tone])));
  keep(catalog, "details", c.details || []);
  const searching = (c.views || []).find((v: J) => v.search) || {};
  keep(catalog, "search", searching.search || null);
  catalog.searchProvider = searching.provider || "";
}

// applyPlugins keeps the enabled plugins' vocabulary GET /api/plugins lists.
export function applyPlugins(list: J[]): void {
  const views = new Map<string, { plugin: string; module: string }>();
  const reacting = new Set<string>();
  const names = new Map(plugins.names);
  for (const p of list || []) {
    if (p.name) names.set(p.id, p.name);
    if (p.enabled && p.reacts) reacting.add(p.id);
    const web = p.enabled && p.ui && p.ui.web;
    if (!web || !web.module) continue;
    for (const id of web.views || []) views.set(id, { plugin: p.id, module: web.module });
  }
  keep(plugins, "views", views);
  keep(plugins, "reacting", reacting);
  keep(plugins, "names", names);
}

// providerName is a provider's service as a person reads it, its id when its plugin names none.
export function providerName(id: string): string {
  return plugins.names.get(id) || id;
}

export function providerReacts(id: string): boolean {
  return plugins.reacting.has(id);
}

export function isPluginView(id: string): boolean {
  return plugins.views.has(id);
}

// searchService is the service the served search runs on, as its plugin names it.
export function searchService(): string {
  return providerName(catalog.searchProvider || "");
}

// exploreSearch is the served Explore search: its presets, filters, sorts and the review decisions' words.
export function exploreSearch(): J {
  return catalog.search || { presets: [], filters: [], sorts: [], decisions: {}, no_decision: "", default_sort: "" };
}

export function reviewDecision(decision: string): string {
  const s = exploreSearch();
  return (s.decisions || {})[decision] || s.no_decision || "";
}

// eventLabel is an event kind in a few words, as the catalog serves it: a plugin's kinds and the core's.
export function eventLabel(kind: string): string {
  return catalog.events.get(kind) || kind.replaceAll("_", " ");
}

// eventTone is the tone the catalog draws an event kind in, "" for none.
export function eventTone(kind: string): string {
  return catalog.eventTones.get(kind) || "";
}

// loopEvent says whether an event kind is the loop's doing: its tone is the loop's.
export function loopEvent(kind: string): boolean {
  return eventTone(kind) === "loop";
}

export function kindLabel(kind: string, provider: string): string {
  const own = catalog.kinds.get(`${provider}:${kind}`);
  if (own) return own;
  for (const [k, label] of catalog.kinds) if (k.slice(k.indexOf(":") + 1) === kind) return label;
  return kind;
}

export function rowKindLabel(r: J): string {
  return kindLabel(r.kind, unitOf(r).provider);
}

// EVENT_DATA shows an event's data the way its plugin's catalog says: whole by default, a field or a short sha,
// or nothing when it names whom a review was requested from, which the row shows on its own.
const EVENT_DATA: Record<string, (d: string) => string> = {
  none: () => "",
  first_field: (d) => d.split(":")[0],
  short_sha: (d) => d.slice(0, 7),
  requested: () => "",
};

export function eventData(e: J): string {
  if (!e.data) return "";
  const show = EVENT_DATA[catalog.eventData.get(e.kind) || ""];
  return show ? show(e.data) : e.data;
}

export function coreViews(cat: J = tasksState.catalog): J[] {
  return (cat && cat.views) || [];
}

export function isCoreView(id: string, cat: J = tasksState.catalog): boolean {
  return id === REVIEW || coreViews(cat).some((v) => v.id === id);
}

export function viewSpec(id: string, cat: J = tasksState.catalog): J {
  return coreViews(cat).find((v) => v.id === id) || null;
}

// viewLabel is the profile's word for a core view.
export function viewLabel(view: string): string {
  return (coreViews().find((v) => v.id === view) || {}).label || view;
}

// resolveView is the view a route shows: a core view, a served plugin view or a plugin's own web view; the
// catalog's first view otherwise. Before anything is loaded every view stands.
export function resolveView(view: string, cat: J, served: J[], plugin: (id: string) => boolean): string {
  if (view === HOME || view === QUEUE || view === PLAN || view === RECENT) return view;
  if (view && ((!cat && !served.length) || isCoreView(view, cat) || served.some((v) => v.id === view) || plugin(view))) return view;
  if (!cat && served.length) return served[0].id;
  return (coreViews(cat)[0] || served[0] || { id: "" }).id;
}

// servedView is the plugin view the catalog serves by id.
export function servedView(id: string): J {
  return catalog.views.find((v) => v.id === id) || null;
}

// listless says whether view lists no rows of its own under the list box.
export function listless(view: string): boolean {
  return view === RECENT || view === EXPLORE || view === PLAN || view === HOME || view === QUEUE || isPluginView(view) || isCoreView(view);
}

export { DASH, EXPLORE, HOME, PLAN, QUEUE, RECENT };

// roleProp is the catalog's property the role acts on (area, contexts, star, deadline); null when none does.
export function roleProp(role: string, cat: J = tasksState.catalog): J {
  return ((cat && cat.props) || []).find((p: J) => p.role === role) || null;
}

// roleValue is e's value of the property the role acts on; undefined when none does.
export function roleValue(e: J, role: string): J {
  const p = roleProp(role);
  return p ? (e.props || {})[p.name] : undefined;
}

// starred says whether e holds the first value, starred, of the property the star role acts on.
export function starred(e: J): boolean {
  const p = roleProp("star");
  return !!p && (p.values || []).length > 0 && (e.props || {})[p.name] === p.values[0];
}

// specsStale says whether rows were drawn with action specs other than those loaded.
export function specsStale(rows: J[]): boolean {
  return !state.specs.version || rows.some((r) => r.specs_version && r.specs_version !== state.specs.version);
}
