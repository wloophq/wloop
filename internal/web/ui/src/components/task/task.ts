import { fillWord, lensWords } from "../../lib/lens";
import { fmtDate, fmtNumber, t } from "../../lib/i18n.svelte";
import { catalog } from "../../state/store.svelte";
import { draftOf, isDraftTask, taskById as findTask, taskSlot, unitLabel } from "../../lib/tasks";
import { isSet, type J } from "../../lib/util";
import { getJSON, postJSON } from "../../state/api";
import { scheduler, setNotice } from "../../state/actions.svelte";
import { handlerVerb, loadDetailPane, loadTaskPage, taskPath, verbFor } from "../../state/loaders.svelte";
import { askInput, showPalette, type PaletteItem } from "../../state/overlays.svelte";
import { readHash, viewHash, writeHash } from "../../state/route.svelte";
import { detailState, editing, taskState, tasksState } from "../../state/store.svelte";
import { draftTarget, editedPayload, editedText } from "../gates/gates";

import { matchPath, PICK_ARG, taskPicker } from "../../lib/palette";
import { offeredVerb, servedAsk, stateLabel, stopEditing, taskAct, taskCatalog, verbHandler, verbLabel, verbState, verbTask } from "../../state/verbs";
import { rescueFocus } from "../keys/focus";

export { PICK_ARG };

export function taskById(id: string): J {
  return findTask(id, detailState.task, detailState.unitTasks);
}

// SKIP_HANDLERS are the handlers of the verbs the page offers elsewhere: seen when it opens, answer in its gate
// and a draft's in its card; the verb whose handler replies is in the composer.
const SKIP_HANDLERS = new Set(["seen", "answer", "edit", "retry"]);
// DRAFT_CARD_HANDLERS are the handlers of every verb a draft's card offers on the draft's own task.
const DRAFT_CARD_HANDLERS = ["approve", "drop", "edit", "retry"];

// pickVerb is the verb e offers whose pick is pick, "" when it offers none.
export function pickVerb(e: J, pick: string): string {
  const v = (e.verbs || []).find((x: J) => verbTask(x.verb).pick === pick);
  return v ? v.verb : "";
}

// verbTitle is a verb button's tooltip: what it does, then why it is off.
export function verbTitle(e: J, verb: string): string | undefined {
  const st = verbState(e, verb);
  return [verbTask(verb).summary, st && !st.enabled ? st.reason : ""].filter(Boolean).join("\n") || undefined;
}

export function verbClass(st: J, primary = false): string {
  return "act" + (st.enabled ? "" : " off") + (primary ? " primary" : "");
}

export function verbSlot(e: J, verb: string, scope = "detail"): string {
  return `${scope}:${verb}:${e.id}`;
}

// offeredVerbs are the verbs e offers that the page lists as its own: not those it offers elsewhere, the reply
// verb, a verb on a related or merged task's row, nor, on a draft's own task, those of the draft's card.
export function offeredVerbs(e: J): J[] {
  const replies = handlerVerb("reply");
  const onRows = (verb: string) => ["merged", "related"].includes(verbTask(verb).pick);
  const skip = (verb: string) => SKIP_HANDLERS.has(verbHandler(verb)) || verb === replies || onRows(verb) || (isDraftTask(e) && DRAFT_CARD_HANDLERS.includes(verbHandler(verb)));
  return (e.verbs || []).filter((v: J) => !skip(v.verb));
}

// detailVerbs are the verbs the profile's detail.verbs names, in its order.
export function detailVerbs(): string[] {
  return taskCatalog().detail_verbs || [];
}

// primaryVerbs are the profile's detail verbs e offers, in its order.
export function primaryVerbs(e: J): J[] {
  const offered = offeredVerbs(e);
  return detailVerbs().map((verb) => offered.find((v) => v.verb === verb)).filter(Boolean);
}

// moreVerbs are the palette items of the verbs e offers past the profile's detail verbs, each running as its
// button would; a verb offered but off says why.
export function moreVerbs(e: J): PaletteItem[] {
  const named = new Set(detailVerbs());
  return offeredVerbs(e).filter((v) => !named.has(v.verb)).map((v) => ({
    label: verbLabel(v.verb),
    key: verbTask(v.verb).key || "",
    meta: v.enabled ? undefined : v.reason,
    run: () => (v.enabled ? runVerb(e, v.verb) : setNotice(`${verbLabel(v.verb)}: ${v.reason || t("not offered here")}`, true)),
  }));
}

export function openMore(e: J): Promise<void> {
  return showPalette({ title: t("Actions"), items: moreVerbs(e) });
}

