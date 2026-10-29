"use strict";

const DASH_TOP = 10;
const DASH_DAY = 86400000;
const DASH_HORIZON_HOURS = 72;
const DASH_LIFT_MIN = 3;
const DASH_DIM = 0.35;

const DASH_KPIS = [
  { id: "pending", label: "Pending" },
  { id: "needs-me", label: "Need me", flag: "needs-me" },
  { id: "stalled", label: "Stalled", flag: "stalled" },
  { id: "in-loop", label: "In loop", flag: "in-loop" },
  { id: "approved", label: "Approved", flag: "approved" },
  { id: "due", label: "Due in 24h", flag: "due" },
];

const DASH_NEXT = [["gate", "Round sends"], ["unmute", "Mute ends"], ["brief_retry", "Brief retry"], ["quota", "Brief quota resets"]];

const DASH_CAUSES = [
  { label: "CI failing", field: "ci", value: "failure" },
  { label: "Merge conflicts", field: "kind", value: "conflicts" },
  { label: "Changes asked", field: "kind", value: "changes" },
  { label: "Agent asks", field: "kind", value: "loop_waiting" },
  { label: "Agent stopped", field: "kind", value: "loop_stopped" },
  { label: "Round blocked", field: "kind", value: "blocked" },
  { label: "Held by the cap", field: "kind", value: "held" },
  { label: "Draft", field: "kind", value: "draft" },
];

const DASH_PANELS = [
  { id: "wait", title: "Waiting on whom", build: (c) => dashWaitOption(c.rows, c.lens, c.pal) },
  { id: "people", title: "People holding the backlog", build: (c) => dashPeopleOption(c.rows, c.lens, c.pal) },
  { id: "authors", title: "Authors", build: (c) => dashAuthorsOption(c.rows, c.lens, c.pal) },
  { id: "repos", title: "Repositories (color: share stalled)", build: (c) => dashReposOption(c.rows, c.lens, c.pal) },
  { id: "kind", title: "Flow stage", build: (c) => dashKindOption(c.rows, c.lens, c.pal) },
  { id: "shape", title: "Size × risk, and type", build: (c) => dashShapeOption(c.rows, c.lens, c.pal) },
  { id: "tags", title: "Tag concentration", build: (c) => dashTagsOption(c.rows, c.lens, c.pal, c.tk), tags: true, wide: true },
  { id: "aging", title: "Aging work in progress", build: (c) => dashAgingOption(c.rows, c.lens, c.pal, c.now, c.stalledHours), wide: true },
  { id: "next", title: "Next 72 hours", build: (c) => dashNextOption(c.rows, c.lens, c.pal, c.now, c.health), wide: true },
  { id: "opened", title: "Pull request age", build: (c) => dashOpenedOption(c.rows, c.lens, c.pal) },
  { id: "cost", title: "Load and spend", build: (c) => dashCostOption(c.rows, c.lens, c.pal) },
  { id: "blockers", title: "What blocks", build: (c) => dashBlockersOption(c.rows, c.lens, c.pal) },
  { id: "lift", title: "Tags over-represented among stalled", build: (c) => dashLiftOption(c.rows, c.lens, c.pal) },
  { id: "box", title: "Waiting time by size and risk", build: (c) => dashAgeBoxOption(c.rows, c.lens, c.pal, c.now), wide: true },
];

function dashPalette(read) {
  const tok = (name) => read(name).trim();
  return {
    me: tok("--me"), author: tok("--author"), reviewer: tok("--reviewer"), loop: tok("--loop"), nobody: tok("--none"),
    ink: tok("--ink"), muted: tok("--muted"), rule: tok("--rule"), panel: tok("--panel"), bg: tok("--bg"), sel: tok("--sel"),
    late: tok("--late"), err: tok("--err"), mine: tok("--mine-text"),
  };
}

function dashSeries(pal) {
  return [pal.me, pal.reviewer, pal.loop, pal.late, pal.author, pal.err, pal.mine];
}

function dashTheme(pal) {
  const axis = { axisLine: { lineStyle: { color: pal.rule } }, axisTick: { lineStyle: { color: pal.rule } }, axisLabel: { color: pal.muted }, splitLine: { lineStyle: { color: pal.rule } }, nameTextStyle: { color: pal.muted } };
  return {
    color: dashSeries(pal),
    backgroundColor: "transparent",
    textStyle: { color: pal.ink, fontFamily: "system-ui, -apple-system, \"Segoe UI\", Roboto, sans-serif" },
    categoryAxis: axis, valueAxis: axis, logAxis: axis, timeAxis: axis,
    tooltip: { backgroundColor: pal.panel, borderColor: pal.rule, textStyle: { color: pal.ink } },
    legend: { textStyle: { color: pal.muted } },
    markLine: { label: { color: pal.muted } },
  };
}

