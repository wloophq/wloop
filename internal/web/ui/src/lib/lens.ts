import { clock, minuteNow } from "../state/clock.svelte";
import { catalog, state, tasksState, type Field } from "../state/store.svelte";
import type { Hash } from "../state/route.svelte";
import { MATCH, isCoreView, viewSpec, rowKindLabel, eventLabel, REVIEW } from "./catalog";
import { fmtDate, pluralForm, t } from "./i18n.svelte";
import { DASH, EXPLORE, PLAN, RECENT } from "./routes";
import { cmp, fold, goDuration, isSet, keyOf, labelOf, rfc3339, tokens, unitOf, unquote, type J } from "./util";

// The lens: every list's one path from what the daemon serves to what it shows. A query filters by free words
// and by field:value qualifiers, a sort orders, a group splits; an item is { facets, texts } or a unit row.

export const pl = (r: J): J => r.payload || {};
export const sm = (r: J): J => r.summary || {};

// fieldOf is a served facet as the lens reads it. A field filters by values, which may be several, and groups and
// sorts by its first value, so a row sits in one group. A time facet's values are its buckets now.
export function fieldOf(spec: J): Field {
  const f: Field = {
    id: spec.id, label: spec.label, provider: spec.provider || "", fact: spec.fact || "", group: !!spec.group, sort: !!spec.sort && spec.menu !== false,
    menu: spec.menu !== false, rank: spec.rank, empty: spec.empty, match: MATCH[spec.match], buckets: spec.buckets && spec.buckets.length ? spec.buckets : null,
  };
  if (spec.labels) f.show = (k) => spec.labels[k] || k;
  if (spec.match === "prefix") {
    f.groupBy = (raw) => "tag:" + raw.split(":")[0];
    f.keys = (it) => facetValues(it, spec.id);
  }
  if (spec.sort === "time") f.sortValue = (r) => facetTime(r, spec.id);
  f.values = f.buckets ? (r) => bucketsOf(f, r) : (r) => facetValues(r, spec.id);
  if (spec.id === "is") f.values = (r) => (dueNow(r) ? [...facetValues(r, "is"), "due"] : facetValues(r, "is"));
  return f;
}

export function facetValues(r: J, id: string): string[] {
  return ((r.facets || {})[id] || []).filter(Boolean);
}

// bucketsOf are a time facet's values now: each time's bucket, "none" without a time.
function bucketsOf(f: Field, r: J): string[] {
  const vs = facetValues(r, f.id).map((v) => bucketOf(f.buckets as J[], v)).filter(Boolean);
  return vs.length ? vs : ["none"];
}

function facetTime(r: J, id: string): number {
  const ms = Date.parse(facetValues(r, id)[0] || "");
  return Number.isNaN(ms) ? -Infinity : ms;
}

// bucketOf is the first bucket whose within is above now - t, the one without within catching the rest; ""
// when no bucket takes iso.
export function bucketOf(buckets: J[], iso: string, now = minuteNow()): string {
  const at = Date.parse(iso);
  if (Number.isNaN(at)) return "";
  const delta = now - at;
  const b = buckets.find((x) => !x.within || delta < goDuration(x.within));
  return b ? b.id : "";
}

// dueNow is the flag "due": an upcoming instant of the row's facet due falls in its "due" bucket now.
export function dueNow(r: J, now = minuteNow()): boolean {
  const f = fieldById("due");
  return !!f && !!f.buckets && facetValues(r, "due").some((iso) => bucketOf(f.buckets as J[], iso, now) === "due");
}

export function fieldById(id: string, fields: Field[] = catalog.fields): Field | undefined {
  return fields.find((f) => f.id === id);
}

// lensWords are what the lens bar says, from the views catalog.
export function lensWords(): J {
  return (tasksState.catalog && tasksState.catalog.lens) || {};
}

