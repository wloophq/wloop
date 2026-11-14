import { isCoreView, kindLabel } from "../../lib/catalog";
import { fmtNumber, t } from "../../lib/i18n.svelte";
import {
  fieldById, fillWord, groupedRows, groupField, groupLabel, inView, lensWords, parseSort, qualifierToken, sm, toggledQualifier, rowByKey, withoutQualifiers, type Lens,
} from "../../lib/lens";
import { taskLensItems } from "../../lib/tasks";
import { isSet, labelOf, unitKey, type J } from "../../lib/util";
import { lensResult, moveFocus, readLens } from "../../lib/view";
import { clipboard, setNotice } from "../../state/actions.svelte";
import { clock } from "../../state/clock.svelte";
import { showPalette, type PaletteRequest } from "../../state/overlays.svelte";
import { writeHash } from "../../state/route.svelte";
import { catalog, state, type Field } from "../../state/store.svelte";

// ACTIVITY_FIELDS are the filters Recent applies: an event's repository and the author of its pull request.
export const ACTIVITY_FIELDS = ["where", "author"];

export function sortLabel(sort: string, fields: Field[] = catalog.fields): string {
  const { field, desc } = parseSort(sort, fields);
  return field ? `${(fieldById(field, fields) as Field).label.toLowerCase()} ${desc ? "descending" : "ascending"}` : "";
}

// lensItems are what lens's view lists before the lens: a task view's tasks, a row view's rows.
export function lensItems(lens: Lens): J[] {
  if (isCoreView(lens.view)) return taskLensItems();
  return state.rows.filter((r) => inView(r, lens.view, lens.done));
}

export const yamlString = (v: unknown): string => JSON.stringify(String(v ?? ""));

export const yamlMap = (fields: Record<string, unknown>): string => `{${Object.entries(fields).map(([k, v]) => `${k}: ${v}`).join(", ")}}`;

export interface Entry {
  ref: string;
  url?: string;
  title: string;
  since?: string;
  reason?: string;
  units?: string[];
  facets?: Record<string, string[]>;
}

// copyEntry is what Copy list says of an item: a task by its id, its first unit's link and its units, a row by its
// label and link, with its summary's reason and since.
export function copyEntry(it: J): Entry {
  const e = it.task;
  if (e) {
    const linked = (e.units || []).map((u: J) => (rowByKey(unitKey(u)) || {}).link).find(Boolean);
    return { ref: e.id, url: linked, title: e.title, units: (e.units || []).map((u: J, i: number) => (e.unit_labels || [])[i] || unitKey(u)), facets: e.facets };
  }
  return { ref: labelOf(it), url: it.link, title: it.title, since: isSet(sm(it).since) ? sm(it).since : "", reason: sm(it).reason, facets: it.facets };
}

export interface ListMeta {
  view: string;
  filter?: string;
  group_by?: string;
  sort?: string;
  at: string;
}

// listYAML writes the items a view shows as YAML whose scalars are JSON strings, so an agent reads it without
// quoting surprises: each item's reference, link, title and units, then its facets by id.
export function listYAML(meta: ListMeta, items: J[]): string {
  const out = [`view: ${yamlString(meta.view)}`];
  if (meta.filter) out.push(`filter: ${yamlString(meta.filter)}`);
  if (meta.group_by) out.push(`group_by: ${yamlString(meta.group_by)}`);
  if (meta.sort) out.push(`sort: ${yamlString(meta.sort)}`);
  out.push(`copied_at: ${yamlString(meta.at)}`, `count: ${items.length}`, "items:");
  const list = (vs: string[]) => `[${vs.map(yamlString).join(", ")}]`;
  for (const it of items) {
    const c = copyEntry(it);
    const facets = Object.keys(c.facets || {})
      .sort()
      .map((id) => [id, ((c.facets as Record<string, string[]>)[id] || []).filter(Boolean)] as [string, string[]])
      .filter(([, vs]) => vs.length);
    const fields: [string, string][] = [
      ["ref", yamlString(c.ref)],
      ["url", c.url ? yamlString(c.url) : ""],
      ["title", yamlString(c.title)],
      ["since", c.since ? yamlString(c.since) : ""],
      ["reason", c.reason ? yamlString(c.reason) : ""],
      ["units", (c.units || []).length ? list(c.units as string[]) : ""],
      ...facets.map(([id, vs]) => [id, list(vs)] as [string, string]),
    ];
    fields.filter(([, v]) => v).forEach(([k, v], i) => out.push(`${i ? "    " : "  - "}${k}: ${v}`));
  }
  return out.join("\n") + "\n";
}

export async function copyList(lens: Lens): Promise<void> {
  const res = lensResult(lens);
  const items = groupedRows(res.groups);
  const grouped = res.group ? (groupField(res.group, lens.fields) as Field).label.toLowerCase() : "";
  const w = lensWords();
  try {
    await clipboard.writeText(listYAML({ view: lens.view, filter: lens.q, group_by: grouped, sort: sortLabel(lens.sort, lens.fields), at: new Date(clock.now).toISOString() }, items));
    setNotice(fillWord(grouped ? w.copied_grouped : w.copied, { count: items.length, view: lens.view, group: grouped }));
  } catch (err) {
    setNotice(t("Could not copy: {error}", { error: (err as Error).message }), true);
  }
}

