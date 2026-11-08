"use strict";

// SKIP_HANDLERS are the handlers of the verbs the page offers elsewhere: seen when it opens, answer in its
// gate and a draft's in its card (gate.js); the verb whose handler replies is in the composer.
const SKIP_HANDLERS = new Set(["seen", "answer", "edit", "retry"]);
const detailState = {
  id: "", task: null, error: "", seenFor: "", editing: new Map(), unitKey: "", unitTasks: [], compose: { key: "", text: "", reaction: "" },
};

// detailEnv is what the pane reaches of the page; tests replace it.
const detailEnv = {
  getJSON: (url) => getJSON(url),
  post: (path, body) => taskPost(path, body),
  notice: (text, err) => setNotice(text, err),
  changed: () => scheduleLoad({ tasks: true }),
  render: () => render(),
  close: () => writeHash({ task: "" }),
  ask: (label, value) => prompt(label, value || ""),
  palette: (o) => showPalette(o),
  postJSON: (path, body) => taskPost(path, body),
  focused: () => {
    const el = typeof document !== "undefined" && document.activeElement && document.activeElement.closest && document.activeElement.closest('[data-row^="task:"]');
    return el ? el.dataset.row.slice("task:".length).replace(/@.*$/, "") : "";
  },
};

async function taskPost(path, body) {
  const res = await fetch(path, {
    method: "POST",
    headers: { "Content-Type": "application/json", Accept: "application/json", "X-Loop-API": API_VERSION },
    body: JSON.stringify(body),
  });
  const out = await res.json().catch(() => ({}));
  if (!res.ok || out.error) throw new Error(apiError(out, `HTTP ${res.status}`, res.status));
  return out;
}

function taskPath(id, ...rest) {
  return "/api/tasks/" + [id, ...rest].map((s) => encodeURIComponent(s).replace(/%3A/gi, ":")).join("/");
}

// verbBody is a verb's request: its args as strings.
function verbBody(args) {
  const body = { source: "cockpit" };
  const kept = Object.entries(args || {}).filter(([, v]) => v !== undefined && v !== null).map(([k, v]) => [k, String(v)]);
  if (kept.length) body.args = Object.fromEntries(kept);
  return body;
}

function taskById(id) {
  if (!id) return null;
  if (detailState.task && detailState.task.id === id) return detailState.task;
  const lists = [typeof tasksState === "object" ? tasksState.tasks : [], detailState.unitTasks];
  for (const list of lists) {
    const e = (list || []).find((x) => x.id === id);
    if (e) return e;
  }
  return null;
}

// taskAct runs verb on task and asks for a reload; the result, or null
// when it failed.
async function taskAct(task, verb, args) {
  if (!task) return null;
  try {
    const out = await detailEnv.post(taskPath(task.id, verb), verbBody(args));
    detailEnv.notice(`${verb}: ${task.title || task.id}${out && out.lines && out.lines.length ? ". " + out.lines.join(" ") : ""}`);
    if (verbHandler(verb) === "approve") detailState.editing.delete(task.id);
    detailEnv.changed();
    return out || {};
  } catch (err) {
    detailEnv.notice(t("{verb} {task} failed: {error}", { verb, task: task.title || task.id, error: err.message }), true);
    return null;
  }
}

// taskIntentAct runs the intent action id on the task's own unit, such as
// start, and asks for a reload.
async function taskIntentAct(task, id) {
  const path = "/api/units/" + ["task", task.id, id].map(encodeURIComponent).join("/");
  try {
    const out = await detailEnv.post(path, { source: "cockpit" });
    detailEnv.notice(`${id}: ${task.title || task.id}${out && out.output && out.output.length ? ". " + out.output.join(" ") : ""}`);
    detailEnv.changed();
  } catch (err) {
    detailEnv.notice(t("{verb} {task} failed: {error}", { verb: id, task: task.title || task.id, error: err.message }), true);
  }
}

