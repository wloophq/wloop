"use strict";

const GTD_DEFAULT_VIEW = "focus";
const gtdState = { catalog: null, catalogError: "", key: "", page: null, entries: [], counters: null, error: "" };

function gtdViews(cat = gtdState.catalog) {
  return (cat && cat.views) || [];
}

function isGTDView(id, cat = gtdState.catalog) {
  return gtdViews(cat).some((v) => v.id === id);
}

// gtdResolveView is the view a route shows: a core view, a served plugin
// view or a plugin's own web view; Focus otherwise. Before anything is
// loaded every view stands.
function gtdResolveView(view, cat, served, plugin) {
  if (view && ((!cat && !served.length) || isGTDView(view, cat) || served.some((v) => v.id === view) || plugin(view))) return view;
  if (!cat && served.length) return served[0].id;
  return GTD_DEFAULT_VIEW;
}

// gtdNav is the rail: the core views with their counters, then each plugin
// view (count left to the caller).
function gtdNav(cat, served, plugin, counters) {
  const views = (counters && counters.views) || {};
  const out = gtdViews(cat).map((v) => ({ id: v.id, label: v.label, count: views[v.id] === undefined ? "" : views[v.id], core: true }));
  const taken = new Set(out.map((v) => v.id));
  for (const v of served) {
    if (taken.has(v.id)) continue;
    taken.add(v.id);
    out.push({ id: v.id, label: v.label, provider: v.provider || "", core: false });
  }
  for (const id of plugin) {
    if (!taken.has(id)) out.push({ id, label: id, provider: (typeof pluginViews !== "undefined" && (pluginViews.get(id) || {}).plugin) || "", core: false });
  }
  return out;
}

// railSections splits gtdNav's list: the core views untitled, then one
// section per provider, titled by name(provider), in the order they first
// appear.
function railSections(nav, name) {
  const out = [{ title: "", views: nav.filter((v) => v.core) }];
  const by = new Map();
  for (const v of nav.filter((x) => !x.core)) {
    if (!by.has(v.provider)) {
      by.set(v.provider, { title: v.provider ? name(v.provider) : "Plugins", views: [] });
      out.push(by.get(v.provider));
    }
    by.get(v.provider).views.push(v);
  }
  return out.filter((sec) => sec.views.length);
}

// entriesURL lists view; every view but Inbox is narrowed to area.
function entriesURL(view, area, ctx, since) {
  const p = new URLSearchParams({ view });
  if (area && view !== "inbox") p.set("area", area);
  if (ctx && ctx.length) p.set("context", ctx.join(","));
  if (since) p.set("since", since);
  return "/api/entries?" + p.toString();
}

function countersURL(area) {
  return "/api/counters" + (area ? "?area=" + encodeURIComponent(area) : "");
}

function gtdQueryKey(view, area, ctx) {
  return [view, view === "inbox" ? "" : area || "", (ctx || []).join(",")].join("|");
}

// gtdSort orders a merged list as the store does: Logbook puts Reference
// first, then the newest; every other view the oldest first.
function gtdSort(view, entries) {
  const t = (s) => Date.parse(s || "") || 0;
  if (view === "outbox") return entries;
  if (view === "logbook") {
    return [...entries].sort((a, b) => Number(b.state === "reference") - Number(a.state === "reference") || t(b.updated) - t(a.updated));
  }
  return [...entries].sort((a, b) => t(a.created) - t(b.created));
}

// applyEntriesPage folds a page of view into the list; a page of another
// query, epoch or a full page replaces it, an older one is dropped.
function applyEntriesPage(key, view, page) {
  const cur = gtdState.page;
  const same = gtdState.key === key && cur && cur.epoch === page.epoch;
  if (same && !page.full && page.rev < cur.rev) return;
  if (!same || page.full) {
    gtdState.entries = page.entries || [];
  } else {
    const gone = new Set(page.removed || []);
    const fresh = new Map((page.entries || []).map((e) => [e.id, e]));
    const kept = gtdState.entries.filter((e) => !gone.has(e.id)).map((e) => {
      const next = fresh.get(e.id);
      fresh.delete(e.id);
      return next || e;
    });
    gtdState.entries = gtdSort(view, [...kept, ...fresh.values()]);
  }
  gtdState.key = key;
  gtdState.page = { epoch: page.epoch || "", rev: page.rev || 0 };
  if (page.counters) gtdState.counters = page.counters;
}

