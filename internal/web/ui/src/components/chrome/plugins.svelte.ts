import { mount, unmount } from "svelte";
import { t } from "../../lib/i18n.svelte";
import { rowByKey } from "../../lib/lens";
import { unitKey, type J } from "../../lib/util";
import { act } from "../../state/actions.svelte";
import { writeHash } from "../../state/route.svelte";
import { plugins, state } from "../../state/store.svelte";
import PluginDetail from "./PluginDetail.svelte";
import PluginList from "../rows/PluginList.svelte";

// THEME_TOKENS are the custom properties of app.css that ctx.theme() hands a plugin view.
const THEME_TOKENS = ["bg", "panel", "raise", "code-bg", "ink", "muted", "faint", "rule", "rule-strong", "hover", "sel", "me", "author", "reviewer", "loop", "none",
  "me-text", "author-text", "reviewer-text", "loop-text", "none-text", "on-me", "mine-bg", "mine-text", "late", "late-bg", "err", "err-bg", "mark", "mono", "sans"];

export interface Host {
  rows: () => J[] | null;
  select: (x: J) => void;
  act: (unit: J, action: string, inputs: J) => unknown;
  list: (el: HTMLElement, rows: J[], opts: J) => void;
  detail: (el: HTMLElement, unit: J) => void;
}

type Sub = { fn: (rows: J[]) => void };
const subscribers = new Set<Sub>();
const keys = new Map<string, { label: string; fn: (e: KeyboardEvent) => void }>();
const mounted = { view: "", el: null as HTMLElement | null, mod: null as J, cancel: null as null | (() => void), seq: 0, rows: null as J[] | null };

export function pluginModuleURL(plugin: string, module: string): string {
  return "/plugins/" + encodeURIComponent(plugin) + "/" + module.split("/").map(encodeURIComponent).join("/");
}

export function pluginTheme(): { mode: string; tokens: Record<string, string> } {
  const dark = typeof matchMedia === "function" && matchMedia("(prefers-color-scheme: dark)").matches;
  const style = getComputedStyle(document.documentElement);
  const tokens: Record<string, string> = {};
  for (const name of THEME_TOKENS) tokens[name] = style.getPropertyValue("--" + name).trim();
  return { mode: dark ? "dark" : "light", tokens };
}

// pluginContext is the ctx a plugin's mount gets; cancel drops what it subscribed and registered.
function pluginContext(host: Host): { ctx: J; cancel: () => void } {
  const owned = new Set<() => void>();
  const own = (drop: () => void) => {
    let done = false;
    const cancel = () => {
      if (done) return;
      done = true;
      owned.delete(cancel);
      drop();
    };
    owned.add(cancel);
    return cancel;
  };
  const ctx = {
    subscribe(fn: (rows: J[]) => void) {
      const sub = { fn };
      subscribers.add(sub);
      const rows = host.rows();
      if (rows) fn(rows);
      return own(() => subscribers.delete(sub));
    },
    select: (x: J) => host.select(x),
    act: (unit: J, action: string, inputs: J) => Promise.resolve(host.act(unit, action, inputs || {})),
    theme: pluginTheme,
    keys: {
      register(key: string, label: string, fn: (e: KeyboardEvent) => void) {
        const entry = { label, fn };
        keys.set(key, entry);
        return own(() => {
          if (keys.get(key) === entry) keys.delete(key);
        });
      },
    },
    list: (el: HTMLElement, rows: J[], opts: J) => host.list(el, rows, opts || {}),
    detail: (el: HTMLElement, unit: J) => host.detail(el, unit),
  };
  return { ctx, cancel: () => [...owned].forEach((c) => c()) };
}

// drawn are the cockpit's components mounted into the elements a plugin view handed over, with the props they
// draw, so a second hand-over of one element updates them in place.
const drawn = new Map<HTMLElement, { kind: string; inst: J; props: J }>();

function draw(el: HTMLElement, kind: string, component: J, props: J): void {
  const d = drawn.get(el);
  if (d && d.kind === kind) {
    Object.assign(d.props, props);
    return;
  }
  if (d) unmount(d.inst);
  el.textContent = "";
  const p = $state(props);
  drawn.set(el, { kind, inst: mount(component, { target: el, props: p }), props: p });
}

function undraw(): void {
  for (const d of drawn.values()) unmount(d.inst);
  drawn.clear();
}

export function unmountPluginView(): void {
  mounted.seq++;
  const { mod, cancel, el } = mounted;
  if (mod && typeof mod.unmount === "function") {
    try {
      mod.unmount();
    } catch (err) {
      console.error(err);
    }
  }
  if (cancel) cancel();
  undraw();
  if (el) el.textContent = "";
  Object.assign(mounted, { view: "", el: null, mod: null, cancel: null, rows: null });
}

// importer is how a plugin view's module loads; tests replace it.
export const importer = { load: (url: string): Promise<J> => import(/* @vite-ignore */ url) };

// showPluginView mounts view's module in el, once; it unmounts whatever view was mounted before. A navigation
// during the import wins.
export async function showPluginView(view: string, el: HTMLElement, host: Host): Promise<void> {
  if (mounted.view === view && mounted.el === el) return;
  unmountPluginView();
  const spec = plugins.views.get(view);
  if (!spec) return;
  const seq = mounted.seq;
  Object.assign(mounted, { view, el });
  let mod: J;
  try {
    mod = await importer.load(pluginModuleURL(spec.plugin, spec.module));
  } catch (err) {
    if (seq === mounted.seq) el.textContent = t("Could not load the {plugin} view: {error}", { plugin: spec.plugin, error: (err as Error).message });
    return;
  }
  if (seq !== mounted.seq) return;
  const { ctx, cancel } = pluginContext(host);
  Object.assign(mounted, { mod, cancel });
  try {
    await mod.mount(el, ctx);
  } catch (err) {
    console.error(err);
  }
}

// publishPlugins hands the subscribers the rows when they changed since the last time.
export function publishPlugins(rows: J[]): void {
  if (rows === mounted.rows) return;
  mounted.rows = rows;
  for (const sub of [...subscribers]) {
    try {
      sub.fn(rows);
    } catch (err) {
      console.error(err);
    }
  }
}

// pluginKeyDown runs the mounted view's handler of e's key; false when it has none.
export function pluginKeyDown(e: KeyboardEvent): boolean {
  if (e.metaKey || e.ctrlKey || e.altKey || e.isComposing) return false;
  const entry = keys.get(e.key);
  if (!entry) return false;
  entry.fn(e);
  return true;
}

// cockpitHost is what a plugin view reaches of the cockpit: its rows, the detail pane, the actions and its list
// and detail drawn with the cockpit's components.
export const cockpitHost: Host = {
  rows: () => (state.loaded ? state.rows : null),
  select: (x) => writeHash({ pr: unitKey((x && x.unit) || x) }),
  act: (unit, action, inputs) => act(rowByKey(unitKey(unit)) || { unit, label: unit.key, actions: [] }, action, inputs),
  list: (el, rows, opts) => draw(el, "list", PluginList, { rows, empty: (opts && opts.empty) || "" }),
  detail: (el, unit) => draw(el, "detail", PluginDetail, { unit }),
};
