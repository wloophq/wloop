"use strict";

const API_VERSION = "1";

// apiError is the daemon's refusal with the command it names to get past it.
// apiError reads a daemon error; a 426 means the daemon is newer than this
// page, which only a reload brings up to date.
function apiError(out, fallback, status) {
  if (status === 426) return t("this page is older than {name}'s daemon; reload this page", { name: "{{name}}" });
  const msg = out.error || fallback;
  return out.fix ? t("{error}; run: {fix}", { error: msg, fix: out.fix }) : msg;
}

const SIZES = ["xs", "s", "m", "l", "xl"];
const RISKS = ["low", "medium", "high"];
const TAG_CHIPS = 3;

// catalog is the daemon's served presentation (GET /api/catalog): its views
// for the web, the lens's fields built from its facets, and the situations'
// labels in rank order.
const catalog = { version: "", views: [], specs: [], fields: [], lens: [], reasons: [], kinds: new Map(), kindOrder: [], events: new Map(), eventData: new Map(), eventTones: new Map(), details: [], search: null };

// MATCH is how a facet's match compares a row's value v with a query q.
const MATCH = {
  suffix: (v, q) => v === q || v.endsWith("/" + q),
  prefix: (v, q) => v === q || v.startsWith(q + ":"),
  team: (v, q) => v === q || v === "team:" + q,
};

function applyCatalog(c) {
  catalog.version = c.version || "";
  catalog.views = (c.views || []).filter((v) => (v.surfaces || []).includes("web")).sort((a, b) => a.order - b.order);
  const sits = [...(c.situations || [])].sort((a, b) => a.rank - b.rank);
  catalog.kinds = new Map(sits.map((x) => [`${x.provider}:${x.kind}`, x.label]));
  catalog.kindOrder = [...new Set(sits.map((x) => x.kind))];
  catalog.specs = c.facets || [];
  catalog.fields = catalog.specs.map(fieldOf);
  catalog.lens = c.lens || [];
  catalog.reasons = c.reasons || [];
  catalog.events = new Map((c.events || []).map((e) => [e.kind, e.label]));
  catalog.eventData = new Map((c.events || []).filter((e) => e.data).map((e) => [e.kind, e.data]));
  catalog.eventTones = new Map((c.events || []).filter((e) => e.tone).map((e) => [e.kind, e.tone]));
  catalog.details = c.details || [];
  const searching = (c.views || []).find((v) => v.search) || {};
  catalog.search = searching.search || null;
  catalog.searchProvider = searching.provider || "";
}

// searchService is the service the served search runs on, as its plugin names it.
function searchService() {
  return providerName(catalog.searchProvider || "");
}

// exploreSearch is the served Explore search: its presets, filters, sorts
// and the review decisions' words.
function exploreSearch() {
  return catalog.search || { presets: [], filters: [], sorts: [], decisions: {}, no_decision: "", default_sort: "" };
}

function reviewDecision(decision) {
  const s = exploreSearch();
  return (s.decisions || {})[decision] || s.no_decision || "";
}

// eventLabel is an event kind in a few words, as the catalog serves it: a plugin's kinds and the core's.
function eventLabel(kind) {
  return catalog.events.get(kind) || kind.replaceAll("_", " ");
}

// eventTone is the tone the catalog draws an event kind in, "" for none.
function eventTone(kind) {
  return catalog.eventTones.get(kind) || "";
}

// loopEvent says whether an event kind is the loop's doing: its tone is the loop's.
function loopEvent(kind) {
  return eventTone(kind) === "loop";
}

function kindLabel(kind, provider) {
  const own = catalog.kinds.get(`${provider}:${kind}`);
  if (own) return own;
  for (const [k, label] of catalog.kinds) if (k.slice(k.indexOf(":") + 1) === kind) return label;
  return kind;
}

function rowKindLabel(r) {
  return kindLabel(r.kind, unitOf(r).provider);
}

// fieldOf is a served facet as the lens reads it. A field filters by values,
// which may be several, and groups and sorts by its first value, so a row
// sits in one group. A time facet's values are its buckets now.
function fieldOf(spec) {
  const f = { id: spec.id, label: spec.label, provider: spec.provider || "", fact: spec.fact || "", group: !!spec.group, sort: !!spec.sort && spec.menu !== false,
    menu: spec.menu !== false, rank: spec.rank, empty: spec.empty, match: MATCH[spec.match], buckets: spec.buckets && spec.buckets.length ? spec.buckets : null };
  if (spec.labels) f.show = (k) => spec.labels[k] || k;
  if (spec.match === "prefix") {
    f.groupBy = (raw) => "tag:" + raw.split(":")[0];
    f.keys = (it) => facetValues(it, spec.id);
  }
  if (spec.sort === "time") f.sortValue = (r) => facetTime(r, spec.id);
  f.values = f.buckets ? (r) => bucketsOf(f, r) : (r) => facetValues(r, spec.id);
  if (spec.id === "is") f.values = (r) => (dueNow(r) ? [...facetValues(r, "is"), "due"] : facetValues(r, "is"));
  return f;
}

function facetValues(r, id) {
  return ((r.facets || {})[id] || []).filter(Boolean);
}

// bucketsOf are a time facet's values now: each time's bucket, "none"
// without a time.
function bucketsOf(f, r) {
  const vs = facetValues(r, f.id).map((v) => bucketOf(f.buckets, v)).filter(Boolean);
  return vs.length ? vs : ["none"];
}

function facetTime(r, id) {
  const ms = Date.parse(facetValues(r, id)[0] || "");
  return Number.isNaN(ms) ? -Infinity : ms;
}

// goDuration reads a Go duration such as "72h", "-24h" or "1h30m" in ms.
function goDuration(s) {
  const m = /^([-+]?)((?:\d+(?:\.\d*)?(?:ns|us|µs|ms|s|m|h))+)$/.exec(String(s || ""));
  if (!m) return NaN;
  const unit = { ns: 1e-6, us: 1e-3, "µs": 1e-3, ms: 1, s: 1000, m: 60000, h: 3600000 };
  let ms = 0;
  for (const [, n, u] of m[2].matchAll(/(\d+(?:\.\d*)?)(ns|us|µs|ms|s|m|h)/g)) ms += Number(n) * unit[u];
  return m[1] === "-" ? -ms : ms;
}

// bucketOf is the first bucket whose within is above now - t, the one
// without within catching the rest; "" when no bucket takes iso.
function bucketOf(buckets, iso, now = Date.now()) {
  const at = Date.parse(iso);
  if (Number.isNaN(at)) return "";
  const delta = now - at;
  const b = buckets.find((x) => !x.within || delta < goDuration(x.within));
  return b ? b.id : "";
}

// dueNow is the flag "due": an upcoming instant of the row's facet due falls
// in its "due" bucket now.
function dueNow(r, now = Date.now()) {
  const f = fieldById("due");
  return !!f && !!f.buckets && facetValues(r, "due").some((iso) => bucketOf(f.buckets, iso, now) === "due");
}

// pl is a row's provider payload, empty for a row that has none.
function pl(r) {
  return r.payload || {};
}

function sm(r) {
  return r.summary || {};
}

const LOOP_ONLY_KEY = "loop.activity.loopOnly";
const DETAIL_WIDTH_KEY = "loop.detailWidth";
const DETAIL_WIDTH_MIN = 300;
const DETAIL_WIDTH_STEP = 16;

const UNIT_KEY = /^[a-z][a-z0-9-]*:\S{1,300}$/;
const QUERY_MAX = 200;
const REQUEST_TIMEOUT_MS = 15000;
const ARTIFACT_LINES = 40;
const PR_URL = /^https:\/\/github\.com\/([A-Za-z0-9-]+)\/([\w.-]+)\/pull\/(\d+)(?:[/?#]\S*)?$/;
const folded = new WeakMap();

const state = { roundGates: new Map(), specs: { version: "", byId: {} }, planSpecs: {}, plan: { items: [], closed: [], error: "", loaded: false }, collapsed: new Set(), collapsedFor: "", rows: [], epoch: "", rev: 0, activity: [], health: null, detail: null, checks: {}, checksLoading: new Set(), busy: new Set(), answered: new Map(), guidance: {}, conn: "connecting", authLost: false,
  explore: { result: null, busy: false, error: "", recent: [], sel: "", status: {} } };
let lastFocus = null;

function h(tag, attrs, ...children) {
  const node = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v === undefined || v === null || v === false) continue;
    if (k === "class") node.className = v;
    else if (k.startsWith("on")) node.addEventListener(k.slice(2), v);
    else node.setAttribute(k, v === true ? "" : String(v));
  }
  for (const c of children.flat(Infinity)) {
    if (c === undefined || c === null || c === false) continue;
    node.append(c instanceof Node ? c : document.createTextNode(String(c)));
  }
  return node;
}

function safeURL(u) {
  return typeof u === "string" && u.startsWith("https://") ? u : undefined;
}

function readHash() {
  const p = new URLSearchParams(location.hash.slice(1));
  const route = readRoute(location.hash);
  const view = resolveView(route.view, tasksState.catalog, catalog.views, (id) => pluginViews.has(id));
  return {
    view,
    queue: view === QUEUE ? route.queue : "",
    ids: view === QUEUE ? route.ids : [],
    pr: UNIT_KEY.test(route.unit) ? route.unit : "",
    task: route.task,
    area: route.area,
    ctx: route.ctx,
    layout: route.layout,
    q: (p.get("q") || "").slice(0, QUERY_MAX),
    g: (p.get("g") || "").slice(0, QUERY_MAX),
    s: (p.get("s") || "").slice(0, QUERY_MAX),
    x: view === "explore" ? (p.get("x") || "").slice(0, 256) : "",
    tk: view === "dash" && /^[a-z-]+$/.test(p.get("tk") || "") ? p.get("tk") : "",
    done: view === "dash" && p.get("done") === "1",
  };
}

// lensPicks are the group and the sort of route that view offers; one it does not offer gives way to the
// view's default, while the hash keeps it for the views that do.
function lensPicks(view, route, g, s) {
  const fields = offeredFields(view, route);
  return {
    g: g === NO_GROUP || (groupField(g, fields) || {}).group ? g : "",
    s: parseSort(s, fields).field ? s : "",
  };
}

function hashString(next) {
  const p = new URLSearchParams();
  const queue = next.view === QUEUE && next.queue;
  if (queue) p.set("queue", next.queue);
  if (queue && next.ids && next.ids.length) p.set("ids", next.ids.join(","));
  if (next.task) p.set("task", next.task);
  if (next.area) p.set("area", next.area);
  if (next.ctx && next.ctx.length) p.set("ctx", [].concat(next.ctx).join(","));
  if (next.layout) p.set("layout", next.layout);
  if (next.q) p.set("q", next.q);
  if (next.g) p.set("g", next.g);
  if (next.s) p.set("s", next.s);
  if (next.x && next.view === "explore") p.set("x", next.x);
  if (next.tk && next.view === "dash") p.set("tk", next.tk);
  if (next.done && next.view === "dash") p.set("done", "1");
  return routeHash(queue ? "" : next.view, next.pr, p);
}

function writeHash(change, replace) {
  const hash = hashString({ ...readHash(), ...change });
  if (!replace) {
    location.hash = hash;
    return;
  }
  history.replaceState(null, "", "#" + hash);
  render();
}

const unitKey = (u) => `${u.provider}:${u.key}`;
const unitOf = (x) => x.unit;
const keyOf = (x) => unitKey(unitOf(x));
const labelOf = (x) => x.label || "";
const refLabel = (ref) => `${ref.owner}/${ref.repo}#${ref.number}`;
const eventRef = (it) => it.label || "";

// fold lowers s and drops its accents as api.Fold does, so the lens, the daemon's counts and the terminal
// compare text the same way.
function fold(s) {
  return String(s || "").toLowerCase().replace(/[áàâãäéèêëíìîïóòôõöúùûüçñ]/g, (c) => c.normalize("NFD")[0]).replace(/\p{Mn}/gu, "");
}

