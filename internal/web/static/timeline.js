"use strict";

// timelineNodes draws a task's timeline as the daemon folds it into facts, newest first: each a time, a dot in
// its tone, its words, the prose of its newest event that carries some, and its raw events folded under a count.
function timelineNodes(nodes) {
  return h("ol", { class: "tl" }, ...(nodes || []).map(timelineNode));
}

function timelineNode(n) {
  const events = n.events || [];
  const spoken = events.filter((ev) => eventText(ev));
  const last = spoken[spoken.length - 1];
  return h("li", { class: ["ev", n.tone, n.running ? "running" : ""].filter(Boolean).join(" "), "data-rule": n.rule || "" },
    h("time", { class: "tm", datetime: n.at, title: fmtDate(n.at, "long") }, timelineStamp(n.at)),
    h("span", { class: "ln", "aria-hidden": "true" }, h("i", {})),
    h("div", { class: "bd" },
      h("div", { class: "lb" }, n.label),
      last ? h("div", { class: "dim" }, renderMarkdown(eventText(last), { fold: 3, key: "timeline:" + last.seq })) : null,
      events.length > 1 ? h("details", {},
        h("summary", {}, tn("{n} event", events.length)),
        h("ol", { class: "raw" }, ...events.map(timelineRawEvent))) : null));
}

// timelineRawEvent is one event a fact folded: its time, its kind's words, its sub and who did it.
function timelineRawEvent(ev) {
  const who = ev.by || ev.actor || "";
  return h("li", { "data-seq": String(ev.seq) },
    h("time", { class: "tm", datetime: ev.at }, fmtDate(ev.at, "time")),
    " ", h("span", { class: "what" }, eventLabel(ev.kind), ev.sub ? " " + ev.sub : ""),
    who ? h("span", { class: "who", title: ev.actor || "" }, " " + who) : null);
}

// timelineStamp is when a fact happened: its time on the day it is read, else its date and time.
function timelineStamp(iso) {
  const at = new Date(iso);
  const now = new Date(Date.now());
  return fmtDate(iso, at.toDateString() === now.toDateString() ? "time" : "datetime");
}

// eventText is the prose an event carries, "" for none.
function eventText(ev) {
  const d = ev.data && typeof ev.data === "object" ? ev.data : {};
  return d.text || d.summary || d.title || d.line || "";
}