function dashMix(a, b, t) {
  const c = (hex, i) => parseInt(hex.slice(1 + 2 * i, 3 + 2 * i), 16);
  return "#" + [0, 1, 2].map((i) => Math.round(c(a, i) + (c(b, i) - c(a, i)) * Math.min(1, Math.max(0, t))).toString(16).padStart(2, "0")).join("");
}

function dashDim(lens, field, raw) {
  const f = lens.filters.get(field);
  return !!f && f.inc.length > 0 && !f.inc.some((v) => (fieldById(field).match ? fieldById(field).match(fold(raw), v) : fold(raw) === v));
}

function dashItem(lens, field, raw, value, color, name) {
  return { name: name || raw, value, q: [[field, raw]], itemStyle: { color, opacity: dashDim(lens, field, raw) ? DASH_DIM : 1 } };
}

// dashTally counts, per value of field, the rows that pass every filter but
// field's own, so each bar is what picking that value would show.
function dashTally(rows, lens, field, valuesOf) {
  const f = fieldById(field);
  const of = valuesOf || ((r) => f.values(r));
  const counts = new Map();
  for (const r of rows) {
    if (!passesFilters(r, lens.filters, field)) continue;
    for (const v of new Set(of(r).filter(Boolean))) counts.set(v, (counts.get(v) || 0) + 1);
  }
  return counts;
}

function dashRanked(counts, limit) {
  return [...counts.entries()].sort((a, b) => b[1] - a[1] || cmp(a[0], b[0])).slice(0, limit);
}

function dashBase() {
  return { animation: false, aria: { enabled: true }, tooltip: { trigger: "item", confine: true } };
}

function dashCountAxis(gridIndex) {
  return { type: "value", gridIndex, minInterval: 1, splitNumber: 2, axisLabel: { hideOverlap: true } };
}

function dashBars(items, pal) {
  if (!items.length) return null;
  return {
    ...dashBase(),
    grid: { left: 8, right: 32, top: 8, bottom: 8, containLabel: true },
    xAxis: dashCountAxis(0),
    yAxis: { type: "category", inverse: true, data: items.map((i) => i.name), axisLabel: { width: 150, overflow: "truncate" } },
    series: [{ type: "bar", barMaxWidth: 18, data: items, label: { show: true, position: "right", color: pal.muted }, tooltip: { formatter: "{b}: {c}" } }],
  };
}

function dashKpis(rows, lens) {
  const all = rows.filter((r) => passesFilters(r, lens.filters)).length;
  const flagged = rows.filter((r) => passesFilters(r, lens.filters, "is")).map(rowFlags);
  const on = (lens.filters.get("is") || { inc: [] }).inc;
  return DASH_KPIS.map((k) => ({ ...k, n: k.flag ? flagged.filter((fs) => fs.includes(k.flag)).length : all, on: !!k.flag && on.includes(k.flag) }));
}

function dashWaitOption(rows, lens, pal) {
  const counts = dashTally(rows, lens, catalog.strip);
  return dashBars(fieldRank(catalog.strip).filter((k) => counts.get(k)).map((k) => dashItem(lens, catalog.strip, k, counts.get(k), pal[k], fieldShow(catalog.strip, k))), pal);
}

function dashPeopleOption(rows, lens, pal) {
  const role = new Map();
  for (const r of rows) {
    for (const who of waitingWho(r)) {
      if (!role.has(who)) role.set(who, who === "me" ? "me" : who === pl(r).author ? "author" : "reviewer");
    }
  }
  const counts = dashTally(rows, lens, "waits-on", waitingWho);
  return dashBars(dashRanked(counts, DASH_TOP).map(([who, n]) => dashItem(lens, "waits-on", who, n, pal[role.get(who) || "reviewer"])), pal);
}

function dashAuthorsOption(rows, lens, pal) {
  const f = fieldById("author");
  const counts = dashTally(rows, lens, "author", (r) => [rowKey(f, r)]);
  return dashBars(dashRanked(counts, DASH_TOP).map(([a, n]) => dashItem(lens, "author", a, n, a === "me" ? pal.me : pal.author)), pal);
}

