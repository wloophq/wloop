"use strict";

const API_VERSION = "1";

// apiError is the daemon's refusal with the command it names to get past it.
// apiError reads a daemon error; a 426 means the daemon is newer than this
// page, which only a reload brings up to date.
function apiError(out, fallback, status) {
  if (status === 426) return "this page is older than {{name}}'s daemon; reload this page";
  const msg = out.error || fallback;
  return out.fix ? `${msg}; run: ${out.fix}` : msg;
}

const EXPLORE_FILTERS = [
  { term: "-reviewed-by:@me", label: "Not reviewed by me" },
  { term: "draft:false", label: "Ready for review" },
  { term: "review-requested:@me", label: "Requested from me or my teams" },
  { window: 30, label: "Active in the last 30 days" },
];
const EXPLORE_PRESETS = [
  { query: "user-review-requested:@me", label: "Asked of me" },
  { query: "review-requested:@me -user-review-requested:@me", label: "Asked of my teams" },
];
const DEFAULT_SORT = "updated-asc";
const EXPLORE_SORTS = [
  { sort: DEFAULT_SORT, label: "Waiting longest", shown: "waiting longest" },
  { sort: "created-asc", label: "Oldest opened", shown: "oldest opened" },
  { sort: "updated-desc", label: "Recently updated", shown: "most recently updated" },
];
const REVIEW_LABEL = { APPROVED: "approved", CHANGES_REQUESTED: "changes requested", REVIEW_REQUIRED: "review required" };

const SIZES = ["xs", "s", "m", "l", "xl"];
const RISKS = ["low", "medium", "high"];
const STRIP_LEGEND = [["me", "you"], ["author", "author"], ["reviewer", "reviewer"], ["loop", "loop"], ["nobody", "nobody"]];
const STRIP_TITLE = { me: "Waits on you", author: "Waits on the author", reviewer: "Waits on a reviewer", loop: "Waits on the loop", nobody: "Waits on nobody" };
const TAG_CHIPS = 3;

// catalog is the daemon's served presentation (GET /api/catalog): its views
// for the web, the lens's fields built from its facets, and the situations'
// labels in rank order.
const catalog = { version: "", views: [], fields: [], kinds: new Map(), kindOrder: [] };

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
  catalog.fields = (c.facets || []).map(fieldOf);
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
  const f = { id: spec.id, label: spec.label, provider: spec.provider || "", group: !!spec.group, sort: !!spec.sort && spec.menu !== false,
    menu: spec.menu !== false, rank: spec.rank, empty: spec.empty, match: MATCH[spec.match], buckets: spec.buckets || null };
  if (spec.labels) f.show = (k) => spec.labels[k] || k;
  if (spec.match === "prefix") f.groupBy = (raw) => "tag:" + raw.split(":")[0];
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
  const t = Date.parse(facetValues(r, id)[0] || "");
  return Number.isNaN(t) ? -Infinity : t;
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
// without within catching the rest; "" when no bucket takes t.
function bucketOf(buckets, t, now = Date.now()) {
  const at = Date.parse(t);
  if (Number.isNaN(at)) return "";
  const delta = now - at;
  const b = buckets.find((x) => !x.within || delta < goDuration(x.within));
  return b ? b.id : "";
}

// dueNow is the flag "due": an upcoming instant of the row's facet due falls
// in its "due" bucket now.
function dueNow(r, now = Date.now()) {
  const f = fieldById("due");
  return !!f && !!f.buckets && facetValues(r, "due").some((t) => bucketOf(f.buckets, t, now) === "due");
}

// pl is a row's provider payload, empty for a row that has none.
function pl(r) {
  return r.payload || {};
}

function sm(r) {
  return r.summary || {};
}

const EVENT_LABEL = {
  opened: "opened", push: "pushed", review_requested: "review requested", review_request_removed: "review request removed",
  review: "reviewed", comment: "commented", check: "checks", ready: "ready for review", draft: "back to draft",
  merged: "merged", closed: "closed",
  round_pending: "round pending", round_sent: "round sent", round_skipped: "round skipped", round_held: "round held",
  round_blocked: "round blocked", round_unblocked: "round unblocked",
  held: "paused", released: "resumed", muted: "muted", unmuted: "unmuted", adopted: "adopted", status: "board",
  removed: "workspace removed", nudged: "nudged", tracked: "tracked", untracked: "untracked", answered: "answered", guidance: "guidance",
  turn_sent: "turn sent", turn_started: "turn started", turn_ended: "turn ended", turn_resumed: "turn resumed",
  turn_withdrawn: "turn withdrawn", question_dismissed: "question dismissed",
  turn_delivered: "turn delivered", answer_routed: "answer routed", late_record: "late record",
  stop_blocked: "stop blocked", turn_stopped: "turn stopped", guide_trimmed: "guide trimmed", dossier_restored: "dossier file restored", dossier_granted: "dossier granted to the session", dossier_call: "dossier compaction, US$", brief_waiting: "waiting for the brief", intake_accepted: "request accepted", intake_briefing: "request waits for the brief", intake_starting: "request starting", intake_started: "request started", intake_failed: "request failed", intake_canceled: "request canceled", intake_guidance: "request guidance replaced",
};
const LOOP_EVENTS = new Set(["round_pending", "round_sent", "round_skipped", "round_held", "round_blocked", "round_unblocked", "held", "released", "muted", "unmuted", "adopted", "status", "removed", "nudged", "tracked", "untracked", "answered", "guidance", "turn_sent", "turn_started", "turn_ended", "turn_resumed", "turn_withdrawn", "question_dismissed", "turn_delivered", "answer_routed", "late_record", "stop_blocked", "turn_stopped", "guide_trimmed", "dossier_restored", "dossier_granted", "dossier_call", "brief_waiting", "intake_accepted", "intake_briefing", "intake_starting", "intake_started", "intake_failed", "intake_canceled", "intake_guidance"]);
const LOOP_ONLY_KEY = "loop.activity.loopOnly";
const DETAIL_WIDTH_KEY = "loop.detailWidth";
const DETAIL_WIDTH_DEFAULT = 380;
const DETAIL_WIDTH_MIN = 300;
const DETAIL_WIDTH_STEP = 16;

