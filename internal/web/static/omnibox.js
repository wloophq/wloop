"use strict";

const PALETTE_CSS = `
.pal-back { position: fixed; inset: 0; background: rgb(0 0 0 / 0.25); display: flex; justify-content: center; align-items: flex-start; padding-top: 12vh; z-index: 50; transition: opacity var(--motion-fast) var(--motion-ease); }
@starting-style { .pal-back { opacity: 0; } }
.pal { background: var(--panel); color: var(--ink); border: 1px solid var(--rule); border-radius: 8px; width: min(36rem, calc(100vw - 2rem)); max-height: 70vh; display: flex; flex-direction: column; box-shadow: 0 8px 32px rgb(0 0 0 / 0.25); font: 14px var(--sans); }
.pal-head { padding: 0.5rem 0.75rem; color: var(--muted); border-bottom: 1px solid var(--rule); }
.pal input { margin: 0.5rem 0.75rem; padding: 0.4rem 0.5rem; font: inherit; color: inherit; background: var(--bg); border: 1px solid var(--rule); border-radius: 4px; }
.pal ol, .pal dl { list-style: none; margin: 0; padding: 0 0 0.5rem; overflow: auto; }
.pal li { display: flex; justify-content: space-between; gap: 1rem; padding: 0.3rem 0.75rem; cursor: pointer; }
.pal li[aria-selected="true"] { background: var(--sel); }
.pal kbd { font: 12px var(--mono); color: var(--muted); }
.pal li .pill { margin-left: auto; }
.pal-empty { margin: 0; padding: 0 0.75rem 0.5rem; color: var(--muted); }
.pal-empty:empty { display: none; }
.pal dt { padding: 0.5rem 0.75rem 0.2rem; color: var(--muted); font-weight: 600; }
.pal dd { margin: 0; display: flex; justify-content: space-between; gap: 1rem; padding: 0.15rem 0.75rem; }
`;

// paletteMatch keeps the items whose label or key holds every word of q.
function paletteMatch(items, q) {
  const words = String(q || "").toLowerCase().split(/\s+/).filter(Boolean);
  return items.filter((it) => {
    const text = `${it.label} ${it.key || ""}`.toLowerCase();
    return words.every((w) => text.includes(w));
  });
}

function palEl(tag, attrs, ...children) {
  const node = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v === undefined || v === null || v === false) continue;
    if (k === "class") node.className = v;
    else if (k.startsWith("on")) node.addEventListener(k.slice(2), v);
    else node.setAttribute(k, v === true ? "" : String(v));
  }
  for (const c of children.flat(Infinity)) if (c !== null && c !== undefined && c !== false) node.append(c instanceof Node ? c : document.createTextNode(String(c)));
  return node;
}

// palOverlay shows box over the page; the returned close removes it, gives
// the focus back and calls closed, as a click on the backdrop does.
function palOverlay(box, closed) {
  if (!document.getElementById("pal-css")) document.head.append(palEl("style", { id: "pal-css" }, PALETTE_CSS));
  for (const old of document.querySelectorAll(".pal-back")) old.remove();
  const back = document.activeElement;
  const shade = palEl("div", { class: "pal-back" }, box);
  const close = () => {
    shade.remove();
    if (back && back.isConnected && back.focus) back.focus();
    if (closed) closed();
  };
  shade.addEventListener("mousedown", (e) => { if (e.target === shade) close(); });
  document.body.append(shade);
  return close;
}