function dashReposOption(rows, lens, pal) {
  const orgs = new Map();
  for (const r of rows) {
    if (!passesFilters(r, lens.filters, "repo")) continue;
    const repo = facetValues(r, "repo")[0];
    if (!repo) continue;
    const org = repo.split("/")[0];
    if (!orgs.has(org)) orgs.set(org, new Map());
    const repos = orgs.get(org);
    const c = repos.get(repo) || { n: 0, stalled: 0 };
    c.n++;
    if (isStalled(r)) c.stalled++;
    repos.set(repo, c);
  }
  if (!orgs.size) return null;
  const data = [...orgs.entries()].sort(cmp).map(([org, repos]) => ({
    name: org,
    children: [...repos.entries()].sort(cmp).map(([repo, c]) => ({
      ...dashItem(lens, "repo", repo, c.n, dashMix(pal.panel, pal.late, 0.6 * c.stalled / c.n), repo.slice(org.length + 1)),
      tooltip: { formatter: `${dashEsc(repo)}: ${c.n} pending, ${c.stalled} stalled` },
    })),
  }));
  return {
    ...dashBase(),
    series: [{
      type: "treemap", roam: false, nodeClick: false, breadcrumb: { show: false }, top: 4, left: 4, right: 4, bottom: 4,
      label: { show: true, formatter: "{b}\n{c}", color: pal.ink },
      upperLabel: { show: true, height: 18, color: pal.ink },
      levels: [{ itemStyle: { borderColor: pal.panel, borderWidth: 2, gapWidth: 2 } }, { itemStyle: { borderColor: pal.panel, gapWidth: 1 } }],
      data,
    }],
  };
}

function dashKindOption(rows, lens, pal) {
  const counts = dashTally(rows, lens, "kind");
  return dashBars(catalog.kindOrder.filter((k) => counts.get(k)).map((k) => dashItem(lens, "kind", k, counts.get(k), pal.author, kindLabel(k))), pal);
}

// dashTagsOption is a treemap of tag keys and their values, or with key the
// ranked values of that key alone.
function dashTagsOption(rows, lens, pal, key) {
  const counts = dashTally(rows, lens, "tag");
  if (key) {
    const of = new Map([...counts].filter(([t]) => t.startsWith(key + ":")));
    return dashBars(dashRanked(of, 15).map(([t, n]) => dashItem(lens, "tag", t, n, pal.me, t.slice(key.length + 1))), pal);
  }
  const keys = new Map();
  for (const [t, n] of counts) {
    const k = t.split(":")[0];
    if (!keys.has(k)) keys.set(k, []);
    keys.get(k).push([t, n]);
  }
  if (!keys.size) return null;
  const colors = dashSeries(pal);
  const data = [...keys.entries()].sort(cmp).map(([k, ts], i) => ({
    name: k, q: [["tag", k]],
    children: ts.sort((a, b) => b[1] - a[1] || cmp(a[0], b[0])).map(([t, n]) => ({
      ...dashItem(lens, "tag", t, n, colors[i % colors.length], t.slice(k.length + 1)),
      tooltip: { formatter: `${dashEsc(t)}: ${n}` },
    })),
  }));
  return {
    ...dashBase(),
    series: [{
      type: "treemap", roam: false, nodeClick: false, breadcrumb: { show: false }, top: 4, left: 4, right: 4, bottom: 4,
      label: { show: true, formatter: "{b}\n{c}", color: pal.panel },
      upperLabel: { show: true, height: 18, color: pal.ink },
      levels: [{ itemStyle: { borderColor: pal.panel, borderWidth: 2, gapWidth: 2 } }, { itemStyle: { borderColor: pal.panel, gapWidth: 1 } }],
      data,
    }],
  };
}

