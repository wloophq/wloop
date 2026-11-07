# OutboxGroup

One group of the Outbox: the drafts one workflow proposed, or those of one purpose no workflow proposed, or those written by hand, each drawn as a [GateCard](../GateCard/README.md) draft. `static/outbox.js` draws it for a view whose `layout` is `outbox`, from `GET /api/outbox`.

## Anatomy

A `section.ob-group`, its groups `space-24` apart:

- a head line, `ob-head`: the title (`ob-title`, `text-list-heading`, 600), the count (`ob-n`, `mono`, `faint`, "2 drafts" through `tn`), the workflow's name (`ob-workflow`, `mono`, `faint`) and, right-aligned, **Stop drafting these**;
- one line on why the group exists (`ob-about`, `muted`, `text-meta`), when the profile has one;
- a draft card per draft (`draftCard(null, d, { scope: "outbox", context })`), whose `d-context` block, above where it posts, shows the asks of its task with their quotes on a quote bar, and the items it is about, each with its state in a `mono` tag.

**Stop drafting these** shows only on a workflow's group. Pressed, it turns into an inline pair, **Stop this workflow** (`primary`) and **Keep it**; only the first posts `POST /api/workflows/<name>/disable`. No browser dialog.

Motion: a card's border answers focus within it over `motion-fast`; nothing moves on its own.

## Consumer provides

The page as `GET /api/outbox` serves it: groups, the largest first, each with its `workflow` or `purpose`, `title` and `about` in the served locale (a workflow's `[outbox]` texts, gates.toml's `[drafts]` words), and its drafts with their `context` (`asks`, `items`). The catalog's `draft_purposes`, `draft_states` and `effects` for the cards.

## Do / don't

- Do take every title and line from the page; never word a group in the client.
- Do keep the daemon's order of groups and of drafts inside a group.
- Don't post the disable before the inline confirm, and don't use `confirm()`.
- Don't offer **Stop drafting these** on a purpose's group or on the hand-written one: no workflow stands behind them.
