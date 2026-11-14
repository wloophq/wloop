import { intakeWaiting, left, openSlot, resolveWho, roundGate, rowTip, stamp, waitingLabel } from "../../lib/lens";
import { stateLabel } from "../../state/verbs";
import { catalog, collapsed, state, tasksState } from "../../state/store.svelte";
import { viewHash, writeHash } from "../../state/route.svelte";
import { clock } from "../../state/clock.svelte";
import { providerName, roleValue, searchService, starred } from "../../lib/catalog";
import { fmtDate, fmtNumber, t, tn } from "../../lib/i18n.svelte";
import {
  ageText, copySlot, fillWord, groupQualifier, normValue, qualifiers, reasonText, rowByURL, rowClasses, rowParts, rowWords,
  stripColor, stripOf, toggledQualifier, type Group, type Lens, type Piece, type Strip,
} from "../../lib/lens";
import { draftOf, everyLabel, isDraftTask, localDate, shortDate, shortLag, taskSlot, unitLabel } from "../../lib/tasks";
import { fold, isSet, keyOf, labelOf, refLabel, safeURL, unitOf, type J } from "../../lib/util";
import { EXPLORE, RECENT } from "../../lib/routes";
import { clearLens } from "../../lib/view";

// The one row every list draws, task and unit rows alike, and its board card, as data: a row model holds the
// lens's pieces by part and what no facet carries; RowView lays them on two lines.

// ROW_PART_CLASS is the class each part's pieces read in; a part not named reads in its own name.
const ROW_PART_CLASS: Record<string, string> = { meta: "pill", flag: "flag", kind: "verb", aside: "col" };

export function partClass(part: string): string {
  return ROW_PART_CLASS[part] || part;
}

// pieceLabel is a piece's text: an age piece's label template gets its time as the viewer's date.
export function pieceLabel(p: Piece): string {
  return p.template ? p.label.replaceAll("{{.at}}", fmtDate(p.raw, "datetime")) : p.label;
}

// pieceTitle is an age piece's tooltip: how long ago, then the exact time.
export function pieceTitle(p: Piece, words: J, now: number): string | undefined {
  const at = Date.parse(p.raw);
  return Number.isNaN(at) ? undefined : `${ageText(words, now - at)} (${stamp(p.raw)})`;
}

// restOf are the words of the values of field the item holds that pieces, a part's pieces, leave out past its
// show_max.
export function restOf(item: J, lens: Pick<Lens, "specs">, pieces: Piece[], field: string): string[] {
  const shown = new Set(pieces.filter((p) => p.field === field && p.value).map((p) => p.raw));
  const labels = ((lens.specs || []).find((s: J) => s.id === field) || {}).labels || {};
  return ((item.facets || {})[field] || []).filter((v: string) => !shown.has(v)).map((v: string) => labels[v] || v);
}

// titleWithoutRef is title without a leading unit label of labels and the separator after it; the whole title
// when nothing would be left.
export function titleWithoutRef(title: unknown, labels: string[]): string {
  const s = String(title || "");
  for (const label of labels || []) {
    if (!label || !s.startsWith(label)) continue;
    const rest = s.slice(label.length).replace(/^\s*[:\-–—|]?\s*/, "");
    if (rest && rest !== s.slice(label.length)) return rest;
  }
  return s;
}

// refTail is a unit's label after the first ref piece's label, when the label starts with it.
export function refTail(label: string, ref: Piece[] | undefined): string {
  const lead = ref && ref[0] ? ref[0].label : "";
  return lead && label.startsWith(lead) ? label.slice(lead.length) : label;
}

export interface Countdown {
  deadline: string;
  provider: string;
  words: J;
  title: string;
}

export function countdown(row: J): Countdown {
  const deadline = roundGate(row).deadline;
  const provider = unitOf(row).provider;
  const words = rowWords(provider);
  return { deadline, provider, words, title: fillWord(words.countdown_title, { time: stamp(deadline) }) };
}

export interface Bit {
  class: string;
  text: string;
  title?: string;
}

export function intakeChip(r: J, w: J): Bit | null {
  if (!intakeWaiting(r)) return null;
  const i = r.payload.intake;
  const title = [fillWord(w.briefing_title, { seq: i.seq }), i.attempt ? fillWord(w.briefing_attempt, { n: i.attempt }) : "",
    i.last_error ? fillWord(w.briefing_error, { error: i.last_error }) : ""].filter(Boolean).join(", ");
  return { class: "flag briefing", text: w.briefing, title };
}