function dashShapeOption(rows, lens, pal) {
  const sizes = [...SIZES, "none"];
  const risks = [...RISKS, "none"];
  const cells = new Map();
  for (const r of rows) {
    if (!passesFilters(r, lens.filters, ["size", "risk"])) continue;
    const k = `${fieldById("size").values(r)[0]}|${fieldById("risk").values(r)[0]}`;
    cells.set(k, (cells.get(k) || 0) + 1);
  }
  const types = dashTally(rows, lens, "type");
  if (!cells.size) return null;
  const max = Math.max(...cells.values());
  const data = [...cells.entries()].map(([k, n]) => {
    const [s, rk] = k.split("|");
    const dim = dashDim(lens, "size", s) || dashDim(lens, "risk", rk);
    return { value: [sizes.indexOf(s), risks.indexOf(rk), n], q: [["size", s], ["risk", rk]], itemStyle: { opacity: dim ? DASH_DIM : 1 }, tooltip: { formatter: dashEsc(`size ${s === "none" ? "unknown" : s.toUpperCase()}, risk ${rk === "none" ? "unknown" : rk}: ${n}`) } };
  });
  const typeItems = dashRanked(types, 8).map(([t, n]) => dashItem(lens, "type", t, n, pal.reviewer, t === "none" ? "no brief" : t));
  return {
    ...dashBase(),
    grid: [{ left: 8, top: 8, bottom: 8, width: "48%", containLabel: true }, { left: "60%", right: 32, top: 8, bottom: 8, containLabel: true }],
    xAxis: [
      { type: "category", gridIndex: 0, data: sizes.map((s) => (s === "none" ? "?" : s.toUpperCase())), splitArea: { show: false } },
      dashCountAxis(1),
    ],
    yAxis: [
      { type: "category", gridIndex: 0, data: risks.map((r) => (r === "none" ? "?" : r)) },
      { type: "category", gridIndex: 1, inverse: true, data: typeItems.map((i) => i.name), axisLabel: { width: 80, overflow: "truncate" } },
    ],
    visualMap: { show: false, min: 0, max, seriesIndex: 0, inRange: { color: [pal.sel, dashMix(pal.sel, pal.me, 0.6)] } },
    series: [
      { type: "heatmap", xAxisIndex: 0, yAxisIndex: 0, data, label: { show: true, color: pal.ink } },
      { type: "bar", xAxisIndex: 1, yAxisIndex: 1, barMaxWidth: 16, data: typeItems, label: { show: true, position: "right", color: pal.muted }, tooltip: { formatter: "{b}: {c}" } },
    ],
  };
}

// dashWorkingMs mirrors workday.Between: the time from from to to
// in UTC, Saturdays and Sundays left out.
function dashWorkingMs(from, to) {
  let total = 0;
  for (let cur = from; cur < to;) {
    const d = new Date(cur);
    const next = Math.min(to, Date.UTC(d.getUTCFullYear(), d.getUTCMonth(), d.getUTCDate() + 1));
    if (d.getUTCDay() !== 0 && d.getUTCDay() !== 6) total += next - cur;
    cur = next;
  }
  return total;
}

function dashDays(r, now) {
  return Math.max(1 / 24, dashWorkingMs(sinceTime(r), now) / DASH_DAY);
}

function dashQuantile(sorted, p) {
  if (!sorted.length) return 0;
  const i = (sorted.length - 1) * p;
  const lo = Math.floor(i);
  return sorted[lo] + (sorted[Math.ceil(i)] - sorted[lo]) * (i - lo);
}

function dashRound(x) {
  return Math.round(x * 100) / 100;
}

// dashAgingOption plots each pull request's wait by its stage; a hidden
// value axis under the stage axis spreads the dots of one stage apart.
function dashAgingOption(rows, lens, pal, now, stalledHours) {
  const shown = rows.filter((r) => passesFilters(r, lens.filters) && isSet(sm(r).since));
  if (!shown.length) return null;
  const kinds = catalog.kindOrder.filter((k) => shown.some((r) => r.kind === k));
  const points = shown.map((r) => {
    const n = [...keyOf(r)].reduce((h, c) => (h * 31 + c.charCodeAt(0)) % 1000003, 0);
    const days = dashDays(r, now);
    return {
      value: [kinds.indexOf(r.kind) + (((n * 0.618034) % 1) - 0.5) * 0.6, dashRound(days)],
      pr: keyOf(r), name: labelOf(r),
      itemStyle: { color: pal[waitCat(r)] },
      tooltip: { formatter: `${dashEsc(labelOf(r))} ${dashEsc(r.title || "")}: waits ${days < 1 ? Math.round(days * 24) + " working hours" : dashRound(days) + " working days"} on ${waitCat(r)}` },
    };
  });
  const ages = shown.map((r) => dashDays(r, now)).sort((a, b) => a - b);
  const lines = [{ name: "p50", yAxis: dashRound(dashQuantile(ages, 0.5)) }, { name: "p85", yAxis: dashRound(dashQuantile(ages, 0.85)) }];
  if (stalledHours > 0) lines.push({ name: "stalled", yAxis: dashRound(stalledHours / 24), lineStyle: { color: pal.late } });
  return {
    ...dashBase(),
    grid: { left: 8, right: 48, top: 16, bottom: 8, containLabel: true },
    xAxis: [
      { type: "category", data: kinds.map((k) => kindLabel(k)), axisLabel: { interval: 0, rotate: kinds.length > 6 ? 30 : 0 } },
      { type: "value", min: -0.5, max: kinds.length - 0.5, show: false },
    ],
    yAxis: { type: "log", logBase: 10, name: "working days", min: 0.01 },
    series: [{
      type: "scatter", xAxisIndex: 1, symbolSize: 9, data: points,
      markLine: { silent: true, symbol: "none", lineStyle: { type: "dashed", color: pal.muted }, label: { formatter: "{b}", position: "end" }, data: lines },
    }],
  };
}