// taskIntentButtons are the buttons of the intents that serve the task's
// own unit, as the task lists them.
function taskIntentButtons(e) {
  return (e.actions || []).map((a) => h("button", {
    type: "button",
    class: "act" + (a.enabled ? "" : " off"),
    disabled: !a.enabled,
    title: a.enabled ? a.note || undefined : a.reason,
    "data-focus": `detail:${a.id}:${e.id}`,
    onclick: () => taskIntentAct(e, a.id),
  }, a.label || a.id));
}

// draftTarget is the Outbox task of the draft e carries: e itself, or
// draft:<id> when e is a message task that only shows the draft.
function draftTarget(e) {
  const id = "draft:" + draftOf(e).id;
  return e.id === id ? e : { id, title: e.title, verbs: [] };
}

// approveEdited approves the draft of e with the text being edited.
async function approveEdited(e) {
  const out = await taskAct(draftTarget(e), verbFor("approve"), { text: detailState.editing.get(e.id), rev: String(draftOf(e).rev) });
  if (out) detailState.editing.delete(e.id);
  return out;
}

// actOn runs verb on task; an approve while its draft is being edited
// sends the edited text.
function actOn(task, verb, args) {
  const d = task ? draftOf(task) : null;
  const handler = verbHandler(verb);
  if (task && handler === "approve" && d && detailState.editing.has(task.id)) return approveEdited(task);
  if (task && d && task.id.startsWith("draft:") && (handler === "approve" || handler === "retry")) args = { ...args, rev: String(d.rev) };
  return taskAct(task, verb, args);
}

// detailPane is what keys.js reaches: the selected task and its verbs.
const detailPane = {
  selected: () => taskById(detailEnv.focused()) || detailState.task,
  act: (verb, args, task) => actOn(task || detailPane.selected(), verb, args),
  load: (route) => loadDetailPane(route),
  resolve: (task, gate, intent) => resolveGate(task, gate, intent),
  tab: (dir) => stepTab(detailState.task, dir === "prev" ? -1 : 1, true),
};

async function loadDetailPane(route) {
  if (!route.task) {
    Object.assign(detailState, { id: "", task: null, error: "", seenFor: "" });
    return loadUnitTasks(route.unit);
  }
  const id = route.task;
  if (detailState.id !== id) Object.assign(detailState, { id, task: null, error: "", seenFor: "" });
  try {
    const e = await detailEnv.getJSON(taskPath(id));
    if (detailState.id !== id) return;
    detailState.task = e;
    detailState.error = "";
    await loadTaskPage(e);
    if (e.category === "inbox" && detailState.seenFor !== id) {
      detailState.seenFor = id;
      detailEnv.post(taskPath(id, verbFor("seen")), { source: "cockpit" }).catch(() => {});
    }
  } catch (err) {
    if (detailState.id !== id) return;
    detailState.task = null;
    detailState.error = err.message;
  }
}

// loadUnitTasks reads the open tasks that hold the unit key, "<provider>:<key>".
async function loadUnitTasks(key) {
  detailState.unitKey = key || "";
  if (!key) {
    detailState.unitTasks = [];
    return;
  }
  const out = await detailEnv.getJSON(unitPath(keyUnit(key), "tasks")).catch(() => []);
  if (detailState.unitKey !== key) return;
  detailState.unitTasks = out;
}

// verbState is how verb stands on e; null when e does not offer it.
function verbState(e, verb) {
  return (e.verbs || []).find((v) => v.verb === verb) || null;
}

function verbButton(e, verb, label, run, extra = {}) {
  const st = verbState(e, verb);
  if (!st) return null;
  return h("button", {
    type: "button",
    class: "act" + (st.enabled ? "" : " off") + (extra.primary ? " primary" : ""),
    disabled: !st.enabled,
    title: [verbTask(verb).summary, st.enabled ? "" : st.reason].filter(Boolean).join("\n") || undefined,
    "data-focus": `${extra.scope || "detail"}:${verb}:${e.id}`,
    "aria-label": extra.name,
    onclick: run,
  }, label);
}

// askedArgs asks the value a verb needs first; null when the user backed out.
function askedArgs(verb) {
  const ask = typeof servedAsk === "function" ? servedAsk(verb, (typeof tasksState === "object" && tasksState.catalog && tasksState.catalog.verbs) || []) : null;
  if (!ask) return {};
  const v = detailEnv.ask(ask.label);
  if (v === null || v === undefined) return null;
  return ask.args(String(v).trim());
}