// showPalette lists items with their keys and meta; typing narrows them, or
// asks source for the items of the text typed, the latest text's answer
// winning; Enter runs the highlighted item of the text typed, once its
// answer is in; Esc closes. empty is what an empty list says.
function showPalette({ title, items, source, empty }) {
  const input = palEl("input", { type: "text", role: "combobox", "aria-label": title, "aria-controls": "pal-list", "aria-expanded": "true", "aria-autocomplete": "list",
    placeholder: t("{title}: type to narrow", { title }), autocomplete: "off", spellcheck: "false" });
  const list = palEl("ol", { id: "pal-list", role: "listbox", "aria-label": title });
  const none = palEl("p", { class: "pal-empty", "aria-live": "polite" });
  const box = palEl("div", { class: "pal", role: "dialog", "aria-label": title }, palEl("div", { class: "pal-head" }, title), input, list, none);
  let done = false;
  const close = palOverlay(box, () => { done = true; });
  let shown = items;
  let at = 0;
  const pick = (it) => {
    if (done) return;
    close();
    if (it) it.run();
  };
  const draw = () => {
    list.replaceChildren(...shown.map((it, i) => palEl("li", { id: `pal-opt-${i}`, role: "option", "aria-selected": String(i === at), onmousedown: (e) => { e.preventDefault(); pick(it); } },
      palEl("span", {}, it.label), it.meta ? palEl("span", { class: "pill" }, it.meta) : null, it.key ? palEl("kbd", {}, it.key) : null)));
    if (shown.length) input.setAttribute("aria-activedescendant", `pal-opt-${at}`);
    else input.removeAttribute("aria-activedescendant");
    none.textContent = shown.length ? "" : empty || t("Nothing matches");
  };
  let asked = 0;
  let pending = null;
  input.addEventListener("input", () => {
    const n = ++asked;
    if (!source) {
      shown = paletteMatch(items, input.value);
      at = 0;
      draw();
      return;
    }
    pending = Promise.resolve(source(input.value)).then((next) => {
      if (n !== asked) return;
      pending = null;
      shown = next || [];
      at = 0;
      draw();
    });
  });
  const enter = async () => {
    while (pending) await pending;
    pick(shown[at]);
  };
  input.addEventListener("keydown", (e) => {
    if (e.key === "Escape") close();
    else if (e.key === "Enter") {
      e.preventDefault();
      enter();
      return;
    } else if (e.key === "ArrowDown") at = Math.max(0, Math.min(shown.length - 1, at + 1));
    else if (e.key === "ArrowUp") at = Math.max(0, at - 1);
    else return;
    e.preventDefault();
    draw();
  });
  draw();
  input.focus();
}

// matchPath asks the daemon for the open tasks but except whose title holds q.
function matchPath(q, except) {
  return "/api/tasks?match=" + encodeURIComponent(q) + "&except=" + encodeURIComponent(except || "");
}

// PICK_ARG is the argument a verb that takes another task sends the pick in, task.ArgTask.
const PICK_ARG = "task";

// pickRefs are the tasks a verb's pick offers on task for the text q: the
// open tasks but task whose title holds q, which match reads from the
// daemon, else the tasks merged into task or related to it whose title
// holds q.
async function pickRefs(pick, task, q, match) {
  if (pick === "open") return (await match(q, task.id)) || [];
  const refs = pick === "merged" ? task.merged_from : pick === "related" ? task.related : [];
  const want = String(q || "").trim().toLowerCase();
  return (refs || []).filter((r) => String(r.title || r.id).toLowerCase().includes(want));
}

// taskPicker opens the palette, titled with the verb's question, over the
// tasks ask.pick offers on task; run takes the id picked.
async function taskPicker({ task, ask, match, palette, stateLabel, run }) {
  const items = (refs) => refs.map((r) => ({ label: r.title || r.id, meta: stateLabel ? stateLabel(r.state) : r.state || "", run: () => run(r.id) }));
  const source = async (q) => items(await pickRefs(ask.pick, task, q, match));
  palette({ title: ask.label, items: await source(""), source, empty: t("No task holds the text") });
}

// showHelp lists every binding by context.
function showHelp(groups) {
  const dl = palEl("dl", {}, groups.map((g) => [palEl("dt", {}, g.head), g.bindings.map((b) => palEl("dd", {}, palEl("span", {}, b.help || b.action), palEl("kbd", {}, b.key)))]));
  const box = palEl("div", { class: "pal", role: "dialog", "aria-label": t("Keys"), tabindex: "-1" }, palEl("div", { class: "pal-head" }, t("Keys (Esc closes)")), dl);
  const close = palOverlay(box);
  box.addEventListener("keydown", (e) => {
    if (e.key === "Escape" || e.key === "?") {
      e.preventDefault();
      close();
    }
  });
  box.focus();
}

// askInput is the small input a key opens: the typed text on Enter, null on Esc.
function askInput({ label, placeholder }) {
  return new Promise((resolve) => {
    const input = palEl("input", { type: "text", "aria-label": label, placeholder: placeholder || "", autocomplete: "off" });
    const box = palEl("div", { class: "pal", role: "dialog", "aria-label": label }, palEl("div", { class: "pal-head" }, label), input);
    let done = false;
    const close = palOverlay(box);
    const finish = (v) => {
      if (done) return;
      done = true;
      close();
      resolve(v);
    };
    input.addEventListener("keydown", (e) => {
      if (e.key === "Escape") finish(null);
      else if (e.key === "Enter" && !e.isComposing) finish(input.value);
      else return;
      e.preventDefault();
    });
    input.addEventListener("blur", () => finish(null));
    input.focus();
  });
}