function dashNextOption(rows, lens, pal, now, health) {
  const end = now + DASH_HORIZON_HOURS * 3600000;
  const lanes = DASH_NEXT.map(([id]) => id);
  const within = (at) => {
    const t = Date.parse(at);
    return t >= now && t <= end ? t : null;
  };
  const data = [];
  for (const r of rows) {
    if (!passesFilters(r, lens.filters)) continue;
    for (const e of upcoming(r)) {
      const t = within(e.at);
      if (t === null || !lanes.includes(e.what)) continue;
      data.push({ value: [t, lanes.indexOf(e.what)], pr: keyOf(r), name: labelOf(r), itemStyle: { color: pal[waitCat(r)] }, tooltip: { formatter: `${dashEsc(labelOf(r))}: ${DASH_NEXT[lanes.indexOf(e.what)][1].toLowerCase()} at ${stamp(new Date(t).toISOString())}` } });
    }
  }
  const quota = health && health.brief && within(health.brief.paused_until);
  if (quota) data.push({ value: [quota, lanes.indexOf("quota")], name: "brief quota", itemStyle: { color: pal.late }, tooltip: { formatter: "Briefs resume" } });
  if (!data.length) return null;
  return {
    ...dashBase(),
    grid: { left: 8, right: 24, top: 8, bottom: 8, containLabel: true },
    xAxis: { type: "time", min: now, max: end },
    yAxis: { type: "category", data: DASH_NEXT.map(([, label]) => label) },
    series: [{ type: "scatter", symbolSize: 11, data }],
  };
}

function dashOpenedOption(rows, lens, pal) {
  const counts = dashTally(rows, lens, "opened");
  const items = fieldRank("opened").filter((k) => counts.get(k)).map((k) => dashItem(lens, "opened", k, counts.get(k), pal.me, fieldShow("opened", k)));
  if (!items.length) return null;
  return {
    ...dashBase(),
    grid: { left: 8, right: 16, top: 16, bottom: 8, containLabel: true },
    xAxis: { type: "category", data: items.map((i) => i.name) },
    yAxis: { type: "value", minInterval: 1 },
    series: [{ type: "bar", barMaxWidth: 36, data: items, label: { show: true, position: "top", color: pal.muted }, tooltip: { formatter: "opened {b} ago: {c}" } }],
  };
}

function dashCostOption(rows, lens, pal) {
  const files = new Map();
  for (const r of rows) {
    const n = Number(((pl(r).facts || {}).changed_files)) || 0;
    if (!n || !passesFilters(r, lens.filters, catalog.strip)) continue;
    for (const c of facetValues(r, catalog.strip)) files.set(c, (files.get(c) || 0) + n);
  }
  const turns = (r) => ((pl(r).facts || {}).turns || {});
  const shown = rows.filter((r) => passesFilters(r, lens.filters));
  const metric = shown.some((r) => (turns(r).cost_usd || 0) > 0) ? "cost_usd" : "tokens";
  const spend = shown.filter((r) => (turns(r)[metric] || 0) > 0)
    .sort((a, b) => turns(b)[metric] - turns(a)[metric] || cmp(labelOf(a), labelOf(b))).slice(0, DASH_TOP);
  if (!files.size && !spend.length) return null;
  const load = fieldRank(catalog.strip).filter((k) => files.get(k)).map((k) => ({ ...dashItem(lens, catalog.strip, k, files.get(k), pal[k], fieldShow(catalog.strip, k)), tooltip: { formatter: `${fieldShow(catalog.strip, k)}: ${files.get(k)} changed files` } }));
  const cost = spend.map((r) => {
    const t = turns(r);
    return { name: labelOf(r), value: metric === "cost_usd" ? dashRound(t.cost_usd) : t.tokens, pr: keyOf(r), itemStyle: { color: pal[waitCat(r)] },
      tooltip: { formatter: `${dashEsc(labelOf(r))}: ${metric === "cost_usd" ? `US$ ${t.cost_usd.toFixed(2)}, ` : ""}${t.tokens || 0} tokens over ${t.count || 0} turns` } };
  });
  const spendTitle = metric === "cost_usd" ? "US$ spent per pull request" : "Tokens per pull request\nno US$ cost recorded";
  const top = metric === "cost_usd" ? 22 : 36;
  return {
    ...dashBase(),
    title: [{ text: "Changed files by whom it waits on", left: 8, top: 0, textStyle: { fontSize: 11, fontWeight: "normal", color: pal.muted } },
      { text: spendTitle, left: "52%", top: 0, textStyle: { fontSize: 11, fontWeight: "normal", color: pal.muted } }],
    grid: [{ left: 8, top, bottom: 8, width: "40%", containLabel: true }, { left: "52%", right: 40, top, bottom: 8, containLabel: true }],
    xAxis: [dashCountAxis(0), { ...dashCountAxis(1), minInterval: 0, axisLabel: { hideOverlap: true, formatter: dashCompact } }],
    yAxis: [
      { type: "category", gridIndex: 0, inverse: true, data: load.map((i) => i.name), axisLabel: { width: 90, overflow: "truncate" } },
      { type: "category", gridIndex: 1, inverse: true, data: cost.map((i) => i.name), axisLabel: { width: 110, overflow: "truncate" } },
    ],
    series: [
      { type: "bar", xAxisIndex: 0, yAxisIndex: 0, barMaxWidth: 16, data: load, label: { show: true, position: "right", color: pal.muted } },
      { type: "bar", xAxisIndex: 1, yAxisIndex: 1, barMaxWidth: 16, data: cost, label: { show: true, position: "right", color: pal.muted, formatter: (p) => dashCompact(p.value) } },
    ],
  };
}