// handlerVerb is the catalog's verb whose handler is handler, its own name when it names none; "" when none is.
function handlerVerb(handler) {
  const v = ((typeof tasksState === "object" && tasksState.catalog && tasksState.catalog.verbs) || []).find((x) => (x.handler || x.verb) === handler);
  return v ? v.verb : "";
}

// verbFor is the verb that runs handler, handler itself before the catalog was read.
function verbFor(handler) {
  return handlerVerb(handler) || handler;
}

// verbHandler is the handler verb runs: its profile handler, else its own name, as the tasks service binds it.
function verbHandler(verb) {
  return verbTask(verb).handler || verb;
}

// offeredVerb is the verb e offers that runs handler, "" when it offers none.
function offeredVerb(e, handler) {
  const v = (e.verbs || []).find((x) => verbHandler(x.verb) === handler);
  return v ? v.verb : "";
}

// verbTask is the catalog's word for verb: its label and summary.
function verbTask(verb) {
  return ((typeof tasksState === "object" && tasksState.catalog && tasksState.catalog.verbs) || []).find((x) => x.verb === verb) || {};
}

function verbLabel(verb) {
  return verbTask(verb).label || verb;
}

// offeredVerbs are the verbs e offers that the page lists as its own: not those it offers elsewhere, the reply
// verb, a verb on a related or merged task's row, nor, on a draft's own task, those of the draft's card.
function offeredVerbs(e) {
  const replies = handlerVerb("reply");
  const onRows = (verb) => ["merged", "related"].includes(verbTask(verb).pick);
  const skip = (verb) => SKIP_HANDLERS.has(verbHandler(verb)) || verb === replies || onRows(verb) || (isDraftTask(e) && DRAFT_CARD_HANDLERS.includes(verbHandler(verb)));
  return (e.verbs || []).filter((v) => !skip(v.verb));
}

// runVerb runs verb on e: over the tasks it picks from, or after the question it puts first.
function runVerb(e, verb) {
  if (verbTask(verb).pick) return pickTask(e, verb);
  const args = askedArgs(verb);
  return args ? taskAct(e, verb, args) : null;
}

// detailVerbs are the verbs the profile's detail.verbs names, in its order.
function detailVerbs() {
  return ((typeof tasksState === "object" && tasksState.catalog && tasksState.catalog.detail_verbs) || []);
}

// primaryVerbs are the buttons of the profile's detail verbs e offers, each with its key, then More, which
// opens the palette over the other verbs e offers.
function primaryVerbs(e) {
  const offered = offeredVerbs(e);
  const named = detailVerbs();
  const buttons = named.map((verb) => offered.find((v) => v.verb === verb)).filter(Boolean).map((v) => {
    const key = verbTask(v.verb).key;
    return verbButton(e, v.verb, [h("span", { class: "lb" }, verbLabel(v.verb)), key ? [" ", h("kbd", {}, key)] : null], () => runVerb(e, v.verb));
  });
  const rest = moreVerbs(e);
  if (!rest.length) return buttons;
  const key = keyCap("palette");
  return [...buttons, h("button", {
    type: "button", class: "act more", "data-focus": `detail:more:${e.id}`, "aria-keyshortcuts": servedKey("palette").replace("Cmd+", "Meta+") || undefined,
    onclick: () => detailEnv.palette({ title: t("Actions"), items: moreVerbs(e) }),
  }, h("span", { class: "lb" }, t("More {n}", { n: fmtNumber(rest.length) })), key ? [" ", h("kbd", {}, key)] : null)];
}

// moreVerbs are the palette items of the verbs e offers past the profile's detail verbs, each running as its
// button would; a verb offered but off says why.
function moreVerbs(e) {
  const named = new Set(detailVerbs());
  return offeredVerbs(e).filter((v) => !named.has(v.verb)).map((v) => ({
    label: verbLabel(v.verb),
    key: verbTask(v.verb).key || "",
    meta: v.enabled ? undefined : v.reason,
    run: () => (v.enabled ? runVerb(e, v.verb) : detailEnv.notice(`${verbLabel(v.verb)}: ${v.reason || t("not offered here")}`, true)),
  }));
}

