import { brand } from "../../brand";
import { t } from "../../lib/i18n.svelte";
import type { J } from "../../lib/util";
import { state } from "../../state/store.svelte";
import { stamp } from "./facts";

export interface Warning {
  key: string;
  text: string;
  detail?: string;
  summary?: string;
}

// warnings are what the warnings bar says, the newest trouble first: a lost connection, the last action's
// failure with its detail, a load's error, then the daemon's own warnings and its jobs' last errors.
export function warnings(): Warning[] {
  const items: (string | { text: string; detail: string; source: string })[] = [];
  const hl: J = state.health;
  if (hl) {
    if (hl.slow_sync) items.push(t("Slow sync: the API budget is low until {time}", { time: stamp(hl.rate_limit.reset_at) }));
    for (const w of hl.warnings || []) items.push(w);
    for (const j of hl.jobs || []) if (j.last_error) items.push(`${j.name}: ${j.last_error}`);
  }
  if (state.detailError) items.unshift(state.detailError);
  if (state.activityError) items.unshift(state.activityError);
  if (state.error) items.unshift(state.error);
  if (state.actionError) items.unshift({ text: state.actionError, detail: state.actionDetail, source: state.actionSource });
  if (state.authLost || state.conn === "closed") items.unshift(t("disconnected: run `{command} open`", { command: brand.command }));
  const seen = new Map<string, number>();
  return items.map((it) => {
    const w: Warning = typeof it === "string" ? { key: "", text: it } : { key: "", text: it.text, detail: it.detail || "" };
    if (typeof it !== "string" && it.detail) {
      w.summary = it.source ? t("{source}'s message", { source: (hl && hl.workspace_manager) || it.source }) : t("Details");
    }
    const n = seen.get(w.text) || 0;
    seen.set(w.text, n + 1);
    w.key = w.text + "\u0000" + n;
    return w;
  });
}