// draftMark is the marker of a task whose drafts wait for the viewer; null without one.
export function draftMark(e: J): Bit | null {
  const n = ((e && e.drafts) || []).length;
  if (!n) return null;
  return { class: "draft-mark", text: fmtNumber(n), title: tn("{n} draft waits for you", n) };
}

export interface Glyph {
  category: string;
  label: string;
  field?: string;
  value?: string;
}

// RefBit is a piece of a row's ref line no facet carries: text marked by the search, or a unit's label on a
// task's row that filters by the where value it names.
export interface RefBit {
  text: string;
  where?: string;
  slot?: string;
}

export interface Link {
  href: string | undefined;
  label: string;
  slot: string;
}

export interface ProjectFacts {
  reviewDue: string;
  noNext: string;
  open: string;
  sequential: string;
  every: string;
  reviewed: string;
}

export interface RowModel {
  key: string;
  scope: string;
  kind: string;
  item: J;
  parts: Record<string, Piece[]>;
  words: J;
  nouns: Record<string, string>;
  selected: boolean;
  open: (() => void) | null;
  href: string;
  title: string;
  titleTip: string;
  tip: string;
  glyph: Glyph | null;
  changed: boolean;
  draftMark: Bit | null;
  star: string;
  ref: RefBit[];
  meta: Bit[];
  flags: Bit[];
  strip: Strip | null;
  age: Countdown | null;
  why: string;
  asideLead: string;
  agents: string;
  link: Link | null;
  actions: J;
  cls: string[];
  project: ProjectFacts | null;
  draft: { task: J; draft: J } | null;
}

export function openRow(scope: string, key: string): void {
  state.openSlot = scope;
  writeHash({ pr: key, task: "" });
}

export function rowLink(row: J, scope: string): Link {
  return { href: safeURL(row.link), label: providerName(unitOf(row).provider), slot: `${scope}:link` };
}

// rowModel is a unit row as the row view lays it out, in a list or as a board's card (kind "card"): its facets
// placed by its provider's specs, then what no facet carries. now is the minute ages read.
export function rowModel(r: J, pr: string, lens: Lens, copy: string, kind = "row", now = clock.now): RowModel {
  const key = keyOf(r);
  const p = r.payload || {};
  const w = rowWords(unitOf(r).provider);
  const item = { facets: r.facets || {}, provider: unitOf(r).provider };
  const parts = rowParts(item, lens.specs, w, now);
  const scope = copySlot(kind, key, copy);
  const n = Number.isInteger((p.brief || {}).open_questions) ? p.brief.open_questions : 0;
  const ws = p.workspace || {};
  const strip = stripOf(item, lens.specs);
  const why = reasonText(r);
  const groups = new Set((p.waits_on || []).filter((x: J) => String(x.key || "").startsWith("group:")).map((x: J) => fold(x.name || "")));
  const nouns = Object.fromEntries((parts.who || []).filter((x) => groups.has(fold(x.raw))).map((x) => [`${x.field}:${x.value}`, w.filter_group]));
  const meta: Bit[] = [];
  if (p.head && lens.ts.some((term) => fold(p.head).includes(term))) meta.push({ class: "head", text: p.head });
  if (n > 0) meta.push({ class: "pill qs", text: fillWord(w.questions, { n }), title: fillWord(w.questions_title, { n }) });
  const flags: Bit[] = [];
  if (r.stale) flags.push({ class: "flag stale", text: w.stale, title: w.stale_title });
  const intake = intakeChip(r, w);
  if (intake) flags.push(intake);
  return {
    key, scope, kind, item, parts, words: w, nouns, selected: key === pr, open: () => openRow(scope, key), href: "",
    title: titleWithoutRef(r.title || labelOf(r), [labelOf(r)]), titleTip: (p.brief || {}).goal || "",
    glyph: null, changed: false, draftMark: null, star: "",
    ref: [{ text: refTail(labelOf(r), parts.ref) }],
    meta, flags, strip,
    age: roundGate(r) ? countdown(r) : null,
    why,
    asideLead: ws.display_name || "",
    agents: (ws.agent_states || []).join(", "),
    link: rowLink(r, scope),
    actions: r,
    cls: [strip ? "w-" + strip.color : "", ...rowClasses(item, lens.specs)],
    tip: rowTip(strip, waitingLabel(r), (parts.kind || []).map((x) => x.label).join(", "), why, w),
    project: null,
    draft: null,
  };
}