// relatedVerbs are the verbs e offers that take another task in: merge and relate.
export function relatedVerbs(e: J): string[] {
  return ["merge", "relate"].map((x) => offeredVerb(e, x)).filter(Boolean);
}

// askedArgs asks the value a verb needs first; null when the user backed out.
export async function askedArgs(verb: string): Promise<Record<string, string> | null> {
  const ask = servedAsk(verb, taskCatalog().verbs || []);
  if (!ask || !ask.args) return {};
  const v = await askInput({ label: ask.label });
  if (v === null || v === undefined) return null;
  return ask.args(String(v).trim());
}

function changed(): void {
  scheduler.load({ tasks: true });
}

// taskIntentAct runs the intent action id on the task's own unit, such as start, and asks for a reload.
export async function taskIntentAct(task: J, id: string): Promise<void> {
  const path = "/api/units/" + ["task", task.id, id].map(encodeURIComponent).join("/");
  try {
    const out = await postJSON(path, { source: "cockpit" });
    setNotice(`${id}: ${task.title || task.id}${out && out.output && out.output.length ? ". " + out.output.join(" ") : ""}`);
    changed();
  } catch (err) {
    setNotice(t("{verb} {task} failed: {error}", { verb: id, task: task.title || task.id, error: (err as Error).message }), true);
  }
}

// approveEdited approves the draft of e with the text being edited.
export async function approveEdited(e: J): Promise<J> {
  const d = draftOf(e);
  const out = await taskAct(draftTarget(e), verbFor("approve"), { ...editedText(d, e.id), ...editedPayload(d, e.id), rev: String(d.rev) });
  if (out) stopEditing(e.id);
  return out;
}

// actOn runs verb on task; an approve while its draft is being edited sends the edited text.
export function actOn(task: J, verb: string, args?: Record<string, unknown> | null): Promise<J> {
  const d = task ? draftOf(task) : null;
  const handler = verbHandler(verb);
  if (task && handler === "approve" && d && editing.has(task.id)) return approveEdited(task);
  if (task && d && task.id.startsWith("draft:") && (handler === "approve" || handler === "retry")) args = { ...args, rev: String(d.rev) };
  return taskAct(task, verb, args);
}

// pickTask asks for the other task verb takes on e, from the tasks its pick offers, and runs verb with it.
export function pickTask(e: J, verb: string): Promise<void> {
  const spec = verbTask(verb);
  return taskPicker({
    task: e,
    ask: { label: spec.input || spec.label || verb, pick: spec.pick },
    match: async (q) => {
      try {
        return (await getJSON(matchPath(q, e.id))).tasks || [];
      } catch (err) {
        setNotice(t("Could not list the tasks: {error}", { error: (err as Error).message }), true);
        return [];
      }
    },
    palette: showPalette,
    stateLabel,
    run: (id) => taskAct(e, verb, { [PICK_ARG]: id }),
  });
}

// runVerb runs verb on e: over the tasks it picks from, or after the question it puts first.
export async function runVerb(e: J, verb: string): Promise<J> {
  if (verbTask(verb).pick) return pickTask(e, verb);
  const args = await askedArgs(verb);
  return args ? taskAct(e, verb, args) : null;
}

// TASK_TABS are the task page's tabs in their order: each one's label, whether e has anything to show in it and
// the count its label carries. Timeline always shows.
export interface TaskTab {
  id: string;
  label: () => string;
  shows: (e: J) => boolean;
  count: (e: J) => number;
}

export function relatedCount(e: J): number {
  return (e.related || []).length + (e.children || []).length + (e.merged_from || []).length;
}

// timelineCount is how many events the timeline's facts fold.
export function timelineCount(): number {
  return (taskState.timeline || []).reduce((n: number, node: J) => n + (node.events || []).length, 0);
}

export const TASK_TABS: TaskTab[] = [
  { id: "brief", label: () => t("Brief"), shows: (e) => !!e.brief, count: () => 0 },
  { id: "timeline", label: () => t("Timeline"), shows: () => true, count: timelineCount },
  { id: "asks", label: () => t("Asks"), shows: (e) => (e.asks || []).length > 0, count: (e) => (e.asks || []).length },
  { id: "related", label: () => t("Related"), shows: (e) => relatedCount(e) > 0, count: relatedCount },
  { id: "runs", label: () => t("Runs"), shows: (e) => (e.runs || []).length > 0, count: (e) => (e.runs || []).length },
];

// shownTabs are the tabs e has something in, in their order.
export function shownTabs(e: J): TaskTab[] {
  return TASK_TABS.filter((x) => x.shows(e));
}

