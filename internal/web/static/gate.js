"use strict";

// The one decision card every surface draws: a task's open gate (a question, a plan, a post, a proposal) or a
// draft, with its actions. The detail draws them; the Outbox and the queues call the same functions.

// gateState is what an open gate's card holds between renders: the text typed and the option picked, by gate id.
const gateState = { answers: new Map(), picked: new Map() };

function resetGates() {
  gateState.answers.clear();
  gateState.picked.clear();
}

function subjectOf(g) {
  return g && g.subject && typeof g.subject === "object" ? g.subject : {};
}

// gateOption is the option picked on g's card: the person's pick, else the recommended one; "" for none.
function gateOption(g) {
  if (gateState.picked.has(g.id)) return gateState.picked.get(g.id);
  const subject = subjectOf(g);
  return (subject.options || []).some((o) => o.id === subject.recommend) ? subject.recommend : "";
}

// markResolving dims the card drawn for selector while its decision is on its way; a failed one comes back.
function markResolving(selector, on) {
  const card = typeof document === "object" && document.querySelector ? document.querySelector(selector) : null;
  if (!card || !card.classList) return;
  card.classList.toggle("resolving", on);
  card.setAttribute("aria-busy", String(on));
}

// resolveGate resolves gate g of task e with intent id; an intent that takes an answer sends the option given,
// else the one picked on the card, then the text typed in it.
async function resolveGate(e, g, id, option) {
  const intent = (g.intents || []).find((x) => x.id === id);
  if (!intent) return null;
  const args = {};
  if (intent.answer) {
    const pick = option === undefined ? gateOption(g) : option;
    const text = (gateState.answers.get(g.id) || "").trim();
    if (!text && !pick) {
      detailEnv.notice(t("{action}: write the answer first", { action: intent.label }), true);
      return null;
    }
    args.answer = [pick, text].filter(Boolean).join("\n");
  }
  const card = `.d-gate[data-gate=${JSON.stringify(String(g.id))}]`;
  markResolving(card, true);
  try {
    const out = await detailEnv.post("/api/gates/" + encodeURIComponent(g.id) + "/" + encodeURIComponent(id), verbBody(args));
    detailEnv.notice(`${intent.label}: ${(e && (e.title || e.id)) || g.text || g.kind}`);
    gateState.answers.delete(g.id);
    gateState.picked.delete(g.id);
    detailEnv.changed();
    return out || {};
  } catch (err) {
    markResolving(card, false);
    detailEnv.notice(t("{action} failed: {error}", { action: intent.label, error: err.message }), true);
    return null;
  }
}

// gateOptions are the option cards of a question gate's message, the recommended first and picked until the
// person picks another; the answer intent sends the one picked with the text typed below.
function gateOptions(g, answer) {
  const subject = subjectOf(g);
  const opts = subject.options || [];
  if (!answer || !opts.length) return null;
  const picked = gateOption(g);
  const ordered = [...opts.filter((o) => o.id === subject.recommend), ...opts.filter((o) => o.id !== subject.recommend)];
  return h("div", { class: "opts" }, ...ordered.map((o) => {
    const on = o.id === picked;
    return h("button", {
      type: "button", class: "opt" + (on ? " on" : ""), "aria-pressed": String(on), "data-focus": `gate:${g.id}:option:${o.id}`,
      onclick: () => {
        gateState.picked.set(g.id, on ? "" : o.id);
        detailEnv.render();
      },
    },
    h("span", { class: "r", "aria-hidden": "true" }),
    h("span", { class: "lb" }, o.label),
    o.id === subject.recommend ? h("span", { class: "rec" }, t("recommended")) : null,
    o.consequence ? h("span", { class: "cs" }, o.consequence) : null);
  }));
}