// The omnibox: instant search while typing, a pasted address's unit, a known verb's operations to confirm, and
// the read-only agent's answer or proposal. The daemon resolves, answers and runs; this asks it and draws what
// it says. Nothing runs before the person confirms it.

// OMNI_DEBOUNCE_MS is the pause in typing after which the omnibox asks for search and resolve.
const OMNI_DEBOUNCE_MS = 120;

// omniboxNew is an empty omnibox; items are the selection's actions it lists while nothing is typed.
function omniboxNew(panel, items) {
  return { panel: !!panel, items: items || [], text: "", seq: 0, askSeq: 0, abort: null, timer: null, search: null, resolved: null,
    answer: null, askError: "", asking: false, follow: [], reply: "", ops: [], checked: [], ran: null, runError: "", sel: "",
    answered: "", pending: Promise.resolve() };
}

// omniboxOps makes ops the operations to confirm, every one checked.
function omniboxOps(st, ops) {
  st.ops = (ops || []).map((op) => ({ ...op }));
  st.checked = st.ops.map(() => true);
}

// omniboxType takes text typed: after a pause it asks for the search and the resolution together; a reply to
// text older than the newest is dropped, and the request the newer text replaces is aborted.
function omniboxType(st, text, env) {
  st.text = text;
  st.sel = "";
  st.answer = null;
  st.askError = "";
  st.ran = null;
  st.askSeq++;
  st.asking = false;
  env.clear(st.timer);
  if (st.abort) st.abort.abort();
  const seq = ++st.seq;
  if (!text.trim()) {
    st.search = null;
    st.resolved = null;
    st.answered = text;
    omniboxOps(st, []);
    env.render();
    st.pending = Promise.resolve();
    return st.pending;
  }
  st.pending = new Promise((done) => {
    st.timer = env.later(async () => {
      const ctl = new AbortController();
      st.abort = ctl;
      const started = env.now();
      const [search, resolved] = await Promise.all([
        env.get("/api/search?q=" + encodeURIComponent(text), ctl.signal).catch(() => null),
        env.post("/api/omnibox/resolve", { text }, ctl.signal).catch(() => null)]);
      if (seq === st.seq) {
        st.search = search;
        st.resolved = resolved;
        st.answered = text;
        st.took = env.now() - started;
        omniboxOps(st, (resolved && resolved.preview && resolved.preview.operations) || []);
        env.render();
      }
      done();
    }, OMNI_DEBOUNCE_MS);
  });
  return st.pending;
}

// omniboxAsk asks the read-only agent about the text, with the questions it asked and their answers; a reply
// to an older ask is dropped. An error shows where the answer would; search stays as it was.
async function omniboxAsk(st, env) {
  if (!st.text.trim() || st.asking) return;
  const seq = ++st.askSeq;
  st.asking = true;
  st.askError = "";
  env.render();
  try {
    const out = await env.post("/api/omnibox/ask", { text: st.text, follow: st.follow });
    if (seq !== st.askSeq) return;
    st.answer = out || {};
    if (st.answer.proposal) omniboxOps(st, st.answer.proposal.operations);
  } catch (err) {
    if (seq === st.askSeq) st.askError = err.message;
  } finally {
    if (seq === st.askSeq) {
      st.asking = false;
      env.render();
    }
  }
}

// omniboxFollow answers the question the agent asked and asks again with the whole exchange.
function omniboxFollow(st, env) {
  const q = st.answer && st.answer.proposal ? st.answer.proposal.question : "";
  if (!q || !st.reply.trim()) return Promise.resolve();
  st.follow = [...st.follow, { question: q, answer: st.reply.trim() }];
  st.reply = "";
  return omniboxAsk(st, env);
}

// omniboxRun runs the checked operations through the daemon, which replays each as the click it stands for.
async function omniboxRun(st, env, ops) {
  const run = ops || st.ops.filter((_, i) => st.checked[i]);
  if (!run.length) return;
  st.runError = "";
  try {
    const out = await env.post("/api/omnibox/run", { operations: run });
    st.ran = { ops: run, results: (out && out.results) || [] };
    if (env.changed) env.changed();
  } catch (err) {
    st.runError = err.message;
  }
  env.render();
}

// omniboxUnit is the unit a pasted address resolved to, or null.
function omniboxUnit(st) {
  return st.resolved && st.resolved.kind === "unit" ? st.resolved.unit : null;
}

// omniboxUnitHref is the route of a resolved unit's detail.
function omniboxUnitHref(unit) {
  return "#" + routeHash("", unit.unit.provider + ":" + unit.unit.key);
}

