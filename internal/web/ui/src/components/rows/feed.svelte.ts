import { activityPasses, eventRef, type Lens } from "../../lib/lens";
import { loopEvent } from "../../lib/catalog";
import { fmtDate, t } from "../../lib/i18n.svelte";
import { keyOf, tokens, type J } from "../../lib/util";
import { RECENT } from "../../lib/routes";
import { state } from "../../state/store.svelte";
import { emptyNote, type NotePart } from "./model";

export const LOOP_ONLY_KEY = "loop.activity.loopOnly";

// storage is where the loop-only toggle is remembered in this browser; tests replace it.
export const storage = {
  get: (k: string): string | null => {
    try {
      return localStorage.getItem(k);
    } catch {
      return null;
    }
  },
  set: (k: string, v: string): void => {
    try {
      localStorage.setItem(k, v);
    } catch {
      // The toggle still applies until the page reloads.
    }
  },
};

// activityView is the feed's toggle and the event last opened from it.
export const activityView = $state({ loopOnly: storage.get(LOOP_ONLY_KEY) === "1", selected: "" });

export function setLoopOnly(on: boolean): void {
  storage.set(LOOP_ONLY_KEY, on ? "1" : "0");
  activityView.loopOnly = on;
}

// dayLabel names the day of iso as seen at now: today, yesterday, else its date.
export function dayLabel(iso: string, now: number): string {
  const startOf = (x: Date) => new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime();
  const days = Math.round((startOf(new Date(now)) - startOf(new Date(iso))) / 86400000);
  if (days === 0) return t("Today");
  if (days === 1) return t("Yesterday");
  return fmtDate(iso, "long");
}

export function actorLabel(actor: string): string {
  const viewer = state.health && state.health.viewer;
  return viewer && actor.toLowerCase() === viewer.toLowerCase() ? "me" : actor;
}

export function requestedFrom(reviewer: string): string {
  return reviewer.startsWith("team:") ? reviewer.slice("team:".length) : actorLabel(reviewer);
}

export function activityID(it: J): string {
  const e = it.event;
  return `activity:${keyOf(e)}:${e.kind}:${e.key}:${e.at}`;
}

export type FeedEntry = { kind: "day"; id: string; label: string } | { kind: "event"; id: string; it: J };

// feed is the activity lens keeps, under a head per day.
export function feed(lens: Lens, now: number): FeedEntry[] {
  const out: FeedEntry[] = [];
  let day = "";
  for (const it of state.activity) {
    if ((activityView.loopOnly && !loopEvent(it.event.kind)) || !activityPasses(it, lens)) continue;
    const label = dayLabel(it.event.at, now);
    if (label !== day) {
      day = label;
      out.push({ kind: "day", id: "day:" + label, label });
    }
    out.push({ kind: "event", id: activityID(it), it });
  }
  return out;
}

export function activityEmptyNote(q: string): NotePart[] {
  const text = (s: string): NotePart[] => [{ kind: "text", text: s }];
  if (!state.activityLoaded) return text(t("Loading the activity."));
  if (state.activityError && !state.activity.length) return text(t("The recent activity could not be loaded."));
  if (!state.activity.length) return text(t("Nothing happened in the inventory yet."));
  if (tokens(q).length) return emptyNote(RECENT, q);
  return text(t("No loop events in the recent activity."));
}

export { eventRef };
