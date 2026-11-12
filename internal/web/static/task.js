"use strict";

// taskState is what the task page holds beside the task: its timeline's facts, the kinds it is narrowed to and every
// kind seen, the composer's text and whether its reply also goes to the conversation (null: as served), a run's
// transcript, the drafts that wait on it and the tab picked.
const taskState = { id: "", timeline: null, timelineError: "", kinds: [], kindsSeen: [], text: "", broadcast: null, sending: false, transcript: null, drafts: [], tab: "" };

function resetTaskPage(id) {
  Object.assign(taskState, { id, timeline: null, timelineError: "", kinds: [], kindsSeen: [], text: "", broadcast: null, transcript: null, drafts: [] });
  resetGates();
}

function taskCatalog() {
  return (typeof tasksState === "object" && tasksState.catalog) || {};
}

function stateLabel(name) {
  return ((taskCatalog().states || []).find((s) => s.name === name) || {}).label || name;
}

// timelineURL is task id's timeline folded into facts, narrowed to kinds when any.
function timelineURL(id, kinds) {
  const q = new URLSearchParams({ as: "nodes" });
  if (kinds.length) q.set("kinds", kinds.join(","));
  return taskPath(id, "timeline") + "?" + q.toString();
}

// loadTaskPage reads the timeline of e, narrowed to the kinds picked, and the drafts that wait on e.
async function loadTaskPage(e) {
  if (taskState.id !== e.id) resetTaskPage(e.id);
  await Promise.all([loadTimeline(e), loadTaskDrafts(e)]);
}

async function loadTimeline(e) {
  try {
    const nodes = await detailEnv.getJSON(timelineURL(e.id, taskState.kinds));
    if (taskState.id !== e.id) return;
    taskState.timeline = nodes || [];
    taskState.timelineError = "";
    for (const n of taskState.timeline) for (const ev of n.events || []) if (!taskState.kindsSeen.includes(ev.kind)) taskState.kindsSeen.push(ev.kind);
  } catch (err) {
    if (taskState.id === e.id) taskState.timelineError = t("Could not load the timeline: {error}", { error: err.message });
  }
}

// loadTaskDrafts reads the drafts e's pending draft ids name; none when it names none or they cannot be read.
async function loadTaskDrafts(e) {
  if (!(e.drafts || []).length) {
    taskState.drafts = [];
    return;
  }
  const out = await detailEnv.getJSON("/api/drafts?" + new URLSearchParams({ task: e.id }).toString()).catch(() => null);
  if (taskState.id === e.id) taskState.drafts = (out && out.drafts) || [];
}

// propText is a property's value as a person reads it: a name, or the word facet f gives the value.
function propText(v, f) {
  if (v === null || v === undefined) return "";
  if (Array.isArray(v)) return v.map((x) => propText(x, f)).filter(Boolean).join(", ");
  if (typeof v === "object") return v.name || v.key || "";
  const labels = (f && f.labels) || {};
  return Object.hasOwn(labels, String(v)) ? labels[String(v)] : String(v);
}

// taskFacetOf is the profile's task facet that reads fact, null when none does.
function taskFacetOf(fact) {
  return (taskCatalog().facets || []).find((f) => f.fact === fact && !f.provider) || null;
}

// LISTED_ROLES are the roles of the properties the page lists as tasks, not as text.
const LISTED_ROLES = ["related", "merged"];

function briefData(e) {
  const r = e.brief;
  return r && r.data && typeof r.data === "object" ? r.data : {};
}

// tagMax is how many brief tags a row shows before it counts the rest, as the profile's tag facet says.
function tagMax() {
  const spec = (taskCatalog().facets || []).find((f) => f.fact === "brief.tags");
  return (spec && spec.show_max) || 0;
}

// moreWord is the lens's word for n more pieces.
function moreWord(n) {
  const w = typeof lensWords === "function" ? lensWords().more : "";
  return w && typeof fillWord === "function" ? fillWord(w, { n: fmtNumber(n) }) : "+" + fmtNumber(n);
}

