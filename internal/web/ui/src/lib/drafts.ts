import { outboxState } from "../state/store.svelte";
import { viewSpec } from "./catalog";
import type { J } from "./util";

// OUTBOX_LAYOUT is the served layout of a view the Outbox draws, whatever the view's id.
export const OUTBOX_LAYOUT = "outbox";

export function isOutboxView(view: string): boolean {
  return (viewSpec(view) || {}).layout === OUTBOX_LAYOUT;
}

// outboxLensItems are the drafts of an Outbox page as the lens's items, in the Outbox's order.
export function outboxLensItems(page: J = outboxState.page): J[] {
  return ((page && page.drafts) || []).map((d: J) => ({ draft: d, facets: d.facets || {}, texts: d.texts || [] }));
}

// outboxEntries are drafts as the Outbox shows them, in order: each draft on its own, but a draft an open question
// of page pins, which shows as that question the first time one of its drafts comes, in shown or not; a draft whose
// question page does not serve shows on its own.
export function outboxEntries(page: J, drafts: J[], shown: Set<number>): { key: string; draft?: J; question?: J }[] {
  const questions = (page && page.questions) || [];
  const out: { key: string; draft?: J; question?: J }[] = [];
  for (const d of drafts) {
    const q = d.pinned_by ? questions.find((g: J) => g.subject && g.subject.id === d.pinned_by) : null;
    if (!q) {
      out.push({ key: "draft:" + d.id, draft: d });
    } else if (!shown.has(q.id)) {
      shown.add(q.id);
      out.push({ key: "question:" + q.id, question: q });
    }
  }
  return out;
}

// outboxHead is what the page says of the group of value under the facet field beside its title: why its drafts
// were written, the workflow that wrote them.
export function outboxHead(page: J, field: string, value: string): J {
  return (((page && page.heads) || {})[field] || {})[value] || null;
}
