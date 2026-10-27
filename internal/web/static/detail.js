"use strict";

// UNIT_ENTRY_VIEWS are the lists a unit's detail reads its open entries from.
const UNIT_ENTRY_VIEWS = ["inbox", "next", "waiting", "someday"];
const MESSAGE_HEAD = { question: "Question", agent_post: "Message from the agent", proposal: "Proposal" };
const SKIP_VERB_BUTTONS = new Set(["seen", "answer", "edit", "retry", "approve", "dismiss", "delegate", "reply"]);
// DRAFT_VERBS are the entry verbs of a draft's actions.
const DRAFT_VERBS = { "draft.approve": "approve", "draft.edit": "edit", "draft.discard": "dismiss", "draft.retry": "retry" };

// draftWord is the label of a draft's action id, from the served specs, else its verb's.
function draftWord(id) {
  const sp = typeof state === "object" && state.specs && state.specs.byId ? state.specs.byId[id] : null;
  return (sp && sp.label) || verbLabel(DRAFT_VERBS[id]);
}
const ENTRY_DRAFT_STATES = "proposed,approved,sending,sent,failed";

const detailState = {
  id: "", entry: null, error: "", seenFor: "", log: null, logOpen: false, logError: "", editing: new Map(), answer: "", unitKey: "", unitEntries: [],
  guidance: "", drafts: [], draftsError: "", compose: { key: "", text: "", reaction: "" },
};

// detailEnv is what the pane reaches of the page; tests replace it.
const detailEnv = {
  getJSON: (url) => getJSON(url),
  post: (path, body) => entryPost(path, body),
  notice: (text, err) => setNotice(text, err),
  changed: () => scheduleLoad({ entries: true }),
  render: () => render(),
  close: () => writeHash({ entry: "" }),
  ask: (label, value) => prompt(label, value || ""),
  postJSON: (path, body) => entryPost(path, body),
  focused: () => {
    const el = typeof document !== "undefined" && document.activeElement && document.activeElement.closest && document.activeElement.closest('[data-focus^="entry:"]');
    return el ? el.dataset.focus.slice("entry:".length) : "";
  },
};

async function entryPost(path, body) {
  const res = await fetch(path, {
    method: "POST",
    headers: { "Content-Type": "application/json", Accept: "application/json", "X-Loop-API": API_VERSION },
    body: JSON.stringify(body),
  });
  const out = await res.json().catch(() => ({}));
  if (!res.ok || out.error) throw new Error(apiError(out, `HTTP ${res.status}`, res.status));
  return out;
}

function entryPath(id, ...rest) {
  return "/api/entries/" + [id, ...rest].map((s) => encodeURIComponent(s).replace(/%3A/gi, ":")).join("/");
}

// verbBody is a verb's request: its args as strings.
function verbBody(args) {
  const body = { source: "cockpit" };
  const kept = Object.entries(args || {}).filter(([, v]) => v !== undefined && v !== null).map(([k, v]) => [k, String(v)]);
  if (kept.length) body.args = Object.fromEntries(kept);
  return body;
}

function entryById(id) {
  if (!id) return null;
  if (detailState.entry && detailState.entry.id === id) return detailState.entry;
  const lists = [typeof gtdState === "object" ? gtdState.entries : [], detailState.unitEntries];
  for (const list of lists) {
    const e = (list || []).find((x) => x.id === id);
    if (e) return e;
  }
  return null;
}

// entryAct runs verb on entry and asks for a reload; the result, or null
// when it failed.
async function entryAct(entry, verb, args) {
  if (!entry) return null;
  try {
    const out = await detailEnv.post(entryPath(entry.id, verb), verbBody(args));
    detailEnv.notice(`${verb}: ${entry.title || entry.id}${out && out.lines && out.lines.length ? ". " + out.lines.join(" ") : ""}`);
    if (verb === "approve" || verb === "answer") {
      detailState.editing.delete(entry.id);
      detailState.answer = "";
    }
    detailEnv.changed();
    return out || {};
  } catch (err) {
    detailEnv.notice(`${verb} ${entry.title || entry.id} failed: ${err.message}`, true);
    return null;
  }
}