// taskProps is the task's facts as a label and value grid: the properties the profile declares, in its order,
// the brief's risk with its reason and its size, each card's signals in their tone, then the brief's tags, the
// first the tag facet shows and the count of the rest, which opens the Brief tab.
function taskProps(e) {
  const props = e.props || {};
  const rows = (taskCatalog().props || []).filter((p) => !LISTED_ROLES.includes(p.role) && propText(props[p.name]))
    .map((p) => {
      const f = taskFacetOf("task.prop." + p.name);
      return [f ? f.label : p.name, propText(props[p.name], f), { "data-prop": p.name }];
    });
  const b = briefData(e);
  const risk = taskFacetOf("brief.risk");
  const size = taskFacetOf("brief.size");
  if (b.risk) rows.push([risk ? risk.label : t("Risk"), [h("span", { class: "risk-" + b.risk }, propText(b.risk, risk)), b.risk_why ? " · " + b.risk_why : null], { "data-prop": "brief:risk" }]);
  if (b.size) rows.push([size ? size.label : t("Size"), propText(b.size, size), { "data-prop": "brief:size" }]);
  for (const c of e.cards || []) {
    for (const g of (c.card || {}).signals || []) {
      const w = signalWords(((c.card || {}).unit || {}).provider, g, (at) => fmtDate(at, "datetime"));
      rows.push([w.label, w.value, { class: "tone-" + (g.tone || "neutral") }]);
    }
  }
  const tags = b.tags || [];
  const max = tagMax() || tags.length;
  if (tags.length) {
    rows.push([t("Tags"), [...tags.slice(0, max).map((x) => h("span", { class: "pill" }, x)),
      tags.length > max ? h("button", { type: "button", class: "more", "data-focus": "task:props:tags", onclick: () => pickTab("brief") }, moreWord(tags.length - max)) : null], {}]);
  }
  if (!rows.length) return null;
  return h("dl", { class: "props d-props" }, ...rows.flatMap(([k, v, at]) => [h("dt", {}, k), h("dd", at, v)]));
}

// taskLinks are the cards of the task's units that have one; the head names every unit.
function taskLinks(e) {
  const cards = (e.units || []).map((u) => cardOf(e, u)).filter(Boolean);
  return cards.length ? h("div", { class: "d-links" }, ...cards.map((c) => unitCard(c, { signals: false }))) : null;
}

function cardOf(e, u) {
  return (e.cards || []).find((x) => x.card && x.card.unit && x.card.unit.provider === u.provider && x.card.unit.key === u.key) || null;
}

function unitLabels(e) {
  return (e.units || []).map((u, i) => (e.unit_labels || [])[i] || unitLabel(u));
}

// taskWhen is whom the task waits on and since when, as its row says it.
function taskWhen(e) {
  if (typeof rowParts !== "function" || typeof rowWhen !== "function") return null;
  const lens = { static: true, ts: [], quals: [], specs: taskCatalog().facets || [], words: typeof lensWords === "function" ? lensWords() : {} };
  const m = { scope: "detail", words: lens.words, parts: rowParts({ facets: e.facets || {} }, lens.specs, lens.words) };
  if (!(m.parts.who || []).length && !(m.parts.age || []).length) return null;
  return h("div", { class: "d-when" }, rowWhen(m, lens));
}

