import { SvelteMap, SvelteSet } from "svelte/reactivity";
import { t } from "../lib/i18n.svelte";
import { unitPath } from "../lib/routes";
import { deferred, escapeHTML, keyOf, labelOf, unitOf, type Deferred, type J } from "../lib/util";
import { APIError, apiError, postJSON } from "./api";
import { askConfirm, askInput } from "./overlays.svelte";
import { answered, busy, drafts, notice, state } from "./store.svelte";

// The page's action buttons, by their data-focus key: those whose request is on its way, which take no click
// until it answers; what each last answered, shown beside it; and the button the notices answer for while its
// request is on its way.
export const pending = new SvelteSet<string>();
export const results = new SvelteMap<string, { text: string; err: boolean }>();
const run = { owner: "" };

// scheduler is how an action asks for the sync that follows it; the sync module installs itself.
export const scheduler = { load: (_c: Partial<Change>) => {} };

export interface Change {
  units: string[];
  all: boolean;
  rows: boolean;
  health: boolean;
  tasks: boolean;
  plan: boolean;
  gates: boolean;
}

// runAction runs click, the action of the button under key: the button is off until it answers, and the notices
// it raises meanwhile are its result, shown beside it.
export async function runAction<T>(key: string, click: () => T | Promise<T>): Promise<T | undefined> {
  if (pending.has(key)) return undefined;
  pending.add(key);
  results.delete(key);
  const owner = run.owner;
  run.owner = key;
  try {
    return await click();
  } finally {
    if (run.owner === key) run.owner = owner;
    pending.delete(key);
  }
}

// actionNotice keeps text as the result of the button whose action raised it; false when no action is on its way.
function actionNotice(text: string, isError: boolean): boolean {
  if (!run.owner || !text) return false;
  results.set(run.owner, { text, err: !!isError });
  return true;
}

// actionResult is the last answer of the first of keys that has one; null for none.
export function actionResult(keys: string[]): { text: string; err: boolean } | null {
  for (const k of keys) {
    const r = results.get(k);
    if (r) return r;
  }
  return null;
}

export function setNotice(text: string, isError = false): void {
  if (actionNotice(text, isError)) return;
  notice.text = text || "";
  notice.err = !!isError;
  notice.title = "";
}

export interface Copy {
  text: string;
  html?: string;
}

export interface Claim {
  pending: Deferred<Copy>;
  claimed: Promise<void> | null;
}

// clipboard is how the page writes the clipboard; tests replace it.
export const clipboard = {
  write: (items: ClipboardItem[]) => navigator.clipboard.write(items),
  writeText: (text: string) => navigator.clipboard.writeText(text),
  canWriteItems: () => typeof window !== "undefined" && !!window.ClipboardItem && !!navigator.clipboard && !!navigator.clipboard.write,
};

// clipboardItem is the clipboard entry of a copy an action answers, or of a promise of one: plain text and HTML,
// the plain text escaped when the copy has no HTML.
function clipboardItem(copy: Copy | Promise<Copy>): ClipboardItem {
  const blob = (type: string, pick: (c: Copy) => string) => {
    const p = Promise.resolve(copy).then((c) => new Blob([pick(c)], { type }));
    p.catch(() => {});
    return p;
  };
  return new ClipboardItem({
    "text/plain": blob("text/plain", (c) => c.text),
    "text/html": blob("text/html", (c) => c.html || escapeHTML(c.text)),
  });
}

// claimClipboard starts writing the copy a pending action will answer while the click that ran it lasts, as WebKit
// allows a write only then; null where the clipboard takes no promised entry.
function claimClipboard(copy: Promise<Copy>): Promise<void> | null {
  if (!clipboard.canWriteItems()) return null;
  const claimed = clipboard.write([clipboardItem(copy)]);
  claimed.catch(() => {});
  return claimed;
}

export function writeClipboard(copy: Copy): Promise<void> {
  if (clipboard.canWriteItems()) return clipboard.write([clipboardItem(copy)]);
  return clipboard.writeText(copy.text);
}

// claimCopy claims the clipboard for the copy an action about to run will answer, while the click lasts: a caller
// that awaits anything before act claims first and hands the claim to act.
export function claimCopy(): Claim {
  const p = deferred<Copy>();
  return { pending: p, claimed: claimClipboard(p.promise) };
}

// settleCopy puts the copy action name answered on the clipboard, through the write claim took in the click when
// there is one; an answer with nothing to copy releases the claim and resolves to false. A refused write throws.
export async function settleCopy(name: string, out: J, claim: Claim | null): Promise<boolean> {
  if (!out.copy) {
    if (claim) claim.pending.reject(new Error(t("{action} answered nothing to copy", { action: name })));
    return false;
  }
  if (claim) claim.pending.resolve(out.copy);
  await ((claim && claim.claimed) || writeClipboard(out.copy));
  return true;
}

// copied settles the copy action name answered and says what it copied; a refused write shows the text to copy by
// hand. False when the answer carries nothing to copy.
export async function copied(name: string, out: J, claim: Claim | null): Promise<boolean> {
  try {
    if (!(await settleCopy(name, out, claim))) return false;
    setNotice((out.output || []).join(" "));
  } catch (err) {
    state.actionError = t("Could not copy: {error}", { error: (err as Error).message });
    state.actionDetail = out.copy.text;
    state.actionSource = "";
    setNotice("");
  }
  return true;
}

