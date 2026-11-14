import { tasksState } from "../state/store.svelte";
import { fold, type J } from "./util";

export function unitLabel(u: J): string {
  return !u ? "" : `${u.provider}:${u.key}`;
}

export function personName(p: J): string {
  return (p && (p.name || p.key)) || "";
}

// shortDate is an instant's UTC day, as a deadline reads.
export function shortDate(at: string): string {
  const d = new Date(at);
  return isNaN(d.getTime()) ? "" : d.toISOString().slice(0, 10);
}

// localDate is an instant's day where the browser is, unlike shortDate's UTC day for deadlines.
export function localDate(at: string): string {
  const d = new Date(at);
  return isNaN(d.getTime()) ? "" : d.toLocaleDateString("en-CA");
}

// shortLag reads a Go duration as its largest unit: 2h, 12m, 30s.
export function shortLag(d: string): string {
  const m = /^(?:(\d+)h)?(?:(\d+)m)?(?:([\d.]+)s)?$/.exec(d || "");
  if (!m) return d || "";
  if (m[1]) return m[1] + "h";
  if (m[2]) return m[2] + "m";
  return Math.round(Number(m[3] || 0)) + "s";
}

// everyLabel reads a Go duration of whole hours as days or hours.
export function everyLabel(d: string): string {
  const m = /^(\d+)h0m0s$/.exec(d || "");
  if (!m) return d || "";
  const hours = Number(m[1]);
  return hours % 24 === 0 ? `${hours / 24}d` : `${hours}h`;
}

// draftOf is the draft of e's draft gate, its subject; null when e carries none.
export function draftOf(e: J): J {
  const g = ((e && e.gates) || []).find((x: J) => x.kind === "draft" && x.subject && typeof x.subject === "object");
  return g ? g.subject : null;
}

// messageOf is the message of e's first open question, post or proposal gate, its subject; null when e carries
// none.
export function messageOf(e: J): J {
  const g = ((e && e.gates) || []).find((x: J) => x.status === "open" && ["question", "post", "proposal"].includes(x.kind) && x.subject && typeof x.subject === "object");
  return g ? g.subject : null;
}

// isDraftTask says whether e is a draft's own task, which shows its draft.
export function isDraftTask(e: J): boolean {
  const d = draftOf(e);
  return !!d && e.id === "draft:" + d.id;
}

// taskSlot is the focus slot of a task's row, or of one of its copies.
export function taskSlot(id: string, copy?: string): string {
  return copy ? `task:${id}@${copy}` : `task:${id}`;
}

// taskHaystack is what the filter's free words search in a task, the texts the daemon's counters search
// (api.TaskView.Texts): its title, notes, state, the text and the lists of text of its properties, its units, who
// owes it, its asks, its cards' people, its draft, its message and its facets' values, by facet id.
export function taskHaystack(e: J): string[] {
  const d = draftOf(e) || {};
  const m = messageOf(e) || {};
  const props = Object.values(e.props || {}).flatMap((v) => (typeof v === "string" ? [v] : Array.isArray(v) && v.every((x) => typeof x === "string") ? v : []));
  return [e.title, e.notes, e.state, ...props,
    ...(e.units || []).flatMap((u: J, i: number) => [(e.unit_labels || [])[i] || unitLabel(u), `${u.provider}:${u.key}`]),
    e.owed_by || "",
    ...(e.asks || []).flatMap((a: J) => [a.summary, a.asker]),
    ...(e.cards || []).flatMap((c: J) => [((c.card || {}).last_human || {}).who ? personName(c.card.last_human.who) : "", ...((c.card || {}).waits_on || []).map(personName)]),
    d.target, d.where, d.text, d.reaction, d.unit, m.text,
    ...Object.keys(e.facets || {}).sort().flatMap((id) => e.facets[id])].map((x) => fold(x));
}

const taskItems = { rev: -1, items: new WeakMap<object, J>() };

// taskItemOf is e as the lens reads an item: its facets and the texts the daemon's counters search.
export function taskItemOf(e: J): J {
  if (taskItems.rev !== tasksState.rev) Object.assign(taskItems, { rev: tasksState.rev, items: new WeakMap() });
  let it = taskItems.items.get(e);
  if (!it) taskItems.items.set(e, (it = { task: e, facets: e.facets || {}, texts: taskHaystack(e) }));
  return it;
}

// taskLensItems are the loaded tasks as the lens's items, in the daemon's order.
export function taskLensItems(): J[] {
  return tasksState.tasks.map(taskItemOf);
}

// taskById is the task of id the page holds: the detail's, one of the list's or of the unit's tasks.
export function taskById(id: string, detail: J, unitTasks: J[]): J {
  if (!id) return null;
  if (detail && detail.id === id) return detail;
  for (const list of [tasksState.tasks, unitTasks]) {
    const e = (list || []).find((x: J) => x.id === id);
    if (e) return e;
  }
  return null;
}