// selectedTab is the tab picked when e shows it, else the first e shows.
export function selectedTab(e: J): string {
  const shown = shownTabs(e);
  return (shown.find((x) => x.id === taskState.tab) || shown[0]).id;
}

// pickTab shows tab id; focus moves the focus to its tab, as the arrow keys do.
export function pickTab(id: string, focus = false): void {
  taskState.tab = id;
  if (!focus || typeof document === "undefined") return;
  const el = document.querySelector<HTMLElement>(`[data-focus="task:tab:${id}"]`);
  if (el) el.focus();
}

// stepTab shows the tab delta tabs from the one shown on e, wrapping around; false when e has one tab.
export function stepTab(e: J, delta: number, focus = false): boolean {
  if (!e) return false;
  const shown = shownTabs(e);
  if (shown.length < 2) return false;
  const at = shown.findIndex((x) => x.id === selectedTab(e));
  pickTab(shown[(at + delta + shown.length) % shown.length].id, focus);
  return true;
}

export const TAB_KEYS: Record<string, number> = { ArrowRight: 1, ArrowLeft: -1 };

// detailPane is what the keys reach: the selected task, its verbs, its load and its tabs.
export const detailPane = {
  focused: (): string => {
    const el = typeof document !== "undefined" && document.activeElement ? document.activeElement.closest<HTMLElement>('[data-row^="task:"]') : null;
    return el ? (el.dataset.row || "").slice("task:".length).replace(/@.*$/, "") : "";
  },
  selected: (): J => taskById(detailPane.focused()) || detailState.task,
  act: (verb: string, args?: Record<string, unknown>, task?: J) => actOn(task || detailPane.selected(), verb, args),
  load: (route: { task: string; unit: string }) => loadDetailPane(route),
  tab: (dir: string) => stepTab(detailState.task, dir === "prev" ? -1 : 1, true),
};

export function closeDetail(): void {
  const { task } = readHash();
  writeHash({ task: "" });
  if (task) void rescueFocus(document, taskSlot(task));
}

// propText is a property's value as a person reads it: a name, or the word facet f gives the value.
export function propText(v: unknown, f?: J): string {
  if (v === null || v === undefined) return "";
  if (Array.isArray(v)) return v.map((x) => propText(x, f)).filter(Boolean).join(", ");
  if (typeof v === "object") return (v as J).name || (v as J).key || "";
  const labels = (f && f.labels) || {};
  return Object.hasOwn(labels, String(v)) ? labels[String(v)] : String(v);
}

// taskFacetOf is the profile's task facet that reads fact, null when none does.
export function taskFacetOf(fact: string): J {
  return (taskCatalog().facets || []).find((f: J) => f.fact === fact && !f.provider) || null;
}

// LISTED_ROLES are the roles of the properties the page lists as tasks, not as text.
const LISTED_ROLES = ["related", "merged"];

export function briefData(e: J): J {
  const r = e.brief;
  return r && r.data && typeof r.data === "object" ? r.data : {};
}

// tagMax is how many brief tags the props show before they count the rest, as the profile's tag facet says.
export function tagMax(): number {
  const spec = (taskCatalog().facets || []).find((f: J) => f.fact === "brief.tags");
  return (spec && spec.show_max) || 0;
}

// moreWord is the lens's word for n more pieces.
export function moreWord(n: number): string {
  const w = lensWords().more;
  return w ? fillWord(w, { n: fmtNumber(n) }) : "+" + fmtNumber(n);
}

export interface SignalWords {
  label: string;
  text: string;
  notes: { tone: string; text: string }[];
}

// signalWords are a card signal in its plugin's words (the catalog's card_signals): its name, and its value
// filled with its params followed by each note in its tone; value is how an instant value shows. A code with no
// words shows as itself, and an empty one as its key.
export function signalWords(provider: string, g: J, value: (at: string) => string): SignalWords {
  const spec = catalog.cardSignals.find((s) => s.provider === provider && s.key === g.key) || {};
  const word = (spec.values || {})[g.value];
  const notes = (g.notes || []).map((n: J) => {
    const w = (spec.notes || {})[n.code];
    return { tone: n.tone || "neutral", text: w ? fillWord(w, n.params || {}) : n.code };
  });
  return { label: spec.label || g.key, text: g.at ? value(g.at) : word ? fillWord(word, g.params || {}) : g.value || g.key, notes };
}

export function stamp(iso: string): string {
  return isSet(iso) ? fmtDate(iso, "datetime") : "";
}

