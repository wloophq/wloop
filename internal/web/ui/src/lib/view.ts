import { tick } from "svelte";
import { readHash, writeHash } from "../state/route.svelte";
import { state } from "../state/store.svelte";
import { setNotice } from "../state/actions.svelte";
import { isCoreView } from "./catalog";
import { tn } from "./i18n.svelte";
import { groupField, lensOf, pipeline, pipelineOf, type Lens, type Pipeline } from "./lens";
import { taskLensItems } from "./tasks";

// readLens is the lens of the current route.
export function readLens(): Lens {
  return lensOf(readHash());
}

// lensResult is what lens shows of its view: a task view's tasks, a row view's rows.
export function lensResult(lens: Lens): Pipeline {
  if (isCoreView(lens.view)) return pipelineOf(taskLensItems(), lens, null);
  return pipeline(state.rows, lens);
}

// moveFocus puts the focus on the control of slot once the page has drawn it, as a move that takes the focus
// along does: clearing the lens back to the search, a group's toggle.
export async function moveFocus(slot: string): Promise<void> {
  await tick();
  const el = document.querySelector<HTMLElement>(`[data-focus="${CSS.escape(slot)}"]`);
  if (el && !(el as HTMLButtonElement).disabled) el.focus({ preventScroll: true });
}

export function setGroup(g: string): void {
  const lens = { ...readLens(), group: g };
  const res = lensResult(lens);
  writeHash({ g });
  const f = res.group ? groupField(res.group, lens.fields) : null;
  if (f) setNotice(tn("Grouped by {field}: {n} groups", res.groups.filter((x) => x.rows.length).length, { field: f.label.toLowerCase() }));
}

export function clearLens(): void {
  writeHash({ q: "", g: "", s: "" });
  void moveFocus("top:search");
}