function dashCompact(n) {
  return n >= 1e6 ? dashRound(n / 1e6) + "M" : n >= 1e4 ? Math.round(n / 1e3) + "k" : String(n);
}

function dashHas(f, r, value) {
  const v = fold(value);
  return rowValues(f, r).some((x) => (f.match ? f.match(x, v) : x === v));
}

function dashBlockersOption(rows, lens, pal) {
  const causes = DASH_CAUSES.map((c) => {
    const f = fieldById(c.field);
    const n = rows.filter((r) => passesFilters(r, lens.filters, c.field) && dashHas(f, r, c.value)).length;
    return n ? dashItem(lens, c.field, c.value, n, c.field === "ci" ? pal.err : pal.late, c.label) : null;
  }).filter(Boolean);
  const checks = new Map();
  for (const r of rows) {
    if (!passesFilters(r, lens.filters, "ci")) continue;
    for (const name of new Set(((pl(r).facts || {}).checks || {}).failing || [])) checks.set(name, (checks.get(name) || 0) + 1);
  }
  const failing = dashRanked(checks, 8).map(([name, n]) => dashItem(lens, "ci", name, n, pal.err));
  if (!causes.length && !failing.length) return null;
  return {
    ...dashBase(),
    title: [{ text: "Cause", left: 8, top: 0, textStyle: { fontSize: 11, fontWeight: "normal", color: pal.muted } },
      { text: "Failing checks", left: "52%", top: 0, textStyle: { fontSize: 11, fontWeight: "normal", color: pal.muted } }],
    grid: [{ left: 8, top: 22, bottom: 8, width: "40%", containLabel: true }, { left: "52%", right: 32, top: 22, bottom: 8, containLabel: true }],
    xAxis: [dashCountAxis(0), dashCountAxis(1)],
    yAxis: [
      { type: "category", gridIndex: 0, inverse: true, data: causes.map((i) => i.name), axisLabel: { width: 100, overflow: "truncate" } },
      { type: "category", gridIndex: 1, inverse: true, data: failing.map((i) => i.name), axisLabel: { width: 110, overflow: "truncate" } },
    ],
    series: [
      { type: "bar", xAxisIndex: 0, yAxisIndex: 0, barMaxWidth: 16, data: causes, label: { show: true, position: "right", color: pal.muted }, tooltip: { formatter: "{b}: {c}" } },
      { type: "bar", xAxisIndex: 1, yAxisIndex: 1, barMaxWidth: 16, data: failing, label: { show: true, position: "right", color: pal.muted }, tooltip: { formatter: "{b}: {c}" } },
    ],
  };
}

// dashLift compares each tag's share among the stalled with its share of
// the backlog: +100% means twice as common among the stalled.
function dashLift(rows, lens) {
  const base = rows.filter((r) => passesFilters(r, lens.filters, ["tag", "is"]));
  const stalled = base.filter(isStalled);
  if (!stalled.length || stalled.length === base.length) return [];
  const count = (rs) => {
    const m = new Map();
    for (const r of rs) for (const t of new Set(facetValues(r, "tag"))) m.set(t, (m.get(t) || 0) + 1);
    return m;
  };
  const all = count(base);
  const among = count(stalled);
  return [...all.entries()].filter(([, n]) => n >= DASH_LIFT_MIN).map(([tag, n]) => {
    const s = among.get(tag) || 0;
    return { tag, n, stalled: s, lift: Math.round(((s / stalled.length) / (n / base.length) - 1) * 100) };
  }).sort((a, b) => b.lift - a.lift || cmp(a.tag, b.tag));
}

