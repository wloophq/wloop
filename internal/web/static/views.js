"use strict";

// REVIEW is the route and the key context of the review, a mode of the cockpit that walks the catalog's review
// views; reviewTitle is its title.
const REVIEW = "review";

function reviewTitle() {
  return t("Review");
}

const tasksState = { catalog: null, catalogError: "", key: "", page: null, units: [], tasks: [], gates: [], loading: [], counters: null, filtered: null, filterSeq: 0, error: "" };

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
  if (view === HOME || view === QUEUE || view === PLAN) return view;
  if (view &&((!cat && !served.length) || isCoreView(view, cat) || served.some((v) => v.id === view) || plugin(view))) return view;
  if (!cat && served.length) return served[0].id;
  return (coreViews(cat)[0] || served[0] || { id: "" }).id;
}

// tasksURL lists view, narrowed to area; a page since a position also
// holds the tasks of units, as provider:key.
function tasksURL(view, area, ctx, since, units) {
  const p = new URLSearchParams({ view });
  if (area) p.set("area", area);
  if (ctx && ctx.length) p.set("context", ctx.join(","));
  if (since) p.set("since", since);
  for (const u of (since && units) || []) p.append("units", u);
  return "/api/tasks?" + p.toString();
}

// FILTER_COUNT_DELAY_MS lets a burst of keystrokes ask for the counts once.
const FILTER_COUNT_DELAY_MS = 250;

// countersURL counts area, every area when "", narrowed to the tasks the
// lens query q keeps.
function countersURL(area, q) {
  const p = new URLSearchParams();
  if (area) p.set("area", area);
  if (q) p.set("q", q);
  const query = p.toString();
  return "/api/counters" + (query ? "?" + query : "");
}

// filterQuery is the filter as the daemon reads it, qualifiers and all.
function filterQuery(route) {
  return String(route.q || "").trim();
}

// viewCounters are the counts the rail, the area picker and the context
// chips show: with a filter, those of the tasks it keeps,
// the last read while a newer one is on its way; null before the first.
function viewCounters(route) {
  if (!filterQuery(route)) return tasksState.counters;
  const f = tasksState.filtered;
  return f && f.area === (route.area || "") ? f.counters : null;
}

// filteredKey names the counts of the filter of route in its area.
function filteredKey(route) {
  return [route.area || "", filterQuery(route)].join("\u0000");
}

