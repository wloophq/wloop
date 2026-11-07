"use strict";

// A decision queue: one decision at a time, the oldest first, with its card or its unit's row, its primary
// action, the progress through the queue and the decision next up. The daemon orders the items and names each
// primary; this draws them and walks them.

// queueState is where the queue screen stands: the queue of key it shows, the item at, why its last action
// failed and why its last load failed.
const queueState = { key: "", queue: null, at: 0, error: "", loadError: "", busy: false };

// queueKey names a queue route's items: a kind and its lens query, or the tasks a list names.
function queueKey(route) {
  return route.queue === QUEUE_LIST ? QUEUE_LIST + ":" + route.ids.join(",") : route.queue + "?" + (route.q || "");
}

// queueURL is the API path of a kind's queue narrowed by its lens query.
function queueURL(route) {
  return "/api/queues/" + encodeURIComponent(route.queue) + (route.q ? "?q=" + encodeURIComponent(route.q) : "");
}

// listQueue is the queue of the tasks ids name, in their order, each opened by its own detail.
function listQueue(tasks) {
  return {
    kind: QUEUE_LIST, title: t("Results"), empty: t("Nothing left in this list."), total: tasks.length,
    items: tasks.map((e) => ({ task: e, primary: { kind: "task", id: e.id, label: t("Open") } })),
  };
}

// queueDraft reports whether gate g is a draft's, which the daemon serves with its delivery and the draft.
function queueDraft(g) {
  return !!(g && g.delivery && g.subject && typeof g.subject === "object");
}

// queueDraftOf is the draft of gate g, which names its gate.
function queueDraftOf(g) {
  return { ...g.subject, gate: g.subject.gate || g.id };
}

// queueItemCard is the decision of item: its gate's card, the draft's card for a gate on a draft (which the
// daemon serves with its delivery), else its unit's row, as view draws it, with the situation that makes it.
function queueItemCard(item, env, view) {
  const g = item.gate;
  if (queueDraft(g)) return draftCard(item.task, queueDraftOf(g), { scope: "queue", keyOf: env.keyOf });
  if (g) return gateCard(item.task, g, { scope: "queue" });
  return h("div", { class: "queue-unit" },
    env.row(item.task, view),
    item.situation && item.situation.label ? h("p", { class: "situation" }, item.situation.label) : null);
}

// queuePrimary is item's primary action as a button; Enter on the queue runs it too.
function queuePrimary(item, env) {
  const p = item.primary || {};
  if (!p.kind) return null;
  return h("button", { type: "button", class: "act primary queue-primary", "data-focus": "queue:primary", "aria-keyshortcuts": "Enter", disabled: queueState.busy || undefined,
    onclick: () => env.primary(item) }, h("kbd", {}, "↵"), " ", p.label || p.id);
}

// queueLoad reads route's queue into queueState through env.get, keeping the item at while the same queue
// reloads; a list queue reads each task it names, in order, leaving out the ones gone.
async function queueLoad(route, env) {
  const key = queueKey(route);
  const same = queueState.key === key;
  try {
    const queue = route.queue === QUEUE_LIST
      ? listQueue((await Promise.all(route.ids.map((id) => env.get("/api/tasks/" + encodeURIComponent(id)).catch(() => null)))).filter(Boolean))
      : await env.get(queueURL(route));
    if (queueKey(env.route()) !== key) return;
    if (!same) Object.assign(queueState, { at: 0, error: "" });
    Object.assign(queueState, { key, queue, loadError: "" });
    queueState.at = Math.min(queueState.at, Math.max((queue.items || []).length - 1, 0));
  } catch (err) {
    if (queueKey(env.route()) !== key) return;
    if (!same) Object.assign(queueState, { key, queue: null, at: 0, error: "" });
    queueState.loadError = t("Could not load the queue: {error}", { error: err.message });
  }
}

// queueShown is the loaded queue when it is route's, else null: a queue loaded for another route never shows
// nor runs.
function queueShown(route) {
  return queueState.key === queueKey(route) ? queueState.queue : null;
}

// queueUnit is the unit of item's task its situation is on: the first of its units of that provider.
function queueUnit(item) {
  const p = item.situation ? item.situation.provider : "";
  return ((item.task && item.task.units) || []).find((u) => !p || u.provider === p) || null;
}

