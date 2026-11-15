import { SvelteMap } from "svelte/reactivity";
import { fmtDate, t } from "../../lib/i18n.svelte";
import { unitPath } from "../../lib/routes";
import { draftOf, isDraftTask } from "../../lib/tasks";
import { safeURL, type J } from "../../lib/util";
import { scheduler, setNotice } from "../../state/actions.svelte";
import { afterSync } from "../../state/sync.svelte";
import { postJSON } from "../../state/api";
import { viewHash } from "../../state/route.svelte";
import { editing, editPayloads, editRevs, gateState, state, tasksState } from "../../state/store.svelte";
import {
  offeredVerb, propText, startEditing, stopEditing, taskAct, taskCatalog as catalogWords, taskFacetOf, taskPath, verbBody, verbFor, verbHandler,
  verbLabel, verbState, verbTask,
} from "../../state/verbs";

// The one decision card every surface draws: a task's open gate (a question, a plan, a post, a proposal) or a
// draft, with its actions. The detail draws them; the Outbox and the queues draw the same components.

function changed(): void {
  scheduler.load({ tasks: true });
}

// resolving are the cards whose decision is on its way, by "gate:<id>" or "draft:<id>", with what the subject
// looked like when it was sent: the card stays dimmed until the daemon serves it otherwise, and a failed one
// comes back at once.
export const resolving = new SvelteMap<string, string>();

// answered are the cards whose decision the daemon took; the read that follows lets them go, whatever it serves.
const answered = new Set<string>();
afterSync.add(() => {
  for (const mark of answered) resolving.delete(mark);
  answered.clear();
});

const gateMark = (g: J): string => String(g.status || "");
const draftMark = (d: J): string => `${d.state}:${d.rev}`;

export function gateResolving(g: J): boolean {
  const k = "gate:" + g.id;
  return resolving.has(k) && resolving.get(k) === gateMark(g);
}

export function draftResolving(d: J): boolean {
  const k = "draft:" + d.id;
  return resolving.has(k) && resolving.get(k) === draftMark(d);
}

export function subjectOf(g: J): J {
  return g && g.subject && typeof g.subject === "object" ? g.subject : {};
}

// gateOption is the option picked on g's card: the person's pick, else the recommended one unless it pins a
// draft, which is sent only once the person picks it; "" for none.
export function gateOption(g: J): string {
  const id = String(g.id);
  if (gateState.picked.has(id)) return gateState.picked.get(id) as string;
  const subject = subjectOf(g);
  const recommended = (subject.options || []).find((o: J) => o.id === subject.recommend);
  return recommended && !recommended.draft ? recommended.id : "";
}

// orderedOptions are a question's options, the recommended first.
export function orderedOptions(g: J): J[] {
  const subject = subjectOf(g);
  const opts = subject.options || [];
  return [...opts.filter((o: J) => o.id === subject.recommend), ...opts.filter((o: J) => o.id !== subject.recommend)];
}

export function pickOption(g: J, id: string): void {
  gateState.picked.set(String(g.id), gateOption(g) === id ? "" : id);
}

// resolveGate resolves gate g of task e with intent id; an intent that takes an answer sends the option given,
// else the one picked on the card, then the text typed in it.
export async function resolveGate(e: J, g: J, id: string, option?: string): Promise<J> {
  const intent = (g.intents || []).find((x: J) => x.id === id);
  if (!intent) return null;
  const key = String(g.id);
  const args: Record<string, string> = {};
  if (intent.answer) {
    const pick = option === undefined ? gateOption(g) : option;
    const text = (gateState.answers.get(key) || "").trim();
    if (!text && !pick) {
      setNotice(t("{action}: write the answer first", { action: intent.label }), true);
      return null;
    }
    args.answer = [pick, text].filter(Boolean).join("\n");
  }
  const mark = "gate:" + g.id;
  resolving.set(mark, gateMark(g));
  try {
    const out = await postJSON("/api/gates/" + encodeURIComponent(key) + "/" + encodeURIComponent(id), verbBody(args));
    setNotice(`${intent.label}: ${(e && (e.title || e.id)) || g.text || g.kind}`);
    gateState.answers.delete(key);
    gateState.picked.delete(key);
    answered.add(mark);
    changed();
    return out || {};
  } catch (err) {
    resolving.delete(mark);
    setNotice(t("{action} failed: {error}", { action: intent.label, error: (err as Error).message }), true);
    changed();
    return null;
  }
}