// gateAnswer is the free-text answer of a gate whose intent answer takes one; Cmd+Enter or Ctrl+Enter sends it.
function gateAnswer(e, g, answer) {
  return h("textarea", {
    rows: 2, "aria-label": t("Answer"), "aria-keyshortcuts": "Meta+Enter", "data-focus": `gate:${g.id}:text`,
    oninput: (ev) => { gateState.answers.set(g.id, ev.target.value); },
    onkeydown: (ev) => {
      if (ev.key !== "Enter" || !(ev.metaKey || ev.ctrlKey)) return;
      if (ev.preventDefault) ev.preventDefault();
      resolveGate(e, g, answer.id);
    },
  }, gateState.answers.get(g.id) || "");
}

// planFactText is a fact that made a plan item, in the words of the profile's task facet that reads it, else as
// name = value.
function planFactText(f) {
  const facet = taskFacetOf(f.name);
  return facet ? `${facet.label}: ${propText(f.value, facet)}` : `${f.name} = ${f.value}`;
}

// outLink is label linked to href, a page of another site, when href is safe; else label as text.
function outLink(href, label) {
  const safe = safeURL(href);
  return safe ? h("a", { href: safe, target: "_blank", rel: "noopener noreferrer" }, label) : h("span", {}, label);
}

// planTarget is what a plan item acts on: its task's title, linked to the task's detail, with the units the task
// came from, each linked when it has a link; else its unit as its plugin labels it.
function planTarget(p) {
  const tg = p.target;
  if (tg) {
    return h("p", { class: "meta plan-on" }, t("Acts on"), " ", h("a", { href: taskHref(tg.task), "data-focus": "plan-task:" + tg.task }, tg.title || tg.task),
      ...(tg.units || []).flatMap((u) => [" · ", outLink(u.link, u.label)]));
  }
  const label = p.label || (p.unit && p.unit.key) || "";
  return label ? h("p", { class: "meta plan-on" }, t("Acts on"), " ", outLink(p.link, label)) : null;
}

// STEP_WORDS are what each step of a plan item does, in the catalog's words, filled with the step's arguments as
// a person reads them.
const STEP_WORDS = {
  "session.close": (a) => t("Close the agent session", a),
  "workspace.remove": (a) => t("Remove the workspace", a),
  "workspace.unregister": (a) => t("Forget the workspace whose folder is gone", a),
  "turn.dismiss": (a) => t("Dismiss the agent's open turn", a),
  "answer.refuse": (a) => t("Refuse a queued answer whose route is gone", a),
  "session.mark_dormant": (a) => t("Mark the agent session dormant", a),
  "turn.start": (a) => t("Start an agent turn: {intent}", a),
  "draft.propose": (a) => t("Draft a message you approve before it posts", a),
  "classify": (a) => t("Classify it with an agent call", a),
  "notify.self": (a) => t("Notify you", a),
  "task.verb": (a) => t("{verb} the task", a),
  "gate.open": (a) => t("Ask you to decide", a),
  "items.resolve": (a) => t("Resolve its open items as {resolution}", a),
  "task.set": (a) => t("Set {prop} to {value}", a),
};

// RESOLUTION_WORDS are how an item resolves, in the catalog's words, by resolution.
const RESOLUTION_WORDS = {
  done: () => t("resolved as done"),
  answered: () => t("resolved as answered"),
  fixed: () => t("resolved as fixed"),
  delegated: () => t("resolved as delegated"),
  declined: () => t("resolved as declined"),
  dismissed: () => t("resolved as dismissed"),
  superseded: () => t("resolved as superseded"),
  moot: () => t("resolved as moot"),
};

// stepText is a plan step in the catalog's words, filled with its arguments as a person reads them; the step's
// name when the catalog has no words for it.
function stepText(st) {
  const words = Object.hasOwn(STEP_WORDS, st.use) ? STEP_WORDS[st.use] : null;
  if (!words) return st.use;
  const args = st.args || {};
  const res = args.resolution || "done";
  const prop = args.prop ? taskFacetOf("task.prop." + args.prop) : null;
  return words({ ...args, resolution: Object.hasOwn(RESOLUTION_WORDS, res) ? RESOLUTION_WORDS[res]() : res,
    verb: args.verb ? verbLabel(args.verb) : "", prop: prop ? prop.label : args.prop || "" });
}

