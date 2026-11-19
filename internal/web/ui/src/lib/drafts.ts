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

// outboxHead is what the page says of the group of value under the facet field: its title, why its drafts were
// written, the workflow that wrote them.
export function outboxHead(page: J, field: string, value: string): J {
  return (((page && page.heads) || {})[field] || {})[value] || null;
}
