# OutboxGroup

One group of the Outbox: the drafts that hold one value of the facet the lens groups by (by default who proposed them: a workflow, an intent, a rule or your own hand), each drawn as a [GateCard](../GateCard/README.md) draft. `DraftList.svelte` (`internal/web/ui/src/components/screens/`) draws it for a view whose `layout` is `outbox` and for the Review's step of that view, from `GET /api/outbox`. With the lens grouping by nothing, the drafts sit in one group without a head.

## Anatomy

A `section.ob-group`, its groups `space-24` apart:

- a head line, `ob-head`: the title (`ob-title`, `text-list-heading`, 600), the count (`ob-n`, `mono`, `faint`, "2 drafts" through `tn`), the workflow's name (`ob-workflow`, `mono`, `faint`) and, right-aligned, **Stop drafting these**;
- one line on why the group exists (`ob-about`, `muted`, `text-meta`), when the profile has one;
- a draft card per draft (`draftCard(null, d, { scope: "outbox", context })`), whose `d-context` block, above where it posts, shows the asks of its task with their quotes on a quote bar, and the items it is about, each with its state in a `mono` tag.

**Stop drafting these** shows only on a group whose head names a workflow. Pressed, it turns into an inline pair, **Stop this workflow** (`primary`) and **Keep it**; only the first posts `POST /api/workflows/<name>/disable`. No browser dialog.

Motion: a card's border answers focus within it over `motion-fast`; nothing moves on its own.

## Consumer provides

The page as `GET /api/outbox` serves it: its drafts, each with its `facets`, its search `texts` and its `context` (`asks`, `items`); the draft facets' specs, which the lens reads as its fields, whose `labels` word each value in the served locale (a workflow's `[outbox]` title, an intent's, gates.toml's `[drafts]` words, a plugin's kind words, the facet's `values`), the title of its group and its entry in the Filter palette; and `heads`, by facet and value, each with the `about` line and the `workflow` behind it. A value with no label shows as served. The catalog's `draft_purposes`, `draft_states` and `effects` for the cards.

## Do / don't

- Do take every title and line from the page; never word a group in the client.
- Do keep the daemon's order of drafts until the lens picks a sort, and order groups as the lens orders them.
- Don't post the disable before the inline confirm, and don't use `confirm()`.
- Don't offer **Stop drafting these** on a group whose head names no workflow: none stands behind it.