// queueAct runs item's primary: its gate's intent as the card does, its unit's catalog action, its unit's
// source, or the task's detail; then the queue reloads, so a decision settled elsewhere leaves it and a
// refused one shows why. Nothing runs twice while a run is in flight.
async function queueAct(item, env) {
  if (queueState.busy) return;
  const p = item.primary || {};
  queueState.busy = true;
  queueState.error = "";
  try {
    const g = item.gate;
    const card = p.kind === "gate" && queueDraft(g) ? queueDraftAction(item, "queue:primary") : null;
    if (card && card.enabled) await card.run();
    else if (p.kind === "gate" && queueDraft(g)) await resolveDraft(queueDraftOf(g), p.id, p.label || p.id, {});
    else if (p.kind === "gate") await resolveGate(item.task, g, p.id);
    else if (p.kind === "action") {
      const u = queueUnit(item);
      if (u) await env.act(u, p.id);
    } else if (p.kind === "open") env.openSource(item.task, queueUnit(item));
    else if (p.kind === "task") env.openTask(item.task.id);
  } catch (err) {
    queueState.error = t("{action} failed: {error}", { action: p.label || p.id, error: err.message });
  } finally {
    queueState.busy = false;
  }
  if (p.kind === "gate" || p.kind === "action") await queueLoad(env.route(), env);
  env.render();
}

// queueDraftAction is the action of item's draft card that key runs, null when the card offers none.
function queueDraftAction(item, key) {
  if (!item || !queueDraft(item.gate)) return null;
  return draftActions(item.task, queueDraftOf(item.gate)).find((a) => a.key === key) || null;
}

// queueDraftKey runs the action of item's draft card that key names, then reloads the queue; false when the
// card offers none, so the key goes on to its other bindings. A refused action says why.
function queueDraftKey(item, key, env) {
  const a = queueDraftAction(item, key);
  if (!a) return false;
  if (!a.enabled) {
    detailEnv.notice(t("{action}: {reason}", { action: a.label, reason: a.reason || a.title || "" }), true);
    return true;
  }
  (async () => {
    await a.run();
    if (key !== "queue:edit") await queueLoad(env.route(), env);
    env.render();
  })();
  return true;
}

// queueKeyAction runs a queue key's action: the primary, the next or the previous decision, its source, the home.
function queueKeyAction(action, env) {
  const queue = queueShown(env.route());
  const items = (queue && queue.items) || [];
  const item = items[queueState.at];
  switch (action) {
    case "queue:primary":
      if (item) queueAct(item, env);
      return !!item;
    case "queue:skip":
    case "queue:back":
      queueState.at = Math.min(Math.max(queueState.at + (action === "queue:skip" ? 1 : -1), 0), Math.max(items.length - 1, 0));
      env.render();
      return true;
    case "queue:edit":
    case "queue:discard":
    case "queue:close-silently":
    case "queue:skip-workflow":
      return queueDraftKey(item, action, env);
    case "open-source":
      if (item) env.openSource(item.task, queueUnit(item));
      return !!item;
    case "queue:home":
      env.navigate(HOME);
      return true;
  }
  return false;
}

// renderQueue is the queue screen's nodes for queue, at its item at.
function renderQueue(queue, at, env) {
  const home = h("a", { class: "crumb", href: "#view=" + HOME, "data-focus": "queue:home" }, t("Home"));
  const why = queueState.key === queueKey(env.route()) ? [queueState.loadError, queueState.error].filter(Boolean) : [];
  const err = why.length ? h("p", { class: "err", role: "alert" }, why.join(" ")) : null;
  if (!queue) return [h("div", { class: "queue" }, h("nav", { class: "crumbs" }, home), err || h("p", { class: "loading" }, t("Loading the queue...")))];
  const items = queue.items || [];
  const head = h("nav", { class: "crumbs", "aria-label": t("Where you are") }, home, h("span", { class: "sep", "aria-hidden": "true" }, "/"), h("span", {}, queue.title));
  if (!items.length) {
    return [h("div", { class: "queue empty" }, head, err, h("p", { class: "empty" }, queue.empty || t("Nothing left in this queue.")),
      h("a", { class: "back-home", href: "#view=" + HOME }, t("Back to the home")))];
  }
  const i = Math.min(Math.max(at, 0), items.length - 1);
  const item = items[i];
  const next = items[i + 1];
  const more = items.length - i - 2;
  return [h("div", { class: "queue", "data-item": String(i) },
    head,
    h("p", { class: "progress", "aria-live": "polite" }, t("{n} of {total}", { n: fmtNumber(i + 1), total: fmtNumber(items.length) })),
    err,
    h("div", { class: "queue-item" }, queueItemCard(item, env, queue.view), queuePrimary(item, env)),
    next ? h("section", { class: "next-up", "aria-label": t("Next up") }, h("h3", {}, t("Next up")), env.row(next.task, queue.view),
      more > 0 ? h("p", { class: "more" }, tn("+{n} more", more)) : null) : null)];
}
