import { brand } from "../../brand";
import { exploreSearch, reviewDecision, searchService } from "../../lib/catalog";
import { t, tn } from "../../lib/i18n.svelte";
import { rowByKey, sm } from "../../lib/lens";
import { isSet, keyOf, labelOf, safeURL, tokens, type J } from "../../lib/util";
import { act, clipboard, setNotice } from "../../state/actions.svelte";
import { postJSON } from "../../state/api";
import { clock } from "../../state/clock.svelte";
import { viewOfExplore } from "../../state/loaders.svelte";
import { EXPLORE_MAX, readHash, writeHash } from "../../state/route.svelte";
import { state } from "../../state/store.svelte";
import { load } from "../../state/sync.svelte";
import { yamlMap, yamlString } from "./toolbar";

// exploreEnv is what an Explore action runs: the unit's action and the load that follows it; tests replace them.
export const exploreEnv = { act, load };

export const candidateURL = (c: J): string | undefined => safeURL(c.url);

export function hasTerm(text: string, term: string): boolean {
  return tokens(text).includes(term);
}

export function toggledQuery(text: string, term: string): string {
  const words = tokens(text);
  return (words.includes(term) ? words.filter((w) => w !== term) : [...words, term]).join(" ");
}

export function querySort(text: string): string {
  const sorts = text.split(/\s+/).filter((w) => w.startsWith("sort:"));
  return sorts.length ? sorts[sorts.length - 1].slice("sort:".length) : exploreSearch().default_sort;
}

export function withSort(text: string, sort: string): string {
  return [...text.split(/\s+/).filter((w) => w && !w.startsWith("sort:")), `sort:${sort}`].join(" ");
}

function windowDate(today: Date, days: number): string {
  const d = new Date(today.getFullYear(), today.getMonth(), today.getDate() - days);
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
}

export function hasWindow(text: string): boolean {
  return text.split(/\s+/).some((w) => w.startsWith("updated:>="));
}

// withWindow replaces any updated:>= term with one days before today, or only drops it when days is 0.
export function withWindow(text: string, today: Date, days: number): string {
  const words = text.split(/\s+/).filter((w) => w && !w.startsWith("updated:>="));
  return [...words, ...(days ? [`updated:>=${windowDate(today, days)}`] : [])].join(" ");
}

const setQuery = (x: string) => writeHash({ x: x.slice(0, EXPLORE_MAX) }, true);
export const toggleTerm = (term: string): void => setQuery(toggledQuery(readHash().x, term));
export const pickSort = (sort: string): void => setQuery(withSort(readHash().x, sort));
export function toggleWindow(days: number): void {
  const x = readHash().x;
  setQuery(withWindow(x, new Date(clock.now), hasWindow(x) ? 0 : days));
}

function waited(ms: number): string {
  const hours = Math.floor(ms / 3600000);
  if (hours < 1) return t("{minutes}m", { minutes: Math.floor(ms / 60000) });
  if (hours < 48) return t("{hours}h", { hours });
  return t("{days}d", { days: Math.floor(hours / 24) });
}

// candidateWait counts from the last activity, or from the row's own clock when the inventory holds the pull
// request.
export function candidateWait(c: J, row: J, hours: number, now = clock.now): { text: string; late: boolean } | null {
  const tracked = row && isSet(sm(row).since);
  const since = tracked ? sm(row).since : c.updated_at;
  if (!isSet(since)) return null;
  const ms = Math.max(0, now - Date.parse(since));
  return { text: t("waiting {time}", { time: waited(ms) }), late: tracked ? !!sm(row).stalled : ms > (hours || 48) * 3600000 };
}

export async function explore(query: string): Promise<void> {
  const q = String(query || "").trim();
  const ex = state.explore;
  if (!q || ex.busy) return;
  ex.busy = true;
  ex.error = "";
  writeHash({ x: q }, true);
  try {
    const out = await postJSON(viewOfExplore("explore"), { query: q });
    ex.result = out;
    ex.recent = out.recent || ex.recent;
    ex.sel = out.candidates.length === 1 ? keyOf(out.candidates[0]) : "";
  } catch (err) {
    ex.error = t("Explore failed: {error}", { error: (err as Error).message });
  }
  ex.busy = false;
}

