import { catalog } from "../../state/store.svelte";
import { kindLabel } from "../../lib/catalog";
import { fmtNumber, t, tn } from "../../lib/i18n.svelte";
import {
  facetValues, fieldById, fieldRank, fieldShow, isStalled, itemValues, normValue, passesFilters, pl, qualifiers, rowFlags, sinceTime, sm, stripColor,
  toggledQualifier, viewStrip,
} from "../../lib/lens";
import { cmp, fold, isSet, keyOf, labelOf, type J } from "../../lib/util";
import { clock } from "../../state/clock.svelte";
import { RISKS, SIZES, stamp, upcoming, waitCat, waitColor, waitingWho } from "./facts";

const fld = (id: string): J => fieldById(id);

// The backlog dashboard's charts as ECharts options over the rows of a lens: each panel counts what picking one
// of its values would show, so it leaves its own field's filter out, and a datum names the filter a click on it
// toggles.

export const DASH_TOP = 10;
export const DASH_DAY = 86400000;
export const DASH_HORIZON_HOURS = 72;
export const DASH_LIFT_MIN = 3;
export const DASH_DIM = 0.35;

export const DASH_KPIS = [
  { id: "pending", get label() { return t("Pending"); } },
  { id: "needs-me", get label() { return t("Need me"); }, flag: "needs-me" },
  { id: "stalled", get label() { return t("Stalled"); }, flag: "stalled" },
  { id: "in-loop", get label() { return t("In loop"); }, flag: "in-loop" },
  { id: "approved", get label() { return t("Approved"); }, flag: "approved" },
  { id: "due", get label() { return t("Due in 24h"); }, flag: "due" },
];

export const DASH_NEXT = [
  { id: "gate", get label() { return t("Round sends"); } },
  { id: "unmute", get label() { return t("Mute ends"); } },
  { id: "brief_retry", get label() { return t("Brief retry"); } },
  { id: "quota", get label() { return t("Brief quota resets"); } },
];

export const DASH_CAUSES = [
  { get label() { return t("CI failing"); }, field: "ci", value: "failure" },
  { get label() { return t("Merge conflicts"); }, field: "kind", value: "conflicts" },
  { get label() { return t("Changes asked"); }, field: "kind", value: "changes" },
  { get label() { return t("Agent asks"); }, field: "kind", value: "loop_waiting" },
  { get label() { return t("Agent stopped"); }, field: "kind", value: "loop_stopped" },
  { get label() { return t("Round blocked"); }, field: "kind", value: "blocked" },
  { get label() { return t("Held by the cap"); }, field: "kind", value: "held" },
  { get label() { return t("Draft"); }, field: "kind", value: "draft" },
];

export const DASH_PANELS = [
  { id: "wait", get title() { return t("Waiting on whom"); }, build: (c: J) => dashWaitOption(c.rows, c.lens, c.pal) },
  { id: "people", get title() { return t("People holding the backlog"); }, build: (c: J) => dashPeopleOption(c.rows, c.lens, c.pal) },
  { id: "authors", get title() { return t("Authors"); }, build: (c: J) => dashAuthorsOption(c.rows, c.lens, c.pal) },
  { id: "repos", get title() { return t("Repositories (color: share stalled)"); }, build: (c: J) => dashReposOption(c.rows, c.lens, c.pal) },
  { id: "kind", get title() { return t("Flow stage"); }, build: (c: J) => dashKindOption(c.rows, c.lens, c.pal) },
  { id: "shape", get title() { return t("Size × risk, and type"); }, build: (c: J) => dashShapeOption(c.rows, c.lens, c.pal) },
  { id: "tags", get title() { return t("Tag concentration"); }, build: (c: J) => dashTagsOption(c.rows, c.lens, c.pal, c.tk), tags: true, wide: true },
  { id: "aging", get title() { return t("Aging work in progress"); }, build: (c: J) => dashAgingOption(c.rows, c.lens, c.pal, c.now, c.stalledHours), wide: true },
  { id: "next", get title() { return t("Next 72 hours"); }, build: (c: J) => dashNextOption(c.rows, c.lens, c.pal, c.now, c.health), wide: true },
  { id: "opened", get title() { return t("Pull request age"); }, build: (c: J) => dashOpenedOption(c.rows, c.lens, c.pal) },
  { id: "cost", get title() { return t("Load and spend"); }, build: (c: J) => dashCostOption(c.rows, c.lens, c.pal) },
  { id: "blockers", get title() { return t("What blocks"); }, build: (c: J) => dashBlockersOption(c.rows, c.lens, c.pal) },
  { id: "lift", get title() { return t("Tags over-represented among stalled"); }, build: (c: J) => dashLiftOption(c.rows, c.lens, c.pal) },
  { id: "box", get title() { return t("Waiting time by size and risk"); }, build: (c: J) => dashAgeBoxOption(c.rows, c.lens, c.pal, c.now), wide: true },
];