// PLAN_DRAFTS are the steps whose message posts once you approve its draft: applying them posts later.
const PLAN_DRAFTS = ["draft.propose"];

// planPosts says whether applying a plan item may post where others see it: a step that reaches others, one whose
// intent decides, or a draft.
function planPosts(does) {
  return does.some((st) => st.reach === "visible" || !st.reach || PLAN_DRAFTS.includes(st.use));
}

// planDoes is what applying a plan item does: each step in the catalog's words with the open items it resolves,
// and that it posts nothing when no step may post.
function planDoes(p) {
  const does = p.does || [];
  if (!does.length) return null;
  return h("div", { class: "plan-does" },
    h("ul", {}, ...does.map((st) => h("li", { class: "plan-do" },
      h("span", { class: "plan-do-what", title: st.use }, stepText(st)),
      (st.items || []).length ? h("ul", { class: "plan-do-items" }, ...st.items.map((x) => h("li", { class: "plan-do-item" }, x))) : null))),
    planPosts(does) ? null : h("p", { class: "meta plan-posts" }, t("Posts nothing")));
}

// planWhy is why a plan item fired, folded: each fact its workflow matched, worded as planFactText words it.
function planWhy(p) {
  const facts = (p.why && p.why.matched) || [];
  return facts.length ? h("details", { class: "plan-why" },
    h("summary", {}, t("Why it fired")),
    h("ul", {}, ...facts.map((f) => h("li", { title: `${f.name} = ${f.value}` }, planFactText(f))))) : null;
}

// gatePlan is what running a plan gate's item does, on what and, folded, why it fired. Null when the item no
// longer waits.
function gatePlan(g) {
  const p = g.plan;
  if (!p) return null;
  return h("div", { class: "gate-plan" },
    p.summary ? h("p", { class: "plan-summary" }, p.summary) : null,
    planTarget(p),
    planDoes(p),
    p.reason ? h("p", { class: "meta plan-reason" }, planReason(p)) : null,
    planWhy(p));
}

// planReason is why the plan holds item p, in the catalog's words: the engine's template filled with its values, a
// wait's facts as planFactText words them and a refusal through the catalog; any other reason through the catalog,
// as it reads when the catalog has no words for it.
function planReason(p) {
  if (!p.reason_key) return t(p.reason);
  const params = { ...(p.reason_params || {}) };
  if ((p.reason_facts || []).length) params.facts = p.reason_facts.map(planFactText).join(", ");
  if (params.refusal) params.refusal = t(params.refusal);
  return t(p.reason_key, params);
}

// gateCard is an open gate of task e: what it asks, the agent's message as markdown folded after its first
// blocks, its option cards, the free-text answer when an intent takes one, then its intents in the profile's
// order, the first primary, each with the digit that picks it while the card has the focus.
function gateCard(e, g, opts = {}) {
  const scope = opts.scope || "gate";
  const intents = g.intents || [];
  const subject = subjectOf(g);
  const deflt = intents.find((x) => x.id === g.default);
  const answer = intents.find((x) => x.answer);
  const title = g.text || g.name || g.kind;
  return h("article", { class: "ask d-gate", "data-gate": String(g.id), "data-kind": g.kind, "aria-label": title },
    h("header", { class: "hd" },
      h("i", { class: "dot", "aria-hidden": "true" }),
      h("h3", { class: "d-sub" }, title),
      subject.created_at ? h("time", { datetime: subject.created_at }, fmtDate(subject.created_at, "datetime")) : null),
    h("div", { class: "bd" },
      subject.text ? renderMarkdown(subject.text, { fold: 4, key: `${scope}:${g.id}` }) : null,
      gatePlan(g),
      g.delivery ? h("p", { class: "meta" }, g.delivery) : null,
      g.deadline ? h("p", { class: "meta" }, deflt ? t("{action} at {time} unless you pick another", { action: deflt.label, time: fmtDate(g.deadline, "datetime") })
        : t("Until {time}", { time: fmtDate(g.deadline, "datetime") })) : null,
      gateOptions(g, answer),
      answer ? gateAnswer(e, g, answer) : null,
      h("div", { class: "d-actions" }, ...intents.map((x, i) => h("button", {
        type: "button", class: "act" + (i === 0 ? " primary" : ""), "data-focus": `gate:${g.id}:${x.id}`, "aria-keyshortcuts": i < 9 ? String(i + 1) : undefined,
        onclick: () => resolveGate(e, g, x.id),
      }, i < 9 ? h("kbd", {}, String(i + 1)) : null, " ", x.label)),
      answer ? h("span", { class: "keyhint" }, h("kbd", {}, "⌘↵"), " ", answer.label) : null)));
}

