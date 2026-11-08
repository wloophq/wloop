"use strict";

// boundKey is the key the served bindings give action, "" until they load or when nothing binds it, so a hint
// never names a key the dispatcher does not run.
function boundKey(action) {
  return typeof cockpitKeys === "object" && cockpitKeys ? cockpitKeys.keyOf(action) : "";
}

// ariaKey names key as aria-keyshortcuts spells it.
function ariaKey(key) {
  return key === "+" ? "Plus" : key;
}

function sortLabel(sort, fields = catalog.fields) {
  const { field, desc } = parseSort(sort, fields);
  return field ? `${fieldById(field, fields).label.toLowerCase()} ${desc ? "descending" : "ascending"}` : "";
}

// lensItems are what lens's view lists before the lens: a task view's tasks, a row view's rows.
function lensItems(lens) {
  if (typeof isCoreView === "function" && isCoreView(lens.view)) return taskLensItems();
  return state.rows.filter((r) => inView(r, lens.view, lens.done));
}

// lensResult is what lens shows of its view: a task view's tasks in the daemon's order, a row view's rows.
function lensResult(lens) {
  if (typeof isCoreView === "function" && isCoreView(lens.view)) return pipelineOf(taskLensItems(), lens, null);
  return pipeline(state.rows, lens);
}

async function copyList(lens) {
  const res = lensResult(lens);
  const items = groupedRows(res.groups);
  const grouped = res.group ? groupField(res.group, lens.fields).label.toLowerCase() : "";
  const w = lensWords();
  try {
    await navigator.clipboard.writeText(listYAML({ view: lens.view, filter: lens.q, group_by: grouped, sort: sortLabel(lens.sort, lens.fields), at: new Date().toISOString() }, items));
    setNotice(fillWord(grouped ? w.copied_grouped : w.copied, { count: items.length, view: lens.view, group: grouped }));
  } catch (err) {
    setNotice(t("Could not copy: {error}", { error: err.message }), true);
  }
}

function chip(attrs, label) {
  return h("button", { type: "button", class: "chip", ...attrs }, label);
}

function setGroup(g) {
  const lens = { ...readLens(), group: g };
  const res = lensResult(lens);
  writeHash({ g });
  if (res.group) setNotice(tn("Grouped by {field}: {n} groups", res.groups.filter((x) => x.rows.length).length, { field: groupField(res.group, lens.fields).label.toLowerCase() }));
}

function clearLens() {
  lastFocus = "top:search";
  writeHash({ q: "", g: "", s: "" });
}

// valueLabel is how a filter names a value of the field id: a kind by its label, any other value as shown.
function valueLabel(id, value, shown) {
  return id === "kind" ? kindLabel(value) : shown;
}

// lensChips are one chip per filtered field and sign, which takes it out.
function lensChips(lens, applied) {
  const by = new Map();
  for (const x of lens.quals) {
    const k = `${x.field}:${x.neg ? "-" : ""}`;
    if (!by.has(k)) by.set(k, { field: x.field, neg: x.neg, values: [] });
    by.get(k).values.push(valueLabel(x.field, x.value, x.raw));
  }
  const list = [...by.values()];
  return list.map((c, i) => {
    const f = fieldById(c.field, lens.fields);
    const off = applied && !applied.includes(c.field);
    const next = list[i + 1] || list[i - 1];
    return chip({
      class: "chip pill on" + (off ? " off" : ""), "data-focus": `lens:chip:${c.field}:${c.neg ? "-" : ""}`,
      title: off ? t("Not applied in Recent") : t("Remove this filter ({filter})", { filter: c.values.map((v) => qualifierToken(c.field, v, c.neg)).join(" ") }),
      "aria-label": t("Remove filter {label}", { label: chipWords(f.label, c.values, c.neg, false) }),
      onclick: () => {
        lastFocus = next ? `lens:chip:${next.field}:${next.neg ? "-" : ""}` : "lens:add-filter";
        writeHash({ q: withoutQualifiers(lens.q, c.field, c.neg, lens.fields) });
      },
    }, chipWords(f.label, c.values, c.neg, true));
  });
}

