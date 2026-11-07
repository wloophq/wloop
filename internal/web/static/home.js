"use strict";

// The home: the briefing the daemon words, its counters as links to their queues or views, the first rows of
// its sections and the capture field. The daemon decides every word and count; this only draws them.

// homeCounterHref is where a counter leads: its decision kind's queue, else its view.
function homeCounterHref(c) {
  return c.kind ? "#queue=" + encodeURIComponent(c.kind) : "#view=" + encodeURIComponent(c.view);
}

// homeBriefing is the briefing's sentences, each counter part a link to its queue or view; its empty text when
// it holds none.
function homeBriefing(b) {
  const sentences = b.sentences || [];
  if (!sentences.length) return h("p", { class: "briefing empty" }, b.empty || "");
  const counters = b.counters || [];
  return h("p", { class: "briefing" }, ...sentences.map((parts) => h("span", { class: "sentence" }, ...parts.map((p) => {
    const c = p.counter === undefined || p.counter === null ? null : counters[p.counter];
    return c ? h("a", { class: "counter", href: homeCounterHref(c), "data-focus": "home:counter:" + c.id }, c.label) : p.text;
  }), " ")));
}

// homeSection is a section's first rows, each drawn by row, and a link to its whole view counting them all.
function homeSection(s, row) {
  const rows = s.rows || [];
  return h("section", { class: "home-section", "data-section": s.id, "aria-label": s.label },
    h("header", { class: "home-head" },
      h("h2", {}, s.label),
      s.total > rows.length ? h("a", { class: "see-all", href: "#view=" + encodeURIComponent(s.view), "data-focus": "home:all:" + s.id }, tn("See all {n}", s.total)) : null),
    rows.length ? h("ol", { class: "rows" }, ...rows.map((e) => row(e, s.view))) : h("p", { class: "empty" }, t("Nothing here right now.")));
}

// homeCapture is the capture field: Enter captures what is typed as a new task and clears the field.
function homeCapture(c, capture) {
  return h("form", { class: "capture", onsubmit: (ev) => ev.preventDefault && ev.preventDefault() },
    h("input", {
      type: "text", class: "capture-input", "aria-label": t("Capture a task"), placeholder: c.hint || t("Capture a task"), autocomplete: "off",
      "aria-keyshortcuts": c.key || undefined, "data-focus": "home:capture",
      onkeydown: (ev) => {
        if (ev.key !== "Enter" || ev.isComposing) return;
        if (ev.preventDefault) ev.preventDefault();
        const field = ev.target;
        const title = String(field.value || "").trim();
        if (!title) return;
        return Promise.resolve(capture(title)).then((ok) => {
          if (ok !== false) field.value = "";
        });
      },
    }),
    c.key ? h("kbd", { class: "capture-key" }, c.key) : null);
}

// renderHome is the home's nodes: env.row draws a task's row in a view, env.capture captures a title.
function renderHome(home, env) {
  if (!home) return [h("p", { class: "loading" }, t("Loading the home..."))];
  return [h("div", { class: "home" },
    homeBriefing(home.briefing || {}),
    homeCapture(home.capture || {}, env.capture),
    ...(home.sections || []).map((s) => homeSection(s, env.row)))];
}