// taskHead is the task's units, each opening in the cockpit and at its source, its title, state, whom it waits on
// and since when, why, and its notes.
function taskHead(e) {
  const labels = unitLabels(e);
  return h("header", { class: "d-head" },
    h("button", { type: "button", class: "close", "aria-label": t("Close detail"), "data-focus": "detail:close", onclick: () => detailEnv.close() }, "×"),
    detailWindowControls(),
    labels.length ? h("div", { class: "d-units meta" }, ...(e.units || []).map((u, i) => h("span", { class: "d-unit" }, unitRef(u, labels[i], (cardOf(e, u) || { card: {} }).card.link || "")))) : null,
    h("h1", {}, starred(e) ? h("span", { class: "star" }, "★ ") : null, titleWithoutRef(e.title || e.id, labels)),
    h("div", { class: "meta" },
      h("span", { class: "pill state", "data-category": e.category || "" }, stateLabel(e.state)),
      e.changed ? [" ", h("span", { class: "flag" }, t("changed"))] : null,
      e.owed_by ? [" ", t("owed by"), " ", h("strong", {}, e.owed_by)] : null,
      e.wake && e.wake.until ? [" ", t("wakes {time}", { time: fmtDate(e.wake.until, "datetime") })] : null,
      draftMark(e) ? [" ", draftMark(e)] : null),
    taskWhen(e),
    e.reason ? h("p", { class: "why" }, e.reason) : null,
    e.notes ? h("p", { class: "d-notes" }, e.notes) : null);
}

function taskAsks(e) {
  const asks = e.asks || [];
  if (!asks.length) return null;
  return h("section", { class: "d-asks", "aria-label": t("Asks") },
    h("ul", { class: "asks" }, ...asks.map((a) => h("li", { class: "ask-item" },
      h("div", {}, a.asker ? h("strong", {}, a.asker + ": ") : null, a.summary,
        safeURL(a.permalink) ? [" ", h("a", { href: safeURL(a.permalink), target: "_blank", rel: "noopener noreferrer", class: "ext" }, t("source"))] : null),
      ...(a.quotes || []).map((q) => h("blockquote", { class: "quote" }, q))))));
}

function taskBrief(e) {
  const r = e.brief;
  if (!r) return null;
  const b = briefData(e);
  const known = b.text || b.goal || b.context || b.risk || (b.open_questions || []).length || (b.tags || []).length;
  return h("section", { class: "d-brief", "aria-label": t("Brief") },
    b.goal ? h("p", { class: "goal" }, b.goal) : null,
    b.text ? renderMarkdown(b.text, { fold: 8, key: "brief:" + e.id }) : null,
    b.context ? h("p", {}, b.context) : null,
    b.risk ? h("p", { class: "risk-" + b.risk }, t("Risk: {risk}", { risk: propText(b.risk, taskFacetOf("brief.risk")) }), b.risk_why ? ` (${b.risk_why})` : "") : null,
    (b.open_questions || []).length ? h("ul", {}, ...b.open_questions.map((q) => h("li", {}, q))) : null,
    (b.tags || []).length ? h("div", { class: "d-brief-tags" }, ...b.tags.map((x) => h("span", { class: "pill" }, x))) : null,
    known ? null : h("pre", { class: "draft-text" }, JSON.stringify(r.data, null, 2)),
    r.at ? h("div", { class: "meta" }, h("time", { datetime: r.at }, fmtDate(r.at, "datetime"))) : null);
}

// taskGates are the decisions that wait on the task: its open gates, the draft it carries as a draft's own task,
// and the drafts that wait on it, each a card.
// taskGates are the decisions that wait on task e: its open gates and its drafts, each once. A draft an open
// question pins is that question's to decide: it shows under the option that pins it, never as a card of its own.
function taskGates(e) {
  const own = isDraftTask(e) ? draftOf(e) : null;
  const drafts = (taskState.id === e.id ? taskState.drafts : []).filter((d) => (!own || d.id !== own.id) && !d.pinned_by);
  const pinnedGate = (g) => g.kind === "draft" && !!subjectOf(g).pinned_by;
  const gates = (e.gates || []).filter((g) => g.status === "open" && !(own && g.subject === own) && !pinnedGate(g) && !drafts.some((d) => d.gate && d.gate === g.id));
  if (!gates.length && !own && !drafts.length) return null;
  return h("section", { class: "d-gates", "aria-label": t("Open gates") },
    h("h2", { class: "d-sub" }, t("Waiting on you")),
    ...gates.map((g) => gateCard(e, g)),
    own && own.pinned_by ? pinnedNote(own) : own ? draftCard(e, own, { scope: "detail" }) : null,
    ...drafts.map((d) => draftCard(e, d, { scope: "detail" })));
}