const UNIT_KEY = /^[a-z][a-z0-9-]*:\S{1,300}$/;
const DISCONNECTED = "disconnected: run `{{command}} open`";
const QUERY_MAX = 200;
const REQUEST_TIMEOUT_MS = 15000;
const ARTIFACT_LINES = 40;
const PR_URL = /^https:\/\/github\.com\/([A-Za-z0-9-]+)\/([\w.-]+)\/pull\/(\d+)(?:[/?#]\S*)?$/;
const folded = new WeakMap();

const state = { specs: { version: "", byId: {} }, planSpecs: {}, plan: { items: [], closed: [], error: "", loaded: false }, collapsed: new Set(), collapsedFor: "", rows: [], epoch: "", rev: 0, activity: [], health: null, detail: null, checks: {}, checksLoading: new Set(), busy: new Set(), guidance: {}, conn: "connecting", authLost: false,
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
  for (const c of children.flat()) {
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
  const view = gtdResolveView(route.view, gtdState.catalog, catalog.views, (id) => pluginViews.has(id));
  const g = p.get("g") || "";
  return {
    view,
    pr: UNIT_KEY.test(route.unit) ? route.unit : "",
    entry: route.entry,
    area: route.area,
    ctx: route.ctx,
    layout: p.get("layout") === "board" ? "board" : "",
    q: (p.get("q") || "").slice(0, QUERY_MAX),
    g: (groupField(g) || {}).group ? g : "",
    s: parseSort(p.get("s")).field ? p.get("s") : "",
    x: view === "explore" ? (p.get("x") || "").slice(0, 256) : "",
    tk: view === "dash" && /^[a-z-]+$/.test(p.get("tk") || "") ? p.get("tk") : "",
    done: view === "dash" && p.get("done") === "1",
  };
}

function hashString(next) {
  const p = new URLSearchParams();
  if (next.entry) p.set("entry", next.entry);
  if (next.area) p.set("area", next.area);
  if (next.ctx && next.ctx.length) p.set("ctx", [].concat(next.ctx).join(","));
  if (next.layout) p.set("layout", next.layout);
  if (next.q) p.set("q", next.q);
  if (next.g) p.set("g", next.g);
  if (next.s) p.set("s", next.s);
  if (next.x && next.view === "explore") p.set("x", next.x);
  if (next.tk && next.view === "dash") p.set("tk", next.tk);
  if (next.done && next.view === "dash") p.set("done", "1");
  return routeHash(next.view, next.pr, p);
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
const keyUnit = (k) => ({ provider: k.slice(0, k.indexOf(":")), key: k.slice(k.indexOf(":") + 1) });
const unitPath = (u, ...rest) => "/api/units/" + [u.provider, u.key, ...rest].map(encodeURIComponent).join("/");
const unitOf = (x) => x.unit;
const keyOf = (x) => unitKey(unitOf(x));
const labelOf = (x) => x.label || refLabel(x.ref);
const labelTail = (r) => (pl(r).repo && labelOf(r).startsWith(pl(r).repo) ? labelOf(r).slice(pl(r).repo.length) : labelOf(r));
const refLabel = (ref) => `${ref.owner}/${ref.repo}#${ref.number}`;

function fold(s) {
  return String(s || "").normalize("NFD").replace(/\p{M}/gu, "").toLowerCase();
}

// tokens splits a query on spaces, keeping a "quoted value" whole.
function tokens(q) {
  return String(q || "").match(/[^\s"]*"[^"]*"?|[^\s"]+/g) || [];
}

function unquote(s) {
  return s.replace(/^"|"$/g, "");
}

// terms are the free words of a query, the text every field is searched for.
function terms(q) {
  return tokens(q).filter((t) => !qualifierOf(t)).map((t) => fold(unquote(t))).filter(Boolean);
}

function fieldById(id) {
  return catalog.fields.find((f) => f.id === id);
}

// tagKeyField groups by one brief tag key, "tag:lang": a row sits under each
// of its tags of that key, or under "no lang" with none. Its group keys are
// whole tags, so no value, "none" included, reads as the empty group.
function tagKeyField(id) {
  const m = /^tag:([a-z-]+)$/.exec(id || "");
  if (!m) return null;
  const key = m[1];
  return {
    id, label: `Tag ${key}`, empty: `no ${key}`, group: true, tagKey: key,
    keys: (r) => facetValues(r, "tag").filter((t) => t.startsWith(key + ":")),
  };
}

function groupField(id) {
  return fieldById(id) || tagKeyField(id);
}

function tagKeys(rows) {
  const keys = new Set();
  for (const r of rows) for (const t of facetValues(r, "tag")) keys.add(t.split(":")[0]);
  return [...keys].filter((k) => /^[a-z-]+$/.test(k)).sort();
}

function normValue(field, raw) {
  const v = fold(unquote(String(raw || "")));
  return (field === "author" || field === "waits-on") && v === "@me" ? "me" : v;
}

// qualifierOf reads a field:value term of a field that filters; anything else
// is free text, so "fix:" or a URL stays a word.
function qualifierOf(token) {
  const m = /^(-?)([a-z][a-z-]*):(.+)$/i.exec(token);
  const f = m && fieldById(m[2].toLowerCase());
  if (!f || !f.values) return null;
  const value = normValue(f.id, m[3]);
  return value ? { field: f.id, value, raw: unquote(m[3]), neg: m[1] === "-" } : null;
}

function qualifiers(q) {
  return tokens(q).map(qualifierOf).filter(Boolean);
}

function qualifierToken(field, raw, neg) {
  return `${neg ? "-" : ""}${field}:${/\s/.test(raw) ? `"${raw}"` : raw}`;
}

// toggledQualifier adds field:value, or -field:value when neg, or takes it
// out when the query holds it already; the opposite sign gives way.
function toggledQualifier(q, field, raw, neg) {
  const v = normValue(field, raw);
  let on = false;
  const kept = tokens(q).filter((t) => {
    const x = qualifierOf(t);
    if (!x || x.field !== field || x.value !== v) return true;
    if (x.neg === !!neg) on = true;
    return false;
  });
  return (on ? kept : [...kept, qualifierToken(field, raw, neg)]).join(" ");
}

function withoutQualifiers(q, field, neg) {
  return tokens(q).filter((t) => {
    const x = qualifierOf(t);
    return !x || x.field !== field || x.neg !== neg;
  }).join(" ");
}

function haystack(r) {
  let f = folded.get(r);
  if (!f) {
    const p = pl(r);
    const ws = p.workspace || {};
    f = [r.title, labelOf(r), p.repo, p.author, p.head, sm(r).waits_on, r.kind, rowKindLabel(r), sm(r).reason, sm(r).line,
      ws.display_name, ws.status, ws.comment, (p.brief || {}).goal, ...((p.brief || {}).tags || [])].map(fold);
    folded.set(r, f);
  }
  return f;
}

function matches(r, ts) {
  return ts.every((t) => haystack(r).some((f) => f.includes(t)));
}

function matchesActivity(it, ts) {
  const e = it.event;
  const fields = [it.title, refLabel(e.pr), it.author, e.actor, EVENT_LABEL[e.kind] || e.kind, e.data].map(fold);
  return ts.every((t) => fields.some((f) => f.includes(t)));
}

// Recent applies the filters an event can answer: its repository and the
// author of its pull request.
const ACTIVITY_FIELDS = ["repo", "author"];

function activityPasses(it, lens) {
  const filters = new Map([...lens.filters].filter(([id]) => ACTIVITY_FIELDS.includes(id)));
  return matchesActivity(it, lens.ts) && passesFilters({ facets: { repo: [`${it.event.pr.owner}/${it.event.pr.repo}`], author: [it.author || ""] } }, filters);
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
  for (const t of ts) {
    for (let i = out.indexOf(t); i >= 0; i = out.indexOf(t, i + 1)) hits.push([at[i], at[i + t.length]]);
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

function isSet(t) {
  return t && !t.startsWith("0001-01-01");
}

function age(since, now = Date.now()) {
  if (!isSet(since)) return "";
  const mins = Math.max(0, Math.floor((now - Date.parse(since)) / 60000));
  if (mins < 1) return "now";
  if (mins < 60) return `${mins} min`;
  const hours = Math.floor(mins / 60);
  if (hours < 24) return `${hours}h ${mins % 60}m`;
  return `${Math.floor(hours / 24)}d ${hours % 24}h`;
}

function left(deadline, now = Date.now()) {
  const s = Math.ceil((Date.parse(deadline) - now) / 1000);
  if (s <= 0) return "sending";
  if (s <= 99) return `in ${s} s`;
  return `in ${Math.floor(s / 60)}m ${String(s % 60).padStart(2, "0")}s`;
}

function countdown(row) {
  return h("span", { class: "age countdown", "data-deadline": row.pending.deadline, title: `sends at ${stamp(row.pending.deadline)}` }, left(row.pending.deadline));
}

function tickCountdowns() {
  for (const el of document.querySelectorAll(".countdown[data-deadline]")) el.textContent = left(el.dataset.deadline);
  for (const el of document.querySelectorAll(".plan-left[data-deadline]")) el.textContent = planWhen({ decision: "pending", default: "run", deadline: el.dataset.deadline });
}

function stamp(t) {
  if (!isSet(t)) return "";
  const d = new Date(t);
  return d.toLocaleString(undefined, { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" });
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

// waitCat is the class of whom a row waits on its strip shows: me, the
// loop, a reviewer, the author, or nobody.
function waitCat(row) {
  return facetValues(row, "wait")[0] || "nobody";
}

function waitClass(row) {
  return "w-" + waitCat(row);
}

function isMine(author) {
  const viewer = state.health && state.health.viewer;
  return !!viewer && (author || "").toLowerCase() === viewer.toLowerCase();
}

// stripTitle names whom the row's strip says it waits on, and why.
function stripTitle(row) {
  const cat = waitCat(row);
  const who = cat === "author" || cat === "reviewer" ? ` (${waitingLabel(row)})` : "";
  return [`${STRIP_TITLE[cat] || cat}${who}: ${rowKindLabel(row)}`, sm(row).reason].filter(Boolean).join(". ");
}

function isMuted(o) {
  return o.mute_until_state_change || (isSet(o.muted_until) && Date.parse(o.muted_until) > Date.now());
}

// The backlog is every pull request still open; done joins it on request.
function inView(row, view, withDone) {
  if (view === "recent" || view === "explore" || view === "plan") return false;
  if (view === "dash") return (row.unit || {}).provider === (catalog.views.find((v) => v.id === view) || {}).provider && (withDone || !sm(row).closed);
  return view === "all" || (row.views || []).includes(view);
}

// isStalled is the daemon's stalled view: past its threshold and in scope.
function isStalled(r) {
  return (r.views || []).includes("stalled");
}

function rowFlags(r) {
  return fieldById("is") ? fieldById("is").values(r) : [];
}

function sinceTime(r) {
  const t = Date.parse(sm(r).since);
  return Number.isNaN(t) ? -Infinity : t;
}

// upcoming are a row's next instants: the daemon's list, or else the
// deadline of its pending round.
function upcoming(r) {
  const next = ((pl(r).facts || {}).next || []).filter((e) => isSet(e.at));
  if (r.pending && isSet(r.pending.deadline) && !next.some((e) => e.what === "gate")) next.push({ at: r.pending.deadline, what: "gate" });
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

// rowValuesCache holds the values of the current minute: a time facet's
// buckets move with the clock.
const rowValuesCache = { minute: -1, rows: new WeakMap() };

// rowValues are a row's values of a field, folded, the way filters compare.
function rowValues(f, r) {
  const minute = Math.floor(Date.now() / 60000);
  if (rowValuesCache.minute !== minute) Object.assign(rowValuesCache, { minute, rows: new WeakMap() });
  const cache = rowValuesCache.rows;
  let m = cache.get(r);
  if (!m) cache.set(r, (m = new Map()));
  if (!m.has(f.id)) m.set(f.id, (f.values ? f.values(r) : []).filter(Boolean).map((v) => fold(v)));
  return m.get(f.id);
}

function rowKey(f, r) {
  return (f.key ? f.key(r) : (f.values(r) || [])[0]) || "none";
}

function filtersOf(quals) {
  const out = new Map();
  for (const x of quals) {
    if (!out.has(x.field)) out.set(x.field, { inc: [], exc: [] });
    out.get(x.field)[x.neg ? "exc" : "inc"].push(x.value);
  }
  return out;
}

// passesFilters ORs the values of one field and ANDs the fields; except
// leaves one field out, to count what picking another of its values shows.
function passesFilters(r, filters, except) {
  for (const [id, { inc, exc }] of filters) {
    if (id === except || (Array.isArray(except) && except.includes(id))) continue;
    const f = fieldById(id);
    const vs = rowValues(f, r);
    const hit = (q) => vs.some((v) => (f.match ? f.match(v, q) : v === q));
    if ((inc.length && !inc.some(hit)) || exc.some(hit)) return false;
  }
  return true;
}

function passes(r, view, lens, except) {
  return inView(r, view, lens.done) && matches(r, lens.ts) && passesFilters(r, lens.filters, except);
}

function parseSort(s) {
  const m = /^([a-z]+)(-desc)?$/.exec(s || "");
  const f = m && fieldById(m[1]);
  return f && f.sort ? { field: f.id, desc: !!m[2] } : { field: "", desc: false };
}

function readLens() {
  const { view, q, g, s, layout, tk, done } = readHash();
  const quals = qualifiers(q);
  return { view, q, ts: terms(q), quals, filters: filtersOf(quals), group: g, sort: s, layout, tk, done };
}

// A board needs groups: the Stalled board keeps its lanes, the others group
// by whom a row waits on.
function effectiveGroup(lens) {
  if (lens.group) return lens.group;
  if (lens.layout !== "board") return "";
  return lens.view === "stalled" ? "lane" : "wait";
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

function compareRows(f, desc) {
  return (a, b) => {
    const c = f.sortValue ? (desc ? cmp(f.sortValue(b), f.sortValue(a)) : cmp(f.sortValue(a), f.sortValue(b))) : keyOrder(f, rowKey(f, a), rowKey(f, b), desc);
    return c || byDefault(a, b);
  };
}

function groupLabel(f, raw) {
  return !raw || raw === "none" ? f.empty || "none" : f.show ? f.show(raw) : raw;
}

function groupRows(f, rows) {
  const buckets = new Map();
  const add = (raw) => {
    const k = fold(raw);
    if (!buckets.has(k)) buckets.set(k, { field: f.id, key: k, raw, label: groupLabel(f, raw), rows: [] });
    return buckets.get(k);
  };
  for (const r of rows) {
    const ks = f.keys ? f.keys(r) : [rowKey(f, r)];
    for (const k of ks.length ? ks : ["none"]) add(k).rows.push(r);
  }
  if (f.id === "lane") for (const id of f.rank || []) add(id);
  return [...buckets.values()].sort((a, b) => keyOrder(f, a.key, b.key, false));
}

// pipeline is every list view's one path: filter, then sort, then group,
// rows keeping the sort inside their group.
function pipeline(rows, lens) {
  const all = rows.filter((r) => inView(r, lens.view, lens.done));
  const shown = all.filter((r) => matches(r, lens.ts) && passesFilters(r, lens.filters));
  const { field, desc } = parseSort(lens.sort);
  shown.sort(field ? compareRows(fieldById(field), desc) : byDefault);
  const g = groupField(effectiveGroup(lens));
  const groups = g ? groupRows(g, shown) : [{ field: "", key: "", raw: "", label: "", rows: shown }];
  return { groups, group: g ? g.id : "", total: all.length, shown: shown.length };
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
    if (hl.slow_sync) items.push(`Slow sync: GitHub budget low until ${stamp(hl.rate_limit.reset_at)}`);
    for (const w of hl.warnings || []) items.push(w);
    for (const j of hl.jobs || []) if (j.last_error) items.push(`${j.name}: ${j.last_error}`);
  }
  if (state.detailError) items.unshift(state.detailError);
  if (state.activityError) items.unshift(state.activityError);
  if (state.error) items.unshift(state.error);
  if (state.actionError) items.unshift({ text: state.actionError, detail: state.actionDetail, source: state.actionSource });
  if (state.authLost || state.conn === "closed") items.unshift(DISCONNECTED);
  const text = items.map((t) => (typeof t === "string" ? t : `${t.text}\n${t.detail || ""}`)).join("\n");
  if (box.dataset.text !== text) {
    box.replaceChildren(...items.map((t) => typeof t === "string" ? h("div", {}, t)
      : h("div", {}, t.text, t.detail ? h("details", {}, h("summary", {}, t.source ? ((state.health && state.health.workspace_manager) || t.source) + "'s message" : "Details"), t.detail) : null)));
    box.dataset.text = text;
  }
  box.hidden = items.length === 0;
}

function renderSync() {
  const el = document.getElementById("sync");
  const hl = state.health;
  if (state.conn !== "open" || !hl) {
    el.textContent = state.conn === "closed" ? "disconnected" : state.conn;
    el.classList.remove("slow", "paused");
    return;
  }
  const ingest = (hl.jobs || []).filter((j) => j.name.startsWith("ingest:"));
  const sync = ingest.find((j) => j.running) || ingest.find((j) => !isSet(j.last_ok)) || ingest.sort((a, b) => Date.parse(a.last_ok) - Date.parse(b.last_ok))[0];
  const parts = [];
  if (hl.paused) parts.push("automatic rounds paused");
  if (sync) parts.push(sync.running ? "syncing" : isSet(sync.last_ok) ? `synced ${age(sync.last_ok) === "now" ? "just now" : age(sync.last_ok) + " ago"}` : "not synced yet");
  const rl = hl.rate_limit;
  if (rl && rl.limit) parts.push(`GitHub budget ${rl.remaining} of ${rl.limit} left`);
  if (hl.version) parts.push(hl.version);
  el.textContent = parts.join(" · ");
  el.classList.toggle("slow", !!hl.slow_sync);
  el.classList.toggle("paused", !!hl.paused);
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
  b.textContent = hl && hl.paused ? "Resume automatic rounds" : "Pause automatic rounds";
}

function yamlString(v) {
  return JSON.stringify(String(v ?? ""));
}

// listYAML writes the rows a view shows as YAML whose scalars are JSON
// strings, so an agent reads it without quoting surprises.
function listYAML(meta, rows) {
  const out = [`view: ${yamlString(meta.view)}`];
  if (meta.filter) out.push(`filter: ${yamlString(meta.filter)}`);
  if (meta.group_by) out.push(`group_by: ${yamlString(meta.group_by)}`);
  if (meta.sort) out.push(`sort: ${yamlString(meta.sort)}`);
  out.push(`copied_at: ${yamlString(meta.at)}`, `count: ${rows.length}`, "pull_requests:");
  for (const r of rows) {
    const p = pl(r);
    const o = p.override || {};
    const flags = [o.hold && "paused", isMuted(o) && "muted", o.adopted && "adopted", o.tracked && "tracked"].filter(Boolean);
    const b = p.brief;
    const ws = p.workspace;
    const fields = [
      ["ref", yamlString(labelOf(r))],
      ["url", yamlString(r.link)],
      ["title", yamlString(r.title)],
      ["author", p.author ? yamlString(p.author) : ""],
      ["situation", yamlString(rowKindLabel(r))],
      ["waiting_on", yamlString(waitingLabel(r))],
      ["since", isSet(sm(r).since) ? yamlString(sm(r).since) : ""],
      ["stalled", sm(r).stalled ? "true" : ""],
      ["reason", sm(r).reason ? yamlString(sm(r).reason) : ""],
      ["head", p.head ? yamlString(p.head) : ""],
      ["workspace", ws ? yamlString([ws.display_name, ws.status ? `(${ws.status})` : ""].filter(Boolean).join(" ")) : ""],
      ["in_scope_as", (p.reasons || []).length ? `[${p.reasons.map(yamlString).join(", ")}]` : ""],
      ["brief", b ? `{size: ${yamlString(b.size)}, risk: ${yamlString(b.risk)}, open_questions: ${Number(b.open_questions) || 0}, goal: ${yamlString(b.goal)}}` : ""],
      ["tags", b && (b.tags || []).length ? `[${b.tags.map(yamlString).join(", ")}]` : ""],
      ["flags", flags.length ? `[${flags.map(yamlString).join(", ")}]` : ""],
    ].filter(([, v]) => v);
    fields.forEach(([k, v], i) => out.push(`${i ? "    " : "  - "}${k}: ${v}`));
  }
  return out.join("\n") + "\n";
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
      ["ref", yamlString(refLabel(c.ref))],
      ["url", yamlString(candidateURL(c))],
      ["title", yamlString(c.title)],
      ["author", yamlString(c.author)],
      ["draft", c.draft ? "true" : ""],
      ["size", yamlString(`+${c.additions} -${c.deletions}, ${c.changed_files} file${c.changed_files === 1 ? "" : "s"}`)],
      ["review", yamlString(REVIEW_LABEL[c.review_decision] || "no review required")],
      ["checks", yamlString(c.checks ? c.checks.toLowerCase() : "none")],
      ["asks", asks.length ? `[${asks.map(yamlString).join(", ")}]` : ""],
      ["my_review", c.my_review ? yamlString(c.my_review.toLowerCase()) : ""],
      ["created_at", yamlString(c.created_at)],
      ["updated_at", yamlString(c.updated_at)],
      ["in_loop", row ? yamlString(`${row.kind}, waiting on ${sm(row).waits_on}`) : "false"],
      ["brief", b ? `{size: ${yamlString(b.size)}, risk: ${yamlString(b.risk)}, open_questions: ${(b.open_questions || []).length}, goal: ${yamlString(b.goal)}}` : ""],
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
    setNotice(`Copied ${cands.length} candidate${cands.length === 1 ? "" : "s"} as YAML.`);
  } catch (err) {
    setNotice(`Could not copy: ${err.message}`, true);
  }
}

function sortLabel(sort) {
  const { field, desc } = parseSort(sort);
  return field ? `${fieldById(field).label.toLowerCase()} ${desc ? "descending" : "ascending"}` : "";
}

async function copyList(view) {
  const lens = readLens();
  const res = pipeline(state.rows, lens);
  const rows = groupedRows(res.groups);
  const grouped = res.group ? groupField(res.group).label.toLowerCase() : "";
  try {
    await navigator.clipboard.writeText(listYAML({ view, filter: lens.q, group_by: grouped, sort: sortLabel(lens.sort), at: new Date().toISOString() }, rows));
    setNotice(`Copied ${rows.length} pull request${rows.length === 1 ? "" : "s"} from ${view}${grouped ? `, grouped by ${grouped}` : ""}, as YAML.`);
  } catch (err) {
    setNotice(`Could not copy: ${err.message}`, true);
  }
}

function chip(attrs, label) {
  return h("button", { type: "button", class: "chip", ...attrs }, label);
}

function setGroup(g) {
  const lens = { ...readLens(), group: g };
  const res = pipeline(state.rows, lens);
  writeHash({ g });
  if (res.group) setNotice(`Grouped by ${groupField(res.group).label.toLowerCase()}: ${res.groups.filter((x) => x.rows.length).length} groups`);
}

function clearLens() {
  lastFocus = "top:search";
  writeHash({ q: "", g: "", s: "" });
}

// lensChips are one chip per filtered field and sign, which takes it out.
function lensChips(lens, applied) {
  const by = new Map();
  for (const x of lens.quals) {
    const k = `${x.field}:${x.neg ? "-" : ""}`;
    if (!by.has(k)) by.set(k, { field: x.field, neg: x.neg, values: [] });
    by.get(k).values.push(x.field === "kind" ? kindLabel(x.value) : x.raw);
  }
  const list = [...by.values()];
  return list.map((c, i) => {
    const f = fieldById(c.field);
    const off = applied && !applied.includes(c.field);
    const next = list[i + 1] || list[i - 1];
    return chip({
      class: off ? "chip on off" : "chip on", "data-focus": `lens:chip:${c.field}:${c.neg ? "-" : ""}`,
      title: off ? "Not applied in Recent" : `Remove this filter (${c.values.map((v) => qualifierToken(c.field, v, c.neg)).join(" ")})`,
      onclick: () => {
        lastFocus = next ? `lens:chip:${next.field}:${next.neg ? "-" : ""}` : "lens:group";
        writeHash({ q: withoutQualifiers(lens.q, c.field, c.neg) });
      },
    }, `${f.label}: ${c.neg ? "not " : ""}${c.values.join(" or ")} ×`);
  });
}

function stripLegend() {
  return h("span", { class: "legend", title: "The strip on the left of each pull request is the color of whom it waits on" },
    h("span", { class: "lens-label" }, "Waits on:"),
    STRIP_LEGEND.map(([cat, label]) => h("span", { class: "lg w-" + cat }, h("span", { class: "sw", "aria-hidden": "true" }), label)));
}

function lensSelect(id, label, value, options, onchange) {
  return h("label", { class: "lens-pick" }, h("span", { class: "lens-label" }, label),
    h("select", { "data-focus": `lens:${id}`, onchange: (e) => onchange(e.target.value) },
      options.map(([v, text]) => h("option", { value: v, selected: v === value }, text))));
}

// renderLens draws the bar of filters, grouping and order. It rebuilds only
// when what it shows changed, so an open select survives live updates.
function renderLens(lens, res) {
  const tools = document.getElementById("tools");
  const recent = lens.view === "recent";
  tools.hidden = lens.view === "explore" || (recent && !lens.quals.length);
  if (tools.hidden) {
    tools.replaceChildren();
    tools.dataset.sig = "";
    return;
  }
  const removable = lens.view === "done" || lens.view === "approved";
  const n = removable ? state.rows.filter((r) => inView(r, lens.view)).length : 0;
  const keys = tagKeys(state.rows);
  const current = tagKeyField(lens.group);
  if (current && !keys.includes(current.tagKey)) keys.push(current.tagKey);
  const sig = JSON.stringify([lens.view, lens.q, lens.group, lens.sort, lens.layout, res && res.shown, res && res.total, n, !!state.removingDone, state.health && state.health.viewer, keys]);
  if (tools.dataset.sig === sig) return;
  tools.dataset.sig = sig;
  const filtered = !!lens.q.trim();
  const clear = h("button", { type: "button", class: "act", "data-focus": "lens:clear", onclick: clearLens }, "Clear");
  if (recent) {
    tools.replaceChildren(...lensChips(lens, ACTIVITY_FIELDS), clear);
    return;
  }
  const board = lens.layout === "board";
  const { field, desc } = parseSort(lens.sort);
  const toggle = (layout, label) => h("button", {
    type: "button", class: "act", "aria-pressed": String(board === (layout === "board")), "data-focus": `lens:${layout || "list"}`,
    onclick: () => writeHash({ layout }),
  }, label);
  const items = [
    lensSelect("group", "Group", lens.group, [["", board ? (lens.view === "stalled" ? "Board lanes" : "Waiting on (board)") : "None"],
      ...catalog.fields.filter((f) => f.group && f.menu).map((f) => [f.id, f.label]),
      ...keys.map((k) => [`tag:${k}`, `Tag ${k}`])], setGroup),
    lensSelect("sort", "Sort", field, [["", "Waiting longest"], ...catalog.fields.filter((f) => f.sort).map((f) => [f.id, f.label])],
      (v) => writeHash({ s: v })),
    h("button", {
      type: "button", class: "act", "data-focus": "lens:dir", disabled: !field, "aria-label": desc ? "Descending" : "Ascending",
      title: field ? `Sorted ${desc ? "descending" : "ascending"}; click to flip` : "Pick a sort to choose its direction",
      onclick: () => writeHash({ s: field + (desc ? "" : "-desc") }),
    }, desc ? "↓" : "↑"),
    h("div", { class: "toggle", role: "group", "aria-label": "Layout" }, toggle("", "List"), toggle("board", "Board")),
    stripLegend(),
    ...lensChips(lens),
    lens.group ? chip({ class: "chip on", "data-focus": "lens:chip:group", title: "Stop grouping", onclick: () => { lastFocus = "lens:group"; writeHash({ g: "" }); } },
      `Grouped by ${groupField(lens.group).label.toLowerCase()} ×`) : null,
    field ? chip({ class: "chip on", "data-focus": "lens:chip:sort", title: "Back to waiting longest", onclick: () => { lastFocus = "lens:sort"; writeHash({ s: "" }); } },
      `Sorted by ${fieldById(field).label.toLowerCase()} ${desc ? "↓" : "↑"} ×`) : null,
    h("span", { class: "lens-count" }, res ? `${res.shown} of ${res.total}` : ""),
    filtered || lens.group || field ? clear : null,
    h("button", {
      type: "button", class: "act", "data-focus": "lens:copy-list", title: "Copy the pull requests shown here, in this order, as YAML, to paste for an agent",
      onclick: () => copyList(lens.view),
    }, "Copy list"),
    removable ? h("button", {
      type: "button", class: "act", "data-focus": "lens:remove-done", disabled: n === 0 || state.removingDone || filtered,
      title: filtered ? "Clear the filters first: this acts on the whole view" : undefined,
      onclick: () => removeAllClean(n, lens.view),
    }, "Remove all clean") : null,
  ];
  tools.replaceChildren(...items.filter(Boolean));
}

async function removeAllClean(n, view) {
  const what = view === "approved" ? "approved pull request" : "finished pull request";
  if (!confirm(`Remove the workspaces of ${n} ${what}${n === 1 ? "" : "s"}? Their checkouts are deleted and their terminals are closed. A workspace whose agent works or waits, or that holds uncommitted files, unpushed commits or ignored files outside build and dependency folders, is kept.`)) return;
  state.removingDone = true;
  state.actionError = "";
  state.actionDetail = "";
  render();
  try {
    const res = await fetch("/api/workspaces/remove-done", {
      method: "POST",
      headers: { "Content-Type": "application/json", Accept: "application/json", "X-Loop-API": API_VERSION },
      body: JSON.stringify({ view }),
    });
    const out = await res.json().catch(() => ({}));
    if (!res.ok || out.error) throw new Error(apiError(out, `HTTP ${res.status}`, res.status));
    const removed = (out.removed || []).map(refLabel);
    const kept = (out.kept || []).map((k) => `${refLabel(k.ref)} (${k.reason})`);
    const details = (out.kept || []).filter((k) => k.detail).map((k) => `${refLabel(k.ref)}: ${k.detail}`);
    const text = [
      `Removed ${removed.length}${removed.length ? ": " + removed.join(", ") : ""}.`,
      kept.length ? `Kept ${kept.length}: ${kept.join("; ")}.` : "",
    ].filter(Boolean).join(" ");
    setNotice(text);
    document.getElementById("notice").title = text;
    if (details.length) {
      state.actionError = `Kept ${details.length} with details`;
      state.actionSource = "";
      state.actionDetail = details.join("\n");
    }
  } catch (err) {
    state.actionError = `remove all clean failed: ${err.message}`;
  }
  state.removingDone = false;
  scheduleLoad();
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
    setNotice(paused ? "Automatic rounds paused" : "Automatic rounds resumed");
  } catch (err) {
    state.actionError = `${paused ? "pause" : "resume"} failed: ${err.message}`;
  }
  state.pausing = false;
  scheduleLoad({ health: true });
}

function railItem(label, count, active, hash, cls) {
  return h("li", {},
    h("a", { href: "#" + hash, class: (active ? "on " : "") + (cls || ""), "aria-current": active ? "page" : undefined, "data-focus": "rail:" + hash },
      h("span", { class: "label", title: label }, label),
      h("span", { class: "count" }, count)));
}

function lensHash(change) {
  const { q, g, s: sort, layout, view, area } = readHash();
  return hashString({ view, q, g, s: sort, layout, area, ...change });
}

function renderRail() {
  const lens = readLens();
  const nav = gtdNav(gtdState.catalog, catalog.views, [...pluginViews.keys()], gtdCounters(readHash()));
  const views = railSections(nav, providerName).flatMap((sec) => [
    sec.title ? h("li", { class: "rail-head", role: "presentation" }, sec.title) : null,
    ...sec.views.map((v) => {
      const rows = !v.core && catalog.views.some((c) => c.id === v.id) && !(v.id === "recent" || v.id === "explore" || v.id === "plan");
      const count = v.core ? v.count : v.id === "plan" ? planCount() : rows ? state.rows.filter((r) => passes(r, v.id, lens)).length : "";
      return railItem(v.label, count, v.id === lens.view, lensHash({ view: v.id, entry: "", pr: "" }), (v.id === "stalled" ? "late-count " : "") + (v.core ? "core" : "plugin"));
    })]).filter(Boolean);
  document.getElementById("views").replaceChildren(...views);

  const counts = new Map();
  for (const r of state.rows) {
    if (!passes(r, lens.view, lens, "waits-on")) continue;
    for (const w of waitingWho(r)) counts.set(w, (counts.get(w) || 0) + 1);
  }
  const people = [...counts.entries()].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]));
  document.getElementById("people").replaceChildren(...people.map(([who, n]) => {
    const on = lens.quals.some((x) => x.field === "waits-on" && !x.neg && x.value === normValue("waits-on", who));
    return railItem(who, n, on, lensHash({ q: toggledQualifier(lens.q, "waits-on", who, false) }));
  }));
}

const focusSlot = (name) => ({ resume: "pause", unmute: "mute", skip: "open", run_now: "review", untrack: "track" })[name] || name;

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
    state.error = `Could not load the actions: ${err.message}`;
  }
}

function actionStates(subject, surface, groups) {
  return (subject.actions || [])
    .map((st) => [st, state.specs.byId[st.id]])
    .filter(([, sp]) => sp && sp.surfaces.includes(surface) && (groups ? groups.includes(sp.group) : surface !== "detail" || !CARD_GROUPS.has(sp.group)));
}

function actionTitle(st) {
  return (st.enabled ? st.hint : st.reason) || undefined;
}

function actionButton(subject, st, sp, opts) {
  const key = keyOf(subject);
  const run = opts.run || ((id) => act(subject, id));
  return h("button", {
    type: "button",
    class: ["act", st.enabled && st.primary ? "primary" : "", st.enabled ? "" : "off"].filter(Boolean).join(" "),
    title: actionTitle(st),
    "data-focus": `${opts.scope || "row:" + key}:${focusSlot(st.id)}`,
    disabled: state.busy.has(key) || !st.enabled,
    onclick: (e) => { e.stopPropagation(); return run(st.id); },
  }, st.label || sp.label);
}

// actionButtons renders the served actions of a row, candidate or detail on a surface; the question card, intake and brief groups render themselves.
function actionButtons(subject, surface, opts = {}) {
  return actionStates(subject, surface, opts.groups).map(([st, sp]) => actionButton(subject, st, sp, opts));
}

function actionNotes(subject, surface) {
  return actionStates(subject, surface).filter(([st]) => st.enabled && st.note).map(([st]) => h("p", { class: "act-note" }, st.note));
}

// facet is a value on screen that filters by itself: a click filters by it,
// Shift groups by its field and Alt leaves it out.
function facet(lens, field, raw, content, slot, noun) {
  const f = fieldById(field);
  const v = normValue(field, raw);
  const name = noun || f.label.toLowerCase();
  const by = f.groupBy ? f.groupBy(raw) : f.group ? field : "";
  const pick = (e) => {
    e.stopPropagation();
    if (e.shiftKey && by) return setGroup(by);
    writeHash({ q: toggledQualifier(readHash().q, field, raw, e.altKey) });
  };
  return h("button", {
    type: "button", class: "facet", "data-focus": slot,
    "aria-pressed": String(lens.quals.some((x) => x.field === field && x.value === v && !x.neg)),
    "aria-label": `Filter by ${name} ${raw}`,
    title: `Filter by ${name} ${raw}${by ? ` · Shift+click groups by ${groupField(by).label.toLowerCase()}` : ""} · Alt+click leaves it out`,
    onclick: pick,
    onkeydown: (e) => {
      if (e.key === "Enter" && (e.shiftKey || e.altKey)) {
        e.preventDefault();
        pick(e);
      }
    },
  }, content);
}

function whoFacets(r, lens, slot) {
  const groups = new Set((pl(r).waits_on || []).filter((p) => String(p.key || "").startsWith("group:")).map((p) => p.name));
  return (sm(r).waits_on || "").split(", ").map((w) => resolveWho(r, w)).flatMap((w, i) => [i ? ", " : null,
    w && w !== "nobody" ? facet(lens, "waits-on", w, w, `${slot}waits-on:${w}`, groups.has(w) ? "waits on (group)" : "") : w]);
}

function where(row, scope, lens) {
  const ws = pl(row).workspace || {};
  const bits = [];
  if (ws.display_name) bits.push(h("span", { class: "ws" }, ws.display_name));
  if (ws.status) bits.push(h("span", { class: "col" }, lens ? facet(lens, "status", ws.status, ws.status, `${scope}:facet:status`) : ws.status));
  if ((ws.agent_states || []).length) bits.push(h("span", { class: "agent" }, ws.agent_states.join(", ")));
  const url = safeURL(row.link);
  bits.push(h("a", { href: url, target: "_blank", rel: "noopener noreferrer", class: "ext", "data-focus": `${scope}:link`, onclick: (e) => e.stopPropagation() }, providerName(unitOf(row).provider)));
  return bits;
}

// copySlot is the focus slot of one copy of a row or card: grouping by a tag
// key shows a pull request once per value, copy naming the group.
function copySlot(kind, key, copy) {
  return copy ? `${kind}:${key}@${copy}` : `${kind}:${key}`;
}

// multiCopy is whether a grouping may show a pull request in several groups.
function multiCopy(field) {
  return !!(groupField(field) || {}).keys;
}

function openRow(scope, key) {
  state.openSlot = scope;
  writeHash({ pr: key, entry: "" });
}

// openSlot is the copy of the open pull request last opened, when it is
// still the open one.
function openSlot(pr) {
  const s = state.openSlot || "";
  return pr && ["row", "card"].some((k) => s === `${k}:${pr}` || s.startsWith(`${k}:${pr}@`)) ? s : "";
}

function rowTr(r, pr, lens, copy) {
  const key = keyOf(r);
  const p = pl(r);
  const o = p.override || {};
  const ts = lens.ts;
  const scope = copySlot("row", key, copy);
  const slot = `${scope}:facet:`;
  const mine = isMine(p.author);
  return h("tr", {
    class: [waitClass(r), mine ? "mine" : "", sm(r).stalled ? "late" : "", key === pr ? "sel" : ""].join(" "),
    title: stripTitle(r),
    tabindex: 0,
    "aria-selected": key === pr ? "true" : "false",
    "data-focus": scope,
    onclick: () => openRow(scope, key),
    onkeydown: (e) => {
      if ((e.key === "Enter" || e.key === " ") && e.target === e.currentTarget) {
        e.preventDefault();
        openRow(scope, key);
      }
    },
  },
    h("td", { class: "c-pr" },
      h("div", { class: "title", title: (p.brief || {}).goal }, marked(r.title || labelOf(r), ts)),
      h("div", { class: "meta" },
        h("span", { class: "ref" }, p.repo ? facet(lens, "repo", p.repo, marked(p.repo, ts), slot + "repo") : null, marked(labelTail(r), ts)),
        p.author ? [" by ", facet(lens, "author", mine ? "me" : p.author, h("span", { class: "who" }, marked(p.author, ts)), slot + "author")] : null,
        mine ? mineMark() : null,
        briefTags(p.brief, lens, slot),
        p.head && ts.some((t) => fold(p.head).includes(t)) ? h("span", { class: "head" }, marked(p.head, ts)) : null,
        o.hold ? facet(lens, "is", "paused", h("span", { class: "flag" }, "paused"), slot + "is:paused") : null,
        isMuted(o) ? facet(lens, "is", "muted", h("span", { class: "flag" }, "muted"), slot + "is:muted") : null,
        intakeChip(r))),
    h("td", { class: "c-wait" },
      h("div", { class: "who" },
        facet(lens, "wait", waitCat(r), h("span", { class: "dot", "aria-hidden": "true" }, "●"), slot + "wait"), " ", whoFacets(r, lens, slot)),
      facet(lens, "kind", r.kind, h("span", { class: "pill" }, rowKindLabel(r)), slot + "kind")),
    h("td", { class: "c-for" }, r.pending ? countdown(r) : h("span", { class: "age", title: stamp(sm(r).since) }, age(sm(r).since))),
    h("td", { class: "c-why" }, sm(r).reason),
    h("td", { class: "c-where" }, where(r, scope, lens)),
    h("td", { class: "c-act" }, actionButtons(r, "row", { scope })));
}

// groupQualifier is the filter that keeps only a group's rows: a tag key's
// "no lang" group is -tag:lang.
function groupQualifier(g) {
  const f = groupField(g.field);
  if (f.tagKey) return g.key === "none" ? { field: "tag", raw: f.tagKey, neg: true } : { field: "tag", raw: g.raw, neg: false };
  return f.values ? { field: g.field, raw: g.raw, neg: false } : null;
}

function onlyThis(g, lens) {
  const q = groupQualifier(g);
  if (!q) return null;
  const v = normValue(q.field, q.raw);
  const on = lens.quals.some((x) => x.field === q.field && x.value === v && x.neg === q.neg);
  return h("button", {
    type: "button", class: "facet only", "aria-pressed": String(on), "data-focus": `group:${g.field}:${g.key}:only`,
    "aria-label": on ? `Show all, not only ${g.label}` : `Only ${g.label}`,
    onclick: (e) => { e.stopPropagation(); writeHash({ q: toggledQualifier(lens.q, q.field, q.raw, q.neg) }); },
  }, on ? "show all" : "only this");
}

function toggleCollapsed(key) {
  if (state.collapsed.has(key)) state.collapsed.delete(key);
  else state.collapsed.add(key);
  lastFocus = `group:${key}`;
  render();
}

function renderTable(res, pr, lens) {
  const table = document.getElementById("table");
  const grouped = !!res.group;
  const multi = grouped && multiCopy(res.group);
  table.replaceChildren(table.tHead, ...res.groups.filter((g) => g.rows.length).map((g, i) => {
    const key = `${g.field}:${g.key}`;
    const collapsed = grouped && state.collapsed.has(key);
    const rows = g.rows.map((r) => {
      const tr = rowTr(r, pr, lens, multi ? g.key : "");
      tr.hidden = collapsed;
      return tr;
    });
    if (!grouped) return h("tbody", {}, rows);
    const id = `g-${i}`;
    return h("tbody", { id },
      h("tr", { class: "g-head" + (g.field === "wait" ? " w-" + g.key : "") },
        h("th", { scope: "rowgroup", colspan: 6 },
          h("button", { type: "button", class: "g-toggle", "aria-expanded": String(!collapsed), "aria-controls": id, "data-focus": `group:${key}`, onclick: () => toggleCollapsed(key) },
            `${collapsed ? "▸" : "▾"} ${g.label}`),
          h("span", { class: "count" }, g.rows.length),
          onlyThis(g, lens))),
      rows);
  }));
}

const SORT_COLUMNS = { "c-pr": "repo", "c-wait": "wait", "c-for": "age", "c-why": "kind", "c-where": "status" };

// unitColumn is how the list names its unit column in view: by the view's
// provider, "Item" when its rows mix providers.
function unitColumn(view) {
  const v = catalog.views.find((x) => x.id === view) || {};
  const sort = v.unit_sort || "repo";
  const f = fieldById(sort);
  return { title: v.unit || "Item", sort, sortTitle: "Sort by " + (f ? f.label.toLowerCase() : sort) };
}

function renderUnitColumn(view) {
  const col = unitColumn(view);
  const b = document.querySelector("#table thead th.c-pr button");
  if (!b) return col;
  b.textContent = col.title;
  b.dataset.sort = col.sort;
  b.dataset.focus = "th:" + col.sort;
  b.title = col.sortTitle;
  return col;
}

function renderSortHeaders(lens) {
  const { field, desc } = parseSort(lens.sort);
  const unitSort = unitColumn(lens.view).sort;
  for (const th of document.querySelectorAll("#table thead th")) {
    const id = th.className === "c-pr" ? unitSort : SORT_COLUMNS[th.className];
    if (id) th.setAttribute("aria-sort", id === field ? (desc ? "descending" : "ascending") : "none");
  }
}

function renderRows() {
  const lens = readLens();
  const { pr } = readHash();
  const { view } = lens;
  const listless = view === "recent" || view === "explore" || view === "plan" || pluginViews.has(view) || isGTDView(view);
  renderActivity(lens);
  const table = document.getElementById("table");
  const boardBox = document.getElementById("board");
  const empty = document.getElementById("empty");
  const listBox = document.getElementById("list-box");
  listBox.classList.toggle("in-dash", view === "dash");
  listBox.hidden = view === "plan" || pluginViews.has(view) || isGTDView(view);
  if (view !== "dash") listBox.open = true;
  if (listless) {
    renderLens(lens, null);
    table.hidden = true;
    table.replaceChildren(table.tHead);
    boardBox.hidden = true;
    boardBox.replaceChildren();
    empty.hidden = true;
    return;
  }
  const res = pipeline(state.rows, lens);
  document.getElementById("list-sum").textContent = `${renderUnitColumn(view).title}s: ${res.shown} of ${res.total}`;
  if (state.collapsedFor !== res.group) {
    state.collapsed.clear();
    state.collapsedFor = res.group;
  }
  const board = lens.layout === "board";
  renderLens(lens, res);
  table.hidden = board;
  boardBox.hidden = !board || !res.shown;
  if (board) {
    table.replaceChildren(table.tHead);
    if (res.shown) renderBoard(res.groups, pr, lens);
  } else {
    boardBox.replaceChildren();
    renderTable(res, pr, lens);
    renderSortHeaders(lens);
  }
  empty.hidden = res.shown > 0;
  if (!res.shown) empty.replaceChildren(...emptyNote(view, lens.q));
}

function emptyNote(view, q) {
  if (!state.loaded) return ["Loading the inventory."];
  const label = (catalog.views.find((v) => v.id === view) || {}).label;
  const row = rowByURL(q);
  if (row) {
    return [`${labelOf(row)} is in the inventory. `, h("button", {
      type: "button", class: "act", "data-focus": "empty:open", onclick: () => writeHash({ q: "", pr: keyOf(row) }),
    }, "Open it")];
  }
  const pull = PR_URL.exec(String(q || "").trim());
  if (pull && !/^\.\.?$/.test(pull[2])) {
    const [, owner, repo, number] = pull;
    const typed = q.trim();
    return [`${refLabel({ owner, repo, number })} is not in the inventory. `,
      h("button", { type: "button", class: "act", "data-focus": "empty:explore", onclick: () => { writeHash({ view: "explore", q: "", x: q.trim() }); explore(q); } }, "Explore it"),
      " ",
      h("a", { href: typed, target: "_blank", rel: "noopener noreferrer", "data-focus": "empty:gh" }, "Open it on GitHub")];
  }
  if (!q) return [`Nothing in ${label} right now.`];
  const note = `Nothing in ${label} matches "${q}".`;
  if (!qualifiers(q).length) return [note];
  return [note, " ", h("button", { type: "button", class: "act", "data-focus": "empty:clear", onclick: () => clearLens() }, "Clear filters")];
}

function renderBoard(groups, pr, lens) {
  document.getElementById("board").replaceChildren(...groups.map((g) => h("section", { class: "lane", "aria-label": g.label },
    h("h2", { class: "lane-head" + (g.field === "wait" ? " w-" + g.key : "") }, h("span", {}, g.label),
      h("span", { class: "lane-tools" }, onlyThis(g, lens), h("span", { class: "count" }, g.rows.length))),
    h("ol", { class: "cards" }, g.rows.map((r) => card(r, pr, multiCopy(g.field) ? g.key : ""))))));
}

function mineMark() {
  return h("span", { class: "flag mine-mark", title: "You opened this pull request" }, "mine");
}

function card(r, pr, copy) {
  const key = keyOf(r);
  const p = pl(r);
  const mine = isMine(p.author);
  const scope = copySlot("card", key, copy);
  return h("li", {},
    h("button", {
      type: "button", class: ["card", waitClass(r), mine ? "mine" : "", key === pr ? "sel" : ""].join(" "), "data-focus": scope, title: stripTitle(r),
      "aria-current": key === pr ? "true" : undefined, onclick: () => openRow(scope, key),
    },
      h("span", { class: "title", title: (p.brief || {}).goal }, r.title || labelOf(r)),
      h("span", { class: "meta" }, `${labelOf(r)} · ${waitingLabel(r)} · ${rowKindLabel(r)}`,
        mine ? mineMark() : null, (p.override || {}).hold ? h("span", { class: "flag" }, "paused") : null, intakeChip(r), briefTags(p.brief)),
      h("span", { class: "age", title: stamp(sm(r).since) }, age(sm(r).since))));
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

function dayLabel(t) {
  const d = new Date(t);
  const startOf = (x) => new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime();
  const days = Math.round((startOf(new Date()) - startOf(d)) / 86400000);
  if (days === 0) return "Today";
  if (days === 1) return "Yesterday";
  return d.toLocaleDateString(undefined, { weekday: "short", month: "short", day: "numeric", year: "numeric" });
}

function clock(t) {
  return new Date(t).toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
}

function actorLabel(actor) {
  const viewer = state.health && state.health.viewer;
  return viewer && actor.toLowerCase() === viewer.toLowerCase() ? "me" : actor;
}

const REQUEST_EVENTS = new Set(["review_requested", "review_request_removed"]);

function requestedFrom(reviewer) {
  return reviewer.startsWith("team:") ? reviewer.slice("team:".length) : actorLabel(reviewer);
}

function eventData(e) {
  if (!e.data || REQUEST_EVENTS.has(e.kind)) return "";
  switch (e.kind) {
    case "review": return e.data.split(":")[0];
    case "merged": return e.data.slice(0, 7);
  }
  return e.data;
}

function renderActivity(lens) {
  const list = document.getElementById("activity");
  const { view, pr } = readHash();
  list.hidden = view !== "recent";
  if (list.hidden) {
    list.replaceChildren();
    return;
  }
  const items = state.activity.filter((it) => (!activityLoopOnly || LOOP_EVENTS.has(it.event.kind)) && activityPasses(it, lens));
  const out = [h("li", { class: "a-tools" },
    h("label", {},
      h("input", { type: "checkbox", checked: activityLoopOnly, "data-focus": "activity:loop-only", onchange: (e) => setLoopOnly(e.target.checked) }),
      " Loop only"))];
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
    const requested = REQUEST_EVENTS.has(e.kind) && e.data;
    out.push(h("li", {
      class: `a-ev ev-${e.kind}` + (LOOP_EVENTS.has(e.kind) ? " loop" : "") + (key === pr && id === activitySelected ? " sel" : ""),
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
        h("span", { class: "kind" }, EVENT_LABEL[e.kind] || e.kind.replaceAll("_", " ")),
        requested ? h("span", { class: "who" }, " from " + requestedFrom(e.data)) : null,
        e.actor ? h("span", { class: "who" }, (requested ? " by " : " ") + actorLabel(e.actor)) : null,
        " ", h("span", { class: "ref" }, refLabel(e.pr)),
        " ", h("span", { class: "title" }, it.title)),
      data ? h("span", { class: "data" }, data) : null));
  }
  if (!items.length) out.push(h("li", { class: "empty" }, activityEmptyNote(readHash().q)));
  list.replaceChildren(...out);
}

function activityEmptyNote(q) {
  if (!state.activityLoaded) return ["Loading the activity."];
  if (state.activityError && !state.activity.length) return ["The recent activity could not be loaded."];
  if (!state.activity.length) return ["Nothing happened in the inventory yet."];
  if (tokens(q).length) return emptyNote("recent", q);
  return ["No loop events in the recent activity."];
}

function guidanceLabel(r) {
  if (r.question) return r.question.state === "asked" && questionChoices(r, r.question).length ?"Or answer in your own words" : "Answer in your own words";
  return r.pending ? "Guidance for the pending round; Run now sends it at once" : "Guidance for the next round (optional)";
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
      if (answer) answer.disabled = state.busy.has(key) || !e.target.value.trim();
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
    ...shown.map((t) => {
      const pill = h("span", { class: "pill tag" }, t);
      return lens ? facet(lens, "tag", t, pill, slot + "tag:" + t) : pill;
    }),
    rest.length ? h("span", { class: "pill tag", title: rest.join(", ") }, `+${rest.length}`) : null,
  ];
}

// briefTags takes a row's brief summary; open_questions is a count. With a
// lens, size, risk and tags are facets.
function briefTags(b, lens, slot) {
  if (!b) return null;
  const size = SIZES.includes(b.size) ? b.size : "";
  const risk = RISKS.includes(b.risk) ? b.risk : "";
  const n = Number.isInteger(b.open_questions) && b.open_questions > 0 ? b.open_questions : 0;
  const tags = Array.isArray(b.tags) ? b.tags : [];
  const type = b.kind || "";
  if (!type && !size && !risk && !n && !tags.length) return null;
  const typePill = type ? h("span", { class: "pill", title: `type ${type}` }, type) : null;
  const sizePill = size ? h("span", { class: "pill", title: `size ${size}` }, size.toUpperCase()) : null;
  const riskPill = risk ? h("span", { class: `pill risk-${risk}` }, `risk ${risk}`) : null;
  return h("span", { class: "btags" },
    typePill && lens ? facet(lens, "type", type, typePill, slot + "type") : typePill,
    sizePill && lens ? facet(lens, "size", size, sizePill, slot + "size") : sizePill,
    riskPill && lens ? facet(lens, "risk", risk, riskPill, slot + "risk") : riskPill,
    n ? h("span", { class: "pill qs", title: `${n} open question${n === 1 ? "" : "s"} in the brief` }, `? ${n}`) : null,
    tagChips(tags, lens, slot));
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
  if (b.state === "pending" && !b.brief) return h("div", { class: "d-brief muted" }, "Brief pending", " ", refreshBriefButton(r));
  if (!b.brief) return h("div", { class: "d-brief muted" }, b.error ? `Brief failed: ${b.error}. It is tried again later.` : "Brief failed. It is tried again later.", " ", refreshBriefButton(r));
  const x = b.brief;
  const failed = b.state === "failed";
  const notes = [b.model, stamp(b.at), b.stale && !failed ? "refreshing" : "", failed ? `last refresh failed: ${b.error || "unknown error"}` : ""].filter(Boolean).join(" · ");
  const risk = ["low", "medium", "high"].includes(x.risk) ? x.risk : "";
  return h("section", { class: "d-brief", "aria-label": "Brief" },
    h("div", { class: "d-brief-tags" },
      briefFacet(lens, "type", x.kind, h("span", { class: "pill" }, x.kind)),
      briefFacet(lens, "size", SIZES.includes(x.size) ? x.size : "", h("span", { class: "pill" }, `size ${x.size}`)),
      risk ? briefFacet(lens, "risk", risk, h("span", { class: `pill risk-${risk}` }, `${risk} risk`)) : null,
      tagChips(Array.isArray(x.tags) ? x.tags : [], lens, "detail:facet:", true)),
    h("p", { class: "goal" }, x.goal),
    x.context ? h("p", {}, x.context) : null,
    x.risk_why ? h("p", { class: "muted" }, `Risk: ${x.risk_why}`) : null,
    (x.open_questions || []).length ? h("ul", { class: "questions" }, x.open_questions.map((q) => h("li", {}, q))) : null,
    h("div", { class: "muted small" }, notes, " ", refreshBriefButton(r)));
}

// trimmedNote names the profile blocks the current turn's guide left out
// to stay within the judgment limit.
function trimmedNote(d) {
  const id = d.turn && d.turn.turn && d.turn.turn.id;
  const e = id && (d.events || []).find((x) => x.kind === "guide_trimmed" && x.key === id);
  if (!e) return null;
  return h("div", { class: "d-trimmed muted small" }, `Guide trimmed over the judgment limit: ${e.data}`);
}

function intakeWaiting(r) {
  const i = r && pl(r).intake;
  return !!i && (i.state === "accepted" || i.state === "briefing");
}

function intakeChip(r) {
  if (!intakeWaiting(r)) return null;
  const i = pl(r).intake;
  const title = [`intake ${i.seq} waits for the brief`, i.attempt ? `attempt ${i.attempt}` : "", i.last_error ? `last error: ${i.last_error}` : ""].filter(Boolean).join(", ");
  return h("span", { class: "flag briefing", title }, "briefing");
}

// intakeLines say what a waiting intake waits on: the attempt, the last
// error and the next attempt's time.
function intakeLines(i) {
  return [
    `Intake ${i.seq} (${i.intent}) waits for the brief${i.attempt ? `, attempt ${i.attempt}` : ""}`,
    i.last_error ? `Last error: ${i.last_error}` : "",
    isSet(i.next) ? `Next attempt at ${stamp(i.next)}` : "",
  ].filter(Boolean);
}

function intakeBlock(r) {
  if (!intakeWaiting(r)) return null;
  const i = pl(r).intake;
  return h("section", { class: "d-intake", "aria-label": "Briefing" },
    h("h2", { class: "d-sub" }, "Briefing"),
    intakeLines(i).map((l) => h("div", {}, l)),
    h("div", { class: "q-turn" }, actionButtons(r, "detail", { scope: "detail", groups: ["intake"] })));
}

// acceptedNote is what the notice says once the daemon took a review, an
// address or a run as an intake.
function acceptedNote(name, ref, i) {
  if (!i) return "";
  if (!i.joined) return `${name} ${ref}: accepted as intake ${i.seq}`;
  if (i.state === "starting") return `${name} ${ref}: intake ${i.seq} is already starting the turn; this request changed nothing`;
  return `${name} ${ref}: joined intake ${i.seq}`;
}

function checksBlock(r) {
  const list = state.checks[keyOf(r)];
  if (r.kind !== "checks" || !list || !list.length) return null;
  return h("section", { class: "d-checks", "aria-label": "Failing checks" },
    h("h2", { class: "d-sub" }, "Failing checks"),
    h("ul", {}, list.map((c) => h("li", {},
      safeURL(c.url) ? h("a", { href: safeURL(c.url), target: "_blank", rel: "noopener noreferrer" }, c.name) : c.name,
      c.summary ? h("span", { class: "muted" }, ` · ${c.summary}`) : null,
      (c.steps || []).length ? h("span", { class: "muted" }, ` · failed at ${c.steps.join(", ")}`) : null))));
}

function escapeHTML(s) {
  return String(s).replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c]);
}

function reviewRequest(r, b) {
  const url = safeURL(r.link);
  const ref = labelOf(r);
  const title = r.title.replaceAll("`", "'");
  const x = b && b.brief;
  const sections = x ? [["Goal:", [x.goal]], ["Context:", [x.context]], ["Risk:", [x.risk_why]]]
    .map(([label, lines]) => [label, lines.filter(Boolean)]).filter(([, lines]) => lines.length) : [];
  const text = [`review, pls? \`${title}\``, url || ref,
    ...sections.flatMap(([label, lines]) => ["", `*${label}*`, ...lines])].join("\n");
  const link = url ? `<a href="${escapeHTML(url)}">${escapeHTML(ref)}</a>` : escapeHTML(ref);
  const html = [`review, pls? <code>${escapeHTML(title)}</code>`, link,
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
    setNotice(`Copied the review request for ${labelOf(r)}; paste it in Slack.`);
  } catch (err) {
    setNotice(`Could not copy: ${err.message}`, true);
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
  return h("button", {
    type: "button",
    class: ["act", st.enabled && attrs.primary ? "primary" : "", st.enabled ? "" : "off"].filter(Boolean).join(" "),
    title: st.enabled ? st.hint || attrs.title : st.reason || undefined,
    disabled: state.busy.has(keyOf(r)) || !st.enabled || !!attrs.disabled,
    "data-focus": `detail:${slot}`,
    onclick: () => act(r, name, name === "answer" && answerNote(r) ? { ...extra, guidance: answerNote(r) } : extra),
  }, st.label || label);
}

// answerBlock is the card's free answer: the box and the button that sends
// its text alone, off while the box is empty.
function answerBlock(r, choices) {
  const send = turnButton(r, "answer", "Send answer", "answer", {}, {
    title: "Send the text as your answer: typed in the agent's session when it is there, else a new session resumes the turn with it",
    disabled: !answerNote(r),
  });
  const placeholder = choices ? "Ask for something else, or add a note to the choice above" : "Tell the agent what to do next";
  return h("div", { class: "q-answer" },
    h("label", { class: "d-sub", for: "guidance" }, guidanceLabel(r)),
    guidanceBox(keyOf(r), placeholder),
    h("div", { class: "q-send" }, send));
}

function recommendedLabel(label, recommended) {
  return recommended ? [label, h("span", { class: "rec" }, " recommended")] : label;
}

function optionButton(r, o, recommended) {
  return turnButton(r, `option:${o.id}`, recommendedLabel(o.label, recommended), "answer",
    { option_id: o.id }, { primary: recommended, title: o.consequence });
}

function questionChoices(r, q) {
  if (q.kind === "confirm") {
    return [
      turnButton(r, "yes", recommendedLabel("Yes", q.recommend === "yes"), "answer", { yes: true }, { primary: q.recommend === "yes" }),
      turnButton(r, "no", recommendedLabel("No", q.recommend === "no"), "answer", { no: true }, { primary: q.recommend === "no" }),
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
  const kind = (a.kind || "artifact").replaceAll("_", " ");
  const lines = text.split("\n").length;
  const body = h("pre", { class: "artifact-body" }, text);
  if (lines <= ARTIFACT_LINES) return h("section", { class: "d-artifact", "aria-label": kind }, h("div", { class: "d-sub" }, kind), body);
  return h("details", { class: "d-artifact" }, h("summary", { class: "d-sub" }, `${kind}, ${lines} lines`), body);
}

// questionBlock is the turn that waits on the user: the agent's question
// with its options, or its stop with the last message.
function questionBlock(d) {
  const r = d.row;
  const q = r.question;
  if (!q) return null;
  const rec = questionRecord(d);
  const asked = q.state === "asked";
  const head = asked ? "Agent asks" : "Agent stopped";
  const last = q.last_message || (d.turn && d.turn.last_message) || "";
  const choices = asked ? questionChoices(r, q) : [];
  return h("section", { class: "d-question", "aria-label": head },
    h("h2", { class: "d-sub" }, head),
    rec.summary ? h("p", { class: "summary" }, rec.summary) : null,
    q.verdict ? h("p", { class: "verdict" }, `drafted ${q.verdict}, ${q.blocking} blocking, ${q.non_blocking} non-blocking`) : null,
    asked && q.text ? h("pre", { class: "q-text" }, q.text) : null,
    !asked && last ? [h("div", { class: "muted small" }, "Last message"), h("pre", { class: "q-text" }, last)] : null,
    asked && q.kind === "free" && q.recommend ? h("p", { class: "recommend" }, `Agent recommends: ${q.recommend}`) : null,
    choices.length ? h("div", { class: "q-options" }, choices) : null,
    answerBlock(r, choices.length > 0),
    h("div", { class: "q-turn" },
      turnButton(r, "continue", "Continue", "continue", {}, { title: "The agent goes on with the turn without an answer" }),
      turnButton(r, "dismiss", "Dismiss", "dismiss", {}, { title: "Close the question; the pull request goes back to whoever it waits on" })),
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
  const t = d.turn;
  const answers = t ? (t.answers || []).filter((a) => a.turn_seq === t.turn.seq) : [];
  const since = t ? Date.parse(t.turn.sent_at) || 0 : 0;
  const events = (d.events || []).filter((e) => !(answers.length && e.kind === "answered" && Date.parse(e.at) >= since));
  for (const a of answers) {
    const queued = a.route === "queued" ? ", queued until the agent stops" : "";
    events.push({ kind: "answered", at: a.at, actor: "", data: `${answerSaid(a)} (from ${a.source}${queued})` });
  }
  return events.sort((a, b) => Date.parse(b.at) - Date.parse(a.at));
}

function clampDetailWidth(px, viewport) {
  const max = Math.max(DETAIL_WIDTH_MIN, Math.floor(viewport * 0.6));
  const w = Number.isFinite(px) ? Math.round(px) : DETAIL_WIDTH_DEFAULT;
  return Math.min(max, Math.max(DETAIL_WIDTH_MIN, w));
}

function storedDetailWidth(viewport) {
  try {
    const raw = localStorage.getItem(DETAIL_WIDTH_KEY);
    if (raw === null) return DETAIL_WIDTH_DEFAULT;
    return clampDetailWidth(Number(raw), viewport);
  } catch {
    return DETAIL_WIDTH_DEFAULT;
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
    choose(DETAIL_WIDTH_DEFAULT);
    saveDetailWidth(null);
  });
  window.addEventListener("resize", layout);
}

function renderDetail() {
  const box = document.getElementById("detail");
  if (renderEntryDetail(box, readHash())) return;
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
  const old = document.getElementById("guidance");
  const caret = old && document.activeElement === old ? [old.selectionStart, old.selectionEnd] : null;
  const guidance = inCard ? null : guidanceBox(key, guidanceLabel(r));
  const muteState = o.mute_until_state_change ? "muted until the situation changes"
    : isMuted(o) ? `muted until ${stamp(o.muted_until)}` : "";
  const events = timelineEvents(d);

  const top = h("div", { class: "d-top" },
    h("div", { class: "d-head" },
      h("button", { type: "button", class: "close", "aria-label": "Close detail", "data-focus": "detail:close", onclick: closeDetail }, "×"),
      h("h1", {}, r.title || labelOf(r)),
      h("div", { class: "meta" },
        h("a", { href: safeURL(r.link), target: "_blank", rel: "noopener noreferrer", class: "ref", "data-focus": "detail:ref" }, labelOf(r)),
        p.author ? [" by ", h("span", { class: "who" }, p.author)] : null)),
    h("div", { class: "d-sit " + waitClass(r) + (sm(r).stalled ? " late" : "") },
      h("div", { class: "d-clock" }, r.pending ? countdown(r) : h("span", { class: "age" }, age(sm(r).since)), h("span", { class: "pill" }, rowKindLabel(r))),
      h("div", {}, "Waiting on ", h("strong", {}, waitingLabel(r)), isSet(sm(r).since) ? ` since ${stamp(sm(r).since)}` : ""),
      h("div", { class: "why" }, sm(r).reason),
      sm(r).line && sm(r).line !== sm(r).reason ? h("div", { class: "line" }, sm(r).line) : null,
      sm(r).stalled ? h("div", { class: "late-note" }, "Past its threshold") : null),
    checksBlock(r),
    h("dl", { class: "d-facts" },
      h("dt", {}, "In scope as"), h("dd", {}, (p.reasons || []).join(", ") || "out of scope"),
      h("dt", {}, "Workspace"), h("dd", {}, p.workspace && p.workspace.display_name ? where(r, "detail") : "none"),
      h("dt", {}, "Controls"), h("dd", {}, [o.hold ? "paused" : "", muteState, o.adopted ? "adopted" : ""].filter(Boolean).join(", ") || "none"),
      d.profile_chain ? [h("dt", { title: "The review profile layers a round here reads, lowest first; {{command}} profile show shows the files" }, "Review profile"), h("dd", {}, d.profile_chain)] : null,
      o.next_guidance ? [h("dt", { title: "Guidance waiting for the next round: given with no round pending, or kept from a round that did not go out or whose prompt was not seen landing. The next round, Review or Address included, takes it unless you give other guidance" }, "Next round guidance"), h("dd", {}, o.next_guidance)] : null),
    questionBlock(d),
    unitExtras(d),
    trimmedNote(d),
    intakeBlock(r),
    inCard ? null : h("label", { class: "d-sub", for: "guidance" }, guidanceLabel(r)));
  const acts = h("div", { class: "d-acts" },
    h("div", { class: "d-actions" }, [...actionButtons(r, "detail", { scope: "detail", run: (id) => act(r, id, { guidance: guidance ? guidance.value.trim() : answerNote(r) }) }), h("button", { type: "button", class: "act", "data-focus": "detail:copy-review", title: "Copy a review request with the title, link and brief, formatted for Slack", onclick: () => copyReviewRequest(r, d.brief) }, "Copy review request")]),
    actionNotes(r, "detail"),
    briefBlock(d.brief, r, readLens()));
  const log = h("div", { class: "d-log" },
    h("h2", { class: "d-sub" }, "Timeline"),
    h("ol", { class: "timeline" }, events.map((e) => h("li", { class: "ev-" + e.kind },
      h("time", { datetime: e.at }, stamp(e.at)),
      h("div", {}, h("span", { class: "kind" }, EVENT_LABEL[e.kind] || e.kind.replaceAll("_", " ")), e.actor ? h("span", { class: "who" }, " " + e.actor) : null),
      e.data ? h("span", { class: "data" }, e.data) : null))));

  if (box.querySelector(".d-acts") && (!guidance || guidance.parentNode === box)) {
    box.querySelector(".d-top").replaceWith(top);
    box.querySelector(".d-acts").replaceWith(acts);
    box.querySelector(".d-log").replaceWith(log);
  } else {
    box.replaceChildren(...[top, guidance, acts, log].filter(Boolean));
  }
  // The card is rebuilt on every render and takes the box along, which drops
  // its focus and caret.
  const now = document.getElementById("guidance");
  if (caret && now && document.activeElement !== now) {
    now.focus({ preventScroll: true });
    now.setSelectionRange(...caret);
  }
  box.dataset.key = key;
  box.hidden = false;
  document.body.classList.add("has-detail");
  if (opening && matchMedia("(max-width: 900px)").matches) box.scrollIntoView({ block: "start" });
}

function revealOpenRow(pr) {
  if (!pr) {
    state.revealed = "";
    return;
  }
  if (state.revealed === pr) return;
  const slot = openSlot(pr);
  const row = (slot && document.querySelector(`[data-focus="${CSS.escape(slot)}"]`)) || document.querySelector("tbody tr.sel, .card.sel");
  if (!row) return;
  state.revealed = pr;
  row.scrollIntoView({ block: "nearest" });
}

function ago(t) {
  const a = age(t);
  return a === "now" ? "just now" : a ? `${a} ago` : "";
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
  return sorts.length ? sorts[sorts.length - 1].slice("sort:".length) : DEFAULT_SORT;
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
  if (hours < 1) return `${Math.floor(ms / 60000)}m`;
  if (hours < 48) return `${hours}h`;
  return `${Math.floor(hours / 24)}d`;
}

// candidateWait counts from the last activity, or from the row's own clock
// when the inventory holds the pull request.
function candidateWait(c, row, hours, now = Date.now()) {
  const tracked = row && isSet(sm(row).since);
  const since = tracked ? sm(row).since : c.updated_at;
  if (!isSet(since)) return null;
  const ms = Math.max(0, now - Date.parse(since));
  return { text: `waiting ${waited(ms)}`, late: tracked ? !!sm(row).stalled : ms > (hours || 48) * 3600000 };
}

function toggleTerm(term) {
  const input = document.getElementById("explore-q");
  input.value = toggledQuery(input.value, term);
  writeHash({ x: input.value }, true);
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
    const res = await fetch("/api/explore", {
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
    state.explore.error = `Explore failed: ${err.message}`;
  }
  state.explore.busy = false;
  render();
}

function exploreStatus() {
  const ex = state.explore;
  if (ex.busy) return "Searching GitHub…";
  if (ex.error) return ex.error;
  const r = ex.result;
  if (!r) return "A search runs only when you press Enter or Search; each one spends about 1 point of GitHub's hourly budget.";
  const shown = r.candidates.length;
  const order = EXPLORE_SORTS.find((s) => s.sort === querySort(r.query || ""));
  const more = r.total > shown ? `, the ${order ? `${shown} ${order.shown}` : `first ${shown}`} of ${r.total}; narrow the search for the rest` : "";
  const cost = r.rate_limit && r.rate_limit.limit ? ` · cost ${r.cost}, ${r.rate_limit.remaining} of ${r.rate_limit.limit} left` : "";
  const read = r.read ? `${r.read} · ` : "";
  if (!shown && r.empty) return `${read}${r.empty}${cost}`;
  return `${read}${shown} pull request${shown === 1 ? "" : "s"}${more} for ${r.query}${cost}`;
}

function candidateItem(c, sel) {
  const key = keyOf(c);
  const row = rowByKey(key);
  const facts = [
    `+${c.additions} -${c.deletions} · ${c.changed_files} file${c.changed_files === 1 ? "" : "s"}`,
    REVIEW_LABEL[c.review_decision] || "no review required",
    c.checks ? `checks ${c.checks.toLowerCase()}` : "no checks",
    `${c.comments} comment${c.comments === 1 ? "" : "s"}`,
  ];
  if ((c.review_requests || []).length) facts.push("asks " + c.review_requests.map((w) => w.replace(/^team:/, "")).join(", "));
  if (c.my_review) facts.push(`my review: ${c.my_review.toLowerCase().replace("_", " ")}${c.my_review_on_head ? "" : " (older head)"}`);
  const b = c.brief && c.brief.brief;
  const wait = candidateWait(c, row, state.health && state.health.stalled_review_hours);
  return h("li", { class: ["cand", row ? waitClass(row) : "", key === sel ? "sel" : ""].join(" ") },
    h("button", { type: "button", class: "cand-pick", "aria-pressed": String(key === sel), "data-focus": `cand:${key}`, onclick: () => selectCandidate(key) },
      h("span", { class: "title" }, c.title || refLabel(c.ref)),
      h("span", { class: "meta" }, `${refLabel(c.ref)} by ${c.author} · opened ${ago(c.created_at)} · updated ${ago(c.updated_at)}`,
        wait ? h("span", { class: wait.late ? "wait late" : "wait", title: row ? `waiting on ${waitingLabel(row)}` : "since the last activity" }, wait.text) : null,
        c.draft ? h("span", { class: "flag" }, "draft") : null,
        c.state && c.state !== "OPEN" ? h("span", { class: "flag" }, c.state.toLowerCase()) : null)),
    h("div", { class: "facts" }, facts.join(" · ")),
    row ? h("div", { class: "inv" }, h("span", { class: "pill" }, rowKindLabel(row)),
      ` waiting on ${waitingLabel(row)} for ${age(sm(row).since)} · in {{name}} as ${(pl(row).reasons || []).join(", ") || "out of scope"}`)
      : h("div", { class: "inv" }, "not in {{name}}"),
    b ? h("div", { class: "brief-line" }, briefTags({ size: b.size, risk: b.risk, open_questions: (b.open_questions || []).length }), ` ${b.kind} · ${b.goal}`) : null,
    candidateStatus(key));
}

function candidateStatus(key) {
  const st = state.explore.status[key];
  if (!st) return null;
  return h("div", { class: st.error ? "inv cand-status err" : "inv cand-status", role: "status" }, st.text,
    st.done && rowByKey(key) ? h("button", { type: "button", class: "act", "data-focus": `cand:open:${key}`, onclick: () => writeHash({ pr: key }) }, "Open in {{name}}") : null);
}

// actOnCandidate brings a pull request the inventory lacks in first, so its
// card shows it in the loop while the action runs, then reports the outcome on
// the card itself.
async function actOnCandidate(c, name, extra = {}) {
  const key = keyOf(c);
  const status = (text, more = {}) => { state.explore.status[key] = { text, ...more }; render(); };
  const implicit = !rowByKey(key);
  if (implicit) {
    status("adding it to {{name}}…");
    if (!(await act(c, "track"))) return status(state.actionError || "adding it to {{name}} failed", { error: true }), false;
    await load();
    if (name === "track") return status("added to {{name}}", { done: true }), true;
  }
  const row = rowByKey(key) || c;
  const offered = (row.actions || []).find((a) => a.id === name);
  const refused = offered && !offered.enabled ? offered.reason : "";
  const ok = refused ? undefined : (status(`${name} running…`), await act(row, name, extra));
  await load();
  if (ok === undefined && implicit) {
    const undone = (await act(row, "untrack")) ? "untracked it again" : `untracking it failed too: ${state.actionError}`;
    status(`${name} ${refLabel(c.ref)}: ${refused || "cancelled"}; ${undone}`, { error: true });
    return false;
  }
  if (refused) return status(`${name} ${refLabel(c.ref)}: ${refused}`, { error: true }), false;
  status(ok ? (implicit ? `${name} started; it is in {{name}} now` : `${name} sent`) : (state.actionError || `${name} failed`), { error: !ok, done: ok });
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
    h("a", { href: candidateURL(c), target: "_blank", rel: "noopener noreferrer", "data-focus": "explore:gh" }, refLabel(c.ref)), " ", c.title);
  document.getElementById("explore-act-buttons").replaceChildren(
    ...actionButtons(c, "explore", { scope: "explore", run: (id) => actOnCandidate(c, id, { guidance: guidance.value.trim() }) }),
    row ? h("button", { type: "button", class: "act", disabled: state.busy.has(key), "data-focus": "explore:detail", onclick: () => writeHash({ pr: key }) }, "Detail") : null);
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
    h("span", { class: "chip-head" }, "Run"),
    ...EXPLORE_PRESETS.map((p) => chip({
      title: p.query, disabled: ex.busy, "data-focus": `explore:preset:${p.query}`, onclick: () => explore(p.query),
    }, p.label)),
    h("span", { class: "chip-head" }, "Filters"),
    ...EXPLORE_FILTERS.map((f) => f.window ? chip({
      title: "updated:>=", "aria-pressed": String(hasWindow(input.value)), "data-focus": "explore:filter:window", onclick: () => toggleWindow(f.window),
    }, f.label) : chip({
      "aria-pressed": String(hasTerm(input.value, f.term)), "data-focus": `explore:filter:${f.term}`, onclick: () => toggleTerm(f.term),
    }, f.label)),
    h("span", { class: "chip-head" }, "Order"),
    ...EXPLORE_SORTS.map((o) => chip({
      title: `sort:${o.sort}`, "aria-pressed": String(querySort(input.value) === o.sort), "data-focus": `explore:sort:${o.sort}`, onclick: () => pickSort(o.sort),
    }, o.label)),
    ex.recent.length ? h("span", { class: "chip-head" }, "Recent") : null,
    ...ex.recent.map((q) => chip({ title: q, "data-focus": `explore:recent:${q}`, onclick: () => explore(q) }, q)));
  const status = document.getElementById("explore-status");
  const cands = (ex.result && ex.result.candidates) || [];
  status.replaceChildren(exploreStatus(), cands.length ? h("button", {
    type: "button", class: "act", "data-focus": "explore:copy", title: "Copy these candidates as YAML, to paste for an agent", onclick: copyCandidates,
  }, "Copy list") : "");
  status.classList.toggle("err", !!ex.error && !ex.busy);
  document.getElementById("explore-results").replaceChildren(...((ex.result && ex.result.candidates) || []).map((c) => candidateItem(c, ex.sel)));
  renderExploreActions();
}

const PLAN_ORDER = ["pending", "blocked"];
const PLAN_HEAD = { pending: "Pending", blocked: "Blocked" };
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
  if (it.decision === "blocked") return "blocked";
  if (it.default !== "run") return "waits for you";
  if (!it.deadline) return "runs now";
  const s = Math.ceil((Date.parse(it.deadline) - now) / 1000);
  if (s <= 0) return "running";
  if (s <= 99) return `runs in ${s} s`;
  return `runs in ${Math.floor(s / 60)}m ${String(s % 60).padStart(2, "0")}s`;
}

function planCount() {
  return state.plan.loaded ? planGroups(state.plan.items).reduce((n, g) => n + g.items.length, 0) : "";
}

const itemLabel = (it) => it.label || unitKey(it.unit);

async function loadPlan() {
  const view = readHash().view;
  try {
    const res = await getJSON(view === "plan" ? "/api/plan?closed=1" : "/api/plan");
    state.plan = { items: res.items || [], closed: view === "plan" ? res.closed || [] : state.plan.closed, error: (res.errors || []).join("; "), loaded: true };
  } catch (err) {
    state.plan = { ...state.plan, error: `Could not load the plan: ${err.message}`, loaded: true };
  }
}

async function actOnPlan(it, id) {
  if (!PLAN_ACTS[id]) return act({ unit: it.unit, actions: it.actions, label: itemLabel(it) }, id);
  const body = { ids: [it.id], action: PLAN_ACTS[id] };
  if (id === "guide") {
    const guidance = prompt(`Guidance for ${it.workflow} on ${itemLabel(it)}`, "");
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
    setNotice(`${body.action}: ${it.workflow} ${itemLabel(it)}`);
  } catch (err) {
    setNotice(`${body.action} ${it.workflow} ${itemLabel(it)} failed: ${err.message}`, true);
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
      title: actionTitle(st),
      "data-focus": `plan:${it.id}:${st.id}`,
      disabled: state.busy.has(it.id) || !st.enabled,
      onclick: () => actOnPlan(it, st.id),
    }, st.label || sp.label));
}

function planItem(it) {
  const ticking = it.decision === "pending" && it.default === "run" && it.deadline;
  return h("li", { class: `plan-item ${it.decision}` },
    h("div", { class: "plan-line" },
      h("span", { class: "plan-when" + (ticking ? " plan-left" : ""), "data-deadline": ticking ? it.deadline : undefined, title: ticking ? `runs at ${stamp(it.deadline)}` : undefined }, planWhen(it)),
      h("span", { class: "plan-wf" }, it.workflow),
      h("span", { class: "plan-unit" }, itemLabel(it)),
      it.mode === "observe" ? h("span", { class: "plan-mode", title: "This workflow only plans; nothing runs until it is switched to apply" }, "observe") : null),
    it.reason ? h("p", { class: "plan-reason" }, it.reason) : null,
    it.error ? h("p", { class: "plan-error" }, `attempt ${it.attempts}: ${it.error}`) : null,
    h("div", { class: "d-actions" }, ...planButtons(it)));
}

function closedItem(c) {
  return h("li", { class: "plan-item closed" },
    h("div", { class: "plan-line" },
      h("time", { datetime: c.at, title: stamp(c.at) }, age(c.at)),
      h("span", { class: "plan-unit" }, c.handle),
      h("span", { class: "plan-reason" }, c.reason)),
    c.resume ? h("div", { class: "plan-resume" },
      h("code", {}, c.resume),
      h("button", { type: "button", class: "act", "data-focus": `plan:closed:${c.handle}`, title: "Copy the command that resumes this session",
        onclick: () => navigator.clipboard.writeText(c.resume).then(() => setNotice(`copied the resume line of ${c.handle}`), (err) => setNotice(`could not copy the resume line of ${c.handle}: ${err.message}`, true)) }, "Copy")) : null);
}

function renderPlan(view) {
  const box = document.getElementById("plan");
  box.hidden = view !== "plan";
  if (box.hidden) return;
  const p = state.plan;
  const groups = planGroups(p.items);
  const parts = [];
  if (p.error) parts.push(h("p", { class: "plan-error" }, p.error));
  if (!groups.length) parts.push(h("p", { class: "empty" }, p.loaded ? "Nothing pending or blocked in the plan." : "Loading the plan."));
  for (const g of groups) {
    parts.push(h("h2", { class: "plan-head" }, `${PLAN_HEAD[g.decision]} (${g.items.length})`), h("ol", { class: "plan-items" }, ...g.items.map(planItem)));
  }
  if (p.closed.length) {
    parts.push(h("h2", { class: "plan-head" }, `Sessions closed in the last day (${p.closed.length})`), h("ol", { class: "plan-items" }, ...p.closed.map(closedItem)));
  }
  box.replaceChildren(...parts);
}

async function loadExploreRecent() {
  if (readHash().view !== "explore") return;
  try {
    state.explore.recent = await getJSON("/api/explore/recent");
  } catch {
    // Without the chips the search box still works.
  }
}

function restoreFocus() {
  if (!lastFocus) return;
  const active = document.activeElement;
  if (active && active !== document.body && active.isConnected) return;
  const el = document.querySelector(`[data-focus="${CSS.escape(lastFocus)}"]`);
  if (el && !el.disabled) el.focus({ preventScroll: true });
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
  renderRail();
  renderExplore(readHash().view);
  renderPlan(readHash().view);
  renderPluginView(readHash().view);
  renderGTD(readHash());
  renderDash(readLens());
  renderRows();
  renderDetail();
  revealOpenRow(readHash().pr);
  restoreFocus();
}

async function getJSON(url) {
  const res = await fetch(url, { headers: { Accept: "application/json", "X-Loop-API": API_VERSION }, signal: AbortSignal.timeout(REQUEST_TIMEOUT_MS) });
  if (res.status === 401) state.authLost = true;
  const body = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(apiError(body, `${url}: HTTP ${res.status}`, res.status));
  return body;
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
    state.activityError = `Could not load the recent activity: ${err.message}`;
    state.activityLoaded = true;
  }
}

async function loadDetail() {
  const { pr, entry } = readHash();
  await detailPane.load({ entry, unit: pr });
  if (!pr || entry) {
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
    state.detailError = err.message;
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
  }
  const failed = [cat, rows, health].filter((r) => r.status === "rejected");
  // A daemon that cannot open its store says why in its health's warnings.
  const storeClosed = health.status === "fulfilled" && health.value.store && health.value.store.state !== "ok";
  if (failed.length && !storeClosed) {
    state.error = `Could not reach the daemon: ${[...new Set(failed.map((r) => r.reason.message))].join("; ")}`;
  } else {
    state.error = "";
    state.authLost = false;
  }
  await Promise.all([loadActivity(), loadDetail(), loadExploreRecent(), loadPlan(), loadGTD()]);
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
  if (input && input.kind === "text" && input.required && !guidance) return refuse(`${input.label || "Guidance"} is required`);
  if (input && input.max && [...guidance].length > input.max) return refuse(`guidance is ${[...guidance].length} characters, the limit is ${input.max}`);
  if (st && st.confirm && !confirm(st.confirm)) return;
  const body = { source: "cockpit" };
  if (st && st.basis) body.basis = st.basis;
  if (input && input.kind === "duration") {
    const hint = [input.placeholder && `a duration such as ${input.placeholder}`, input.until_state && `"state" to wait until the situation changes`].filter(Boolean).join(", or ");
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
    setNotice(acceptedNote(name, labelOf(row), out.intake) || `${name} done: ${labelOf(row)}${out.output && out.output.length ? ". " + out.output.join(" ") : ""}`);
    ok = true;
    if (body.guidance) {
      delete state.guidance[key];
      for (const id of ["guidance", "explore-guidance"]) {
        const box = document.getElementById(id);
        if (box && box.dataset.key === key) box.value = "";
      }
    }
  } catch (err) {
    state.actionError = `${name} ${labelOf(row)} failed: ${err.message}`;
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
    }
    if (c.health) {
      state.health = await getJSON("/api/health");
      checkBuild(state.health);
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
    (pr && c.units.some((u) => u.toLowerCase() === pr.toLowerCase())) || (c.entries && (route.entry || pr)) ? loadDetail() : null,
    loadPlan(),
    c.entries ? loadGTD(route) : null,
  ]);
  await refreshSpecs([...state.rows, ...(state.detail ? [state.detail.row] : [])]);
  render();
}

let pending;
let queued = null;
let chain = Promise.resolve();
function scheduleLoad(c = { all: true }) {
  queued = queued || { units: [], all: false, rows: false, health: false, entries: false };
  queued.units = [...new Set([...queued.units, ...(c.units || [])])];
  queued.all = queued.all || !!c.all;
  queued.rows = queued.rows || !!c.rows;
  queued.health = queued.health || !!c.health;
  queued.entries = queued.entries || !!c.entries;
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
  scheduleLoad({ units: (c.units || []).map(unitKey), all: !!c.all, health: !!c.health, entries: !!c.counters });
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
    if (opened) scheduleLoad({ rows: true, health: true });
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
  await Promise.all([viewChanged || !state.activityLoaded ? loadActivity() : null, viewChanged ? loadExploreRecent() : null, loadDetail(), viewChanged ? loadPlan() : null, loadGTD(readHash())]);
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

// A column header cycles its sort: ascending, descending, then the default.
document.querySelector("#table thead").addEventListener("click", (e) => {
  const b = e.target.closest("button[data-sort]");
  if (!b) return;
  const { field, desc } = parseSort(readHash().s);
  const id = b.dataset.sort;
  writeHash({ s: field !== id ? id : desc ? "" : id + "-desc" });
});

setupDetailResize();
setInterval(render, 60000);
setInterval(tickCountdowns, 1000);
load();
connect();
