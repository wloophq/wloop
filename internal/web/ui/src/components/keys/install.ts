import { t } from "../../lib/i18n.svelte";
import { getJSON } from "../../state/api";
import { served } from "../../state/keycaps.svelte";
import { showOmnibox } from "../../state/overlays.svelte";
import { createKeys, type Binding, type Keys, type KeysEnv } from "./keys";

// RELOAD_AFTER_MS is how long after a failed read of the bindings a key asks for them again.
const RELOAD_AFTER_MS = 5000;

// cockpit is the page's dispatcher once the keys are installed.
export const cockpit = { keys: null as Keys | null };

// openOmnibox opens the omnibox over the selection's actions, as Cmd+K does.
export function openOmnibox(): void {
  if (cockpit.keys) cockpit.keys.openPalette();
  else showOmnibox();
}

// installKeys reads the bindings the daemon serves the web into served, which the key hints read, and
// dispatches each key of target; the returned stop removes the listener.
export function installKeys(env: KeysEnv, target: Pick<Document, "addEventListener" | "removeEventListener"> = document, now: () => number = () => performance.now()): { keys: Keys; loaded: Promise<void>; stop: () => void } {
  const keys = createKeys(env, served as { bindings: Binding[] });
  cockpit.keys = keys;
  let loadedAt = 0;
  const load = async () => {
    loadedAt = now();
    try {
      keys.setBindings((await getJSON("/api/keys?surface=web")).bindings);
    } catch (err) {
      if (env.notice) env.notice(t("Could not load the keys: {error}", { error: (err as Error).message }), true);
    }
  };
  const down = (e: Event) => {
    if (!keys.bindings().length && now() - loadedAt > RELOAD_AFTER_MS) void load();
    void keys.handle(e as KeyboardEvent);
  };
  target.addEventListener("keydown", down);
  return {
    keys,
    loaded: load(),
    stop: () => {
      target.removeEventListener("keydown", down);
      if (cockpit.keys === keys) cockpit.keys = null;
    },
  };
}
