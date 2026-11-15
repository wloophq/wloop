import { state } from "../state/store.svelte";
import type { J } from "./util";

const CARD_GROUPS = new Set(["turn", "intake", "brief"]);

// FOCUS_PAIRS are the actions whose button takes the slot of their counterpart's, so the focus stays on a toggle
// as it flips.
const FOCUS_PAIRS: Record<string, string> = { resume: "pause", unmute: "mute", untrack: "track" };

// focusSlot is the slot an action's button takes: a toggle's pair, or a round's start, which an approving gate
// intent and an intent that starts a round on request share.
export function focusSlot(name: string): string {
  const sp = state.specs.byId[name];
  if (sp && sp.group === "start" && sp.intake) return "start";
  if (sp && sp.group === "start" && sp.verb === "dismiss") return "open";
  return FOCUS_PAIRS[name] || name;
}

// actionStates are the served actions of a subject on a surface with their specs; the detail leaves the question
// card's, intake's and brief's groups to them unless groups names them.
export function actionStates(subject: J, surface: string, groups?: string[]): [J, J][] {
  return (subject.actions || [])
    .map((st: J) => [st, state.specs.byId[st.id]] as [J, J])
    .filter(([, sp]: [J, J]) => sp && sp.surfaces.includes(surface) && (groups ? groups.includes(sp.group) : surface !== "detail" || !CARD_GROUPS.has(sp.group)));
}

// actionTitle is an action's tooltip: what it does, then what it would do on this subject, or why it is off.
export function actionTitle(st: J, sp: J): string | undefined {
  return [sp && sp.summary, st.enabled ? st.note : st.reason].filter(Boolean).join("\n") || undefined;
}

export function actionClass(st: J, primary = true): string {
  return ["act", st.enabled && primary && st.primary ? "primary" : "", st.enabled ? "" : "off"].filter(Boolean).join(" ");
}

// actionNotes are the warnings of a subject's enabled actions on a surface.
export function actionNotes(subject: J, surface: string): string[] {
  return actionStates(subject, surface).filter(([st]) => st.enabled && st.warning).map(([st]) => st.warning);
}