// loadGTD loads the catalog once, then the list of route (the hash's when
// not given, read once the catalog can resolve it) with its counters, or the
// counters alone on a plugin view.
async function loadGTD(given) {
  if (!gtdState.catalog) {
    try {
      gtdState.catalog = await getJSON("/api/gtd");
      gtdState.catalogError = "";
    } catch (err) {
      gtdState.catalogError = err.message;
      return;
    }
  }
  const route = given || readHash();
  if (route.view === "review" && isGTDView(route.view)) return loadReview();
  if (!isGTDView(route.view)) {
    try {
      gtdState.counters = await getJSON(countersURL(route.area));
    } catch {
      // The rail keeps its last counts.
    }
    return;
  }
  const key = gtdQueryKey(route.view, route.area, route.ctx);
  const cur = gtdState.page;
  const since = gtdState.key === key && cur && cur.epoch ? `${cur.epoch}:${cur.rev}` : "";
  try {
    applyEntriesPage(key, route.view, await getJSON(entriesURL(route.view, route.area, route.ctx, since)));
    gtdState.error = "";
    if (route.view === "inbox" && route.area) await areaCounters(route.area);
  } catch (err) {
    gtdState.error = `Could not load the ${route.view} list: ${err.message}`;
  }
}

// areaCounters are the rail's counts of area while Inbox, which ignores it,
// is shown; the Inbox count stays the page's.
async function areaCounters(area) {
  try {
    const c = await getJSON(countersURL(area));
    gtdState.counters = { ...c, views: { ...c.views, inbox: gtdState.counters.views.inbox } };
  } catch {
    // The rail keeps the page's counts.
  }
}

// gtdGroups splits a view's list: Next by context, Waiting by person,
// Logbook into Reference then Done; other views are one group.
function gtdGroups(view, entries) {
  const by = (keyOf, label) => {
    const groups = new Map();
    for (const e of entries) {
      const k = keyOf(e);
      if (!groups.has(k)) groups.set(k, []);
      groups.get(k).push(e);
    }
    return [...groups.keys()].sort((a, b) => (a === "") - (b === "") || a.localeCompare(b)).map((k) => ({ key: k, label: label(k), entries: groups.get(k) }));
  };
  if (view === "next") return by((e) => (e.contexts || [])[0] || "", (k) => k || "No context");
  if (view === "waiting") return by((e) => (((e.waiting || {}).on || [])[0] || {}).name || "", (k) => k || "Someone");
  if (view === "logbook") {
    return [["reference", "Reference"], ["done", "Done"]].map(([s, label]) => ({ key: s, label, entries: entries.filter((e) => e.state === s) })).filter((g) => g.entries.length);
  }
  return [{ key: "", label: "", entries }];
}

function unitLabel(u) {
  return !u ? "" : u.provider === "github" ? u.key : `${u.provider}:${u.key}`;
}

function shortDate(t) {
  const d = new Date(t);
  return isNaN(d) ? "" : d.toISOString().slice(0, 10);
}

