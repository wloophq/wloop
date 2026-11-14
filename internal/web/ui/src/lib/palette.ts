import { t } from "./i18n.svelte";
import type { J } from "./util";
import type { PaletteItem, PaletteRequest } from "../state/overlays.svelte";

// paletteMatch keeps the items whose label or key holds every word of q.
export function paletteMatch(items: PaletteItem[], q: string): PaletteItem[] {
  const words = String(q || "").toLowerCase().split(/\s+/).filter(Boolean);
  return items.filter((it) => {
    const text = `${it.label} ${it.key || ""}`.toLowerCase();
    return words.every((w) => text.includes(w));
  });
}

// matchPath asks the daemon for the open tasks but except whose title holds q.
export function matchPath(q: string, except: string): string {
  return "/api/tasks?match=" + encodeURIComponent(q) + "&except=" + encodeURIComponent(except || "");
}

// PICK_ARG is the argument a verb that takes another task sends the pick in, task.ArgTask.
export const PICK_ARG = "task";

// pickRefs are the tasks a verb's pick offers on task for the text q: the open tasks but task whose title holds
// q, which match reads from the daemon, else the tasks merged into task or related to it whose title holds q.
export async function pickRefs(pick: string, task: J, q: string, match: (q: string, except: string) => Promise<J[]>): Promise<J[]> {
  if (pick === "open") return (await match(q, task.id)) || [];
  const refs = pick === "merged" ? task.merged_from : pick === "related" ? task.related : [];
  const want = String(q || "").trim().toLowerCase();
  return ((refs || []) as J[]).filter((r) => String(r.title || r.id).toLowerCase().includes(want));
}

export interface TaskPick {
  task: J;
  ask: { label: string; pick: string };
  match: (q: string, except: string) => Promise<J[]>;
  palette: (p: PaletteRequest) => unknown;
  stateLabel?: (name: string) => string;
  run: (id: string) => unknown;
}

// taskPicker opens the palette, titled with the verb's question, over the tasks ask.pick offers on task; run
// takes the id picked.
export async function taskPicker({ task, ask, match, palette, stateLabel, run }: TaskPick): Promise<void> {
  const items = (refs: J[]): PaletteItem[] =>
    refs.map((r) => ({ label: r.title || r.id, meta: stateLabel ? stateLabel(r.state) : r.state || "", run: () => run(r.id) }));
  const source = async (q: string) => items(await pickRefs(ask.pick, task, q, match));
  palette({ title: ask.label, items: await source(""), source, empty: t("No task holds the text") });
}