// loadFiltered reads the counters the filter narrows, once
// the query stops changing for FILTER_COUNT_DELAY_MS; only the latest read
// is kept, and a failed one leaves the rail without counts.
async function loadFiltered(route) {
  const q = filterQuery(route);
  const seq = ++tasksState.filterSeq;
  if (!q) {
    tasksState.filtered = null;
    return;
  }
  const key = filteredKey(route);
  if (!tasksState.filtered || tasksState.filtered.key !== key) await new Promise((done) => setTimeout(done, FILTER_COUNT_DELAY_MS));
  if (seq !== tasksState.filterSeq) return;
  try {
    const c = await getJSON(countersURL(route.area, q));
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
  tasksState.loading = page.loading || [];
  tasksState.page = { epoch: page.epoch || "", rev: page.rev || 0 };
  if (page.counters) tasksState.counters = page.counters;
}

// loadTasks loads the catalog once, then the list of route (the hash's when
// not given, read once the catalog can resolve it) with its counters, or the
// counters alone on a plugin view, and the counters the filter narrows.
// units are those a change named, whose tasks the next page since the list's
// position holds.
async function loadTasks(given, units) {
  if (units) tasksState.units = [...new Set([...tasksState.units, ...units])];
  if (!tasksState.catalog && !(await readViews())) return;
  const route = given || readHash();
  await Promise.all([loadTaskList(route), loadFiltered(route)]);
}

// readViews reads the catalog of the task views; false when it failed.
async function readViews() {
  try {
    tasksState.catalog = await getJSON("/api/views");
    tasksState.catalogError = "";
    return true;
  } catch (err) {
    tasksState.catalogError = err.message;
    return false;
  }
}

// forgetTasksPage makes the next list read whole.
function forgetTasksPage() {
  tasksState.page = null;
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
  const units = tasksState.units;
  let page;
  try {
    page = await getJSON(tasksURL(route.view, route.area, route.ctx, since, units));
    applyTasksPage(key, page);
    tasksState.units = tasksState.units.filter((u) => !units.includes(u));
    tasksState.error = "";
  } catch (err) {
    tasksState.error = t("Could not load the {view} list: {error}", { view: route.view, error: err.message });
    return;
  }
  if ((viewSpec(route.view) || {}).layout === OUTBOX_LAYOUT) await loadOutbox();
  if (page.specs && page.specs !== (tasksState.catalog || {}).version) await readViews();
}

function unitLabel(u) {
  return !u ? "" : `${u.provider}:${u.key}`;
}

function shortDate(at) {
  const d = new Date(at);
  return isNaN(d) ? "" : d.toISOString().slice(0, 10);
}

// roleProp is the catalog's property the role acts on (area, contexts, star, deadline); null when none does.
function roleProp(role, cat = tasksState.catalog) {
  return ((cat && cat.props) || []).find((p) => p.role === role) || null;
}

// roleValue is e's value of the property the role acts on; undefined when none does.
function roleValue(e, role) {
  const p = roleProp(role);
  return p ? (e.props || {})[p.name] : undefined;
}

// starred says whether e holds the first value, starred, of the property the star role acts on.
function starred(e) {
  const p = roleProp("star");
  return !!p && (p.values || []).length > 0 && (e.props || {})[p.name] === p.values[0];
}

// taskModel is a task as the row renderer lays it out: its facets placed by the profile's task specs, then
// what no facet carries. A draft's own task shows its draft under its title and opens from its title alone.
function taskModel(e, open, copy, lens) {
  const w = lens.words || {};
  const item = { facets: e.facets || {} };
  const parts = rowParts(item, lens.specs, w);
  const strip = stripOf(item, lens.specs);
  const draft = isDraftTask(e);
  const due = roleValue(e, "deadline");
  const scope = taskSlot(e.id, copy);
  const labels = (e.units || []).map((u, i) => (e.unit_labels || [])[i] || unitLabel(u));
  const stateSpec = (lens.specs || []).find((s) => s.fact === "task.state" && !s.provider);
  const statePiece = stateSpec && (parts.kind || []).find((p) => p.field === stateSpec.id);
  if (statePiece) parts.kind = parts.kind.filter((p) => p !== statePiece);
  return {
    key: e.id, scope, item, parts, words: w, selected: e.id === open,
    open: draft ? null : () => writeHash({ task: e.id, pr: "" }),
    href: draft ? "#" + viewHash({ task: e.id, pr: "" }) : "",
    title: titleWithoutRef(e.title || e.id, labels),
    glyph: statePiece ? { category: e.category || "", label: statePiece.label, field: statePiece.field, value: statePiece.value }
      : { category: e.category || "", label: stateLabel(e.state) },
    changed: !!e.changed,
    drafts: e.drafts || [],
    draftMark: draftMark(e),
    pre: starred(e) ? h("span", { class: "star", title: w.starred }, "★ ") : null,
    ref: labels.map((label) => whereRef(label, item, lens, scope, w)),
    flags: [
      due ? h("span", { class: "flag due" }, fillWord(w.due, { date: shortDate(due) })) : null,
      e.wake && e.wake.until ? h("span", { class: "flag due" }, fillWord(w.until, { date: shortDate(e.wake.until) })) : null,
      e.extraction_lag ? h("span", { class: "flag due", title: w.unread_title }, fillWord(w.unread, { lag: shortLag(e.extraction_lag) })) : null],
    after: [projectFacts(e, w), draft ? draftCard(e, draftOf(e), { scope: "outbox" }) : null],
    lead: stripDot(strip, lens, scope, w),
    why: e.reason || "",
    cls: ["task-row", strip ? "w-" + strip.color : "", e.changed ? "changed" : "", draft ? "outbox" : "", ...rowClasses(item, lens.specs)],
  };
}

// whereRef is a unit's label on a task's row, a button that filters by the where value it names when the task
// holds one: the longest the label ends with, or holds after its owner, as "#chan" or "owner/repo#7" do.
function whereRef(label, item, lens, scope, w) {
  const named = ((item.facets || {}).where || []).filter((v) => v && (label.endsWith(v) || label.includes(v + "#")))
    .sort((a, b) => b.length - a.length)[0];
  const text = h("span", { class: "unit" }, marked(label, lens.ts));
  return named ? facet(lens, "where", named, text, `facet:${scope}:where:${named}`, { words: w, rove: true }) : text;
}

// taskList is the lens's groups of tasks as rows.
function taskList(res, lens, open) {
  return rowList(res, (it, copy) => taskModel(it.task, open, copy, lens), lens, "task-list");
}

// shortLag reads a Go duration as its largest unit: 2h, 12m, 30s.
function shortLag(d) {
  const m = /^(?:(\d+)h)?(?:(\d+)m)?(?:([\d.]+)s)?$/.exec(d || "");
  if (!m) return d || "";
  if (m[1]) return m[1] + "h";
  if (m[2]) return m[2] + "m";
  return Math.round(Number(m[3] || 0)) + "s";
}

// everyLabel reads a Go duration of whole hours as days or hours.
function everyLabel(d) {
  const m = /^(\d+)h0m0s$/.exec(d || "");
  if (!m) return d || "";
  const hours = Number(m[1]);
  return hours % 24 === 0 ? `${hours / 24}d` : `${hours}h`;
}

// projectFacts are a project's open children and review, in the row words w.
function projectFacts(e, w) {
  if ((e.props || {}).kind !== "project") return null;
  const open = (e.children || []).length;
  return h("div", { class: "meta project-facts" },
    e.review_due ? h("span", { class: "flag" }, w.review_due) : null,
    open ? null : h("span", { class: "pill" }, w.no_next_action),
    h("span", { class: "pill" }, fillWord(w.open_children, { n: open })),
    e.sequential ? h("span", { class: "pill" }, w.sequential) : null,
    e.review_every ? h("span", { class: "who" }, fillWord(w.every, { every: everyLabel(e.review_every) })) : null,
    e.reviewed_at ? h("span", { class: "who" }, fillWord(w.reviewed, { date: localDate(e.reviewed_at) })) : null);
}

// localDate is an instant's day where the browser is, unlike shortDate's UTC day for deadlines.
function localDate(at) {
  const d = new Date(at);
  return isNaN(d) ? "" : d.toLocaleDateString("en-CA");
}

// draftOf is the draft of e's draft gate, its subject; null when e carries none.
function draftOf(e) {
  const g = ((e && e.gates) || []).find((x) => x.kind === "draft" && x.subject && typeof x.subject === "object");
  return g ? g.subject : null;
}

// messageOf is the message of e's first open question, post or proposal gate, its subject; null when e carries none.
function messageOf(e) {
  const g = ((e && e.gates) || []).find((x) => x.status === "open" && ["question", "post", "proposal"].includes(x.kind) && x.subject && typeof x.subject === "object");
  return g ? g.subject : null;
}

// isDraftTask says whether e is a draft's own task, which shows its draft.
function isDraftTask(e) {
  const d = draftOf(e);
  return !!d && e.id === "draft:" + d.id;
}

// taskSlot is the focus slot of a task's row, or of one of its copies.
function taskSlot(id, copy) {
  return copy ? `task:${id}@${copy}` : `task:${id}`;
}

// nextTaskNode is the task row delta rows from the focused one among nodes, else from the open task's first
// copy, else the first.
function nextTaskNode(nodes, active, openID, delta) {
  if (!nodes.length) return null;
  let at = nodes.indexOf(active);
  if (at < 0 && openID) at = nodes.findIndex((n) => n.dataset.focus === taskSlot(openID) || n.dataset.focus.startsWith(taskSlot(openID) + "@"));
  return nodes[Math.max(0, Math.min(nodes.length - 1, at < 0 ? 0 : at + delta))];
}

function viewHash(change) {
  return typeof hashString === "function" && typeof readHash === "function" ? hashString({ ...readHash(), ...change }) : "";
}

function areaPicker(route) {
  const areas = (tasksState.catalog && tasksState.catalog.areas) || [];
  if (!areas.length) return null;
  const counts = (viewCounters(route) || {}).areas || {};
  return h("label", { class: "lens-pick" },
    h("span", { class: "lens-label" }, t("Area")),
    h("select", { "data-focus": "view:area", onchange: (ev) => writeHash({ area: ev.target.value }) },
      h("option", { value: "", selected: !route.area }, t("All areas")),
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
// (api.TaskView.Texts): its title, notes, state, the text and the lists of text of its properties, its units,
// who owes it, its asks, its cards' people, its draft, its message and its facets' values, by facet id.
function taskHaystack(e) {
  const d = draftOf(e) || {};
  const m = messageOf(e) || {};
  const props = Object.values(e.props || {}).flatMap((v) => typeof v === "string" ? [v] : Array.isArray(v) && v.every((x) => typeof x === "string") ? v : []);
  return [e.title, e.notes, e.state, ...props,
    ...(e.units || []).flatMap((u, i) => [(e.unit_labels || [])[i] || unitLabel(u), `${u.provider}:${u.key}`]),
    e.owed_by || "",
    ...(e.asks || []).flatMap((a) => [a.summary, a.asker]),
    ...(e.cards || []).flatMap((c) => [((c.card || {}).last_human || {}).who ? personName(c.card.last_human.who) : "", ...((c.card || {}).waits_on || []).map(personName)]),
    d.target, d.where, d.text, d.reaction, d.unit, m.text,
    ...Object.keys(e.facets || {}).sort().flatMap((id) => e.facets[id])].map((x) => fold(x));
}

const taskItems = new WeakMap();

// taskItemOf is e as the lens reads an item: its facets and the texts the daemon's counters search.
function taskItemOf(e) {
  let it = taskItems.get(e);
  if (!it) taskItems.set(e, (it = { task: e, facets: e.facets || {}, texts: taskHaystack(e) }));
  return it;
}

// taskLensItems are the loaded tasks as the lens's items, in the daemon's order.
function taskLensItems() {
  return tasksState.tasks.map(taskItemOf);
}

// viewPage is a core view's page: its head, the lens over its tasks with what it shows (null while it loads,
// for a list of gates or the Outbox), and its body, drawn in the view's served layout. The lens counts what the filter shows of the view, as a pull
// request list's bar does.
function viewPage(route, open) {
  const label = viewLabel(route.view);
  const loaded = tasksState.key === viewQueryKey(route.view, route.area, route.ctx);
  const spec = viewSpec(route.view);
  const n = loaded ? tasksState.tasks.length : 0;
  const head = [h("div", { class: "tools tasks-tools" }, h("h2", { class: "tasks-head" }, label), areaPicker(route), contextChips(route),
    spec && spec.remove_clean ? h("button", {
      type: "button", class: "act", "data-focus": "tasks:remove-clean", disabled: n === 0 || removeState.busy,
      title: t("Remove the workspaces of this view's tasks whose checkouts hold nothing unpublished"),
      onclick: () => removeAllClean(n, route.view),
    }, t("Remove all clean")) : null), viewIntro(spec)];
  const page = { head, lens: lensOf(route), res: null, body: [] };
  const body = page.body;
  if (tasksState.error) body.push(h("p", { class: "plan-error" }, tasksState.error));
  if (loaded) body.push(...loadingNotes(tasksState.loading));
  if (!loaded) {
    body.push(h("p", { class: "empty" }, t("Loading.")));
    return page;
  }
  if (spec && spec.layout === OUTBOX_LAYOUT) {
    if (outboxState.error) body.push(h("p", { class: "plan-error" }, outboxState.error));
    const groups = (outboxState.page && outboxState.page.groups) || [];
    body.push(groups.length ? outboxPage(outboxState.page) : h("p", { class: "empty" }, outboxState.page ? spec.empty : t("Loading.")));
    return page;
  }
  if (spec && spec.over === "gates") {
    body.push(tasksState.gates.length ? h("div", { class: "gate-list" }, ...tasksState.gates.map((g) => gateCard(taskById(g.task), g))) : h("p", { class: "empty" }, spec.empty));
    return page;
  }
  page.res = pipelineOf(taskLensItems(), page.lens, null);
  if (!tasksState.tasks.length) {
    body.push(h("p", { class: "empty" }, spec && spec.empty));
    return page;
  }
  if (!page.res.shown) {
    body.push(h("p", { class: "empty" }, t("Nothing here matches \"{query}\".", { query: route.q })));
    return page;
  }
  if (page.lens.layout === "board") body.push(taskBoard(page.res, page.lens, open));
  else body.push(taskList(page.res, page.lens, open));
  return page;
}

// taskBoard is a lane per group of the lens, each holding its tasks' cards.
function taskBoard(res, lens, open) {
  return h("div", { class: "board task-board" }, ...res.groups.filter((g) => g.rows.length)
    .map((g) => lane(g, lens, viewLabel(lens.view), (it, copy) => rowView(taskModel(it.task, open, copy, lens), lens, { card: true }))));
}

// viewIntro is a view's summary under its heading, and the rule its profile lists it by behind a toggle.
function viewIntro(spec) {
  if (!spec || !spec.summary) return null;
  const rule = [[t("Lists"), spec.when], [t("Sorted by"), spec.sort], [t("Grouped by"), spec.group]].filter(([, v]) => v);
  return h("div", { class: "view-intro" },
    h("p", { class: "view-summary" }, spec.summary),
    rule.length ? h("details", { class: "view-rule" },
      h("summary", {}, t("Why these tasks?")),
      h("dl", {}, ...rule.flatMap(([k, v]) => [h("dt", {}, k), h("dd", {}, h("code", {}, v))]))) : null);
}

// loadingNotes say which plugins have not reported all their units yet, and why, so a short list reads as
// incomplete rather than empty.
function loadingNotes(loading) {
  return (loading || []).map((l) => h("p", { class: "loading", "data-plugin": l.plugin }, l.reason ? t("Loading {plugin}: {reason}", { plugin: l.plugin, reason: l.reason }) : t("Loading {plugin}", { plugin: l.plugin })));
}

// viewLabel is the profile's word for a core view.
function viewLabel(view) {
  return (coreViews().find((v) => v.id === view) || {}).label || view;
}

// loadRoundGates keeps the open plan gates with a deadline by unit: the automatic rounds a row counts down to.
async function loadRoundGates() {
  try {
    const gs = await getJSON("/api/gates?status=open&kind=plan");
    state.roundGates = new Map((gs || []).filter((g) => g.kind === "plan" && g.unit && isSet(g.deadline)).map((g) => [g.unit, g]));
  } catch {
    // The rows still show their situation; only the countdown waits for the next load.
  }
}

const removeState = { busy: false };

// removeAllClean removes the clean workspaces the n tasks of a view that offers it hold, and says what it removed
// and what it kept, with why.
async function removeAllClean(n, view) {
  if (!confirm(tn("Remove the workspaces of the {n} tasks in {view}? Their checkouts are deleted and their terminals are closed. A workspace whose agent works or waits, or that holds uncommitted files, unpushed commits or ignored files outside build and dependency folders, is kept.", n, { view: viewLabel(view) }))) return;
  removeState.busy = true;
  state.actionError = "";
  state.actionDetail = "";
  render();
  try {
    const res = await fetch("/api/workspaces/remove-clean", {
      method: "POST",
      headers: { "Content-Type": "application/json", Accept: "application/json", "X-Loop-API": API_VERSION },
      body: JSON.stringify({ view }),
    });
    const out = await res.json().catch(() => ({}));
    if (!res.ok || out.error) throw new Error(apiError(out, `HTTP ${res.status}`, res.status));
    const removed = (out.removed || []).map(labelOf);
    const kept = (out.kept || []).map((k) => `${labelOf(k)} (${k.reason})`);
    const details = (out.kept || []).filter((k) => k.detail).map((k) => `${labelOf(k)}: ${k.detail}`);
    const text = [
      removed.length ? tn("Removed {n}: {list}.", removed.length, { list: removed.join(", ") }) : tn("Removed {n}.", 0),
      kept.length ? tn("Kept {n}: {list}.", kept.length, { list: kept.join("; ") }) : "",
    ].filter(Boolean).join(" ");
    setNotice(text);
    document.getElementById("notice").title = text;
    if (details.length) {
      state.actionError = tn("Kept {n} with details", details.length);
      state.actionSource = "";
      state.actionDetail = details.join("\n");
    }
  } catch (err) {
    state.actionError = t("remove all clean failed: {error}", { error: err.message });
  }
  removeState.busy = false;
  scheduleLoad({ all: true, tasks: true });
}

function renderTasks(route) {
  const box = document.getElementById("tasks");
  const head = document.getElementById("tasks-head");
  const tools = document.getElementById("task-tools");
  const body = document.getElementById("tasks-body");
  const on = isCoreView(route.view);
  box.hidden = !on;
  if (route.view !== REVIEW) reviewState.step = "";
  if (!on || route.view === REVIEW) {
    head.replaceChildren();
    hideLens(tools);
    body.replaceChildren(...(on ? reviewPanel(route.task) : []));
    if (on) box.setAttribute("aria-label", reviewTitle());
    return;
  }
  box.setAttribute("aria-label", viewLabel(route.view));
  const page = viewPage(route, route.task);
  head.replaceChildren(...page.head);
  if (page.res) renderLens(page.lens, page.res, tools);
  else hideLens(tools);
  replaceKeepingScroll(body, page.body, [route.view, route.q, route.g, route.s, route.layout].join("\n"));
}

// replaceKeepingScroll replaces body's children and, while the page is the same, keeps where its board was
// scrolled: a refresh builds a new board, which would start at its origin.
function replaceKeepingScroll(body, children, page) {
  const old = body.querySelector(":scope > .board");
  const at = old && body.dataset.page === page ? [old.scrollLeft, old.scrollTop] : null;
  body.replaceChildren(...children);
  body.dataset.page = page;
  const board = body.querySelector(":scope > .board");
  if (at && board) [board.scrollLeft, board.scrollTop] = at;
}