// draftTarget is the Outbox entry of the draft e carries: e itself, or
// draft:<id> when e is a message entry that only shows the draft.
function draftTarget(e) {
  const id = "draft:" + e.draft.id;
  return e.id === id ? e : { id, title: e.title, verbs: [] };
}

// approveEdited approves the draft of e with the text being edited.
async function approveEdited(e) {
  const out = await entryAct(draftTarget(e), "approve", { text: detailState.editing.get(e.id), rev: String(e.draft.rev) });
  if (out) detailState.editing.delete(e.id);
  return out;
}

// actOn runs verb on entry; an approve while its draft is being edited
// sends the edited text.
function actOn(entry, verb, args) {
  if (entry && verb === "approve" && entry.draft && detailState.editing.has(entry.id)) return approveEdited(entry);
  if (entry && entry.draft && entry.id.startsWith("draft:") && (verb === "approve" || verb === "retry")) args = { ...args, rev: String(entry.draft.rev) };
  return entryAct(entry, verb, args);
}

// detailPane is what keys.js reaches: the selected entry and its verbs.
const detailPane = {
  selected: () => entryById(detailEnv.focused()) || detailState.entry,
  act: (verb, args, entry) => actOn(entry || detailPane.selected(), verb, args),
  load: (route) => loadDetailPane(route),
  questionAt: (el) => {
    const e = detailState.entry;
    const m = e && e.message;
    if (!el || !el.closest || !el.closest(".d-question") || !m || m.state !== "open") return null;
    return { entry: e, question: { kind: m.confirm ? "confirm" : m.kind, options: m.options || [], recommend: m.recommend || "" } };
  },
};

async function loadDetailPane(route) {
  if (!route.entry) {
    Object.assign(detailState, { id: "", entry: null, error: "", seenFor: "", log: null, logOpen: false, logError: "", answer: "", guidance: "", drafts: [], draftsError: "" });
    return loadUnitEntries(route.unit);
  }
  const id = route.entry;
  if (detailState.id !== id) Object.assign(detailState, { id, entry: null, error: "", seenFor: "", log: null, logOpen: false, logError: "", answer: "", guidance: "", drafts: [], draftsError: "" });
  try {
    const e = await detailEnv.getJSON(entryPath(id));
    if (detailState.id !== id) return;
    detailState.entry = e;
    detailState.error = "";
    await loadEntryDrafts(e);
    if (e.state === "inbox" && detailState.seenFor !== id) {
      detailState.seenFor = id;
      detailEnv.post(entryPath(id, "seen"), { source: "cockpit" }).catch(() => {});
    }
    if (detailState.logOpen) await loadAgentLog();
  } catch (err) {
    if (detailState.id !== id) return;
    detailState.entry = null;
    detailState.error = err.message;
  }
}

// loadEntryDrafts reads the drafts made for e, but for a draft's own entry, which shows its draft.
async function loadEntryDrafts(e) {
  if (e.kind === "draft") {
    detailState.drafts = [];
    return;
  }
  try {
    const out = await detailEnv.getJSON("/api/drafts?" + new URLSearchParams({ entry: e.id, state: ENTRY_DRAFT_STATES }).toString());
    if (detailState.id !== e.id) return;
    detailState.drafts = out.drafts || [];
    detailState.draftsError = "";
  } catch (err) {
    detailState.draftsError = `Could not load the drafts: ${err.message}`;
  }
}

// loadUnitEntries reads the open entries that hold the unit key.
async function loadUnitEntries(key) {
  detailState.unitKey = key || "";
  if (!key) {
    detailState.unitEntries = [];
    return;
  }
  const pages = await Promise.allSettled(UNIT_ENTRY_VIEWS.map((v) => detailEnv.getJSON(entriesURL(v, "", [], ""))));
  if (detailState.unitKey !== key) return;
  const seen = new Set();
  const out = [];
  for (const p of pages) {
    if (p.status !== "fulfilled") continue;
    for (const e of p.value.entries || []) {
      if (seen.has(e.id) || !(e.units || []).some((u) => `${u.provider}:${u.key}` === key)) continue;
      seen.add(e.id);
      out.push(e);
    }
  }
  detailState.unitEntries = out;
}