// pinnedNote stands for draft d's card on its own task while an open question pins it: the question decides it,
// so the note sends the person to that question's task.
function pinnedNote(d) {
  return h("section", { class: "d-draft", "data-draft": String(d.id) },
    h("p", { class: "d-note" }, t("An open question decides this draft: answer it on its task.")),
    d.task ? h("a", { href: taskHref(d.task), class: "ref", "data-focus": `detail:task:draft:${d.id}` }, t("Open its task")) : null);
}

const RUN_PATHS = { open: "/api/runs/{seq}/open", enter: "/api/runs/{seq}/enter", transcript: "/api/runs/{seq}/transcript" };

function runPath(action, seq) {
  return RUN_PATHS[action].replace("{seq}", encodeURIComponent(seq));
}

async function runAct(r, action, label) {
  try {
    await detailEnv.post(runPath(action, r.seq), { source: "cockpit" });
    detailEnv.notice(`${label}: ${r.intent}`);
  } catch (err) {
    detailEnv.notice(t("{action} failed: {error}", { action: label, error: err.message }), true);
  }
}

async function runTranscript(r) {
  try {
    const out = await detailEnv.getJSON(runPath("transcript", r.seq));
    taskState.transcript = { seq: r.seq, lines: transcriptLines(out) };
  } catch (err) {
    taskState.transcript = { seq: r.seq, error: t("Could not load the transcript: {error}", { error: err.message }) };
  }
  detailEnv.render();
}

function transcriptLines(out) {
  const list = Array.isArray(out) ? out : (out && (out.lines || out.turns || out.messages)) || [];
  return list.map((x) => (typeof x === "string" ? x : [x.role || x.actor || "", x.text || ""].filter(Boolean).join(": ")));
}

// runStateWord is state in the words the catalog serves from the profile's table, else as the daemon names it.
function runStateWord(table, state) {
  const words = taskCatalog()[table] || {};
  return Object.hasOwn(words, state) ? words[state] : state;
}

// runCard is a run with where it ran and its session; its actions are the
// ones the run offers.
function runCard(r) {
  const offers = new Set(r.actions || []);
  const button = (action, label) => h("button", {
    type: "button", class: "act" + (offers.has(action) ? "" : " off"), disabled: !offers.has(action), "data-focus": `run:${r.seq}:${action}`,
    onclick: () => (action === "transcript" ? runTranscript(r) : runAct(r, action, label)),
  }, label);
  const shown = taskState.transcript && taskState.transcript.seq === r.seq ? taskState.transcript : null;
  return h("article", { class: "d-run", "data-run": String(r.seq) },
    h("div", { class: "meta" }, h("strong", {}, r.intent), " ", h("span", { class: "pill" }, runStateWord("run_states", r.state)),
      r.manager ? [" ", t("in {manager}", { manager: r.manager })] : null,
      r.session ? [" ", t("session"), " ", h("code", {}, r.session), r.session_state ? ` (${runStateWord("session_states", r.session_state)})` : ""] : null),
    r.worktree || r.workspace ? h("div", { class: "meta" }, h("code", {}, r.worktree || r.workspace)) : null,
    r.resume ? h("div", { class: "meta" }, t("Resume:"), " ", h("code", {}, r.resume)) : null,
    h("div", { class: "d-actions" }, button("open", t("Open workspace")), button("enter", t("Enter session")), button("transcript", t("Transcript"))),
    shown ? (shown.error ? h("p", { class: "plan-error" }, shown.error) : h("ol", { class: "transcript" }, ...shown.lines.map((l, i) => h("li", {}, renderMarkdown(l, { fold: 12, key: `run:${r.seq}:${i}` }))))) : null);
}

function taskRuns(e) {
  const runs = e.runs || [];
  if (!runs.length) return null;
  return h("section", { class: "d-runs", "aria-label": t("Runs") }, ...runs.map(runCard));
}

async function pickKinds(e, kinds) {
  taskState.kinds = kinds;
  await loadTaskPage(e);
  detailEnv.render();
}

