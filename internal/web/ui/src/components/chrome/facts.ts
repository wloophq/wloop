import { RISKS, SIZES, age, stamp, upcoming, waitCat, waitColor, waitingLabel, waitingWho } from "../../lib/lens";
import { facetValues, pl, rowStrip, sm } from "../../lib/lens";
import { fmtDate, t } from "../../lib/i18n.svelte";
import { clock } from "../../state/clock.svelte";
import { state } from "../../state/store.svelte";
import { isSet, keyOf, type J } from "../../lib/util";

export function ago(iso: string, now = clock.now): string {
  if (!isSet(iso)) return "";
  return now - Date.parse(iso) < 60000 ? t("just now") : t("{age} ago", { age: age(iso, now) });
}

function resolveWho(row: J, w: string): string {
  if (w !== "author") return w;
  const viewer = state.health && state.health.viewer;
  const author = pl(row).author || "";
  return viewer && author.toLowerCase() === viewer.toLowerCase() ? "me" : author;
}

export { RISKS, SIZES, age, stamp, upcoming, waitCat, waitColor, waitingLabel, waitingWho };
