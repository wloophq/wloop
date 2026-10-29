"use strict";

const CHORD_MS = 1000;
const KEY_CONTEXTS = ["question", "plan", "review", "list", "global"];
const CORE_ACTIONS = /^(move:|detail:|nav:|area:|answer:|step:|palette$|help$|filter$|open-source$|capture$|quit$)/;
const NAMED_KEYS = { Enter: "Enter", Escape: "Esc", Esc: "Esc", Backspace: "Backspace", Tab: "Tab" };

// VERB_ASKS are, by handler, how a verb that asks for a value first turns
// the answer into its arguments, null when the answer is not usable; the
// question is the served verb's input.
const VERB_ASKS = {
  waiting: { args: (v) => (v ? { on: v } : {}) },
  schedule: { args: (v) => (!v ? null : v.toLowerCase() === "activity" ? { on_activity: "true" } : { until: v }) },
  project: { args: (v) => (v ? { title: v } : null) },
  context: { args: (v) => ({ set: v }) },
  area: { args: (v) => ({ set: v }) },
  delegate: { args: (v) => (v ? { guidance: v } : {}) },
  reply: { args: (v) => (v ? { text: v } : null) },
};

// servedAsk is how the served verb named verb, among specs, asks for its
// value: its input and its handler's args; null for a verb that asks none.
function servedAsk(verb, specs) {
  const spec = (specs || []).find((v) => v.verb === verb);
  const ask = spec && spec.input ? VERB_ASKS[spec.handler] : null;
  return ask ? { label: spec.input, args: ask.args } : null;
}

// eventKey is the canonical name of a key event, as the served bindings
// spell it; "" when no binding can match. Shift+digit reads the physical
// key, so layouts such as ABNT2, where Shift+6 types "¨", still pick areas.
function eventKey(e) {
  if (e.isComposing) return "";
  const altGr = e.ctrlKey && e.altKey;
  const letter = typeof e.key === "string" && /^[a-z]$/i.test(e.key);
  if (e.metaKey) return letter ? "Cmd+" + e.key.toUpperCase() : "";
  if (e.ctrlKey && !altGr) return letter ? "Ctrl+" + e.key.toUpperCase() : "";
  if (e.altKey && !altGr) return "";
  if (NAMED_KEYS[e.key]) return NAMED_KEYS[e.key];
  const digit = /^Digit([1-9])$/.exec(e.code || "");
  if (digit) return e.shiftKey ? "Shift+" + digit[1] : digit[1];
  if (letter) return e.shiftKey ? e.key.toUpperCase() : e.key.toLowerCase();
  return typeof e.key === "string" && [...e.key].length === 1 && e.key !== " " ? e.key : "";
}

function typingTarget(el) {
  if (!el || typeof el.tagName !== "string") return false;
  const tag = el.tagName.toUpperCase();
  if (tag === "INPUT") return !["checkbox", "radio", "button", "submit", "reset"].includes(String(el.type || "text").toLowerCase());
  return tag === "TEXTAREA" || tag === "SELECT" || !!el.isContentEditable;
}

// pressable elements take Enter themselves.
function pressable(el) {
  if (!el || typeof el.tagName !== "string") return false;
  const tag = el.tagName.toUpperCase();
  const role = el.getAttribute ? el.getAttribute("role") : null;
  return ["BUTTON", "A", "SUMMARY"].includes(tag) || role === "button" || role === "link";
}

const isTask = (sel) => !!sel && typeof sel.id === "string" && Array.isArray(sel.units);
const unitsOf = (sel) => (!sel ? [] : isTask(sel) ? sel.units : sel.unit ? [sel.unit] : []);
const sameUnit = (a, b) => !!a && !!b && a.provider === b.provider && a.key === b.key;

// sourceLink is the link of the selection's first unit.
function sourceLink(sel) {
  if (!sel) return "";
  if (!isTask(sel)) return safeURL(sel.link) || "";
  const u = sel.units[0];
  const c = (sel.cards || []).find((x) => x.card && sameUnit(x.card.unit, u));
  return (c && safeURL(c.card.link)) || "";
}

// questionOptions are a question's answers in the order the card shows
// them: yes and no for a confirmation, the recommended option first.
function questionOptions(q) {
  if (!q) return [];
  if (q.kind === "confirm") return [{ yes: true }, { no: true }];
  const opts = q.options || [];
  return [...opts.filter((o) => o.id === q.recommend), ...opts.filter((o) => o.id !== q.recommend)].map((o) => ({ option_id: o.id }));
}

// keyContextOf is the key context a view adds before the list's.
const keyContextOf = (view) => (view === "plan" || view === "review" ? view : "");

