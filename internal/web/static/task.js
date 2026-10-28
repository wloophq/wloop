"use strict";

// taskState is what the task page holds beside the task: its timeline, the
// kinds it is narrowed to and every kind seen, the composer's text, each
// gate's answer and a run's transcript.
const taskState = { id: "", timeline: null, timelineError: "", kinds: [], kindsSeen: [], text: "", answers: new Map(), transcript: null };

function resetTaskPage(id) {
  Object.assign(taskState, { id, timeline: null, timelineError: "", kinds: [], kindsSeen: [], text: "", answers: new Map(), transcript: null });
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

// loadTaskPage reads the timeline of e, narrowed to the kinds picked.
async function loadTaskPage(e) {
  if (taskState.id !== e.id) resetTaskPage(e.id);
  try {
    const events = await detailEnv.getJSON(timelineURL(e.id, taskState.kinds));
    if (taskState.id !== e.id) return;
    taskState.timeline = events || [];
    taskState.timelineError = "";
    for (const ev of taskState.timeline) if (!taskState.kindsSeen.includes(ev.kind)) taskState.kindsSeen.push(ev.kind);
  } catch (err) {
    if (taskState.id === e.id) taskState.timelineError = `Could not load the timeline: ${err.message}`;
  }
}

function propText(v) {
  if (v === null || v === undefined) return "";
  if (Array.isArray(v)) return v.map(propText).filter(Boolean).join(", ");
  if (typeof v === "object") return v.name || v.key || "";
  return String(v);
}

// taskProps are the task's properties the profile declares, in its order.
function taskProps(e) {
  const props = e.props || {};
  const shown = (taskCatalog().props || []).filter((p) => propText(props[p.name]));
  if (!shown.length) return null;
  return h("dl", { class: "d-props" }, ...shown.flatMap((p) => [h("dt", {}, p.name), h("dd", { "data-prop": p.name }, propText(props[p.name]))]));
}

// taskLinks are the task's units: a unit's card where it has one, else its
// label, opening the unit in the cockpit.
function taskLinks(e) {
  const cards = e.cards || [];
  const units = e.units || [];
  if (!units.length) return null;
  return h("div", { class: "d-links" }, ...units.map((u, i) => {
    const c = cards.find((x) => x.card && x.card.unit && x.card.unit.provider === u.provider && x.card.unit.key === u.key);
    if (c) return unitCard(c);
    return h("div", { class: "meta" }, unitRef(u, (e.unit_labels || [])[i] || unitLabel(u), ""));
  }));
}

function taskHeader(e) {
  const waiting = ((e.waiting || {}).on || []).map((p) => p.name).filter(Boolean);
  return h("header", { class: "d-head" },
    h("button", { type: "button", class: "close", "aria-label": "Close detail", "data-focus": "detail:close", onclick: () => detailEnv.close() }, "×"),
    h("h1", {}, (e.props || {}).focus === "on" ? h("span", { class: "star" }, "★ ") : null, e.title || e.id),
    h("div", { class: "meta" },
      h("span", { class: "pill state", "data-category": e.category || "" }, stateLabel(e.state)),
      e.changed ? [" ", h("span", { class: "flag" }, "changed")] : null,
      e.owed_by ? [" owed by ", h("strong", {}, e.owed_by)] : null,
      e.wake && e.wake.until ? [" wakes ", stamp(e.wake.until)] : null),
    waiting.length ? h("div", {}, "Waiting on ", h("strong", {}, waiting.join(", ")), e.waiting.since ? ` since ${stamp(e.waiting.since)}` : "") : null,
    e.notes ? h("p", { class: "d-notes" }, e.notes) : null,
    taskProps(e),
    taskLinks(e),
    h("div", { class: "d-actions" }, ...verbButtons(e), ...taskIntentButtons(e)));
}

function taskAsks(e) {
  const asks = e.asks || [];
  if (!asks.length) return null;
  return h("section", { class: "d-asks", "aria-label": "Asks" },
    h("h2", { class: "d-sub" }, "Asks"),
    h("ul", { class: "asks" }, ...asks.map((a) => h("li", { class: "ask" },
      h("div", {}, a.asker ? h("strong", {}, a.asker + ": ") : null, a.summary,
        safeURL(a.permalink) ? [" ", h("a", { href: safeURL(a.permalink), target: "_blank", rel: "noopener noreferrer", class: "ext" }, "source")] : null),
      ...(a.quotes || []).map((q) => h("blockquote", { class: "quote" }, q))))));
}

function taskBrief(e) {
  const r = e.brief;
  if (!r) return null;
  const b = r.data && typeof r.data === "object" ? r.data : {};
  const known = b.text || b.goal || b.context || b.risk || (b.open_questions || []).length;
  return h("section", { class: "d-brief", "aria-label": "Brief" },
    h("h2", { class: "d-sub" }, "Brief"),
    b.text ? h("div", { class: "draft-text" }, b.text) : null,
    b.goal ? h("p", { class: "goal" }, b.goal) : null,
    b.context ? h("p", {}, b.context) : null,
    b.risk ? h("p", { class: "risk-" + b.risk }, `Risk: ${b.risk}`, b.risk_why ? ` (${b.risk_why})` : "") : null,
    (b.open_questions || []).length ? h("ul", {}, ...b.open_questions.map((q) => h("li", {}, q))) : null,
    known ? null : h("pre", { class: "draft-text" }, JSON.stringify(r.data, null, 2)),
    r.at ? h("div", { class: "meta" }, stamp(r.at)) : null);
}

// resolveGate resolves gate g of task e with intent id; an intent that takes
// an answer sends the text typed in the gate.
async function resolveGate(e, g, id) {
  const intent = (g.intents || []).find((x) => x.id === id);
  if (!intent) return null;
  const args = {};
  if (intent.answer) {
    const text = (taskState.answers.get(g.id) || "").trim();
    if (!text) {
      detailEnv.notice(`${intent.label}: write the answer first`, true);
      return null;
    }
    args.guidance = text;
  }
  try {
    const out = await detailEnv.post("/api/gates/" + encodeURIComponent(g.id) + "/" + encodeURIComponent(id), verbBody(args));
    detailEnv.notice(`${intent.label}: ${(e && (e.title || e.id)) || g.text || g.kind}`);
    taskState.answers.delete(g.id);
    detailEnv.changed();
    return out || {};
  } catch (err) {
    detailEnv.notice(`${intent.label} failed: ${err.message}`, true);
    return null;
  }
}

// gateCard is an open gate with its intents in the profile's order, each
// with the digit that picks it while the card has the focus.
function gateCard(e, g) {
  const intents = g.intents || [];
  const subject = g.subject && typeof g.subject === "object" ? g.subject : {};
  const deflt = intents.find((x) => x.id === g.default);
  return h("article", { class: "d-gate", "data-gate": String(g.id), "data-kind": g.kind, "aria-label": g.text || g.name || g.kind },
    h("h3", { class: "d-sub" }, g.text || g.name || g.kind),
    subject.text ? h("pre", { class: "draft-text" }, subject.text) : null,
    g.delivery ? h("p", { class: "meta" }, g.delivery) : null,
    g.deadline ? h("p", { class: "meta" }, deflt ? `${deflt.label} at ${stamp(g.deadline)} unless you pick another` : `Until ${stamp(g.deadline)}`) : null,
    intents.some((x) => x.answer) ? h("textarea", { rows: 2, "aria-label": "Answer", "data-focus": `gate:${g.id}:text`,
      oninput: (ev) => { taskState.answers.set(g.id, ev.target.value); } }, taskState.answers.get(g.id) || "") : null,
    h("div", { class: "d-actions" }, ...intents.map((x, i) => h("button", {
      type: "button", class: "act" + (i === 0 ? " primary" : ""), "data-focus": `gate:${g.id}:${x.id}`, "aria-keyshortcuts": i < 9 ? String(i + 1) : undefined,
      onclick: () => resolveGate(e, g, x.id),
    }, i < 9 ? h("kbd", {}, String(i + 1)) : null, " ", x.label))));
}

function taskGates(e) {
  const d = isDraftTask(e) ? draftOf(e) : null;
  const gates = (e.gates || []).filter((g) => g.status === "open" && !(d && g.kind === "draft"));
  if (!gates.length && !d) return null;
  return h("section", { class: "d-gates", "aria-label": "Open gates" },
    h("h2", { class: "d-sub" }, "Waiting on you"),
    ...gates.map((g) => gateCard(e, g)),
    d ? draftCard(e, "detail") : null);
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
    detailEnv.notice(`${label} failed: ${err.message}`, true);
  }
}

