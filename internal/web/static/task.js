"use strict";

// taskState is what the task page holds beside the task: its timeline, the kinds it is narrowed to and every
// kind seen, the composer's text, a run's transcript, the drafts that wait on it and the tab picked.
const taskState = { id: "", timeline: null, timelineError: "", kinds: [], kindsSeen: [], text: "", transcript: null, drafts: [], tab: "" };

function resetTaskPage(id) {
  Object.assign(taskState, { id, timeline: null, timelineError: "", kinds: [], kindsSeen: [], text: "", transcript: null, drafts: [] });
  resetGates();
}

function taskCatalog() {
  return (typeof tasksState === "object" && tasksState.catalog) || {};
}

function stateLabel(name) {
  return ((taskCatalog().states || []).find((s) => s.name === name) || {}).label || name;
}

function timelineURL(id, kinds) {
  const q = kinds.length ? "?" + new URLSearchParams({ kinds: kinds.join(",") }).toString() : "";
  return taskPath(id, "timeline") + q;
}

// loadTaskPage reads the timeline of e, narrowed to the kinds picked, and the drafts that wait on e.
async function loadTaskPage(e) {
  if (taskState.id !== e.id) resetTaskPage(e.id);
  await Promise.all([loadTimeline(e), loadTaskDrafts(e)]);
}

