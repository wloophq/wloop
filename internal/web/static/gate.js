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

// gateOption is the option picked on g's card: the person's pick, else the recommended one unless it pins a
// draft, which is sent only once the person picks it; "" for none.
function gateOption(g) {
  if (gateState.picked.has(g.id)) return gateState.picked.get(g.id);
  const subject = subjectOf(g);
  const recommended = (subject.options || []).find((o) => o.id === subject.recommend);
  return recommended && !recommended.draft ? recommended.id : "";
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
    detailEnv.changed();
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
  return h("div", { class: "opts" }, ...ordered.flatMap((o) => {
    const on = o.id === picked;
    return [h("button", {
      type: "button", class: "opt" + (on ? " on" : ""), "aria-pressed": String(on), "data-focus": `gate:${g.id}:option:${o.id}`,
      "data-pin": o.draft ? `${o.draft.id}@${o.draft.rev}` : undefined,
      onclick: () => {
        gateState.picked.set(g.id, on ? "" : o.id);
        detailEnv.render();
      },
    },
    h("span", { class: "r", "aria-hidden": "true" }),
    h("span", { class: "lb" }, o.label),
    o.id === subject.recommend ? h("span", { class: "rec" }, t("recommended")) : null,
    o.consequence ? h("span", { class: "cs" }, o.consequence) : null),
    o.draft ? pinnedDraft(subject, o.draft, `gate:${g.id}:option:${o.id}`) : null];
  }));
}

