import { pending } from "./actions.svelte";
import { omni, overlays } from "./overlays.svelte";
import { busy, detailState, drafts, editPayloads, editing, gateState, taskState } from "./store.svelte";

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

// reloadBlocked says whether a reload would lose something: an open overlay or omnibox, an action whose answer is
// on its way, a draft being edited (its text or its fields), or text the page holds for a composer, a guidance, an answer or the capture field.
// The filter and the Explore search are left out, since the hash keeps them.
export function reloadBlocked(): boolean {
  if (overlays.stack.length || omni.open || busy.size || pending.size || editing.size || editPayloads.size) return true;
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
