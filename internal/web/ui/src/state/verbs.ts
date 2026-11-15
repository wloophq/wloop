import { t } from "../lib/i18n.svelte";
import type { J } from "../lib/util";
import { scheduler, setNotice } from "./actions.svelte";
import { postJSON } from "./api";
import { handlerVerb, taskPath, verbFor } from "./loaders.svelte";
import { editing, editPayloads, editRevs, tasksState } from "./store.svelte";

// A task's verbs as the profile's catalog words them, and running one.

export { handlerVerb, taskPath, verbFor };

export function taskCatalog(): J {
  return tasksState.catalog || {};
}

export function stateLabel(name: string): string {
  return ((taskCatalog().states || []).find((s: J) => s.name === name) || {}).label || name;
}

// verbTask is the catalog's word for verb: its label, summary, key, input and pick.
export function verbTask(verb: string): J {
  return (taskCatalog().verbs || []).find((x: J) => x.verb === verb) || {};
}

export function verbLabel(verb: string): string {
  return verbTask(verb).label || verb;
}

// verbHandler is the handler verb runs: its profile handler, else its own name, as the tasks service binds it.
export function verbHandler(verb: string): string {
  return verbTask(verb).handler || verb;
}

// verbState is how verb stands on e; null when e does not offer it.
export function verbState(e: J, verb: string): J {
  return (e.verbs || []).find((v: J) => v.verb === verb) || null;
}

// offeredVerb is the verb e offers that runs handler, "" when it offers none.
export function offeredVerb(e: J, handler: string): string {
  const v = (e.verbs || []).find((x: J) => verbHandler(x.verb) === handler);
  return v ? v.verb : "";
}

// verbBody is a verb's request: its args as strings.
export function verbBody(args?: Record<string, unknown> | null): J {
  const body: J = { source: "cockpit" };
  const kept = Object.entries(args || {}).filter(([, v]) => v !== undefined && v !== null).map(([k, v]) => [k, String(v)]);
  if (kept.length) body.args = Object.fromEntries(kept);
  return body;
}

export interface Ask {
  label: string;
  pick?: string;
  args?: (v: string) => Record<string, string> | null;
}

type Args = Record<string, string>;

// VERB_ASKS are, by handler, how a verb that asks for a value first turns the answer into its arguments, null when
// the answer is not usable; the question is the served verb's input.
const VERB_ASKS: Record<string, { args: (v: string) => Args | null }> = {
  waiting: { args: (v): Args => (v ? { on: v } : {}) },
  schedule: { args: (v): Args | null => (!v ? null : v.toLowerCase() === "activity" ? { on_activity: "true" } : { until: v }) },
  project: { args: (v): Args | null => (v ? { title: v } : null) },
  context: { args: (v): Args => ({ set: v }) },
  area: { args: (v): Args => ({ set: v }) },
  delegate: { args: (v): Args => (v ? { guidance: v } : {}) },
  reply: { args: (v): Args | null => (v ? { text: v } : null) },
  promise: { args: (v): Args => (v ? { by: v } : {}) },
};

// servedAsk is how the served verb named verb, among specs, asks for its value: its input and its handler's args,
// or, for a verb that takes another task, its input and the tasks it picks from; null for a verb that asks none.
export function servedAsk(verb: string, specs: J[]): Ask | null {
  const spec = (specs || []).find((v) => v.verb === verb);
  if (spec && spec.pick) return { label: spec.input || spec.label || verb, pick: spec.pick };
  const ask = spec && spec.input ? VERB_ASKS[spec.handler] : null;
  return ask ? { label: spec.input, args: ask.args } : null;
}

// stopEditing forgets the edits of the draft edited under key.
export function stopEditing(key: string): void {
  editing.delete(key);
  editPayloads.delete(key);
  editRevs.delete(key);
}

export function startEditing(key: string, d: J): void {
  editing.set(key, d.text || "");
  editPayloads.delete(key);
  editRevs.set(key, d.rev);
}

function changed(): void {
  scheduler.load({ tasks: true });
}

// taskAct runs verb on task and asks for a reload; the result, or null when it failed.
export async function taskAct(task: J, verb: string, args?: Record<string, unknown> | null): Promise<J> {
  if (!task) return null;
  try {
    const out = await postJSON(taskPath(task.id, verb), verbBody(args));
    setNotice(`${verb}: ${task.title || task.id}${out && out.lines && out.lines.length ? ". " + out.lines.join(" ") : ""}`);
    if (verbHandler(verb) === "approve") stopEditing(task.id);
    changed();
    return out || {};
  } catch (err) {
    setNotice(t("{verb} {task} failed: {error}", { verb, task: task.title || task.id, error: (err as Error).message }), true);
    changed();
    return null;
  }
}

// propText is a property's value as a person reads it: a name, or the word facet f gives the value.
export function propText(v: unknown, f: J): string {
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
