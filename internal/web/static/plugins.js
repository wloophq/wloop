"use strict";

// THEME_TOKENS are the custom properties of app.css that ctx.theme() hands
// a plugin view.
const THEME_TOKENS = ["bg", "panel", "ink", "muted", "rule", "hover", "sel", "me", "author", "reviewer", "loop", "none",
  "me-text", "author-text", "reviewer-text", "loop-text", "none-text", "on-me", "mine-bg", "mine-text", "late", "late-bg",
  "err", "err-bg", "mark", "mono", "sans"];

// pluginViews maps a web view id to the plugin and module that draw it, for
// the enabled plugins GET /api/plugins lists.
const pluginViews = new Map();
// providerNames maps a provider to its service as a person reads it, "GitHub" or "Slack".
const providerNames = new Map();
const pluginSubscribers = new Set();
const pluginKeys = new Map();
const pluginMount = { view: "", el: null, mod: null, cancel: null, seq: 0, rows: null };

function applyPlugins(list) {
  pluginViews.clear();
  for (const p of list || []) {
    if (p.name) providerNames.set(p.id, p.name);
    const web = p.enabled && p.ui && p.ui.web;
    if (!web || !web.module) continue;
    for (const id of web.views || []) pluginViews.set(id, { plugin: p.id, module: web.module });
  }
}

function providerName(id) {
  return providerNames.get(id) || id;
}

function pluginModuleURL(plugin, module) {
  return "/plugins/" + encodeURIComponent(plugin) + "/" + module.split("/").map(encodeURIComponent).join("/");
}

function pluginTheme() {
  const dark = typeof matchMedia === "function" && matchMedia("(prefers-color-scheme: dark)").matches;
  const style = getComputedStyle(document.documentElement);
  const tokens = {};
  for (const name of THEME_TOKENS) tokens[name] = style.getPropertyValue("--" + name).trim();
  return { mode: dark ? "dark" : "light", tokens };
}

// pluginContext is the ctx a plugin's mount gets; cancel drops what it
// subscribed and registered.
function pluginContext(host) {
  const owned = new Set();
  const own = (drop) => {
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
    subscribe(fn) {
      const sub = { fn };
      pluginSubscribers.add(sub);
      const rows = host.rows();
      if (rows) fn(rows);
      return own(() => pluginSubscribers.delete(sub));
    },
    select: (x) => host.select(x),
    act: (unit, action, inputs) => Promise.resolve(host.act(unit, action, inputs || {})),
    theme: pluginTheme,
    keys: {
      register(key, label, fn) {
        const entry = { label, fn };
        pluginKeys.set(key, entry);
        return own(() => {
          if (pluginKeys.get(key) === entry) pluginKeys.delete(key);
        });
      },
    },
    list: (el, rows, opts) => host.list(el, rows, opts || {}),
    detail: (el, unit) => host.detail(el, unit),
  };
  return { ctx, cancel: () => [...owned].forEach((c) => c()) };
}

function unmountPluginView() {
  pluginMount.seq++;
  const { mod, cancel, el } = pluginMount;
  if (mod && typeof mod.unmount === "function") {
    try {
      mod.unmount();
    } catch (err) {
      console.error(err);
    }
  }
  if (cancel) cancel();
  if (el) el.replaceChildren();
  Object.assign(pluginMount, { view: "", el: null, mod: null, cancel: null, rows: null });
}

// showPluginView mounts view's module in el, once; it unmounts whatever
// view was mounted before. A navigation during the import wins.
async function showPluginView(view, el, host, load = (url) => import(url)) {
  if (pluginMount.view === view && pluginMount.el === el) return;
  unmountPluginView();
  const spec = pluginViews.get(view);
  if (!spec) return;
  const seq = pluginMount.seq;
  Object.assign(pluginMount, { view, el });
  let mod;
  try {
    mod = await load(pluginModuleURL(spec.plugin, spec.module));
  } catch (err) {
    if (seq === pluginMount.seq) el.replaceChildren(`Could not load the ${spec.plugin} view: ${err.message}`);
    return;
  }
  if (seq !== pluginMount.seq) return;
  const { ctx, cancel } = pluginContext(host);
  Object.assign(pluginMount, { mod, cancel });
  try {
    await mod.mount(el, ctx);
  } catch (err) {
    console.error(err);
  }
}

// publishPlugins hands the subscribers the rows when they changed since the
// last time.
function publishPlugins(rows) {
  if (rows === pluginMount.rows) return;
  pluginMount.rows = rows;
  for (const sub of [...pluginSubscribers]) {
    try {
      sub.fn(rows);
    } catch (err) {
      console.error(err);
    }
  }
}

// pluginKeyDown runs the mounted view's handler of e's key; false when it
// has none.
function pluginKeyDown(e) {
  if (e.metaKey || e.ctrlKey || e.altKey || e.isComposing) return false;
  const entry = pluginKeys.get(e.key);
  if (!entry) return false;
  entry.fn(e);
  return true;
}

// cockpitHost is what a plugin view reaches of the cockpit: its rows, the
// detail pane, the actions and its list and detail rendering.
const cockpitHost = {
  rows: () => (state.loaded ? state.rows : null),
  select: (x) => writeHash({ pr: unitKey((x && x.unit) || x) }),
  act: (unit, action, inputs) => act(rowByKey(unitKey(unit)) || { unit, label: unit.key, actions: [] }, action, inputs),
  list: pluginList,
  detail: pluginDetail,
};

function pluginList(el, rows, opts) {
  if (!rows.length) {
    el.replaceChildren(h("p", { class: "empty" }, opts.empty || "Nothing here right now."));
    return;
  }
  el.replaceChildren(h("ol", { class: "plugin-list" }, ...rows.map((r) => h("li", { class: waitClass(r) },
    h("a", { href: "#" + lensHash({ pr: keyOf(r) }), class: "ref", "data-focus": "plugin:" + keyOf(r) }, labelOf(r)),
    " ", r.title || "",
    " ", h("span", { class: "pill" }, rowKindLabel(r)),
    sm(r).reason ? h("div", { class: "why" }, sm(r).reason) : null))));
}

function pluginDetail(el, unit) {
  const r = rowByKey(unitKey(unit));
  if (!r) {
    el.replaceChildren(h("p", { class: "empty" }, `${unit.key} is not in the rows.`));
    return;
  }
  el.replaceChildren(h("div", { class: "plugin-detail" },
    h("h2", {}, r.title || labelOf(r)),
    h("a", { href: safeURL(r.link), target: "_blank", rel: "noopener noreferrer", class: "ref" }, labelOf(r)),
    h("div", {}, "Waiting on ", h("strong", {}, waitingLabel(r))),
    sm(r).reason ? h("div", { class: "why" }, sm(r).reason) : null,
    h("div", { class: "d-actions" }, actionButtons(r, "detail", { scope: "plugin-detail" }))));
}

// renderPluginView shows the plugin view the hash names, mounting it once,
// and unmounts it when the hash leaves it or its plugin goes.
function renderPluginView(view) {
  let box = document.getElementById("plugin-view");
  if (!box) {
    box = h("section", { id: "plugin-view", class: "plugin-view", "aria-label": "Plugin view", hidden: true });
    document.getElementById("list").prepend(box);
  }
  const on = pluginViews.has(view);
  box.hidden = !on;
  if (!on) {
    if (pluginMount.view) unmountPluginView();
    return;
  }
  showPluginView(view, box, cockpitHost);
  if (state.loaded) publishPlugins(state.rows);
}