// gateAt is the gate whose card holds el, with its task, among sources: each a task and the gates the page
// draws of it.
export function gateAt(el: Element | null | undefined, sources: { task: J; gates?: J[] }[]): { task: J; gate: J } | null {
  const card = el && el.closest ? el.closest(".d-gate") : null;
  if (!card) return null;
  const id = card.getAttribute("data-gate");
  for (const s of sources) {
    const g = (s.gates || []).find((x: J) => String(x.id) === id);
    if (g) return { task: s.task, gate: g };
  }
  return null;
}

// planFactText is a fact that made a plan item, in the words of the profile's task facet that reads it, else as
// name = value.
export function planFactText(f: J): string {
  const facet = taskFacetOf(f.name);
  return facet ? `${facet.label}: ${propText(f.value, facet)}` : `${f.name} = ${f.value}`;
}

export interface OutLink {
  label: string;
  href?: string;
}

const outLink = (href: unknown, label: string): OutLink => ({ label, href: safeURL(href) });

export interface PlanTargetData {
  task?: { href: string; focus: string; title: string };
  links: OutLink[];
}

// planTarget is what a plan item acts on: its task's title, linked to the task's detail, with the units the task
// came from, each linked when it has a link; else its unit as its plugin labels it. Null when it names none.
export function planTarget(p: J): PlanTargetData | null {
  const tg = p.target;
  if (tg) {
    return { task: { href: taskHref(tg.task), focus: "plan-task:" + tg.task, title: tg.title || tg.task }, links: (tg.units || []).map((u: J) => outLink(u.link, u.label)) };
  }
  const label = p.label || (p.unit && p.unit.key) || "";
  return label ? { links: [outLink(p.link, label)] } : null;
}

// stepText is a plan step in the catalog's words the daemon names, filled with its arguments as a person reads
// them; the step's name when the catalog has no words for it.
export function stepText(st: J): string {
  if (!st.says) return st.use;
  const args = st.args || {};
  const prop = args.prop ? taskFacetOf("task.prop." + args.prop) : null;
  return t(st.says, {
    ...args,
    resolution: args.resolution_says ? t(args.resolution_says) : args.resolution || "",
    intent: args.intent_label || args.intent || "",
    task: args.task_title || "",
    verb: args.verb ? verbLabel(args.verb) : "",
    prop: prop ? prop.label : args.prop || "",
    value: propText(args.value, prop),
  });
}

export interface PlanDoesData {
  steps: { text: string; use: string; items: string[] }[];
  postsNothing: boolean;
}

// planDoes is what applying a plan item does: each step in the catalog's words with the open items it resolves,
// and whether no step may post. Null when it does nothing.
export function planDoes(p: J): PlanDoesData | null {
  const does = p.does || [];
  if (!does.length) return null;
  return { steps: does.map((st: J) => ({ text: stepText(st), use: st.use, items: st.items || [] })), postsNothing: !does.some((st: J) => st.posts) };
}

// planWhy is why a plan item fired: each fact its workflow matched, worded as planFactText words it, with its raw
// form as its title. Null when none matched.
export function planWhy(p: J): { text: string; title: string }[] | null {
  const facts = (p.why && p.why.matched) || [];
  return facts.length ? facts.map((f: J) => ({ text: planFactText(f), title: `${f.name} = ${f.value}` })) : null;
}

// planReason is why the plan holds item p, in the catalog's words: the engine's template filled with its values, a
// wait's facts as planFactText words them and a refusal through the catalog; any other reason through the catalog,
// as it reads when the catalog has no words for it.
export function planReason(p: J): string {
  if (!p.reason_key) return t(p.reason);
  const params: Record<string, unknown> = { ...(p.reason_params || {}) };
  if ((p.reason_facts || []).length) params.facts = p.reason_facts.map(planFactText).join(", ");
  if (params.refusal) params.refusal = t(String(params.refusal));
  return t(p.reason_key, params);
}

// taskHref is the cockpit's link to task id.
export function taskHref(id: string): string {
  return "#" + (viewHash({ task: id, pr: "" }) || "task=" + encodeURIComponent(id));
}

// DRAFT_HANDLERS are the handlers of a draft's delivery actions; DRAFT_CARD_HANDLERS those of every verb its
// card offers on the draft's own task, the kind's own dismiss among them.
export const DRAFT_HANDLERS: Record<string, string> = { "draft.edit": "edit", "draft.retry": "retry" };
export const DRAFT_CARD_HANDLERS = ["approve", "drop", "edit", "retry"];