const CONTEXT_HEADS = { global: "Everywhere", list: "On the selection", question: "In a question", plan: "In the plan", review: "In the review" };

const HELP_ORDER = ["global", "list", "question", "plan", "review"];

// helpGroups are every binding by context.
function helpGroups(bindings) {
  const order = (c) => (HELP_ORDER.includes(c) ? HELP_ORDER.indexOf(c) : HELP_ORDER.length);
  const contexts = [...new Set(bindings.map((b) => b.context))].sort((a, b) => order(a) - order(b) || a.localeCompare(b));
  return contexts.map((c) => ({ context: c, head: CONTEXT_HEADS[c] || c, bindings: bindings.filter((b) => b.context === c) }));
}

function createKeys(env) {
  let bindings = [];
  let chord = null;
  const now = env.now || (() => Date.now());
  // taskVerbs are the verbs the catalog serves.
  const taskVerbs = () => new Set(env.verbs ? env.verbs() : []);
  const keyVerb = (name) => taskVerbs().has(name);
  // A served action's key is one uppercase letter, or its plan context's own key.
  const servedAction = (b) => !!b.served || /^[A-Z]$/.test(b.key) || (b.context === "plan" && !CORE_ACTIONS.test(b.action) && !keyVerb(b.action));

  const isPrefix = (key) => bindings.some((b) => b.key.startsWith(key + " "));
  // reserved mirrors keys.Reserved: a core key, a chord prefix, a lowercase
  // letter or a digit.
  const reserved = (key) => /^[a-z0-9]$/.test(key) || isPrefix(key) || bindings.some((b) => b.owner === "core" && !servedAction(b) && b.key === key);
  const bindingOf = (action, context) => bindings.find((b) => b.action === action && (!context || b.context === context));

  async function postJSON(path, body) {
    const res = await env.fetch(path, {
      method: "POST",
      headers: { "Content-Type": "application/json", Accept: "application/json", "X-Loop-API": API_VERSION },
      body: JSON.stringify(body),
    });
    const out = await res.json().catch(() => ({}));
    if (!res.ok || out.error) throw new Error(out.error || `HTTP ${res.status}`);
    return out;
  }

  const say = (text, err) => env.notice && env.notice(text, err);

  async function taskVerb(task, verb, args) {
    if (env.act) return env.act(verb, args, task);
    const body = { source: "cockpit" };
    if (args && Object.keys(args).length) body.args = Object.fromEntries(Object.entries(args).map(([k, v]) => [k, String(v)]));
    try {
      const out = await postJSON(`/api/tasks/${encodeURIComponent(task.id)}/${encodeURIComponent(verb)}`, body);
      say(`${verb}: ${task.title || task.id}`);
      if (env.changed) env.changed();
      return out;
    } catch (err) {
      say(`${verb} ${task.title || task.id} failed: ${err.message}`, true);
    }
  }

  async function verb(name) {
    const task = isTask(env.selected()) ? env.selected() : null;
    if (!task) return false;
    const st = (task.verbs || []).find((v) => v.verb === name);
    if (!st || !st.enabled) {
      say(`${name}: ${(st && st.reason) || "not offered here"}`, true);
      return true;
    }
    let args = {};
    const ask = servedAsk(name, env.verbSpecs ? env.verbSpecs() : []);
    if (ask) {
      const v = await env.ask({ label: ask.label });
      if (v === null || v === undefined) return true;
      args = ask.args(String(v).trim());
      if (!args) return true;
    }
    await taskVerb(task, name, args);
    return true;
  }

  // unitRows are the rows whose served actions the selection's unit offers.
  function unitRows(sel) {
    if (sel && !isTask(sel) && sel.unit) return [sel];
    return unitsOf(sel).map((u) => env.rowOf && env.rowOf(u)).filter(Boolean);
  }

  async function unitAction(id) {
    const sel = env.selected();
    for (const row of unitRows(sel)) {
      const st = (row.actions || []).find((a) => a.id === id && a.enabled);
      if (!st) continue;
      const spec = (env.spec && env.spec(id)) || {};
      const extra = {};
      const input = spec.input;
      if (input && (input.kind === "text" || input.kind === "answer")) {
        const v = await env.ask({ label: input.label || st.label || id, placeholder: input.placeholder });
        if (v === null || v === undefined) return true;
        extra.guidance = String(v).trim();
      }
      await env.unitAction(row, id, extra);
      return true;
    }
    return false;
  }

  async function answer(n, target) {
    const q = env.questionAt && env.questionAt(target);
    if (!q) return false;
    if (q.gate) {
      const intent = (q.gate.intents || [])[n - 1];
      if (intent && env.resolveGate) await env.resolveGate(q.task, q.gate, intent.id);
      return true;
    }
    const pick = questionOptions(q.question)[n - 1];
    if (!pick) return true;
    if (q.task) await taskVerb(q.task, "answer", pick);
    else if (q.row) await env.unitAction(q.row, "answer", pick);
    return true;
  }

  // planAction runs id on the focused plan step. With no step focused, Enter
  // focuses the first one and every other plan key does nothing.
  async function planAction(id, key) {
    const step = env.planStep && env.planStep();
    if (!step) {
      if (key === "Enter") env.move(1);
      return true;
    }
    if (!(step.actions || []).some((a) => a.id === id && a.enabled)) return false;
    await env.planAction(step, id);
    return true;
  }

  // navTarget is the view nav:<to> opens: the first view of the plugin to
  // names, "" for a plugin with none served, else the view whose id is to.
  function navTarget(to) {
    const vs = (env.views ? env.views() : []).filter((v) => v.provider === to);
    vs.sort((a, b) => (a.order || 0) - (b.order || 0));
    if (vs.length) return vs[0].id;
    return env.providers && env.providers().includes(to) ? "" : to;
  }

  async function areaList() {
    try {
      return (await env.areas()) || [];
    } catch (err) {
      say(`Could not load the areas: ${err.message}`, true);
      return null;
    }
  }

  async function pickArea(n) {
    const areas = await areaList();
    if (!areas) return true;
    const a = areas[n - 1];
    if (!a) return false;
    env.setArea(a);
    return true;
  }

  async function chooseArea() {
    const areas = await areaList();
    if (!areas) return true;
    const items = [{ label: "No area", key: "", run: () => env.setArea("") }, ...areas.map((a, i) => ({ label: a, key: i < 9 ? `Shift+${i + 1}` : "", run: () => env.setArea(a) }))];
    env.palette({ title: "Area", items });
    return true;
  }

  async function capture() {
    const title = await env.ask({ label: "Capture" });
    if (!title || !String(title).trim()) return true;
    try {
      await postJSON("/api/tasks", { title: String(title).trim() });
      say(`captured: ${String(title).trim()}`);
      if (env.changed) env.changed();
    } catch (err) {
      say(`capture failed: ${err.message}`, true);
    }
    return true;
  }

  // paletteItems are every action of the selection with its key, the global
  // actions, then "Area...".
  function paletteItems() {
    const sel = env.selected();
    const items = [];
    const item = (b, label) => ({ label: label || b.help || b.action, key: b.key || "", run: () => run(b, {}) });
    const listBinding = (action, served) => bindings.find((b) => b.action === action && b.context === "list" && servedAction(b) === served);
    if (isTask(sel)) {
      for (const v of (sel.verbs || []).filter((x) => x.enabled && keyVerb(x.verb) && (listBinding(x.verb, false) || servedAsk(x.verb, env.verbSpecs ? env.verbSpecs() : [])))) {
        const b = listBinding(v.verb, false) || { key: "", action: v.verb, context: "list" };
        items.push(item(b, b.help || v.verb));
      }
    }
    for (const row of unitRows(sel)) {
      for (const st of (row.actions || []).filter((a) => a.enabled)) {
        const b = listBinding(st.id, true) || { key: "", action: st.id, context: "list", served: true };
        items.push(item(b, st.label || b.help || st.id));
      }
    }
    if (sourceLink(sel)) items.push(item(bindingOf("open-source", "list") || { key: "", action: "open-source", context: "list" }));
    for (const b of bindings.filter((x) => x.context === "global" && !/^(palette|area:)/.test(x.action))) items.push(item(b));
    items.push({ label: "Area...", key: "", run: () => chooseArea() });
    for (const extra of (env.extraItems && env.extraItems()) || []) items.push(extra);
    return items;
  }

  async function run(b, ctx) {
    const a = b.action;
    if (b.context === "plan") return servedAction(b) ? planAction(a, b.key) : a === "approve" ? planAction("plan_apply", b.key) : false;
    if (servedAction(b)) return b.context === "list" ? unitAction(a) : false;
    if (a === "move:down" || a === "move:up") return env.move(a === "move:down" ? 1 : -1) !== false;
    if (a === "detail:open") return env.open() !== false;
    if (a === "detail:close") return env.close() !== false;
    if (a.startsWith("nav:")) {
      const view = navTarget(a.slice(4));
      if (!view) return false;
      env.navigate(view);
      return true;
    }
    if (a.startsWith("area:")) return pickArea(Number(a.slice(5)));
    if (a.startsWith("answer:")) return answer(Number(a.slice(7)), ctx.target);
    if (a.startsWith("step:")) return env.step ? env.step(a.slice(5)) !== false : false;
    if (a === "palette") {
      env.palette({ title: "Actions", items: paletteItems() });
      return true;
    }
    if (a === "help") {
      env.help(helpGroups(bindings));
      return true;
    }
    if (a === "filter") return env.filter() !== false;
    if (a === "capture") return capture();
    if (a === "open-source") {
      const link = sourceLink(env.selected());
      if (!link) return false;
      env.openURL(link);
      return true;
    }
    if (keyVerb(a)) return verb(a);
    return false;
  }

  // handle dispatches e; true when a binding took it.
  async function handle(e) {
    if (e.defaultPrevented) return false;
    const key = eventKey(e);
    if (!key) return false;
    if (env.overlay && env.overlay() && key !== "Esc") return false;
    if (typingTarget(e.target)) {
      if (key !== "Esc") return false;
      e.preventDefault();
      if (env.leave) env.leave(e.target);
      return true;
    }
    if (chord && now() - chord.at <= CHORD_MS) {
      const full = `${chord.prefix} ${key}`;
      chord = null;
      if (env.hint) env.hint("");
      e.preventDefault();
      const b = bindings.find((x) => x.key === full);
      if (b) await run(b, { target: e.target });
      return true;
    }
    chord = null;
    if (env.pluginKey && !reserved(key) && e.key === key && env.pluginKey(e)) {
      e.preventDefault();
      return true;
    }
    if (key === "Enter" && pressable(e.target)) return false;
    const contexts = ["list", "global"];
    if (env.questionAt && env.questionAt(e.target)) contexts.unshift("question");
    const view = env.viewContext ? env.viewContext() : "";
    if (view) contexts.unshift(view);
    const candidates = bindings.filter((b) => b.key === key && contexts.includes(b.context))
      .sort((x, y) => contexts.indexOf(x.context) - contexts.indexOf(y.context) || Number(servedAction(y)) - Number(servedAction(x)));
    if (!candidates.length && isPrefix(key)) {
      e.preventDefault();
      const c = { prefix: key, at: now() };
      chord = c;
      if (env.hint) {
        env.hint(`${key} …`);
        (env.later || setTimeout)(() => {
          if (chord !== c) return;
          chord = null;
          env.hint("");
        }, CHORD_MS);
      }
      return true;
    }
    if (!candidates.length) return false;
    e.preventDefault();
    for (const b of candidates) {
      if (await run(b, { target: e.target })) return true;
    }
    return true;
  }

  return {
    handle,
    paletteItems,
    setBindings(list) { bindings = (list || []).map((b) => ({ ...b })); },
    bindings: () => bindings,
  };
}

