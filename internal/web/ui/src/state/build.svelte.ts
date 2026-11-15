import { pending } from "./actions.svelte";
import { omni, overlays } from "./overlays.svelte";
import type { J } from "../lib/util";
import { busy, detailState, drafts, editPayloads, editing, gateState, outboxState, queueState, taskState, tasksState } from "./store.svelte";

// update says whether the banner that offers a reload into the daemon's new build shows.
export const update = $state({ shown: false });

// buildAction is what a page that loaded build loaded does once the daemon serves build served: nothing while
// they agree or either is unknown, a reload when the page holds nothing a reload would lose and has not reloaded
// for served already, else the banner.
export function buildAction(loaded: string, served: string, blocked: boolean, reloadedFor: string): "" | "banner" | "reload" {
  if (!loaded || !served || loaded === served) return "";
  return blocked || reloadedFor === served ? "banner" : "reload";
}

const held = (text: unknown) => String(text || "").trim() !== "";

// draftsShown are the drafts the page holds, as "draft:<id>", the key a draft's edits live under: those of the
// open task, its drafts, the open unit's, the listed tasks and gates, the Outbox and the queue.
function draftsShown(): Set<string> {
  const out = new Set<string>();
  const subject = (g: J) => g && g.subject && typeof g.subject === "object" && g.subject.id !== undefined && out.add("draft:" + g.subject.id);
  const task = (e: J) => e && (e.gates || []).forEach(subject);
  task(detailState.task);
  for (const d of taskState.drafts) out.add("draft:" + d.id);
  for (const d of detailState.unitDrafts) out.add("draft:" + d.id);
  tasksState.tasks.forEach(task);
  tasksState.gates.forEach(subject);
  for (const g of (outboxState.page && outboxState.page.groups) || []) for (const d of g.drafts || []) out.add("draft:" + d.id);
  for (const it of (queueState.queue && queueState.queue.items) || []) subject(it.gate);
  return out;
}

// editsShown says whether a draft the page still shows is being edited: an edit of a draft that left the page,
// sent or withdrawn elsewhere, holds nothing a reload would lose.
function editsShown(): boolean {
  if (!editing.size && !editPayloads.size) return false;
  const shown = draftsShown();
  return [...editing.keys(), ...editPayloads.keys()].some((k) => shown.has(k));
}

// reloadBlocked says whether a reload would lose something: an open overlay or omnibox, an action whose answer is
// on its way, a draft being edited (its text or its fields), or text the page holds for a composer, a guidance, an answer or the capture field.
// The filter and the Explore search are left out, since the hash keeps them.
export function reloadBlocked(): boolean {
  if (overlays.stack.length || omni.open || busy.size || pending.size || editsShown()) return true;
  const texts = [
    ...Object.values(drafts.guidance), drafts.capture, detailState.answer, detailState.guidance, detailState.compose.text,
    detailState.compose.reaction, taskState.text, ...gateState.answers.values(),
  ];
  return texts.some(held);
}

const RELOADED_KEY = "reloaded-for";

// page is how the build check reaches the page; tests replace it.
export const page = {
  build: (): string => (typeof document === "undefined" ? "" : document.querySelector('meta[name="build"]')?.getAttribute("content") || ""),
  reloadedFor: (): string => {
    try {
      return sessionStorage.getItem(RELOADED_KEY) || "";
    } catch {
      return "";
    }
  },
  remember: (served: string): void => {
    try {
      sessionStorage.setItem(RELOADED_KEY, served);
    } catch {
      // Without the session storage a second mismatch reloads again.
    }
  },
  reload: (): void => location.reload(),
};

function reloadForBuild(served: string): void {
  page.remember(served);
  page.reload();
}

// checkPanelBuild reloads the floating panel once health names a build other than the page's; the panel holds
// nothing a reload would lose once it shows again.
export function checkPanelBuild(health: { build?: string } | null): void {
  const served = (health && health.build) || "";
  if (buildAction(page.build(), served, false, page.reloadedFor()) === "reload") reloadForBuild(served);
}

// checkBuild reloads the page, or offers to, once health names a build other than the page's.
export function checkBuild(health: { build?: string } | null): void {
  const served = (health && health.build) || "";
  const action = buildAction(page.build(), served, reloadBlocked(), page.reloadedFor());
  if (action === "reload") {
    reloadForBuild(served);
    return;
  }
  if (action === "banner") update.shown = true;
  else if (!served || served === page.build()) update.shown = false;
}