function entryItem(e, open) {
  const go = () => writeHash({ entry: e.id, pr: "" });
  const waiting = ((e.waiting || {}).on || []).map((p) => p.name).filter(Boolean);
  return h("li", {
    class: "entry" + (e.id === open ? " sel" : "") + (e.changed ? " changed" : ""),
    tabindex: 0,
    "data-focus": "entry:" + e.id,
    "aria-selected": e.id === open ? "true" : "false",
    onclick: go,
    onkeydown: (ev) => {
      if ((ev.key === "Enter" || ev.key === " ") && ev.target === ev.currentTarget) {
        ev.preventDefault();
        go();
      }
    },
  },
    h("div", { class: "e-line" },
      e.focus ? h("span", { class: "star", title: e.focus_auto ? `Starred: ${e.focus_auto}` : "Starred" }, "★") : null,
      h("span", { class: "title" }, e.title || e.id),
      e.changed ? h("span", { class: "flag" }, "changed") : null),
    h("div", { class: "meta" },
      ...(e.contexts || []).map((c) => h("span", { class: "pill" }, c)),
      e.area ? h("span", { class: "pill area" }, e.area) : null,
      e.project_title ? h("span", { class: "proj" }, e.project_title) : null,
      e.deadline ? h("span", { class: "due" }, "due " + shortDate(e.deadline)) : null,
      e.snooze_until ? h("span", { class: "due" }, "until " + shortDate(e.snooze_until)) : null,
      waiting.length ? h("span", { class: "who" }, "on " + waiting.join(", ")) : null,
      ...(e.units || []).map((u, i) => h("span", { class: "ref" }, (e.unit_labels || [])[i] || unitLabel(u)))),
    projectFacts(e));
}

// everyLabel reads a Go duration of whole hours as days or hours.
function everyLabel(d) {
  const m = /^(\d+)h0m0s$/.exec(d || "");
  if (!m) return d || "";
  const hours = Number(m[1]);
  return hours % 24 === 0 ? `${hours / 24}d` : `${hours}h`;
}

// projectFacts are a project's next action, open actions and review.
function projectFacts(e) {
  if (e.kind !== "project") return null;
  return h("div", { class: "meta project-facts" },
    e.review_due ? h("span", { class: "flag" }, "review due") : null,
    e.next_action ? h("span", { class: "next" }, "next: " + (e.next_action_title || e.next_action)) : h("span", { class: "pill" }, "no next action"),
    h("span", { class: "pill" }, `${e.open_actions || 0} open`),
    e.sequential ? h("span", { class: "pill" }, "sequential") : null,
    e.review_every ? h("span", { class: "who" }, "every " + everyLabel(e.review_every)) : null,
    e.reviewed_at ? h("span", { class: "who" }, "reviewed " + localDate(e.reviewed_at)) : null);
}

// localDate is an instant's day where the browser is, unlike shortDate's UTC day for deadlines.
function localDate(t) {
  const d = new Date(t);
  return isNaN(d) ? "" : d.toLocaleDateString("en-CA");
}

function outboxItem(e, open) {
  return h("li", { class: "entry outbox" + (e.id === open ? " sel" : ""), "data-focus": "entry:" + e.id, tabindex: 0 },
    h("div", { class: "e-line" }, h("a", { href: "#" + gtdHash({ entry: e.id, pr: "" }), class: "title" }, e.title || e.id)),
    e.draft ? draftCard(e, "outbox") : null);
}

function gtdHash(change) {
  return typeof hashString === "function" && typeof readHash === "function" ? hashString({ ...readHash(), ...change }) : "";
}

function areaPicker(route) {
  const areas = (gtdState.catalog && gtdState.catalog.areas) || [];
  if (!areas.length) return null;
  if (route.view === "inbox") return h("span", { class: "lens-label" }, "Inbox shows every area");
  const counts = (gtdState.counters && gtdState.counters.areas) || {};
  return h("label", { class: "lens-pick" },
    h("span", { class: "lens-label" }, "Area"),
    h("select", { "data-focus": "gtd:area", onchange: (ev) => writeHash({ area: ev.target.value }) },
      h("option", { value: "", selected: !route.area }, "All areas"),
      ...areas.map((a) => h("option", { value: a, selected: route.area === a }, counts[a] === undefined ? a : `${a} (${counts[a]})`))));
}

function contextChips(route) {
  if (route.view !== "next") return null;
  const ctxs = (gtdState.catalog && gtdState.catalog.contexts) || [];
  const counts = (gtdState.counters && gtdState.counters.contexts) || {};
  return ctxs.map((c) => {
    const on = route.ctx.includes(c);
    const next = on ? route.ctx.filter((x) => x !== c) : [...route.ctx, c];
    return h("button", { type: "button", class: "chip" + (on ? " on" : ""), "aria-pressed": String(on), "data-focus": "gtd:ctx:" + c, onclick: () => writeHash({ ctx: next.join(",") }) },
      c, counts[c] === undefined ? "" : ` ${counts[c]}`);
  });
}

