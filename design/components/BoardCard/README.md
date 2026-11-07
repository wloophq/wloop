# BoardCard

The board layout's unit: the list row's parts stacked in a card, built from the same row model.

`panel` fill, `rule` border, `radius-md`, `strip-card` left edge in the waiter's mark color. An `li` drawn by `rowView(m, lens, { card: true })`, with the TaskRow's slots stacked beside the StateGlyph: the title, the `.open` button stretched over the card so a click anywhere opens it, with the kind pieces and the changed dot; the meta and flags; the ref and why; whom it waits on and the date. The workspace and the actions stay on the list row. Each piece is a facet button above the stretched title. Tab stops on the title alone; the facet buttons take `tabindex="-1"` and the arrows move between them. Selected adds the `me` tint and inset bar and `aria-current` on the title; `.mine` uses `mine-bg`.

## Consumer provides
The same row model as PullRequestRow. Cards sit in a lane (`lane-head` with an `overline` title and count).