// taskTimeline is the task's timeline folded into facts, newest first, narrowed to the kinds picked.
function taskTimeline(e) {
  const kinds = taskState.kinds;
  const chip = (kind) => {
    const on = kinds.includes(kind);
    return h("button", { type: "button", class: "chip" + (on ? " on" : ""), "aria-pressed": String(on), "data-focus": "timeline:kind:" + kind,
      onclick: () => pickKinds(e, on ? kinds.filter((k) => k !== kind) : [...kinds, kind]) }, eventLabel(kind));
  };
  const nodes = taskState.timeline || [];
  return h("section", { class: "d-timeline", "aria-label": t("Timeline") },
    taskState.kindsSeen.length ? h("div", { class: "chips kinds", role: "group", "aria-label": t("Kinds") },
      h("button", { type: "button", class: "chip" + (kinds.length ? "" : " on"), "aria-pressed": String(!kinds.length), "data-focus": "timeline:kind:", onclick: () => pickKinds(e, []) }, t("all")),
      ...taskState.kindsSeen.map(chip)) : null,
    taskState.timelineError ? h("p", { class: "plan-error" }, taskState.timelineError) : null,
    !taskState.timeline && !taskState.timelineError ? h("p", { class: "empty" }, t("Loading the timeline.")) : null,
    taskState.timeline && !nodes.length ? h("p", { class: "empty" }, t("Nothing here yet.")) : null,
    nodes.length ? timelineNodes(nodes) : null);
}

// timelineCount is how many events the timeline's facts fold.
function timelineCount() {
  return (taskState.timeline || []).reduce((n, node) => n + (node.events || []).length, 0);
}

// sendChat writes the composer's text to the task's agent.
async function sendChat(e) {
  if (taskState.sending) return null;
  const text = taskState.text.trim();
  if (!text) {
    detailEnv.notice(t("Write a message first"), true);
    return null;
  }
  taskState.sending = true;
  detailEnv.notice(t("Sending to {task}…", { task: e.title || e.id }));
  detailEnv.render();
  try {
    const out = await detailEnv.post(taskPath(e.id, "chat"), { text });
    taskState.text = "";
    detailEnv.notice(t("Sent to {task}", { task: e.title || e.id }));
    await loadTaskPage(e);
    detailEnv.render();
    return out || {};
  } catch (err) {
    detailEnv.notice(t("Sending failed: {error}", { error: err.message }), true);
    return null;
  } finally {
    taskState.sending = false;
    detailEnv.render();
  }
}

// replyWith writes the composer's text as a reply in the task's conversation,
// through the verb whose handler replies: send "now" posts it, "draft" holds
// it for approval.
async function replyWith(e, verb, send) {
  const text = taskState.text.trim();
  if (!text) {
    detailEnv.notice(t("Write a message first"), true);
    return null;
  }
  const args = { text, send };
  if (e.reply_broadcast) args.broadcast = String(replyBroadcasts(e));
  const out = await taskAct(e, verb, args);
  if (out) {
    taskState.text = "";
    taskState.broadcast = null;
    detailEnv.render();
  }
  return out;
}

// replyBroadcasts says whether e's reply also goes to the conversation: as the composer's checkbox was turned, else
// as served.
function replyBroadcasts(e) {
  return taskState.broadcast === null ? e.reply_broadcast === "on" : taskState.broadcast;
}

// replyBroadcastToggle is the checkbox that also posts the composer's reply to the conversation, offered when the
// daemon serves e.reply_broadcast: the reply goes in a thread that can.
function replyBroadcastToggle(e) {
  if (!e.reply_broadcast) return null;
  return h("label", { class: "meta broadcast" },
    h("input", { type: "checkbox", checked: replyBroadcasts(e), "data-focus": "task:compose:broadcast",
      onchange: (ev) => { taskState.broadcast = !!ev.target.checked; detailEnv.render(); } }),
    " ", t("Also send to the conversation"));
}