// pickTask asks for the other task verb takes on e, from the tasks its pick offers, and runs verb with it.
function pickTask(e, verb) {
  const spec = verbTask(verb);
  return taskPicker({
    task: e,
    ask: { label: spec.input || spec.label || verb, pick: spec.pick },
    match: async (q) => {
      try {
        return (await detailEnv.getJSON(matchPath(q, e.id))).tasks || [];
      } catch (err) {
        detailEnv.notice(t("Could not list the tasks: {error}", { error: err.message }), true);
        return [];
      }
    },
    palette: detailEnv.palette,
    stateLabel,
    run: (id) => taskAct(e, verb, { [PICK_ARG]: id }),
  });
}

// pickVerb is the verb e offers whose pick is pick, "" when it offers none.
function pickVerb(e, pick) {
  const v = (e.verbs || []).find((x) => verbTask(x.verb).pick === pick);
  return v ? v.verb : "";
}

// refRow is another task a task's page names: its link and state, the
// page's own copy of it first, which a later page keeps fresh, and the
// button of verb on it.
function refRow(e, r, verb, scope) {
  const own = !r.merged ? taskById(r.id) : null;
  const title = (own && own.title) || r.title || r.id;
  const state = own ? own.state : r.state;
  return [
    h("a", { href: "#" + viewHash({ task: r.id, pr: "" }), "data-focus": `${scope}:${r.id}` }, title),
    state ? [" ", h("span", { class: "pill" }, stateLabel(state))] : null,
    verb ? [" ", verbButton(e, verb, verbLabel(verb), () => taskAct(e, verb, { [PICK_ARG]: r.id }), { scope: `${scope}:${r.id}`, name: `${verbLabel(verb)} ${title}` })] : null,
  ];
}

// taskRelated lists the tasks related to e, each with the verb that takes the link off.
function taskRelated(e) {
  const refs = e.related || [];
  if (!refs.length) return null;
  const verb = pickVerb(e, "related");
  return h("section", { class: "d-related", "aria-label": t("Related") },
    h("h2", { class: "d-sub" }, t("Related")),
    h("ul", { class: "d-tasks" }, ...refs.map((r) => h("li", {}, refRow(e, r, verb, "related")))));
}

// taskMerged is e's merged-from trace: each task merged into it with the
// verb that gives it back, and under it, without one, the tasks merged into
// that one before, which come back with it.
function taskMerged(e) {
  const refs = e.merged_from || [];
  if (!refs.length) return null;
  const verb = pickVerb(e, "merged");
  const within = (r) => (r.within || []).length ? h("ul", { class: "d-tasks" }, ...r.within.map((w) => h("li", {}, refRow(e, w, "", "merged"), within(w)))) : null;
  return h("section", { class: "d-merged", "aria-label": t("Merged from") },
    h("h2", { class: "d-sub" }, t("Merged from")),
    h("ul", { class: "d-tasks" }, ...refs.map((r) => h("li", {}, refRow(e, r, verb, "merged"), within(r)))));
}

// composeFor is the composer's text for key, empty when it last held another key's.
function composeFor(key) {
  if (detailState.compose.key !== key) detailState.compose = { key, text: "", reaction: "" };
  return detailState.compose;
}

// replyArgs are the reply verb's args of the composer, null when it holds nothing to post.
function replyArgs(c, send) {
  const text = c.text.trim();
  const reaction = c.reaction.trim().replace(/^:|:$/g, "");
  if (!text && !reaction) return null;
  const args = text ? { kind: "reply", text } : { kind: "react", reaction };
  args.send = send ? "now" : "draft";
  return args;
}

// replySendsNow says whether the reply verb's served send posts a reply at once.
function replySendsNow() {
  return verbTask(handlerVerb("reply")).send === "now";
}