// valueLabel is how a filter names a value of the field id: a kind by its label, any other value as shown.
function valueLabel(id: string, value: string, shown: string): string {
  return id === "kind" ? kindLabel(value, "") : shown;
}

// chipWords are what a filter's chip says of its field and values, with its × when removable.
export function chipWords(label: string, values: string[], neg: boolean, removable: boolean): string {
  const vars = { field: label, values: values.join(" " + t("or") + " ") };
  if (!removable) return neg ? t("{field}: not {values}", vars) : t("{field}: {values}", vars);
  return neg ? t("{field}: not {values} ×", vars) : t("{field}: {values} ×", vars);
}

export interface Chip {
  slot: string;
  off: boolean;
  title: string;
  label: string;
  text: string;
  remove: () => void;
}

// lensChips are one chip per filtered field and sign, which takes it out; applied names the fields a view
// applies, the others' chips off.
export function lensChips(lens: Lens, applied?: string[]): Chip[] {
  const by = new Map<string, { field: string; neg: boolean; values: string[] }>();
  for (const x of lens.quals) {
    const k = `${x.field}:${x.neg ? "-" : ""}`;
    if (!by.has(k)) by.set(k, { field: x.field, neg: x.neg, values: [] });
    by.get(k)!.values.push(valueLabel(x.field, x.value, x.raw));
  }
  const list = [...by.values()];
  const slot = (c: { field: string; neg: boolean }) => `lens:chip:${c.field}:${c.neg ? "-" : ""}`;
  return list.map((c, i) => {
    const f = fieldById(c.field, lens.fields) as Field;
    const off = !!applied && !applied.includes(c.field);
    const next = list[i + 1] || list[i - 1];
    return {
      slot: slot(c), off,
      title: off ? t("Not applied in Recent") : t("Remove this filter ({filter})", { filter: c.values.map((v) => qualifierToken(c.field, v, c.neg)).join(" ") }),
      label: t("Remove filter {label}", { label: chipWords(f.label, c.values, c.neg, false) }),
      text: chipWords(f.label, c.values, c.neg, true),
      remove: () => {
        writeHash({ q: withoutQualifiers(lens.q, c.field, c.neg, lens.fields) });
        void moveFocus(next ? slot(next) : "lens:add-filter");
      },
    };
  });
}

// sortFields are the fields the Sort chip offers beside the view's own order: every sortable field lens offers.
export function sortFields(lens: Lens): Field[] {
  return (lens.offered || catalog.fields).filter((f) => f.sort);
}

// filterFields are the fields "+ Filter" offers: those lens offers that filter and that some of items holds.
export function filterFields(lens: Lens, items: J[]): Field[] {
  return (lens.offered || catalog.fields).filter((f) => f.values && f.menu !== false && items.some((it) => (f.values!(it) || []).some(Boolean)));
}

// filterValues are the values of f that items hold, in f's rank, then the most held first, each with its label and
// how many items hold it.
export function filterValues(f: Field, items: J[]): { value: string; count: number; label: string }[] {
  const counts = new Map<string, number>();
  for (const it of items) for (const v of new Set((f.values!(it) || []).filter(Boolean))) counts.set(v, (counts.get(v) || 0) + 1);
  const rank = f.rank || [];
  const at = (v: string) => (rank.includes(v) ? rank.indexOf(v) : rank.length);
  return [...counts.entries()]
    .sort((a, b) => at(a[0]) - at(b[0]) || b[1] - a[1] || String(a[0]).localeCompare(String(b[0])))
    .map(([value, count]) => ({ value, count, label: valueLabel(f.id, value, groupLabel(f, value)) }));
}

// filterPalette opens, through palette, the fields lens filters by, then the values of the one picked, which adds
// it to the query; false when items hold no field to filter by.
export function filterPalette(lens: Lens, items: J[], palette: (p: PaletteRequest) => unknown): boolean {
  const fields = filterFields(lens, items);
  if (!fields.length) return false;
  const pickValue = (f: Field, x: { value: string }) => {
    writeHash({ q: toggledQualifier(lens.q, f.id, x.value, false, lens.fields) });
    void moveFocus("lens:add-filter");
  };
  palette({
    title: t("Filter"),
    items: fields.map((f) => ({
      label: f.label,
      key: "",
      run: () => palette({ title: f.label, items: filterValues(f, items).map((x) => ({ label: x.label, meta: fmtNumber(x.count), key: "", run: () => pickValue(f, x) })) }),
    })),
  });
  return true;
}

// openFilterPalette opens "+ Filter" over the list shown, when it shows a toolbar that offers it.
export function openFilterPalette(): boolean {
  const button = document.querySelector('[data-focus="lens:add-filter"]');
  if (!button || button.closest("[hidden]")) return false;
  const lens = readLens();
  return filterPalette(lens, lensItems(lens), showPalette);
}
