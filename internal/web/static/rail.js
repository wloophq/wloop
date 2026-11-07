"use strict";

// railEnv is what the rail's omnibox entry runs: the actions palette, until the omnibox takes its place.
const railEnv = {
  omnibox: () => {
    if (typeof cockpitKeys === "object" && cockpitKeys) cockpitKeys.openPalette();
  },
};

const railState = { emptiesOpen: false };

// viewNav is the rail: the core views with their counters, then each plugin
// view (count left to the caller).
function viewNav(cat, served, plugin, counters) {
  const views = (counters && counters.views) || {};
  const out = coreViews(cat).map((v) => ({ id: v.id, label: v.label, summary: v.summary || "", count: views[v.id] === undefined ? "" : views[v.id], core: true,
    offRail: v.rail === false }));
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
      by.set(v.provider, { title: v.provider ? name(v.provider) : t("Plugins"), views: [] });
      out.push(by.get(v.provider));
    }
    by.get(v.provider).views.push(v);
  }
  return out.filter((sec) => sec.views.length);
}

function railItem(label, count, active, hash, cls, tip) {
  const tipID = tip ? "rail-tip-" + hash.replace(/[^A-Za-z0-9_-]/g, "-") : undefined;
  return h("li", {},
    h("a", { href: "#" + hash, class: "nav " + (active ? "on " : "") + (cls || ""), "aria-current": active ? "page" : undefined, "aria-describedby": tipID, "data-focus": "rail:" + hash },
      h("span", { class: "label", title: tip ? undefined : label }, label),
      h("span", { class: "count" }, typeof count === "number" ? fmtNumber(count) : count),
      tip ? h("span", { class: "rail-tip", role: "tooltip", id: tipID }, tip) : null));
}

// lensHash is route's hash with change over it, keeping its lens.
function lensHash(change, route = readHash()) {
  const { q, g, s, layout, view, area } = route;
  return hashString({ view, q, g, s, layout, area, ...change });
}

// railCount is what the rail counts of the entry v under lens: a core view's counter, the plan's steps, the
// rows of a row view the lens keeps; "" for a view it does not count.
function railCount(v, lens) {
  if (v.core) return v.count;
  if (v.id === "plan") return planCount();
  const rows = catalog.views.some((c) => c.id === v.id) && v.id !== "recent" && v.id !== "explore";
  return rows ? state.rows.filter((r) => passes(r, v.id, lens)).length : "";
}

// railNav is viewNav's list for lens, each entry with what the rail counts of it.
function railNav(lens) {
  return viewNav(tasksState.catalog, catalog.views, [...pluginViews.keys()], viewCounters(readHash())).map((v) => ({ ...v, count: railCount(v, lens) }));
}

// railFolds says whether the rail folds the entry v into its Empty entry: it counts nothing and is not the view
// shown.
const railFolds = (v, current) => v.count === 0 && v.id !== current;

// railViews are the rail's entries: each section's views under its provider's name, but the empty ones, which
// one Empty entry counts and lists in place when opened, and the views the profile keeps off the rail, unless
// one is the view shown.
function railViews(all, route) {
  const nav = all.filter((v) => !v.offRail || v.id === route.view);
  const item = (v) => railItem(v.label, v.count, v.id === route.view, lensHash({ view: v.id, task: "", pr: "" }, route),
    (v.id === "stalled" ? "late-count " : "") + (v.core ? "core" : "plugin"), v.summary);
  const empties = nav.filter((v) => railFolds(v, route.view));
  const home = railItem(t("Home"), "", route.view === HOME, "view=" + HOME, "core",t("What waits on you, and what stalled the longest"));
  const views = [home, ...railSections(nav.filter((v) => !railFolds(v, route.view)), providerName).flatMap((sec) => [
    sec.title ? h("li", { class: "rail-head", role: "presentation" }, sec.title) : null,
    ...sec.views.map(item)])];
  if (!empties.length) return views.filter(Boolean);
  const open = railState.emptiesOpen;
  const toggle = h("li", {}, h("button", {
    type: "button", class: "nav rail-empties", "aria-expanded": String(open), "data-focus": "rail:empties",
    onclick: () => {
      railState.emptiesOpen = !open;
      renderRail(all, route);
      const again = document.querySelector('[data-focus="rail:empties"]');
      if (again) again.focus();
    },
  }, h("span", { class: "label" }, t("Empty")), h("span", { class: "count" }, fmtNumber(empties.length))));
  return [...views, toggle, ...(open ? empties.map(item) : [])].filter(Boolean);
}

// railPeople are the people the rows of lens wait on, most rows first, each a link that filters by them.
function railPeople(lens, route) {
  const counts = new Map();
  for (const r of state.rows) {
    if (!passes(r, lens.view, lens, "waits-on")) continue;
    for (const w of waitingWho(r)) counts.set(w, (counts.get(w) || 0) + 1);
  }
  const people = [...counts.entries()].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]));
  return people.map(([who, n]) => {
    const on = lens.quals.some((x) => x.field === "waits-on" && !x.neg && x.value === normValue("waits-on", who));
    return railItem(who, n, on, lensHash({ q: toggledQualifier(lens.q, "waits-on", who, false) }, route));
  });
}

// railOmnibox labels the omnibox entry and binds it to railEnv.omnibox.
function railOmnibox(box) {
  if (!box) return;
  box.replaceChildren(h("span", { class: "label" }, t("Search or ask")), h("kbd", {}, "⌘K"));
  box.onclick = () => railEnv.omnibox();
}

// renderRail draws the rail of nav for route: the omnibox entry, the views by section with the empty ones
// folded, and whom the rows shown wait on.
function renderRail(nav, route) {
  railOmnibox(document.getElementById("omnibox-entry"));
  document.getElementById("views").replaceChildren(...railViews(nav, route));
  document.getElementById("people").replaceChildren(...railPeople(lensOf(route), route));
}