export function dashPalette(read: (name: string) => string): Record<string, string> {
  const tok = (name: J) => read(name).trim();
  return {
    me: tok("--me"), author: tok("--author"), reviewer: tok("--reviewer"), loop: tok("--loop"), nobody: tok("--none"),
    ink: tok("--ink"), muted: tok("--muted"), rule: tok("--rule"), panel: tok("--panel"), bg: tok("--bg"), sel: tok("--sel"),
    late: tok("--late"), err: tok("--err"), mine: tok("--mine-text"),
  };
}

export function dashSeries(pal: J) {
  return [pal.me, pal.reviewer, pal.loop, pal.late, pal.author, pal.err, pal.mine];
}

export function dashTheme(pal: J, font?: J) {
  const axis = { axisLine: { lineStyle: { color: pal.rule } }, axisTick: { lineStyle: { color: pal.rule } }, axisLabel: { color: pal.muted }, splitLine: { lineStyle: { color: pal.rule } }, nameTextStyle: { color: pal.muted } };
  return {
    color: dashSeries(pal),
    backgroundColor: "transparent",
    textStyle: { color: pal.ink, fontFamily: font },
    categoryAxis: axis, valueAxis: axis, logAxis: axis, timeAxis: axis,
    tooltip: { backgroundColor: pal.panel, borderColor: pal.rule, textStyle: { color: pal.ink } },
    legend: { textStyle: { color: pal.muted } },
    markLine: { label: { color: pal.muted } },
  };
}

export function dashMix(a: J, b: J, f: J) {
  const c = (hex: J, i: J) => parseInt(hex.slice(1 + 2 * i, 3 + 2 * i), 16);
  return "#" + [0, 1, 2].map((i: J) => Math.round(c(a, i) + (c(b, i) - c(a, i)) * Math.min(1, Math.max(0, f))).toString(16).padStart(2, "0")).join("");
}

export function dashDim(lens: J, field: J, raw: J) {
  const f = lens.filters.get(field);
  return !!f && f.inc.length > 0 && !f.inc.some((v: J) => (fld(field).match ? fld(field).match(fold(raw), v) : fold(raw) === v));
}

export function dashItem(lens: J, field: J, raw: J, value: J, color: J, name?: J) {
  return { name: name || raw, value, q: [[field, raw]], itemStyle: { color, opacity: dashDim(lens, field, raw) ? DASH_DIM : 1 } };
}

// dashTally counts, per value of field, the rows that pass every filter but
// field's own, so each bar is what picking that value would show.
export function dashTally(rows: J, lens: J, field: J, valuesOf?: J) {
  const f = fld(field);
  const of = valuesOf || ((r: J) => f.values(r));
  const counts = new Map();
  for (const r of rows) {
    if (!passesFilters(r, lens.filters, field)) continue;
    for (const v of new Set(of(r).filter(Boolean))) counts.set(v, (counts.get(v) || 0) + 1);
  }
  return counts;
}

export function dashRanked(counts: J, limit: J) {
  return [...counts.entries()].sort((a: J, b: J) => b[1] - a[1] || cmp(a[0], b[0])).slice(0, limit);
}

export function dashBase() {
  return { animation: false, aria: { enabled: true }, tooltip: { trigger: "item", confine: true } };
}