async function loadAgentLog() {
  const e = detailState.entry;
  const u = e && (e.units || [])[0];
  if (!u) return;
  try {
    const d = await detailEnv.getJSON("/api/units/" + [u.provider, u.key].map(encodeURIComponent).join("/"));
    detailState.log = { events: d.events || [], unit: u };
    detailState.logError = "";
  } catch (err) {
    detailState.logError = `Could not load the agent log: ${err.message}`;
  }
}

async function openAgentLog() {
  detailState.logOpen = !detailState.logOpen;
  if (detailState.logOpen && !detailState.log) await loadAgentLog();
  detailEnv.render();
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
    title: [verbEntry(verb).summary, st.enabled ? "" : st.reason].filter(Boolean).join("\n") || undefined,
    "data-focus": `${extra.scope || "detail"}:${verb}:${e.id}`,
    onclick: run,
  }, label);
}

// askedArgs asks the value a verb needs first; null when the user backed out.
function askedArgs(verb) {
  const ask = typeof VERB_ASKS === "object" ? VERB_ASKS[verb] : null;
  if (!ask) return {};
  const v = detailEnv.ask(verbQuestion(verb));
  if (v === null || v === undefined) return null;
  return ask.args(String(v).trim());
}

// verbEntry is the catalog's word for verb: its label and summary.
function verbEntry(verb) {
  return ((typeof gtdState === "object" && gtdState.catalog && gtdState.catalog.verbs) || []).find((x) => x.verb === verb) || {};
}

// verbQuestion is what a verb that asks puts first: the catalog's input.
function verbQuestion(verb) {
  const v = verbEntry(verb);
  return v.input || v.label || verb;
}

function verbLabel(verb) {
  return verbEntry(verb).label || verb;
}

function verbButtons(e) {
  return (e.verbs || []).filter((v) => !SKIP_VERB_BUTTONS.has(v.verb)).map((v) => verbButton(e, v.verb, verbLabel(v.verb), () => {
    const args = askedArgs(v.verb);
    if (args) entryAct(e, v.verb, args);
  }));
}

function wouldLine(d) {
  const w = d.would || {};
  if (!w.policy) return "";
  return `Auto-send would ${w.policy}${w.reason ? `: ${w.reason}` : ""}`;
}

// draftCard shows the draft an entry carries, with approve, edit and
// discard through the entry's verbs.
function draftCard(e, scope) {
  const d = e.draft;
  const editing = detailState.editing.has(e.id);
  const head = `Draft ${(d.purpose || "").replaceAll("_", " ")}`.trim() + (d.state && d.state !== "proposed" ? ` (${d.state})` : "");
  const buttons = editing ? [
    verbButton(e, "approve", draftWord("draft.approve"), () => approveEdited(e), { scope, primary: true }),
    h("button", { type: "button", class: "act", "data-focus": `${scope}:cancel:${e.id}`, onclick: () => { detailState.editing.delete(e.id); detailEnv.render(); } }, "Cancel"),
  ] : [
    d.state === "failed" ? verbButton(e, "retry", draftWord("draft.retry"), () => entryAct(e, "retry", { rev: String(d.rev) }), { scope, primary: true })
      : verbButton(e, "approve", draftWord("draft.approve"), () => entryAct(e, "approve", { rev: String(d.rev) }), { scope, primary: true }),
    verbButton(e, (e.verbs || []).some((v) => v.verb === "edit") ? "edit" : "approve", draftWord("draft.edit"), () => { detailState.editing.set(e.id, d.text || ""); detailEnv.render(); }, { scope: scope + ":open" }),
    verbButton(e, "dismiss", draftWord("draft.discard"), () => entryAct(e, "dismiss", {}), { scope }),
  ];
  return h("section", { class: "d-draft", "aria-label": "Draft" },
    h("div", { class: "d-sub" }, head),
    draftWhere(d),
    editing ? h("textarea", { class: "draft-text", rows: 6, "aria-label": "Draft text", "data-focus": `${scope}:draft-text:${e.id}`, oninput: (ev) => detailState.editing.set(e.id, ev.target.value) }, detailState.editing.get(e.id))
      : h("pre", { class: "draft-text" }, d.text || (d.reaction ? `:${d.reaction}:` : "")),
    d.error ? h("p", { class: "err" }, d.error) : null,
    wouldLine(d) ? h("p", { class: "would" }, wouldLine(d)) : null,
    h("div", { class: "d-actions" }, ...buttons));
}