// draftWord is the label of a draft's action id, from the served specs, else its verb's.
export function draftWord(id: string): string {
  const sp = state.specs.byId[id];
  return (sp && sp.label) || verbLabel(verbFor(DRAFT_HANDLERS[id]));
}

// gateWord is the label gates.toml gives the intent of d's gate on e that resolves it to status, else verb's.
export function gateWord(e: J, d: J, status: string, verb: string): string {
  const name = (d.intents || {})[status];
  const g = ((e && e.gates) || []).find((x: J) => x.subject === d || (d.gate && x.id === d.gate));
  const intent = g && name ? (g.intents || []).find((x: J) => x.id === name) : null;
  return (intent && intent.label) || verbLabel(verb);
}

// unitSkipPath is the path that stops d's workflow on d's unit, "" when d names no workflow.
export function unitSkipPath(d: J): string {
  const at = String(d.unit || "").indexOf("/");
  const workflow = (d.origin || {}).workflow;
  if (at < 1 || !workflow) return "";
  return unitPath({ provider: d.unit.slice(0, at), key: d.unit.slice(at + 1) }, "skip", workflow);
}

export interface DraftOffer {
  key: string;
  label: () => string;
  path: (d: J) => string;
  body: (d: J) => J;
}

// DRAFT_OFFERS are the draft actions a card offers only when the draft serves them: their words, and the request
// each sends.
export const DRAFT_OFFERS: Record<string, DraftOffer> = {
  "draft.close-silently": { key: "queue:close-silently", label: () => t("Close without a message"), path: (d) => "/api/drafts/" + encodeURIComponent(d.id) + "/close-silently", body: (d) => ({ rev: d.rev }) },
  "draft.skip-workflow": { key: "queue:skip-workflow", label: () => t("Don't nudge this conversation"), path: unitSkipPath, body: () => ({ source: "cockpit" }) },
};

export async function runDraftOffer(d: J, label: string, offer: DraftOffer): Promise<void> {
  try {
    await postJSON(offer.path(d), offer.body(d));
    setNotice(`${label}: ${d.where || d.target || d.unit}`);
    changed();
  } catch (err) {
    setNotice(t("{action} failed: {error}", { action: label, error: (err as Error).message }), true);
  }
}

// DraftAction is a button of a draft's card: its words, whether it runs, why not, its focus slot within the
// card's scope, the queue key that runs it and what it runs.
export interface DraftAction {
  label: string;
  enabled: boolean;
  reason?: string;
  title?: string;
  focus: string;
  key?: string;
  primary?: boolean;
  run: () => unknown;
}

// offeredDraftActions are the offered draft actions d serves, each off with its reason where refused.
export function offeredDraftActions(d: J): DraftAction[] {
  return (d.actions || []).filter((a: J) => DRAFT_OFFERS[a.id] && DRAFT_OFFERS[a.id].path(d)).map((a: J) => {
    const offer = DRAFT_OFFERS[a.id];
    const label = a.label || offer.label();
    return { label, enabled: !!a.enabled, reason: a.reason, title: a.enabled ? a.note : a.reason, focus: `${a.id}:draft:${d.id}`, key: offer.key, run: () => runDraftOffer(d, label, offer) };
  });
}

// editableFields says whether fields hold one a person may change.
export function editableFields(fields: J[]): boolean {
  return (fields || []).some((f: J) => (f.editable && (f.options || []).length) || editableFields(f.fields));
}

// draftEditable says whether a person may change anything of d: its text, unless its kind locks it, or a field.
export function draftEditable(d: J): boolean {
  return !d.text_locked || editableFields(d.fields);
}

// draftKey is the key d's edits live under on e's card: e's id on d's own task, else the draft's.
export function draftKey(e: J, d: J): string {
  return ownDraft(e, d) ? e.id : "draft:" + d.id;
}

export function ownDraft(e: J, d: J): boolean {
  return !!e && isDraftTask(e) && draftOf(e).id === d.id;
}

// editConflict says whether d, edited under key, changed since its edit began; its field edits then no longer
// count, since they were made over values that may have changed.
export function editConflict(d: J, key: string): boolean {
  const since = editRevs.get(key);
  return since !== undefined && since !== d.rev;
}

// fieldEdits are the field edits of d edited under key that still count: those made over the revision d is at
// now; an edit made before d changed was made over values that may have changed.
export function fieldEdits(d: J, key: string): Record<string, string> {
  const e = editPayloads.get(key);
  return e && e.rev === d.rev ? e.fields : {};
}