export function dashCountAxis(gridIndex: J) {
  return { type: "value", gridIndex, minInterval: 1, splitNumber: 2, axisLabel: { hideOverlap: true } };
}

export function dashBars(items: J, pal: J) {
  if (!items.length) return null;
  return {
    ...dashBase(),
    grid: { left: 8, right: 32, top: 8, bottom: 8, containLabel: true },
    xAxis: dashCountAxis(0),
    yAxis: { type: "category", inverse: true, data: items.map((i: J) => i.name), axisLabel: { width: 150, overflow: "truncate" } },
    series: [{ type: "bar", barMaxWidth: 18, data: items, label: { show: true, position: "right", color: pal.muted }, tooltip: { formatter: "{b}: {c}" } }],
  };
}

export function dashKpis(rows: J, lens: J) {
  const all = rows.filter((r: J) => passesFilters(r, lens.filters)).length;
  const flagged = rows.filter((r: J) => passesFilters(r, lens.filters, "is")).map(rowFlags);
  const on = (lens.filters.get("is") || { inc: [] }).inc;
  return DASH_KPIS.map((k: J) => ({ ...k, n: k.flag ? flagged.filter((fs: J) => fs.includes(k.flag)).length : all, on: !!k.flag && on.includes(k.flag) }));
}

// dashStrip is the facet that colors the backlog's strip, its plugin's.
export function dashStrip() {
  return viewStrip("dash") || { id: "" };
}

export function dashWaitOption(rows: J, lens: J, pal: J) {
  const s = dashStrip();
  const counts = dashTally(rows, lens, s.id);
  return dashBars(fieldRank(s.id).filter((k: J) => counts.get(k)).map((k: J) => dashItem(lens, s.id, k, counts.get(k), pal[stripColor(s, k)], fieldShow(s.id, k))), pal);
}

export function dashPeopleOption(rows: J, lens: J, pal: J) {
  const role = new Map();
  for (const r of rows) {
    for (const who of waitingWho(r)) {
      if (!role.has(who)) role.set(who, who === "me" ? "me" : who === pl(r).author ? "author" : "reviewer");
    }
  }
  const counts = dashTally(rows, lens, "waits-on", waitingWho);
  return dashBars(dashRanked(counts, DASH_TOP).map(([who, n]: J) => dashItem(lens, "waits-on", who, n, pal[role.get(who) || "reviewer"])), pal);
}

export function dashAuthorsOption(rows: J, lens: J, pal: J) {
  const f = fld("author");
  const counts = dashTally(rows, lens, "author", (r: J) => [(f!.key ? f!.key(r) : (f!.values!(r) || [])[0]) || "none"]);
  return dashBars(dashRanked(counts, DASH_TOP).map(([a, n]: J) => dashItem(lens, "author", a, n, a === "me" ? pal.me : pal.author)), pal);
}

