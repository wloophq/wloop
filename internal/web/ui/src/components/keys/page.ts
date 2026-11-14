import { QUEUE, readRoute } from "../../lib/routes";
import { taskById } from "../../lib/tasks";
import type { J } from "../../lib/util";
import { queueShown } from "../../state/loaders.svelte";
import { nav } from "../../state/route.svelte";
import { detailState, queueState, tasksState } from "../../state/store.svelte";
import { gateAt } from "../gates/gates";

// queueDraft says whether gate g is a draft's, which the daemon serves with its delivery.
function queueDraft(g: J): boolean {
  return !!(g && g.delivery && g.subject && typeof g.subject === "object");
}

// pageGates are the gates the page draws as cards, each with its task: the detail's, the queue's item's and
// the gates list's.
export function pageGates(hash: string = nav.hash): { task: J; gates: J[] }[] {
  const out: { task: J; gates: J[] }[] = [];
  const e = detailState.task;
  if (e) out.push({ task: e, gates: e.gates || [] });
  const queue = queueShown(readRoute(hash));
  const item = queue && (queue.items || [])[queueState.at];
  if (item && item.gate) out.push({ task: item.task, gates: [item.gate] });
  for (const g of tasksState.gates || []) out.push({ task: taskById(g.task, detailState.task, detailState.unitTasks), gates: [g] });
  return out;
}

// pageQuestion is the gate a digit answers from el: the one whose card holds el, else, on a queue, its item's
// gate when that is no draft's and no detail is open over the queue.
export function pageQuestion(el: Element | null | undefined, hash: string = nav.hash): { task: J; gate: J } | null {
  const at = gateAt(el, pageGates(hash));
  if (at || (el && el.closest && el.closest(".d-gate"))) return at;
  const route = readRoute(hash);
  if (route.view !== QUEUE || route.task || route.unit || (el && el.closest && el.closest("#detail"))) return null;
  const queue = queueShown(route);
  const item = queue && (queue.items || [])[queueState.at];
  return item && item.gate && !queueDraft(item.gate) ? { task: item.task, gate: item.gate } : null;
}

// answerableQuestion is the unit detail's question whose options the keys pick: asked, its answer not queued.
export function answerableQuestion(d: J): J {
  const q = d && d.row && d.row.question;
  return q && q.state === "asked" && !q.queued ? q : null;
}

// nextTaskNode is the task row delta rows from the focused one among nodes, else from the open task's first
// copy, else the first.
export function nextTaskNode(nodes: HTMLElement[], active: HTMLElement | null, openID: string, delta: number): HTMLElement | null {
  if (!nodes.length) return null;
  let at = active ? nodes.indexOf(active) : -1;
  const slot = "task:" + openID;
  if (at < 0 && openID) at = nodes.findIndex((n) => n.dataset.focus === slot || (n.dataset.focus || "").startsWith(slot + "@"));
  return nodes[Math.max(0, Math.min(nodes.length - 1, at < 0 ? 0 : at + delta))];
}