// omniboxUnitAction runs one of a resolved unit's catalog actions as its detail would, then refreshes. The
// floating panel holds no detail: it runs an action that copies itself and hands any other to the main window.
async function omniboxUnitAction(st, env, id) {
  const unit = omniboxUnit(st);
  const spec = (unit.actions || []).find((a) => a.id === id) || { id };
  if (st.panel) return spec.copies && !spec.input ? omniboxPanelCopy(st, env, spec) : env.open(omniboxUnitHref(unit));
  await env.act(unit.unit, id, !!spec.copies);
  if (env.changed) env.changed();
  env.render();
}

// omniboxCopied is what the panel's last copy of the resolved unit left to show: why it failed, and the text
// to copy by hand when the clipboard refused it.
function omniboxCopied(st) {
  const c = st.copied;
  if (!c || c.resolved !== st.resolved) return [];
  return [h("p", { class: "err omni-copy-error" }, c.error),
    c.text ? h("textarea", { class: "omni-copy", readonly: true, rows: 6, "aria-label": c.error }, c.text) : null];
}

// omniboxPanelCopy runs, from the floating panel, an action that copies: it claims the clipboard while the
// click lasts, asks the confirm of the row the unit serves, posts the action on the basis that row rests on,
// puts its copy on the clipboard and hides the panel. A failed action says why, and a refused write shows the
// text to copy by hand.
async function omniboxPanelCopy(st, env, spec) {
  if (st.copying) return;
  st.copying = true;
  st.copied = null;
  const claim = env.claim();
  const resolved = st.resolved;
  const u = resolved.unit.unit;
  const action = spec.label || spec.id;
  const show = (error, text) => { st.copied = { resolved, error, text }; env.render(); };
  try {
    let out;
    try {
      const d = await env.get(unitPath(u));
      const offered = ((d.row && d.row.actions) || []).find((a) => a.id === spec.id) || {};
      if (offered.confirm && !env.confirm(offered.confirm)) return claim.pending.reject(new Error("cancelled"));
      out = await env.post(unitPath(u, spec.id), offered.basis ? { source: "cockpit", basis: offered.basis } : { source: "cockpit" });
      if (out.error) throw new Error(out.error);
    } catch (err) {
      claim.pending.reject(err);
      return show(t("{action} failed: {error}", { action, error: err.message }), "");
    }
    try {
      if (!(await env.settle(spec.id, out, claim))) return show(t("{action} answered nothing to copy", { action }), "");
    } catch (err) {
      return show(t("Could not copy: {error}", { error: err.message }), out.copy.text);
    }
    env.render();
    env.close();
  } finally {
    st.copying = false;
  }
}

// omniboxAct runs action id on u as the unit's detail does: through act, on the row the detail serves, which
// asks for the action's confirm and input. An action that copies claims the clipboard before the first await.
async function omniboxAct(u, id, copies = (state.specs.byId[id] || {}).copies) {
  const claim = copies ? claimCopy() : null;
  try {
    const d = await getJSON(unitPath(u));
    await refreshSpecs([d.row]);
    const input = (state.specs.byId[id] || {}).input;
    const extra = claim ? { claim } : {};
    if (input && (input.kind === "text" || input.kind === "answer")) {
      const v = await askInput({ label: input.label || id, placeholder: input.placeholder });
      if (v === null || v === undefined) return;
      extra.guidance = String(v).trim();
    }
    return await act(d.row, id, extra);
  } finally {
    if (claim) claim.pending.reject(new Error(t("{action} answered nothing to copy", { action: id })));
  }
}

// omniboxChoices are what Enter may run, in the order the arrows walk them: the operations to confirm, a
// pasted address's unit, the agent where only it would help, each search result, and the selection's actions
// while nothing is typed.
function omniboxChoices(st, env) {
  if (!st.text.trim()) return st.items.map((it, i) => ({ id: "item:" + i, run: () => { env.close(); it.run(); } }));
  const out = [];
  if (st.ops.some((_, i) => st.checked[i])) out.push({ id: "ops", run: () => omniboxRun(st, env) });
  const unit = omniboxUnit(st);
  if (unit) {
    const first = (unit.tasks || [])[0];
    out.push({ id: "unit", run: () => env.open(first ? "#task=" + encodeURIComponent(first.id) : omniboxUnitHref(unit)) });
  }
  if (st.resolved && st.resolved.kind === "agent" && !st.answer) out.push({ id: "ask", run: () => omniboxAsk(st, env) });
  for (const g of (st.search && st.search.groups) || []) {
    for (const r of g.results || []) out.push({ id: "task:" + r.task.id, run: () => env.open("#task=" + encodeURIComponent(r.task.id)) });
  }
  return out;
}

