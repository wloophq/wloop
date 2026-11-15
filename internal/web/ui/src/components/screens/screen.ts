import { coreViews } from "../../lib/catalog";
import { t } from "../../lib/i18n.svelte";
import { lensOf, type Lens } from "../../lib/lens";
import type { J } from "../../lib/util";
import { postJSON } from "../../state/api";
import { setNotice } from "../../state/actions.svelte";
import { scheduleLoad } from "../../state/sync.svelte";
import { taskModel, type RowModel } from "../rows/model";

// screenLens is the lens a task's row reads in view, the profile's first view when none is named.
export function screenLens(view: string): Lens {
  return lensOf({ view: view || (coreViews()[0] || { id: "" }).id, q: "", g: "", s: "", layout: "", area: "", ctx: [], tk: "", done: false });
}

// screenRow is task e's row as the list of view draws it.
export function screenRow(e: J, view: string, now?: number): { model: RowModel; lens: Lens } {
  const lens = screenLens(view);
  return { model: taskModel(e, "", "", lens, now), lens };
}

// capture creates a task titled title; false when the daemon refused it.
export async function capture(title: string): Promise<boolean> {
  try {
    await postJSON("/api/tasks", { title });
    setNotice(t("captured: {title}", { title }));
    scheduleLoad({ tasks: true });
    return true;
  } catch (err) {
    setNotice(t("capture failed: {error}", { error: (err as Error).message }), true);
    return false;
  }
}