// dashEsc escapes text for an echarts string formatter, which renders HTML.
function dashEsc(s) {
  return String(s).replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c]);
}

function dashLiftOption(rows, lens, pal) {
  const lifts = dashLift(rows, lens);
  if (!lifts.length) return null;
  const shown = lifts.length > 12 ? [...lifts.slice(0, 8), ...lifts.slice(-4)] : lifts;
  const items = shown.map((l) => ({
    name: l.tag, value: l.lift, q: [["tag", l.tag], ["is", "stalled"]],
    itemStyle: { color: l.lift > 0 ? pal.late : pal.loop, opacity: dashDim(lens, "tag", l.tag) ? DASH_DIM : 1 },
    tooltip: { formatter: `${dashEsc(l.tag)}: ${l.stalled} of ${l.n} stalled, ${l.lift > 0 ? "+" : ""}${l.lift}% against the backlog` },
  }));
  return {
    ...dashBase(),
    grid: { left: 8, right: 40, top: 8, bottom: 8, containLabel: true },
    xAxis: { type: "value", splitNumber: 3, axisLabel: { formatter: "{value}%", hideOverlap: true } },
    yAxis: { type: "category", inverse: true, data: items.map((i) => i.name), axisLabel: { width: 150, overflow: "truncate" } },
    series: [{ type: "bar", barMaxWidth: 16, data: items, label: { show: true, position: "right", formatter: "{c}%", color: pal.muted } }],
  };
}

function dashAgeBoxOption(rows, lens, pal, now) {
  const boxes = [];
  const add = (field, keys, label, color) => {
    for (const k of keys) {
      const ages = rows.filter((r) => passesFilters(r, lens.filters, field) && isSet(sm(r).since) && fieldById(field).values(r)[0] === k)
        .map((r) => dashDays(r, now)).sort((a, b) => a - b);
      if (!ages.length) continue;
      const v = [ages[0], dashQuantile(ages, 0.25), dashQuantile(ages, 0.5), dashQuantile(ages, 0.75), ages[ages.length - 1]].map(dashRound);
      boxes.push({
        name: label(k), value: v, q: [[field, k]],
        itemStyle: { color: pal.panel, borderColor: color, opacity: dashDim(lens, field, k) ? DASH_DIM : 1 },
        tooltip: { formatter: `${dashEsc(label(k))}: ${ages.length} pull requests, median ${v[2]} working days, middle half ${v[1]}-${v[3]}` },
      });
    }
  };
  add("size", SIZES, (k) => `size ${k.toUpperCase()}`, pal.me);
  add("risk", RISKS, (k) => `${k} risk`, pal.reviewer);
  if (!boxes.length) return null;
  return {
    ...dashBase(),
    grid: { left: 8, right: 16, top: 16, bottom: 8, containLabel: true },
    xAxis: { type: "category", data: boxes.map((b) => b.name), axisLabel: { interval: 0, rotate: boxes.length > 6 ? 30 : 0 } },
    yAxis: { type: "value", name: "working days" },
    series: [{ type: "boxplot", data: boxes }],
  };
}

// dashClickQuery folds a click into the query: the pairs it names go in,
// or out when the query holds them all; neg excludes instead.
function dashClickQuery(q, pairs, neg) {
  const has = ([field, raw]) => qualifiers(q).some((x) => x.field === field && x.value === normValue(field, raw) && x.neg === !!neg);
  const todo = pairs.every(has) ? pairs : pairs.filter((p) => !has(p));
  return todo.reduce((acc, [field, raw]) => toggledQualifier(acc, field, raw, neg), q);
}

const dash = { charts: new Map(), theme: "", built: false };

function dashThemeName() {
  return matchMedia("(prefers-color-scheme: dark)").matches ? "loop-dark" : "loop-light";
}

function dashDispose() {
  for (const { chart, observer } of dash.charts.values()) {
    observer.disconnect();
    chart.dispose();
  }
  dash.charts.clear();
  dash.built = false;
}

function dashClicked(p) {
  const d = p.data || {};
  if (d.pr) {
    writeHash({ pr: d.pr });
    return;
  }
  if (!d.q) return;
  const e = p.event && p.event.event;
  lastFocus = null;
  writeHash({ q: dashClickQuery(readHash().q, d.q, !!(e && e.altKey)) });
}