// gateAt is the gate whose card holds el, with its task, among sources: each a task and the gates the page
// draws of it.
function gateAt(el, sources) {
  const card = el && el.closest ? el.closest(".d-gate") : null;
  if (!card) return null;
  const id = card.getAttribute("data-gate");
  for (const s of sources) {
    const g = (s.gates || []).find((x) => String(x.id) === id);
    if (g) return { task: s.task, gate: g };
  }
  return null;
}

// DRAFT_HANDLERS are the handlers of a draft's delivery actions; DRAFT_CARD_HANDLERS those of every verb its
// card offers on the draft's own task, the kind's own dismiss among them.
const DRAFT_HANDLERS = { "draft.edit": "edit", "draft.retry": "retry" };
const DRAFT_CARD_HANDLERS = ["approve", "drop", "edit", "retry"];

// draftWord is the label of a draft's action id, from the served specs, else its verb's.
function draftWord(id) {
  const sp = typeof state === "object" && state.specs && state.specs.byId ? state.specs.byId[id] : null;
  return (sp && sp.label) || verbLabel(verbFor(DRAFT_HANDLERS[id]));
}

// gateWord is the label gates.toml gives the intent of d's gate on e that resolves it to status, else verb's.
function gateWord(e, d, status, verb) {
  const name = (d.intents || {})[status];
  const g = ((e && e.gates) || []).find((x) => x.subject === d || (d.gate && x.id === d.gate));
  const intent = g && name ? (g.intents || []).find((x) => x.id === name) : null;
  return (intent && intent.label) || verbLabel(verb);
}

// unitSkipPath is the path that stops d's workflow on d's unit, "" when d names no workflow.
function unitSkipPath(d) {
  const at = String(d.unit || "").indexOf("/");
  const workflow = (d.origin || {}).workflow;
  if (at < 1 || !workflow) return "";
  return unitPath({ provider: d.unit.slice(0, at), key: d.unit.slice(at + 1) }, "skip", workflow);
}

// DRAFT_OFFERS are the draft actions a card offers only when the draft serves them: their words, and the request
// each sends.
const DRAFT_OFFERS = {
  "draft.close-silently": { key: "queue:close-silently", label: () => t("Close without a message"), path: (d) => "/api/drafts/" + encodeURIComponent(d.id) + "/close-silently", body: (d) => ({ rev: d.rev }) },
  "draft.skip-workflow": { key: "queue:skip-workflow", label: () => t("Don't nudge this conversation"), path: unitSkipPath, body: () => ({ source: "cockpit" }) },
};

async function runDraftOffer(d, label, offer) {
  try {
    await detailEnv.post(offer.path(d), offer.body(d));
    detailEnv.notice(`${label}: ${d.where || d.target || d.unit}`);
    detailEnv.changed();
  } catch (err) {
    detailEnv.notice(t("{action} failed: {error}", { action: label, error: err.message }), true);
  }
}

