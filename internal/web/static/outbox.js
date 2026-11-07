"use strict";

// The Outbox: the drafts waiting on you, grouped by why they were drafted, as GET /api/outbox serves them.
const OUTBOX_PATH = "/api/outbox";

// OUTBOX_LAYOUT is the served layout of a view outboxPage draws, whatever the view's id.
const OUTBOX_LAYOUT = "outbox";

// outboxState is the last Outbox page read, why the last read failed, and the workflow whose "Stop drafting
// these" waits for its confirm.
const outboxState = { page: null, error: "", confirming: "" };

async function loadOutbox() {
  try {
    outboxState.page = await getJSON(OUTBOX_PATH);
    outboxState.error = "";
  } catch (err) {
    outboxState.error = t("Could not load the Outbox: {error}", { error: err.message });
  }
}

// outboxPage is a head and the draft cards of each group of page, in its order.
function outboxPage(page) {
  return h("div", { class: "outbox" }, ...((page && page.groups) || []).map(outboxGroup));
}

function outboxGroup(g) {
  const drafts = g.drafts || [];
  return h("section", { class: "ob-group", "data-workflow": g.workflow || "", "data-purpose": g.purpose || "", "aria-label": g.title },
    h("div", { class: "ob-head" },
      h("h3", { class: "ob-title" }, g.title),
      h("span", { class: "ob-n" }, tn("{n} draft", drafts.length)),
      g.workflow ? h("code", { class: "ob-workflow" }, g.workflow) : null,
      g.workflow ? stopDrafting(g.workflow) : null),
    g.about ? h("p", { class: "ob-about" }, g.about) : null,
    ...drafts.map((d) => draftCard(null, d, { scope: "outbox", context: d.context })));
}

// stopDrafting turns workflow off once its inline confirm is pressed.
function stopDrafting(workflow) {
  const focus = (what) => `outbox:${what}:${workflow}`;
  if (outboxState.confirming !== workflow) {
    return h("span", { class: "ob-stop" }, h("button", {
      type: "button", class: "act", "data-focus": focus("stop"),
      onclick: () => { outboxState.confirming = workflow; detailEnv.render(); },
    }, t("Stop drafting these")));
  }
  return h("span", { class: "ob-stop", role: "group", "aria-label": t("Stop drafting these") },
    h("button", { type: "button", class: "act primary", "data-focus": focus("stop-confirm"), onclick: () => disableWorkflow(workflow) }, t("Stop this workflow")),
    h("button", { type: "button", class: "act", "data-focus": focus("stop-cancel"), onclick: () => { outboxState.confirming = ""; detailEnv.render(); } }, t("Keep it")));
}

async function disableWorkflow(workflow) {
  outboxState.confirming = "";
  try {
    await detailEnv.post("/api/workflows/" + encodeURIComponent(workflow) + "/disable", { source: "cockpit" });
    detailEnv.notice(t("Stopped {workflow}", { workflow }));
    detailEnv.changed();
  } catch (err) {
    detailEnv.notice(t("{action} failed: {error}", { action: t("Stop drafting these"), error: err.message }), true);
  }
  detailEnv.render();
}