async function loadTimeline(e) {
  try {
    const events = await detailEnv.getJSON(timelineURL(e.id, taskState.kinds));
    if (taskState.id !== e.id) return;
    taskState.timeline = events || [];
    taskState.timelineError = "";
    for (const ev of taskState.timeline) if (!taskState.kindsSeen.includes(ev.kind)) taskState.kindsSeen.push(ev.kind);
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

function propText(v) {
  if (v === null || v === undefined) return "";
  if (Array.isArray(v)) return v.map(propText).filter(Boolean).join(", ");
  if (typeof v === "object") return v.name || v.key || "";
  return String(v);
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
    .map((p) => [p.name, propText(props[p.name]), { "data-prop": p.name }]);
  const b = briefData(e);
  if (b.risk) rows.push([t("Risk"), [h("span", { class: "risk-" + b.risk }, b.risk), b.risk_why ? " · " + b.risk_why : null], { "data-prop": "brief:risk" }]);
  if (b.size) rows.push([t("Size"), b.size, { "data-prop": "brief:size" }]);
  for (const c of e.cards || []) {
    for (const g of (c.card || {}).signals || []) rows.push([g.key, g.at ? fmtDate(g.at, "datetime") : g.value, { class: "tone-" + (g.tone || "neutral") }]);
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
  return cards.length ? h("div", { class: "d-links" }, ...cards.map(unitCard)) : null;
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
    b.risk ? h("p", { class: "risk-" + b.risk }, t("Risk: {risk}", { risk: b.risk }), b.risk_why ? ` (${b.risk_why})` : "") : null,
    (b.open_questions || []).length ? h("ul", {}, ...b.open_questions.map((q) => h("li", {}, q))) : null,
    (b.tags || []).length ? h("div", { class: "d-brief-tags" }, ...b.tags.map((x) => h("span", { class: "pill" }, x))) : null,
    known ? null : h("pre", { class: "draft-text" }, JSON.stringify(r.data, null, 2)),
    r.at ? h("div", { class: "meta" }, h("time", { datetime: r.at }, fmtDate(r.at, "datetime"))) : null);
}

// taskGates are the decisions that wait on the task: its open gates, the draft it carries as a draft's own task,
// and the drafts that wait on it, each a card.
function taskGates(e) {
  const own = isDraftTask(e) ? draftOf(e) : null;
  const drafts = (taskState.id === e.id ? taskState.drafts : []).filter((d) => !own || d.id !== own.id);
  const gates = (e.gates || []).filter((g) => g.status === "open" && !(own && g.subject === own) && !drafts.some((d) => d.gate && d.gate === g.id));
  if (!gates.length && !own && !drafts.length) return null;
  return h("section", { class: "d-gates", "aria-label": t("Open gates") },
    h("h2", { class: "d-sub" }, t("Waiting on you")),
    ...gates.map((g) => gateCard(e, g)),
    own ? draftCard(e, own, { scope: "detail" }) : null,
    ...drafts.map((d) => draftCard(e, d, { scope: "detail" })));
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
    h("div", { class: "meta" }, h("strong", {}, r.intent), " ", h("span", { class: "pill" }, r.state),
      r.manager ? [" ", t("in {manager}", { manager: r.manager })] : null,
      r.session ? [" ", t("session"), " ", h("code", {}, r.session), r.session_state ? ` (${r.session_state})` : ""] : null),
    r.worktree || r.workspace ? h("div", { class: "meta" }, h("code", {}, r.worktree || r.workspace)) : null,
    r.resume ? h("div", { class: "meta" }, t("Resume:"), " ", h("code", {}, r.resume)) : null,
    h("div", { class: "d-actions" }, button("open", t("Open workspace")), button("enter", t("Enter session")), button("transcript", t("Transcript"))),
    shown ? (shown.error ? h("p", { class: "plan-error" }, shown.error) : h("ol", { class: "transcript" }, ...shown.lines.map((l) => h("li", {}, l)))) : null);
}

function taskRuns(e) {
  const runs = e.runs || [];
  if (!runs.length) return null;
  return h("section", { class: "d-runs", "aria-label": t("Runs") }, ...runs.map(runCard));
}

function eventText(ev) {
  const d = ev.data && typeof ev.data === "object" ? ev.data : {};
  return d.text || d.summary || d.title || d.line || "";
}

async function pickKinds(e, kinds) {
  taskState.kinds = kinds;
  await loadTaskPage(e);
  detailEnv.render();
}

// taskTimeline is the task's timeline as a chat, oldest first, narrowed to
// the kinds picked.
function taskTimeline(e) {
  const kinds = taskState.kinds;
  const chip = (kind) => {
    const on = kinds.includes(kind);
    return h("button", { type: "button", class: "chip" + (on ? " on" : ""), "aria-pressed": String(on), "data-focus": "timeline:kind:" + kind,
      onclick: () => pickKinds(e, on ? kinds.filter((k) => k !== kind) : [...kinds, kind]) }, kind.replaceAll("_", " "));
  };
  const events = (taskState.timeline || []).filter((ev) => !kinds.length || kinds.includes(ev.kind));
  return h("section", { class: "d-timeline", "aria-label": t("Timeline") },
    taskState.kindsSeen.length ? h("div", { class: "chips kinds", role: "group", "aria-label": t("Kinds") },
      h("button", { type: "button", class: "chip" + (kinds.length ? "" : " on"), "aria-pressed": String(!kinds.length), "data-focus": "timeline:kind:", onclick: () => pickKinds(e, []) }, t("all")),
      ...taskState.kindsSeen.map(chip)) : null,
    taskState.timelineError ? h("p", { class: "plan-error" }, taskState.timelineError) : null,
    !taskState.timeline && !taskState.timelineError ? h("p", { class: "empty" }, t("Loading the timeline.")) : null,
    taskState.timeline && !events.length ? h("p", { class: "empty" }, t("Nothing here yet.")) : null,
    events.length ? h("ol", { class: "chat" }, ...events.map((ev) => h("li", { class: `msg ev-${ev.kind} by-${String(ev.actor || "").replace(/[^a-z0-9-]/gi, "")}`, "data-seq": String(ev.seq) },
      h("div", { class: "meta" }, ev.by || ev.actor ? h("strong", { title: ev.actor || "" }, ev.by || ev.actor) : null, " ", h("span", { class: "kind" }, String(ev.kind).replaceAll("_", " "), ev.sub ? " " + ev.sub : ""), " ", h("time", { datetime: ev.at }, stamp(ev.at))),
      eventText(ev) ? h("p", {}, eventText(ev)) : null))) : null);
}

// sendChat writes the composer's text to the task's agent.
async function sendChat(e) {
  const text = taskState.text.trim();
  if (!text) {
    detailEnv.notice(t("Write a message first"), true);
    return null;
  }
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
  const out = await taskAct(e, verb, { kind: "reply", text, send });
  if (out) {
    taskState.text = "";
    detailEnv.render();
  }
  return out;
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
  return h("section", { class: "compose d-compose", "aria-label": t("Message") },
    h("textarea", { rows: 3, "aria-label": t("Message"), placeholder: t("Write to the agent"), "data-focus": "task:compose:text",
      oninput: (ev) => { taskState.text = ev.target.value; },
      onkeydown: (ev) => {
        if (ev.key !== "Enter" || !(ev.ctrlKey || ev.metaKey)) return;
        if (ev.preventDefault) ev.preventDefault();
        sendChat(e);
      } }, taskState.text),
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
  { id: "timeline", label: () => t("Timeline"), shows: () => true, count: () => (taskState.timeline || []).length, body: (e) => [taskTimeline(e)] },
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
  const strip = h("div", {
    class: "tabs", role: "tablist", "aria-label": t("Sections"),
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
  }));
  const body = h("div", { class: "d-tabs" }, strip,
    h("div", { class: "tabpanel", role: "tabpanel", id: "task-tabpanel", "aria-labelledby": "task-tab-" + selected }, tab.body(e)));
  return { tabs: shown.map((x) => ({ id: x.id, label: x.label(), count: x.count(e) })), selected, body };
}

// taskVerbs are the profile's primary verbs the task offers, More over the rest, and the intents that serve it.
function taskVerbs(e) {
  const buttons = [...primaryVerbs(e), ...taskIntentButtons(e)].filter(Boolean);
  return buttons.length ? h("div", { class: "verbs d-actions" }, ...buttons) : null;
}

// taskPage is a task's page: its head, facts, units, the decisions that wait on it, its verbs, its tabs and the
// composer.
function taskPage(e) {
  return [taskHead(e), taskProps(e), taskLinks(e), taskGates(e), taskVerbs(e), taskTabs(e).body, taskComposer(e)];
}