// offeredDraftActions are the offered draft actions d serves, each off with its reason where refused.
function offeredDraftActions(d) {
  return (d.actions || []).filter((a) => DRAFT_OFFERS[a.id] && DRAFT_OFFERS[a.id].path(d)).map((a) => {
    const offer = DRAFT_OFFERS[a.id];
    const label = a.label || offer.label();
    return { label, enabled: !!a.enabled, reason: a.reason, title: a.enabled ? a.note : a.reason, focus: `${a.id}:draft:${d.id}`, key: offer.key,
      run: () => runDraftOffer(d, label, offer) };
  });
}

// windowLine says when the window of d's gate sends it, "" when it has none.
function windowLine(d) {
  return d.deadline ? t("Sends at {time} unless you decide first", { time: fmtDate(d.deadline, "datetime") }) : "";
}

// draftWhere is where a draft posts, as its publisher named it, the raw target marked unknown when it could
// not, and the language its text is written in.
function draftWhere(d) {
  const pending = ["held", "failed", "queued", "sending"].includes(d.state);
  const lang = d.language ? [" · ", h("span", { class: "lang", lang: d.language }, fmtLanguage(d.language))] : null;
  if (d.where) return h("div", { class: "meta" }, t("To"), " ", h("span", { class: "ref" }, d.where), lang);
  return h("div", { class: "meta" }, t("To"), " ", h("span", { class: "ref" }, d.target || d.unit || ""), pending ? " (" + t("destination unknown") + ")" : "", lang);
}

// broadcastToggle is the checkbox that also posts thread reply d to its conversation, which d's publisher offers
// by setting d.broadcast; it turns it through d's own task's edit verb while d may still change.
function broadcastToggle(d, scope) {
  if (!d.broadcast) return null;
  const pending = ["held", "failed"].includes(d.state);
  const id = "draft:" + d.id;
  return h("label", { class: "meta broadcast" },
    h("input", { type: "checkbox", checked: d.broadcast === "on", disabled: !pending, "data-focus": `${scope}:broadcast:${id}`,
      onchange: (ev) => taskAct({ id, title: d.title || id }, verbFor("edit"), { broadcast: String(!!ev.target.checked) }) }),
    " ", t("Also send to the conversation"));
}

// catalogWords is the served views catalog, which holds the profile's draft words; empty before it loads.
function catalogWords() {
  return (typeof tasksState === "object" && tasksState.catalog) || {};
}

// draftEffects are the profile's sentences for what sending d does besides posting it.
function draftEffects(d) {
  const words = catalogWords().effects || {};
  return (d.effects || []).map((x) => words[x]).filter(Boolean);
}

// draftContextView is what a draft answers: its task's asks, each with the quotes its brief keeps, and the items
// it is about, each with where it stands.
function draftContextView(c) {
  const asks = c.asks || [];
  const items = c.items || [];
  if (!asks.length && !items.length) return null;
  return h("div", { class: "d-context" },
    ...asks.map((a) => h("div", { class: "ask" },
      h("div", { class: "ask-head" }, a.summary, a.asker ? h("span", { class: "who" }, " · " + a.asker) : null),
      ...(a.quotes || []).map((q) => h("blockquote", { class: "quote" }, q)))),
    ...items.map((it) => h("div", { class: "item" }, h("span", { class: "st st-" + it.state }, itemStateWord(it.state)), " ", it.summary || it.id)));
}

// itemStateWord is the word for an item's state, open or resolved.
function itemStateWord(st) {
  if (st === "open") return t("open");
  if (st === "resolved") return t("resolved");
  return st;
}

// draftLinks link d to its task, when the card is not on it, and to its unit at the source.
function draftLinks(e, d, scope) {
  const task = d.task && (!e || e.id !== d.task) ? h("a", { href: taskHref(d.task), class: "ref", "data-focus": `${scope}:task:draft:${d.id}` }, t("Open its task")) : null;
  const link = safeURL(d.permalink || d.link);
  const unit = link ? h("a", { href: link, target: "_blank", rel: "noopener noreferrer", class: "ext" }, d.label || t("source")) : null;
  return task || unit ? h("div", { class: "meta d-links" }, task, task && unit ? " · " : null, unit) : null;
}