// whereRef is a unit's label on a task's row, which filters by the where value it names when the task holds one:
// the longest the label ends with, or holds after its owner, as "#chan" or "owner/repo#7" do.
export function whereRef(label: string, item: J, scope: string): RefBit {
  const named = ((item.facets || {}).where || []).filter((v: string) => v && (label.endsWith(v) || label.includes(v + "#")))
    .sort((a: string, b: string) => b.length - a.length)[0];
  return named ? { text: label, where: named, slot: `facet:${scope}:where:${named}` } : { text: label };
}

// projectFacts are a project's open children and review, in the row words w.
export function projectFacts(e: J, w: J): ProjectFacts | null {
  if ((e.props || {}).kind !== "project") return null;
  const open = (e.children || []).length;
  return {
    reviewDue: e.review_due ? w.review_due : "",
    noNext: open ? "" : w.no_next_action,
    open: fillWord(w.open_children, { n: open }),
    sequential: e.sequential ? w.sequential : "",
    every: e.review_every ? fillWord(w.every, { every: everyLabel(e.review_every) }) : "",
    reviewed: e.reviewed_at ? fillWord(w.reviewed, { date: localDate(e.reviewed_at) }) : "",
  };
}

// taskModel is a task as the row view lays it out: its facets placed by the profile's task specs, then what no
// facet carries. A draft's own task shows its draft under its title and opens from its title alone.
export function taskModel(e: J, open: string, copy: string, lens: Lens, now = clock.now): RowModel {
  const w = lens.words || {};
  const item = { facets: e.facets || {} };
  const parts = rowParts(item, lens.specs, w, now);
  const strip = stripOf(item, lens.specs);
  const draft = isDraftTask(e);
  const due = roleValue(e, "deadline");
  const scope = taskSlot(e.id, copy);
  const labels: string[] = (e.units || []).map((u: J, i: number) => (e.unit_labels || [])[i] || unitLabel(u));
  const stateSpec = (lens.specs || []).find((s: J) => s.fact === "task.state" && !s.provider);
  const statePiece = stateSpec && (parts.kind || []).find((p) => p.field === stateSpec.id);
  if (statePiece) parts.kind = parts.kind.filter((p) => p !== statePiece);
  const flags: Bit[] = [];
  if (due) flags.push({ class: "flag due", text: fillWord(w.due, { date: shortDate(due) }) });
  if (e.wake && e.wake.until) flags.push({ class: "flag due", text: fillWord(w.until, { date: shortDate(e.wake.until) }) });
  if (e.extraction_lag) flags.push({ class: "flag due", text: fillWord(w.unread, { lag: shortLag(e.extraction_lag) }), title: w.unread_title });
  return {
    key: e.id, scope, kind: "task", item, parts, words: w, nouns: {}, selected: e.id === open,
    open: draft ? null : () => writeHash({ task: e.id, pr: "" }),
    href: draft ? "#" + viewHash({ task: e.id, pr: "" }) : "",
    title: titleWithoutRef(e.title || e.id, labels), titleTip: "",
    glyph: statePiece ? { category: e.category || "", label: statePiece.label, field: statePiece.field, value: statePiece.value }
      : { category: e.category || "", label: stateLabel(e.state) },
    changed: !!e.changed,
    draftMark: draftMark(e),
    star: starred(e) ? w.starred || "" : "",
    ref: labels.map((label) => whereRef(label, item, scope)),
    meta: [],
    flags,
    strip,
    age: null,
    why: e.reason || "",
    asideLead: "",
    agents: "",
    link: null,
    actions: null,
    cls: ["task-row", strip ? "w-" + strip.color : "", e.changed ? "changed" : "", draft ? "outbox" : "", ...rowClasses(item, lens.specs)],
    tip: "",
    project: projectFacts(e, w),
    draft: draft ? { task: e, draft: draftOf(e) } : null,
  };
}

// rowClass is the class of a row or a card.
export function rowClass(m: RowModel, card: boolean): string {
  return [card ? "card" : "row", ...m.cls, m.selected ? "sel" : ""].filter(Boolean).join(" ");
}

// headClass is the class of a group's or a lane's head: its strip's hue when the lens groups by its strip.
export function headClass(base: string, g: Group, lens: Pick<Lens, "strip" | "stripSpec">): string {
  return base + (g.field && g.field === lens.strip ? " w-" + stripColor(lens.stripSpec, g.key) : "");
}

