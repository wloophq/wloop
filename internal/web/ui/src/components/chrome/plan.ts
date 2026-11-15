import { t } from "../../lib/i18n.svelte";
import { unitKey, type J } from "../../lib/util";
import { act, setNotice } from "../../state/actions.svelte";
import { postJSON } from "../../state/api";
import { loadPlan } from "../../state/loaders.svelte";
import { askInput } from "../../state/overlays.svelte";
import { busy, state } from "../../state/store.svelte";
import { clock } from "../../state/clock.svelte";

const PLAN_ORDER = ["pending", "blocked"];

export function planHead(decision: string): string {
  return decision === "blocked" ? t("Blocked") : t("Pending");
}

// PLAN_ACTS are the plan actions POST /api/plan carries, by spec id; an item's other actions are its unit's.
const PLAN_ACTS: Record<string, string> = { plan_apply: "apply", plan_dismiss: "dismiss", guide: "guide" };

export interface PlanGroup {
  decision: string;
  items: J[];
}

// planGroups are the items waiting on the user by decision, pending first, and within pending those that run by
// themselves soonest first.
export function planGroups(items: J[]): PlanGroup[] {
  const waits = (it: J) => (it.default === "run" ? 0 : 1);
  const at = (it: J) => (it.deadline ? Date.parse(it.deadline) : Infinity);
  return PLAN_ORDER.map((decision) => ({
    decision,
    items: items.filter((it) => it.decision === decision).sort((a, b) => waits(a) - waits(b) || at(a) - at(b)),
  })).filter((g) => g.items.length);
}

// planWhen says when an item runs: at its window's end, when the user applies it, or not while it is blocked.
export function planWhen(it: J, now = clock.now): string {
  if (it.decision === "blocked") return t("blocked");
  if (it.default !== "run") return t("waits for you");
  if (!it.deadline) return t("runs now");
  const s = Math.ceil((Date.parse(it.deadline) - now) / 1000);
  if (s <= 0) return t("running");
  if (s <= 99) return t("runs in {seconds} s", { seconds: s });
  return t("runs in {minutes}m {seconds}s", { minutes: Math.floor(s / 60), seconds: String(s % 60).padStart(2, "0") });
}

export function planCount(): number | "" {
  return state.plan.loaded ? planGroups(state.plan.items).reduce((n, g) => n + g.items.length, 0) : "";
}

export const itemLabel = (it: J): string => it.label || unitKey(it.unit);
export const planName = (it: J): string => (it.title ? `${itemLabel(it)} · ${it.title}` : itemLabel(it));

// actOnPlan runs action id on plan item it: a plan action through POST /api/plan, any other on its unit.
export async function actOnPlan(it: J, id: string): Promise<void> {
  if (!PLAN_ACTS[id]) {
    await act({ unit: it.unit, actions: it.actions, label: planName(it) }, id);
    return;
  }
  const body: J = { ids: [it.id], action: PLAN_ACTS[id] };
  if (id === "guide") {
    const guidance = await askInput({ label: t("Guidance for {workflow} on {item}", { workflow: it.workflow, item: planName(it) }) });
    if (guidance === null || !guidance.trim()) return;
    body.guidance = guidance.trim();
  }
  busy.add(it.id);
  try {
    const out = await postJSON("/api/plan", body);
    if ((out.errors || []).length) throw new Error(out.errors.join("; "));
    setNotice(`${body.action}: ${it.workflow} ${planName(it)}`);
  } catch (err) {
    setNotice(t("{action} failed: {error}", { action: `${body.action} ${it.workflow} ${planName(it)}`, error: (err as Error).message }), true);
  } finally {
    busy.delete(it.id);
  }
  await loadPlan();
}
