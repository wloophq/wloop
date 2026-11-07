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
  try {
    const out = await detailEnv.post("/api/gates/" + encodeURIComponent(g.id) + "/" + encodeURIComponent(id), verbBody(args));
    detailEnv.notice(`${intent.label}: ${(e && (e.title || e.id)) || g.text || g.kind}`);
    gateState.answers.delete(g.id);
    gateState.picked.delete(g.id);
    detailEnv.changed();
    return out || {};
  } catch (err) {
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
  "draft.close-silently": { label: () => t("Close without a message"), path: (d) => "/api/drafts/" + encodeURIComponent(d.id) + "/close-silently", body: (d) => ({ rev: d.rev }) },
  "draft.skip-workflow": { label: () => t("Don't nudge this conversation"), path: unitSkipPath, body: () => ({ source: "cockpit" }) },
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

// draftOffers are the buttons of the offered draft actions d serves, each off with its reason where refused.
function draftOffers(d, scope) {
  return (d.actions || []).filter((a) => DRAFT_OFFERS[a.id] && DRAFT_OFFERS[a.id].path(d)).map((a) => {
    const offer = DRAFT_OFFERS[a.id];
    const label = a.label || offer.label();
    return h("button", {
      type: "button", class: "act" + (a.enabled ? "" : " off"), disabled: !a.enabled, title: (a.enabled ? a.note : a.reason) || undefined,
      "data-focus": `${scope}:${a.id}:draft:${d.id}`, onclick: () => runDraftOffer(d, label, offer),
    }, label);
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

// draftEffects are the profile's sentences for what sending d does besides posting it.
function draftEffects(d) {
  const words = (typeof tasksState === "object" && tasksState.catalog && tasksState.catalog.effects) || {};
  return (d.effects || []).map((x) => words[x]).filter(Boolean);
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
  try {
    await detailEnv.post("/api/gates/" + encodeURIComponent(d.gate) + "/" + encodeURIComponent(intent), verbBody({ ...args, rev: String(d.rev) }));
    detailEnv.notice(`${label}: ${d.where || d.target || d.unit}`);
    detailState.editing.delete("draft:" + d.id);
    detailEnv.changed();
  } catch (err) {
    detailEnv.notice(t("{action} failed: {error}", { action: label, error: err.message }), true);
  }
}

// ownDraftButtons are the actions of the draft e, its own task, carries: approve, edit and discard through
// the task's verbs, retry for a draft whose delivery failed.
function ownDraftButtons(e, d, scope, editing) {
  const [approve, drop, edit, retry] = DRAFT_CARD_HANDLERS.map((x) => offeredVerb(e, x));
  if (editing) {
    return [
      verbButton(e, approve, gateWord(e, d, "approved", approve), () => approveEdited(e), { scope, primary: true }),
      h("button", { type: "button", class: "act", "data-focus": `${scope}:cancel:${e.id}`, onclick: () => { detailState.editing.delete(e.id); detailEnv.render(); } }, t("Cancel")),
    ];
  }
  return [
    d.state === "failed" ? verbButton(e, retry, draftWord("draft.retry"), () => taskAct(e, retry, { rev: String(d.rev) }), { scope, primary: true })
      : verbButton(e, approve, gateWord(e, d, "approved", approve), () => taskAct(e, approve, { rev: String(d.rev) }), { scope, primary: true }),
    verbButton(e, edit || approve, draftWord("draft.edit"), () => { detailState.editing.set(e.id, d.text || ""); detailEnv.render(); }, { scope: scope + ":open" }),
    verbButton(e, drop, gateWord(e, d, "rejected", drop), () => taskAct(e, drop, {}), { scope }),
  ];
}

// gateDraftButtons are the actions of a draft shown on another task: approve, edit and discard through its
// gate's intents, retry through its own task for a draft whose delivery failed.
function gateDraftButtons(e, d, scope, editing) {
  const key = "draft:" + d.id;
  const intents = (d.gate && d.intents) || {};
  const approve = verbFor("approve");
  const approveLabel = gateWord(e, d, "approved", approve);
  const button = (label, focus, run, primary) => h("button", { type: "button", class: "act" + (primary ? " primary" : ""), "data-focus": `${scope}:${focus}:${key}`, onclick: run }, label);
  if (editing) {
    return [
      intents.approved ? button(approveLabel, approve, () => resolveDraft(d, intents.approved, approveLabel, { text: detailState.editing.get(key) }), true) : null,
      button(t("Cancel"), "cancel", () => { detailState.editing.delete(key); detailEnv.render(); }, false),
    ];
  }
  const retry = verbFor("retry");
  const drop = verbFor("drop");
  return [
    d.state === "failed" ? button(draftWord("draft.retry"), retry, () => taskAct({ id: key, title: d.title || key }, retry, { rev: String(d.rev) }), true)
      : intents.approved ? button(approveLabel, approve, () => resolveDraft(d, intents.approved, approveLabel, {}), true) : null,
    intents.approved ? button(draftWord("draft.edit"), "open:" + approve, () => { detailState.editing.set(key, d.text || ""); detailEnv.render(); }, false) : null,
    intents.rejected ? button(gateWord(e, d, "rejected", drop), drop, () => resolveDraft(d, intents.rejected, gateWord(e, d, "rejected", drop), {}), false) : null,
  ];
}

// draftCard is draft d on task e: where it posts and in what language, its text as posted, why it was drafted,
// what sending it does, its window, its actions and links to its task and its unit. On d's own task its
// actions are the task's verbs, elsewhere its gate's intents.
function draftCard(e, d, opts = {}) {
  const scope = opts.scope || "detail";
  const own = !!e && isDraftTask(e) && draftOf(e).id === d.id;
  const key = own ? e.id : "draft:" + d.id;
  const editing = detailState.editing.has(key);
  const purpose = (d.purpose || "").replaceAll("_", " ").trim();
  const head = (purpose ? t("Draft {purpose}", { purpose }) : t("Draft")) + (d.state && d.state !== "held" ? ` (${d.state})` : "");
  const effects = draftEffects(d);
  return h("section", { class: "d-draft", "data-draft": String(d.id), "aria-label": t("Draft") },
    h("div", { class: "d-sub" }, head),
    draftWhere(d),
    editing ? h("textarea", { class: "draft-text", rows: 6, "aria-label": t("Draft text"), "data-focus": `${scope}:draft-text:${key}`, oninput: (ev) => detailState.editing.set(key, ev.target.value) }, detailState.editing.get(key))
      : d.text ? renderMarkdown(d.text, { fold: 6, key: `${scope}:draft:${d.id}` }) : h("p", { class: "draft-text" }, d.reaction ? `:${d.reaction}:` : ""),
    d.why ? h("p", { class: "why" }, d.why) : null,
    effects.length ? h("ul", { class: "effects" }, ...effects.map((x) => h("li", {}, x))) : null,
    d.error ? h("p", { class: "err" }, d.error) : null,
    windowLine(d) ? h("p", { class: "would" }, windowLine(d)) : null,
    h("div", { class: "d-actions" }, ...(own ? ownDraftButtons(e, d, scope, editing) : gateDraftButtons(e, d, scope, editing)), ...(editing ? [] : draftOffers(d, scope))),
    draftLinks(e, d, scope));
}
