import { fmtNumber, t } from "../../lib/i18n.svelte";
import { isSet, type J } from "../../lib/util";
import { ago } from "./facts";

export function connWord(conn: string): string {
  switch (conn) {
    case "connecting":
      return t("connecting");
    case "reconnecting":
      return t("reconnecting");
    case "closed":
      return t("disconnected");
    default:
      return conn;
  }
}

// syncText is what the top bar says of the connection and the daemon's sync: the connection while it is not
// open, else whether rounds are paused, the oldest ingest's sync, the API budget and the version.
export function syncText(conn: string, hl: J): string {
  if (conn !== "open" || !hl) return connWord(conn);
  const ingest = (hl.jobs || []).filter((j: J) => j.name.startsWith("ingest:"));
  const sync =
    ingest.find((j: J) => j.running) || ingest.find((j: J) => !isSet(j.last_ok)) || [...ingest].sort((a: J, b: J) => Date.parse(a.last_ok) - Date.parse(b.last_ok))[0];
  const parts: string[] = [];
  if (hl.paused) parts.push(t("automatic rounds paused"));
  if (sync) parts.push(sync.running ? t("syncing") : isSet(sync.last_ok) ? t("synced {ago}", { ago: ago(sync.last_ok) }) : t("not synced yet"));
  const rl = hl.rate_limit;
  if (rl && rl.limit) parts.push(t("API budget {remaining} of {limit} left", { remaining: fmtNumber(rl.remaining), limit: fmtNumber(rl.limit) }));
  if (hl.version) parts.push(hl.version);
  return parts.join(" · ");
}
