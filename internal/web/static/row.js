"use strict";

// The one row every list draws, task and plugin rows alike, and its board card. A row model (taskModel,
// rowModel) holds the lens's pieces by part and what no facet carries; rowView lays them on two lines:
//   line 1: glyph, kind verbs, title, changed dot, draft marker | meta and flag pieces, actions
//   line 2: ref, why                                            | aside, strip dot, who, the time rule's date

// ROW_PART_CLASS is the class each part's pieces read in; a part not named reads in its own name.
const ROW_PART_CLASS = { meta: "pill", flag: "flag", kind: "verb", aside: "col" };

// pieceLabel is a piece's text: an age piece's label template gets its time as the viewer's date.
function pieceLabel(p) {
  return p.template ? p.label.replaceAll("{{.at}}", fmtDate(p.raw, "datetime")) : p.label;
}

// pieceTitle is an age piece's tooltip: how long ago, then the exact time.
function pieceTitle(p, words) {
  const at = Date.parse(p.raw);
  return Number.isNaN(at) ? undefined : `${ageText(words, Date.now() - at)} (${stamp(p.raw)})`;
}

// pieceButtons are a part's pieces, each a facet button whose label marks the search's hits; the piece that
// counts the rest past show_max holds no value and stays text.
function pieceButtons(m, lens, part) {
  const pieces = m.parts[part] || [];
  return pieces.map((p) => {
    if (!p.value) return h("span", { class: ROW_PART_CLASS[part] || part, "data-field": p.field, "data-more": "", title: restOf(m, lens, pieces, p.field).join(", ") || undefined }, p.label);
    const span = h("span", { class: ROW_PART_CLASS[part] || part, "data-field": p.field, "data-value": p.value, title: agePart(part) ? pieceTitle(p, m.words) : undefined },
      marked(pieceLabel(p), lens.ts));
    return facet(lens, p.field, p.value, span, `facet:${m.scope}:${p.field}:${p.value}`, { words: m.words, noun: (m.nouns || {})[`${p.field}:${p.value}`], rove: true });
  });
}

// restOf are the words of the values of field the model holds that pieces, a part's pieces, leave out past its
// show_max.
function restOf(m, lens, pieces, field) {
  const shown = new Set(pieces.filter((p) => p.field === field && p.value).map((p) => p.raw));
  const labels = ((lens.specs || []).find((s) => s.id === field) || {}).labels || {};
  return ((m.item.facets || {})[field] || []).filter((v) => !shown.has(v)).map((v) => labels[v] || v);
}

// markWrapped makes inert each child of box that wrapped below its first line, which the row never shows, so
// neither Tab nor the row's arrows reach it.
function markWrapped(box) {
  const kids = [...box.children];
  const top = kids.length ? kids[0].offsetTop : 0;
  for (const k of kids) k.inert = k.offsetTop > top;
}

const wrapWatch = typeof ResizeObserver === "function" ? new ResizeObserver((es) => es.forEach((e) => markWrapped(e.target))) : null;

// watchWrapped keeps the pieces and actions root's rows hide out of the focus order, as they wrap and unwrap.
function watchWrapped(root) {
  for (const box of root.querySelectorAll(".row > .meta > .pieces, .row > .meta > .acts")) {
    markWrapped(box);
    if (wrapWatch) wrapWatch.observe(box);
  }
}

function joined(nodes) {
  return nodes.flatMap((n, i) => (i ? [", ", n] : [n]));
}

// titleWithoutRef is title without a leading unit label of labels and the separator after it; the whole
// title when nothing would be left.
function titleWithoutRef(title, labels) {
  const s = String(title || "");
  for (const label of labels || []) {
    if (!label || !s.startsWith(label)) continue;
    const rest = s.slice(label.length).replace(/^\s*[:\-–—|]?\s*/, "");
    if (rest && rest !== s.slice(label.length)) return rest;
  }
  return s;
}

// rowGlyph is a model's state glyph: a CSS shape by its state's category, in its waiter's hue, titled with
// its state; a facet button that filters by the state when the lens offers it. A unit has no state, so no glyph.
function rowGlyph(m, lens) {
  const g = m.glyph;
  if (!g) return null;
  const shape = h("span", { class: "glyph", "data-category": g.category || "", "data-field": g.field || undefined, "data-value": g.value || undefined, title: g.label || undefined, "aria-label": g.label || undefined, role: "img" });
  if (!g.field) return shape;
  return facet(lens, g.field, g.value, shape, `facet:${m.scope}:${g.field}:${g.value}`, { words: m.words, label: g.label, rove: true });
}

// rowTitle is a model's title on its row, a link when the model opens from its title alone.
function rowTitle(m, lens) {
  const text = marked(m.title, lens.ts);
  return h("span", { class: "t", title: m.titleTip || m.title || undefined },
    m.href ? h("a", { href: m.href, "data-focus": m.scope + ":title", ...roveStop(true), onclick: (e) => e.stopPropagation() }, text) : text);
}

// rowChanged is the dot of a model that changed since its viewer last saw it.
function rowChanged(m) {
  if (!m.changed) return null;
  const label = t("Changed since you saw it");
  return h("span", { class: "ch", role: "img", title: label, "aria-label": label });
}