// pinnedDraft is the draft an answer approves, at the rev the question pinned in ref, as its own card shows it:
// its preview, its text and its whole payload, with a note when it changed since or is gone. The person sees
// what they approve, never only the agent's words for it.
function pinnedDraft(subject, ref, scope) {
  const d = (subject.pinned || []).find((x) => x.id === ref.id);
  if (!d) return h("div", { class: "d-pin" }, h("p", { class: "d-note err" }, t("Approves draft #{id}, which is gone.", { id: ref.id })));
  return h("div", { class: "d-pin", "data-draft": String(ref.id), "data-rev": String(ref.rev) },
    h("p", { class: "d-note" }, t("Approves draft #{id} at rev {rev}:", { id: ref.id, rev: ref.rev })),
    d.rev !== ref.rev ? h("p", { class: "d-note err" }, t("It changed since the question pinned it and is now at rev {rev}: below is what it says now.", { rev: d.rev })) : null,
    d.preview ? h("div", { class: "d-preview" }, renderMarkdown(d.preview, { key: `${scope}:pin-preview`, names: d.names || {} })) : null,
    draftWhere(d),
    d.text ? renderMarkdown(d.text, { fold: 6, key: `${scope}:pin`, names: d.names || {} }) : null,
    payloadView(d, `pin:${ref.id}`, false, scope));
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

// stepText is a plan step in the catalog's words the daemon names, filled with its arguments as a person reads
// them; the step's name when the catalog has no words for it.
function stepText(st) {
  if (!st.says) return st.use;
  const args = st.args || {};
  const prop = args.prop ? taskFacetOf("task.prop." + args.prop) : null;
  return t(st.says, { ...args, resolution: args.resolution_says ? t(args.resolution_says) : args.resolution || "",
    intent: args.intent_label || args.intent || "", task: args.task_title || "",
    verb: args.verb ? verbLabel(args.verb) : "", prop: prop ? prop.label : args.prop || "", value: propText(args.value, prop) });
}

// planPosts says whether applying a plan item may post where others see it, as the daemon says of each step.
function planPosts(does) {
  return does.some((st) => st.posts);
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

// gateAbout is a unit a gate is about: its title and reference, linked to it at its source when it has a link.
function gateAbout(u) {
  return h("p", { class: "meta gate-about" }, outLink(u.link, u.title ? `${u.title} · ${u.label}` : u.label));
}

// gateSays is what each of a gate's intents does, as the profile words it; null when none says.
function gateSays(intents) {
  const said = intents.filter((x) => x.summary);
  return said.length ? h("ul", { class: "meta gate-says" }, ...said.map((x) => h("li", {}, h("b", {}, x.label), ": ", x.summary))) : null;
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
  const buttons = intents.map((x, i) => h("button", {
    type: "button", class: "act" + (i === 0 ? " primary" : ""), "data-focus": `gate:${g.id}:${x.id}`, "aria-keyshortcuts": i < 9 ? String(i + 1) : undefined,
    onclick: () => resolveGate(e, g, x.id),
  }, i < 9 ? h("kbd", {}, String(i + 1)) : null, " ", x.label));
  return h("article", { class: "ask d-gate", "data-gate": String(g.id), "data-kind": g.kind, "aria-label": title },
    h("header", { class: "hd" },
      h("i", { class: "dot", "aria-hidden": "true" }),
      h("h3", { class: "d-sub" }, title),
      subject.created_at ? h("time", { datetime: subject.created_at }, fmtDate(subject.created_at, "datetime")) : null),
    h("div", { class: "bd" },
      subject.text ? renderMarkdown(subject.text, { fold: 4, key: `${scope}:${g.id}` }) : null,
      gatePlan(g),
      ...(g.units || []).map(gateAbout),
      gateSays(intents),
      g.delivery ? h("p", { class: "meta" }, g.delivery) : null,
      g.deadline ? h("p", { class: "meta" }, deflt ? t("{action} at {time} unless you pick another", { action: deflt.label, time: fmtDate(g.deadline, "datetime") })
        : t("Until {time}", { time: fmtDate(g.deadline, "datetime") })) : null,
      gateOptions(g, answer),
      subject.draft ? pinnedDraft(subject, subject.draft, `gate:${g.id}:confirm`) : null,
      answer ? gateAnswer(e, g, answer) : null,
      h("div", { class: "d-actions" }, ...buttons,
      answer ? h("span", { class: "keyhint" }, h("kbd", {}, "⌘↵"), " ", answer.label) : null),
      actionResult(buttons)));
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

// payloadView is every value d's payload posts, under its field's word, as the daemon serves them: a worded value
// keeps its raw one in its title. While d is being edited under key, each editable field is a control.
function payloadView(d, key, editing, scope) {
  const fields = d.fields || [];
  return fields.length ? h("dl", { class: "d-payload" }, ...fields.flatMap((f) => payloadField(f, key, editing, scope))) : null;
}

function payloadField(f, key, editing, scope) {
  let value;
  if ((f.fields || []).length) value = h("dl", { class: "d-payload" }, ...f.fields.flatMap((x) => payloadField(x, key, editing, scope)));
  else if (editing && f.editable && (f.options || []).length) value = fieldControl(f, key, scope);
  else value = h("span", { class: "pv", "data-value": f.value, title: f.shown && f.shown !== f.value ? f.value : undefined }, f.shown || f.value);
  return [h("dt", {}, f.label), h("dd", { "data-pointer": f.pointer }, value)];
}

// fieldControl changes editable field f of the draft edited under key: a select over its options, else a line.
function fieldControl(f, key, scope) {
  const edits = detailState.payloads.get(key) || {};
  const current = f.pointer in edits ? edits[f.pointer] : f.value;
  const set = (ev) => { detailState.payloads.set(key, { ...(detailState.payloads.get(key) || {}), [f.pointer]: ev.target.value }); };
  const attrs = { class: "d-field", "aria-label": f.label, "data-focus": `${scope}:field:${key}:${f.pointer}` };
  return h("select", { ...attrs, onchange: set }, ...f.options.map((o) => h("option", { value: o.value, selected: o.value === current }, o.label)));
}

// editableFields says whether d has a payload field a person may change.
function editableFields(fields) {
  return (fields || []).some((f) => (f.editable && (f.options || []).length) || editableFields(f.fields));
}

// draftEditable says whether a person may change anything of d: its text, unless its kind locks it, or a field.
function draftEditable(d) {
  return !d.text_locked || editableFields(d.fields);
}

// editedText is the text arg of an approval of d edited under key; none when its kind locks its text.
function editedText(d, key) {
  return d.text_locked ? {} : { text: detailState.editing.get(key) };
}

// editConflict is what the card of d, edited under key since an older rev, shows: that it changed meanwhile, who
// wrote it and its current text, so an approval never replaces a revision the person did not see. The edits of
// its fields are dropped, since they were made over values that may have changed.
function editConflict(d, key) {
  const since = detailState.editRevs.get(key);
  if (since === undefined || since === d.rev) return null;
  detailState.payloads.delete(key);
  return h("div", { class: "d-conflict" },
    h("p", { class: "d-note err" }, t("This draft changed since you began editing it; your edit replaces what it says now:"), d.author ? " " + d.author : ""),
    d.text ? renderMarkdown(d.text, { key: `conflict:${key}`, names: d.names || {} }) : null);
}

// editedPayload is the payload arg of an approval of d edited under key: d's payload with the fields changed,
// none when nothing changed.
function editedPayload(d, key) {
  const edits = Object.entries(detailState.payloads.get(key) || {});
  if (!edits.length || !d.payload) return {};
  const out = JSON.parse(JSON.stringify(d.payload));
  for (const [pointer, v] of edits) setPointer(out, pointer, v);
  return { payload: JSON.stringify(out) };
}

// setPointer sets the value at JSON pointer in doc.
function setPointer(doc, pointer, v) {
  const path = pointer.split("/").slice(1).map((p) => p.replaceAll("~1", "/").replaceAll("~0", "~"));
  const last = path.pop();
  path.reduce((at, p) => at[p], doc)[last] = v;
}

// stopEditing forgets the edits of the draft edited under key.
function stopEditing(key) {
  detailState.editing.delete(key);
  detailState.payloads.delete(key);
  detailState.editRevs.delete(key);
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
      onchange: (ev) => taskAct({ id, title: d.title || id }, verbFor("edit"), { broadcast: String(!!ev.target.checked), rev: String(d.rev) }) }),
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
    stopEditing("draft:" + d.id);
    detailEnv.changed();
  } catch (err) {
    markResolving(card, false);
    detailEnv.notice(t("{action} failed: {error}", { action: label, error: err.message }), true);
    detailEnv.changed();
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

const editDraft = (key, d) => () => {
  detailState.editing.set(key, d.text || "");
  detailState.payloads.delete(key);
  detailState.editRevs.set(key, d.rev);
  detailEnv.render();
};
const cancelEdit = (key) => ({ label: t("Cancel"), enabled: true, focus: "cancel:" + key, run: () => { stopEditing(key); detailEnv.render(); } });

// staleOff turns action a off on stale draft d, with the reason the daemon worded.
const staleOff = (d, a) => (a && d.stale ? { ...a, enabled: false, reason: (d.notes || [])[0], title: (d.notes || [])[0] } : a);

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
  if (editing) return [staleOff(d, verb(approve, gateWord(e, d, "approved", approve), () => approveEdited(e), primary)), cancelEdit(key)];
  return [
    staleOff(d, d.state === "failed" ? verb(retry, draftWord("draft.retry"), () => taskAct(e, retry, { rev: String(d.rev) }), primary)
      : verb(approve, gateWord(e, d, "approved", approve), () => taskAct(e, approve, { rev: String(d.rev) }), primary)),
    draftEditable(d) ? verb(edit || approve, draftWord("draft.edit"), editDraft(key, d), { key: "queue:edit", focus: `open:${edit || approve}:${e.id}` }) : null,
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
    return [intents.approved ? staleOff(d, act(approveLabel, approve, () => resolveDraft(d, intents.approved, approveLabel,
      { ...editedText(d, key), ...editedPayload(d, key) }), primary)) : null,
    cancelEdit(key)];
  }
  const retry = verbFor("retry");
  const drop = verbFor("drop");
  const dropLabel = gateWord(e, d, "rejected", drop);
  return [
    staleOff(d, d.state === "failed" ? act(draftWord("draft.retry"), retry, () => taskAct({ id: key, title: d.title || key }, retry, { rev: String(d.rev) }), primary)
      : intents.approved ? act(approveLabel, approve, () => resolveDraft(d, intents.approved, approveLabel, {}), primary) : null),
    intents.approved && draftEditable(d) ? act(draftWord("draft.edit"), "open:" + approve, editDraft(key, d), { key: "queue:edit" }) : null,
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
  const buttons = draftActions(e, d).map((a) => draftActionButton(a, scope, opts.keyOf));
  return h("section", { class: "d-draft", "data-draft": String(d.id), "aria-label": t("Draft") },
    h("div", { class: "d-sub" }, head),
    d.preview ? h("div", { class: "d-preview" }, renderMarkdown(d.preview, { key: `${scope}:preview:${d.id}`, names: d.names || {} })) : null,
    opts.context ? draftContextView(opts.context) : null,
    draftWhere(d),
    d.author ? h("div", { class: "meta d-by" }, d.author) : null,
    broadcastToggle(d, scope),
    editing ? editConflict(d, key) : null,
    editing && !d.text_locked ? h("textarea", { class: "draft-text", rows: 6, "aria-label": t("Draft text"), "data-focus": `${scope}:draft-text:${key}`, oninput: (ev) => detailState.editing.set(key, ev.target.value) }, detailState.editing.get(key))
      : d.text ? renderMarkdown(d.text, { fold: 6, key: `${scope}:draft:${d.id}`, names: d.names || {} }) : h("p", { class: "draft-text" }, d.reaction ? `:${d.reaction}:` : ""),
    payloadView(d, key, editing, scope),
    ...(d.notes || []).map((n) => h("p", { class: "d-note" + (d.stale ? " err" : "") }, n)),
    d.why ? h("p", { class: "why" }, d.why) : null,
    d.quoted ? h("p", { class: "why" }, d.quoted) : null,
    effects.length ? h("ul", { class: "effects" }, ...effects.map((x) => h("li", {}, x))) : null,
    d.error ? h("p", { class: "err" }, d.error) : null,
    windowLine(d) ? h("p", { class: "would" }, windowLine(d)) : null,
    h("div", { class: "d-actions" }, ...buttons),
    actionResult(buttons),
    draftLinks(e, d, scope));
}