// omniboxSelected is the id of the choice selected: the one picked while it is still offered, else the first.
function omniboxSelected(st, choices) {
  return (choices.find((c) => c.id === st.sel) || choices[0] || { id: "" }).id;
}

// omniboxMove moves the selection by delta choices, stopping at either end.
function omniboxMove(st, delta, env) {
  const choices = omniboxChoices(st, env);
  if (!choices.length) return;
  const at = choices.findIndex((c) => c.id === omniboxSelected(st, choices));
  st.sel = choices[Math.min(Math.max(at + delta, 0), choices.length - 1)].id;
  env.render();
}

// omniboxEnter is Enter in the field: run the selected choice once the answer to the text typed is in; with
// Cmd it asks the agent.
async function omniboxEnter(st, env, ask) {
  if (ask) return omniboxAsk(st, env);
  const text = st.text;
  if (st.answered !== text) await st.pending;
  if (st.answered !== text || st.text !== text) return;
  const choices = omniboxChoices(st, env);
  const pick = choices.find((c) => c.id === omniboxSelected(st, choices));
  return pick ? pick.run() : undefined;
}

// omniboxKey is a key in the omnibox's field: the arrows (and Ctrl+N, Ctrl+P) move the selection, Enter runs
// it, Cmd+Enter asks the agent, Escape closes; true when the omnibox took the key.
function omniboxKey(st, env, e, close) {
  const ctrl = e.ctrlKey && !e.metaKey && !e.altKey;
  if (e.key === "Escape") close();
  else if (e.key === "Enter" && !e.isComposing) omniboxEnter(st, env, e.metaKey || e.ctrlKey);
  else if (e.key === "ArrowDown" || (ctrl && e.key === "n")) omniboxMove(st, 1, env);
  else if (e.key === "ArrowUp" || (ctrl && e.key === "p")) omniboxMove(st, -1, env);
  else return false;
  e.preventDefault();
  return true;
}

// OMNI_STOPS are the controls Tab moves through inside the omnibox.
const OMNI_STOPS = ["input", "button", "a[href]", '[tabindex]:not([tabindex="-1"])'].join(",");

// omniboxTrap is where Tab from active goes so the focus stays among stops: the other end past either end,
// else null, leaving the move to the page.
function omniboxTrap(e, stops, active) {
  if (e.key !== "Tab" || !stops.length) return null;
  const first = stops[0];
  const last = stops[stops.length - 1];
  if (e.shiftKey && active === first) return last;
  if (!e.shiftKey && active === last) return first;
  return null;
}

// omniboxKeysHint names the omnibox's keys.
function omniboxKeysHint() {
  const key = (k, what) => [h("kbd", {}, k), " ", what];
  return h("p", { class: "omni-keys" }, key("↑↓", t("to navigate")), " · ", key("↵", t("to open")), " · ", key("⌘↵", t("to ask the agent")), " · ",
    key("esc", t("to close")));
}

// omniboxOpLabel is an operation in words.
function omniboxOpLabel(op) {
  const unit = op.unit ? op.unit.key : "";
  switch (op.op) {
    case "action": return t("{action} on {unit}", { action: op.reason || op.action, unit });
    case "verb": return t("{verb} task {task}", { verb: op.verb, task: op.task });
    case "capture": return t("Capture: {title}", { title: op.title });
    case "start": return op.repo ? t("Start in {repo}: {brief}", { repo: op.repo, brief: op.brief }) : t("Start: {brief}", { brief: op.brief });
  }
  return op.op;
}

// omniboxTaskLink is a task as a link to it, which the floating panel hands to the main window.
function omniboxTaskLink(e, extra) {
  return h("a", { class: "omni-task", href: "#task=" + encodeURIComponent(e.id) }, e.title || e.id, extra || null);
}

// omniboxEdits are the fields of a proposed operation a person may change before running it.
function omniboxEdits(st, i, env) {
  const op = st.ops[i];
  const field = (name, label) => h("input", { type: "text", class: "omni-edit", "aria-label": label, value: op[name] || "", "data-focus": `omni:edit:${i}:${name}`,
    oninput: (ev) => { op[name] = ev.target.value; } });
  if (op.op === "start") return [field("brief", t("Brief")), field("repo", t("Repository"))];
  if (op.op === "capture") return [field("title", t("Title"))];
  return [];
}

