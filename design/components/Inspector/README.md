# Inspector

The task's page in the detail panel: everything about one task, in one column, in this order.

1. **Head**: the task's units, each label opening it in the cockpit with the service's name beside it opening it at the source (`ext`), in `mono` and `faint`; the title (`heading`) with the focus star and without the unit label it starts with; the state as a `pill` carrying its category (`data-category`), the changed flag, who owes it, when it wakes and the pending-drafts marker; whom it waits on and since when, as its row says it (the lens's who and age pieces, the age labeled by the time rule); why it waits; its notes.
2. **Props**: a `dl.props` grid, label in `faint` (96px), value beside it: the properties the profile declares, the brief's risk with its reason and its size, each card's signals with the signal's tone coloring the value, then the brief's tags, the first `show_max` the tag facet allows and a `+n` button that opens the Brief tab.
3. **Cards**: each unit's stored card, when it has one.
4. **Waiting on you**: one GateCard per open gate, the draft a draft's own task carries, and one draft card per draft the task's `drafts` names (read from `GET /api/drafts?task=<id>`); a draft's gate does not show again beside its card.
5. **Verbs**: the profile's primary verbs (`tasks.toml`'s `detail.verbs`) the task offers, each with its key in a `kbd`, then **More n** with `⌘K`, which opens the palette over every other verb the task offers (an offered verb that is off reads its reason there), then the intents that serve the task.
6. **Tabs**: a `role="tablist"` strip, the selected tab in `ink` with an underline, each label with its count in `mono`: **Brief**, **Timeline**, **Asks**, **Related**, **Runs**. A tab with nothing in it does not show, Timeline always does; the selection survives a reload and falls back to the first tab shown when its tab empties. The arrow keys move along the strip; `]` and `[` switch tabs while the focus is anywhere in the panel. Related holds the related tasks, the children and the merged-from trace, then the verbs that take another task in (merge, relate).
7. **Composer**: a box with **Send** (`primary`, to the task's agent) and **Save as draft** beside it (a reply held in the Outbox).

A section with nothing to show is left out, so a fresh task reads as a head, the Timeline tab and a composer.

## Consumer provides
The task with its properties, units, cards, asks, brief, gates, pending drafts, runs, children, related tasks and merged-from trace; the catalog's states, properties, verbs, facets, `detail_verbs` and `effects`; the timeline's events.

## Do / don't
- Do word states, properties, verbs and intents from the catalog; the client names none of them, and the profile picks the primary verbs.
- Don't add a section per kind of gate or per intent: gates, drafts and runs each have one card.
- Do put a verb that picks from the merged or related tasks on each of their rows, never among the verbs; the verbs that take another task open the palette over the open tasks.
- Don't list the related or merged property as text among the props: the Related tab shows them as tasks.