function dashChart(id, el) {
  let c = dash.charts.get(id);
  if (!c) {
    const chart = echarts.init(el, dash.theme, { renderer: "svg" });
    chart.on("click", dashClicked);
    const observer = new ResizeObserver(() => chart.resize());
    observer.observe(el);
    c = { chart, observer };
    dash.charts.set(id, c);
  }
  return c.chart;
}

function dashBuild(box) {
  dashDispose();
  box.replaceChildren(
    h("div", { class: "dash-head" },
      h("ul", { id: "dash-kpis", class: "kpis", "aria-label": "Counts" }),
      h("button", { id: "dash-done", type: "button", class: "act", "data-focus": "dash:done", onclick: () => writeHash({ done: !readHash().done }) }, "Include done"),
      h("span", { class: "dash-hint" }, "Click a chart to filter; Alt+click to exclude.")),
    h("div", { class: "dash-grid" }, DASH_PANELS.map((p) => h("section", { class: "panel" + (p.wide ? " wide" : ""), "aria-label": p.title },
      h("h3", { class: "panel-head" }, h("span", {}, p.title), p.tags ? h("select", {
        id: "dash-tk", "data-focus": "dash:tk", "aria-label": "Tag key",
        onchange: (e) => writeHash({ tk: e.target.value }),
      }) : null),
      h("div", { id: `dash-${p.id}`, class: "chart", role: "img", "aria-label": p.title }),
      h("p", { id: `dash-${p.id}-none`, class: "nodata", hidden: true }, "No data for this filter yet.")))));
  dash.built = true;
}

function renderDashKpis(lens, rows) {
  document.getElementById("dash-kpis").replaceChildren(...dashKpis(rows, lens).map((k) => h("li", {},
    k.flag ? h("button", {
      type: "button", class: "kpi" + (k.id === "stalled" ? " late" : ""), "aria-pressed": String(k.on), "data-focus": `dash:kpi:${k.id}`,
      title: `Filter is:${k.flag}; Alt+click excludes`,
      onclick: (e) => writeHash({ q: dashClickQuery(readHash().q, [["is", k.flag]], e.altKey) }),
    }, h("span", { class: "n" }, k.n), h("span", { class: "label" }, k.label))
      : h("span", { class: "kpi" }, h("span", { class: "n" }, k.n), h("span", { class: "label" }, k.label)))));
}

function renderDashTagKeys(lens, rows) {
  const sel = document.getElementById("dash-tk");
  const keys = tagKeys(rows);
  const sig = JSON.stringify([keys, lens.tk]);
  if (sel.dataset.sig === sig) return;
  sel.dataset.sig = sig;
  sel.replaceChildren(h("option", { value: "", selected: !lens.tk }, "All keys"), ...keys.map((k) => h("option", { value: k, selected: k === lens.tk }, k)));
}

function renderDash(lens) {
  const box = document.getElementById("dash");
  box.hidden = lens.view !== "dash";
  if (box.hidden) {
    if (dash.built) {
      dashDispose();
      box.replaceChildren();
    }
    return;
  }
  if (typeof echarts === "undefined") {
    box.replaceChildren(h("p", { class: "empty" }, "The charts library did not load; reload the page."));
    return;
  }
  const theme = dashThemeName();
  if (theme !== dash.theme || !dash.built) {
    const pal = dashPalette((name) => getComputedStyle(document.documentElement).getPropertyValue(name));
    echarts.registerTheme(theme, dashTheme(pal));
    dash.theme = theme;
    dash.pal = pal;
    dashBuild(box);
  }
  const rows = state.rows.filter((r) => inView(r, "dash", lens.done) && matches(r, lens.ts));
  const done = document.getElementById("dash-done");
  done.setAttribute("aria-pressed", String(lens.done));
  renderDashKpis(lens, rows);
  renderDashTagKeys(lens, rows);
  const ctx = { rows, lens, pal: dash.pal, now: Date.now(), health: state.health, tk: lens.tk, stalledHours: (state.health && state.health.stalled_review_hours) || 0 };
  for (const p of DASH_PANELS) {
    const el = document.getElementById(`dash-${p.id}`);
    const opt = p.build(ctx);
    document.getElementById(`dash-${p.id}-none`).hidden = !!opt;
    el.hidden = !opt;
    if (opt) dashChart(p.id, el).setOption(opt, { notMerge: true });
    else if (dash.charts.has(p.id)) dash.charts.get(p.id).chart.clear();
  }
}

matchMedia("(prefers-color-scheme: dark)").addEventListener("change", () => {
  dash.theme = "";
  if (!document.getElementById("dash").hidden) renderDash(readLens());
});