// omniboxChecklist is the operations to confirm, each with a box to leave it out and its reason, and the
// button that runs the checked ones.
function omniboxChecklist(st, env, editable, opt) {
  if (!st.ops.length) return null;
  const n = st.checked.filter(Boolean).length;
  return h("section", { class: "omni-ops", "aria-label": t("To confirm") },
    h("ol", {}, ...st.ops.map((op, i) => h("li", { class: "omni-op" },
      h("label", {},
        h("input", { type: "checkbox", checked: st.checked[i] || undefined, "data-focus": `omni:op:${i}`,
          onchange: (ev) => { st.checked[i] = !!ev.target.checked; env.render(); } }),
        " ", omniboxOpLabel(op)),
      op.reason && op.op !== "action" ? h("p", { class: "why" }, op.reason) : null,
      ...(editable ? omniboxEdits(st, i, env) : [])))),
    h("button", { type: "button", class: "act primary", disabled: n === 0 || undefined, "data-focus": "omni:run", ...(opt ? opt("ops") : {}), onclick: () => omniboxRun(st, env) },
      h("kbd", {}, "↵"), " ", tn("Run {n}", n)));
}

// omniboxAnswer is what the agent answered: a list of tasks to open, or to walk as a queue, or a proposal
// with its question.
function omniboxAnswer(st, env) {
  const a = st.answer;
  if (!a) return [];
  const out = [];
  if (a.answer) {
    const ids = (a.answer.tasks || []).map((e) => e.id);
    out.push(h("section", { class: "omni-answer" },
      renderMarkdown(a.answer.text || "", { fold: 12, key: "omni:answer" }),
      ids.length ? h("ol", {}, ...a.answer.tasks.map((e) => h("li", {}, omniboxTaskLink(e)))) : null,
      ids.length ? h("a", { class: "omni-queue", href: "#queue=list&ids=" + ids.map(encodeURIComponent).join(",") }, t("Open as a queue")) : null));
  }
  if (a.proposal) {
    out.push(h("div", { class: "omni-summary" }, renderMarkdown(a.proposal.summary || "", { fold: 12, key: "omni:summary" })));
    if (a.proposal.question) {
      out.push(h("div", { class: "omni-question" },
        renderMarkdown(a.proposal.question, { fold: 0 }),
        h("input", { type: "text", "aria-label": t("Your answer"), value: st.reply, "data-focus": "omni:reply",
          oninput: (ev) => { st.reply = ev.target.value; },
          onkeydown: (ev) => {
            if (ev.key !== "Enter") return;
            if (ev.preventDefault) ev.preventDefault();
            if (ev.stopPropagation) ev.stopPropagation();
            omniboxFollow(st, env);
          } })));
    }
  }
  for (const d of a.dropped || []) out.push(h("p", { class: "omni-dropped" }, t(d.why, d.params || {})));
  return out;
}

// omniboxSearch is the search's groups, each with its count and its results, each result linking its task
// and naming the views it is in.
function omniboxSearch(st, opt, env) {
  const groups = (st.search && st.search.groups) || [];
  return groups.map((g) => h("section", { class: "omni-group", "data-group": g.id, role: "group", "aria-label": g.label },
    h("h3", { "aria-hidden": "true" }, g.label, " ", h("span", { class: "count" }, fmtNumber(g.count))),
    h("ul", { role: "none" }, ...(g.results || []).map((r) => {
      const href = "#task=" + encodeURIComponent(r.task.id);
      return h("li", { ...opt("task:" + r.task.id, true), class: "omni-task", "data-href": href, onclick: () => env.open(href) },
        r.task.title || r.task.id, r.views && r.views.length ? h("span", { class: "views" }, r.views.join(" · ")) : null);
    }))));
}

// omniboxCount is how many results the search found, said politely as they arrive, or the selected choice
// when it is no option of the list.
function omniboxCount(st, env) {
  const choices = omniboxChoices(st, env);
  const sel = omniboxSelected(st, choices);
  if (sel && !omniboxIsOption(sel)) return h("output", { class: "omni-status", "aria-live": "polite" }, omniboxSelectionWords(st, sel));
  const n = ((st.search && st.search.groups) || []).reduce((sum, g) => sum + (g.results || []).length, 0);
  return h("output", { class: "omni-status", "aria-live": "polite" }, st.search ? tn("{n} results", n) : "");
}

// omniboxRan is each operation run and what came of it.
function omniboxRan(st) {
  if (!st.ran && !st.runError) return null;
  if (st.runError) return h("p", { class: "err" }, st.runError);
  return h("ol", { class: "omni-ran" }, ...st.ran.ops.map((op, i) => {
    const r = st.ran.results[i] || {};
    return h("li", { class: r.ok ? "ok" : "err" }, r.ok ? "✓ " : "✗ ", omniboxOpLabel(op), r.ok ? null : h("span", { class: "why" }, " ", t(r.error || "run_failed", r.params || {})));
  }));
}