// setFieldEdit records a field edit of d, edited under key, over the revision d is at; edits over an older one go.
export function setFieldEdit(key: string, d: J, pointer: string, value: string): void {
  const e = editPayloads.get(key);
  const fields = e && e.rev === d.rev ? e.fields : {};
  editPayloads.set(key, { rev: d.rev, fields: { ...fields, [pointer]: value } });
}

// editedText is the text arg of an approval of d edited under key; none when its kind locks its text.
export function editedText(d: J, key: string): Record<string, string> {
  return d.text_locked ? {} : { text: editing.get(key) as string };
}

// editedPayload is the payload arg of an approval of d edited under key: d's payload with the fields changed,
// none when nothing changed.
export function editedPayload(d: J, key: string): Record<string, string> {
  const edits = Object.entries(fieldEdits(d, key));
  if (!edits.length || !d.payload) return {};
  const out = JSON.parse(JSON.stringify(d.payload));
  for (const [pointer, v] of edits) setPointer(out, pointer, v);
  return { payload: JSON.stringify(out) };
}

// setPointer sets the value at JSON pointer in doc.
export function setPointer(doc: J, pointer: string, v: unknown): void {
  const path = pointer.split("/").slice(1).map((p) => p.replaceAll("~1", "/").replaceAll("~0", "~"));
  const last = path.pop() as string;
  path.reduce((at, p) => at[p], doc)[last] = v;
}

// windowLine says when the window of d's gate sends it, "" when it has none.
export function windowLine(d: J): string {
  return d.deadline ? t("Sends at {time} unless you decide first", { time: fmtDate(d.deadline, "datetime") }) : "";
}

// draftEffects are the profile's sentences for what sending d does besides posting it.
export function draftEffects(d: J): string[] {
  const words = catalogWords().effects || {};
  return (d.effects || []).map((x: string) => words[x]).filter(Boolean);
}

// draftHead is the card's heading: the profile's word for d's purpose and, unless held, its state, with why a
// dropped one was dropped.
export function draftHead(d: J): string {
  const words = catalogWords();
  const purpose = d.purpose ? (words.draft_purposes || {})[d.purpose] || d.purpose : "";
  const st = d.state && d.state !== "held" ? (words.draft_states || {})[d.state] || d.state : "";
  const why = d.state === "dropped" && d.reason_text ? `: ${d.reason_text}` : "";
  return (purpose ? t("Draft {purpose}", { purpose }) : t("Draft")) + (st ? ` (${st}${why})` : "");
}

// itemStateWord is the word for an item's state, open or resolved.
export function itemStateWord(st: string): string {
  if (st === "open") return t("open");
  if (st === "resolved") return t("resolved");
  return st;
}

// draftDecided says d left the person's hands for good: posted, or dropped; its card then offers no action.
export function draftDecided(d: J): boolean {
  return d.state === "sent" || d.state === "dropped";
}

// draftTarget is the Outbox task of the draft e carries: e itself, or draft:<id> when e only shows the draft.
export function draftTarget(e: J): J {
  const id = "draft:" + draftOf(e).id;
  return e.id === id ? e : { id, title: e.title, verbs: [] };
}

// approveEdited approves the draft of e with the text and fields being edited.
export async function approveEdited(e: J): Promise<J> {
  const d = draftOf(e);
  const out = await taskAct(draftTarget(e), verbFor("approve"), { ...editedText(d, e.id), ...editedPayload(d, e.id), rev: String(d.rev) });
  if (out) stopEditing(e.id);
  return out;
}

// resolveDraft resolves d's gate with intent, sending args with the draft's version.
export async function resolveDraft(d: J, intent: string, label: string, args: Record<string, unknown>): Promise<void> {
  const mark = "draft:" + d.id;
  resolving.set(mark, draftMark(d));
  try {
    await postJSON("/api/gates/" + encodeURIComponent(d.gate) + "/" + encodeURIComponent(intent), verbBody({ ...args, rev: String(d.rev) }));
    setNotice(`${label}: ${d.where || d.target || d.unit}`);
    stopEditing("draft:" + d.id);
    answered.add(mark);
    changed();
  } catch (err) {
    resolving.delete(mark);
    setNotice(t("{action} failed: {error}", { action: label, error: (err as Error).message }), true);
    changed();
  }
}

const cancelEdit = (key: string): DraftAction => ({ label: t("Cancel"), enabled: true, focus: "cancel:" + key, run: () => stopEditing(key) });

