import { t } from "../lib/i18n.svelte";

// served are the key bindings the daemon serves the web (GET /api/keys?surface=web), which the keys module
// sets once they load; until then no hint names a key the dispatcher does not run.
export const served = $state({ bindings: [] as { key: string; action: string }[] });

export function servedKey(action: string | undefined): string {
  const b = action ? served.bindings.find((x) => x.action === action) : undefined;
  return b ? b.key : "";
}

// keyCap is the served key of action as a key cap reads it, "" when none is bound.
export function keyCap(action: string): string {
  return servedKey(action).replace(/Cmd\+/g, "⌘").replace(/Ctrl\+/g, "⌃").replace(/Alt\+/g, "⌥").replace(/Shift\+/g, "⇧");
}

// keyHint is label with the key the served bindings give action.
export function keyHint(label: string, action: string | undefined): string {
  const key = servedKey(action);
  return key ? t("{action} ({key})", { action: label, key }) : label;
}

// ariaKey names key as aria-keyshortcuts spells it.
export function ariaKey(key: string): string {
  return key === "+" ? "Plus" : key;
}