// omniboxOptionID is the element id of the choice at i.
const omniboxOptionID = (i) => "omni-opt-" + i;

// omniboxOption is, for a choice's id, the attributes that make its element an option, selected or not.
function omniboxOption(st, env) {
  const choices = omniboxChoices(st, env);
  const sel = omniboxSelected(st, choices);
  return (id, option) => {
    const i = choices.findIndex((c) => c.id === id);
    if (i < 0) return {};
    const at = { id: omniboxOptionID(i), "data-choice": id };
    return option ? { ...at, role: "option", "aria-selected": String(id === sel) } : { ...at, "data-selected": String(id === sel) };
  };
}

// omniboxActiveID is the element id of the selected choice, "" when there is none.
function omniboxActiveID(st, env) {
  const choices = omniboxChoices(st, env);
  const i = choices.findIndex((c) => c.id === omniboxSelected(st, choices));
  return i < 0 || !omniboxIsOption(choices[i].id) ? "" : omniboxOptionID(i);
}

// omniboxIsOption says whether choice id is an option of the results' listbox: a result or a selection's
// action; the operations to run, a pasted unit and the agent keep their own controls.
function omniboxIsOption(id) {
  return id.startsWith("task:") || id.startsWith("item:");
}

// omniboxSelectionWords are the words of a selected choice that is no option, which the status says.
function omniboxSelectionWords(st, id) {
  if (id === "ops") return tn("Run {n}", st.checked.filter(Boolean).length);
  if (id === "unit") return (omniboxUnit(st) || {}).label || "";
  if (id === "ask") return t("Press ⌘↵ to ask the agent");
  return "";
}

// renderOmnibox is the omnibox below its field: the path answering, then the selection's actions while
// nothing is typed, the unit, the operations to confirm, the agent's answer and the search.
function renderOmnibox(st, env) {
  const agent = st.asking || !!st.answer || !!st.askError;
  const opt = omniboxOption(st, env);
  const out = [h("p", { class: "omni-badge" + (agent ? " agent" : "") }, agent ? t("agent") : t("instant"))];
  if (!st.text.trim()) {
    out.push(h("ul", { class: "omni-items", role: "listbox", id: "omni-list", "aria-label": t("Search or ask") }, ...st.items.map((it, i) => h("li", {
      ...opt("item:" + i, true), class: "omni-item", onclick: () => { env.close(); it.run(); },
    }, it.label, it.meta ? h("span", { class: "pill" }, it.meta) : null, it.key ? h("kbd", {}, it.key) : null))));
    return out;
  }
  const r = st.resolved;
  if (r && r.why) out.push(h("p", { class: "omni-why" }, t(r.why, r.params || {})));
  if (r && r.kind === "unit" && r.unit) {
    out.push(h("section", { class: "omni-unit", ...opt("unit") },
      h("h3", {}, r.unit.permalink ? h("a", { href: r.unit.permalink, target: "_blank", rel: "noopener" }, r.unit.label) : r.unit.label),
      h("ol", {}, ...(r.unit.tasks || []).map((e) => h("li", {}, omniboxTaskLink(e)))),
      h("div", { class: "d-actions" }, ...(r.unit.actions || []).map((a) => h("button", { type: "button", class: "act",
        onclick: () => omniboxUnitAction(st, env, a.id) }, a.label || a.id))),
      ...omniboxCopied(st)));
  }
  if (r && r.kind === "agent" && !agent) out.push(h("p", { class: "omni-hint", ...opt("ask") }, t("Press ⌘↵ to ask the agent")));
  out.push(...omniboxAnswer(st, env));
  out.push(omniboxChecklist(st, env, !!(st.answer && st.answer.proposal), opt));
  out.push(omniboxRan(st));
  if (st.asking) out.push(h("p", { class: "loading" }, t("Asking the agent...")));
  if (st.askError) out.push(h("p", { class: "err omni-ask-error" }, st.askError));
  out.push(omniboxCount(st, env), h("div", { class: "omni-list", role: "listbox", id: "omni-list", "aria-label": t("Search or ask") }, ...omniboxSearch(st, opt, env)));
  return out.filter(Boolean);
}