// taskHref is the cockpit's link to task id.
function taskHref(id) {
  return "#" + (viewHash({ task: id, pr: "" }) || "task=" + encodeURIComponent(id));
}

// resolveDraft resolves d's gate with intent, sending args with the draft's version.
async function resolveDraft(d, intent, label, args) {
  const card = `.d-draft[data-draft=${JSON.stringify(String(d.id))}]`;
  markResolving(card, true);
  try {
    await detailEnv.post("/api/gates/" + encodeURIComponent(d.gate) + "/" + encodeURIComponent(intent), verbBody({ ...args, rev: String(d.rev) }));
    detailEnv.notice(`${label}: ${d.where || d.target || d.unit}`);
    detailState.editing.delete("draft:" + d.id);
    detailEnv.changed();
  } catch (err) {
    markResolving(card, false);
    detailEnv.notice(t("{action} failed: {error}", { action: label, error: err.message }), true);
  }
}

// draftActions are the actions draft d's card offers on task e, each with the queue key that runs it: approve
// (retry for a failed delivery), edit and discard through d's own task's verbs when e is its task, else
// through its gate's intents, then the offered draft actions d serves; while d is being edited, approve with
// the text and cancel.
function draftActions(e, d) {
  const own = !!e && isDraftTask(e) && draftOf(e).id === d.id;
  const key = own ? e.id : "draft:" + d.id;
  const editing = detailState.editing.has(key);
  const acts = own ? ownDraftActions(e, d, key, editing) : gateDraftActions(e, d, key, editing);
  return [...acts, ...(editing ? [] : offeredDraftActions(d))].filter(Boolean);
}

const editDraft = (key, d) => () => { detailState.editing.set(key, d.text || ""); detailEnv.render(); };
const cancelEdit = (key) => ({ label: t("Cancel"), enabled: true, focus: "cancel:" + key, run: () => { detailState.editing.delete(key); detailEnv.render(); } });

// ownDraftActions are the actions of the draft e, its own task, carries, through the task's verbs.
function ownDraftActions(e, d, key, editing) {
  const [approve, drop, edit, retry] = DRAFT_CARD_HANDLERS.map((x) => offeredVerb(e, x));
  const verb = (v, label, run, extra) => {
    const st = verbState(e, v);
    if (!st) return null;
    const title = [verbTask(v).summary, st.enabled ? "" : st.reason].filter(Boolean).join("\n");
    return { label, run, enabled: st.enabled, reason: st.reason, title, focus: `${v}:${e.id}`, ...extra };
  };
  const primary = { primary: true, key: "queue:primary" };
  if (editing) return [verb(approve, gateWord(e, d, "approved", approve), () => approveEdited(e), primary), cancelEdit(key)];
  return [
    d.state === "failed" ? verb(retry, draftWord("draft.retry"), () => taskAct(e, retry, { rev: String(d.rev) }), primary)
      : verb(approve, gateWord(e, d, "approved", approve), () => taskAct(e, approve, { rev: String(d.rev) }), primary),
    verb(edit || approve, draftWord("draft.edit"), editDraft(key, d), { key: "queue:edit", focus: `open:${edit || approve}:${e.id}` }),
    verb(drop, gateWord(e, d, "rejected", drop), () => taskAct(e, drop, {}), { key: "queue:discard" }),
  ];
}

