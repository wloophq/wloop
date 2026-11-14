import { RISKS, SIZES, age, intakeWaiting, left, roundGate, stamp, waitingLabel } from "../../lib/lens";
import { rowWords, fillWord, pl, sm } from "../../lib/lens";
import { fmtDate, t } from "../../lib/i18n.svelte";
import { routeHash } from "../../lib/routes";
import { isSet, keyOf, unitOf, type J } from "../../lib/util";
import { clock } from "../../state/clock.svelte";
import { handlerVerb } from "../../state/loaders.svelte";
import { readHash, writeHash } from "../../state/route.svelte";
import { rescueFocus } from "../keys/focus";
import { catalog, drafts, state, tasksState } from "../../state/store.svelte";
import { openSlot } from "../../lib/lens";

export const TAG_CHIPS = 3;
export const ARTIFACT_LINES = 40;
export const RESOLVED_ITEMS_SHOWN = 3;

// detailOf is the unit detail the route opens, null when the route names none, a task, or a unit not loaded yet.
export function detailOf(): J {
  const { pr, task } = readHash();
  const d = state.detail;
  return !task && pr && d && keyOf(d.row) === pr ? d : null;
}

export function closeDetail(): void {
  const { pr } = readHash();
  writeHash({ pr: "" });
  if (pr) void rescueFocus(document, openSlot(pr) || `row:${pr}`);
}

export function countdownOf(r: J): { deadline: string; provider: string; title: string; text: string } {
  const deadline = roundGate(r).deadline;
  const provider = unitOf(r).provider;
  const w = rowWords(provider);
  return { deadline, provider, title: fillWord(w.countdown_title, { time: stamp(deadline) }), text: left(deadline, w) };
}

function resolveWho(r: J, w: string): string {
  if (w !== "author") return w;
  const viewer = state.health && state.health.viewer;
  const author = pl(r).author || "";
  return viewer && author.toLowerCase() === viewer.toLowerCase() ? "me" : author;
}

// answerNote is the text typed in the unit's box: alone it is the answer, with a choice a note to it.
export function answerNote(r: J): string {
  return (drafts.guidance[keyOf(r)] || "").trim();
}

export interface Choice {
  slot: string;
  label: string;
  rec: boolean;
  extra: J;
  title?: string;
}

function served(r: J, name: string): boolean {
  return (r.actions || []).some((a: J) => a.id === name);
}

// questionChoices are the one-click answers of a row's question; an answer that approves a pinned draft is none,
// since the row does not show the draft: the person answers it on the question's card.
export function questionChoices(r: J, q: J): Choice[] {
  if (!served(r, "answer")) return [];
  if (q.kind === "confirm") {
    const out: Choice[] = [];
    if (!q.draft) out.push({ slot: "yes", label: t("Yes"), rec: q.recommend === "yes", extra: { yes: true } });
    out.push({ slot: "no", label: t("No"), rec: q.recommend === "no", extra: { no: true } });
    return out;
  }
  if (q.kind !== "choice") return [];
  const opts = q.options || [];
  const ordered = [...opts.filter((o: J) => o.id === q.recommend), ...opts.filter((o: J) => o.id !== q.recommend)];
  return ordered.filter((o: J) => !o.draft).map((o: J) => ({ slot: `option:${o.id}`, label: o.label, rec: o.id === q.recommend, extra: { option_id: o.id }, title: o.consequence }));
}

// questionPins says whether an answer to q approves a draft.
export function questionPins(q: J): boolean {
  return !!q.draft || (q.options || []).some((o: J) => o.draft);
}

// answerableQuestion is the detail's question whose options the keys pick: asked, its answer not queued.
export function answerableQuestion(d: J): J {
  const q = d && d.row.question;
  return q && q.state === "asked" && !q.queued ? q : null;
}

// questionRecord is the record the open question comes from: its summary and artifact.
export function questionRecord(d: J): J {
  const id = d.row.question && d.row.question.record_id;
  const found = ((d.turn && d.turn.records) || []).find((x: J) => x.id === id);
  return (found && found.record) || {};
}

export function guidanceLabel(r: J): string {
  if (r.question && r.question.queued) return t("Replace the queued answer");
  if (r.question) return r.question.state === "asked" && questionChoices(r, r.question).length ? t("Or answer in your own words") : t("Answer in your own words");
  const start = Object.values(state.specs.byId).find((sp: J) => sp.group === "start" && sp.verb === "approve") as J;
  if (!roundGate(r)) return t("Guidance for the next round (optional)");
  return start ? t("Guidance for the pending round; {action} sends it at once", { action: start.label }) : t("Guidance for the pending round");
}

export interface Artifact {
  kind: string;
  text: string;
  lines: number;
}