// omniFetch is a JSON request to the daemon that signal may abort; a refusal throws the daemon's error.
async function omniFetch(url, body, signal) {
  const res = await fetch(url, {
    method: body === undefined ? "GET" : "POST", credentials: "same-origin", signal,
    headers: { "Content-Type": "application/json", Accept: "application/json", "X-Loop-API": API_VERSION },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const out = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(out.error || `${url}: HTTP ${res.status}`);
  return out;
}

// panelFollow keeps the floating panel current each time it shows again after it was left: an empty
// omnibox, then the daemon's strings and build as its health names them.
function panelFollow(win, doc, env) {
  let away = false;
  const leave = () => { away = true; };
  const back = async () => {
    if (!away || doc.visibilityState === "hidden") return;
    away = false;
    env.reset();
    try {
      const health = await env.health();
      env.strings(health.strings);
      env.build(health);
    } catch {
      // The panel's next show asks again.
    }
  };
  win.addEventListener("blur", leave);
  win.addEventListener("focus", back);
  doc.addEventListener("visibilitychange", () => (doc.visibilityState === "hidden" ? leave() : back()));
}

// openOmnibox opens the omnibox in this window over the selection's actions, as Cmd+K does.
function openOmnibox() {
  if (typeof cockpitKeys === "object" && cockpitKeys) cockpitKeys.openPalette();
  else showOmnibox();
}

// omniboxRedraw draws the omnibox's results, keeps the focus on the control of the same data-focus it was on,
// and points the field at the selected choice, scrolled into view.
function omniboxRedraw(st, env, input, results) {
  const at = document.activeElement;
  const slot = at && results.contains(at) ? at.getAttribute("data-focus") : "";
  results.replaceChildren(...renderOmnibox(st, env));
  const again = slot ? results.querySelector(`[data-focus="${CSS.escape(slot)}"]`) : null;
  if (again) again.focus();
  const id = omniboxActiveID(st, env);
  if (!id) {
    input.removeAttribute("aria-activedescendant");
    return;
  }
  input.setAttribute("aria-activedescendant", id);
  const sel = document.getElementById(id);
  if (sel && sel.scrollIntoView) sel.scrollIntoView({ block: "nearest" });
}

// showOmnibox opens the omnibox over the page, or, in the floating panel, as the whole page; items are the
// selection's actions it lists while nothing is typed. Esc closes it, and in the panel hides the panel.
function showOmnibox(opts = {}) {
  const st = omniboxNew(opts.panel, opts.items);
  const input = h("input", { type: "text", class: "omni-input", role: "combobox", "aria-label": t("Search or ask"), "aria-expanded": "true", autocomplete: "off",
    spellcheck: "false", placeholder: t("Search, paste a link, or ask"), "aria-controls": "omni-list", "aria-autocomplete": "list" });
  const results = h("div", { class: "omni-results" });
  const box = h("div", { class: "pal omni" + (st.panel ? " omni-panel" : ""), role: "dialog", "aria-modal": "true", "aria-label": t("Search or ask") },
    input, results, omniboxKeysHint());
  let close = () => {};
  const env = {
    get: (url, signal) => omniFetch(url, undefined, signal),
    post: (url, body, signal) => omniFetch(url, body, signal),
    later: (fn, ms) => setTimeout(fn, ms),
    clear: (id) => clearTimeout(id),
    now: () => performance.now(),
    open: (href) => { location.hash = href; if (!st.panel) close(); },
    act: (u, id, copies) => omniboxAct(u, id, copies),
    claim: () => claimCopy(),
    settle: (name, out, claim) => settleCopy(name, out, claim),
    confirm: (text) => confirm(text),
    close: () => close(),
    changed: () => { if (typeof scheduleLoad === "function") scheduleLoad({ tasks: true }); },
    render: () => omniboxRedraw(st, env, input, results),
  };
  if (st.panel) {
    document.body.classList.add("panel-omnibox");
    document.body.replaceChildren(box);
    close = () => window.close();
  } else {
    close = palOverlay(box);
  }
  input.addEventListener("input", () => omniboxType(st, input.value, env));
  input.addEventListener("keydown", (e) => omniboxKey(st, env, e, () => close()));
  box.addEventListener("keydown", (e) => {
    if (e.key === "Escape") {
      e.preventDefault();
      e.stopPropagation();
      close();
      return;
    }
    const to = omniboxTrap(e, [...box.querySelectorAll(OMNI_STOPS)].filter((el) => !el.disabled), document.activeElement);
    if (to) {
      e.preventDefault();
      to.focus();
    }
  }, true);
  env.render();
  input.focus();
  if (opts.text) {
    input.value = opts.text;
    omniboxType(st, opts.text, env);
  }
  return st;
}