export function exploreStatus(): string {
  const ex = state.explore;
  if (ex.busy) return t("Searching {provider}…", { provider: searchService() });
  if (ex.error) return ex.error;
  const r = ex.result;
  if (!r) return t("A search runs only when you press Enter or Search; each one spends about 1 point of the hourly API budget of {provider}.", { provider: searchService() });
  const shown = r.candidates.length;
  const order = (exploreSearch().sorts || []).find((s: J) => s.sort === querySort(r.query || ""));
  const cost = r.rate_limit && r.rate_limit.limit ? " · " + t("cost {cost}, {remaining} of {limit} left", { cost: r.cost, remaining: r.rate_limit.remaining, limit: r.rate_limit.limit }) : "";
  const read = r.read ? `${r.read} · ` : "";
  if (!shown && r.empty) return `${read}${r.empty}${cost}`;
  const vars = { count: tn("{n} pull requests", shown), shown, order: order ? order.shown : "", total: r.total, query: r.query };
  if (r.total <= shown) return read + t("{count} for {query}", vars) + cost;
  return (
    read +
    (order ? t("{count}, the {shown} {order} of {total}; narrow the search for the rest for {query}", vars) : t("{count}, the first {shown} of {total}; narrow the search for the rest for {query}", vars)) +
    cost
  );
}

// actOnCandidate brings a pull request the inventory lacks in first, so its card shows it in the loop while the
// action runs, then reports the outcome on the card itself.
export async function actOnCandidate(c: J, name: string, extra: J = {}): Promise<boolean> {
  const key = keyOf(c);
  const status = (text: string, more: J = {}) => {
    state.explore.status[key] = { text, ...more };
  };
  const implicit = !rowByKey(key);
  if (implicit) {
    status(t("adding it to {name}…", { name: brand.name }));
    if (!(await exploreEnv.act(c, "track"))) {
      status(state.actionError || t("adding it to {name} failed", { name: brand.name }), { error: true });
      return false;
    }
    await exploreEnv.load();
    if (name === "track") {
      status(t("added to {name}", { name: brand.name }), { done: true });
      return true;
    }
  }
  const row = rowByKey(key) || c;
  const offered = (row.actions || []).find((a: J) => a.id === name);
  const refused = offered && !offered.enabled ? offered.reason : "";
  let ok: boolean | undefined;
  if (!refused) {
    status(t("{action} running…", { action: name }));
    ok = await exploreEnv.act(row, name, extra);
  }
  await exploreEnv.load();
  if (ok === undefined && implicit) {
    const undone = (await exploreEnv.act(row, "untrack")) ? t("untracked it again") : t("untracking it failed too: {error}", { error: state.actionError });
    status(t("{action} {unit}: {why}; {undone}", { action: name, unit: labelOf(c), why: refused || t("cancelled"), undone }), { error: true });
    return false;
  }
  if (refused) {
    status(`${name} ${labelOf(c)}: ${refused}`, { error: true });
    return false;
  }
  status(
    ok ? (implicit ? t("{action} started; it is in {name} now", { name: brand.name, action: name }) : t("{action} sent", { action: name })) : state.actionError || t("{action} failed", { action: name }),
    { error: !ok, done: ok },
  );
  return !!ok;
}

export function candidatesYAML(meta: { query: string; read?: string; at: string }, cands: J[]): string {
  const out = [`query: ${yamlString(meta.query)}`];
  if (meta.read) out.push(`read_as: ${yamlString(meta.read)}`);
  out.push(`copied_at: ${yamlString(meta.at)}`, `count: ${cands.length}`, "pull_requests:");
  for (const c of cands) {
    const row = rowByKey(keyOf(c));
    const b = c.brief && c.brief.brief;
    const asks = (c.review_requests || []).map((w: string) => w.replace(/^team:/, ""));
    const fields: [string, string][] = [
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
    ];
    fields.filter(([, v]) => v).forEach(([k, v], i) => out.push(`${i ? "    " : "  - "}${k}: ${v}`));
  }
  return out.join("\n") + "\n";
}

export async function copyCandidates(): Promise<void> {
  const ex = state.explore;
  const cands = (ex.result && ex.result.candidates) || [];
  try {
    await clipboard.writeText(candidatesYAML({ query: readHash().x, read: ex.result && ex.result.read, at: new Date(clock.now).toISOString() }, cands));
    setNotice(tn("Copied {n} candidates as YAML.", cands.length));
  } catch (err) {
    setNotice(t("Could not copy: {error}", { error: (err as Error).message }), true);
  }
}