export interface OnlyThis {
  on: boolean;
  slot: string;
  label: string;
  text: string;
  pick: () => void;
}

// onlyThis is a group's "only this" filter, or its "show all" once it filters; null when the group's field
// cannot filter.
export function onlyThis(g: Group, lens: Pick<Lens, "fields" | "quals" | "q">): OnlyThis | null {
  const q = groupQualifier(g, lens.fields);
  if (!q) return null;
  const v = normValue(q.field, q.raw);
  const on = lens.quals.some((x) => x.field === q.field && x.value === v && x.neg === q.neg);
  return {
    on, slot: `group:${g.field}:${g.key}:only`,
    label: on ? t("Show all, not only {group}", { group: g.label }) : t("Only {group}", { group: g.label }),
    text: on ? t("show all") : t("only this"),
    pick: () => writeHash({ q: toggledQualifier(lens.q, q.field, q.raw, q.neg, lens.fields) }),
  };
}

export function groupKey(g: Pick<Group, "field" | "key">): string {
  return `${g.field}:${g.key}`;
}

export function toggleCollapsed(key: string): void {
  if (collapsed.has(key)) collapsed.delete(key);
  else collapsed.add(key);
}

// unitTitle names a row view's unit in its count line: the view's unit, else the lens's word for a mixed view.
export function unitTitle(view: string, lens: Pick<Lens, "words">): string {
  return (catalog.views.find((x) => x.id === view) || {}).unit || (lens.words || {}).col_unit || "";
}

export const PR_URL = /^https:\/\/github\.com\/([A-Za-z0-9-]+)\/([\w.-]+)\/pull\/(\d+)(?:[/?#]\S*)?$/;

// NotePart is a piece of an empty note: text, or a control that acts on it.
export type NotePart =
  | { kind: "text"; text: string }
  | { kind: "button"; text: string; slot: string; run: () => void }
  | { kind: "link"; text: string; slot: string; href: string };

// exploring is how an empty note starts an Explore search; the integrator wires the Explore screen's.
export const exploring = { explore: (_q: string): unknown => undefined };

const text = (s: string): NotePart => ({ kind: "text", text: s });

export function emptyNote(view: string, q: string): NotePart[] {
  if (!state.loaded) return [text(t("Loading the inventory."))];
  const label = view === RECENT ? t("Recent") : (catalog.views.find((v) => v.id === view) || {}).label;
  const row = rowByURL(q);
  if (row) {
    return [text(t("{unit} is in the inventory.", { unit: labelOf(row) })), text(" "),
      { kind: "button", text: t("Open it"), slot: "empty:open", run: () => writeHash({ q: "", pr: keyOf(row) }) }];
  }
  const pull = PR_URL.exec(String(q || "").trim());
  if (pull && !/^\.\.?$/.test(pull[2])) {
    const [, owner, repo, number] = pull;
    const typed = q.trim();
    return [text(t("{unit} is not in the inventory.", { unit: refLabel({ owner, repo, number }) })), text(" "),
      { kind: "button", text: t("Explore it"), slot: "empty:explore", run: () => { writeHash({ view: EXPLORE, q: "", x: typed }); exploring.explore(typed); } },
      text(" "),
      { kind: "link", text: t("Open it on {provider}", { provider: searchService() }), slot: "empty:gh", href: typed }];
  }
  if (!q) return [text(t("Nothing in {view} right now.", { view: label }))];
  const note = t("Nothing in {view} matches \"{query}\".", { view: label, query: q });
  if (!qualifiers(q).length) return [text(note)];
  return [text(note), text(" "), { kind: "button", text: t("Clear filters"), slot: "empty:clear", run: () => clearLens() }];
}

// reveal remembers the unit whose open row was last scrolled into view, so each opening scrolls to it once.
export const reveal = { pr: "" };

export function revealOpenRow(pr: string, root: ParentNode = document): void {
  if (!pr) {
    reveal.pr = "";
    return;
  }
  if (reveal.pr === pr) return;
  const slot = openSlot(pr);
  const row = (slot && root.querySelector(`[data-focus="${CSS.escape(slot)}"]`)) || root.querySelector(".row.sel, .card.sel");
  if (!row) return;
  reveal.pr = pr;
  row.scrollIntoView({ block: "nearest" });
}


export { intakeWaiting, left, openSlot, resolveWho, roundGate, rowTip, stamp, waitingLabel, stateLabel };