// appEnv binds the dispatcher to the cockpit's page.
function appEnv() {
  const rowOf = (u) => state.rows.find((r) => sameUnit(unitOf(r), u)) || (state.detail && sameUnit(unitOf(state.detail.row), u) ? state.detail.row : null);
  const focusedRow = () => {
    const el = document.activeElement && document.activeElement.closest && document.activeElement.closest('[data-focus^="row:"], [data-focus^="card:"]');
    const m = el && /^(?:row|card):(.+?)(?:@.*)?$/.exec(el.dataset.focus);
    return m ? state.rows.find((r) => keyOf(r) === m[1]) || null : null;
  };
  const selectedRow = () => {
    const pr = readHash().pr;
    return focusedRow() || (pr && state.rows.find((r) => keyOf(r) === pr)) || (state.detail && state.detail.row) || null;
  };
  const planItems = () => planGroups(state.plan.items).flatMap((g) => g.items);
  const focusedPlan = () => {
    const m = /^plan:(.+):[^:]+$/.exec((document.activeElement && document.activeElement.dataset && document.activeElement.dataset.focus) || "");
    return m ? planItems().find((it) => it.id === m[1]) || null : null;
  };
  const focusPlan = (it) => {
    const acts = it.actions || [];
    const id = acts.some((a) => a.id === "plan_apply" && a.enabled) ? "plan_apply" : (acts.find((a) => a.enabled) || {}).id;
    const el = document.querySelector(`[data-focus="${CSS.escape(`plan:${it.id}:${id}`)}"]`);
    if (el) el.focus();
  };
  const setHashParam = (name, value) => {
    const p = new URLSearchParams(location.hash.slice(1));
    if (value) p.set(name, value);
    else p.delete(name);
    location.hash = p.toString();
  };
  let areas = null;
  const taskNodes = () => [...document.querySelectorAll('#tasks [data-focus^="task:"]')];
  return {
    fetch: (...a) => fetch(...a),
    selected: () => detailPane.selected() || selectedRow(),
    act: (verb, args, task) => detailPane.act(verb, args, task),
    verbs: () => ((tasksState.catalog && tasksState.catalog.verbs) || []).map((v) => v.verb),
    verbSpecs: () => (tasksState.catalog && tasksState.catalog.verbs) || [],
    resolveGate: (task, gate, intent) => detailPane.resolve(task, gate, intent),
    rowOf,
    spec: (id) => state.specs.byId[id],
    unitAction: (row, id, extra) => act(row, id, extra),
    planStep: focusedPlan,
    overlay: () => !!document.querySelector(".pal-back"),
    pluginKey: (e) => pluginViews.has(readHash().view) && pluginKeyDown(e),
    planAction: (it, id) => actOnPlan(it, id),
    viewContext: () => keyContextOf(readHash().view),
    step: (action) => reviewKey(action),
    questionAt: (el) => {
      if (readHash().task) return detailPane.gateAt(el);
      const d = state.detail;
      if (!el || !el.closest || !el.closest(".d-question") || !d || !d.row.question || d.row.question.state !== "asked") return null;
      return { row: d.row, question: d.row.question };
    },
    move: (delta) => {
      if (isCoreView(readHash().view)) {
        const nodes = taskNodes();
        if (!nodes.length) return false;
        const sel = detailPane.selected();
        const at = sel ? nodes.findIndex((n) => n.dataset.focus === "task:" + sel.id) : -1;
        nodes[Math.max(0, Math.min(nodes.length - 1, at < 0 ? 0 : at + delta))].focus();
        return true;
      }
      if (readHash().view === "plan") {
        const items = planItems();
        const at = items.indexOf(focusedPlan());
        const next = items[Math.max(0, Math.min(items.length - 1, at < 0 ? 0 : at + delta))];
        if (next) focusPlan(next);
        return !!next;
      }
      const rows = visibleRows();
      if (!rows.length) return false;
      const cur = selectedRow();
      const at = cur ? rows.findIndex((r) => keyOf(r) === keyOf(cur)) : -1;
      const next = rows[Math.max(0, Math.min(rows.length - 1, at < 0 ? 0 : at + delta))];
      const el = document.querySelector(`[data-focus="${CSS.escape(`row:${keyOf(next)}`)}"], [data-focus="${CSS.escape(`card:${keyOf(next)}`)}"]`);
      if (el) el.focus();
      return true;
    },
    open: () => {
      const e = detailPane.selected();
      if (e) {
        writeHash({ task: e.id, pr: "" });
        return true;
      }
      const r = selectedRow();
      if (!r) return false;
      openRow(`row:${keyOf(r)}`, keyOf(r));
      return true;
    },
    close: () => {
      if (readHash().task) {
        writeHash({ task: "" });
        return true;
      }
      if (!readHash().pr) return false;
      closeDetail();
      return true;
    },
    navigate: (view) => setHashParam("view", view),
    views: () => catalog.views,
    providers: () => [...providerNames.keys()],
    areas: async () => {
      if (!areas) areas = (await getJSON("/api/views")).areas || [];
      return areas;
    },
    setArea: (a) => setHashParam("area", a),
    filter: () => {
      const box = document.getElementById("search");
      box.focus();
      box.select();
    },
    openURL: (u) => window.open(u, "_blank", "noopener"),
    leave: (el) => el.blur(),
    ask: (o) => askInput(o),
    palette: (o) => showPalette(o),
    help: (groups) => showHelp(groups),
    notice: (text, err) => setNotice(text, err),
    hint: (text) => setNotice(text),
    changed: () => scheduleLoad(),
    extraItems: () => ["group", "sort"].map((id) => ({
      label: id === "group" ? "Group by..." : "Sort by...",
      key: "",
      run: () => {
        const pick = document.querySelector(`[data-focus="lens:${id}"]`);
        if (pick && !pick.closest("[hidden]")) pick.focus();
      },
    })),
  };
}

async function installKeys(env) {
  const keys = createKeys(env);
  let loadedAt = 0;
  const load = async () => {
    loadedAt = Date.now();
    try {
      keys.setBindings((await getJSON("/api/keys?surface=web")).bindings);
    } catch (err) {
      env.notice(`Could not load the keys: ${err.message}`, true);
    }
  };
  document.addEventListener("keydown", (e) => {
    if (!keys.bindings().length && Date.now() - loadedAt > 5000) load();
    keys.handle(e);
  });
  await load();
  return keys;
}

if (typeof document !== "undefined" && typeof state !== "undefined") installKeys(appEnv());
