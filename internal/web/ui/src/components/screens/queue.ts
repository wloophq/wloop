import { t } from "../../lib/i18n.svelte";
import { readRoute, unitPath, HOME, type Unit } from "../../lib/routes";
import { safeURL, type J } from "../../lib/util";
import { act } from "../../state/actions.svelte";
import { setNotice } from "../../state/actions.svelte";
import { getJSON } from "../../state/api";
import { queueLoad, queueShown } from "../../state/loaders.svelte";
import { currentHash, hashString, writeHash } from "../../state/route.svelte";
import { queueState } from "../../state/store.svelte";
import { draftActions, resolveDraft, resolveGate, type DraftAction } from "../gates/gates";

// A decision queue walked one decision at a time, the oldest first. The daemon orders the items and names each
// primary; this runs them and moves through them.

// queueDeps are what the queue reaches beyond the store: running a unit's action as the omnibox does, opening a
// link, the key bound to an action and moving the page; the integrator wires the omnibox's and the keys'.
export const queueDeps = {
  act: async (u: Unit, id: string): Promise<boolean | undefined> => {
    const d = await getJSON(unitPath(u));
    return act(d.row, id);
  },
  open: (url: string): unknown => window.open(url, "_blank", "noopener"),
  keyOf: (_action: string): string => "",
  navigate: (view: string): void => {
    location.hash = hashString({ view });
  },
};

export const queueRoute = () => readRoute(currentHash());

// queueDraft reports whether gate g is a draft's, which the daemon serves with its delivery and the draft.
export function queueDraft(g: J): boolean {
  return !!(g && g.delivery && g.subject && typeof g.subject === "object");
}

// queueDraftOf is the draft of gate g, which names its gate.
export function queueDraftOf(g: J): J {
  return { ...g.subject, gate: g.subject.gate || g.id };
}

// queueUnit is the unit of item's task its situation is on: the first of its units of that provider.
export function queueUnit(item: J): Unit | null {
  const p = item.situation ? item.situation.provider : "";
  return ((item.task && item.task.units) || []).find((u: Unit) => !p || u.provider === p) || null;
}

const sameUnit = (a: J, b: J) => !!a && !!b && a.provider === b.provider && a.key === b.key;

// sourceLink is the link of task e's first unit, from its card.
function sourceLink(e: J): string {
  const u = ((e && e.units) || [])[0];
  const c = ((e && e.cards) || []).find((x: J) => x.card && sameUnit(x.card.unit, u));
  return (c && safeURL(c.card.link)) || "";
}

// openTaskSource opens unit u of task e at its source, else the task's first unit's; the unit's detail when no
// card links it.
export function openTaskSource(e: J, u: Unit | null): void {
  const card = ((e && e.cards) || []).find((x: J) => x.card && u && sameUnit(x.card.unit, u));
  const link = (card && safeURL(card.card.link)) || sourceLink(e);
  if (link) queueDeps.open(link);
  else if (u) writeHash({ pr: u.provider + ":" + u.key, task: "" });
}

// queueDraftAction is the action of item's draft card that key runs, null when the card offers none.
export function queueDraftAction(item: J, key: string): DraftAction | null {
  if (!item || !queueDraft(item.gate)) return null;
  return draftActions(item.task, queueDraftOf(item.gate)).find((a) => a.key === key) || null;
}

// queueRefused says why the card refuses action a.
function queueRefused(a: DraftAction): void {
  setNotice(t("{action}: {reason}", { action: a.label, reason: a.reason || a.title || "" }), true);
}

// queueAct runs item's primary: its gate's intent as the card does, its unit's catalog action, its unit's source,
// or the task's detail; then the queue reloads, so a decision settled elsewhere leaves it and a refused one shows
// why. Nothing runs twice while a run is in flight.
export async function queueAct(item: J): Promise<void> {
  if (queueState.busy) return;
  const p = item.primary || {};
  queueState.busy = true;
  queueState.error = "";
  try {
    const g = item.gate;
    const card = p.kind === "gate" && queueDraft(g) ? queueDraftAction(item, "queue:primary") : null;
    if (card && !card.enabled) return queueRefused(card);
    if (card) await card.run();
    else if (p.kind === "gate" && queueDraft(g)) await resolveDraft(queueDraftOf(g), p.id, p.label || p.id, {});
    else if (p.kind === "gate") await resolveGate(item.task, g, p.id);
    else if (p.kind === "action") {
      const u = queueUnit(item);
      if (u && (await queueDeps.act(u, p.id)) === false) throw new Error(t("{action} failed", { action: p.id }));
    } else if (p.kind === "open") openTaskSource(item.task, queueUnit(item));
    else if (p.kind === "task") writeHash({ task: item.task.id, pr: "" });
  } catch (err) {
    queueState.error = t("{action} failed: {error}", { action: p.label || p.id, error: (err as Error).message });
  } finally {
    queueState.busy = false;
  }
  if (p.kind === "gate" || p.kind === "action") await queueLoad(queueRoute());
}

// queueDraftKey runs the action of item's draft card that key names, then reloads the queue; false when the card
// offers none, so the key goes on to its other bindings. A refused action says why.
export function queueDraftKey(item: J, key: string): boolean {
  const a = queueDraftAction(item, key);
  if (!a) return false;
  if (!a.enabled) {
    queueRefused(a);
    return true;
  }
  (async () => {
    await a.run();
    if (key !== "queue:edit") await queueLoad(queueRoute());
  })();
  return true;
}

// queueStep moves the queue to the next or the previous decision, staying inside it.
export function queueStep(delta: number): void {
  const queue = queueShown(queueRoute());
  const items = (queue && queue.items) || [];
  queueState.at = Math.min(Math.max(queueState.at + delta, 0), Math.max(items.length - 1, 0));
}

// queueKeyAction runs a queue key's action: the primary, the next or the previous decision, a draft card's action,
// its source, the home; false when the key has nothing to run here.
export function queueKeyAction(action: string): boolean {
  const queue = queueShown(queueRoute());
  const items = (queue && queue.items) || [];
  const item = items[queueState.at];
  switch (action) {
    case "queue:primary":
      if (item) void queueAct(item);
      return !!item;
    case "queue:skip":
    case "queue:back":
      queueStep(action === "queue:skip" ? 1 : -1);
      return true;
    case "queue:edit":
    case "queue:discard":
    case "queue:close-silently":
    case "queue:skip-workflow":
      return queueDraftKey(item, action);
    case "open-source":
      if (item) openTaskSource(item.task, queueUnit(item));
      return !!item;
    case "queue:home":
      queueDeps.navigate(HOME);
      return true;
  }
  return false;
}