// gateDraftActions are the actions of a draft shown on another task, through its gate's intents; retry goes
// through its own task.
function gateDraftActions(e, d, key, editing) {
  const intents = (d.gate && d.intents) || {};
  const approve = verbFor("approve");
  const approveLabel = gateWord(e, d, "approved", approve);
  const act = (label, focus, run, extra) => ({ label, run, enabled: true, focus: `${focus}:${key}`, ...extra });
  const primary = { primary: true, key: "queue:primary" };
  if (editing) {
    return [intents.approved ? act(approveLabel, approve, () => resolveDraft(d, intents.approved, approveLabel, { text: detailState.editing.get(key) }), primary) : null,
      cancelEdit(key)];
  }
  const retry = verbFor("retry");
  const drop = verbFor("drop");
  const dropLabel = gateWord(e, d, "rejected", drop);
  return [
    d.state === "failed" ? act(draftWord("draft.retry"), retry, () => taskAct({ id: key, title: d.title || key }, retry, { rev: String(d.rev) }), primary)
      : intents.approved ? act(approveLabel, approve, () => resolveDraft(d, intents.approved, approveLabel, {}), primary) : null,
    intents.approved ? act(draftWord("draft.edit"), "open:" + approve, editDraft(key, d), { key: "queue:edit" }) : null,
    intents.rejected ? act(dropLabel, drop, () => resolveDraft(d, intents.rejected, dropLabel, {}), { key: "queue:discard" }) : null,
  ];
}

// draftActionButton is action a of a draft's card in scope, with the key keyOf gives its queue key, if any.
function draftActionButton(a, scope, keyOf) {
  const key = keyOf && a.key ? keyOf(a.key) : "";
  return h("button", {
    type: "button", class: "act" + (a.enabled ? "" : " off") + (a.primary ? " primary" : ""), disabled: !a.enabled, title: a.title || undefined,
    "data-focus": `${scope}:${a.focus}`, "aria-keyshortcuts": key || undefined, onclick: a.run,
  }, key ? [h("kbd", {}, key === "Enter" ? "↵" : key), " "] : null, a.label);
}

// draftCard is draft d on task e: where it posts and in what language, its text as posted, why it was drafted,
// what sending it does, its window, its actions and links to its task and its unit. On d's own task its
// actions are the task's verbs, elsewhere its gate's intents. opts.context, an Outbox draft's, adds what it
// answers.
function draftCard(e, d, opts = {}) {
  const scope = opts.scope || "detail";
  const own = !!e && isDraftTask(e) && draftOf(e).id === d.id;
  const key = own ? e.id : "draft:" + d.id;
  const editing = detailState.editing.has(key);
  const words = catalogWords();
  const purpose = d.purpose ? (words.draft_purposes || {})[d.purpose] || d.purpose : "";
  const st = d.state && d.state !== "held" ? (words.draft_states || {})[d.state] || d.state : "";
  const why = d.state === "dropped" && d.reason_text ? `: ${d.reason_text}` : "";
  const head = (purpose ? t("Draft {purpose}", { purpose }) : t("Draft")) + (st ? ` (${st}${why})` : "");
  const effects = draftEffects(d);
  return h("section", { class: "d-draft", "data-draft": String(d.id), "aria-label": t("Draft") },
    h("div", { class: "d-sub" }, head),
    opts.context ? draftContextView(opts.context) : null,
    draftWhere(d),
    broadcastToggle(d, scope),
    editing ? h("textarea", { class: "draft-text", rows: 6, "aria-label": t("Draft text"), "data-focus": `${scope}:draft-text:${key}`, oninput: (ev) => detailState.editing.set(key, ev.target.value) }, detailState.editing.get(key))
      : d.text ? renderMarkdown(d.text, { fold: 6, key: `${scope}:draft:${d.id}`, names: d.names || {} }) : h("p", { class: "draft-text" }, d.reaction ? `:${d.reaction}:` : ""),
    d.why ? h("p", { class: "why" }, d.why) : null,
    d.quoted ? h("p", { class: "why" }, d.quoted) : null,
    effects.length ? h("ul", { class: "effects" }, ...effects.map((x) => h("li", {}, x))) : null,
    d.error ? h("p", { class: "err" }, d.error) : null,
    windowLine(d) ? h("p", { class: "would" }, windowLine(d)) : null,
    h("div", { class: "d-actions" }, ...draftActions(e, d).map((a) => draftActionButton(a, scope, opts.keyOf))),
    draftLinks(e, d, scope));
}