// staleOff turns action a off on stale draft d, with the reason the daemon worded.
const staleOff = (d: J, a: DraftAction | null): DraftAction | null =>
  a && d.stale ? { ...a, enabled: false, reason: (d.notes || [])[0], title: (d.notes || [])[0] } : a;

const primary = { primary: true, key: "queue:primary" };

// ownDraftActions are the actions of the draft e, its own task, carries, through the task's verbs.
function ownDraftActions(e: J, d: J, key: string, isEditing: boolean): (DraftAction | null)[] {
  const [approve, drop, edit, retry] = DRAFT_CARD_HANDLERS.map((x) => offeredVerb(e, x));
  const verb = (v: string, label: string, run: () => unknown, extra: Partial<DraftAction> = {}): DraftAction | null => {
    const st = verbState(e, v);
    if (!st) return null;
    const title = [verbTask(v).summary, st.enabled ? "" : st.reason].filter(Boolean).join("\n");
    return { label, run, enabled: !!st.enabled, reason: st.reason, title, focus: `${v}:${e.id}`, ...extra };
  };
  if (isEditing) return [staleOff(d, verb(approve, gateWord(e, d, "approved", approve), () => approveEdited(e), primary)), cancelEdit(key)];
  return [
    staleOff(d, d.state === "failed" ? verb(retry, draftWord("draft.retry"), () => taskAct(e, retry, { rev: String(d.rev) }), primary)
      : verb(approve, gateWord(e, d, "approved", approve), () => taskAct(e, approve, { rev: String(d.rev) }), primary)),
    draftEditable(d) ? verb(edit || approve, draftWord("draft.edit"), () => startEditing(key, d), { key: "queue:edit", focus: `open:${edit || approve}:${e.id}` }) : null,
    verb(drop, gateWord(e, d, "rejected", drop), () => taskAct(e, drop, {}), { key: "queue:discard" }),
  ];
}

// gateDraftActions are the actions of a draft shown on another task, through its gate's intents; retry goes
// through its own task.
function gateDraftActions(e: J, d: J, key: string, isEditing: boolean): (DraftAction | null)[] {
  const intents = (d.gate && d.intents) || {};
  const approve = verbFor("approve");
  const approveLabel = gateWord(e, d, "approved", approve);
  const act = (label: string, focus: string, run: () => unknown, extra: Partial<DraftAction> = {}): DraftAction => ({ label, run, enabled: true, focus: `${focus}:${key}`, ...extra });
  if (isEditing) {
    return [intents.approved ? staleOff(d, act(approveLabel, approve, () => resolveDraft(d, intents.approved, approveLabel, { ...editedText(d, key), ...editedPayload(d, key) }), primary)) : null,
      cancelEdit(key)];
  }
  const retry = verbFor("retry");
  const drop = verbFor("drop");
  const dropLabel = gateWord(e, d, "rejected", drop);
  return [
    staleOff(d, d.state === "failed" ? act(draftWord("draft.retry"), retry, () => taskAct({ id: key, title: d.title || key }, retry, { rev: String(d.rev) }), primary)
      : intents.approved ? act(approveLabel, approve, () => resolveDraft(d, intents.approved, approveLabel, {}), primary) : null),
    intents.approved && draftEditable(d) ? act(draftWord("draft.edit"), "open:" + approve, () => startEditing(key, d), { key: "queue:edit" }) : null,
    intents.rejected ? act(dropLabel, drop, () => resolveDraft(d, intents.rejected, dropLabel, {}), { key: "queue:discard" }) : null,
  ];
}

// draftActions are the actions draft d's card offers on task e: approve (retry for a failed delivery), edit and
// discard through d's own task's verbs when e is its task, else through its gate's intents, then the offered
// draft actions d serves; while d is being edited, approve with the edits and cancel. None once d is decided.
export function draftActions(e: J, d: J): DraftAction[] {
  if (draftDecided(d)) return [];
  const key = draftKey(e, d);
  const isEditing = editing.has(key);
  const acts = ownDraft(e, d) ? ownDraftActions(e, d, key, isEditing) : gateDraftActions(e, d, key, isEditing);
  return [...acts, ...(isEditing ? [] : offeredDraftActions(d))].filter((a): a is DraftAction => !!a);
}

// broadcastToggle turns whether thread reply d also goes to its conversation, through d's own task's edit verb.
export function broadcastToggle(d: J, on: boolean): Promise<J> {
  const id = "draft:" + d.id;
  return taskAct({ id, title: d.title || id }, verbFor("edit"), { broadcast: String(on), rev: String(d.rev) });
}