// composer writes a reply in the unit's conversation, or a reaction where the unit takes one: Save to
// Outbox holds it for approval, Send now approves it at once. post runs the args; a result clears it.
function composer(key, opts) {
  const c = composeFor(key);
  const run = async (send) => {
    const args = replyArgs(c, send);
    if (!args) {
      detailEnv.notice(t("Write a reply or name a reaction first"), true);
      return;
    }
    if (await opts.post(args)) {
      detailState.compose = { key, text: "", reaction: "" };
      detailEnv.render();
    }
  };
  const off = opts.reason || "";
  const button = (label, send, primary) => h("button", { type: "button", class: "act" + (off ? " off" : "") + (primary ? " primary" : ""), disabled: !!off, title: off || undefined,
    "data-focus": `${opts.scope}:${send ? "send" : "save"}`, onclick: () => run(send) }, label);
  return h("section", { class: "d-compose", "aria-label": t("Reply") },
    h("h2", { class: "d-sub" }, t("Reply")),
    off ? h("p", { class: "empty" }, off) : null,
    h("textarea", { rows: 3, "aria-label": t("Reply"), placeholder: opts.placeholder || t("Your reply"), disabled: !!off, "data-focus": `${opts.scope}:text`,
      oninput: (ev) => { c.text = ev.target.value; } }, c.text),
    opts.reacts ? h("input", { type: "text", class: "d-reaction", "aria-label": t("Reaction"), placeholder: t("or a reaction, such as eyes"), disabled: !!off, value: c.reaction,
      "data-focus": `${opts.scope}:reaction`, oninput: (ev) => { c.reaction = ev.target.value; } }) : null,
    h("div", { class: "d-actions" }, ...(replySendsNow() ? [button(t("Send now"), true, true), button(t("Save to Outbox"), false, false)]
      : [button(t("Save to Outbox"), false, true), button(t("Send now"), true, false)])));
}

// unitComposer replies on a unit through POST /api/drafts.
function unitComposer(u) {
  if (!u || !u.provider) return null;
  return composer("unit:" + u.provider + ":" + u.key, { scope: "unit:reply", reacts: providerReacts(u.provider), post: async (args) => {
    try {
      const d = await detailEnv.postJSON("/api/drafts", { unit: u, kind: args.kind, text: args.text, reaction: args.reaction, send: args.send === "now", source: "cockpit" });
      detailEnv.notice(d.state === "queued" ? t("Draft {id} approved; it goes out in a moment", { id: d.id }) : t("Draft {id} saved to the Outbox for your approval", { id: d.id }));
      detailEnv.changed();
      return d;
    } catch (err) {
      detailEnv.notice(t("Reply on {unit} failed: {error}", { unit: unitLabel(u), error: err.message }), true);
      return null;
    }
  } });
}

function personName(p) {
  return (p && (p.name || p.key)) || "";
}

// unitHomeView is the view a unit opens in: its provider's "all" when it
// serves one, else the provider's first rows view; "all" when none is known.
function unitHomeView(provider, views) {
  const own = (views || []).filter((v) => v.provider === provider && v.rows);
  return (own.find((v) => v.id === "all") || own[0] || { id: "all" }).id;
}

// unitRef links a unit to its row in the cockpit, then to its service.
function unitRef(u, label, link) {
  const views = typeof catalog !== "undefined" ? catalog.views : [];
  const service = typeof providerName === "function" ? providerName(u.provider) : u.provider;
  return [
    h("a", { href: "#" + routeHash(unitHomeView(u.provider, views), u.provider + ":" + u.key), class: "ref" }, label),
    link ? [" ", h("a", { href: safeURL(link), target: "_blank", rel: "noopener noreferrer", class: "ext" }, service)] : null,
  ];
}

// signalWords are a card signal in its plugin's words (the catalog's card_signals): its name, and its value
// filled with its params followed by each note in its tone; value is how an instant value shows. A code with no
// words shows as itself.
function signalWords(provider, g, value) {
  const spec = (catalog.cardSignals || []).find((s) => s.provider === provider && s.key === g.key) || {};
  const word = (spec.values || {})[g.value];
  const notes = (g.notes || []).map((n) => {
    const w = (spec.notes || {})[n.code];
    return [" · ", h("span", { class: "tone-" + (n.tone || "neutral") }, w ? fillWord(w, n.params || {}) : n.code)];
  });
  return { label: spec.label || g.key, value: [g.at ? value(g.at) : word ? fillWord(word, g.params || {}) : g.value, ...notes] };
}