// fillWord fills a lens word's {{.name}} with vars, and its {{plural .name "one" "…" "other" "…"}} with the form
// of vars[name] in the cockpit's locale.
export function fillWord(word: unknown, vars: Record<string, unknown>): string {
  return String(word || "")
    .replace(/\{\{\s*plural\s+\.(\w+)((?:\s+"[^"]*")+)\s*\}\}/g, (_, k: string, pairs: string) => {
      const words = [...pairs.matchAll(/"([^"]*)"/g)].map((m) => m[1]);
      const forms: Record<string, string> = {};
      for (let i = 0; i + 1 < words.length; i += 2) forms[words[i]] = words[i + 1];
      return forms[pluralForm(Number(vars[k]) || 0)] ?? forms.other ?? "";
    })
    .replace(/\{\{\s*\.(\w+)\s*\}\}/g, (_, k: string) => String(vars[k] ?? ""));
}

// tagKeyField groups by one brief tag key, "tag:lang": a row sits under each of its tags of that key, or under
// "no lang" with none. Its group keys are whole tags, so no value, "none" included, reads as the empty group.
export function tagKeyField(id: string, fields: Field[] = catalog.fields): Field | null {
  const m = /^tag:([a-z-]+)$/.exec(id || "");
  const tag = fieldById("tag", fields);
  if (!m || !tag || tag.match !== MATCH.prefix) return null;
  const key = m[1];
  const w = lensWords();
  return {
    id, label: fillWord(w.tag_key, { label: tag.label, key }), empty: fillWord(w.tag_key_empty, { key }), group: true, tagKey: key,
    keys: (r) => facetValues(r, tag.id).filter((v) => v.startsWith(key + ":")),
  };
}

export function groupField(id: string, fields: Field[] = catalog.fields): Field | null {
  return fieldById(id, fields) || tagKeyField(id, fields);
}

export function tagKeys(rows: J[]): string[] {
  const keys = new Set<string>();
  for (const r of rows) {
    for (const tag of facetValues(r, "tag")) {
      const at = tag.indexOf(":");
      if (at > 0) keys.add(tag.slice(0, at));
    }
  }
  return [...keys].filter((k) => /^[a-z-]+$/.test(k)).sort();
}

export function normValue(_field: string, raw: unknown): string {
  const v = fold(unquote(String(raw || "")));
  return v === "@me" ? "me" : v;
}

export interface Qualifier {
  field: string;
  value: string;
  raw: string;
  neg: boolean;
}

// qualifierOf reads a field:value term of a field that filters; anything else is free text, so "fix:" or a URL
// stays a word.
export function qualifierOf(token: string, fields: Field[] = catalog.fields): Qualifier | null {
  const m = /^(-?)([a-zA-Z][a-zA-Z.-]*):(.+)$/.exec(token);
  const f = m && fieldById(m[2].toLowerCase(), fields);
  if (!m || !f || !f.values) return null;
  const value = normValue(f.id, m[3]);
  return value ? { field: f.id, value, raw: unquote(m[3]), neg: m[1] === "-" } : null;
}

export function qualifiers(q: string, fields: Field[] = catalog.fields): Qualifier[] {
  return tokens(q).map((tok) => qualifierOf(tok, fields)).filter((x): x is Qualifier => !!x);
}

// terms are the free words of a query over fields, the text every field is searched for.
export function terms(q: string, fields: Field[] = catalog.fields): string[] {
  return tokens(q).filter((tok) => !qualifierOf(tok, fields)).map((tok) => fold(unquote(tok))).filter(Boolean);
}

export function qualifierToken(field: string, raw: string, neg: boolean): string {
  return `${neg ? "-" : ""}${field}:${/\s/.test(raw) ? `"${raw}"` : raw}`;
}

// toggledQualifier adds field:value, or -field:value when neg, or takes it out when the query holds it already;
// the opposite sign gives way.
export function toggledQualifier(q: string, field: string, raw: string, neg: boolean, fields: Field[] = catalog.fields): string {
  const v = normValue(field, raw);
  let on = false;
  const kept = tokens(q).filter((tok) => {
    const x = qualifierOf(tok, fields);
    if (!x || x.field !== field || x.value !== v) return true;
    if (x.neg === !!neg) on = true;
    return false;
  });
  return (on ? kept : [...kept, qualifierToken(field, raw, neg)]).join(" ");
}

export function withoutQualifiers(q: string, field: string, neg: boolean, fields: Field[] = catalog.fields): string {
  return tokens(q).filter((tok) => {
    const x = qualifierOf(tok, fields);
    return !x || x.field !== field || x.neg !== neg;
  }).join(" ");
}

// reasonText is why row r waits, in its plugin's words of the reason it names filled with the reason's values,
// else as the plugin wrote it.
export function reasonText(r: J): string {
  const s = r.summary || {};
  const words = ((catalog.reasons || []).find((w) => w.provider === unitOf(r).provider) || {}).words || {};
  const word = s.reason_key ? words[s.reason_key] : "";
  return word ? fillWord(word, s.reason_params || {}) : s.reason || "";
}

// rowWords are the row words of a provider's rows: the profile's, the plugin's own over them.
export function rowWords(provider: string): J {
  const own = provider ? ((catalog.lens || []).find((w) => w.provider === provider) || {}).words : null;
  return own ? { ...lensWords(), ...own } : lensWords();
}

// rowTexts are the texts a free word searches in a row.
export function rowTexts(r: J): string[] {
  return [r.title, labelOf(r), r.link, r.kind, rowKindLabel(r), sm(r).waits_on, sm(r).reason, reasonText(r), sm(r).line,
    ...(r.search || []), ...Object.values(r.facets || {}).flat()] as string[];
}

// The caches below hold what an item's served data gives, so they start over whenever a sync writes into the
// items (reconcile keeps their identity) and, for time buckets, every minute.
const cache = { key: "", folded: new WeakMap<object, string[]>(), values: new WeakMap<object, Map<string, string[]>>() };

function cacheKey(): string {
  return `${state.rev}:${state.epoch}:${tasksState.rev}:${clock.minute}:${catalog.version}`;
}

function caches() {
  const key = cacheKey();
  if (cache.key !== key) Object.assign(cache, { key, folded: new WeakMap(), values: new WeakMap() });
  return cache;
}

// haystack is an item's folded texts: its own, or a row's.
export function haystack(it: J): string[] {
  const c = caches();
  let f = c.folded.get(it);
  if (!f) {
    f = ((it.texts as string[]) || rowTexts(it)).map(fold);
    c.folded.set(it, f);
  }
  return f;
}

export function matches(it: J, ts: string[]): boolean {
  return ts.every((term) => haystack(it).some((f) => f.includes(term)));
}

export const eventRef = (it: J): string => it.label || "";

function matchesActivity(it: J, ts: string[]): boolean {
  const e = it.event;
  const fields = [it.title, eventRef(it), it.author, e.actor, eventLabel(e.kind), e.data].map(fold);
  return ts.every((term) => fields.some((f) => f.includes(term)));
}

// Recent applies the filters an event can answer: its repository and the author of its pull request.
const ACTIVITY_FIELDS = ["where", "author"];

export function activityPasses(it: J, lens: Lens): boolean {
  const filters = new Map([...lens.filters].filter(([id]) => ACTIVITY_FIELDS.includes(id)));
  return matchesActivity(it, lens.ts) && passesFilters({ facets: { where: activityRepos(it), author: [it.author || ""] } }, filters);
}

function activityRepos(it: J): string[] {
  const m = /^(\S+\/\S+)#\d+$/.exec(it.label || "");
  return m ? [m[1]] : [];
}

// foldMap folds s and maps each folded position back to s, so a match in the folded text marks the right
// characters of the original.
export function foldMap(s: string): { out: string; at: number[] } {
  let out = "";
  const at: number[] = [];
  for (let i = 0; i < s.length;) {
    const ch = String.fromCodePoint(s.codePointAt(i) as number);
    const f = fold(ch);
    for (let k = 0; k < f.length; k++) at.push(i);
    out += f;
    i += ch.length;
  }
  at.push(s.length);
  return { out, at };
}

export interface Span {
  text: string;
  hit: boolean;
}

// markedSpans splits text into the runs a search's terms ts hit and the runs between, for <mark>.
export function markedSpans(text: unknown, ts: string[]): Span[] {
  const s = String(text ?? "");
  if (!ts.length || !s) return s ? [{ text: s, hit: false }] : [];
  const { out, at } = foldMap(s);
  const hits: [number, number][] = [];
  for (const term of ts) {
    for (let i = out.indexOf(term); i >= 0; i = out.indexOf(term, i + 1)) hits.push([at[i], at[i + term.length]]);
  }
  if (!hits.length) return [{ text: s, hit: false }];
  hits.sort((a, b) => a[0] - b[0]);
  const spans: [number, number][] = [];
  for (const [a, b] of hits) {
    const last = spans[spans.length - 1];
    if (last && a <= last[1]) last[1] = Math.max(last[1], b);
    else spans.push([a, b]);
  }
  const parts: Span[] = [];
  let pos = 0;
  for (const [a, b] of spans) {
    if (a > pos) parts.push({ text: s.slice(pos, a), hit: false });
    parts.push({ text: s.slice(a, b), hit: true });
    pos = b;
  }
  if (pos < s.length) parts.push({ text: s.slice(pos), hit: false });
  return parts;
}

// itemValues are an item's values of a field, folded, the way filters compare.
export function itemValues(f: Field, it: J): string[] {
  const c = caches();
  let m = c.values.get(it);
  if (!m) c.values.set(it, (m = new Map()));
  if (!m.has(f.id)) m.set(f.id, (f.values ? f.values(it) : []).filter(Boolean).map((v) => fold(v)));
  return m.get(f.id) as string[];
}

function itemKey(f: Field, it: J): string {
  return (f.key ? f.key(it) : ((f.values && f.values(it)) || [])[0]) || "none";
}

export interface Filter {
  f: Field | undefined;
  inc: string[];
  exc: string[];
}

export function filtersOf(quals: Qualifier[], fields: Field[] = catalog.fields): Map<string, Filter> {
  const out = new Map<string, Filter>();
  for (const x of quals) {
    if (!out.has(x.field)) out.set(x.field, { f: fieldById(x.field, fields), inc: [], exc: [] });
    (out.get(x.field) as Filter)[x.neg ? "exc" : "inc"].push(x.value);
  }
  return out;
}

// passesFilters ORs the values of one field and ANDs the fields; except leaves one field out, to count what
// picking another of its values shows.
export function passesFilters(it: J, filters: Map<string, Filter>, except?: string | string[]): boolean {
  for (const [id, { f: own, inc, exc }] of filters) {
    if (id === except || (Array.isArray(except) && except.includes(id))) continue;
    const f = own || fieldById(id);
    if (!f) continue;
    const vs = itemValues(f, it);
    const hit = (q: string) => vs.some((v) => (f.match ? f.match(v, q) : v === q));
    if ((inc.length && !inc.some(hit)) || exc.some(hit)) return false;
  }
  return true;
}

export function isMuted(p: J, now = minuteNow()): boolean {
  const m = p.mute;
  return !!m && (m.until_state_change || (isSet(m.until) && Date.parse(m.until) > now));
}

// The backlog is every pull request still open; done joins it on request.
export function inView(row: J, view: string, withDone?: boolean): boolean {
  if (view === RECENT || view === EXPLORE || view === PLAN) return false;
  if (view === DASH) return (row.unit || {}).provider === (catalog.views.find((v) => v.id === view) || {}).provider && (withDone || !sm(row).closed);
  return view === "all" || (row.views || []).includes(view);
}

export function passes(r: J, view: string, lens: Lens, except?: string | string[]): boolean {
  return inView(r, view, lens.done) && matches(r, lens.ts) && passesFilters(r, lens.filters, except);
}

export function parseSort(s: string, fields: Field[] = catalog.fields): { field: string; desc: boolean } {
  const m = /^([a-z][a-z.-]*?)(-desc)?$/.exec(s || "");
  const f = m && fieldById(m[1], fields);
  return m && f && f.sort ? { field: f.id, desc: !!m[2] } : { field: "", desc: false };
}

// NO_GROUP is the pick that lists a view whose lens groups by default in one list.
export const NO_GROUP = "none";

const lensMemo: { fields: Field[] | null; viewsCat: J; tasks: Field[] | null; offered: { key: unknown[]; fields: Field[] } | null } = { fields: null, viewsCat: null, tasks: null, offered: null };

// lensFields are the fields a query over view names: a task view's are the profile's task facets and the
// plugins' facets, one by id as the daemon counts with them; a row view's are the plugins'.
export function lensFields(view: string): Field[] {
  if (view === PLAN) return [];
  if (!isCoreView(view)) return catalog.fields;
  const cat = tasksState.catalog;
  if (lensMemo.fields !== catalog.fields || lensMemo.viewsCat !== cat) {
    const byId = new Map(catalog.fields.map((f) => [f.id, f]));
    for (const spec of (cat && cat.facets) || []) byId.set(spec.id, fieldOf(spec));
    Object.assign(lensMemo, { fields: catalog.fields, viewsCat: cat, tasks: [...byId.values()], offered: null });
  }
  return lensMemo.tasks as Field[];
}

// fieldsFor are the fields of view's lens that some of its items has a value of, and a field per brief tag key
// they hold.
export function fieldsFor(view: string, items: J[]): Field[] {
  const has = new Set<string>();
  for (const it of items) for (const [id, vs] of Object.entries(it.facets || {})) if (((vs as string[]) || []).some(Boolean)) has.add(id);
  return [...lensFields(view).filter((f) => has.has(f.id)), ...tagKeys(items).map((k) => tagKeyField("tag:" + k, lensFields(view))).filter((f): f is Field => !!f)];
}

export const viewQueryKey = (view: string, area?: string, ctx?: string[]): string => [view, area || "", (ctx || []).join(",")].join("|");

// viewSource is what view lists before the lens, as loaded: a task view's tasks, a row view's rows, which items
// keeps to the view; null until they are loaded, or for a view the lens does not list.
export function viewSource(view: string, route: Pick<Hash, "area" | "ctx" | "done">): { all: J[]; items: () => J[] } | null {
  if (isCoreView(view)) {
    const loaded = view !== REVIEW && tasksState.key === viewQueryKey(view, route.area, route.ctx);
    return loaded ? { all: tasksState.tasks, items: () => tasksState.tasks } : null;
  }
  if (!catalog.views.some((v) => v.id === view) || !state.loaded) return null;
  return { all: state.rows, items: () => state.rows.filter((r) => inView(r, view, route.done)) };
}

// offeredFields are what the menus of view's lens offer: the fields its items have, every field of its lens
// until they are loaded.
export function offeredFields(view: string, route: Pick<Hash, "area" | "ctx" | "done">): Field[] {
  const src = viewSource(view, route);
  if (!src) return lensFields(view);
  const key = [view, route.done, src.all, lensFields(view), cacheKey()];
  const m = lensMemo.offered;
  if (m && m.key.every((k, i) => k === key[i])) return m.fields;
  const fields = fieldsFor(view, src.items());
  lensMemo.offered = { key, fields };
  return fields;
}

// viewGroupField is the field a task view groups by unless the user picks another: the facet reading its group
// fact.
export function viewGroupField(view: string): string {
  const spec = viewSpec(view);
  if (!spec || !spec.group) return "";
  return (lensFields(view).find((f) => f.fact === spec.group) || ({} as Field)).id || "";
}

// defaultSortLabel is what the sort menu calls view's own order.
export function defaultSortLabel(view: string): string {
  if (isCoreView(view)) return (tasksState.catalog || {}).default_sort || "";
  return (catalog.views.find((v) => v.id === view) || {}).default_sort || (tasksState.catalog || {}).default_sort || "";
}

// specFor mirrors api.FacetSpec.For: a merged spec with provider's display keys.
export function specFor(spec: J, provider: string): J {
  const d = (spec.displays || {})[provider];
  if (!d) return spec;
  return { ...spec, show: d.show || "", show_only: d.show_only, show_values: d.show_values, label_prefix: d.label_prefix, row_class: d.row_class,
    show_when: d.show_when, show_max: d.show_max, show_label: d.show_label,
    strip: !!d.strip, colors: d.colors,
    buckets: d.bucket_labels ? (spec.buckets || []).map((b: J) => (b.id in d.bucket_labels ? { ...b, label: d.bucket_labels[b.id] } : b)) : spec.buckets,
    labels: d.labels && Object.keys(d.labels).length ? d.labels : spec.labels };
}

// stripSpec is the facet that colors the strip of provider's items among specs ("" for a task's), as that
// provider shows it; null when the provider has none.
export function stripSpec(specs: J[], provider: string): J {
  for (const s of specs || []) {
    if (s.provider ? s.provider !== provider : s.displays && !(provider in s.displays)) continue;
    const spec = specFor(s, provider);
    if (spec.strip) return spec;
  }
  return null;
}

// stripColor is the hue a strip facet's value colors a strip with, its colors' keys matched folded as group keys
// are.
export function stripColor(spec: J, value: string): string {
  const v = fold(value);
  const hit = Object.entries((spec && spec.colors) || {}).find(([k]) => fold(k) === v);
  return hit ? (hit[1] as string) : "nobody";
}

export interface Strip {
  spec: J;
  value: string;
  color: string;
  label: string;
}

// stripOf is an item's strip: its provider's strip facet, the item's first value of it, that value's hue and
// label; null when the provider has no strip or the item no value of it.
export function stripOf(it: J, specs: J[]): Strip | null {
  const spec = stripSpec(specs, it.provider || "");
  const value = spec ? ((it.facets || {})[spec.id] || []).find((v: string) => v && fold(v) !== "none") : "";
  return value ? { spec, value, color: stripColor(spec, value), label: (spec.labels || {})[value] || value } : null;
}

export function rowStrip(row: J): Strip | null {
  return stripOf({ facets: row.facets || {}, provider: unitOf(row).provider }, catalog.specs);
}

// waitClass is the class of a row's strip hue.
export function waitClass(row: J): string {
  const s = rowStrip(row);
  return s ? "w-" + s.color : "";
}

// viewStrip is the facet that colors the strip of view's items: a task view's from its profile, a row view's from
// its provider; null for a view of mixed rows, whose rows each take their own.
export function viewStrip(view: string): J {
  if (isCoreView(view)) return stripSpec((tasksState.catalog || {}).facets, "");
  const provider = (catalog.views.find((v) => v.id === view) || {}).provider || "";
  return provider ? stripSpec(catalog.specs, provider) : null;
}

// boardGroupField is the field a board of view groups by when nothing is picked: the facet that colors its strip
// when the view offers it; "" otherwise.
export function boardGroupField(view: string, offered: Field[]): string {
  const s = viewStrip(view);
  return s && offered.some((f) => f.id === s.id) ? s.id : "";
}

// servedLayout is the layout the profile draws a core view in when the hash names none: "board", or "" for its
// list.
export function servedLayout(view: string): string {
  const spec = viewSpec(view);
  return spec && spec.layout === "board" ? "board" : "";
}

// lensPicks are the group and the sort of route that view offers; one it does not offer gives way to the view's
// default, while the hash keeps it for the views that do.
export function lensPicks(view: string, route: Pick<Hash, "area" | "ctx" | "done">, g: string, s: string): { g: string; s: string } {
  const fields = offeredFields(view, route);
  return {
    g: g === NO_GROUP || (groupField(g, fields) || ({} as Field)).group ? g : "",
    s: parseSort(s, fields).field ? s : "",
  };
}

export interface Lens {
  view: string;
  q: string;
  ts: string[];
  quals: Qualifier[];
  filters: Map<string, Filter>;
  group: string;
  sort: string;
  layout: string;
  tk: string;
  done: boolean;
  fields: Field[];
  offered: Field[];
  defaultGroup: string;
  boardGroup: string;
  defaultSort: string;
  specs: J[];
  provider: string;
  words: J;
  strip: string;
  stripSpec: J;
  static?: boolean;
}

export type LensRoute = Pick<Hash, "view" | "q" | "g" | "s" | "layout" | "area" | "ctx" | "tk" | "done">;

// lensOf is the lens of route: its query read over the fields of its view, trimmed as the daemon counts it, the
// picks its view offers and its view's defaults.
export function lensOf(route: LensRoute): Lens {
  const { view, tk, done } = route;
  const q = route.q || "";
  const fields = lensFields(view);
  const offered = offeredFields(view, route);
  const quals = qualifiers(q.trim(), fields);
  const { g, s } = lensPicks(view, route, route.g || "", route.s || "");
  const core = isCoreView(view);
  const provider = core ? "" : (catalog.views.find((v) => v.id === view) || {}).provider || "";
  const specs = core ? (tasksState.catalog || {}).facets || [] : catalog.specs;
  const strip = viewStrip(view);
  return { view, q, ts: terms(q.trim(), fields), quals, filters: filtersOf(quals, fields), group: g, sort: s, layout: route.layout || servedLayout(view), tk, done,
    fields, offered, defaultGroup: viewGroupField(view), boardGroup: boardGroupField(view, offered), defaultSort: defaultSortLabel(view),
    specs, provider, words: rowWords(provider), strip: (strip || {}).id || "", stripSpec: strip };
}

// effectiveGroup is the field a lens groups by: the user's pick, else its view's own, else, on a board, the field
// its strip is colored by.
export function effectiveGroup(lens: Pick<Lens, "group" | "defaultGroup" | "layout" | "boardGroup" | "view">): string {
  if (lens.group === NO_GROUP) return "";
  if (lens.group) return lens.group;
  if (lens.defaultGroup) return lens.defaultGroup;
  if (lens.layout !== "board") return "";
  return lens.boardGroup === undefined ? (viewStrip(lens.view) || {}).id || "" : lens.boardGroup;
}

export function sinceTime(r: J): number {
  const ms = Date.parse(sm(r).since);
  return Number.isNaN(ms) ? -Infinity : ms;
}

// byDefault is the daemon's order: stalled first, then the longest waiting.
export function byDefault(a: J, b: J): number {
  if (!!sm(a).stalled !== !!sm(b).stalled) return sm(a).stalled ? -1 : 1;
  return cmp(sinceTime(a), sinceTime(b)) || cmp(unitOf(a).provider, unitOf(b).provider) || cmp(unitOf(a).key, unitOf(b).key);
}

function keyRank(f: Field, k: string): [number, number | string] {
  k = fold(k);
  if (k === "none") return [2, ""];
  const i = f.rank ? f.rank.indexOf(k) : -1;
  return i >= 0 ? [0, i] : [1, k];
}

// keyOrder puts ranked values in rank order and the rest by name, with no value always last, whatever the
// direction.
export function keyOrder(f: Field, a: string, b: string, desc: boolean): number {
  const [ba, sa] = keyRank(f, a);
  const [bb, sb] = keyRank(f, b);
  if (ba !== bb) return ba === 2 || bb === 2 || !desc ? ba - bb : bb - ba;
  return desc ? cmp(sb, sa) : cmp(sa, sb);
}

// compareItems orders by f, ties by byDefault when there is one; the sort is stable, so the rest keep their
// order.
export function compareItems(f: Field, desc: boolean, byDefault: ((a: J, b: J) => number) | null): (a: J, b: J) => number {
  return (a, b) => {
    const sv = f.sortValue;
    const c = sv ? (desc ? cmp(sv(b), sv(a)) : cmp(sv(a), sv(b))) : keyOrder(f, itemKey(f, a), itemKey(f, b), desc);
    return c || (byDefault ? byDefault(a, b) : 0);
  };
}

export function groupLabel(f: Field, raw: string): string {
  return !raw || raw === "none" ? f.empty || t("none") : f.show ? f.show(raw) : raw;
}

export interface Group {
  field: string;
  key: string;
  raw: string;
  label: string;
  rows: J[];
}

export function groupItems(f: Field, items: J[]): Group[] {
  const buckets = new Map<string, Group>();
  const add = (raw: string) => {
    const k = fold(raw);
    if (!buckets.has(k)) buckets.set(k, { field: f.id, key: k, raw, label: groupLabel(f, raw), rows: [] });
    return buckets.get(k) as Group;
  };
  for (const it of items) {
    const ks = f.keys ? f.keys(it) : [itemKey(f, it)];
    const seen = new Set<string>();
    for (const k of ks.length ? ks : ["none"]) {
      if (!seen.has(fold(k))) add(k).rows.push(it);
      seen.add(fold(k));
    }
  }
  if (f.id === "lane") for (const id of f.rank || []) add(id);
  return [...buckets.values()].sort((a, b) => keyOrder(f, a.key, b.key, false));
}

export interface Pipeline {
  groups: Group[];
  group: string;
  total: number;
  shown: number;
}

// pipelineOf is every list's one path: filter, then sort, then group, items keeping the sort inside their group.
// An item is { facets, texts }.
export function pipelineOf(items: J[], lens: Pick<Lens, "fields" | "ts" | "filters" | "sort" | "group" | "defaultGroup" | "layout" | "boardGroup" | "view">, byDefaultOrder: ((a: J, b: J) => number) | null): Pipeline {
  const fields = lens.fields || catalog.fields;
  const shown = items.filter((it) => matches(it, lens.ts) && passesFilters(it, lens.filters));
  const { field, desc } = parseSort(lens.sort, fields);
  shown.sort(field ? compareItems(fieldById(field, fields) as Field, desc, byDefaultOrder) : byDefaultOrder || (() => 0));
  const g = groupField(effectiveGroup(lens), fields);
  const groups = g ? groupItems(g, shown) : [{ field: "", key: "", raw: "", label: "", rows: shown }];
  return { groups, group: g ? g.id : "", total: items.length, shown: shown.length };
}

export function pipeline(rows: J[], lens: Lens): Pipeline {
  return pipelineOf(rows.filter((r) => inView(r, lens.view, lens.done)), lens, byDefault);
}

// groupedRows lists each row once, though grouping by a tag key may put it in several groups.
export function groupedRows(groups: Group[]): J[] {
  return [...new Set(groups.flatMap((g) => g.rows))];
}

// groupQualifier is the filter that keeps only a group's rows: a tag key's "no lang" group is -tag:lang.
export function groupQualifier(g: Group, fields: Field[] = catalog.fields): { field: string; raw: string; neg: boolean } | null {
  const f = groupField(g.field, fields);
  if (!f) return null;
  if (f.tagKey) return g.key === "none" ? { field: "tag", raw: f.tagKey, neg: true } : { field: "tag", raw: g.raw, neg: false };
  return f.values ? { field: g.field, raw: g.raw, neg: false } : null;
}

// multiCopy is whether a grouping may show a pull request in several groups.
export function multiCopy(field: string, fields: Field[] = catalog.fields): boolean {
  return !!field && !!(groupField(field, fields) || ({} as Field)).keys;
}

// copySlot is the focus slot of one copy of a row or card: grouping by a tag key shows a pull request once per
// value, copy naming the group.
export function copySlot(kind: string, key: string, copy: string): string {
  return copy ? `${kind}:${key}@${copy}` : `${kind}:${key}`;
}

// ageText mirrors lens.Age: ms in the row words.
export function ageText(w: J, ms: number): string {
  const mins = Math.floor(Math.max(ms, 0) / 60000);
  if (mins < 1) return w.age_now || "";
  if (mins < 60) return fillWord(w.age_minutes, { m: mins });
  if (mins < 1440) return fillWord(w.age_hours, { h: Math.floor(mins / 60), m: mins % 60 });
  return fillWord(w.age_days, { d: Math.floor(mins / 1440), h: Math.floor(mins / 60) % 24 });
}

// agePart says whether a part shows one time of one facet, as its age.
export function agePart(part: string): boolean {
  return part === "age";
}

export interface Piece {
  field: string;
  value: string;
  raw: string;
  label: string;
  template?: string;
}

function partPiece(spec: J, raw: string, words: J, now: number): Piece | null {
  if (!raw || fold(raw) === "none") return null;
  const p: Piece = { field: spec.id, value: raw, raw, label: (spec.labels || {})[raw] || raw };
  if (spec.sort === "time" || (spec.buckets || []).length) {
    const at = rfc3339(raw) ? Date.parse(raw) : NaN;
    if (Number.isNaN(at)) return null;
    if ((spec.buckets || []).length) {
      p.value = bucketOf(spec.buckets, raw, now);
      if (!p.value) return null;
      const b = spec.buckets.find((x: J) => x.id === p.value);
      if (b) p.label = b.label || p.value;
    }
    if (agePart(spec.show)) {
      p.label = ageText(words, now - at);
      if (spec.show_label) [p.label, p.template] = [spec.show_label.replaceAll("{{.age}}", p.label), spec.show_label];
    }
  }
  if (spec.label_prefix && !p.label.startsWith(spec.label_prefix)) p.label = spec.label_prefix + p.label;
  return p;
}

// holdsWhen mirrors lens's holds: whether it holds, in each facet when names, one of its values.
export function holdsWhen(it: J, when: J): boolean {
  return Object.entries(when || {}).every(([facet, values]) => ((it.facets || {})[facet] || []).some((v: string) => (values as string[]).some((w) => fold(v) === fold(w))));
}

// rowParts mirrors lens.Parts: the pieces of item it each part of a row shows, by part, from the specs that show
// in one, in their order, none from another provider's spec.
export function rowParts(it: J, specs: J[], words: J, now = minuteNow()): Record<string, Piece[]> {
  const out: Record<string, Piece[]> = {};
  for (const s of specs || []) {
    if (s.provider && it.provider && s.provider !== it.provider) continue;
    const spec = specFor(s, it.provider || "");
    if (!spec.show || (agePart(spec.show) && out[spec.show]) || !holdsWhen(it, spec.show_when)) continue;
    const pieces: Piece[] = [];
    let rest = 0;
    for (const raw of (it.facets || {})[spec.id] || []) {
      const p = partPiece(spec, raw, words, now);
      if (!p || ((spec.show_only || []).length && !spec.show_only.some((v: string) => fold(v) === fold(raw)))) continue;
      if (spec.show_max > 0 && pieces.length === spec.show_max) {
        rest++;
        continue;
      }
      pieces.push(p);
      if (spec.show_values === "first" || agePart(spec.show)) break;
    }
    if (rest) pieces.push({ field: spec.id, value: "", raw: "", label: fillWord(words.more, { n: rest }) });
    if (pieces.length) out[spec.show] = [...(out[spec.show] || []), ...pieces];
  }
  return out;
}

// rowClasses are the classes the specs' row_class give the values an item holds.
export function rowClasses(it: J, specs: J[]): string[] {
  const out: string[] = [];
  for (const s of specs || []) {
    if (s.provider && it.provider && s.provider !== it.provider) continue;
    const spec = specFor(s, it.provider || "");
    for (const v of (it.facets || {})[spec.id] || []) if ((spec.row_class || {})[v]) out.push(spec.row_class[v]);
  }
  return [...new Set(out)];
}

// rowFlags are the values of the "is" facet a row holds now.
export function rowFlags(r: J): string[] {
  const f = fieldById("is");
  return f && f.values ? f.values(r) : [];
}

export function isStalled(r: J): boolean {
  return rowFlags(r).includes("stalled");
}

export function fieldRank(id: string): string[] {
  return (fieldById(id) || ({} as Field)).rank || [];
}

export function fieldShow(id: string, k: string): string {
  const f = fieldById(id);
  return f && f.show ? f.show(k) : k;
}

// rowByURL is the row whose link q is, or a page under that link such as its files or one comment.
export function rowByURL(q: unknown): J {
  const u = String(q || "").trim();
  if (!u.startsWith("https://")) return null;
  return state.rows.find((r) => r.link && (u === r.link || (u.startsWith(r.link) && "/?#".includes(u[r.link.length])))) || null;
}

export function rowByKey(key: string): J {
  const k = key.toLowerCase();
  return state.rows.find((r) => keyOf(r).toLowerCase() === k);
}

// roundGate is the open plan gate an automatic round of the row waits on, until its deadline.
export function roundGate(row: J): J {
  return state.roundGates.get(keyOf(row));
}

// upcoming are a row's next instants: the daemon's list, or else the deadline of the plan gate its automatic
// round waits on.
export function upcoming(r: J): J[] {
  const next = ((pl(r).facts || {}).next || []).filter((e: J) => isSet(e.at));
  const g = roundGate(r);
  if (g && !next.some((e: J) => e.what === "gate")) next.push({ at: g.deadline, what: "gate" });
  return next;
}

export function age(since: string, now = minuteNow()): string {
  if (!isSet(since)) return "";
  const mins = Math.max(0, Math.floor((now - Date.parse(since)) / 60000));
  if (mins < 1) return t("now");
  if (mins < 60) return t("{minutes} min", { minutes: mins });
  const hours = Math.floor(mins / 60);
  if (hours < 24) return t("{hours}h {minutes}m", { hours, minutes: mins % 60 });
  return t("{days}d {hours}h", { days: Math.floor(hours / 24), hours: hours % 24 });
}

// left is a pending round's countdown to deadline in the row words w.
export function left(deadline: string, w: J, now = clock.now): string {
  const s = Math.ceil((Date.parse(deadline) - now) / 1000);
  if (s <= 0) return w.countdown_sending || "";
  if (s <= 99) return fillWord(w.countdown_seconds, { s });
  return fillWord(w.countdown_minutes, { m: Math.floor(s / 60), s: String(s % 60).padStart(2, "0") });
}

export function stamp(iso: string): string {
  if (!isSet(iso)) return "";
  return fmtDate(iso, "datetime");
}

export function resolveWho(row: J, w: string): string {
  if (w !== "author") return w;
  const viewer = state.health && state.health.viewer;
  const author = pl(row).author || "";
  return viewer && author.toLowerCase() === viewer.toLowerCase() ? "me" : author;
}

export function waitingWho(row: J): string[] {
  return facetValues(row, "waits-on");
}

export function waitingLabel(row: J): string {
  return (sm(row).waits_on || "").split(", ").map((w: string) => resolveWho(row, w)).join(", ");
}

// waitCat is the value of a row's strip facet, "" when it has no strip.
export function waitCat(row: J): string {
  const s = rowStrip(row);
  return s ? s.value : "";
}

// waitColor is the hue of a row's strip.
export function waitColor(row: J): string {
  const s = rowStrip(row);
  return s ? s.color : "nobody";
}

// rowTip is a row's tooltip in the row words w: its strip value's label, whom it waits on unless that is you or
// nobody, its kind, then why.
export function rowTip(strip: Pick<Strip, "label"> | null, who: string, kind: string, why: string, w: J): string {
  let tip = strip ? strip.label : "";
  if (tip && who && who !== "me" && who !== "nobody") tip = fillWord(w.tip_named, { strip: tip, who });
  if (kind) tip = tip ? fillWord(w.tip_kind, { tip, kind }) : kind;
  if (why) tip = tip ? fillWord(w.tip_why, { tip, why }) : why;
  return tip;
}

// openSlot is the copy of the open pull request last opened, when it is still the open one.
export function openSlot(pr: string): string {
  const s = state.openSlot || "";
  return pr && ["row", "card"].some((k) => s === `${k}:${pr}` || s.startsWith(`${k}:${pr}@`)) ? s : "";
}

export const SIZES = ["xs", "s", "m", "l", "xl"];
export const RISKS = ["low", "medium", "high"];

// intakeWaiting says whether the row's intake waits for its brief.
export function intakeWaiting(r: J): boolean {
  const i = r && pl(r).intake;
  return !!i && (i.state === "accepted" || i.state === "briefing");
}