// draftMark is the marker of a task whose drafts wait for the viewer; null without one.
function draftMark(e) {
  const n = ((e && e.drafts) || []).length;
  if (!n) return null;
  const label = tn("{n} draft waits for you", n);
  return h("span", { class: "draft-mark", role: "img", title: label, "aria-label": label }, fmtNumber(n));
}

// rowLine1 is a row's first line: its title, which leads it, the dot of a change and its drafts' marker.
function rowLine1(m, title) {
  return h("div", { class: "l1" }, m.pre || null, title, rowChanged(m), m.draftMark || null);
}

// rowRight1 is the right side of a row's first line: its kind, meta and flag pieces, then its actions.
function rowRight1(m, lens, card) {
  return h("div", { class: "meta" },
    h("span", { class: "pieces" }, pieceButtons(m, lens, "kind"), pieceButtons(m, lens, "meta"), m.meta || null, pieceButtons(m, lens, "flag"), m.flags || null),
    card || !m.actions ? null : h("span", { class: "acts" }, m.actions));
}

// rowLine2 is a row's second line: its ref and why it waits.
function rowLine2(m, lens) {
  return h("div", { class: "l2" },
    h("span", { class: "ref" }, pieceButtons(m, lens, "ref"), (m.ref || []).map((x) => (typeof x === "string" ? marked(x, lens.ts) : x))),
    m.why ? h("span", { class: "why", title: m.why }, marked(m.why, lens.ts)) : null);
}

// rowAside is a row's workspace, agents and links, before whom it waits on.
function rowAside(m, lens) {
  const bits = [m.asideLead || null, pieceButtons(m, lens, "aside"), ...(m.aside || [])].flat().filter(Boolean);
  return bits.length ? h("span", { class: "aside" }, bits) : null;
}

// rowWhen is whom a model waits on and since when: its strip's dot, its who pieces, then the time rule's
// date with its label, or a pending round's countdown.
function rowWhen(m, lens) {
  return h("span", { class: "when" },
    m.lead || null,
    h("span", { class: "who" }, joined(pieceButtons(m, lens, "who"))),
    m.age || pieceButtons(m, lens, "age"));
}

// rowView is a model as a list row, or with opts.card as a board's card: the same slots either way. A row is
// the control that opens it; a card's title is, stretched over the card.
function rowView(m, lens, opts = {}) {
  const card = !!opts.card;
  const cls = [card ? "card" : "row", ...(m.cls || []), m.selected ? "sel" : ""].filter(Boolean).join(" ");
  const right2 = h("div", { class: "r2" }, card ? null : rowAside(m, lens), rowWhen(m, lens));
  if (card) {
    const label = h("span", { class: "t", title: m.titleTip || undefined }, marked(m.title, lens.ts));
    const at = { class: "open", "data-focus": m.scope, "data-rove": "", title: m.tip || undefined, "aria-current": m.selected ? "true" : undefined };
    const title = m.open ? h("button", { type: "button", ...at, onclick: m.open }, label) : m.href ? h("a", { ...at, href: m.href }, label) : label;
    return h("li", { class: cls, "data-row": m.scope, onkeydown: roveKey },
      rowLine1(m, title), rowGlyph(m, lens), rowRight1(m, lens, true), rowLine2(m, lens), right2, m.after || null);
  }
  const open = m.open;
  return h("div", {
    class: cls,
    title: m.tip || undefined,
    tabindex: 0,
    "aria-current": m.selected ? "true" : undefined,
    "data-focus": m.scope,
    "data-row": m.scope,
    "data-rove": "",
    onclick: open || undefined,
    onkeydown: (e) => {
      if (roveKey(e) || !open) return;
      if ((e.key === "Enter" || e.key === " ") && e.target === e.currentTarget) {
        e.preventDefault();
        open();
      }
    },
  }, rowGlyph(m, lens), rowLine1(m, rowTitle(m, lens)), rowRight1(m, lens, false), rowLine2(m, lens), right2,
  [m.after].flat().some(Boolean) ? h("div", { class: "after" }, m.after) : null);
}

// rowList is res's groups of items as rows, model drawing each: under a sticky head per group when the lens
// groups, the head a collapse toggle with its count and an "only this" filter; cls names the kind of list.
function rowList(res, model, lens, cls = "") {
  const grouped = !!res.group;
  const multi = grouped && multiCopy(res.group);
  return h("div", { class: cls ? "rows " + cls : "rows" }, res.groups.filter((g) => g.rows.length).map((g, i) => {
    const key = `${g.field}:${g.key}`;
    const collapsed = grouped && state.collapsed.has(key);
    const rows = g.rows.map((it) => {
      const row = rowView(model(it, multi ? g.key : ""), lens);
      row.hidden = collapsed;
      return row;
    });
    if (!grouped) return rows;
    const id = `g-${lens.view}-${i}`;
    return h("section", { class: "group", id, "aria-label": g.label },
      h("div", { class: "g-head" + (g.field && g.field === lens.strip ? " w-" + stripColor(lens.stripSpec, g.key) : "") },
        h("button", { type: "button", class: "g-toggle", "aria-expanded": String(!collapsed), "aria-controls": id, "data-focus": `group:${key}`, onclick: () => toggleCollapsed(key) },
          `${collapsed ? "▸" : "▾"} ${g.label}`),
        h("span", { class: "count" }, fmtNumber(g.rows.length)),
        onlyThis(g, lens)),
      rows);
  }));
}