// acceptedNote is what the notice says once the daemon took a review, an address or a run as an intake.
export function acceptedNote(name: string, ref: string, i: J): string {
  if (!i) return "";
  if (!i.joined) return t("{action} {unit}: accepted as intake {seq}", { action: name, unit: ref, seq: i.seq });
  if (i.state === "starting") return t("{action} {unit}: intake {seq} is already starting the turn; this request changed nothing", { action: name, unit: ref, seq: i.seq });
  return t("{action} {unit}: joined intake {seq}", { action: name, unit: ref, seq: i.seq });
}

export function questionShown(r: J): string {
  const q = r.question;
  return q ? `${q.record_id || ""}:${q.state}:${!!q.queued}` : "";
}

// answerSent says that an answer to the question r shows was accepted, so its controls stay off until the row
// shows the question it left behind.
export function answerSent(r: J): boolean {
  const key = keyOf(r);
  if (!answered.has(key)) return false;
  return answered.get(key) === questionShown(r);
}

// forgetAnswers drops the accepted answers whose row now shows another question.
export function forgetAnswers(rows: J[]): void {
  for (const r of rows) {
    const key = keyOf(r);
    if (answered.has(key) && answered.get(key) !== questionShown(r)) answered.delete(key);
  }
}

export interface ActExtra {
  guidance?: string;
  claim?: Claim;
  option_id?: string;
  yes?: boolean;
  no?: boolean;
}

// act runs the intent action name on row's unit; it resolves to undefined when the user backs out or the input is
// invalid, else to whether the daemon took it.
export async function act(row: J, name: string, extra: ActExtra = {}): Promise<boolean | undefined> {
  const key = keyOf(row);
  const st = (row.actions || []).find((a: J) => a.id === name);
  const spec = state.specs.byId[name] || {};
  const input = spec.input;
  const guidance = extra.guidance || "";
  const refuse = (why: string): undefined => {
    state.actionError = `${name} ${labelOf(row)}: ${why}`;
    state.actionDetail = "";
    state.actionSource = "";
    return undefined;
  };
  if (input && input.kind === "text" && input.required && !guidance) return refuse(t("{field} is required", { field: input.label || t("Guidance") }));
  if (input && input.max && [...guidance].length > input.max) return refuse(t("guidance is {length} characters, the limit is {max}", { length: [...guidance].length, max: input.max }));
  const shown = questionShown(row);
  const copy = extra.claim || (spec.copies ? claimCopy() : null);
  const backOut = (): undefined => {
    if (copy) copy.pending.reject(new Error(t("{action} answered nothing to copy", { action: name })));
    return undefined;
  };
  if (st && st.confirm && !(await askConfirm(st.confirm))) return backOut();
  const body: J = { source: "cockpit" };
  if (st && st.basis) body.basis = st.basis;
  if (input && input.kind === "duration") {
    const hint = [input.placeholder && t("a duration such as {example}", { example: input.placeholder }), input.until_state && t("\"state\" to wait until the situation changes")].filter(Boolean).join(", " + t("or") + " ");
    const answer = await askInput({ label: `${input.label || name}? ${hint}`, value: input.default });
    if (answer === null) return backOut();
    if (input.until_state && answer.trim() === "state") body.until_state = true;
    else body.until = answer.trim();
  }
  if (guidance && (spec.intake || (input && (input.kind === "text" || input.kind === "answer")))) body.guidance = guidance;
  if (name === "answer") {
    if (extra.option_id) body.option_id = extra.option_id;
    if (extra.yes) body.yes = true;
    if (extra.no) body.no = true;
  }
  busy.add(key);
  state.actionError = "";
  state.actionDetail = "";
  setNotice(`${name}: ${labelOf(row)}`);
  let ok = false;
  try {
    const out = await postJSON(unitPath(unitOf(row), name), body);
    if (!(await copied(name, out, copy))) {
      setNotice(acceptedNote(name, labelOf(row), out.intake) || t("{action} done: {unit}", { action: name, unit: labelOf(row) }) + (out.output && out.output.length ? ". " + out.output.join(" ") : ""));
    }
    ok = true;
    if (name === "answer" && shown) answered.set(key, shown);
    if (body.guidance) delete drafts.guidance[key];
  } catch (err) {
    if (copy) copy.pending.reject(err);
    const failed = t("{action} failed: {error}", { action: `${name} ${labelOf(row)}`, error: (err as Error).message });
    if (!actionNotice(failed, true)) state.actionError = failed;
    state.actionDetail = err instanceof APIError ? err.detail : "";
    state.actionSource = err instanceof APIError ? err.source : "";
    setNotice("");
  } finally {
    busy.delete(key);
  }
  scheduler.load({ units: [key] });
  return ok;
}

export async function setPaused(paused: boolean): Promise<void> {
  state.pausing = true;
  state.actionError = "";
  state.actionDetail = "";
  try {
    await postJSON("/api/pause", { paused });
    setNotice(paused ? t("Automatic rounds paused") : t("Automatic rounds resumed"));
  } catch (err) {
    const error = (err as Error).message;
    state.actionError = paused ? t("pause failed: {error}", { error }) : t("resume failed: {error}", { error });
  }
  state.pausing = false;
  scheduler.load({ health: true });
}

export { apiError };