export type PropRow =
  | { key: string; kind: "text"; label: string; text: string; prop?: string }
  | { key: string; kind: "risk"; label: string; risk: string; text: string; why: string }
  | { key: string; kind: "signal"; label: string; tone: string; words: SignalWords }
  | { key: string; kind: "tags"; label: string; tags: string[]; more: number };

// propRows are the task's facts as label and value rows: the properties the profile declares, in its order, the
// brief's risk with its reason and its size, each card's signals in their tone, then the brief's tags, the first
// the tag facet shows and the count of the rest.
export function propRows(e: J): PropRow[] {
  const props = e.props || {};
  const rows: PropRow[] = (taskCatalog().props || []).filter((p: J) => !LISTED_ROLES.includes(p.role) && propText(props[p.name]))
    .map((p: J) => {
      const f = taskFacetOf("task.prop." + p.name);
      return { key: "prop:" + p.name, kind: "text", label: f ? f.label : p.name, text: propText(props[p.name], f), prop: p.name };
    });
  const b = briefData(e);
  const risk = taskFacetOf("brief.risk");
  const size = taskFacetOf("brief.size");
  if (b.risk) rows.push({ key: "brief:risk", kind: "risk", label: risk ? risk.label : t("Risk"), risk: b.risk, text: propText(b.risk, risk), why: b.risk_why || "" });
  if (b.size) rows.push({ key: "brief:size", kind: "text", label: size ? size.label : t("Size"), text: propText(b.size, size), prop: "brief:size" });
  for (const c of e.cards || []) {
    const card = c.card || {};
    const u = card.unit || {};
    for (const g of card.signals || []) {
      rows.push({ key: `signal:${u.provider}:${u.key}:${g.key}`, kind: "signal", label: "", tone: g.tone || "neutral", words: signalWords(u.provider, g, (at) => fmtDate(at, "datetime")) });
    }
  }
  const tags: string[] = b.tags || [];
  const max = tagMax() || tags.length;
  if (tags.length) rows.push({ key: "tags", kind: "tags", label: t("Tags"), tags: tags.slice(0, max), more: Math.max(0, tags.length - max) });
  return rows;
}

// cardOf is the card of e's unit u, null when it has none.
export function cardOf(e: J, u: J): J {
  return (e.cards || []).find((x: J) => x.card && x.card.unit && x.card.unit.provider === u.provider && x.card.unit.key === u.key) || null;
}

export function unitLabels(e: J): string[] {
  return (e.units || []).map((u: J, i: number) => (e.unit_labels || [])[i] || unitLabel(u));
}

// titleWithoutRef is title without a leading unit label of labels and the separator after it; the whole title
// when nothing would be left.
export function titleWithoutRef(title: string, labels: string[]): string {
  const s = String(title || "");
  for (const label of labels || []) {
    if (!label || !s.startsWith(label)) continue;
    const rest = s.slice(label.length).replace(/^\s*[:\-–—|]?\s*/, "");
    if (rest && rest !== s.slice(label.length)) return rest;
  }
  return s;
}

// draftStateWord is the profile's word for a draft's state, capitalized as a heading.
export function draftStateWord(state: string): string {
  const w = (taskCatalog().draft_states || {})[state] || state;
  return w.charAt(0).toUpperCase() + w.slice(1);
}

function subjectOf(g: J): J {
  return g && g.subject && typeof g.subject === "object" ? g.subject : {};
}

export interface TaskGates {
  heading: string;
  gates: J[];
  own: J;
  drafts: J[];
}

// taskGates are the decisions that wait on task e: its open gates and its drafts, each once. A draft an open
// question pins is that question's to decide: it shows under the option that pins it, never as a card of its own.
export function taskGates(e: J): TaskGates | null {
  const own = isDraftTask(e) ? draftOf(e) : null;
  const drafts = (taskState.id === e.id ? taskState.drafts : []).filter((d: J) => (!own || d.id !== own.id) && !d.pinned_by);
  const gates = (e.gates || []).filter((g: J) => g.status === "open" && !(own && g.subject === own) && !subjectOf(g).pinned_by && !drafts.some((d: J) => d.gate && d.gate === g.id));
  if (!gates.length && !own && !drafts.length) return null;
  const heading = !gates.length && !drafts.length && own && !["held", "failed"].includes(own.state) ? draftStateWord(own.state) : t("Waiting on you");
  return { heading, gates, own, drafts };
}

const RUN_PATHS: Record<string, string> = { open: "/api/runs/{seq}/open", enter: "/api/runs/{seq}/enter", transcript: "/api/runs/{seq}/transcript" };