// taskComposer writes to the task's agent: Send, the default that Ctrl+Enter
// runs, posts to its chat. Send reply and Save as draft write the text as a
// reply, the one the reply verb's served send names first and marked default.
function taskComposer(e) {
  const verb = handlerVerb("reply");
  const reply = verb ? verbState(e, verb) : null;
  const on = !!(reply && reply.enabled);
  const replyButton = (label, send, focus, first) => h("button", { type: "button", class: "act" + (first ? " default" : "") + (on ? "" : " off"), disabled: !on,
    title: reply ? (on ? verbTask(verb).summary : reply.reason) || undefined : t("Not offered here"),
    "data-focus": focus, onclick: () => replyWith(e, verb, send) }, label);
  const now = verbTask(verb).send === "now";
  const replies = [replyButton(t("Send reply"), "now", "task:compose:reply", now), replyButton(t("Save as draft"), "draft", "task:compose:draft", !now)];
  if (!now) replies.reverse();
  return h("section", { class: "compose d-compose" + (taskState.sending ? " resolving" : ""), "aria-label": t("Message"), "aria-busy": String(!!taskState.sending) },
    h("textarea", { rows: 3, "aria-label": t("Message"), placeholder: t("Write to the agent"), "data-focus": "task:compose:text",
      oninput: (ev) => { taskState.text = ev.target.value; },
      onkeydown: (ev) => {
        if (ev.key !== "Enter" || !(ev.ctrlKey || ev.metaKey)) return;
        if (ev.preventDefault) ev.preventDefault();
        sendChat(e);
      } }, taskState.text),
    replyBroadcastToggle(e),
    h("div", { class: "d-actions" },
      h("button", { type: "button", class: "act primary", "data-focus": "task:compose:send", onclick: () => sendChat(e) }, t("Send")),
      ...replies));
}

function taskChildren(e) {
  const kids = e.children || [];
  if (!kids.length) return null;
  return h("section", { class: "d-children", "aria-label": t("Children") },
    h("h2", { class: "d-sub" }, t("Children")),
    h("ol", { class: "d-tasks" }, ...kids.map((id) => {
      const c = taskById(id);
      return h("li", {}, h("a", { href: "#" + viewHash({ task: id, pr: "" }), "data-focus": "child:" + id }, (c && c.title) || id),
        c ? [" ", h("span", { class: "pill" }, stateLabel(c.state))] : null);
    })));
}

// relatedVerbs are the buttons of the verbs e offers that take another task in: merge and relate.
function relatedVerbs(e) {
  const verbs = ["merge", "relate"].map((x) => offeredVerb(e, x)).filter(Boolean);
  if (!verbs.length) return null;
  return h("div", { class: "d-actions" }, ...verbs.map((v) => verbButton(e, v, verbLabel(v), () => pickTask(e, v), { scope: "related" })));
}

// TASK_TABS are the task page's tabs in their order: each one's label, whether e has anything to show in it,
// the count its label carries, and its body. Timeline always shows.
const TASK_TABS = [
  { id: "brief", label: () => t("Brief"), shows: (e) => !!e.brief, count: () => 0, body: (e) => [taskBrief(e)] },
  { id: "timeline", label: () => t("Timeline"), shows: () => true, count: timelineCount, body: (e) => [taskTimeline(e)] },
  { id: "asks", label: () => t("Asks"), shows: (e) => (e.asks || []).length > 0, count: (e) => (e.asks || []).length, body: (e) => [taskAsks(e)] },
  { id: "related", label: () => t("Related"), shows: (e) => relatedCount(e) > 0, count: relatedCount, body: (e) => [taskRelated(e), taskChildren(e), taskMerged(e), relatedVerbs(e)] },
  { id: "runs", label: () => t("Runs"), shows: (e) => (e.runs || []).length > 0, count: (e) => (e.runs || []).length, body: (e) => [taskRuns(e)] },
];

function relatedCount(e) {
  return (e.related || []).length + (e.children || []).length + (e.merged_from || []).length;
}

// shownTabs are the tabs e has something in, in their order.
function shownTabs(e) {
  return TASK_TABS.filter((x) => x.shows(e));
}