// tokens splits a query on spaces, keeping a "quoted value" whole.
function tokens(q) {
  return String(q || "").match(/[^\s"]*"[^"]*"?|[^\s"]+/g) || [];
}

function unquote(s) {
  return s.replace(/^"|"$/g, "");
}

// terms are the free words of a query over fields, the text every field is searched for.
function terms(q, fields = catalog.fields) {
  return tokens(q).filter((tok) => !qualifierOf(tok, fields)).map((tok) => fold(unquote(tok))).filter(Boolean);
}

function fieldById(id, fields = catalog.fields) {
  return fields.find((f) => f.id === id);
}

// tagKeyField groups by one brief tag key, "tag:lang": a row sits under each
// of its tags of that key, or under "no lang" with none. Its group keys are
// whole tags, so no value, "none" included, reads as the empty group.
function tagKeyField(id, fields = catalog.fields) {
  const m = /^tag:([a-z-]+)$/.exec(id || "");
  const tag = fieldById("tag", fields);
  if (!m || !tag || tag.match !== MATCH.prefix) return null;
  const key = m[1];
  const w = lensWords();
  return {
    id, label: fillWord(w.tag_key, { label: tag.label, key }), empty: fillWord(w.tag_key_empty, { key }), group: true, tagKey: key,
    keys: (r) => facetValues(r, tag.id).filter((v) => v.startsWith(key + ":")),
  };
}

function groupField(id, fields = catalog.fields) {
  return fieldById(id, fields) || tagKeyField(id, fields);
}

function tagKeys(rows) {
  const keys = new Set();
  for (const r of rows) {
    for (const tag of facetValues(r, "tag")) {
      const at = tag.indexOf(":");
      if (at > 0) keys.add(tag.slice(0, at));
    }
  }
  return [...keys].filter((k) => /^[a-z-]+$/.test(k)).sort();
}

function normValue(field, raw) {
  const v = fold(unquote(String(raw || "")));
  return v === "@me" ? "me" : v;
}

// qualifierOf reads a field:value term of a field that filters; anything else
// is free text, so "fix:" or a URL stays a word.
function qualifierOf(token, fields = catalog.fields) {
  const m = /^(-?)([a-zA-Z][a-zA-Z.-]*):(.+)$/.exec(token);
  const f = m && fieldById(m[2].toLowerCase(), fields);
  if (!f || !f.values) return null;
  const value = normValue(f.id, m[3]);
  return value ? { field: f.id, value, raw: unquote(m[3]), neg: m[1] === "-" } : null;
}

function qualifiers(q, fields = catalog.fields) {
  return tokens(q).map((tok) => qualifierOf(tok, fields)).filter(Boolean);
}

function qualifierToken(field, raw, neg) {
  return `${neg ? "-" : ""}${field}:${/\s/.test(raw) ? `"${raw}"` : raw}`;
}

// toggledQualifier adds field:value, or -field:value when neg, or takes it
// out when the query holds it already; the opposite sign gives way.
function toggledQualifier(q, field, raw, neg, fields = catalog.fields) {
  const v = normValue(field, raw);
  let on = false;
  const kept = tokens(q).filter((tok) => {
    const x = qualifierOf(tok, fields);
    if (!x || x.field !== field || x.value !== v) return true;
    if (x.neg === !!neg) on = true;
    return false;
  });
  return (on ? kept : [...kept, qualifierToken(field, raw, neg)]).join(" ");
}

function withoutQualifiers(q, field, neg, fields = catalog.fields) {
  return tokens(q).filter((tok) => {
    const x = qualifierOf(tok, fields);
    return !x || x.field !== field || x.neg !== neg;
  }).join(" ");
}

// rowTexts are the texts a free word searches in a row.
function rowTexts(r) {
  return [r.title, labelOf(r), r.link, r.kind, rowKindLabel(r), sm(r).waits_on, sm(r).reason, reasonText(r), sm(r).line,
    ...(r.search || []), ...Object.values(r.facets || {}).flat()];
}

// haystack is an item's folded texts: its own, or a row's.
function haystack(it) {
  let f = folded.get(it);
  if (!f) {
    f = (it.texts || rowTexts(it)).map(fold);
    folded.set(it, f);
  }
  return f;
}

function matches(it, ts) {
  return ts.every((term) => haystack(it).some((f) => f.includes(term)));
}

function matchesActivity(it, ts) {
  const e = it.event;
  const fields = [it.title, eventRef(it), it.author, e.actor, eventLabel(e.kind), e.data].map(fold);
  return ts.every((term) => fields.some((f) => f.includes(term)));
}

// Recent applies the filters an event can answer: its repository and the
// author of its pull request.
const ACTIVITY_FIELDS = ["where", "author"];

function activityPasses(it, lens) {
  const filters = new Map([...lens.filters].filter(([id]) => ACTIVITY_FIELDS.includes(id)));
  return matchesActivity(it, lens.ts) && passesFilters({ facets: { where: activityRepos(it), author: [it.author || ""] } }, filters);
}

function activityRepos(it) {
  const m = /^(\S+\/\S+)#\d+$/.exec(it.label || "");
  return m ? [m[1]] : [];
}

// foldMap folds s and maps each folded position back to s, so a match in
// the folded text marks the right characters of the original.
function foldMap(s) {
  let out = "";
  const at = [];
  for (let i = 0; i < s.length;) {
    const ch = String.fromCodePoint(s.codePointAt(i));
    const f = fold(ch);
    for (let k = 0; k < f.length; k++) at.push(i);
    out += f;
    i += ch.length;
  }
  at.push(s.length);
  return { out, at };
}

function marked(text, ts) {
  const s = String(text || "");
  if (!ts.length || !s) return s;
  const { out, at } = foldMap(s);
  const hits = [];
  for (const term of ts) {
    for (let i = out.indexOf(term); i >= 0; i = out.indexOf(term, i + 1)) hits.push([at[i], at[i + term.length]]);
  }
  if (!hits.length) return s;
  hits.sort((a, b) => a[0] - b[0]);
  const spans = [];
  for (const [a, b] of hits) {
    const last = spans[spans.length - 1];
    if (last && a <= last[1]) last[1] = Math.max(last[1], b);
    else spans.push([a, b]);
  }
  const nodes = [];
  let pos = 0;
  for (const [a, b] of spans) {
    if (a > pos) nodes.push(s.slice(pos, a));
    nodes.push(h("mark", {}, s.slice(a, b)));
    pos = b;
  }
  if (pos < s.length) nodes.push(s.slice(pos));
  return nodes;
}

// rowByURL is the row whose link q is, or a page under that link such as
// its files or one comment.
function rowByURL(q) {
  const u = String(q || "").trim();
  if (!u.startsWith("https://")) return null;
  return state.rows.find((r) => r.link && (u === r.link || (u.startsWith(r.link) && "/?#".includes(u[r.link.length])))) || null;
}

function rowByKey(key) {
  const k = key.toLowerCase();
  return state.rows.find((r) => keyOf(r).toLowerCase() === k);
}

function typing(el) {
  if (el instanceof HTMLInputElement) return !["checkbox", "radio", "button", "submit", "reset"].includes(el.type);
  return el instanceof HTMLTextAreaElement || el instanceof HTMLSelectElement || !!(el && el.isContentEditable);
}

function isSet(iso) {
  return iso && !iso.startsWith("0001-01-01");
}

function age(since, now = Date.now()) {
  if (!isSet(since)) return "";
  const mins = Math.max(0, Math.floor((now - Date.parse(since)) / 60000));
  if (mins < 1) return t("now");
  if (mins < 60) return t("{minutes} min", { minutes: mins });
  const hours = Math.floor(mins / 60);
  if (hours < 24) return t("{hours}h {minutes}m", { hours, minutes: mins % 60 });
  return t("{days}d {hours}h", { days: Math.floor(hours / 24), hours: hours % 24 });
}

// left is a pending round's countdown to deadline in the row words w.
function left(deadline, w, now = Date.now()) {
  const s = Math.ceil((Date.parse(deadline) - now) / 1000);
  if (s <= 0) return w.countdown_sending || "";
  if (s <= 99) return fillWord(w.countdown_seconds, { s });
  return fillWord(w.countdown_minutes, { m: Math.floor(s / 60), s: String(s % 60).padStart(2, "0") });
}

// roundGate is the open plan gate an automatic round of the row waits on, until its deadline.
function roundGate(row) {
  return state.roundGates.get(keyOf(row));
}

function countdown(row) {
  const deadline = roundGate(row).deadline;
  const provider = unitOf(row).provider;
  const w = rowWords(provider);
  return h("span", { class: "age countdown", "data-deadline": deadline, "data-provider": provider, title: fillWord(w.countdown_title, { time: stamp(deadline) }) }, left(deadline, w));
}

function tickCountdowns() {
  for (const el of document.querySelectorAll(".countdown[data-deadline]")) el.textContent = left(el.dataset.deadline, rowWords(el.dataset.provider));
  for (const el of document.querySelectorAll(".plan-left[data-deadline]")) el.textContent = planWhen({ decision: "pending", default: "run", deadline: el.dataset.deadline });
}

function stamp(iso) {
  if (!isSet(iso)) return "";
  return fmtDate(iso, "datetime");
}

function resolveWho(row, w) {
  if (w !== "author") return w;
  const viewer = state.health && state.health.viewer;
  const author = pl(row).author || "";
  return viewer && author.toLowerCase() === viewer.toLowerCase() ? "me" : author;
}

function waitingWho(row) {
  return facetValues(row, "waits-on");
}

function waitingLabel(row) {
  return (sm(row).waits_on || "").split(", ").map((w) => resolveWho(row, w)).join(", ");
}

// stripSpec is the facet that colors the strip of provider's items among specs ("" for a task's), as that
// provider shows it; null when the provider has none.
function stripSpec(specs, provider) {
  for (const s of specs || []) {
    if (s.provider ? s.provider !== provider : s.displays && !(provider in s.displays)) continue;
    const spec = specFor(s, provider);
    if (spec.strip) return spec;
  }
  return null;
}

// stripColor is the hue a strip facet's value colors a strip with, its colors' keys matched folded as group
// keys are.
function stripColor(spec, value) {
  const v = fold(value);
  const hit = Object.entries((spec && spec.colors) || {}).find(([k]) => fold(k) === v);
  return hit ? hit[1] : "nobody";
}

// stripOf is an item's strip: its provider's strip facet, the item's first value of it, that value's hue and
// label; null when the provider has no strip or the item no value of it.
function stripOf(it, specs) {
  const spec = stripSpec(specs, it.provider || "");
  const value = spec ? ((it.facets || {})[spec.id] || []).find((v) => v && fold(v) !== "none") : "";
  return value ? { spec, value, color: stripColor(spec, value), label: (spec.labels || {})[value] || value } : null;
}

function rowStrip(row) {
  return stripOf({ facets: row.facets || {}, provider: unitOf(row).provider }, catalog.specs);
}

// waitCat is the value of a row's strip facet, "" when it has no strip.
function waitCat(row) {
  const s = rowStrip(row);
  return s ? s.value : "";
}

// waitColor is the hue of a row's strip.
function waitColor(row) {
  const s = rowStrip(row);
  return s ? s.color : "nobody";
}

function waitClass(row) {
  const s = rowStrip(row);
  return s ? "w-" + s.color : "";
}

// rowTip is a row's tooltip in the row words w: its strip value's label, whom it waits on unless that is you
// or nobody, its kind, then why.
function rowTip(strip, who, kind, why, w) {
  let tip = strip ? strip.label : "";
  if (tip && who && who !== "me" && who !== "nobody") tip = fillWord(w.tip_named, { strip: tip, who });
  if (kind) tip = tip ? fillWord(w.tip_kind, { tip, kind }) : kind;
  if (why) tip = tip ? fillWord(w.tip_why, { tip, why }) : why;
  return tip;
}

function isMuted(p) {
  const m = p.mute;
  return !!m && (m.until_state_change || (isSet(m.until) && Date.parse(m.until) > Date.now()));
}

// The backlog is every pull request still open; done joins it on request.
function inView(row, view, withDone) {
  if (view === "recent" || view === "explore" || view === "plan") return false;
  if (view === "dash") return (row.unit || {}).provider === (catalog.views.find((v) => v.id === view) || {}).provider && (withDone || !sm(row).closed);
  return view === "all" || (row.views || []).includes(view);
}

// isStalled is the row's stalled flag: past its threshold and in scope.
function isStalled(r) {
  return rowFlags(r).includes("stalled");
}

function rowFlags(r) {
  return fieldById("is") ? fieldById("is").values(r) : [];
}

function sinceTime(r) {
  const ms = Date.parse(sm(r).since);
  return Number.isNaN(ms) ? -Infinity : ms;
}

// upcoming are a row's next instants: the daemon's list, or else the
// deadline of the plan gate its automatic round waits on.
function upcoming(r) {
  const next = ((pl(r).facts || {}).next || []).filter((e) => isSet(e.at));
  const g = roundGate(r);
  if (g && !next.some((e) => e.what === "gate")) next.push({ at: g.deadline, what: "gate" });
  return next;
}

// fieldRank and fieldShow are a served facet's rank and shown text.
function fieldRank(id) {
  return (fieldById(id) || {}).rank || [];
}

function fieldShow(id, k) {
  const f = fieldById(id);
  return f && f.show ? f.show(k) : k;
}

// itemValuesCache holds the values of the current minute: a time facet's
// buckets move with the clock.
const itemValuesCache = { minute: -1, items: new WeakMap() };

// itemValues are an item's values of a field, folded, the way filters compare.
function itemValues(f, it) {
  const minute = Math.floor(Date.now() / 60000);
  if (itemValuesCache.minute !== minute) Object.assign(itemValuesCache, { minute, items: new WeakMap() });
  const cache = itemValuesCache.items;
  let m = cache.get(it);
  if (!m) cache.set(it, (m = new Map()));
  if (!m.has(f.id)) m.set(f.id, (f.values ? f.values(it) : []).filter(Boolean).map((v) => fold(v)));
  return m.get(f.id);
}

function itemKey(f, it) {
  return (f.key ? f.key(it) : (f.values(it) || [])[0]) || "none";
}

function filtersOf(quals, fields = catalog.fields) {
  const out = new Map();
  for (const x of quals) {
    if (!out.has(x.field)) out.set(x.field, { f: fieldById(x.field, fields), inc: [], exc: [] });
    out.get(x.field)[x.neg ? "exc" : "inc"].push(x.value);
  }
  return out;
}

// passesFilters ORs the values of one field and ANDs the fields; except
// leaves one field out, to count what picking another of its values shows.
function passesFilters(it, filters, except) {
  for (const [id, { f: own, inc, exc }] of filters) {
    if (id === except || (Array.isArray(except) && except.includes(id))) continue;
    const f = own || fieldById(id);
    const vs = itemValues(f, it);
    const hit = (q) => vs.some((v) => (f.match ? f.match(v, q) : v === q));
    if ((inc.length && !inc.some(hit)) || exc.some(hit)) return false;
  }
  return true;
}

function passes(r, view, lens, except) {
  return inView(r, view, lens.done) && matches(r, lens.ts) && passesFilters(r, lens.filters, except);
}

function parseSort(s, fields = catalog.fields) {
  const m = /^([a-z][a-z.-]*?)(-desc)?$/.exec(s || "");
  const f = m && fieldById(m[1], fields);
  return f && f.sort ? { field: f.id, desc: !!m[2] } : { field: "", desc: false };
}

// NO_GROUP is the pick that lists a view whose lens groups by default in one list.
const NO_GROUP = "none";

const lensMemo = { tasks: null, fields: null, viewsCat: null, offered: null };

// lensFields are the fields a query over view names: a task view's are the profile's task facets and the
// plugins' facets, one by id as the daemon counts with them; a row view's are the plugins'.
function lensFields(view) {
  if (typeof isCoreView !== "function" || !isCoreView(view)) return catalog.fields;
  const cat = tasksState.catalog;
  if (lensMemo.fields !== catalog.fields || lensMemo.viewsCat !== cat) {
    const byId = new Map(catalog.fields.map((f) => [f.id, f]));
    for (const spec of (cat && cat.facets) || []) byId.set(spec.id, fieldOf(spec));
    Object.assign(lensMemo, { fields: catalog.fields, viewsCat: cat, tasks: [...byId.values()], offered: null });
  }
  return lensMemo.tasks;
}

// fieldsFor are the fields of view's lens that some of its items has a value of, and a field per brief tag key
// they hold.
function fieldsFor(view, items) {
  const has = new Set();
  for (const it of items) for (const [id, vs] of Object.entries(it.facets || {})) if ((vs || []).some(Boolean)) has.add(id);
  return [...lensFields(view).filter((f) => has.has(f.id)), ...tagKeys(items).map((k) => tagKeyField("tag:" + k, lensFields(view))).filter(Boolean)];
}

// viewSource is what view lists before the lens, as loaded: a task view's tasks, a row view's rows, which
// items keeps to the view; null until they are loaded, or for a view the lens does not list.
function viewSource(view, route) {
  if (typeof isCoreView === "function" && isCoreView(view)) {
    const loaded = view !== REVIEW && tasksState.key === viewQueryKey(view, route.area, route.ctx);
    return loaded ? { all: tasksState.tasks, items: () => tasksState.tasks } : null;
  }
  if (!catalog.views.some((v) => v.id === view) || !state.loaded) return null;
  return { all: state.rows, items: () => state.rows.filter((r) => inView(r, view, route.done)) };
}

// offeredFields are what the menus of view's lens offer: the fields its items have, every field of its lens
// until they are loaded.
function offeredFields(view, route) {
  const src = viewSource(view, route);
  if (!src) return lensFields(view);
  const key = [view, route.done, src.all, lensFields(view)];
  const m = lensMemo.offered;
  if (m && m.key.every((k, i) => k === key[i])) return m.fields;
  const fields = fieldsFor(view, src.items());
  lensMemo.offered = { key, fields };
  return fields;
}

// viewGroupField is the field a task view groups by unless the user picks another: the facet reading its
// group fact.
function viewGroupField(view) {
  const spec = typeof viewSpec === "function" ? viewSpec(view) : null;
  if (!spec || !spec.group) return "";
  return (lensFields(view).find((f) => f.fact === spec.group) || {}).id || "";
}

// defaultSortLabel is what the sort menu calls view's own order.
function defaultSortLabel(view) {
  if (typeof isCoreView === "function" && isCoreView(view)) return (tasksState.catalog || {}).default_sort || "";
  return (catalog.views.find((v) => v.id === view) || {}).default_sort || (typeof tasksState === "object" && (tasksState.catalog || {}).default_sort) || "";
}

// viewStrip is the facet that colors the strip of view's items: a task view's from its profile, a row view's
// from its provider; null for a view of mixed rows, whose rows each take their own.
function viewStrip(view) {
  if (typeof isCoreView === "function" && isCoreView(view)) return stripSpec(((typeof tasksState === "object" && tasksState.catalog) || {}).facets, "");
  const provider = (catalog.views.find((v) => v.id === view) || {}).provider || "";
  return provider ? stripSpec(catalog.specs, provider) : null;
}

// boardGroupField is the field a board of view groups by when nothing is picked: the facet that colors its
// strip when the view offers it; "" otherwise.
function boardGroupField(view, offered) {
  const s = viewStrip(view);
  return s && offered.some((f) => f.id === s.id) ? s.id : "";
}

// lensWords are what the lens bar says, from the views catalog.
function lensWords() {
  return (typeof tasksState === "object" && tasksState.catalog && tasksState.catalog.lens) || {};
}

// fillWord fills a lens word's {{.name}} with vars, and its {{plural .name "one" "…" "other" "…"}} with the form
// of vars[name] in the cockpit's locale.
function fillWord(word, vars) {
  return String(word || "")
    .replace(/\{\{\s*plural\s+\.(\w+)((?:\s+"[^"]*")+)\s*\}\}/g, (_, k, pairs) => {
      const words = [...pairs.matchAll(/"([^"]*)"/g)].map((m) => m[1]);
      const forms = {};
      for (let i = 0; i + 1 < words.length; i += 2) forms[words[i]] = words[i + 1];
      return forms[pluralForm(Number(vars[k]) || 0)] ?? forms.other ?? "";
    })
    .replace(/\{\{\s*\.(\w+)\s*\}\}/g, (_, k) => String(vars[k] ?? ""));
}

// lensOf is the lens of route: its query read over the fields of its view, trimmed as the daemon counts it,
// the picks its view offers and its view's defaults.
function lensOf(route) {
  const { view, tk, done } = route;
  const q = route.q || "";
  const fields = lensFields(view);
  const offered = offeredFields(view, route);
  const quals = qualifiers(q.trim(), fields);
  const { g, s } = lensPicks(view, route, route.g || "", route.s || "");
  const core = typeof isCoreView === "function" && isCoreView(view);
  const provider = core ? "" : (catalog.views.find((v) => v.id === view) || {}).provider || "";
  const specs = core ? ((typeof tasksState === "object" && tasksState.catalog) || {}).facets || [] : catalog.specs;
  const strip = viewStrip(view);
  return { view, q, ts: terms(q.trim(), fields), quals, filters: filtersOf(quals, fields), group: g, sort: s, layout: route.layout || servedLayout(view), tk, done,
    fields, offered, defaultGroup: viewGroupField(view), boardGroup: boardGroupField(view, offered), defaultSort: defaultSortLabel(view),
    specs, provider, words: rowWords(provider), strip: (strip || {}).id || "", stripSpec: strip };
}

// servedLayout is the layout the profile draws a core view in when the hash names none: "board", or "" for
// its list.
function servedLayout(view) {
  const spec = typeof viewSpec === "function" ? viewSpec(view) : null;
  return spec && spec.layout === "board" ? "board" : "";
}

// reasonText is why row r waits, in its plugin's words of the reason it names filled with the reason's values,
// else as the plugin wrote it.
function reasonText(r) {
  const s = r.summary || {};
  const words = ((catalog.reasons || []).find((w) => w.provider === unitOf(r).provider) || {}).words || {};
  const word = s.reason_key ? words[s.reason_key] : "";
  return word ? fillWord(word, s.reason_params || {}) : s.reason || "";
}

// rowWords are the row words of a provider's rows: the profile's, the plugin's own over them.
function rowWords(provider) {
  const own = provider ? ((catalog.lens || []).find((w) => w.provider === provider) || {}).words : null;
  return own ? { ...lensWords(), ...own } : lensWords();
}

function readLens() {
  return lensOf(readHash());
}

// effectiveGroup is the field a lens groups by: the user's pick, else its view's own, else, on a board, the
// field its strip is colored by.
function effectiveGroup(lens) {
  if (lens.group === NO_GROUP) return "";
  if (lens.group) return lens.group;
  if (lens.defaultGroup) return lens.defaultGroup;
  if (lens.layout !== "board") return "";
  return lens.boardGroup === undefined ? (viewStrip(lens.view) || {}).id || "" : lens.boardGroup;
}

function cmp(a, b) {
  return a < b ? -1 : a > b ? 1 : 0;
}

// byDefault is the daemon's order: stalled first, then the longest waiting.
function byDefault(a, b) {
  if (!!sm(a).stalled !== !!sm(b).stalled) return sm(a).stalled ? -1 : 1;
  return cmp(sinceTime(a), sinceTime(b)) || cmp(unitOf(a).provider, unitOf(b).provider) || cmp(unitOf(a).key, unitOf(b).key);
}

function keyRank(f, k) {
  k = fold(k);
  if (k === "none") return [2, ""];
  const i = f.rank ? f.rank.indexOf(k) : -1;
  return i >= 0 ? [0, i] : [1, k];
}

// keyOrder puts ranked values in rank order and the rest by name, with no
// value always last, whatever the direction.
function keyOrder(f, a, b, desc) {
  const [ba, sa] = keyRank(f, a);
  const [bb, sb] = keyRank(f, b);
  if (ba !== bb) return ba === 2 || bb === 2 || !desc ? ba - bb : bb - ba;
  return desc ? cmp(sb, sa) : cmp(sa, sb);
}

// compareItems orders by f, ties by byDefault when there is one; the sort is stable, so the rest keep their
// order.
function compareItems(f, desc, byDefault) {
  return (a, b) => {
    const c = f.sortValue ? (desc ? cmp(f.sortValue(b), f.sortValue(a)) : cmp(f.sortValue(a), f.sortValue(b))) : keyOrder(f, itemKey(f, a), itemKey(f, b), desc);
    return c || (byDefault ? byDefault(a, b) : 0);
  };
}

function groupLabel(f, raw) {
  return !raw || raw === "none" ? f.empty || t("none") : f.show ? f.show(raw) : raw;

}

function groupItems(f, items) {
  const buckets = new Map();
  const add = (raw) => {
    const k = fold(raw);
    if (!buckets.has(k)) buckets.set(k, { field: f.id, key: k, raw, label: groupLabel(f, raw), rows: [] });
    return buckets.get(k);
  };
  for (const it of items) {
    const ks = f.keys ? f.keys(it) : [itemKey(f, it)];
    const seen = new Set();
    for (const k of ks.length ? ks : ["none"]) {
      if (!seen.has(fold(k))) add(k).rows.push(it);
      seen.add(fold(k));
    }
  }
  if (f.id === "lane") for (const id of f.rank || []) add(id);
  return [...buckets.values()].sort((a, b) => keyOrder(f, a.key, b.key, false));
}

// pipelineOf is every list's one path: filter, then sort, then group, items
// keeping the sort inside their group. An item is { facets, texts }.
function pipelineOf(items, lens, byDefault) {
  const fields = lens.fields || catalog.fields;
  const shown = items.filter((it) => matches(it, lens.ts) && passesFilters(it, lens.filters));
  const { field, desc } = parseSort(lens.sort, fields);
  shown.sort(field ? compareItems(fieldById(field, fields), desc, byDefault) : byDefault || (() => 0));
  const g = groupField(effectiveGroup(lens), fields);
  const groups = g ? groupItems(g, shown) : [{ field: "", key: "", raw: "", label: "", rows: shown }];
  return { groups, group: g ? g.id : "", total: items.length, shown: shown.length };
}

function pipeline(rows, lens) {
  return pipelineOf(rows.filter((r) => inView(r, lens.view, lens.done)), lens, byDefault);
}

// groupedRows lists each row once, though grouping by a tag key may put it
// in several groups.
function groupedRows(groups) {
  return [...new Set(groups.flatMap((g) => g.rows))];
}

function visibleRows() {
  return groupedRows(pipeline(state.rows, readLens()).groups);
}

function setNotice(text, isError) {
  const n = document.getElementById("notice");
  n.textContent = text || "";
  n.classList.toggle("err", !!isError);
}

function renderWarnings() {
  const box = document.getElementById("warnings");
  const items = [];
  const hl = state.health;
  if (hl) {
    if (hl.slow_sync) items.push(t("Slow sync: the API budget is low until {time}", { time: stamp(hl.rate_limit.reset_at) }));
    for (const w of hl.warnings || []) items.push(w);
    for (const j of hl.jobs || []) if (j.last_error) items.push(`${j.name}: ${j.last_error}`);
  }
  if (state.detailError) items.unshift(state.detailError);
  if (state.activityError) items.unshift(state.activityError);
  if (state.error) items.unshift(state.error);
  if (state.actionError) items.unshift({ text: state.actionError, detail: state.actionDetail, source: state.actionSource });
  if (state.authLost || state.conn === "closed") items.unshift(t("disconnected: run `{command} open`", { command: "{{command}}" }));
  const text = items.map((it) => (typeof it === "string" ? it : `${it.text}\n${it.detail || ""}`)).join("\n");
  if (box.dataset.text !== text) {
    box.replaceChildren(...items.map((it) => typeof it === "string" ? h("div", {}, it)
      : h("div", {}, it.text, it.detail ? h("details", {}, h("summary", {}, it.source ? t("{source}'s message", { source: (state.health && state.health.workspace_manager) || it.source }) : t("Details")), it.detail) : null)));
    box.dataset.text = text;
  }
  box.hidden = items.length === 0;
}

function renderSync() {
  const el = document.getElementById("sync");
  const hl = state.health;
  if (state.conn !== "open" || !hl) {
    el.textContent = connWord(state.conn);
    el.classList.remove("slow", "paused");
    return;
  }
  const ingest = (hl.jobs || []).filter((j) => j.name.startsWith("ingest:"));
  const sync = ingest.find((j) => j.running) || ingest.find((j) => !isSet(j.last_ok)) || ingest.sort((a, b) => Date.parse(a.last_ok) - Date.parse(b.last_ok))[0];
  const parts = [];
  if (hl.paused) parts.push(t("automatic rounds paused"));
  if (sync) parts.push(sync.running ? t("syncing") : isSet(sync.last_ok) ? t("synced {ago}", { ago: ago(sync.last_ok) }) : t("not synced yet"));
  const rl = hl.rate_limit;
  if (rl && rl.limit) parts.push(t("API budget {remaining} of {limit} left", { remaining: fmtNumber(rl.remaining), limit: fmtNumber(rl.limit) }));
  if (hl.version) parts.push(hl.version);
  el.textContent = parts.join(" · ");
  el.classList.toggle("slow", !!hl.slow_sync);
  el.classList.toggle("paused", !!hl.paused);
}

function connWord(conn) {
  switch (conn) {
    case "connecting": return t("connecting");
    case "reconnecting": return t("reconnecting");
    case "closed": return t("disconnected");
    default: return conn;
  }
}


function renderPause() {
  const hl = state.health;
  let b = document.getElementById("pause");
  if (!b) {
    b = h("button", { id: "pause", type: "button", class: "act", "data-focus": "top:pause", onclick: () => setPaused(!(state.health && state.health.paused)) });
    document.getElementById("top").append(b);
  }
  b.hidden = !hl;
  b.disabled = !!state.pausing;
  b.textContent = hl && hl.paused ? t("Resume automatic rounds") : t("Pause automatic rounds");
}

function yamlString(v) {
  return JSON.stringify(String(v ?? ""));
}

function yamlMap(fields) {
  return `{${Object.entries(fields).map(([k, v]) => `${k}: ${v}`).join(", ")}}`;
}

// listYAML writes the items a view shows as YAML whose scalars are JSON strings, so an agent reads it without
// quoting surprises: each item's reference, link, title and units, then its facets by id.
function listYAML(meta, items) {
  const out = [`view: ${yamlString(meta.view)}`];
  if (meta.filter) out.push(`filter: ${yamlString(meta.filter)}`);
  if (meta.group_by) out.push(`group_by: ${yamlString(meta.group_by)}`);
  if (meta.sort) out.push(`sort: ${yamlString(meta.sort)}`);
  out.push(`copied_at: ${yamlString(meta.at)}`, `count: ${items.length}`, "items:");
  const list = (vs) => `[${vs.map(yamlString).join(", ")}]`;
  for (const it of items) {
    const c = copyEntry(it);
    const facets = Object.keys(c.facets || {}).sort().map((id) => [id, (c.facets[id] || []).filter(Boolean)]).filter(([, vs]) => vs.length);
    const fields = [
      ["ref", yamlString(c.ref)],
      ["url", c.url ? yamlString(c.url) : ""],
      ["title", yamlString(c.title)],
      ["since", c.since ? yamlString(c.since) : ""],
      ["reason", c.reason ? yamlString(c.reason) : ""],
      ["units", (c.units || []).length ? list(c.units) : ""],
      ...facets.map(([id, vs]) => [id, list(vs)]),
    ].filter(([, v]) => v);
    fields.forEach(([k, v], i) => out.push(`${i ? "    " : "  - "}${k}: ${v}`));
  }
  return out.join("\n") + "\n";
}

// copyEntry is what Copy list says of an item: a task by its id, its first unit's link and its units, a row
// by its label and link, with its summary's reason and since.
function copyEntry(it) {
  const e = it.task;
  if (e) {
    const linked = (e.units || []).map((u) => (rowByKey(unitKey(u)) || {}).link).find(Boolean);
    return { ref: e.id, url: linked, title: e.title, units: (e.units || []).map((u, i) => (e.unit_labels || [])[i] || unitKey(u)), facets: e.facets };
  }
  return { ref: labelOf(it), url: it.link, title: it.title, since: isSet(sm(it).since) ? sm(it).since : "", reason: sm(it).reason, facets: it.facets };
}

function candidatesYAML(meta, cands) {
  const out = [`query: ${yamlString(meta.query)}`];
  if (meta.read) out.push(`read_as: ${yamlString(meta.read)}`);
  out.push(`copied_at: ${yamlString(meta.at)}`, `count: ${cands.length}`, "pull_requests:");
  for (const c of cands) {
    const row = rowByKey(keyOf(c));
    const b = c.brief && c.brief.brief;
    const asks = (c.review_requests || []).map((w) => w.replace(/^team:/, ""));
    const fields = [
      ["ref", yamlString(labelOf(c))],
      ["url", yamlString(candidateURL(c))],
      ["title", yamlString(c.title)],
      ["author", yamlString(c.author)],
      ["draft", c.draft ? "true" : ""],
      ["size", yamlString(`+${c.additions} -${c.deletions}, ${c.changed_files} file${c.changed_files === 1 ? "" : "s"}`)],
      ["review", yamlString(reviewDecision(c.review_decision))],
      ["checks", yamlString(c.checks ? c.checks.toLowerCase() : "none")],
      ["asks", asks.length ? `[${asks.map(yamlString).join(", ")}]` : ""],
      ["my_review", c.my_review ? yamlString(c.my_review.toLowerCase()) : ""],
      ["created_at", yamlString(c.created_at)],
      ["updated_at", yamlString(c.updated_at)],
      ["in_loop", row ? yamlMap({ kind: yamlString(row.kind), waits_on: yamlString(sm(row).waits_on) }) : "false"],
      ["brief", b ? yamlMap({ size: yamlString(b.size), risk: yamlString(b.risk), open_questions: (b.open_questions || []).length, goal: yamlString(b.goal) }) : ""],
    ].filter(([, v]) => v);
    fields.forEach(([k, v], i) => out.push(`${i ? "    " : "  - "}${k}: ${v}`));
  }
  return out.join("\n") + "\n";
}

async function copyCandidates() {
  const ex = state.explore;
  const cands = (ex.result && ex.result.candidates) || [];
  try {
    await navigator.clipboard.writeText(candidatesYAML({ query: readHash().x, read: ex.result && ex.result.read, at: new Date().toISOString() }, cands));
    setNotice(tn("Copied {n} candidates as YAML.", cands.length));
  } catch (err) {
    setNotice(t("Could not copy: {error}", { error: err.message }), true);
  }
}

async function setPaused(paused) {
  state.pausing = true;
  state.actionError = "";
  state.actionDetail = "";
  renderPause();
  renderWarnings();
  try {
    const res = await fetch("/api/pause", {
      method: "POST",
      headers: { "Content-Type": "application/json", Accept: "application/json", "X-Loop-API": API_VERSION },
      body: JSON.stringify({ paused }),
    });
    if (!res.ok) throw new Error(apiError(await res.json().catch(() => ({})), `HTTP ${res.status}`, res.status));
    setNotice(paused ? t("Automatic rounds paused") : t("Automatic rounds resumed"));
  } catch (err) {
    state.actionError = paused ? t("pause failed: {error}", { error: err.message }) : t("resume failed: {error}", { error: err.message });
  }
  state.pausing = false;
  scheduleLoad({ health: true });
}

// focusSlot is the slot an action keeps focus in when a render swaps it for
// its counterpart: a toggle's pair, or a round's start, which an approving
// gate intent and an intent that starts a round on request share.
const FOCUS_PAIRS = { resume: "pause", unmute: "mute", untrack: "track" };
function focusSlot(name) {
  const sp = state.specs.byId[name];
  if (sp && sp.group === "start" && sp.intake) return "start";
  if (sp && sp.group === "start" && sp.verb === "dismiss") return "open";
  return FOCUS_PAIRS[name] || name;
}

const CARD_GROUPS = new Set(["turn", "intake", "brief"]);

async function loadSpecs() {
  const r = await getJSON("/api/actions");
  state.specs = { version: r.version, byId: Object.fromEntries((r.specs || []).map((x) => [x.id, x])) };
  state.planSpecs = Object.fromEntries((r.plan || []).map((x) => [x.id, x]));
}

function specsStale(rows) {
  return !state.specs.version || rows.some((r) => r.specs_version && r.specs_version !== state.specs.version);
}

async function refreshSpecs(rows) {
  if (!specsStale(rows)) return;
  try {
    await loadSpecs();
  } catch (err) {
    state.error = t("Could not load the actions: {error}", { error: err.message });
  }
}

function actionStates(subject, surface, groups) {
  return (subject.actions || [])
    .map((st) => [st, state.specs.byId[st.id]])
    .filter(([, sp]) => sp && sp.surfaces.includes(surface) && (groups ? groups.includes(sp.group) : surface !== "detail" || !CARD_GROUPS.has(sp.group)));
}

// actionTitle is an action's tooltip: what it does, then what it would do on
// this row, or why it is off.
function actionTitle(st, sp) {
  return [sp && sp.summary, st.enabled ? st.note : st.reason].filter(Boolean).join("\n") || undefined;
}

function actionButton(subject, st, sp, opts) {
  const key = keyOf(subject);
  const run = opts.run || ((id) => act(subject, id));
  return h("button", {
    type: "button",
    class: ["act", st.enabled && st.primary ? "primary" : "", st.enabled ? "" : "off"].filter(Boolean).join(" "),
    title: actionTitle(st, sp),
    "data-focus": `${opts.scope || "row:" + key}:${focusSlot(st.id)}`, ...roveStop(opts.rove),
    disabled: state.busy.has(key) || !st.enabled,
    onclick: (e) => { e.stopPropagation(); return run(st.id); },
  }, st.label || sp.label);
}

// actionButtons renders the served actions of a row, candidate or detail on a surface; the question card, intake and brief groups render themselves.
function actionButtons(subject, surface, opts = {}) {
  return actionStates(subject, surface, opts.groups).map(([st, sp]) => actionButton(subject, st, sp, opts));
}

function actionNotes(subject, surface) {
  return actionStates(subject, surface).filter(([st]) => st.enabled && st.warning).map(([st]) => h("p", { class: "act-note" }, st.warning));
}

// facet is a value on screen that filters by itself: a click filters by it,
// Shift groups by its field and Alt leaves it out. A field the lens does not
// know, or a lens that does not filter, shows its content alone. opts may
// give the words to say it in, the noun that names the field, the word the value shows as, and rove for
// a stop of a row's roving focus.
function facet(lens, field, raw, content, slot, opts = {}) {
  const fields = lens.fields || catalog.fields;
  const f = fieldById(field, fields);
  if (!f || lens.static) return content;
  const v = normValue(field, raw);
  const w = opts.words || lens.words || lensWords();
  const by = f.groupBy ? f.groupBy(raw) : f.group ? field : "";
  const group = by ? groupField(by, fields) : null;
  const vars = { label: opts.noun || f.label.toLowerCase(), value: opts.label || (f.show ? f.show(raw) : raw), group: group ? group.label.toLowerCase() : "" };
  const pick = (e) => {
    e.stopPropagation();
    if (e.shiftKey && by) return setGroup(by);
    writeHash({ q: toggledQualifier(readHash().q, field, raw, e.altKey, fields) });
  };
  return h("button", {
    type: "button", class: "facet", "data-focus": slot, ...roveStop(opts.rove),
    "aria-pressed": String(lens.quals.some((x) => x.field === field && x.value === v && !x.neg)),
    "aria-label": fillWord(w.filter_label, vars),
    title: fillWord(group ? w.filter_title_group : w.filter_title, vars),
    onclick: pick,
    onkeydown: (e) => {
      if (e.key === "Enter" && (e.shiftKey || e.altKey)) {
        e.preventDefault();
        pick(e);
      }
    },
  }, content);
}

// where is a row's workspace, its column and agents, and its link, as the detail shows them.
function where(row, scope) {
  const ws = pl(row).workspace || {};
  const bits = [];
  if (ws.display_name) bits.push(h("span", { class: "ws" }, ws.display_name));
  if (ws.status) bits.push(h("span", { class: "col" }, ws.status));
  if ((ws.agent_states || []).length) bits.push(h("span", { class: "agent" }, ws.agent_states.join(", ")));
  bits.push(rowLink(row, scope));
  return bits;
}

function rowLink(row, scope, rove) {
  return h("a", { href: safeURL(row.link), target: "_blank", rel: "noopener noreferrer", class: "ext", "data-focus": `${scope}:link`, ...roveStop(rove), onclick: (e) => e.stopPropagation() }, providerName(unitOf(row).provider));
}

// roveStop marks a control of a row as a stop of its roving focus: out of the Tab order, reached with the
// arrows from the row.
function roveStop(on) {
  return on ? { tabindex: -1, "data-rove": "" } : {};
}

// rowStops are the stops of a row's roving focus in their order: the row itself when it takes the focus, then
// its controls that are shown and enabled.
function rowStops(row) {
  const out = [];
  const walk = (n) => {
    if (!n || !n.getAttribute || n.hidden || n.inert || n.getAttribute("disabled") != null) return;
    if (n.getAttribute("data-rove") != null) out.push(n);
    for (const c of n.children || []) walk(c);
  };
  walk(row);
  return out;
}

// ROVE_KEYS are how far each key moves a row's roving focus.
const ROVE_KEYS = { ArrowLeft: -1, ArrowRight: 1, Home: -Infinity, End: Infinity };

// roveKey moves the focus among a row's stops with the arrows, Home and End; true when it took the key.
function roveKey(e) {
  const step = ROVE_KEYS[e.key];
  if (!step || e.altKey || e.ctrlKey || e.metaKey || e.shiftKey) return false;
  const stops = rowStops(e.currentTarget);
  const at = stops.indexOf(e.target);
  if (at < 0) return false;
  e.preventDefault();
  const next = stops[Math.max(0, Math.min(stops.length - 1, at + step))];
  if (next !== e.target) next.focus();
  return true;
}

// copySlot is the focus slot of one copy of a row or card: grouping by a tag
// key shows a pull request once per value, copy naming the group.
function copySlot(kind, key, copy) {
  return copy ? `${kind}:${key}@${copy}` : `${kind}:${key}`;
}

// multiCopy is whether a grouping may show a pull request in several groups.
function multiCopy(field) {
  return !!field && !!(groupField(field) || {}).keys;
}

function openRow(scope, key) {
  state.openSlot = scope;
  writeHash({ pr: key, task: "" });
}

// openSlot is the copy of the open pull request last opened, when it is
// still the open one.
function openSlot(pr) {
  const s = state.openSlot || "";
  return pr && ["row", "card"].some((k) => s === `${k}:${pr}` || s.startsWith(`${k}:${pr}@`)) ? s : "";
}

// specFor mirrors api.FacetSpec.For: a merged spec with provider's display keys.
function specFor(spec, provider) {
  const d = (spec.displays || {})[provider];
  if (!d) return spec;
  return { ...spec, show: d.show || "", show_only: d.show_only, show_values: d.show_values, label_prefix: d.label_prefix, row_class: d.row_class,
    show_when: d.show_when, show_max: d.show_max, show_label: d.show_label,
    strip: !!d.strip, colors: d.colors,
    buckets: d.bucket_labels ? (spec.buckets || []).map((b) => (b.id in d.bucket_labels ? { ...b, label: d.bucket_labels[b.id] } : b)) : spec.buckets,
    labels: d.labels && Object.keys(d.labels).length ? d.labels : spec.labels };
}

// ageText mirrors lens.Age: ms in the row words.
function ageText(w, ms) {
  const mins = Math.floor(Math.max(ms, 0) / 60000);
  if (mins < 1) return w.age_now || "";
  if (mins < 60) return fillWord(w.age_minutes, { m: mins });
  if (mins < 1440) return fillWord(w.age_hours, { h: Math.floor(mins / 60), m: mins % 60 });
  return fillWord(w.age_days, { d: Math.floor(mins / 1440), h: Math.floor(mins / 60) % 24 });
}

function partPiece(spec, raw, words, now) {
  if (!raw || fold(raw) === "none") return null;
  const p = { field: spec.id, value: raw, raw, label: (spec.labels || {})[raw] || raw };
  if (spec.sort === "time" || (spec.buckets || []).length) {
    const at = rfc3339(raw) ? Date.parse(raw) : NaN;
    if (Number.isNaN(at)) return null;
    if ((spec.buckets || []).length) {
      p.value = bucketOf(spec.buckets, raw, now);
      if (!p.value) return null;
      const b = spec.buckets.find((x) => x.id === p.value);
      if (b) p.label = b.label || p.value;
    }
    if (agePart(spec.show)) {
      p.label = ageText(words, now - at);
      if (spec.show_label) [p.label, p.template] = [spec.show_label.replaceAll("{{.age}}", p.label), spec.show_label];
    }
  }
  if (spec.label_prefix && !p.label.startsWith(spec.label_prefix)) p.label = spec.label_prefix + p.label;
  return p;
}

// rfc3339 says whether s is an RFC 3339 instant, as Go's lens parses them.
function rfc3339(s) {
  return /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})$/.test(String(s));
}

// agePart says whether a part shows one time of one facet, as its age.
function agePart(part) {
  return part === "age";
}

// holdsWhen mirrors lens's holds: whether it holds, in each facet when names, one of its values.
function holdsWhen(it, when) {
  return Object.entries(when || {}).every(([facet, values]) => ((it.facets || {})[facet] || []).some((v) => values.some((w) => fold(v) === fold(w))));
}

// rowParts mirrors lens.Parts: the pieces of item it each part of a row shows, by part, from the specs
// that show in one, in their order, none from another provider's spec.
function rowParts(it, specs, words, now = Date.now()) {
  const out = {};
  for (const s of specs || []) {
    if (s.provider && it.provider && s.provider !== it.provider) continue;
    const spec = specFor(s, it.provider || "");
    if (!spec.show || (agePart(spec.show) && out[spec.show]) || !holdsWhen(it, spec.show_when)) continue;
    const pieces = [];
    let rest = 0;
    for (const raw of (it.facets || {})[spec.id] || []) {
      const p = partPiece(spec, raw, words, now);
      if (!p || ((spec.show_only || []).length && !spec.show_only.some((v) => fold(v) === fold(raw)))) continue;
      if (spec.show_max > 0 && pieces.length === spec.show_max) {
        rest++;
        continue;
      }
      pieces.push(p);
      if (spec.show_values === "first" || agePart(spec.show)) break;
    }
    if (rest) pieces.push({ field: spec.id, value: "", raw: "", label: fillWord(words.more, { n: rest }) });
    if (pieces.length) out[spec.show] = [...(out[spec.show] || []), ...pieces];
  }
  return out;
}

// rowClasses are the classes the specs' row_class give the values an item holds.
function rowClasses(it, specs) {
  const out = [];
  for (const s of specs || []) {
    if (s.provider && it.provider && s.provider !== it.provider) continue;
    const spec = specFor(s, it.provider || "");
    for (const v of (it.facets || {})[spec.id] || []) if ((spec.row_class || {})[v]) out.push(spec.row_class[v]);
  }
  return [...new Set(out)];
}

// refTail is a unit's label after the first ref piece's label, when the label starts with it.
function refTail(label, ref) {
  const lead = ref && ref[0] ? ref[0].label : "";
  return lead && label.startsWith(lead) ? label.slice(lead.length) : label;
}

// rowModel is a plugin row as the row renderer lays it out, in a list or as a board's card (kind "card"): its
// facets placed by its provider's specs, then what no facet carries.
function rowModel(r, pr, lens, copy, kind = "row") {
  const key = keyOf(r);
  const p = pl(r);
  const w = rowWords(unitOf(r).provider);
  const item = { facets: r.facets || {}, provider: unitOf(r).provider };
  const parts = rowParts(item, lens.specs, w);
  const scope = copySlot(kind, key, copy);
  const n = Number.isInteger((p.brief || {}).open_questions) ? p.brief.open_questions : 0;
  const ws = p.workspace || {};
  const strip = stripOf(item, lens.specs);
  const why = reasonText(r);
  const groups = new Set((p.waits_on || []).filter((x) => String(x.key || "").startsWith("group:")).map((x) => fold(x.name || "")));
  const nouns = Object.fromEntries((parts.who || []).filter((x) => groups.has(fold(x.raw))).map((x) => [`${x.field}:${x.value}`, w.filter_group]));
  return {
    key, scope, item, parts, words: w, nouns, selected: key === pr, open: () => openRow(scope, key),
    title: titleWithoutRef(r.title || labelOf(r), [labelOf(r)]), titleTip: (p.brief || {}).goal,
    ref: [refTail(labelOf(r), parts.ref)],
    meta: [
      p.head && lens.ts.some((term) => fold(p.head).includes(term)) ? h("span", { class: "head" }, marked(p.head, lens.ts)) : null,
      n > 0 ? h("span", { class: "pill qs", title: fillWord(w.questions_title, { n }) }, fillWord(w.questions, { n })) : null],
    flags: [r.stale ? h("span", { class: "flag stale", title: w.stale_title }, w.stale) : null, intakeChip(r, w)],
    lead: stripDot(strip, lens, scope, w),
    age: roundGate(r) ? countdown(r) : null,
    why,
    asideLead: ws.display_name ? h("span", { class: "ws" }, ws.display_name) : null,
    aside: [(ws.agent_states || []).length ? h("span", { class: "agent" }, ws.agent_states.join(", ")) : null, rowLink(r, scope, true)],
    actions: actionButtons(r, "row", { scope, rove: true }),
    cls: [strip ? "w-" + strip.color : "", ...rowClasses(item, lens.specs)],
    tip: rowTip(strip, waitingLabel(r), (parts.kind || []).map((x) => x.label).join(", "), why, w),
  };
}

// stripDot is the dot of an item's strip, a facet button that filters by its value; null without a strip.
function stripDot(strip, lens, scope, words) {
  if (!strip) return null;
  const id = strip.spec.id;
  return facet(lens, id, strip.value, h("span", { class: "dot", "aria-hidden": "true", "data-field": id, "data-value": strip.value }, "●"), `facet:${scope}:${id}`, { words, rove: true });
}

// groupQualifier is the filter that keeps only a group's rows: a tag key's
// "no lang" group is -tag:lang.
function groupQualifier(g, fields = catalog.fields) {
  const f = groupField(g.field, fields);
  if (!f) return null;
  if (f.tagKey) return g.key === "none" ? { field: "tag", raw: f.tagKey, neg: true } : { field: "tag", raw: g.raw, neg: false };
  return f.values ? { field: g.field, raw: g.raw, neg: false } : null;
}

function onlyThis(g, lens) {
  const q = groupQualifier(g, lens.fields);
  if (!q) return null;
  const v = normValue(q.field, q.raw);
  const on = lens.quals.some((x) => x.field === q.field && x.value === v && x.neg === q.neg);
  return h("button", {
    type: "button", class: "facet only", "aria-pressed": String(on), "data-focus": `group:${g.field}:${g.key}:only`,
    "aria-label": on ? t("Show all, not only {group}", { group: g.label }) : t("Only {group}", { group: g.label }),
    onclick: (e) => { e.stopPropagation(); writeHash({ q: toggledQualifier(lens.q, q.field, q.raw, q.neg, lens.fields) }); },
  }, on ? t("show all") : t("only this"));
}

function toggleCollapsed(key) {
  if (state.collapsed.has(key)) state.collapsed.delete(key);
  else state.collapsed.add(key);
  lastFocus = `group:${key}`;
  render();
}

// unitTitle names a row view's unit in its count line: the view's unit, else the lens's word for a mixed view.
function unitTitle(view, lens) {
  return (catalog.views.find((x) => x.id === view) || {}).unit || (lens.words || {}).col_unit || "";
}

function renderTable(res, pr, lens) {
  document.getElementById("table").replaceChildren(rowList(res, (r, copy) => rowModel(r, pr, lens, copy), lens));
}

function inViewOf(r, lens) {
  return inView(r, lens.view, lens.done);
}

function renderRows() {
  const lens = readLens();
  const { pr } = readHash();
  const { view } = lens;
  const listless = view === "recent" || view === "explore" || view === "plan" || view === HOME || view === QUEUE || pluginViews.has(view) || isCoreView(view);
  renderActivity(lens);
  const table = document.getElementById("table");
  const boardBox = document.getElementById("board");
  const empty = document.getElementById("empty");
  const listBox = document.getElementById("list-box");
  listBox.classList.toggle("in-dash", view === "dash");
  listBox.hidden = view === "plan" || view === HOME || view === QUEUE || pluginViews.has(view) || isCoreView(view);
  if (view !== "dash") listBox.open = true;
  if (listless) {
    if (isCoreView(view)) hideLens(document.getElementById("tools"));
    else renderLens(lens, null);
    table.hidden = true;
    table.replaceChildren();
    boardBox.hidden = true;
    boardBox.replaceChildren();
    empty.hidden = true;
    return;
  }
  const res = pipeline(state.rows, lens);
  document.getElementById("list-sum").textContent = fillWord((lens.words || {}).list_count, { unit: unitTitle(view, lens), shown: res.shown, total: res.total });
  if (state.collapsedFor !== res.group) {
    state.collapsed.clear();
    state.collapsedFor = res.group;
  }
  const board = lens.layout === "board";
  renderLens(lens, res);
  table.hidden = board;
  boardBox.hidden = !board || !res.shown;
  if (board) {
    table.replaceChildren();
    if (res.shown) renderBoard(res.groups, pr, lens);
  } else {
    boardBox.replaceChildren();
    renderTable(res, pr, lens);
  }
  empty.hidden = res.shown > 0;
  if (!res.shown) empty.replaceChildren(...emptyNote(view, lens.q));
}

function emptyNote(view, q) {
  if (!state.loaded) return [t("Loading the inventory.")];
  const label = (catalog.views.find((v) => v.id === view) || {}).label;
  const row = rowByURL(q);
  if (row) {
    return [t("{unit} is in the inventory.", { unit: labelOf(row) }), " ", h("button", {
      type: "button", class: "act", "data-focus": "empty:open", onclick: () => writeHash({ q: "", pr: keyOf(row) }),
    }, t("Open it"))];
  }
  const pull = PR_URL.exec(String(q || "").trim());
  if (pull && !/^\.\.?$/.test(pull[2])) {
    const [, owner, repo, number] = pull;
    const typed = q.trim();
    return [t("{unit} is not in the inventory.", { unit: refLabel({ owner, repo, number }) }), " ",
      h("button", { type: "button", class: "act", "data-focus": "empty:explore", onclick: () => { writeHash({ view: "explore", q: "", x: q.trim() }); explore(q); } }, t("Explore it")),
      " ",
      h("a", { href: typed, target: "_blank", rel: "noopener noreferrer", "data-focus": "empty:gh" }, t("Open it on {provider}", { provider: searchService() }))];
  }
  if (!q) return [t("Nothing in {view} right now.", { view: label })];
  const note = t("Nothing in {view} matches \"{query}\".", { view: label, query: q });
  if (!qualifiers(q).length) return [note];
  return [note, " ", h("button", { type: "button", class: "act", "data-focus": "empty:clear", onclick: () => clearLens() }, t("Clear filters"))];
}

// lane is a board's column for group g of lens, its items drawn by item; an ungrouped board's one lane is
// titled title.
function lane(g, lens, title, item) {
  const label = g.label || title;
  return h("section", { class: "lane", "aria-label": label },
    h("h2", { class: "lane-head" + (g.field && g.field === lens.strip ? " w-" + stripColor(lens.stripSpec, g.key) : "") }, h("span", {}, label),
      h("span", { class: "lane-tools" }, onlyThis(g, lens), h("span", { class: "count" }, g.rows.length))),
    h("ol", { class: "cards" }, g.rows.map((it) => item(it, multiCopy(g.field) ? g.key : ""))));
}

function renderBoard(groups, pr, lens) {
  const title = (catalog.views.find((v) => v.id === lens.view) || {}).label || lens.view;
  document.getElementById("board").replaceChildren(...groups.map((g) => lane(g, lens, title, (r, copy) => rowView(rowModel(r, pr, lens, copy, "card"), lens, { card: true }))));
}

function loopOnly() {
  try {
    return localStorage.getItem(LOOP_ONLY_KEY) === "1";
  } catch {
    return false;
  }
}

function setLoopOnly(on) {
  try {
    localStorage.setItem(LOOP_ONLY_KEY, on ? "1" : "0");
  } catch {
    // The toggle still applies until the page reloads.
  }
  activityLoopOnly = on;
  renderActivity(readLens());
}

let activityLoopOnly = loopOnly();
let activitySelected = "";

function dayLabel(iso) {
  const d = new Date(iso);
  const startOf = (x) => new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime();
  const days = Math.round((startOf(new Date()) - startOf(d)) / 86400000);
  if (days === 0) return t("Today");
  if (days === 1) return t("Yesterday");
  return fmtDate(iso, "long");
}

function clock(iso) {
  return fmtDate(iso, "time");
}

function actorLabel(actor) {
  const viewer = state.health && state.health.viewer;
  return viewer && actor.toLowerCase() === viewer.toLowerCase() ? "me" : actor;
}

function requestedFrom(reviewer) {
  return reviewer.startsWith("team:") ? reviewer.slice("team:".length) : actorLabel(reviewer);
}

// EVENT_DATA shows an event's data the way its plugin's catalog says:
// whole by default, a field or a short sha, or nothing when it names whom
// a review was requested from, which the row shows on its own.
const EVENT_DATA = {
  none: () => "",
  first_field: (d) => d.split(":")[0],
  short_sha: (d) => d.slice(0, 7),
  requested: () => "",
};

function eventData(e) {
  if (!e.data) return "";
  const show = EVENT_DATA[catalog.eventData.get(e.kind)];
  return show ? show(e.data) : e.data;
}

function renderActivity(lens) {
  const list = document.getElementById("activity");
  const { view, pr } = readHash();
  list.hidden = view !== "recent";
  if (list.hidden) {
    list.replaceChildren();
    return;
  }
  const items = state.activity.filter((it) => (!activityLoopOnly || loopEvent(it.event.kind)) && activityPasses(it, lens));
  const out = [h("li", { class: "a-tools" },
    h("label", {},
      h("input", { type: "checkbox", checked: activityLoopOnly, "data-focus": "activity:loop-only", onchange: (e) => setLoopOnly(e.target.checked) }),
      " ", t("Loop only")))];
  let day = "";
  for (const it of items) {
    const e = it.event;
    const label = dayLabel(e.at);
    if (label !== day) {
      day = label;
      out.push(h("li", { class: "a-day" }, label));
    }
    const key = keyOf(e);
    const id = `activity:${key}:${e.kind}:${e.key}:${e.at}`;
    const open = () => {
      activitySelected = id;
      writeHash({ pr: key });
    };
    const data = eventData(e);
    const requested = catalog.eventData.get(e.kind) === "requested" && e.data;
    out.push(h("li", {
      class: ["a-ev", "ev-" + e.kind, eventTone(e.kind), key === pr && id === activitySelected ? "sel" : ""].filter(Boolean).join(" "),
      tabindex: 0,
      "data-focus": id,
      onclick: open,
      onkeydown: (ev) => {
        if ((ev.key === "Enter" || ev.key === " ") && ev.target === ev.currentTarget) {
          ev.preventDefault();
          open();
        }
      },
    },
      h("time", { datetime: e.at, title: stamp(e.at) }, clock(e.at)),
      h("div", {},
        h("span", { class: "kind" }, eventLabel(e.kind)),
        requested ? h("span", { class: "who" }, " " + t("from {who}", { who: requestedFrom(e.data) })) : null,
        e.actor ? h("span", { class: "who" }, " " + (requested ? t("by {who}", { who: actorLabel(e.actor) }) : actorLabel(e.actor))) : null,
        " ", h("span", { class: "ref" }, eventRef(it)),
        " ", h("span", { class: "title" }, it.title)),
      data ? h("span", { class: "data" }, data) : null));
  }
  if (!items.length) out.push(h("li", { class: "empty" }, activityEmptyNote(readHash().q)));
  list.replaceChildren(...out);
}

function activityEmptyNote(q) {
  if (!state.activityLoaded) return [t("Loading the activity.")];
  if (state.activityError && !state.activity.length) return [t("The recent activity could not be loaded.")];
  if (!state.activity.length) return [t("Nothing happened in the inventory yet.")];
  if (tokens(q).length) return emptyNote("recent", q);
  return [t("No loop events in the recent activity.")];
}

function guidanceLabel(r) {
  if (r.question && r.question.queued) return t("Replace the queued answer");
  if (r.question) return r.question.state === "asked" && questionChoices(r, r.question).length ? t("Or answer in your own words") : t("Answer in your own words");
  const start = Object.values(state.specs.byId).find((sp) => sp.group === "start" && sp.verb === "approve");
  if (!roundGate(r)) return t("Guidance for the next round (optional)");
  return start ? t("Guidance for the pending round; {action} sends it at once", { action: start.label }) : t("Guidance for the pending round");
}

function guidanceBox(key, placeholder) {
  const old = document.getElementById("guidance");
  if (old && old.dataset.key === key) {
    old.placeholder = placeholder;
    return old;
  }
  const box = h("textarea", {
    id: "guidance", rows: 3, placeholder,
    "data-key": key, "data-focus": "detail:guidance",
    oninput: (e) => {
      state.guidance[key] = e.target.value;
      const answer = document.querySelector('[data-focus="detail:answer"]');
      if (answer) answer.disabled = state.busy.has(key) || state.answered.has(key) || !e.target.value.trim();
    },
  });
  box.value = state.guidance[key] || "";
  return box;
}

function closeDetail() {
  const { pr } = readHash();
  if (pr) lastFocus = openSlot(pr) || `row:${pr}`;
  writeHash({ pr: "" });
}

// tagChips shows a brief's key:value tags, the first TAG_CHIPS and a +N
// unless all; with a lens each is a facet.
function tagChips(tags, lens, slot, all) {
  const shown = all ? tags : tags.slice(0, TAG_CHIPS);
  const rest = tags.slice(shown.length);
  return [
    ...shown.map((tag) => {
      const pill = h("span", { class: "pill tag" }, tag);
      return lens ? facet(lens, "tag", tag, pill, slot + "tag:" + tag) : pill;
    }),
    rest.length ? h("span", { class: "pill tag", title: rest.join(", ") }, `+${rest.length}`) : null,
  ];
}

// briefTags are the pills of an Explore candidate's brief summary; open_questions is a count.
function briefTags(b) {
  if (!b) return null;
  const size = SIZES.includes(b.size) ? b.size : "";
  const risk = RISKS.includes(b.risk) ? b.risk : "";
  const n = Number.isInteger(b.open_questions) && b.open_questions > 0 ? b.open_questions : 0;
  const tags = Array.isArray(b.tags) ? b.tags : [];
  const type = b.kind || "";
  if (!type && !size && !risk && !n && !tags.length) return null;
  return h("span", { class: "btags" },
    type ? h("span", { class: "pill", title: t("type {type}", { type }) }, type) : null,
    size ? h("span", { class: "pill", title: t("size {size}", { size }) }, size.toUpperCase()) : null,
    risk ? h("span", { class: `pill risk-${risk}` }, t("risk {risk}", { risk })) : null,
    n ? h("span", { class: "pill qs", title: tn("{n} open questions in the brief", n) }, `? ${n}`) : null,
    tagChips(tags));
}

function refreshBriefButton(r) {
  return r ? actionButtons(r, "detail", { scope: "detail", groups: ["brief"] }) : null;
}

// briefFacet is a detail panel pill that filters by its field, when the
// value is one the field knows.
function briefFacet(lens, field, value, pill) {
  return lens && value ? facet(lens, field, value, pill, "detail:facet:" + field) : pill;
}

function briefBlock(b, r, lens) {
  if (!b) {
    const refresh = refreshBriefButton(r);
    return refresh && refresh.length ? h("div", { class: "d-brief muted" }, refresh) : null;
  }
  if (b.state === "pending" && !b.brief) return h("div", { class: "d-brief muted" }, t("Brief pending"), " ", refreshBriefButton(r));
  if (!b.brief) return h("div", { class: "d-brief muted" }, b.error ? t("Brief failed: {error}. It is tried again later.", { error: b.error }) : t("Brief failed. It is tried again later."), " ", refreshBriefButton(r));
  const x = b.brief;
  const failed = b.state === "failed";
  const notes = [b.model, stamp(b.at), b.stale && !failed ? t("refreshing") : "", failed ? t("last refresh failed: {error}", { error: b.error || t("unknown error") }) : ""].filter(Boolean).join(" · ");
  const risk = ["low", "medium", "high"].includes(x.risk) ? x.risk : "";
  return h("section", { class: "d-brief", "aria-label": t("Brief") },
    h("div", { class: "d-brief-tags" },
      briefFacet(lens, "type", x.kind, h("span", { class: "pill" }, x.kind)),
      briefFacet(lens, "size", SIZES.includes(x.size) ? x.size : "", h("span", { class: "pill" }, t("size {size}", { size: x.size }))),
      risk ? briefFacet(lens, "risk", risk, h("span", { class: `pill risk-${risk}` }, t("{risk} risk", { risk }))) : null,
      tagChips(Array.isArray(x.tags) ? x.tags : [], lens, "detail:facet:", true)),
    h("p", { class: "goal" }, x.goal),
    x.context ? h("p", {}, x.context) : null,
    x.risk_why ? h("p", { class: "muted" }, t("Risk: {risk}", { risk: x.risk_why })) : null,
    (x.open_questions || []).length ? h("ul", { class: "questions" }, x.open_questions.map((q) => h("li", {}, q))) : null,
    h("div", { class: "muted small" }, notes, " ", refreshBriefButton(r)));
}

// trimmedNote names the profile blocks the current turn's guide left out
// to stay within the judgment limit.
function trimmedNote(d) {
  const id = d.turn && d.turn.turn && d.turn.turn.id;
  const e = id && (d.events || []).find((x) => x.kind === "guide_trimmed" && x.key === id);
  if (!e) return null;
  return h("div", { class: "d-trimmed muted small" }, t("Guide trimmed over the judgment limit: {blocks}", { blocks: e.data }));
}

function intakeWaiting(r) {
  const i = r && pl(r).intake;
  return !!i && (i.state === "accepted" || i.state === "briefing");
}

function intakeChip(r, w) {
  if (!intakeWaiting(r)) return null;
  const i = pl(r).intake;
  const title = [fillWord(w.briefing_title, { seq: i.seq }), i.attempt ? fillWord(w.briefing_attempt, { n: i.attempt }) : "",
    i.last_error ? fillWord(w.briefing_error, { error: i.last_error }) : ""].filter(Boolean).join(", ");
  return h("span", { class: "flag briefing", title }, w.briefing);
}

// intakeLines say what a waiting intake waits on: the attempt, the last
// error and the next attempt's time.
function intakeLines(i) {
  return [
    i.attempt ? t("Intake {seq} ({intent}) waits for the brief, attempt {attempt}", { seq: i.seq, intent: i.intent, attempt: i.attempt }) : t("Intake {seq} ({intent}) waits for the brief", { seq: i.seq, intent: i.intent }),
    i.last_error ? t("Last error: {error}", { error: i.last_error }) : "",
    isSet(i.next) ? t("Next attempt at {time}", { time: stamp(i.next) }) : "",
  ].filter(Boolean);
}

function intakeBlock(r) {
  if (!intakeWaiting(r)) return null;
  const i = pl(r).intake;
  return h("section", { class: "d-intake", "aria-label": t("Briefing") },
    h("h2", { class: "d-sub" }, t("Briefing")),
    intakeLines(i).map((l) => h("div", {}, l)),
    h("div", { class: "q-turn" }, actionButtons(r, "detail", { scope: "detail", groups: ["intake"] })));
}

// acceptedNote is what the notice says once the daemon took a review, an
// address or a run as an intake.
function acceptedNote(name, ref, i) {
  if (!i) return "";
  if (!i.joined) return t("{action} {unit}: accepted as intake {seq}", { action: name, unit: ref, seq: i.seq });
  if (i.state === "starting") return t("{action} {unit}: intake {seq} is already starting the turn; this request changed nothing", { action: name, unit: ref, seq: i.seq });
  return t("{action} {unit}: joined intake {seq}", { action: name, unit: ref, seq: i.seq });
}

function checksBlock(r) {
  const list = state.checks[keyOf(r)];
  if (r.kind !== "checks" || !list || !list.length) return null;
  return h("section", { class: "d-checks", "aria-label": t("Failing checks") },
    h("h2", { class: "d-sub" }, t("Failing checks")),
    h("ul", {}, list.map((c) => h("li", {},
      safeURL(c.url) ? h("a", { href: safeURL(c.url), target: "_blank", rel: "noopener noreferrer" }, c.name) : c.name,
      c.summary ? h("span", { class: "muted" }, ` · ${c.summary}`) : null,
      (c.steps || []).length ? h("span", { class: "muted" }, " · " + t("failed at {steps}", { steps: c.steps.join(", ") })) : null))));
}

function escapeHTML(s) {
  return String(s).replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c]);
}

function reviewRequest(r, b) {
  const url = safeURL(r.link);
  const ref = labelOf(r);
  const title = r.title.replaceAll("`", "'");
  const x = b && b.brief;
  const sections = x ? [[t("Goal:"), [x.goal]], [t("Context:"), [x.context]], [t("Risk:"), [x.risk_why]]]
    .map(([label, lines]) => [label, lines.filter(Boolean)]).filter(([, lines]) => lines.length) : [];
  const ask = t("review, pls?");
  const text = [`${ask} \`${title}\``, url || ref,
    ...sections.flatMap(([label, lines]) => ["", `*${label}*`, ...lines])].join("\n");
  const link = url ? `<a href="${escapeHTML(url)}">${escapeHTML(ref)}</a>` : escapeHTML(ref);
  const html = [`${escapeHTML(ask)} <code>${escapeHTML(title)}</code>`, link,
    ...sections.flatMap(([label, lines]) => ["", `<b>${label}</b>`, ...lines.map(escapeHTML)])].join("<br>");
  return { text, html };
}

async function copyReviewRequest(r, b) {
  const { text, html } = reviewRequest(r, b);
  try {
    if (window.ClipboardItem) {
      await navigator.clipboard.write([new ClipboardItem({
        "text/plain": new Blob([text], { type: "text/plain" }),
        "text/html": new Blob([html], { type: "text/html" }),
      })]);
    } else {
      await navigator.clipboard.writeText(text);
    }
    setNotice(t("Copied the review request for {unit}; paste it in a chat.", { unit: labelOf(r) }));
  } catch (err) {
    setNotice(t("Could not copy: {error}", { error: err.message }), true);
  }
}

// questionRecord is the record the open question comes from: its summary
// and artifact.
function questionRecord(d) {
  const id = d.row.question && d.row.question.record_id;
  const found = ((d.turn && d.turn.records) || []).find((x) => x.id === id);
  return (found && found.record) || {};
}

function answerNote(r) {
  return ((state.guidance || {})[keyOf(r)] || "").trim();
}

// An answer takes the text in the card's box along: alone it is the answer,
// with a choice it is a note to it.
function turnButton(r, slot, label, name, extra, attrs = {}) {
  const st = (r.actions || []).find((a) => a.id === name);
  if (!st) return null;
  const sent = answerSent(r);
  return h("button", {
    type: "button",
    class: ["act", st.enabled && attrs.primary ? "primary" : "", st.enabled ? "" : "off"].filter(Boolean).join(" "),
    title: st.enabled && !st.note && attrs.title ? attrs.title : actionTitle(st, state.specs.byId[name]),
    disabled: state.busy.has(keyOf(r)) || sent || !st.enabled || !!attrs.disabled,
    "data-focus": `detail:${slot}`,
    onclick: () => act(r, name, name === "answer" && answerNote(r) ? { ...extra, guidance: answerNote(r) } : extra),
  }, st.label || label);
}

function questionShown(r) {
  const q = r.question;
  return q ? `${q.record_id || ""}:${q.state}:${!!q.queued}` : "";
}

// answerSent says that an answer to the question r shows was accepted, so its controls stay off until the
// row shows the question it left behind.
function answerSent(r) {
  const key = keyOf(r);
  if (!state.answered.has(key)) return false;
  if (state.answered.get(key) === questionShown(r)) return true;
  state.answered.delete(key);
  return false;
}

// answerBlock is the card's free answer: the box and the button that sends
// its text alone, off while the box is empty.
function answerBlock(r, choices) {
  const send = turnButton(r, "answer", t("Send answer"), "answer", {}, {
    title: t("Send the text as your answer: typed in the agent's session when it is there, else a new session resumes the turn with it"),
    disabled: !answerNote(r),
  });
  const placeholder = choices ? t("Ask for something else, or add a note to the choice above") : t("Tell the agent what to do next");

  return h("div", { class: "q-answer" },
    h("label", { class: "d-sub", for: "guidance" }, guidanceLabel(r)),
    guidanceBox(keyOf(r), placeholder),
    h("div", { class: "q-send" }, send));
}

function recommendedLabel(label, recommended) {
  return recommended ? [label, h("span", { class: "rec" }, " " + t("recommended"))] : label;
}

function optionButton(r, o, recommended) {
  return turnButton(r, `option:${o.id}`, recommendedLabel(o.label, recommended), "answer",
    { option_id: o.id }, { primary: recommended, title: o.consequence });
}

function questionChoices(r, q) {
  if (q.kind === "confirm") {
    return [
      turnButton(r, "yes", recommendedLabel(t("Yes"), q.recommend === "yes"), "answer", { yes: true }, { primary: q.recommend === "yes" }),
      turnButton(r, "no", recommendedLabel(t("No"), q.recommend === "no"), "answer", { no: true }, { primary: q.recommend === "no" }),
    ];
  }
  if (q.kind !== "choice") return [];
  const opts = q.options || [];
  const ordered = [...opts.filter((o) => o.id === q.recommend), ...opts.filter((o) => o.id !== q.recommend)];
  return ordered.map((o) => optionButton(r, o, o.id === q.recommend));
}

function artifactBlock(a) {
  if (!a) return null;
  const items = (a.items || []).map((it) => `${it.path}:${it.line}\n${it.body}`);
  const text = [a.body, ...items].filter(Boolean).join("\n\n").replace(/\n+$/, "");
  if (!text) return null;
  const kind = a.kind ? a.kind.replaceAll("_", " ") : t("artifact");
  const lines = text.split("\n").length;
  const body = h("pre", { class: "artifact-body" }, text);
  if (lines <= ARTIFACT_LINES) return h("section", { class: "d-artifact", "aria-label": kind }, h("div", { class: "d-sub" }, kind), body);
  return h("details", { class: "d-artifact" }, h("summary", { class: "d-sub" }, tn("{kind}, {n} lines", lines, { kind })), body);
}

// answerableQuestion is the detail's question whose options the keys pick:
// asked, its answer not queued.
function answerableQuestion(d) {
  const q = d && d.row.question;
  return q && q.state === "asked" && !q.queued ? q : null;
}

// questionBlock is the turn that waits on the user: the agent's question
// with its options, its stop with the last message, or its queued answer.
function questionBlock(d) {
  const r = d.row;
  const q = r.question;
  if (!q) return null;
  const rec = questionRecord(d);
  const asked = q.state === "asked" && !q.queued;
  const head = q.queued ? t("Your answer is queued") : asked ? t("Agent asks") : t("Agent stopped");
  const last = q.last_message || (d.turn && d.turn.last_message) || "";
  const choices = asked ? questionChoices(r, q) : [];
  return h("section", { class: "d-question", "aria-label": head },
    h("h2", { class: "d-sub" }, head),
    q.queued ? h("p", { class: "muted small" }, t("The agent gets it once its session is free. An answer sent now replaces it.")) : null,
    rec.summary ? h("p", { class: "summary" }, rec.summary) : null,
    q.verdict ? h("p", { class: "verdict" }, t("drafted {verdict}, {blocking} blocking, {nonBlocking} non-blocking", { verdict: q.verdict, blocking: q.blocking, nonBlocking: q.non_blocking })) : null,
    asked && q.text ? h("pre", { class: "q-text" }, q.text) : null,
    !asked && !q.queued && last ? [h("div", { class: "muted small" }, t("Last message")), h("pre", { class: "q-text" }, last)] : null,
    asked && q.kind === "free" && q.recommend ? h("p", { class: "recommend" }, t("Agent recommends: {answer}", { answer: q.recommend })) : null,
    choices.length ? h("div", { class: "q-options" }, choices) : null,
    answerBlock(r, choices.length > 0),
    h("div", { class: "q-turn" },
      turnButton(r, "continue", t("Continue"), "continue", {}, { title: t("The agent goes on with the turn without an answer") }),
      turnButton(r, "dismiss", t("Dismiss"), "dismiss", {}, { title: t("Close the question; the pull request goes back to whoever it waits on") })),
    artifactBlock(rec.artifact));
}

// answerSaid leaves out the option a client already worded as the text.
function answerSaid(a) {
  const choice = a.option_id ? `option ${a.option_id}` : a.yes ? "yes" : a.no ? "no" : "";
  const note = (a.text || "").trim();
  if (!choice || (a.option_id && note.endsWith(`(${choice})`))) return note || choice || "no";
  return note ? `${choice}. ${note}` : choice;
}

// timelineEvents shows the current turn's answers from the turn, which knows
// where each came from, in place of their one-line events.
function timelineEvents(d) {
  const turn = d.turn;
  const answers = turn ? (turn.answers || []).filter((a) => a.turn_seq === turn.turn.seq) : [];
  const since = turn ? Date.parse(turn.turn.sent_at) || 0 : 0;
  const events = (d.events || []).filter((e) => !(answers.length && e.kind === "answered" && Date.parse(e.at) >= since));
  for (const a of answers) {
    const vars = { answer: answerSaid(a), source: a.source };
    const data = a.route === "queued" ? t("{answer} (from {source}, queued until the agent stops)", vars) : t("{answer} (from {source})", vars);
    events.push({ kind: "answered", at: a.at, actor: "", data });
  }
  return events.sort((a, b) => Date.parse(b.at) - Date.parse(a.at));
}

function defaultDetailWidth() {
  const px = parseFloat(getComputedStyle(document.documentElement).getPropertyValue("--detail-width"));
  return Number.isFinite(px) ? px : DETAIL_WIDTH_MIN;
}

function clampDetailWidth(px, viewport) {
  const max = Math.max(DETAIL_WIDTH_MIN, Math.floor(viewport * 0.6));
  const w = Number.isFinite(px) ? Math.round(px) : defaultDetailWidth();
  return Math.min(max, Math.max(DETAIL_WIDTH_MIN, w));
}

function storedDetailWidth(viewport) {
  try {
    const raw = localStorage.getItem(DETAIL_WIDTH_KEY);
    if (raw === null) return defaultDetailWidth();
    return clampDetailWidth(Number(raw), viewport);
  } catch {
    return defaultDetailWidth();
  }
}

function saveDetailWidth(px) {
  try {
    if (px === null) localStorage.removeItem(DETAIL_WIDTH_KEY);
    else localStorage.setItem(DETAIL_WIDTH_KEY, String(px));
  } catch {
    // The width still applies until the page reloads.
  }
}

function setupDetailResize() {
  const handle = document.getElementById("detail-resize");
  const root = document.documentElement;
  let preferred = storedDetailWidth(window.innerWidth);
  let width = preferred;
  const layout = () => {
    width = clampDetailWidth(preferred, window.innerWidth);
    root.style.setProperty("--detail-width", width + "px");
    handle.setAttribute("aria-valuenow", String(width));
    handle.setAttribute("aria-valuemin", String(DETAIL_WIDTH_MIN));
    handle.setAttribute("aria-valuemax", String(Math.max(DETAIL_WIDTH_MIN, Math.floor(window.innerWidth * 0.6))));
  };
  const choose = (px) => {
    preferred = clampDetailWidth(px, window.innerWidth);
    layout();
  };
  layout();
  handle.addEventListener("pointerdown", (e) => {
    handle.setPointerCapture(e.pointerId);
    document.body.classList.add("resizing");
    e.preventDefault();
  });
  handle.addEventListener("pointermove", (e) => {
    if (handle.hasPointerCapture(e.pointerId)) choose(window.innerWidth - e.clientX);
  });
  const release = (e) => {
    if (!handle.hasPointerCapture(e.pointerId)) return;
    handle.releasePointerCapture(e.pointerId);
    document.body.classList.remove("resizing");
    saveDetailWidth(preferred);
  };
  handle.addEventListener("pointerup", release);
  handle.addEventListener("pointercancel", release);
  handle.addEventListener("keydown", (e) => {
    const delta = e.key === "ArrowLeft" ? DETAIL_WIDTH_STEP : e.key === "ArrowRight" ? -DETAIL_WIDTH_STEP : 0;
    if (!delta) return;
    e.preventDefault();
    choose(width + delta);
    saveDetailWidth(preferred);
  });
  handle.addEventListener("dblclick", () => {
    root.style.removeProperty("--detail-width");
    choose(defaultDetailWidth());
    saveDetailWidth(null);
  });
  window.addEventListener("resize", layout);
}

// detailFacts are the row's plugin's own detail fields, as its catalog
// words them, read from the detail or the row's override.
function detailFacts(r, d, o) {
  const provider = unitOf(r).provider;
  return catalog.details.filter((x) => x.provider === provider).map((x) => {
    const v = d[x.field] ?? o[x.field];
    return v ? [h("dt", x.title ? { title: x.title } : {}, x.label), h("dd", {}, v)] : null;
  });
}

function renderDetail() {
  const box = document.getElementById("detail");
  if (renderTaskDetail(box, readHash())) return;
  const { pr } = readHash();
  const d = state.detail;
  if (!pr || !d || keyOf(d.row) !== pr) {
    box.hidden = true;
    box.replaceChildren();
    document.body.classList.remove("has-detail");
    return;
  }
  const r = d.row;
  const p = pl(r);
  const o = p.override || {};
  const key = keyOf(r);
  const opening = box.hidden || box.dataset.key !== key;
  const inCard = !!r.question;
  const guidance = inCard ? null : guidanceBox(key, guidanceLabel(r));
  const muteState = (p.mute || {}).until_state_change ? t("muted until who it waits on changes")
    : isMuted(p) ? t("muted until {time}", { time: stamp(p.mute.until) }) : "";
  const events = timelineEvents(d);

  const top = h("div", { class: "d-top" },
    h("div", { class: "d-head" },
      h("button", { type: "button", class: "close", "aria-label": t("Close detail"), "data-focus": "detail:close", onclick: closeDetail }, "×"),
      detailWindowControls(),
      h("h1", {}, r.title || labelOf(r)),
      h("div", { class: "meta" },
        h("span", { class: "ref" }, labelOf(r)),
        " ", h("a", { href: safeURL(r.link), target: "_blank", rel: "noopener noreferrer", class: "ext", "data-focus": "detail:link" }, providerName(unitOf(r).provider)),
        p.author ? [" ", t("by"), " ", h("span", { class: "who" }, p.author)] : null)),
    h("div", { class: "d-sit " + waitClass(r) + (sm(r).stalled ? " late" : "") },
      h("div", { class: "d-clock" }, roundGate(r) ? countdown(r) : h("span", { class: "age" }, age(sm(r).since)), h("span", { class: "pill" }, rowKindLabel(r))),
      h("div", {}, t("Waiting on"), " ", h("strong", {}, waitingLabel(r)), isSet(sm(r).since) ? " " + t("since {time}", { time: stamp(sm(r).since) }) : ""),
      h("div", { class: "why" }, reasonText(r)),
      sm(r).line && sm(r).line !== sm(r).reason ? h("div", { class: "line" }, sm(r).line) : null,
      sm(r).stalled ? h("div", { class: "late-note" }, t("Past its threshold")) : null),
    checksBlock(r),
    h("dl", { class: "d-facts" },
      h("dt", {}, t("In scope as")), h("dd", {}, (p.reasons || []).join(", ") || t("out of scope")),
      h("dt", {}, t("Workspace")), h("dd", {}, p.workspace && p.workspace.display_name ? where(r, "detail") : t("none")),
      h("dt", {}, t("Controls")), h("dd", {}, [o.hold ? t("paused") : "", muteState, o.adopted ? t("adopted") : ""].filter(Boolean).join(", ") || t("none")),
      detailFacts(r, d, o)),
    questionBlock(d),
    unitExtras(d),
    trimmedNote(d),
    intakeBlock(r),
    inCard ? null : h("label", { class: "d-sub", for: "guidance" }, guidanceLabel(r)));
  const acts = h("div", { class: "d-acts" },
    h("div", { class: "d-actions" }, [...actionButtons(r, "detail", { scope: "detail", run: (id) => act(r, id, { guidance: guidance ? guidance.value.trim() : answerNote(r) }) }), h("button", { type: "button", class: "act", "data-focus": "detail:copy-review", title: t("Copy a review request with the title, link and brief, formatted for a chat message"), onclick: () => copyReviewRequest(r, d.brief) }, t("Copy review request"))]),
    actionNotes(r, "detail"),
    briefBlock(d.brief, r, readLens()));
  const log = h("div", { class: "d-log" },
    h("h2", { class: "d-sub" }, t("Timeline")),
    h("ol", { class: "timeline" }, events.map((e) => h("li", { class: "ev-" + e.kind },
      h("time", { datetime: e.at }, stamp(e.at)),
      h("div", {}, h("span", { class: "kind" }, eventLabel(e.kind)), e.actor ? h("span", { class: "who" }, " " + e.actor) : null),
      e.data ? h("span", { class: "data" }, e.data) : null))));

  keepFocusIn(box, () => {
    if (box.querySelector(".d-acts") && (!guidance || guidance.parentNode === box)) {
      box.querySelector(".d-top").replaceWith(top);
      box.querySelector(".d-acts").replaceWith(acts);
      box.querySelector(".d-log").replaceWith(log);
    } else {
      box.replaceChildren(...[top, guidance, acts, log].filter(Boolean));
    }
  });
  box.dataset.key = key;
  box.hidden = false;
  document.body.classList.add("has-detail");
  if (opening && matchMedia("(max-width: 900px)").matches) box.scrollIntoView({ block: "start" });
}

// keepFocusIn runs redraw, which rebuilds the controls inside box, and gives the focus, the caret and the
// scroll back to the rebuilt control of the slot that had them, so a sync does not interrupt typing.
function keepFocusIn(box, redraw) {
  const was = document.activeElement && box.contains(document.activeElement) ? document.activeElement : null;
  const slot = was && was.dataset && was.dataset.focus;
  const caret = slot && typeof was.selectionStart === "number" ? [was.selectionStart, was.selectionEnd, was.selectionDirection] : null;
  const scroll = was ? was.scrollTop : 0;
  redraw();
  const now = slot && box.querySelector(`[data-focus="${CSS.escape(slot)}"]`);
  if (!now || now === was) return;
  now.focus({ preventScroll: true });
  if (caret && now.setSelectionRange) now.setSelectionRange(...caret);
  now.scrollTop = scroll;
}

function revealOpenRow(pr) {
  if (!pr) {
    state.revealed = "";
    return;
  }
  if (state.revealed === pr) return;
  const slot = openSlot(pr);
  const row = (slot && document.querySelector(`[data-focus="${CSS.escape(slot)}"]`)) || document.querySelector(".row.sel, .card.sel");
  if (!row) return;
  state.revealed = pr;
  row.scrollIntoView({ block: "nearest" });
}

function ago(iso) {
  if (!isSet(iso)) return "";
  return Date.now() - Date.parse(iso) < 60000 ? t("just now") : t("{age} ago", { age: age(iso) });
}

function candidateURL(c) {
  return safeURL(c.url);
}

function hasTerm(text, term) {
  return tokens(text).includes(term);
}

function toggledQuery(text, term) {
  const words = tokens(text);
  return (words.includes(term) ? words.filter((w) => w !== term) : [...words, term]).join(" ");
}

function querySort(text) {
  const sorts = text.split(/\s+/).filter((w) => w.startsWith("sort:"));
  return sorts.length ? sorts[sorts.length - 1].slice("sort:".length) : exploreSearch().default_sort;
}

function withSort(text, sort) {
  return [...text.split(/\s+/).filter((w) => w && !w.startsWith("sort:")), `sort:${sort}`].join(" ");
}

function windowDate(today, days) {
  const d = new Date(today.getFullYear(), today.getMonth(), today.getDate() - days);
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
}

function hasWindow(text) {
  return text.split(/\s+/).some((w) => w.startsWith("updated:>="));
}

// withWindow replaces any updated:>= term with one days before today, or
// only drops it when days is 0.
function withWindow(text, today, days) {
  const words = text.split(/\s+/).filter((w) => w && !w.startsWith("updated:>="));
  return [...words, ...(days ? [`updated:>=${windowDate(today, days)}`] : [])].join(" ");
}

function toggleWindow(days) {
  const input = document.getElementById("explore-q");
  input.value = withWindow(input.value, new Date(), hasWindow(input.value) ? 0 : days);
  writeHash({ x: input.value }, true);
}

function pickSort(sort) {
  const input = document.getElementById("explore-q");
  input.value = withSort(input.value, sort);
  writeHash({ x: input.value }, true);
}

function waited(ms) {
  const hours = Math.floor(ms / 3600000);
  if (hours < 1) return t("{minutes}m", { minutes: Math.floor(ms / 60000) });
  if (hours < 48) return t("{hours}h", { hours });
  return t("{days}d", { days: Math.floor(hours / 24) });
}

// candidateWait counts from the last activity, or from the row's own clock
// when the inventory holds the pull request.
function candidateWait(c, row, hours, now = Date.now()) {
  const tracked = row && isSet(sm(row).since);
  const since = tracked ? sm(row).since : c.updated_at;
  if (!isSet(since)) return null;
  const ms = Math.max(0, now - Date.parse(since));
  return { text: t("waiting {time}", { time: waited(ms) }), late: tracked ? !!sm(row).stalled : ms > (hours || 48) * 3600000 };
}

function toggleTerm(term) {
  const input = document.getElementById("explore-q");
  input.value = toggledQuery(input.value, term);
  writeHash({ x: input.value }, true);
}

// viewOfExplore is the route of the view name of the plugin that serves the Explore view.
function viewOfExplore(name) {
  const provider = (catalog.views.find((v) => v.id === "explore") || {}).provider || "";
  return `/api/plugins/${encodeURIComponent(provider)}/views/${encodeURIComponent(name)}`;
}

async function explore(query) {
  const input = document.getElementById("explore-q");
  query = String(query || "").trim();
  if (!query || state.explore.busy) return;
  input.value = query;
  state.explore.busy = true;
  state.explore.error = "";
  writeHash({ x: query }, true);
  try {
    const res = await fetch(viewOfExplore("explore"), {
      method: "POST",
      headers: { "Content-Type": "application/json", Accept: "application/json", "X-Loop-API": API_VERSION },
      body: JSON.stringify({ query }),
    });
    const out = await res.json().catch(() => ({}));
    if (!res.ok || out.error) throw new Error(apiError(out, `HTTP ${res.status}`, res.status));
    state.explore.result = out;
    state.explore.recent = out.recent || state.explore.recent;
    state.explore.sel = out.candidates.length === 1 ? keyOf(out.candidates[0]) : "";
  } catch (err) {
    state.explore.error = t("Explore failed: {error}", { error: err.message });
  }
  state.explore.busy = false;
  render();
}

function exploreStatus() {
  const ex = state.explore;
  if (ex.busy) return t("Searching {provider}…", { provider: searchService() });
  if (ex.error) return ex.error;
  const r = ex.result;
  if (!r) return t("A search runs only when you press Enter or Search; each one spends about 1 point of the hourly API budget of {provider}.", { provider: searchService() });
  const shown = r.candidates.length;
  const order = (exploreSearch().sorts || []).find((s) => s.sort === querySort(r.query || ""));
  const cost = r.rate_limit && r.rate_limit.limit ? " · " + t("cost {cost}, {remaining} of {limit} left", { cost: r.cost, remaining: r.rate_limit.remaining, limit: r.rate_limit.limit }) : "";
  const read = r.read ? `${r.read} · ` : "";
  if (!shown && r.empty) return `${read}${r.empty}${cost}`;
  const vars = { count: tn("{n} pull requests", shown), shown, order: order ? order.shown : "", total: r.total, query: r.query };
  if (r.total <= shown) return read + t("{count} for {query}", vars) + cost;
  return read + (order ? t("{count}, the {shown} {order} of {total}; narrow the search for the rest for {query}", vars) : t("{count}, the first {shown} of {total}; narrow the search for the rest for {query}", vars)) + cost;
}

function candidateItem(c, sel) {
  const key = keyOf(c);
  const row = rowByKey(key);
  const facts = [
    `+${c.additions} -${c.deletions} · ` + tn("{n} files", c.changed_files),
    reviewDecision(c.review_decision),
    c.checks ? t("checks {state}", { state: c.checks.toLowerCase() }) : t("no checks"),
    tn("{n} comments", c.comments),
  ];
  if ((c.review_requests || []).length) facts.push(t("asks {who}", { who: c.review_requests.map((w) => w.replace(/^team:/, "")).join(", ") }));
  if (c.my_review) {
    const review = c.my_review.toLowerCase().replace("_", " ");
    facts.push(c.my_review_on_head ? t("my review: {review}", { review }) : t("my review: {review} (older head)", { review }));
  }
  const b = c.brief && c.brief.brief;
  const wait = candidateWait(c, row, state.health && state.health.stalled_review_hours);
  return h("li", { class: ["cand", row ? waitClass(row) : "", key === sel ? "sel" : ""].join(" ") },
    h("button", { type: "button", class: "cand-pick", "aria-pressed": String(key === sel), "data-focus": `cand:${key}`, onclick: () => selectCandidate(key) },
      h("span", { class: "title" }, c.title || labelOf(c)),
      h("span", { class: "meta" }, t("{unit} by {author} · opened {opened} · updated {updated}", { unit: labelOf(c), author: c.author, opened: ago(c.created_at), updated: ago(c.updated_at) }),
        wait ? h("span", { class: "wait" + (wait.late ? " late" : ""), title: row ? t("waiting on {who}", { who: waitingLabel(row) }) : t("since the last activity") }, wait.text) : null,
        c.draft ? h("span", { class: "flag" }, t("draft")) : null,
        c.state && c.state !== "OPEN" ? h("span", { class: "flag" }, c.state.toLowerCase()) : null)),
    h("div", { class: "facts" }, facts.join(" · ")),
    row ? h("div", { class: "inv" }, h("span", { class: "pill" }, rowKindLabel(row)),
      " " + t("waiting on {who} for {age} · in {name} as {reasons}", { name: "{{name}}", who: waitingLabel(row), age: age(sm(row).since), reasons: (pl(row).reasons || []).join(", ") || t("out of scope") }))
      : h("div", { class: "inv" }, t("not in {name}", { name: "{{name}}" })),
    b ? h("div", { class: "brief-line" }, briefTags({ size: b.size, risk: b.risk, open_questions: (b.open_questions || []).length }), ` ${b.kind} · ${b.goal}`) : null,
    candidateStatus(key));
}

function candidateStatus(key) {
  const st = state.explore.status[key];
  if (!st) return null;
  return h("div", { class: "inv cand-status" + (st.error ? " err" : ""), role: "status" }, st.text,

    st.done && rowByKey(key) ? h("button", { type: "button", class: "act", "data-focus": `cand:open:${key}`, onclick: () => writeHash({ pr: key }) }, t("Open in {name}", { name: "{{name}}" })) : null);
}

// actOnCandidate brings a pull request the inventory lacks in first, so its
// card shows it in the loop while the action runs, then reports the outcome on
// the card itself.
async function actOnCandidate(c, name, extra = {}) {
  const key = keyOf(c);
  const status = (text, more = {}) => { state.explore.status[key] = { text, ...more }; render(); };
  const implicit = !rowByKey(key);
  if (implicit) {
    status(t("adding it to {name}…", { name: "{{name}}" }));
    if (!(await act(c, "track"))) return status(state.actionError || t("adding it to {name} failed", { name: "{{name}}" }), { error: true }), false;
    await load();
    if (name === "track") return status(t("added to {name}", { name: "{{name}}" }), { done: true }), true;
  }
  const row = rowByKey(key) || c;
  const offered = (row.actions || []).find((a) => a.id === name);
  const refused = offered && !offered.enabled ? offered.reason : "";
  const ok = refused ? undefined : (status(t("{action} running…", { action: name })), await act(row, name, extra));
  await load();
  if (ok === undefined && implicit) {
    const undone = (await act(row, "untrack")) ? t("untracked it again") : t("untracking it failed too: {error}", { error: state.actionError });
    status(t("{action} {unit}: {why}; {undone}", { action: name, unit: labelOf(c), why: refused || t("cancelled"), undone }), { error: true });
    return false;
  }
  if (refused) return status(`${name} ${labelOf(c)}: ${refused}`, { error: true }), false;
  status(ok ? (implicit ? t("{action} started; it is in {name} now", { name: "{{name}}", action: name }) : t("{action} sent", { action: name })) : (state.actionError || t("{action} failed", { action: name })), { error: !ok, done: ok });
  return ok;
}

function selectCandidate(key) {
  state.explore.sel = state.explore.sel === key ? "" : key;
  render();
}

function renderExploreActions() {
  const ex = state.explore;
  const panel = document.getElementById("explore-act");
  const c = ex.result && ex.result.candidates.find((x) => keyOf(x) === ex.sel);
  panel.hidden = !c;
  if (!c) return;
  const key = keyOf(c);
  const row = rowByKey(key);
  const guidance = document.getElementById("explore-guidance");
  if (guidance.dataset.key !== key) {
    guidance.dataset.key = key;
    guidance.value = state.guidance[key] || "";
  }
  document.getElementById("explore-act-head").replaceChildren(
    h("a", { href: candidateURL(c), target: "_blank", rel: "noopener noreferrer", "data-focus": "explore:gh" }, labelOf(c)), " ", c.title);
  document.getElementById("explore-act-buttons").replaceChildren(
    ...actionButtons(c, "explore", { scope: "explore", run: (id) => actOnCandidate(c, id, { guidance: guidance.value.trim() }) }),
    row ? h("button", { type: "button", class: "act", disabled: state.busy.has(key), "data-focus": "explore:detail", onclick: () => writeHash({ pr: key }) }, t("Detail")) : null);
}

function renderExplore(view) {
  const box = document.getElementById("explore");
  box.hidden = view !== "explore";
  if (box.hidden) return;
  const ex = state.explore;
  const input = document.getElementById("explore-q");
  const { x } = readHash();
  if (document.activeElement !== input && input.value !== x) input.value = x;
  document.getElementById("explore-go").disabled = ex.busy;
  document.getElementById("explore-chips").replaceChildren(
    h("span", { class: "chip-head" }, t("Run")),
    ...(exploreSearch().presets || []).map((p) => chip({
      title: p.query, disabled: ex.busy, "data-focus": `explore:preset:${p.query}`, onclick: () => explore(p.query),
    }, p.label)),
    h("span", { class: "chip-head" }, t("Filters")),
    ...(exploreSearch().filters || []).map((f) => f.window ? chip({
      title: "updated:>=", "aria-pressed": String(hasWindow(input.value)), "data-focus": "explore:filter:window", onclick: () => toggleWindow(f.window),
    }, f.label) : chip({
      "aria-pressed": String(hasTerm(input.value, f.term)), "data-focus": `explore:filter:${f.term}`, onclick: () => toggleTerm(f.term),
    }, f.label)),
    h("span", { class: "chip-head" }, t("Order")),
    ...(exploreSearch().sorts || []).map((o) => chip({
      title: `sort:${o.sort}`, "aria-pressed": String(querySort(input.value) === o.sort), "data-focus": `explore:sort:${o.sort}`, onclick: () => pickSort(o.sort),
    }, o.label)),
    ex.recent.length ? h("span", { class: "chip-head" }, t("Recent")) : null,
    ...ex.recent.map((q) => chip({ title: q, "data-focus": `explore:recent:${q}`, onclick: () => explore(q) }, q)));
  const status = document.getElementById("explore-status");
  const cands = (ex.result && ex.result.candidates) || [];
  status.replaceChildren(exploreStatus(), cands.length ? h("button", {
    type: "button", class: "act", "data-focus": "explore:copy", title: t("Copy these candidates as YAML, to paste for an agent"), onclick: copyCandidates,
  }, t("Copy list")) : "");
  status.classList.toggle("err", !!ex.error && !ex.busy);
  document.getElementById("explore-results").replaceChildren(...((ex.result && ex.result.candidates) || []).map((c) => candidateItem(c, ex.sel)));
  renderExploreActions();
}

const PLAN_ORDER = ["pending", "blocked"];
function planHead(decision) {
  return decision === "blocked" ? t("Blocked") : t("Pending");
}
// PLAN_ACTS are the plan actions POST /api/plan carries, by spec id; an
// item's other actions are its unit's.
const PLAN_ACTS = { plan_apply: "apply", plan_dismiss: "dismiss", guide: "guide" };

// planGroups are the items waiting on the user by decision, pending first,
// and within pending those that run by themselves soonest first.
function planGroups(items) {
  const waits = (it) => (it.default === "run" ? 0 : 1);
  const at = (it) => (it.deadline ? Date.parse(it.deadline) : Infinity);
  return PLAN_ORDER.map((decision) => ({
    decision,
    items: items.filter((it) => it.decision === decision).sort((a, b) => waits(a) - waits(b) || at(a) - at(b)),
  })).filter((g) => g.items.length);
}

// planWhen says when an item runs: at its window's end, when the user
// applies it, or not while it is blocked.
function planWhen(it, now = Date.now()) {
  if (it.decision === "blocked") return t("blocked");
  if (it.default !== "run") return t("waits for you");
  if (!it.deadline) return t("runs now");
  const s = Math.ceil((Date.parse(it.deadline) - now) / 1000);
  if (s <= 0) return t("running");
  if (s <= 99) return t("runs in {seconds} s", { seconds: s });
  return t("runs in {minutes}m {seconds}s", { minutes: Math.floor(s / 60), seconds: String(s % 60).padStart(2, "0") });
}

function planCount() {
  return state.plan.loaded ? planGroups(state.plan.items).reduce((n, g) => n + g.items.length, 0) : "";
}

const itemLabel = (it) => it.label || unitKey(it.unit);
const planName = (it) => (it.title ? `${itemLabel(it)} · ${it.title}` : itemLabel(it));

async function loadPlan() {
  const view = readHash().view;
  try {
    const res = await getJSON(view === "plan" ? "/api/plan?closed=1" : "/api/plan");
    state.plan = { items: res.items || [], closed: view === "plan" ? res.closed || [] : state.plan.closed, error: (res.errors || []).join("; "), loaded: true };
  } catch (err) {
    state.plan = { ...state.plan, error: t("Could not load the plan: {error}", { error: err.message }), loaded: true };
  }
}

async function actOnPlan(it, id) {
  if (!PLAN_ACTS[id]) return act({ unit: it.unit, actions: it.actions, label: planName(it) }, id);
  const body = { ids: [it.id], action: PLAN_ACTS[id] };
  if (id === "guide") {
    const guidance = prompt(t("Guidance for {workflow} on {item}", { workflow: it.workflow, item: planName(it) }), "");
    if (guidance === null || !guidance.trim()) return;
    body.guidance = guidance.trim();
  }
  state.busy.add(it.id);
  render();
  try {
    const res = await fetch("/api/plan", {
      method: "POST",
      headers: { "Content-Type": "application/json", Accept: "application/json", "X-Loop-API": API_VERSION },
      body: JSON.stringify(body),
    });
    const out = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(apiError(out, `HTTP ${res.status}`, res.status));
    if ((out.errors || []).length) throw new Error(out.errors.join("; "));
    setNotice(`${body.action}: ${it.workflow} ${planName(it)}`);
  } catch (err) {
    setNotice(t("{action} failed: {error}", { action: `${body.action} ${it.workflow} ${planName(it)}`, error: err.message }), true);
  } finally {
    state.busy.delete(it.id);
  }
  await loadPlan();
  render();
}

function planButtons(it) {
  return (it.actions || [])
    .map((st) => [st, state.planSpecs[st.id]])
    .filter(([, sp]) => sp && sp.surfaces.includes("detail"))
    .map(([st, sp]) => h("button", {
      type: "button",
      class: ["act", st.enabled && st.primary ? "primary" : "", st.enabled ? "" : "off"].filter(Boolean).join(" "),
      title: actionTitle(st, sp),
      "data-focus": `plan:${it.id}:${st.id}`,
      disabled: state.busy.has(it.id) || !st.enabled,
      onclick: () => actOnPlan(it, st.id),
    }, st.label || sp.label));
}

function planItem(it) {
  const ticking = it.decision === "pending" && it.default === "run" && it.deadline;
  return h("li", { class: `plan-item ${it.decision}` },
    h("div", { class: "plan-line" },
      h("span", { class: "plan-when" + (ticking ? " plan-left" : ""), "data-deadline": ticking ? it.deadline : undefined, title: ticking ? t("runs at {time}", { time: stamp(it.deadline) }) : undefined }, planWhen(it)),
      h("span", { class: "plan-wf" }, it.workflow),
      h("span", { class: "plan-unit" }, safeURL(it.link) ? h("a", { href: safeURL(it.link), target: "_blank", rel: "noopener noreferrer" }, itemLabel(it)) : itemLabel(it)),
      it.title ? h("span", { class: "plan-title" }, it.title) : null,
      it.observe ? h("span", { class: "plan-mode", title: t("This workflow observes: it only plans, and nothing runs until you apply an item or turn observe off") }, t("observe")) : null),
    it.reason ? h("p", { class: "plan-reason" }, it.reason) : null,
    it.error ? h("p", { class: "plan-error" }, t("attempt {n}: {error}", { n: it.attempts, error: it.error })) : null,
    h("div", { class: "d-actions" }, ...planButtons(it)));
}

function closedItem(c) {
  return h("li", { class: "plan-item closed" },
    h("div", { class: "plan-line" },
      h("time", { datetime: c.at, title: stamp(c.at) }, age(c.at)),
      h("span", { class: "plan-unit" }, c.session),
      h("span", { class: "plan-reason" }, c.reason)),
    c.resume ? h("div", { class: "plan-resume" },
      h("code", {}, c.resume),
      h("button", { type: "button", class: "act", "data-focus": `plan:closed:${c.session}`, title: t("Copy the command that resumes this session"),
        onclick: () => navigator.clipboard.writeText(c.resume).then(() => setNotice(t("copied the resume line of {session}", { session: c.session })), (err) => setNotice(t("could not copy the resume line of {session}: {error}", { session: c.session, error: err.message }), true)) }, t("Copy"))) : null);
}

function renderPlan(view) {
  const box = document.getElementById("plan");
  box.hidden = view !== "plan";
  if (box.hidden) return;
  const p = state.plan;
  const groups = planGroups(p.items);
  const parts = [];
  if (p.error) parts.push(h("p", { class: "plan-error" }, p.error));
  if (!groups.length) parts.push(h("p", { class: "empty" }, p.loaded ? t("Nothing pending or blocked in the plan.") : t("Loading the plan.")));
  for (const g of groups) {
    parts.push(h("h2", { class: "plan-head" }, `${planHead(g.decision)} (${g.items.length})`), h("ol", { class: "plan-items" }, ...g.items.map(planItem)));
  }
  if (p.closed.length) {
    parts.push(h("h2", { class: "plan-head" }, t("Sessions closed in the last day ({n})", { n: p.closed.length })), h("ol", { class: "plan-items" }, ...p.closed.map(closedItem)));
  }
  box.replaceChildren(...parts);
}

async function loadExploreRecent() {
  if (readHash().view !== "explore") return;
  try {
    state.explore.recent = await getJSON(viewOfExplore("explore.recent"));
  } catch {
    // Without the chips the search box still works.
  }
}

function restoreFocus() {
  if (!lastFocus) return;
  const active = document.activeElement;
  if (active && active !== document.body && active.isConnected) return;
  const el = document.querySelector(`[data-focus="${CSS.escape(lastFocus)}"]`) || rowHomeOf(lastFocus);
  if (el && !el.disabled) el.focus({ preventScroll: true });
}

// rowHomeOf is the element that holds the focus of the row a vanished slot was a control of: a facet whose
// value changed, an action no longer served.
function rowHomeOf(slot) {
  const row = [...document.querySelectorAll("[data-row]")].map((r) => r.getAttribute("data-row"))
    .filter((r) => slot.startsWith(r + ":") || slot.startsWith(`facet:${r}:`))
    .sort((a, b) => b.length - a.length)[0];
  return row ? document.querySelector(`[data-focus="${CSS.escape(row)}"]`) : null;
}

function renderSearch() {
  const box = document.getElementById("search");
  const { q } = readHash();
  if (document.activeElement !== box && box.value !== q) box.value = q;
}

// A pasted URL of a pull request the inventory holds opens it instead of
// filtering for text no row contains. A URL typed by hand passes through
// shorter URLs of other pull requests on its way, so only a paste jumps.
function onSearch(value, pasted) {
  const row = pasted && rowByURL(value);
  if (row) {
    document.getElementById("search").value = "";
    lastFocus = `row:${keyOf(row)}`;
    writeHash({ q: "", pr: keyOf(row) });
    return;
  }
  writeHash({ q: value.slice(0, QUERY_MAX) }, true);
}

function render() {
  renderSearch();
  renderSync();
  renderPause();
  renderWarnings();
  renderRail(railNav(readLens()), readHash());
  renderExplore(readHash().view);
  renderPlan(readHash().view);
  renderPluginView(readHash().view);
  renderTasks(readHash());
  renderScreens(readHash());
  renderDash(readLens());
  renderRows();
  renderDetail();
  syncWindowChrome();
  revealOpenRow(readHash().pr);
  watchWrapped(document);
  restoreFocus();
}

async function getJSON(url) {
  const res = await fetch(url, { headers: { Accept: "application/json", "X-Loop-API": API_VERSION }, signal: AbortSignal.timeout(REQUEST_TIMEOUT_MS) });
  if (res.status === 401) state.authLost = true;
  const body = await res.json().catch(() => ({}));
  if (!res.ok) throw Object.assign(new Error(apiError(body, `${url}: HTTP ${res.status}`, res.status)), { status: res.status });
  return body;
}

// unitFallback is the route a unit with no detail opens instead: the first
// open task that holds it; "" when none does or the read failed otherwise.
function unitFallback(err, tasks) {
  return err && err.status === 404 && tasks && tasks.length ? "#" + viewHash({ task: tasks[0].id, pr: "" }) : "";
}

// homeState is the home the daemon last served, and why it could not serve it.
const homeState = { home: null, error: "" };

async function loadHome() {
  if (readHash().view !== HOME) return;
  try {
    homeState.home = await getJSON("/api/home");
    homeState.error = "";
  } catch (err) {
    homeState.error = t("Could not load the home: {error}", { error: err.message });
  }
}

async function loadQueue() {
  const route = readRoute(location.hash);
  if (route.view === QUEUE) await queueLoad(route, screenEnv());
}

// screenRow is task e's row as the list of view draws it, the profile's first view when none is named.
function screenRow(e, view) {
  const lens = lensOf({ view: view || (coreViews()[0] || { id: "" }).id, q: "", g: "", s: "", layout: "", area: "", ctx: [], tk: "", done: false });
  return rowView(taskModel(e, "", "", lens), lens);
}

// openTaskSource opens unit u of task e at its source, else the task's first unit's; the unit's detail when
// no card links it.
function openTaskSource(e, u) {
  const card = ((e && e.cards) || []).find((x) => x.card && u && x.card.unit.provider === u.provider && x.card.unit.key === u.key);
  const link = (card && safeURL(card.card.link)) || sourceLink(e);
  if (link) window.open(link, "_blank", "noopener");
  else if (u) writeHash({ pr: u.provider + ":" + u.key, task: "" });
}

// pageGates are the gates the page draws as cards, each with its task: the detail's, the queue's item's and
// the gates list's.
function pageGates() {
  const out = [];
  const e = detailState.task;
  if (e) out.push({ task: e, gates: e.gates || [] });
  const queue = queueShown(readRoute(location.hash));
  const item = queue && (queue.items || [])[queueState.at];
  if (item && item.gate) out.push({ task: item.task, gates: [item.gate] });
  for (const g of tasksState.gates || []) out.push({ task: taskById(g.task), gates: [g] });
  return out;
}

// pageQuestion is the gate a digit answers from el: the one whose card holds el, else, on a queue, its item's
// gate when that is no draft's and no detail is open over the queue.
function pageQuestion(el) {
  const at = gateAt(el, pageGates());
  if (at || (el && el.closest && el.closest(".d-gate"))) return at;
  const route = readRoute(location.hash);
  if (route.view !== QUEUE || route.task || route.unit || (el && el.closest && el.closest("#detail"))) return null;
  const queue = queueShown(route);
  const item = queue && (queue.items || [])[queueState.at];
  return item && item.gate && !queueDraft(item.gate) ? { task: item.task, gate: item.gate } : null;
}

// screenEnv is what the home and the queue screens read and run through.
function screenEnv() {
  return {
    route: () => readRoute(location.hash),
    get: getJSON,
    post: (url, body) => detailEnv.post(url, body),
    row: screenRow,
    openSource: openTaskSource,
    openTask: (id) => writeHash({ task: id, pr: "" }),
    keyOf: (action) => (typeof cockpitKeys === "object" && cockpitKeys ? cockpitKeys.keyOf(action) : ""),
    act: async (u, id) => {
      if ((await omniboxAct(u, id)) === false) throw new Error(state.actionError || t("{action} failed", { action: id }));
    },
    navigate: (view) => { location.hash = hashString({ view }); },
    render,
    capture: async (title) => {
      try {
        await detailEnv.post("/api/tasks", { title });
        setNotice(t("captured: {title}", { title }));
        scheduleLoad({ tasks: true });
        return true;
      } catch (err) {
        setNotice(t("capture failed: {error}", { error: err.message }), true);
        return false;
      }
    },
  };
}

// renderScreens draws the home or the queue when the route shows one, and hides both otherwise.
function renderScreens(route) {
  const home = document.getElementById("home");
  const queue = document.getElementById("queue");
  home.hidden = route.view !== HOME;
  queue.hidden = route.view !== QUEUE;
  if (!home.hidden) home.replaceChildren(...(homeState.error ? [h("p", { class: "err" }, homeState.error)] : renderHome(homeState.home, screenEnv())));
  if (!queue.hidden) queue.replaceChildren(...renderQueue(queueShown(readRoute(location.hash)), queueState.at, screenEnv()));
}

async function loadActivity() {
  if (readHash().view !== "recent") {
    state.activityError = "";
    return;
  }
  try {
    state.activity = await getJSON("/api/activity?limit=200");
    state.activityLoaded = true;
    state.activityError = "";
  } catch (err) {
    state.activityError = t("Could not load the recent activity: {error}", { error: err.message });
    state.activityLoaded = true;
  }
}

async function loadDetail() {
  const { pr, task } = readHash();
  await detailPane.load({ task, unit: pr });
  if (!pr || task) {
    state.detail = null;
    state.detailError = "";
    return;
  }
  try {
    const d = await getJSON(unitPath(keyUnit(pr)));
    if (readHash().pr !== pr) return;
    state.detail = d;
    state.detailError = "";
    if (d.row.kind === "checks") loadChecks(pr);
  } catch (err) {
    if (readHash().pr !== pr) return;
    state.detail = null;
    const instead = unitFallback(err, detailState.unitTasks);
    state.detailError = instead ? "" : err.message;
    if (instead) location.replace(instead);
  }
}

// loadChecks runs beside the render: naming the failed steps can take a few
// GitHub calls, and the rows must not wait for them.
async function loadChecks(pr) {
  if (state.checksLoading.has(pr)) return;
  state.checksLoading.add(pr);
  try {
    const list = await getJSON(unitPath(keyUnit(pr), "checks"));
    state.checks[pr] = list;
    if (readHash().pr === pr) render();
  } catch {
    // The row's reason already names the checks; the panel goes without links.
  } finally {
    state.checksLoading.delete(pr);
  }
}

async function load() {
  const plugins = getJSON("/api/plugins").then(applyPlugins, () => {});
  const [cat, rows, health] = await Promise.allSettled([getJSON("/api/catalog"), getJSON("/api/rows"), getJSON("/api/health")]);
  await plugins;
  if (cat.status === "fulfilled") applyCatalog(cat.value);
  if (rows.status === "fulfilled") {
    const page = rows.value;
    if (!(page.epoch && page.epoch === state.epoch && page.rev < state.rev)) {
      state.rows = page.rows;
      state.epoch = page.epoch || "";
      state.rev = page.rev || 0;
    }
    state.loaded = true;
  }
  if (health.status === "fulfilled") {
    state.health = health.value;
    checkBuild(state.health);
    followStrings(state.health.strings, getJSON);
  }
  const failed = [cat, rows, health].filter((r) => r.status === "rejected");
  // A daemon that cannot open its store says why in its health's warnings.
  const storeClosed = health.status === "fulfilled" && health.value.store && health.value.store.state !== "ok";
  if (failed.length && !storeClosed) {
    state.error = t("Could not reach the daemon: {error}", { error: [...new Set(failed.map((r) => r.reason.message))].join("; ") });
  } else {
    state.error = "";
    state.authLost = false;
  }
  forgetTasksPage();
  await Promise.all([loadActivity(), loadDetail(), loadExploreRecent(), loadPlan(), loadRoundGates(), loadTasks(), loadHome(), loadQueue()]);
  await refreshSpecs([...state.rows, ...(state.detail ? [state.detail.row] : [])]);
  render();
}

// act resolves to undefined when the user backs out or the input is invalid.
async function act(row, name, extra = {}) {
  const key = keyOf(row);
  const st = (row.actions || []).find((a) => a.id === name);
  const spec = state.specs.byId[name] || {};
  const input = spec.input;
  const guidance = extra.guidance || "";
  const refuse = (why) => { state.actionError = `${name} ${labelOf(row)}: ${why}`; state.actionDetail = ""; state.actionSource = ""; render(); };
  if (input && input.kind === "text" && input.required && !guidance) return refuse(t("{field} is required", { field: input.label || t("Guidance") }));
  if (input && input.max && [...guidance].length > input.max) return refuse(t("guidance is {length} characters, the limit is {max}", { length: [...guidance].length, max: input.max }));
  if (st && st.confirm && !confirm(st.confirm)) return;
  const body = { source: "cockpit" };
  if (st && st.basis) body.basis = st.basis;
  if (input && input.kind === "duration") {
    const hint = [input.placeholder && t("a duration such as {example}", { example: input.placeholder }), input.until_state && t("\"state\" to wait until the situation changes")].filter(Boolean).join(", " + t("or") + " ");
    const answer = prompt(`${input.label || name}? ${hint}`, input.default || "");
    if (answer === null) return;
    if (input.until_state && answer.trim() === "state") body.until_state = true;
    else body.until = answer.trim();
  }
  if (guidance && (spec.intake || (input && (input.kind === "text" || input.kind === "answer")))) body.guidance = guidance;
  if (name === "answer") {
    if (extra.option_id) body.option_id = extra.option_id;
    if (extra.yes) body.yes = true;
    if (extra.no) body.no = true;
  }
  state.busy.add(key);
  state.actionError = "";
  state.actionDetail = "";
  render();
  setNotice(`${name}: ${labelOf(row)}`);
  let ok = false;
  try {
    const res = await fetch(unitPath(unitOf(row), name), {
      method: "POST",
      headers: { "Content-Type": "application/json", Accept: "application/json", "X-Loop-API": API_VERSION },
      body: JSON.stringify(body),
    });
    const out = await res.json().catch(() => ({}));
    if (!res.ok || out.error) throw Object.assign(new Error(apiError(out, `HTTP ${res.status}`, res.status)), { detail: out.detail, source: out.source });
    setNotice(acceptedNote(name, labelOf(row), out.intake) || t("{action} done: {unit}", { action: name, unit: labelOf(row) }) + (out.output && out.output.length ? ". " + out.output.join(" ") : ""));
    ok = true;
    if (name === "answer" && row.question) state.answered.set(key, questionShown(row));
    if (body.guidance) {
      delete state.guidance[key];
      for (const id of ["guidance", "explore-guidance"]) {
        const box = document.getElementById(id);
        if (box && box.dataset.key === key) box.value = "";
      }
    }
  } catch (err) {
    state.actionError = t("{action} failed: {error}", { action: `${name} ${labelOf(row)}`, error: err.message });

    state.actionDetail = err.detail || "";
    state.actionSource = err.source || "";
    setNotice("");
  } finally {
    state.busy.delete(key);
  }
  scheduleLoad({ units: [key] });
  return ok;
}

// mergeRows applies a page of GET /api/rows to state.rows; false when the page
// cannot be merged and the caller must load everything.
function mergeRows(page) {
  if (page.full) {
    state.rows = page.rows;
    state.epoch = page.epoch;
    state.rev = page.rev;
    return true;
  }
  if (!state.loaded || page.epoch !== state.epoch) return false;
  if (page.rev < state.rev) return true;
  const gone = new Set((page.removed || []).map(unitKey));
  const fresh = new Map(page.rows.map((r) => [keyOf(r), r]));
  const kept = state.rows.filter((r) => !gone.has(keyOf(r))).map((r) => {
    const next = fresh.get(keyOf(r));
    fresh.delete(keyOf(r));
    return next || r;
  });
  state.rows = [...kept, ...fresh.values()];
  state.rev = page.rev;
  return true;
}

// applyChange brings the cockpit up to a change: the rows that moved, the
// health only when it did, the detail only when its unit moved.
async function applyChange(c) {
  if (c.all || !state.loaded) return load();
  try {
    if (c.rows || c.units.length) {
      const page = await getJSON(`/api/rows?since=${encodeURIComponent(`${state.epoch}:${state.rev}`)}`);
      if (!mergeRows(page)) return load();
      if (page.catalog && page.catalog !== catalog.version) applyCatalog(await getJSON("/api/catalog"));
    }
    if (c.health) {
      state.health = await getJSON("/api/health");
      checkBuild(state.health);
      followStrings(state.health.strings, getJSON);
    }
  } catch {
    return load();
  }
  state.error = "";
  state.authLost = false;
  const route = readHash();
  const { view, pr } = route;
  await Promise.all([
    c.units.length && view === "recent" ? loadActivity() : null,
    (pr && c.units.some((u) => u.toLowerCase() === pr.toLowerCase())) || (c.tasks && (route.task || pr)) ? loadDetail() : null,
    loadPlan(),
    loadRoundGates(),
    c.tasks ? loadTasks(route, c.units) : null,
    c.tasks || c.all || c.units.length ? loadHome() : null,
    c.tasks || c.all || c.units.length ? loadQueue() : null,
  ]);
  await refreshSpecs([...state.rows, ...(state.detail ? [state.detail.row] : [])]);
  render();
}

let pending;
let queued = null;
let chain = Promise.resolve();
function scheduleLoad(c = { all: true }) {
  queued = queued || { units: [], all: false, rows: false, health: false, tasks: false };
  queued.units = [...new Set([...queued.units, ...(c.units || [])])];
  queued.all = queued.all || !!c.all;
  queued.rows = queued.rows || !!c.rows;
  queued.health = queued.health || !!c.health;
  queued.tasks = queued.tasks || !!c.tasks;
  clearTimeout(pending);
  pending = setTimeout(() => {
    const c = queued;
    queued = null;
    chain = chain.then(() => applyChange(c)).catch(() => {});
  }, 300);
}

function onChange(e) {
  let c;
  try {
    c = JSON.parse(e.data);
  } catch {
    return scheduleLoad();
  }
  scheduleLoad({ units: (c.units || []).map(unitKey), all: !!c.all, health: !!c.health, tasks: !!c.counters });
}

let retries = 0;
let opened = false;
function connect() {
  const es = new EventSource(`/api/events?api=${API_VERSION}`);
  es.onmessage = onChange;
  es.onopen = () => {
    retries = 0;
    state.conn = "open";
    state.authLost = false;
    if (opened) {
      forgetTasksPage();
      scheduleLoad({ rows: true, health: true, tasks: true });
    }
    opened = true;
  };
  es.onerror = () => {
    if (es.readyState !== EventSource.CLOSED) {
      state.conn = "reconnecting";
      render();
      return;
    }
    es.close();
    state.conn = "closed";
    load();
    setTimeout(connect, Math.min(30000, 1000 * 2 ** retries++));
  };
}

let lastView = readHash().view;

window.addEventListener("hashchange", async () => {
    const { view } = readHash();
  const viewChanged = view !== lastView;
  lastView = view;
  searchBox.value = readHash().q;
  document.getElementById("explore-q").value = readHash().x;
  render();
  await Promise.all([viewChanged || !state.activityLoaded ? loadActivity() : null, viewChanged ? loadExploreRecent() : null, loadDetail(), viewChanged ? loadPlan() : null, loadTasks(readHash()), loadHome(), loadQueue()]);
  render();
});

document.addEventListener("focusin", (e) => {
  lastFocus = (e.target.dataset && e.target.dataset.focus) || null;
});

document.addEventListener("focusout", (e) => {
  if (e.relatedTarget) return;
  const leaving = e.target;
  setTimeout(() => {
    if (leaving.isConnected && document.activeElement === document.body) lastFocus = null;
  });
});

const searchBox = document.getElementById("search");
// Embedded browsers may insert a paste as plain text, so a paste is also any
// single input that adds more than one character.
let pasting = false;
searchBox.addEventListener("paste", () => { pasting = true; });
searchBox.addEventListener("input", (e) => {
  const pasted = pasting || e.inputType === "insertFromPaste" || e.inputType === "insertFromDrop" || (e.data || "").length > 1;
  pasting = false;
  onSearch(e.target.value, pasted);
});
searchBox.addEventListener("keydown", (e) => {
  if (e.key === "Escape") {
    e.preventDefault();
    if (searchBox.value) {
      searchBox.value = "";
      writeHash({ q: "" }, true);
    } else searchBox.blur();
  } else if (e.key === "Enter") {
    const rows = visibleRows();
    if (rows.length === 1) writeHash({ pr: keyOf(rows[0]) });
  }
});

document.getElementById("explore-form").addEventListener("submit", (e) => {
  e.preventDefault();
  explore(document.getElementById("explore-q").value);
});
document.getElementById("explore-q").addEventListener("input", (e) => writeHash({ x: e.target.value.slice(0, 256) }, true));
document.getElementById("explore-guidance").addEventListener("input", (e) => { state.guidance[e.target.dataset.key] = e.target.value; });

// panelPage says the page is the floating panel's, which shows the omnibox alone and none of the cockpit.
function panelPage() {
  return readRoute(location.hash).panel === "omnibox";
}

// refreshClock re-renders the cockpit's relative times; the panel has none.
function refreshClock() {
  if (!panelPage()) render();
}

setupDetailResize();
setInterval(refreshClock, 60000);
setInterval(tickCountdowns, 1000);
loadStrings(getJSON).then(() => {
  document.documentElement.lang = I18N.locale;
  translateStatic(document);
  if (panelPage()) {
    showOmnibox({ panel: true });
    panelFollow(window, document, {
      reset: () => showOmnibox({ panel: true }),
      health: () => getJSON("/api/health"),
      strings: (stamp) => followStrings(stamp, getJSON),
      build: checkPanelBuild,
    });
    return;
  }
  tellLinkNotice();
  load();
  connect();
});

// i18nChanged re-renders in the strings that arrived after the boot stopped waiting for them: the cockpit, or
// the panel's omnibox with the text typed in it.
function i18nChanged() {
  document.documentElement.lang = I18N.locale;
  translateStatic(document);
  if (!panelPage()) {
    render();
    return;
  }
  const field = document.querySelector(".omni-input");
  showOmnibox({ panel: true, text: field ? field.value : "" });
}