export function runPath(action: string, seq: number | string): string {
  return RUN_PATHS[action].replace("{seq}", encodeURIComponent(seq));
}

export async function runAct(r: J, action: string, label: string): Promise<void> {
  try {
    await postJSON(runPath(action, r.seq), { source: "cockpit" });
    setNotice(`${label}: ${r.intent}`);
  } catch (err) {
    setNotice(t("{action} failed: {error}", { action: label, error: (err as Error).message }), true);
  }
}

export function transcriptLines(out: J): string[] {
  const list = Array.isArray(out) ? out : (out && (out.lines || out.turns || out.messages)) || [];
  return list.map((x: J) => (typeof x === "string" ? x : [x.role || x.actor || "", x.text || ""].filter(Boolean).join(": ")));
}

export async function runTranscript(r: J): Promise<void> {
  try {
    const out = await getJSON(runPath("transcript", r.seq));
    taskState.transcript = { seq: r.seq, lines: transcriptLines(out) };
  } catch (err) {
    taskState.transcript = { seq: r.seq, error: t("Could not load the transcript: {error}", { error: (err as Error).message }) };
  }
}

// runStateWord is state in the words the catalog serves from the profile's table, else as the daemon names it.
export function runStateWord(table: string, state: string): string {
  const words = taskCatalog()[table] || {};
  return Object.hasOwn(words, state) ? words[state] : state;
}

export async function pickKinds(e: J, kinds: string[]): Promise<void> {
  taskState.kinds = kinds;
  await loadTaskPage(e);
}

// sendChat writes the composer's text to the task's agent.
export async function sendChat(e: J): Promise<J> {
  if (taskState.sending) return null;
  const text = taskState.text.trim();
  if (!text) {
    setNotice(t("Write a message first"), true);
    return null;
  }
  taskState.sending = true;
  setNotice(t("Sending to {task}…", { task: e.title || e.id }));
  try {
    const out = await postJSON(taskPath(e.id, "chat"), { text });
    taskState.text = "";
    setNotice(t("Sent to {task}", { task: e.title || e.id }));
    await loadTaskPage(e);
    return out || {};
  } catch (err) {
    setNotice(t("Sending failed: {error}", { error: (err as Error).message }), true);
    return null;
  } finally {
    taskState.sending = false;
  }
}

// replyBroadcasts says whether e's reply also goes to the conversation: as the composer's checkbox was turned,
// else as served.
export function replyBroadcasts(e: J): boolean {
  return taskState.broadcast === null ? e.reply_broadcast === "on" : !!taskState.broadcast;
}

// replyWith writes the composer's text as a reply in the task's conversation, through the verb whose handler
// replies: send "now" posts it, "draft" holds it for approval.
export async function replyWith(e: J, verb: string, send: string): Promise<J> {
  const text = taskState.text.trim();
  if (!text) {
    setNotice(t("Write a message first"), true);
    return null;
  }
  const args: Record<string, string> = { text, send };
  if (e.reply_broadcast) args.broadcast = String(replyBroadcasts(e));
  const out = await taskAct(e, verb, args);
  if (out) {
    taskState.text = "";
    taskState.broadcast = null;
  }
  return out;
}

export interface ReplyButton {
  label: string;
  send: string;
  focus: string;
  first: boolean;
}

// replyButtons are Send reply and Save as draft, the one the reply verb's served send names first and marked
// default.
export function replyButtons(): ReplyButton[] {
  const now = verbTask(handlerVerb("reply")).send === "now";
  const out = [
    { label: t("Send reply"), send: "now", focus: "task:compose:reply", first: now },
    { label: t("Save as draft"), send: "draft", focus: "task:compose:draft", first: !now },
  ];
  return now ? out : out.reverse();
}

// eventText is the prose an event carries, "" for none.
export function eventText(ev: J): string {
  const d = ev.data && typeof ev.data === "object" ? ev.data : {};
  return d.text || d.summary || d.title || d.line || "";
}

// timelineStamp is when a fact happened: its time on the day it is read, else its date and time.
export function timelineStamp(iso: string, now: number): string {
  return fmtDate(iso, new Date(iso).toDateString() === new Date(now).toDateString() ? "time" : "datetime");
}

// nodeKey names a timeline node across reads by its first event, which no other node holds.
export function nodeKey(n: J): string {
  const evs = n.events || [];
  return evs.length ? "seq:" + evs[0].seq : "at:" + n.rule + ":" + n.at;
}

export function viewHref(id: string): string {
  return "#" + viewHash({ task: id, pr: "" });
}