// selectedTab is the tab picked when e shows it, else the first e shows.
function selectedTab(e) {
  const shown = shownTabs(e);
  return (shown.find((x) => x.id === taskState.tab) || shown[0]).id;
}

// pickTab shows tab id; focus moves the focus to its tab, as the arrow keys do.
function pickTab(id, focus) {
  taskState.tab = id;
  detailEnv.render();
  if (!focus || typeof document === "undefined" || !document.querySelector) return;
  const el = document.querySelector(`[data-focus="task:tab:${id}"]`);
  if (el) el.focus();
}

// stepTab shows the tab delta tabs from the one shown on e, wrapping around; false when e has one tab.
function stepTab(e, delta, focus) {
  if (!e) return false;
  const shown = shownTabs(e);
  if (shown.length < 2) return false;
  const at = shown.findIndex((x) => x.id === selectedTab(e));
  pickTab(shown[(at + delta + shown.length) % shown.length].id, focus);
  return true;
}

const TAB_KEYS = { ArrowRight: 1, ArrowLeft: -1 };

// taskTabs are e's tabs with something in them, the one selected and the tab strip and panel; the strip is a
// tablist the arrow keys move along.
function taskTabs(e) {
  const shown = shownTabs(e);
  const selected = selectedTab(e);
  const tab = shown.find((x) => x.id === selected);
  const steps = [boundKey("tab:prev"), boundKey("tab:next")];
  const hint = shown.length > 1 && steps.every(Boolean);
  const strip = h("div", {
    class: "tabs", role: "tablist", "aria-label": t("Sections"), "aria-keyshortcuts": hint ? steps.map(ariaKey).join(" ") : undefined,
    onkeydown: (ev) => {
      if (!TAB_KEYS[ev.key]) return;
      if (ev.preventDefault) ev.preventDefault();
      stepTab(e, TAB_KEYS[ev.key], true);
    },
  }, ...shown.map((x) => {
    const on = x.id === selected;
    const n = x.count(e);
    return h("button", {
      type: "button", role: "tab", class: "tab" + (on ? " on" : ""), id: "task-tab-" + x.id, "aria-selected": String(on), "aria-controls": "task-tabpanel",
      tabindex: on ? 0 : -1, "data-focus": "task:tab:" + x.id, onclick: () => pickTab(x.id),
    }, x.label(), n ? [" ", h("span", { class: "n" }, fmtNumber(n))] : null);
  }), hint ? h("span", { class: "keyhint", "aria-hidden": "true" }, h("kbd", {}, steps[0]), " ", h("kbd", {}, steps[1])) : null);
  const body = h("div", { class: "d-tabs" }, strip,
    h("div", { class: "tabpanel", role: "tabpanel", id: "task-tabpanel", "aria-labelledby": "task-tab-" + selected }, tab.body(e)));
  return { tabs: shown.map((x) => ({ id: x.id, label: x.label(), count: x.count(e) })), selected, body };
}

// unitActionButtons are the actions the task's units offer on a task's page, each run on its unit.
function unitActionButtons(e) {
  if (typeof actionButtons !== "function") return [];
  return (e.unit_actions || []).flatMap((u) => actionButtons(u, "task", { scope: "task:" + keyOf(u) }));
}

// taskVerbs are the profile's primary verbs the task offers, More over the rest, the intents that serve it and
// the actions its units offer on it.
function taskVerbs(e) {
  const buttons = [...primaryVerbs(e), ...taskIntentButtons(e), ...unitActionButtons(e)].filter(Boolean);
  return buttons.length ? [h("div", { class: "verbs d-actions" }, ...buttons), actionResult(buttons)] : null;
}

// taskPage is a task's page: its head, facts, units, the decisions that wait on it, its verbs, its tabs and the
// composer.
function taskPage(e) {
  return [taskHead(e), taskProps(e), taskLinks(e), taskGates(e), taskVerbs(e), taskTabs(e).body, taskComposer(e)];
}