// chipWords are what a filter's chip says of its field and values, with its × when removable.
function chipWords(label, values, neg, removable) {
  const vars = { field: label, values: values.join(" " + t("or") + " ") };
  if (!removable) return neg ? t("{field}: not {values}", vars) : t("{field}: {values}", vars);
  return neg ? t("{field}: not {values} ×", vars) : t("{field}: {values} ×", vars);
}

// stripLegend is the hues of lens's strip facet, in its rank, when fields offer it.
function stripLegend(lens, fields) {
  const s = lens.stripSpec;
  if (!s || !s.rank || (fields && !fields.some((x) => x.id === s.id))) return null;
  return h("span", { class: "legend", title: (lens.words || lensWords()).legend_title || undefined },
    h("span", { class: "lens-label" }, `${s.label}:`),
    s.rank.map((v) => h("span", { class: "lg w-" + stripColor(s, v) }, h("span", { class: "sw", "aria-hidden": "true" }), (s.labels || {})[v] || v)));
}

function lensSelect(id, label, value, options, onchange, title) {
  return h("label", { class: "pill lens-pick" }, h("span", { class: "lens-label" }, label),
    h("select", { "data-focus": `lens:${id}`, title: title || undefined, onchange: (e) => onchange(e.target.value) },
      options.map(([v, text]) => h("option", { value: v, selected: v === value }, text))));
}

// sortFields are the fields the Sort chip offers beside the view's own order: every sortable field lens
// offers, the strip's and each placed facet's among them.
function sortFields(lens) {
  return (lens.offered || catalog.fields).filter((f) => f.sort);
}

// filterFields are the fields "+ Filter" offers: those lens offers that filter and that some of items holds.
function filterFields(lens, items) {
  return (lens.offered || catalog.fields).filter((f) => f.values && f.menu !== false && items.some((it) => (f.values(it) || []).some(Boolean)));
}

// filterValues are the values of f that items hold, in f's rank, then the most held first, each with its label
// and how many items hold it.
function filterValues(f, items) {
  const counts = new Map();
  for (const it of items) for (const v of new Set((f.values(it) || []).filter(Boolean))) counts.set(v, (counts.get(v) || 0) + 1);
  const rank = f.rank || [];
  const at = (v) => (rank.includes(v) ? rank.indexOf(v) : rank.length);
  return [...counts.entries()].sort((a, b) => at(a[0]) - at(b[0]) || b[1] - a[1] || String(a[0]).localeCompare(String(b[0])))
    .map(([value, count]) => ({ value, count, label: valueLabel(f.id, value, groupLabel(f, value)) }));
}

// filterPalette opens, through palette, the fields lens filters by, then the values of the one picked, which
// adds it to the query; false when items hold no field to filter by.
function filterPalette(lens, items, palette) {
  const fields = filterFields(lens, items);
  if (!fields.length) return false;
  const pickValue = (f, x) => {
    lastFocus = "lens:add-filter";
    writeHash({ q: toggledQualifier(lens.q, f.id, x.value, false, lens.fields) });
  };
  palette({
    title: t("Filter"),
    items: fields.map((f) => ({
      label: f.label, key: "",
      run: () => palette({ title: f.label, items: filterValues(f, items).map((x) => ({ label: x.label, meta: fmtNumber(x.count), key: "", run: () => pickValue(f, x) })) }),
    })),
  });
  return true;
}

// openFilterPalette opens "+ Filter" over the list shown, when it shows a toolbar that offers it.
function openFilterPalette() {
  const button = document.querySelector('[data-focus="lens:add-filter"]');
  if (!button || button.closest("[hidden]")) return false;
  const lens = readLens();
  return filterPalette(lens, lensItems(lens), showPalette);
}