export function artifactOf(a: J): Artifact | null {
  if (!a) return null;
  const items = (a.items || []).map((it: J) => `${it.path}:${it.line}\n${it.body}`);
  const text = [a.body, ...items].filter(Boolean).join("\n\n").replace(/\n+$/, "");
  if (!text) return null;
  return { kind: a.kind ? a.kind.replaceAll("_", " ") : t("artifact"), text, lines: text.split("\n").length };
}

// answerSaid leaves out the option a client already worded as the text.
export function answerSaid(a: J): string {
  const choice = a.option_id ? `option ${a.option_id}` : a.yes ? "yes" : a.no ? "no" : "";
  const note = (a.text || "").trim();
  if (!choice || (a.option_id && note.endsWith(`(${choice})`))) return note || choice || "no";
  return note ? `${choice}. ${note}` : choice;
}

// timelineEvents shows the current turn's answers from the turn, which knows where each came from, in place of
// their one-line events; each has an id that stays the same across syncs.
export function timelineEvents(d: J): J[] {
  const turn = d.turn;
  const answers = turn ? (turn.answers || []).filter((a: J) => a.turn_seq === turn.turn.seq) : [];
  const since = turn ? Date.parse(turn.turn.sent_at) || 0 : 0;
  const events = (d.events || []).filter((e: J) => !(answers.length && e.kind === "answered" && Date.parse(e.at) >= since));
  for (const a of answers) {
    const vars = { answer: answerSaid(a), source: a.source };
    const data = a.route === "queued" ? t("{answer} (from {source}, queued until the agent stops)", vars) : t("{answer} (from {source})", vars);
    events.push({ kind: "answered", at: a.at, actor: "", data, answer: a.id ?? "" });
  }
  const seen = new Map<string, number>();
  return events
    .sort((a: J, b: J) => Date.parse(b.at) - Date.parse(a.at))
    .map((e: J) => {
      const base = [e.at, e.kind, e.key ?? "", e.answer ?? "", e.data ?? ""].join("|");
      const n = seen.get(base) || 0;
      seen.set(base, n + 1);
      return { ...e, id: n ? `${base}#${n}` : base };
    });
}

// trimmedBlocks names the profile blocks the current turn's guide left out to stay within the judgment limit.
export function trimmedBlocks(d: J): string {
  const id = d.turn && d.turn.turn && d.turn.turn.id;
  const e = id && (d.events || []).find((x: J) => x.kind === "guide_trimmed" && x.key === id);
  return e ? e.data : "";
}

// intakeLines say what a waiting intake waits on: the attempt, the last error and the next attempt's time.
export function intakeLines(i: J): string[] {
  return [
    i.attempt ? t("Intake {seq} ({intent}) waits for the brief, attempt {attempt}", { seq: i.seq, intent: i.intent, attempt: i.attempt }) : t("Intake {seq} ({intent}) waits for the brief", { seq: i.seq, intent: i.intent }),
    i.last_error ? t("Last error: {error}", { error: i.last_error }) : "",
    isSet(i.next) ? t("Next attempt at {time}", { time: stamp(i.next) }) : "",
  ].filter(Boolean);
}

// detailFacts are the row's plugin's own detail fields, as its catalog words them, read from the detail or the
// row's override.
export function detailFacts(r: J, d: J, o: J): { field: string; label: string; title?: string; value: string }[] {
  const provider = unitOf(r).provider;
  return catalog.details
    .filter((x: J) => x.provider === provider)
    .map((x: J) => ({ field: x.field, label: x.label, title: x.title || undefined, value: d[x.field] ?? o[x.field] }))
    .filter((x) => x.value);
}

// unitHomeView is the view a unit opens in: its provider's "all" when it serves one, else the provider's first rows
// view; "all" when none is known.
export function unitHomeView(provider: string, views: J[]): string {
  const own = (views || []).filter((v: J) => v.provider === provider && v.rows);
  return (own.find((v: J) => v.id === "all") || own[0] || { id: "all" }).id;
}

export function unitRefHref(u: J): string {
  return "#" + routeHash(unitHomeView(u.provider, catalog.views), u.provider + ":" + u.key);
}

// replyArgs are the reply verb's args of the composer, null when it holds nothing to post.
export function replyArgs(c: { text: string; reaction: string }, send: boolean): J {
  const text = c.text.trim();
  const reaction = c.reaction.trim().replace(/^:|:$/g, "");
  if (!text && !reaction) return null;
  const args: J = text ? { text } : { reaction };
  args.send = send ? "now" : "draft";
  return args;
}

// replySendsNow says whether the reply verb's served send posts a reply at once.
export function replySendsNow(): boolean {
  const verb = handlerVerb("reply");
  const v = ((tasksState.catalog && tasksState.catalog.verbs) || []).find((x: J) => x.verb === verb) || {};
  return v.send === "now";
}

// itemStateWord is the word for an item's state, open or resolved.
export function itemStateWord(st: string): string {
  if (st === "open") return t("open");
  if (st === "resolved") return t("resolved");
  return st;
}

export { RISKS, SIZES, age, intakeWaiting, left, roundGate, stamp, waitingLabel };