export function dashReposOption(rows: J, lens: J, pal: J) {
  const orgs = new Map();
  for (const r of rows) {
    if (!passesFilters(r, lens.filters, "where")) continue;
    const repo = facetValues(r, "where")[0];
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
  const data = [...orgs.entries()].sort(cmp).map(([org, repos]: J) => ({
    name: org,
    children: [...repos.entries()].sort(cmp).map(([repo, c]: J) => ({
      ...dashItem(lens, "where", repo, c.n, dashMix(pal.panel, pal.late, 0.6 * c.stalled / c.n), repo.slice(org.length + 1)),
      tooltip: { formatter: dashEsc(repo) + ": " + dashEsc(t("{pending} pending, {stalled} stalled", { pending: c.n, stalled: c.stalled })) },
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

export function dashKindOption(rows: J, lens: J, pal: J) {
  const counts = dashTally(rows, lens, "kind");
  return dashBars(catalog.kindOrder.filter((k: J) => counts.get(k)).map((k: J) => dashItem(lens, "kind", k, counts.get(k), pal.author, kindLabel(k, ""))), pal);
}

// dashTagsOption is a treemap of tag keys and their values, or with key the
// ranked values of that key alone.
export function dashTagsOption(rows: J, lens: J, pal: J, key: J) {
  const counts = dashTally(rows, lens, "tag");
  if (key) {
    const of = new Map([...counts].filter(([tag]) => tag.startsWith(key + ":")));
    return dashBars(dashRanked(of, 15).map(([tag, n]: J) => dashItem(lens, "tag", tag, n, pal.me, tag.slice(key.length + 1))), pal);
  }
  const keys = new Map();
  for (const [tag, n] of counts) {
    const k = tag.split(":")[0];
    if (!keys.has(k)) keys.set(k, []);
    keys.get(k).push([tag, n]);
  }
  if (!keys.size) return null;
  const colors = dashSeries(pal);
  const data = [...keys.entries()].sort(cmp).map(([k, ts]: J, i: number) => ({
    name: k, q: [["tag", k]],
    children: ts.sort((a: J, b: J) => b[1] - a[1] || cmp(a[0], b[0])).map(([tag, n]: J) => ({
      ...dashItem(lens, "tag", tag, n, colors[i % colors.length], tag.slice(k.length + 1)),
      tooltip: { formatter: `${dashEsc(tag)}: ${n}` },
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

export function dashShapeOption(rows: J, lens: J, pal: J) {
  const sizes = [...SIZES, "none"];
  const risks = [...RISKS, "none"];
  const cells = new Map();
  for (const r of rows) {
    if (!passesFilters(r, lens.filters, ["size", "risk"])) continue;
    const k = `${fld("size").values(r)[0]}|${fld("risk").values(r)[0]}`;
    cells.set(k, (cells.get(k) || 0) + 1);
  }
  const types = dashTally(rows, lens, "type");
  if (!cells.size) return null;
  const max = Math.max(...cells.values());
  const data = [...cells.entries()].map(([k, n]: J) => {
    const [s, rk] = k.split("|");
    const dim = dashDim(lens, "size", s) || dashDim(lens, "risk", rk);
    return { value: [sizes.indexOf(s), risks.indexOf(rk), n], q: [["size", s], ["risk", rk]], itemStyle: { opacity: dim ? DASH_DIM : 1 }, tooltip: { formatter: dashEsc(t("size {size}, risk {risk}: {n}", { size: s === "none" ? t("unknown") : s.toUpperCase(), risk: rk === "none" ? t("unknown") : rk, n })) } };
  });
  const typeItems = dashRanked(types, 8).map(([type, n]: J) => dashItem(lens, "type", type, n, pal.reviewer, type === "none" ? t("no brief") : type));
  return {
    ...dashBase(),
    grid: [{ left: 8, top: 8, bottom: 8, width: "48%", containLabel: true }, { left: "60%", right: 32, top: 8, bottom: 8, containLabel: true }],
    xAxis: [
      { type: "category", gridIndex: 0, data: sizes.map((s: J) => (s === "none" ? "?" : s.toUpperCase())), splitArea: { show: false } },
      dashCountAxis(1),
    ],
    yAxis: [
      { type: "category", gridIndex: 0, data: risks.map((r: J) => (r === "none" ? "?" : r)) },
      { type: "category", gridIndex: 1, inverse: true, data: typeItems.map((i: J) => i.name), axisLabel: { width: 80, overflow: "truncate" } },
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
export function dashWorkingMs(from: J, to: J) {
  let total = 0;
  for (let cur = from; cur < to;) {
    const d = new Date(cur);
    const next = Math.min(to, Date.UTC(d.getUTCFullYear(), d.getUTCMonth(), d.getUTCDate() + 1));
    if (d.getUTCDay() !== 0 && d.getUTCDay() !== 6) total += next - cur;
    cur = next;
  }
  return total;
}

export function dashDays(r: J, now: J) {
  return Math.max(1 / 24, dashWorkingMs(sinceTime(r), now) / DASH_DAY);
}

export function dashQuantile(sorted: J, p: J) {
  if (!sorted.length) return 0;
  const i = (sorted.length - 1) * p;
  const lo = Math.floor(i);
  return sorted[lo] + (sorted[Math.ceil(i)] - sorted[lo]) * (i - lo);
}

export function dashRound(x: J) {
  return Math.round(x * 100) / 100;
}

// dashAgingOption plots each pull request's wait by its stage; a hidden
// value axis under the stage axis spreads the dots of one stage apart.
export function dashAgingOption(rows: J, lens: J, pal: J, now: J, stalledHours: J) {
  const shown = rows.filter((r: J) => passesFilters(r, lens.filters) && isSet(sm(r).since));
  if (!shown.length) return null;
  const kinds = catalog.kindOrder.filter((k: J) => shown.some((r: J) => r.kind === k));
  const points = shown.map((r: J) => {
    const n = [...keyOf(r)].reduce((h: J, c: J) => (h * 31 + c.charCodeAt(0)) % 1000003, 0);
    const days = dashDays(r, now);
    const time = days < 1 ? tn("{n} working hours", Math.round(days * 24)) : tn("{n} working days", dashRound(days));
    return {
      value: [kinds.indexOf(r.kind) + (((n * 0.618034) % 1) - 0.5) * 0.6, dashRound(days)],
      pr: keyOf(r), name: labelOf(r),
      itemStyle: { color: pal[waitColor(r)] },
      tooltip: { formatter: `${dashEsc(labelOf(r))} ${dashEsc(r.title || "")}: ` + dashEsc(t("waits {time} on {who}", { time, who: waitCat(r) })) },
    };
  });
  const ages = shown.map((r: J) => dashDays(r, now)).sort((a: J, b: J) => a - b);
  const lines: J[] = [{ name: "p50", yAxis: dashRound(dashQuantile(ages, 0.5)) }, { name: "p85", yAxis: dashRound(dashQuantile(ages, 0.85)) }];
  if (stalledHours > 0) lines.push({ name: "stalled", yAxis: dashRound(stalledHours / 24), lineStyle: { color: pal.late } });
  return {
    ...dashBase(),
    grid: { left: 8, right: 48, top: 16, bottom: 8, containLabel: true },
    xAxis: [
      { type: "category", data: kinds.map((k: J) => kindLabel(k, "")), axisLabel: { interval: 0, rotate: kinds.length > 6 ? 30 : 0 } },
      { type: "value", min: -0.5, max: kinds.length - 0.5, show: false },
    ],
    yAxis: { type: "log", logBase: 10, name: t("working days"), min: 0.01 },
    series: [{
      type: "scatter", xAxisIndex: 1, symbolSize: 9, data: points,
      markLine: { silent: true, symbol: "none", lineStyle: { type: "dashed", color: pal.muted }, label: { formatter: "{b}", position: "end" }, data: lines },
    }],
  };
}

export function dashNextOption(rows: J, lens: J, pal: J, now: J, health: J) {
  const end = now + DASH_HORIZON_HOURS * 3600000;
  const lanes = DASH_NEXT.map((lane: J) => lane.id);
  const within = (at: J) => {
    const ms = Date.parse(at);
    return ms >= now && ms <= end ? ms : null;
  };
  const data = [];
  for (const r of rows) {
    if (!passesFilters(r, lens.filters)) continue;
    for (const e of upcoming(r)) {
      const ms = within(e.at);
      if (ms === null || !lanes.includes(e.what)) continue;
      const what = DASH_NEXT[lanes.indexOf(e.what)].label.toLowerCase();
      data.push({ value: [ms, lanes.indexOf(e.what)], pr: keyOf(r), name: labelOf(r), itemStyle: { color: pal[waitColor(r)] }, tooltip: { formatter: dashEsc(labelOf(r)) + ": " + dashEsc(t("{what} at {time}", { what, time: stamp(new Date(ms).toISOString()) })) } });
    }
  }
  const quota = health && health.brief && within(health.brief.paused_until);
  if (quota) data.push({ value: [quota, lanes.indexOf("quota")], name: t("brief quota"), itemStyle: { color: pal.late }, tooltip: { formatter: dashEsc(t("Briefs resume")) } });
  if (!data.length) return null;
  return {
    ...dashBase(),
    grid: { left: 8, right: 24, top: 8, bottom: 8, containLabel: true },
    xAxis: { type: "time", min: now, max: end },
    yAxis: { type: "category", data: DASH_NEXT.map((lane: J) => lane.label) },
    series: [{ type: "scatter", symbolSize: 11, data }],
  };
}

export function dashOpenedOption(rows: J, lens: J, pal: J) {
  const counts = dashTally(rows, lens, "opened");
  const items = fieldRank("opened").filter((k: J) => counts.get(k)).map((k: J) => dashItem(lens, "opened", k, counts.get(k), pal.me, fieldShow("opened", k)));
  if (!items.length) return null;
  return {
    ...dashBase(),
    grid: { left: 8, right: 16, top: 16, bottom: 8, containLabel: true },
    xAxis: { type: "category", data: items.map((i: J) => i.name) },
    yAxis: { type: "value", minInterval: 1 },
    series: [{ type: "bar", barMaxWidth: 36, data: items, label: { show: true, position: "top", color: pal.muted }, tooltip: { formatter: dashEsc(t("opened {b} ago: {c}")) } }],
  };
}

export function dashCostOption(rows: J, lens: J, pal: J) {
  const s = dashStrip();
  const files = new Map();
  for (const r of rows) {
    const n = Number(((pl(r).facts || {}).changed_files)) || 0;
    if (!n || !passesFilters(r, lens.filters, s.id)) continue;
    for (const c of facetValues(r, s.id)) files.set(c, (files.get(c) || 0) + n);
  }
  const turns = (r: J) => ((pl(r).facts || {}).turns || {});
  const shown = rows.filter((r: J) => passesFilters(r, lens.filters));
  const metric = shown.some((r: J) => (turns(r).cost_usd || 0) > 0) ? "cost_usd" : "tokens";
  const spend = shown.filter((r: J) => (turns(r)[metric] || 0) > 0)
    .sort((a: J, b: J) => turns(b)[metric] - turns(a)[metric] || cmp(labelOf(a), labelOf(b))).slice(0, DASH_TOP);
  if (!files.size && !spend.length) return null;
  const load = fieldRank(s.id).filter((k: J) => files.get(k)).map((k: J) => ({ ...dashItem(lens, s.id, k, files.get(k), pal[stripColor(s, k)], fieldShow(s.id, k)), tooltip: { formatter: dashEsc(tn("{label}: {n} changed files", files.get(k), { label: fieldShow(s.id, k) })) } }));
  const cost = spend.map((r: J) => {
    const tr = turns(r);
    return { name: labelOf(r), value: metric === "cost_usd" ? dashRound(tr.cost_usd) : tr.tokens, pr: keyOf(r), itemStyle: { color: pal[waitColor(r)] },
      tooltip: { formatter: `${dashEsc(labelOf(r))}: ${metric === "cost_usd" ? `US$ ${tr.cost_usd.toFixed(2)}, ` : ""}` + dashEsc(tn("{tokens} tokens over {n} turns", tr.count || 0, { tokens: tr.tokens || 0 })) } };
  });
  const spendTitle = metric === "cost_usd" ? t("US$ spent per pull request") : t("Tokens per pull request") + "\n" + t("no US$ cost recorded");
  const top = metric === "cost_usd" ? 22 : 36;
  return {
    ...dashBase(),
    title: [{ text: t("Changed files by whom it waits on"), left: 8, top: 0, textStyle: { fontSize: 11, fontWeight: "normal", color: pal.muted } },
      { text: spendTitle, left: "52%", top: 0, textStyle: { fontSize: 11, fontWeight: "normal", color: pal.muted } }],
    grid: [{ left: 8, top, bottom: 8, width: "40%", containLabel: true }, { left: "52%", right: 40, top, bottom: 8, containLabel: true }],
    xAxis: [dashCountAxis(0), { ...dashCountAxis(1), minInterval: 0, axisLabel: { hideOverlap: true, formatter: dashCompact } }],
    yAxis: [
      { type: "category", gridIndex: 0, inverse: true, data: load.map((i: J) => i.name), axisLabel: { width: 90, overflow: "truncate" } },
      { type: "category", gridIndex: 1, inverse: true, data: cost.map((i: J) => i.name), axisLabel: { width: 110, overflow: "truncate" } },
    ],
    series: [
      { type: "bar", xAxisIndex: 0, yAxisIndex: 0, barMaxWidth: 16, data: load, label: { show: true, position: "right", color: pal.muted } },
      { type: "bar", xAxisIndex: 1, yAxisIndex: 1, barMaxWidth: 16, data: cost, label: { show: true, position: "right", color: pal.muted, formatter: (p: J) => dashCompact(p.value) } },
    ],
  };
}

export function dashCompact(n: J) {
  return n >= 1e6 ? dashRound(n / 1e6) + "M" : n >= 1e4 ? Math.round(n / 1e3) + "k" : String(n);
}

export function dashHas(f: J, r: J, value: J) {
  const v = fold(value);
  return itemValues(f, r).some((x: J) => (f.match ? f.match(x, v) : x === v));
}

export function dashBlockersOption(rows: J, lens: J, pal: J) {
  const causes = DASH_CAUSES.map((c: J) => {
    const f = fld(c.field);
    const n = rows.filter((r: J) => passesFilters(r, lens.filters, c.field) && dashHas(f, r, c.value)).length;
    return n ? dashItem(lens, c.field, c.value, n, c.field === "ci" ? pal.err : pal.late, c.label) : null;
  }).filter(Boolean);
  const checks = new Map();
  for (const r of rows) {
    if (!passesFilters(r, lens.filters, "ci")) continue;
    for (const name of new Set(((pl(r).facts || {}).checks || {}).failing || [])) checks.set(name, (checks.get(name) || 0) + 1);
  }
  const failing = dashRanked(checks, 8).map(([name, n]: J) => dashItem(lens, "ci", name, n, pal.err));
  if (!causes.length && !failing.length) return null;
  return {
    ...dashBase(),
    title: [{ text: t("Cause"), left: 8, top: 0, textStyle: { fontSize: 11, fontWeight: "normal", color: pal.muted } },
      { text: t("Failing checks"), left: "52%", top: 0, textStyle: { fontSize: 11, fontWeight: "normal", color: pal.muted } }],
    grid: [{ left: 8, top: 22, bottom: 8, width: "40%", containLabel: true }, { left: "52%", right: 32, top: 22, bottom: 8, containLabel: true }],
    xAxis: [dashCountAxis(0), dashCountAxis(1)],
    yAxis: [
      { type: "category", gridIndex: 0, inverse: true, data: causes.map((i: J) => i.name), axisLabel: { width: 100, overflow: "truncate" } },
      { type: "category", gridIndex: 1, inverse: true, data: failing.map((i: J) => i.name), axisLabel: { width: 110, overflow: "truncate" } },
    ],
    series: [
      { type: "bar", xAxisIndex: 0, yAxisIndex: 0, barMaxWidth: 16, data: causes, label: { show: true, position: "right", color: pal.muted }, tooltip: { formatter: "{b}: {c}" } },
      { type: "bar", xAxisIndex: 1, yAxisIndex: 1, barMaxWidth: 16, data: failing, label: { show: true, position: "right", color: pal.muted }, tooltip: { formatter: "{b}: {c}" } },
    ],
  };
}

// dashLift compares each tag's share among the stalled with its share of
// the backlog: +100% means twice as common among the stalled.
export function dashLift(rows: J, lens: J) {
  const base = rows.filter((r: J) => passesFilters(r, lens.filters, ["tag", "is"]));
  const stalled = base.filter(isStalled);
  if (!stalled.length || stalled.length === base.length) return [];
  const count = (rs: J) => {
    const m = new Map();
    for (const r of rs) for (const tag of new Set(facetValues(r, "tag"))) m.set(tag, (m.get(tag) || 0) + 1);
    return m;
  };
  const all = count(base);
  const among = count(stalled);
  return [...all.entries()].filter(([, n]) => n >= DASH_LIFT_MIN).map(([tag, n]: J) => {
    const s = among.get(tag) || 0;
    return { tag, n, stalled: s, lift: Math.round(((s / stalled.length) / (n / base.length) - 1) * 100) };
  }).sort((a: J, b: J) => b.lift - a.lift || cmp(a.tag, b.tag));
}

// dashEsc escapes text for an echarts string formatter, which renders HTML.
const ESCAPES: Record<string, string> = { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" };

export function dashEsc(s: J): string {
  return String(s).replace(/[&<>"']/g, (c) => ESCAPES[c]);
}

export function dashLiftOption(rows: J, lens: J, pal: J) {
  const lifts = dashLift(rows, lens);
  if (!lifts.length) return null;
  const shown = lifts.length > 12 ? [...lifts.slice(0, 8), ...lifts.slice(-4)] : lifts;
  const items = shown.map((l: J) => ({
    name: l.tag, value: l.lift, q: [["tag", l.tag], ["is", "stalled"]],
    itemStyle: { color: l.lift > 0 ? pal.late : pal.loop, opacity: dashDim(lens, "tag", l.tag) ? DASH_DIM : 1 },
    tooltip: { formatter: dashEsc(l.tag) + ": " + dashEsc(t("{stalled} of {n} stalled, {lift}% against the backlog", { stalled: l.stalled, n: l.n, lift: (l.lift > 0 ? "+" : "") + l.lift })) },
  }));
  return {
    ...dashBase(),
    grid: { left: 8, right: 40, top: 8, bottom: 8, containLabel: true },
    xAxis: { type: "value", splitNumber: 3, axisLabel: { formatter: "{value}%", hideOverlap: true } },
    yAxis: { type: "category", inverse: true, data: items.map((i: J) => i.name), axisLabel: { width: 150, overflow: "truncate" } },
    series: [{ type: "bar", barMaxWidth: 16, data: items, label: { show: true, position: "right", formatter: "{c}%", color: pal.muted } }],
  };
}

export function dashAgeBoxOption(rows: J, lens: J, pal: J, now: J) {
  const boxes: J[] = [];
  const add = (field: string, keys: string[], label: (k: string) => string, color: string) => {
    for (const k of keys) {
      const ages = rows.filter((r: J) => passesFilters(r, lens.filters, field) && isSet(sm(r).since) && fld(field).values(r)[0] === k)
        .map((r: J) => dashDays(r, now)).sort((a: J, b: J) => a - b);
      if (!ages.length) continue;
      const v = [ages[0], dashQuantile(ages, 0.25), dashQuantile(ages, 0.5), dashQuantile(ages, 0.75), ages[ages.length - 1]].map(dashRound);
      boxes.push({
        name: label(k), value: v, q: [[field, k]],
        itemStyle: { color: pal.panel, borderColor: color, opacity: dashDim(lens, field, k) ? DASH_DIM : 1 },
        tooltip: { formatter: dashEsc(label(k)) + ": " + dashEsc(tn("{n} pull requests, median {median} working days, middle half {low}-{high}", ages.length, { median: fmtNumber(v[2]), low: fmtNumber(v[1]), high: fmtNumber(v[3]) })) },
      });
    }
  };
  add("size", SIZES, (k: J) => t("size {size}", { size: k.toUpperCase() }), pal.me);
  add("risk", RISKS, (k: J) => t("{risk} risk", { risk: k }), pal.reviewer);
  if (!boxes.length) return null;
  return {
    ...dashBase(),
    grid: { left: 8, right: 16, top: 16, bottom: 8, containLabel: true },
    xAxis: { type: "category", data: boxes.map((b: J) => b.name), axisLabel: { interval: 0, rotate: boxes.length > 6 ? 30 : 0 } },
    yAxis: { type: "value", name: t("working days") },
    series: [{ type: "boxplot", data: boxes }],
  };
}

// dashClickQuery folds a click into the query: the pairs it names go in,
// or out when the query holds them all; neg excludes instead.
export function dashClickQuery(q: string, pairs: [string, string][], neg?: boolean) {
  const has = ([field, raw]: J) => qualifiers(q).some((x: J) => x.field === field && x.value === normValue(field, raw) && x.neg === !!neg);
  const todo = pairs.every(has) ? pairs : pairs.filter((p: J) => !has(p));
  return todo.reduce((acc, [field, raw]) => toggledQualifier(acc, field, raw, !!neg), q);
}

