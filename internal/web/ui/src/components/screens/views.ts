import { viewLabel, viewSpec } from "../../lib/catalog";
import { t, tn } from "../../lib/i18n.svelte";
import { labelOf, type J } from "../../lib/util";
import { postJSON } from "../../state/api";
import { setNotice } from "../../state/actions.svelte";
import { viewCounters } from "../../state/loaders.svelte";
import { askConfirm } from "../../state/overlays.svelte";
import type { Hash } from "../../state/route.svelte";
import { notice, removeState, state, tasksState } from "../../state/store.svelte";
import { scheduleLoad } from "../../state/sync.svelte";

// areaOptions are the area picker's options with their counts; none when the catalog has no area.
export function areaOptions(route: Pick<Hash, "q" | "area">): { value: string; label: string }[] {
  const areas: string[] = (tasksState.catalog && tasksState.catalog.areas) || [];
  const counts = (viewCounters(route) || {}).areas || {};
  return areas.map((a) => ({ value: a, label: counts[a] === undefined ? a : `${a} (${counts[a]})` }));
}

export interface Chip {
  ctx: string;
  on: boolean;
  count: string;
  next: string[];
}

// contextChips are the context chips of a view of tasks; a view of gates has none.
export function contextChips(route: Pick<Hash, "view" | "q" | "area" | "ctx">): Chip[] {
  if ((viewSpec(route.view) || {}).over === "gates") return [];
  const ctxs: string[] = (tasksState.catalog && tasksState.catalog.contexts) || [];
  const counts = (viewCounters(route) || {}).contexts || {};
  return ctxs.map((c) => {
    const on = route.ctx.includes(c);
    return { ctx: c, on, count: counts[c] === undefined ? "" : ` ${counts[c]}`, next: on ? route.ctx.filter((x) => x !== c) : [...route.ctx, c] };
  });
}

// viewRule is the rule a view's profile lists it by, as the profile writes it.
export function viewRule(spec: J): [string, string][] {
  if (!spec) return [];
  return ([[t("Lists"), spec.when], [t("Sorted by"), spec.sort], [t("Grouped by"), spec.group]] as [string, string][]).filter(([, v]) => v);
}

// removeAllClean removes the clean workspaces the n tasks of a view that offers it hold, and says what it removed
// and what it kept, with why.
export async function removeAllClean(n: number, view: string): Promise<void> {
  if (!(await askConfirm(tn("Remove the workspaces of the {n} tasks in {view}? Their checkouts are deleted and their terminals are closed. A workspace whose agent works or waits, or that holds uncommitted files, unpushed commits or ignored files outside build and dependency folders, is kept.", n, { view: viewLabel(view) })))) return;
  removeState.busy = true;
  state.actionError = "";
  state.actionDetail = "";
  try {
    const out = await postJSON("/api/workspaces/remove-clean", { view });
    const removed = (out.removed || []).map(labelOf);
    const kept = (out.kept || []).map((k: J) => `${labelOf(k)} (${k.reason})`);
    const details = (out.kept || []).filter((k: J) => k.detail).map((k: J) => `${labelOf(k)}: ${k.detail}`);
    const text = [
      removed.length ? tn("Removed {n}: {list}.", removed.length, { list: removed.join(", ") }) : tn("Removed {n}.", 0),
      kept.length ? tn("Kept {n}: {list}.", kept.length, { list: kept.join("; ") }) : "",
    ].filter(Boolean).join(" ");
    setNotice(text);
    notice.title = text;
    if (details.length) {
      state.actionError = tn("Kept {n} with details", details.length);
      state.actionSource = "";
      state.actionDetail = details.join("\n");
    }
  } catch (err) {
    state.actionError = t("remove all clean failed: {error}", { error: (err as Error).message });
  }
  removeState.busy = false;
  scheduleLoad({ all: true, tasks: true });
}
