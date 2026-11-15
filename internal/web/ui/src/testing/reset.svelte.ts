import { pending, results, scheduler } from "../state/actions.svelte";
import { resetSync } from "../state/sync.svelte";
import { foldTexts } from "../components/common/Markdown.svelte";
import { clock } from "../state/clock.svelte";
import { dropOverlays } from "../state/overlays.svelte";
import { served } from "../state/keycaps.svelte";
import { nav } from "../state/route.svelte";
import {
  answered, busy, catalog, collapsed, detailState, drafts, editing, editPayloads, editRevs, gateState, homeState, notice, outboxState,
  plugins, queueState, removeState, reviewState, state, taskState, tasksState,
} from "../state/store.svelte";
import { I18N } from "../lib/i18n.svelte";

// fresh are the store's values as the page starts, taken before any test writes.
const fresh = $state.snapshot({ catalog, state, detailState, drafts, homeState, notice, outboxState, queueState, removeState, reviewState, taskState, tasksState });

// resetStore puts every store back as the page starts, the clock at now and the hash at hash.
// A test that wants the sync an action asks for drives it itself: resetStore stops any read a previous test left
// queued on the real debounce, and takes the actions' requests for a sync.
export function resetStore(now = Date.parse("2026-11-07T12:00:00Z"), hash = ""): void {
  resetSync();
  foldTexts();
  scheduler.load = () => {};
  dropOverlays();
  served.bindings = [];
  const copy = structuredClone(fresh);
  Object.assign(catalog, copy.catalog, { kinds: new Map(), events: new Map(), eventData: new Map(), eventTones: new Map() });
  Object.assign(state, copy.state, { roundGates: new Map() });
  Object.assign(detailState, copy.detailState);
  Object.assign(drafts, copy.drafts);
  Object.assign(homeState, copy.homeState);
  Object.assign(notice, copy.notice);
  Object.assign(outboxState, copy.outboxState);
  Object.assign(queueState, copy.queueState);
  Object.assign(removeState, copy.removeState);
  Object.assign(reviewState, copy.reviewState);
  Object.assign(taskState, copy.taskState);
  Object.assign(tasksState, copy.tasksState);
  Object.assign(plugins, { views: new Map(), names: new Map(), reacting: new Set() });
  for (const s of [busy, collapsed, pending]) s.clear();
  for (const m of [answered, editing, editPayloads, editRevs, results, gateState.answers, gateState.picked]) m.clear();
  Object.assign(I18N, { locale: "en", strings: {}, stamp: "", latest: "", fetching: "" });
  clock.now = now;
  nav.hash = hash;
}