async function runTranscript(r) {
  try {
    const out = await detailEnv.getJSON(runPath("transcript", r.seq));
    taskState.transcript = { seq: r.seq, lines: transcriptLines(out) };
  } catch (err) {
    taskState.transcript = { seq: r.seq, error: `Could not load the transcript: ${err.message}` };
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
  const t = taskState.transcript && taskState.transcript.seq === r.seq ? taskState.transcript : null;
  return h("article", { class: "d-run", "data-run": String(r.seq) },
    h("div", { class: "meta" }, h("strong", {}, r.intent), " ", h("span", { class: "pill" }, r.state),
      r.manager ? [" in ", r.manager] : null,
      r.session ? [" session ", h("code", {}, r.session), r.session_state ? ` (${r.session_state})` : ""] : null),
    r.worktree || r.workspace ? h("div", { class: "meta" }, h("code", {}, r.worktree || r.workspace)) : null,
    r.resume ? h("div", { class: "meta" }, "Resume: ", h("code", {}, r.resume)) : null,
    h("div", { class: "d-actions" }, button("open", "Open workspace"), button("enter", "Enter session"), button("transcript", "Transcript")),
    t ? (t.error ? h("p", { class: "plan-error" }, t.error) : h("ol", { class: "transcript" }, ...t.lines.map((l) => h("li", {}, l)))) : null);
}

function taskRuns(e) {
  const runs = e.runs || [];
  if (!runs.length) return null;
  return h("section", { class: "d-runs", "aria-label": "Runs" }, h("h2", { class: "d-sub" }, "Runs"), ...runs.map(runCard));
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
  return h("section", { class: "d-timeline", "aria-label": "Timeline" },
    h("h2", { class: "d-sub" }, "Timeline"),
    taskState.kindsSeen.length ? h("div", { class: "chips kinds", role: "group", "aria-label": "Kinds" },
      h("button", { type: "button", class: "chip" + (kinds.length ? "" : " on"), "aria-pressed": String(!kinds.length), "data-focus": "timeline:kind:", onclick: () => pickKinds(e, []) }, "all"),
      ...taskState.kindsSeen.map(chip)) : null,
    taskState.timelineError ? h("p", { class: "plan-error" }, taskState.timelineError) : null,
    !taskState.timeline && !taskState.timelineError ? h("p", { class: "empty" }, "Loading the timeline.") : null,
    taskState.timeline && !events.length ? h("p", { class: "empty" }, "Nothing here yet.") : null,
    events.length ? h("ol", { class: "chat" }, ...[...events].reverse().map((ev) => h("li", { class: `msg ev-${ev.kind} by-${String(ev.actor || "").replace(/[^a-z0-9-]/gi, "")}`, "data-seq": String(ev.seq) },
      h("div", { class: "meta" }, ev.actor ? h("strong", {}, ev.actor) : null, " ", h("span", { class: "kind" }, String(ev.kind).replaceAll("_", " "), ev.sub ? " " + ev.sub : ""), " ", h("time", { datetime: ev.at }, stamp(ev.at))),
      eventText(ev) ? h("p", {}, eventText(ev)) : null))) : null);
}

// sendChat writes the composer's text to the task's agent.
async function sendChat(e) {
  const text = taskState.text.trim();
  if (!text) {
    detailEnv.notice("Write a message first", true);
    return null;
  }
  try {
    const out = await detailEnv.post(taskPath(e.id, "chat"), { text });
    taskState.text = "";
    detailEnv.notice(`Sent to ${e.title || e.id}`);
    await loadTaskPage(e);
    detailEnv.render();
    return out || {};
  } catch (err) {
    detailEnv.notice(`Sending failed: ${err.message}`, true);
    return null;
  }
}

// saveDraft holds the composer's text as a reply in the task's conversation,
// for approval, through the verb whose handler replies.
async function saveDraft(e, verb) {
  const text = taskState.text.trim();
  if (!text) {
    detailEnv.notice("Write a message first", true);
    return null;
  }
  const out = await taskAct(e, verb, { kind: "reply", text, send: "draft" });
  if (out) {
    taskState.text = "";
    detailEnv.render();
  }
  return out;
}

// taskComposer writes to the task's agent: Send, the default that Ctrl+Enter
// runs, posts to its chat; Save as draft holds the text as a reply.
function taskComposer(e) {
  const verb = handlerVerb("reply");
  const reply = verb ? verbState(e, verb) : null;
  return h("section", { class: "d-compose", "aria-label": "Message" },
    h("textarea", { rows: 3, "aria-label": "Message", placeholder: "Write to the agent", "data-focus": "task:compose:text",
      oninput: (ev) => { taskState.text = ev.target.value; },
      onkeydown: (ev) => {
        if (ev.key !== "Enter" || !(ev.ctrlKey || ev.metaKey)) return;
        if (ev.preventDefault) ev.preventDefault();
        sendChat(e);
      } }, taskState.text),
    h("div", { class: "d-actions" },
      h("button", { type: "button", class: "act primary", "data-focus": "task:compose:send", onclick: () => sendChat(e) }, "Send"),
      h("button", { type: "button", class: "act" + (reply && reply.enabled ? "" : " off"), disabled: !(reply && reply.enabled),
        title: reply ? (reply.enabled ? verbTask(verb).summary : reply.reason) || undefined : "Not offered here",
        "data-focus": "task:compose:draft", onclick: () => saveDraft(e, verb) }, "Save as draft")));
}

function taskChildren(e) {
  const kids = e.children || [];
  if (!kids.length) return null;
  return h("section", { class: "d-children", "aria-label": "Children" },
    h("h2", { class: "d-sub" }, "Children"),
    h("ol", { class: "d-tasks" }, ...kids.map((id) => {
      const c = taskById(id);
      return h("li", {}, h("a", { href: "#" + viewHash({ task: id, pr: "" }), "data-focus": "child:" + id }, (c && c.title) || id),
        c ? [" ", h("span", { class: "pill" }, stateLabel(c.state))] : null);
    })));
}

// taskPage is a task's page: its header, asks, brief, open gates, runs,
// timeline, composer and children.
function taskPage(e) {
  return [taskHeader(e), taskAsks(e), taskBrief(e), taskGates(e), taskRuns(e), taskTimeline(e), taskComposer(e), taskChildren(e)];
}