const GTD_EMPTY = {
  focus: "Nothing starred. Star an entry to keep it here.",
  inbox: "Inbox zero.",
  outbox: "No drafts wait for approval.",
  next: "No next actions.",
  waiting: "Waiting on nobody.",
  projects: "No open projects.",
  someday: "Nothing for someday.",
  logbook: "Nothing done or filed yet.",
};

// entryHaystack is what the filter's free words search in an entry: its title and notes, its items, the
// people it waits on, its conversations and repositories, its project, area and contexts, and its message
// or draft.
function entryHaystack(e) {
  const d = e.draft || {};
  const m = e.message || {};
  return [e.title, e.notes, e.area, e.project_title, ...(e.contexts || []),
    ...(e.items || []).map((it) => it.summary),
    ...((e.waiting || {}).on || []).map((p) => p.name),
    ...(e.unit_labels || []), ...(e.units || []).map((u) => `${u.provider}:${u.key}`),
    ...(e.cards || []).flatMap((c) => [((c.card || {}).last_human || {}).who ? personName(c.card.last_human.who) : "", ...((c.card || {}).waits_on || []).map(personName)]),
    m.text, d.text, d.where, d.reaction, d.unit].map((x) => gtdFold(x));
}

function gtdFold(s) {
  return String(s || "").normalize("NFD").replace(/\p{M}/gu, "").toLowerCase();
}

// gtdTerms are the free words of the filter query, as the pull request lists read them.
function gtdTerms(q) {
  if (typeof terms === "function") return terms(q);
  return (String(q || "").match(/[^\s"]*"[^"]*"?|[^\s"]+/g) || []).map((t) => gtdFold(t.replace(/^"|"$/g, ""))).filter(Boolean);
}

// entryMatches says whether every free word of ts is in e.
function entryMatches(e, ts) {
  if (!ts.length) return true;
  const hay = entryHaystack(e);
  return ts.every((t) => hay.some((f) => f.includes(t)));
}

// gtdList is the list of a core view.
function gtdList(route, open) {
  const label = (gtdViews().find((v) => v.id === route.view) || {}).label || route.view;
  const parts = [h("div", { class: "tools gtd-tools" }, h("h2", { class: "gtd-head" }, label), areaPicker(route), contextChips(route))];
  if (gtdState.error) parts.push(h("p", { class: "plan-error" }, gtdState.error));
  if (gtdState.key !== gtdQueryKey(route.view, route.area, route.ctx)) {
    parts.push(h("p", { class: "empty" }, "Loading."));
    return parts;
  }
  if (!gtdState.entries.length) {
    parts.push(h("p", { class: "empty" }, GTD_EMPTY[route.view] || "Nothing here."));
    return parts;
  }
  const ts = gtdTerms(route.q);
  const shown = gtdState.entries.filter((e) => entryMatches(e, ts));
  if (!shown.length) {
    parts.push(h("p", { class: "empty" }, `Nothing here matches "${route.q}".`));
    return parts;
  }
  const item = route.view === "outbox" ? outboxItem : entryItem;
  for (const g of gtdGroups(route.view, shown)) {
    if (g.label) parts.push(h("h3", { class: "plan-head" }, `${g.label} (${g.entries.length})`));
    parts.push(h("ol", { class: "entries" }, ...g.entries.map((e) => item(e, open))));
  }
  return parts;
}

function renderGTD(route) {
  const box = document.getElementById("gtd");
  const on = isGTDView(route.view);
  box.hidden = !on;
  if (route.view !== "review") reviewState.step = "";
  if (!on) {
    box.replaceChildren();
    return;
  }
  box.replaceChildren(...(route.view === "review" ? reviewPanel(route.entry) : gtdList(route, route.entry)));
}