// renderLens draws the toolbar in tools, #tools by default. It rebuilds only when what it shows changed, so an
// open select survives live updates.
function renderLens(lens, res, tools = document.getElementById("tools")) {
  const recent = lens.view === "recent";
  if (lens.view === "explore" || (recent && !lens.quals.length)) {
    hideLens(tools);
    return;
  }
  tools.hidden = false;
  const offered = (lens.offered || catalog.fields).map((f) => f.id);
  const sig = JSON.stringify([lens.view, lens.q, lens.group, lens.sort, lens.layout, res && res.shown, res && res.total, state.health && state.health.viewer, offered,
    lens.defaultGroup, lens.boardGroup, lens.defaultSort, lensWords(), boundKey("filter:add")]);
  if (tools.dataset.sig === sig) return;
  tools.dataset.sig = sig;
  const clear = h("button", { type: "button", class: "act", "data-focus": "lens:clear", onclick: clearLens }, t("Clear"));
  tools.replaceChildren(...(recent ? [...lensChips(lens, ACTIVITY_FIELDS), clear] : lensToolbar(lens, res)));
}

function hideLens(tools) {
  tools.hidden = true;
  tools.replaceChildren();
  tools.dataset.sig = "";
}

// lensToolbar is the toolbar's controls: the Group and Sort chips over the fields lens offers, a removable chip
// per filter, "+ Filter", the strip's legend, then the count, the list/board switch and Copy list.
function lensToolbar(lens, res) {
  const offered = lens.offered || catalog.fields;
  const fields = lens.fields || catalog.fields;
  const current = tagKeyField(lens.group, fields);
  const groups = offered.filter((f) => f.group && f.menu !== false);
  if (current && !groups.some((f) => f.id === current.id)) groups.push(current);
  const board = lens.layout === "board";
  const w = lensWords();
  const filterKey = boundKey("filter:add");
  const own = lens.defaultGroup || (board ? lens.boardGroup : "");
  const ownField = own ? groupField(own, fields) : null;
  const { field, desc } = parseSort(lens.sort, fields);
  const sortWord = lens.defaultSort;
  const toggle = (layout, label) => h("button", {
    type: "button", "aria-pressed": String(board === (layout === "board")), "data-focus": `lens:${layout || "list"}`,
    onclick: () => writeHash({ layout }),
  }, label);
  const items = [
    lensSelect("group", t("Group"), lens.group, [
      ["", ownField ? fillWord(lens.defaultGroup ? w.default_group : w.board_group, { label: ownField.label }) : w.no_group],
      ...(ownField ? [[NO_GROUP, w.no_group]] : []),
      ...groups.map((f) => [f.id, f.label])], setGroup),
    h("span", { class: "pill sort-pick" },
      lensSelect("sort", t("Sort"), field, [["", sortWord], ...sortFields(lens).map((f) => [f.id, f.label])],
        (v) => writeHash({ s: v }), field ? fillWord(w.sort_by, { label: fieldById(field, fields).label.toLowerCase() }) : ""),
      h("button", {
        type: "button", class: "dir", "data-focus": "lens:dir", disabled: !field, "aria-label": desc ? t("Descending") : t("Ascending"),
        title: field ? (desc ? t("Sorted descending; click to flip") : t("Sorted ascending; click to flip")) : t("Pick a sort to choose its direction"),
        onclick: () => writeHash({ s: field + (desc ? "" : "-desc") }),
      }, desc ? "↓" : "↑")),
    ...lensChips(lens),
    h("button", {
      type: "button", class: "pill add", "data-focus": "lens:add-filter", "aria-keyshortcuts": filterKey ? ariaKey(filterKey) : undefined,
      onclick: () => openFilterPalette(),
    }, t("Filter"), filterKey ? [" ", h("kbd", {}, filterKey)] : null),
    stripLegend(lens, offered),
    h("span", { class: "lens-count" }, res ? t("{shown} of {total}", { shown: fmtNumber(res.shown), total: fmtNumber(res.total) }) : ""),
    lens.q.trim() || lens.group || field ? h("button", { type: "button", class: "act", "data-focus": "lens:clear", onclick: clearLens }, t("Clear")) : null,
    h("div", { class: "seg", role: "group", "aria-label": t("Layout") }, toggle(servedLayout(lens.view) === "board" ? "list" : "", t("List")), toggle("board", t("Board"))),
    h("button", {
      type: "button", class: "pill", "data-focus": "lens:copy-list", title: t("Copy the items shown here, in this order, as YAML, to paste for an agent"),
      onclick: () => copyList(lens),
    }, t("Copy list")),
  ];
  return items.filter(Boolean);
}