// unitCard is a unit's stored card (#unit-card); opts.signals false leaves its signals to a grid that shows them.
function unitCard(s, opts = {}) {
  const c = s.card || {};
  const signals = opts.signals === false ? [] : c.signals || [];
  const lh = c.last_human;
  return h("section", { class: "d-card" + (s.stale ? " stale" : ""), "aria-label": t("Card") },
    h("div", { class: "meta" },
      unitRef(c.unit || {}, c.label || unitLabel(c.unit), c.link),
      c.state ? [" ", h("span", { class: "pill" }, c.state)] : null,
      c.outcome ? [" ", h("span", { class: "pill" }, c.outcome)] : null,
      s.stale ? [" ", h("span", { class: "flag" }, t("plugin disabled"))] : null),
    c.title ? h("div", { class: "title" }, c.title) : null,
    c.why ? h("div", { class: "why" }, c.why) : null,
    (c.waits_on || []).length ? h("div", {}, t("Waiting on"), " ", h("strong", {}, c.waits_on.map(personName).join(", "))) : null,
    signals.length ? h("ul", { class: "signals" }, ...signals.map((g) => {
      const w = signalWords((c.unit || {}).provider, g, stamp);
      return h("li", { class: "tone-" + (g.tone || "neutral") }, h("span", { class: "k" }, w.label), " ", ...w.value);
    })) : null,
    lh ? h("div", { class: "meta" }, `${personName(lh.who)}: ${lh.what}`, lh.at ? ` (${stamp(lh.at)})` : "") : null);
}

function itemsBlock(items) {
  if (!items || !items.length) return null;
  return [h("h2", { class: "d-sub" }, t("Items")),
    h("ul", { class: "d-items" }, ...items.map((it) => h("li", { class: "item-" + (it.state || "open") },
      h("span", {}, it.summary || it.id), " ", h("span", { class: "pill" }, it.state || ""))))];
}

// unitExtras are a unit detail's card and open tasks.
function unitExtras(d) {
  const key = detailState.unitKey;
  const tasks = key ? detailState.unitTasks : [];
  return h("div", { class: "d-unit" },
    d && d.card ? unitCard(d.card) : null,
    d && (d.items || []).length ? itemsBlock(d.items) : null,
    d && d.row ? unitComposer(d.row.unit) : null,
    tasks.length ? [h("h2", { class: "d-sub" }, t("Tasks")),
      h("ul", { class: "d-tasks" }, ...tasks.map((e) => h("li", {},
        h("a", { href: "#" + viewHash({ task: e.id, pr: "" }), "data-focus": "unit-task:" + e.id }, e.title || e.id), " ", h("span", { class: "pill" }, e.state))))] : null);
}

// renderTaskDetail draws the task route into box; false when the route
// names no task.
function renderTaskDetail(box, route) {
  if (!route.task) return false;
  const e = detailState.task && detailState.task.id === route.task ? detailState.task : null;
  const parts = e ? taskPage(e) : [
    h("div", { class: "d-head" },
      h("button", { type: "button", class: "close", "aria-label": t("Close detail"), "data-focus": "detail:close", onclick: () => detailEnv.close() }, "×"),
      h("p", { class: detailState.error ? "plan-error" : "empty" }, detailState.error ? t("Could not load the task: {error}", { error: detailState.error }) : t("Loading the task."))),
  ];
  const was = document.activeElement && box.contains(document.activeElement) ? document.activeElement : null;
  const caret = was && typeof was.selectionStart === "number" ? [was.selectionStart, was.selectionEnd] : null;
  box.replaceChildren(...parts.flat().filter(Boolean));
  box.dataset.key = "task:" + route.task;
  box.hidden = false;
  document.body.classList.add("has-detail");
  const el = was && was.dataset.focus && box.querySelector(`[data-focus="${CSS.escape(was.dataset.focus)}"]`);
  if (el) {
    el.focus({ preventScroll: true });
    if (caret && el.setSelectionRange) el.setSelectionRange(...caret);
  }
  return true;
}