// draftEntry is the Outbox entry of draft d, its verbs read from the draft's actions, for draftCard.
function draftEntry(d, title) {
  const verbs = (d.actions || []).filter((a) => DRAFT_VERBS[a.id]).map((a) => ({ verb: DRAFT_VERBS[a.id], enabled: !!a.enabled, reason: a.reason || "" }));
  return { id: "draft:" + d.id, title, verbs, draft: d };
}

// entryDrafts are the drafts made for e, each with its card.
function entryDrafts(e) {
  const shown = e.draft ? e.draft.id : null;
  const ds = detailState.drafts.filter((d) => d.id !== shown);
  if (!ds.length && !detailState.draftsError) return null;
  return h("section", { class: "d-drafts", "aria-label": "Drafts" },
    h("h2", { class: "d-sub" }, "Drafts"),
    detailState.draftsError ? h("p", { class: "plan-error" }, detailState.draftsError) : null,
    ...ds.map((d) => draftCard(draftEntry(d, e.title || e.id), "drafts")));
}

const MODE_NAMES = { headless: "draft headless", session: "run a session" };

// delegateMode is the delegate's state in mode: the verb's own when the daemon sends no runners, else the
// runner's, off when the action does not run on it.
function delegateMode(st, mode) {
  if (!st.enabled || !st.runners) return st;
  const m = st.runners.find((x) => x.runner === mode);
  return m || { enabled: false, reason: `This action does not ${MODE_NAMES[mode] || mode}` };
}

// delegateBox hands the entry to an agent with optional guidance: headless, which drafts what it needs
// into the Outbox, or a session, off with its reason when the unit has nowhere to run one.
function delegateBox(e) {
  const st = verbState(e, "delegate");
  if (!(e.verbs || []).some((v) => v.verb === "delegate")) return null;
  const run = async (mode) => {
    const args = { mode };
    if (detailState.guidance.trim()) args.guidance = detailState.guidance.trim();
    if (await entryAct(e, "delegate", args)) {
      detailState.guidance = "";
      detailEnv.render();
    }
  };
  return h("section", { class: "d-delegate", "aria-label": "Delegate" },
    h("h2", { class: "d-sub" }, "Delegate"),
    st.enabled ? null : h("p", { class: "empty" }, st.reason || "Not offered here"),
    h("textarea", { rows: 2, "aria-label": "Guidance", placeholder: verbQuestion("delegate"), disabled: !st.enabled, "data-focus": "detail:guidance",
      oninput: (ev) => { detailState.guidance = ev.target.value; } }, detailState.guidance),
    h("div", { class: "d-actions" },
      modeButton(e, st, "headless", "Draft for me", () => run("headless"), true),
      modeButton(e, st, "session", "Hand to an agent", () => run("session"), false)),
    st.enabled && !delegateMode(st, "session").enabled ? h("p", { class: "empty" }, "Hand to an agent: " + delegateMode(st, "session").reason) : null);
}

function modeButton(e, st, mode, label, run, primary) {
  const m = delegateMode(st, mode);
  return h("button", {
    type: "button",
    class: "act" + (m.enabled ? "" : " off") + (primary ? " primary" : ""),
    disabled: !m.enabled,
    title: m.enabled ? undefined : m.reason,
    "data-focus": `detail:${mode}:delegate:${e.id}`,
    onclick: run,
  }, label);
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
  if (send) args.send = "now";
  return args;
}

