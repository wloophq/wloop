"use strict";

// REVIEW is the route and the key context of the review, a mode of the cockpit that walks the catalog's review
// views; REVIEW_TITLE is its title.
const REVIEW = "review";
const REVIEW_TITLE = "Review";
const tasksState = { catalog: null, catalogError: "", key: "", page: null, tasks: [], gates: [], counters: null, filtered: null, filterSeq: 0, error: "" };

function coreViews(cat = tasksState.catalog) {
  return (cat && cat.views) || [];
}

function isCoreView(id, cat = tasksState.catalog) {
  return id === REVIEW || coreViews(cat).some((v) => v.id === id);
}

function viewSpec(id, cat = tasksState.catalog) {
  return coreViews(cat).find((v) => v.id === id) || null;
}

// resolveView is the view a route shows: a core view, a served plugin
// view or a plugin's own web view; the catalog's first view otherwise.
// Before anything is loaded every view stands.
function resolveView(view, cat, served, plugin) {
  if (view && ((!cat && !served.length) || isCoreView(view, cat) || served.some((v) => v.id === view) || plugin(view))) return view;
  if (!cat && served.length) return served[0].id;
  return (coreViews(cat)[0] || served[0] || { id: "" }).id;
}

// viewNav is the rail: the core views with their counters, then each plugin
// view (count left to the caller).
function viewNav(cat, served, plugin, counters) {
  const views = (counters && counters.views) || {};
  const out = coreViews(cat).map((v) => ({ id: v.id, label: v.label, count: views[v.id] === undefined ? "" : views[v.id], core: true }));
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

// railSections splits viewNav's list: the core views untitled, then one
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

// tasksURL lists view, narrowed to area.
function tasksURL(view, area, ctx, since) {
  const p = new URLSearchParams({ view });
  if (area) p.set("area", area);
  if (ctx && ctx.length) p.set("context", ctx.join(","));
  if (since) p.set("since", since);
  return "/api/tasks?" + p.toString();
}

// FILTER_COUNT_DELAY_MS lets a burst of keystrokes ask for the counts once.
const FILTER_COUNT_DELAY_MS = 250;

// countersURL counts area, every area when "", narrowed to the tasks
// holding each of ts.
function countersURL(area, ts) {
  const p = new URLSearchParams();
  if (area) p.set("area", area);
  for (const t of ts || []) p.append("term", t);
  const q = p.toString();
  return "/api/counters" + (q ? "?" + q : "");
}

// viewCounters are the counts the rail, the area picker and the context
// chips show: with free words in the filter, those of the tasks it keeps,
// the last read while a newer one is on its way; null before the first.
function viewCounters(route) {
  if (!filterTerms(route.q).length) return tasksState.counters;
  const f = tasksState.filtered;
  return f && f.area === (route.area || "") ? f.counters : null;
}

// filteredKey names the counts of the free words of route in its area.
function filteredKey(route) {
  return [route.area || "", ...filterTerms(route.q)].join("\u0000");
}

// loadFiltered reads the counters the filter's free words narrow, once
// the words stop changing for FILTER_COUNT_DELAY_MS; only the latest read
// is kept, and a failed one leaves the rail without counts.
async function loadFiltered(route) {
  const ts = filterTerms(route.q);
  const seq = ++tasksState.filterSeq;
  if (!ts.length) {
    tasksState.filtered = null;
    return;
  }
  const key = filteredKey(route);
  if (!tasksState.filtered || tasksState.filtered.key !== key) await new Promise((done) => setTimeout(done, FILTER_COUNT_DELAY_MS));
  if (seq !== tasksState.filterSeq) return;
  try {
    const c = await getJSON(countersURL(route.area, ts));
    if (seq === tasksState.filterSeq) tasksState.filtered = { key, area: route.area || "", counters: c };
  } catch {
    if (seq === tasksState.filterSeq) tasksState.filtered = null;
  }
}

function viewQueryKey(view, area, ctx) {
  return [view, area || "", (ctx || []).join(",")].join("|");
}

// applyTasksPage folds a page into the list; a page of another query,
// epoch or a full page replaces it, an older one is dropped. A changed task
// keeps its place and a new one comes last.
function applyTasksPage(key, page) {
  const cur = tasksState.page;
  const same = tasksState.key === key && cur && cur.epoch === page.epoch;
  if (same && !page.full && page.rev < cur.rev) return;
  if (!same || page.full) {
    tasksState.tasks = page.tasks || [];
    tasksState.gates = page.gates || [];
  } else {
    const gone = new Set(page.removed || []);
    const fresh = new Map((page.tasks || []).map((e) => [e.id, e]));
    const kept = tasksState.tasks.filter((e) => !gone.has(e.id)).map((e) => {
      const next = fresh.get(e.id);
      fresh.delete(e.id);
      return next || e;
    });
    tasksState.tasks = [...kept, ...fresh.values()];
    if (page.gates) tasksState.gates = page.gates;
  }
  tasksState.key = key;
  tasksState.page = { epoch: page.epoch || "", rev: page.rev || 0 };
  if (page.counters) tasksState.counters = page.counters;
}

// loadTasks loads the catalog once, then the list of route (the hash's when
// not given, read once the catalog can resolve it) with its counters, or the
// counters alone on a plugin view, and the counters the filter narrows.
async function loadTasks(given) {
  if (!tasksState.catalog) {
    try {
      tasksState.catalog = await getJSON("/api/views");
      tasksState.catalogError = "";
    } catch (err) {
      tasksState.catalogError = err.message;
      return;
    }
  }
  const route = given || readHash();
  await Promise.all([loadTaskList(route), loadFiltered(route)]);
}

async function loadTaskList(route) {
  if (route.view === REVIEW) return loadReview();
  if (!isCoreView(route.view)) {
    try {
      tasksState.counters = await getJSON(countersURL(route.area));
    } catch {
      // The rail keeps its last counts.
    }
    return;
  }
  const key = viewQueryKey(route.view, route.area, route.ctx);
  const cur = tasksState.page;
  const since = tasksState.key === key && cur && cur.epoch ? `${cur.epoch}:${cur.rev}` : "";
  try {
    applyTasksPage(key, await getJSON(tasksURL(route.view, route.area, route.ctx, since)));
    tasksState.error = "";
  } catch (err) {
    tasksState.error = `Could not load the ${route.view} list: ${err.message}`;
  }
}



// groupValue reads fact, a task fact such as task.category or task.prop.size, from a task as the wire serves it:
// its first value when it holds several.
function groupValue(fact, e) {
  const name = String(fact || "").replace(/^task\./, "");
  let v;
  if (name.startsWith("prop.")) v = (e.props || {})[name.slice(5)];
  else if (name === "waiting_on") v = ((e.waiting || {}).on || []).map((p) => p.name || p.key);
  else v = e[name];
  if (Array.isArray(v)) v = v[0];
  if (v && typeof v === "object") v = v.name || v.key;
  return v === undefined || v === null ? "" : String(v);
}

// viewGroups splits a view's list by the fact its spec groups by; a view
// that names none is one group. Groups of a state or a category follow the
// catalog's states, with their labels; others sort by value. The empty
// value comes last.
function viewGroups(spec, tasks, cat = tasksState.catalog) {
  const fact = (spec && spec.group) || "";
  if (!fact) return [{ key: "", label: "", tasks }];
  const states = (cat && cat.states) || [];
  const name = fact.replace(/^task\./, "");
  const order = name === "state" ? states.map((s) => s.name) : name === "category" ? [...new Set(states.map((s) => s.category))] : [];
  const label = (k) => (!k ? "None" : name === "state" ? stateLabel(k) : k);
  const groups = new Map();
  for (const e of tasks) {
    const k = groupValue(fact, e);
    if (!groups.has(k)) groups.set(k, []);
    groups.get(k).push(e);
  }
  const rank = (k) => (order.includes(k) ? order.indexOf(k) : order.length);
  return [...groups.keys()].sort((a, b) => (a === "") - (b === "") || rank(a) - rank(b) || a.localeCompare(b))
    .map((k) => ({ key: k, label: label(k), tasks: groups.get(k) }));
}

function unitLabel(u) {
  return !u ? "" : `${u.provider}:${u.key}`;
}

function shortDate(t) {
  const d = new Date(t);
  return isNaN(d) ? "" : d.toISOString().slice(0, 10);
}

function taskItem(e, open) {
  const go = () => writeHash({ task: e.id, pr: "" });
  const waiting = ((e.waiting || {}).on || []).map((p) => p.name).filter(Boolean);
  return h("li", {
    class: "task-row" + (e.id === open ? " sel" : "") + (e.changed ? " changed" : ""),
    tabindex: 0,
    "data-focus": "task:" + e.id,
    "aria-selected": e.id === open ? "true" : "false",
    onclick: go,
    onkeydown: (ev) => {
      if ((ev.key === "Enter" || ev.key === " ") && ev.target === ev.currentTarget) {
        ev.preventDefault();
        go();
      }
    },
  },
    h("div", { class: "task-line" },
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

// isDraftTask says whether e is a draft's own task, which shows its draft.
function isDraftTask(e) {
  return !!e.draft && e.id === "draft:" + e.draft.id;
}

// listItem is a task's row: a draft's own task shows the draft to approve.
function listItem(e, open) {
  return isDraftTask(e) ? outboxItem(e, open) : taskItem(e, open);
}

function outboxItem(e, open) {
  return h("li", { class: "task-row outbox" + (e.id === open ? " sel" : ""), "data-focus": "task:" + e.id, tabindex: 0 },
    h("div", { class: "task-line" }, h("a", { href: "#" + viewHash({ task: e.id, pr: "" }), class: "title" }, e.title || e.id)),
    e.draft ? draftCard(e, "outbox") : null);
}

function viewHash(change) {
  return typeof hashString === "function" && typeof readHash === "function" ? hashString({ ...readHash(), ...change }) : "";
}

function areaPicker(route) {
  const areas = (tasksState.catalog && tasksState.catalog.areas) || [];
  if (!areas.length) return null;
  const counts = (viewCounters(route) || {}).areas || {};
  return h("label", { class: "lens-pick" },
    h("span", { class: "lens-label" }, "Area"),
    h("select", { "data-focus": "view:area", onchange: (ev) => writeHash({ area: ev.target.value }) },
      h("option", { value: "", selected: !route.area }, "All areas"),
      ...areas.map((a) => h("option", { value: a, selected: route.area === a }, counts[a] === undefined ? a : `${a} (${counts[a]})`))));
}

function contextChips(route) {
  if ((viewSpec(route.view) || {}).over === "gates") return null;
  const ctxs = (tasksState.catalog && tasksState.catalog.contexts) || [];
  const counts = (viewCounters(route) || {}).contexts || {};
  return ctxs.map((c) => {
    const on = route.ctx.includes(c);
    const next = on ? route.ctx.filter((x) => x !== c) : [...route.ctx, c];
    return h("button", { type: "button", class: "chip" + (on ? " on" : ""), "aria-pressed": String(on), "data-focus": "view:ctx:" + c, onclick: () => writeHash({ ctx: next.join(",") }) },
      c, counts[c] === undefined ? "" : ` ${counts[c]}`);
  });
}


// taskHaystack is what the filter's free words search in a task, the texts the daemon's counters search
// (api.TaskView.Texts): its title, notes, state, kind, area, project and contexts, its units, the people
// it waits on, its items, its cards' people, its draft and its message.
function taskHaystack(e) {
  const d = e.draft || {};
  const m = e.message || {};
  return [e.title, e.notes, e.state, e.kind, e.area, e.project_title, ...(e.contexts || []),
    ...(e.units || []).flatMap((u, i) => [(e.unit_labels || [])[i] || unitLabel(u), `${u.provider}:${u.key}`]),
    ...((e.waiting || {}).on || []).map((p) => p.name),
    ...(e.items || []).map((it) => it.summary),
    ...(e.cards || []).flatMap((c) => [((c.card || {}).last_human || {}).who ? personName(c.card.last_human.who) : "", ...((c.card || {}).waits_on || []).map(personName)]),
    d.target, d.where, d.text, d.reaction, d.unit, m.text].map((x) => foldText(x));
}

// FOLD_ACCENTS are the accents api.Fold folds; foldText folds as api.Fold does, so the daemon's counts
// match what the list shows.
const FOLD_ACCENTS = {
  á: "a", à: "a", â: "a", ã: "a", ä: "a", é: "e", è: "e", ê: "e", ë: "e", í: "i", ì: "i", î: "i", ï: "i",
  ó: "o", ò: "o", ô: "o", õ: "o", ö: "o", ú: "u", ù: "u", û: "u", ü: "u", ç: "c", ñ: "n",
};

function foldText(s) {
  return String(s || "").toLowerCase().replace(/[áàâãäéèêëíìîïóòôõöúùûüçñ]/g, (c) => FOLD_ACCENTS[c]).replace(/\p{Mn}/gu, "");
}

// filterTerms are the free words of the filter query, the qualifiers left to the pull request lists, as the
// daemon reads them.
function filterTerms(q) {
  const qualifier = typeof qualifierOf === "function" ? qualifierOf : () => null;
  return (String(q || "").match(/[^\s"]*"[^"]*"?|[^\s"]+/g) || []).filter((t) => !qualifier(t))
    .map((t) => foldText(t.replace(/^"|"$/g, "")).trim()).filter(Boolean);
}

// taskMatches says whether every free word of ts is in e.
function taskMatches(e, ts) {
  if (!ts.length) return true;
  const hay = taskHaystack(e);
  return ts.every((t) => hay.some((f) => f.includes(t)));
}

// taskList is the list of a core view; its heading counts what the filter
// shows of the view, as a pull request list's bar does.
function taskList(route, open) {
  const label = viewLabel(route.view);
  const loaded = tasksState.key === viewQueryKey(route.view, route.area, route.ctx);
  const ts = filterTerms(route.q);
  const shown = loaded ? tasksState.tasks.filter((e) => taskMatches(e, ts)) : [];
  const parts = [h("div", { class: "tools tasks-tools" }, h("h2", { class: "tasks-head" }, label), areaPicker(route), contextChips(route),
    loaded ? h("span", { class: "lens-count" }, `${shown.length} of ${tasksState.tasks.length}`) : null)];
  if (tasksState.error) parts.push(h("p", { class: "plan-error" }, tasksState.error));
  if (!loaded) {
    parts.push(h("p", { class: "empty" }, "Loading."));
    return parts;
  }
  const spec = viewSpec(route.view);
  if (spec && spec.over === "gates") {
    parts.push(tasksState.gates.length ? h("div", { class: "gate-list" }, ...tasksState.gates.map((g) => gateCard(taskById(g.task), g))) : h("p", { class: "empty" }, `Nothing in ${label}.`));
    return parts;
  }
  if (!tasksState.tasks.length) {
    parts.push(h("p", { class: "empty" }, `Nothing in ${label}.`));
    return parts;
  }
  if (!shown.length) {
    parts.push(h("p", { class: "empty" }, `Nothing here matches "${route.q}".`));
    return parts;
  }
  for (const g of viewGroups(spec, shown)) {
    if (g.label) parts.push(h("h3", { class: "plan-head" }, `${g.label} (${g.tasks.length})`));
    parts.push(h("ol", { class: "task-list" }, ...g.tasks.map((e) => listItem(e, open))));
  }
  return parts;
}

// viewLabel is the profile's word for a core view.
function viewLabel(view) {
  return (coreViews().find((v) => v.id === view) || {}).label || view;
}

function renderTasks(route) {
  const box = document.getElementById("tasks");
  const on = isCoreView(route.view);
  box.hidden = !on;
  if (route.view !== REVIEW) reviewState.step = "";
  if (!on) {
    box.replaceChildren();
    return;
  }
  box.setAttribute("aria-label", route.view === REVIEW ? REVIEW_TITLE : viewLabel(route.view));
  box.replaceChildren(...(route.view === REVIEW ? reviewPanel(route.task) : taskList(route, route.task)));
}