// composer writes a reply in the unit's conversation, or a reaction where the unit takes one: Save to
// Outbox holds it for approval, Send now approves it at once. post runs the args; a result clears it.
function composer(key, opts) {
  const c = composeFor(key);
  const run = async (send) => {
    const args = replyArgs(c, send);
    if (!args) {
      detailEnv.notice("Write a reply or name a reaction first", true);
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
  return h("section", { class: "d-compose", "aria-label": "Reply" },
    h("h2", { class: "d-sub" }, "Reply"),
    off ? h("p", { class: "empty" }, off) : null,
    h("textarea", { rows: 3, "aria-label": "Reply", placeholder: opts.placeholder || "Your reply", disabled: !!off, "data-focus": `${opts.scope}:text`,
      oninput: (ev) => { c.text = ev.target.value; } }, c.text),
    opts.reacts ? h("input", { type: "text", class: "d-reaction", "aria-label": "Reaction", placeholder: "or a reaction, such as eyes", disabled: !!off, value: c.reaction,
      "data-focus": `${opts.scope}:reaction`, oninput: (ev) => { c.reaction = ev.target.value; } }) : null,
    h("div", { class: "d-actions" }, button("Save to Outbox", false, true), button("Send now", true, false)));
}

// entryComposer replies on the entry's first unit through the reply verb.
function entryComposer(e) {
  if (!(e.verbs || []).some((v) => v.verb === "reply")) return null;
  const st = verbState(e, "reply");
  const u = (e.units || [])[0];
  return composer("entry:" + e.id, { scope: "detail:reply", placeholder: verbQuestion("reply"), reason: st.enabled ? "" : st.reason || "Not offered here", reacts: !!u && providerReacts(u.provider),
    post: (args) => entryAct(e, "reply", args) });
}

// unitComposer replies on a unit through POST /api/drafts.
function unitComposer(u) {
  if (!u || !u.provider) return null;
  return composer("unit:" + u.provider + ":" + u.key, { scope: "unit:reply", reacts: providerReacts(u.provider), post: async (args) => {
    try {
      const d = await detailEnv.postJSON("/api/drafts", { unit: u, kind: args.kind, text: args.text, reaction: args.reaction, send: args.send === "now", source: "cockpit" });
      detailEnv.notice(d.state === "approved" ? `Draft ${d.id} approved; it goes out in a moment` : `Draft ${d.id} saved to the Outbox for your approval`);
      detailEnv.changed();
      return d;
    } catch (err) {
      detailEnv.notice(`Reply on ${unitLabel(u)} failed: ${err.message}`, true);
      return null;
    }
  } });
}

// questionCard shows the entry's message with its options; .d-question
// lets the digit keys answer it.
function questionCard(e) {
  const m = e.message;
  const open = m.state === "open";
  const opts = m.options || [];
  const answer = (args) => entryAct(e, "answer", detailState.answer ? { ...args, guidance: detailState.answer } : args);
  const choices = !open ? [] : m.confirm ? [
    h("button", { type: "button", class: "act primary", "data-focus": "question:yes", onclick: () => answer({ yes: true }) }, "Yes"),
    h("button", { type: "button", class: "act", "data-focus": "question:no", onclick: () => answer({ no: true }) }, "No"),
  ] : [...opts.filter((o) => o.id === m.recommend), ...opts.filter((o) => o.id !== m.recommend)].map((o) => h("button", {
    type: "button", class: "act" + (o.id === m.recommend ? " primary" : ""), title: o.consequence || undefined, "data-focus": "question:" + o.id, onclick: () => answer({ option_id: o.id }),
  }, o.id === m.recommend ? `${o.label} (recommended)` : o.label));
  return h("section", { class: "d-question", "aria-label": MESSAGE_HEAD[m.kind] || "Message" },
    h("h2", { class: "d-sub" }, MESSAGE_HEAD[m.kind] || "Message", open ? "" : ` (${m.state})`),
    h("p", { class: "summary" }, m.text),
    (m.answers || []).length ? h("ul", { class: "d-answers", "aria-label": "Answers" }, ...m.answers.map((a) => answerItem(m, a))) : null,
    open ? h("textarea", { rows: 2, "aria-label": "Answer", placeholder: opts.length || m.confirm ? "A note with your choice (optional)" : "Your answer", "data-focus": "question:text", oninput: (ev) => { detailState.answer = ev.target.value; } }, detailState.answer) : null,
    open ? h("div", { class: "d-actions" }, ...choices,
      !opts.length && !m.confirm ? h("button", { type: "button", class: "act primary", "data-focus": "question:send", onclick: () => detailState.answer.trim() && answer({}) }, "Send") : null) : null);
}

// answerItem is one answer to m as the user gave it, and whether the agent read it.
function answerItem(m, a) {
  const option = (m.options || []).find((o) => o.id === a.option_id);
  const said = [a.option_id ? (option ? option.label : a.option_id) : "", a.yes ? "yes" : a.no ? "no" : "", (a.text || "").trim()].filter(Boolean).join(", ");
  return h("li", { class: "d-answer" }, said, ": ", h("span", { class: "muted" }, a.read_at ? "read by the agent" : "not read by the agent yet"));
}

function personName(p) {
  return (p && (p.name || p.key)) || "";
}

// draftWhere is where a draft posts, as its publisher named it; the raw target, marked unknown, when it could not.
function draftWhere(d) {
  if (d.where) return h("div", { class: "meta" }, "To ", h("span", { class: "ref" }, d.where));
  const pending = ["proposed", "failed", "approved", "sending"].includes(d.state);
  return h("div", { class: "meta" }, "To ", h("span", { class: "ref" }, d.target || d.unit || ""), pending ? " (destination unknown)" : "");
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

// unitCard is a unit's stored card (#unit-card).
function unitCard(s) {
  const c = s.card || {};
  const signals = c.signals || [];
  const lh = c.last_human;
  return h("section", { class: "d-card" + (s.stale ? " stale" : ""), "aria-label": "Card" },
    h("div", { class: "meta" },
      unitRef(c.unit || {}, c.label || unitLabel(c.unit), c.link),
      c.state ? [" ", h("span", { class: "pill" }, c.state)] : null,
      c.outcome ? [" ", h("span", { class: "pill" }, c.outcome)] : null,
      s.stale ? [" ", h("span", { class: "flag" }, "plugin disabled")] : null),
    c.title ? h("div", { class: "title" }, c.title) : null,
    c.why ? h("div", { class: "why" }, c.why) : null,
    (c.waits_on || []).length ? h("div", {}, "Waiting on ", h("strong", {}, c.waits_on.map(personName).join(", "))) : null,
    signals.length ? h("ul", { class: "signals" }, ...signals.map((g) => h("li", { class: "tone-" + (g.tone || "neutral") }, h("span", { class: "k" }, g.key), " ", g.at ? stamp(g.at) : g.value))) : null,
    lh ? h("div", { class: "meta" }, `${personName(lh.who)}: ${lh.what}`, lh.at ? ` (${stamp(lh.at)})` : "") : null);
}

function itemsBlock(items) {
  if (!items || !items.length) return null;
  return [h("h2", { class: "d-sub" }, "Items"),
    h("ul", { class: "d-items" }, ...items.map((it) => h("li", { class: "item-" + (it.state || "open") },
      h("span", {}, it.summary || it.id), " ", h("span", { class: "pill" }, it.state || ""))))];
}

function agentLog() {
  const log = detailState.log;
  if (detailState.logError) return h("p", { class: "plan-error" }, detailState.logError);
  if (!log) return h("p", { class: "empty" }, "Loading the agent log.");
  if (!log.events.length) return h("p", { class: "empty" }, "No agent activity on this unit.");
  return h("ol", { class: "timeline" }, ...log.events.map((ev) => h("li", { class: "ev-" + ev.kind },
    h("time", { datetime: ev.at }, stamp(ev.at)),
    h("div", {}, h("span", { class: "kind" }, String(ev.kind || "").replaceAll("_", " ")), ev.actor ? h("span", { class: "who" }, " " + ev.actor) : null))));
}

// entryPane is the detail of an entry: its head, verbs, items, question,
// draft and cards; the agent log waits for a click.
function entryPane(e) {
  const waiting = ((e.waiting || {}).on || []).map((p) => p.name).filter(Boolean);
  return [
    h("div", { class: "d-head" },
      h("button", { type: "button", class: "close", "aria-label": "Close detail", "data-focus": "detail:close", onclick: () => detailEnv.close() }, "×"),
      h("h1", {}, e.focus ? "★ " : "", e.title || e.id),
      h("div", { class: "meta" },
        h("span", { class: "pill" }, e.state), e.kind && e.kind !== e.state ? [" ", h("span", { class: "pill" }, e.kind)] : null,
        e.changed ? [" ", h("span", { class: "flag" }, "changed")] : null,
        e.area ? [" area ", e.area] : null,
        e.project_title ? [" in ", e.project_title] : null)),
    (e.contexts || []).length ? h("div", { class: "d-contexts" }, ...e.contexts.map((c) => h("span", { class: "pill" }, c))) : null,
    e.notes ? h("p", { class: "d-notes" }, e.notes) : null,
    waiting.length ? h("div", {}, "Waiting on ", h("strong", {}, waiting.join(", ")), e.waiting.since ? ` since ${stamp(e.waiting.since)}` : "") : null,
    e.deadline ? h("div", {}, "Due ", stamp(e.deadline)) : null,
    h("div", { class: "d-actions" }, ...verbButtons(e),
      e.message || e.draft ? null : verbButton(e, "approve", verbLabel("approve"), () => entryAct(e, "approve", {})),
      e.draft ? null : verbButton(e, "dismiss", verbLabel("dismiss"), () => entryAct(e, "dismiss", {}))),
    itemsBlock(e.items),
    e.message ? questionCard(e) : null,
    e.draft ? draftCard(e, "detail") : null,
    delegateBox(e),
    entryComposer(e),
    entryDrafts(e),
    ...(e.cards || []).map(unitCard),
    (e.units || []).length ? h("div", { class: "d-log" },
      h("button", { type: "button", class: "act", "aria-expanded": String(detailState.logOpen), "data-focus": "detail:agent-log", onclick: () => openAgentLog() }, "Agent log"),
      detailState.logOpen ? agentLog() : null) : null,
  ];
}

// unitExtras are a unit detail's card and open entries.
function unitExtras(d) {
  const key = detailState.unitKey;
  const entries = key ? detailState.unitEntries : [];
  return h("div", { class: "d-unit" },
    d && d.card ? unitCard(d.card) : null,
    d && (d.items || []).length ? itemsBlock(d.items) : null,
    d && d.row ? unitComposer(d.row.unit) : null,
    entries.length ? [h("h2", { class: "d-sub" }, "Entries"),
      h("ul", { class: "d-entries" }, ...entries.map((e) => h("li", {},
        h("a", { href: "#" + gtdHash({ entry: e.id, pr: "" }), "data-focus": "unit-entry:" + e.id }, e.title || e.id), " ", h("span", { class: "pill" }, e.state))))] : null);
}

// renderEntryDetail draws the entry route into box; false when the route
// names no entry.
function renderEntryDetail(box, route) {
  if (!route.entry) return false;
  const e = detailState.entry && detailState.entry.id === route.entry ? detailState.entry : null;
  const parts = e ? entryPane(e) : [
    h("div", { class: "d-head" },
      h("button", { type: "button", class: "close", "aria-label": "Close detail", "data-focus": "detail:close", onclick: () => detailEnv.close() }, "×"),
      h("p", { class: detailState.error ? "plan-error" : "empty" }, detailState.error ? `Could not load the entry: ${detailState.error}` : "Loading the entry.")),
  ];
  const was = document.activeElement && box.contains(document.activeElement) ? document.activeElement : null;
  const caret = was && typeof was.selectionStart === "number" ? [was.selectionStart, was.selectionEnd] : null;
  box.replaceChildren(...parts.flat().filter(Boolean));
  box.dataset.key = "entry:" + route.entry;
  box.hidden = false;
  document.body.classList.add("has-detail");
  const el = was && was.dataset.focus && box.querySelector(`[data-focus="${CSS.escape(was.dataset.focus)}"]`);
  if (el) {
    el.focus({ preventScroll: true });
    if (caret && el.setSelectionRange) el.setSelectionRange(...caret);
  }
  return true;
}
