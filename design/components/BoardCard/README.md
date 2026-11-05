# BoardCard

The board layout's unit: the list row's parts stacked in a card, built from the same row model.

`panel` fill, `rule` border, `radius-md`, `strip-card` left edge in the waiter's mark color. An `li` whose title is the `.open` button, stretched over the card so a click anywhere opens it; then the ref, meta and flags, then whom it waits on, its situation and its age, each piece a facet button above the stretched title. Tab stops on the title alone; the facet buttons take `tabindex="-1"` and the arrows move between them. Selected adds the `me` tint and inset bar and `aria-current` on the title; `.mine` uses `mine-bg`.

## Consumer provides
The same row model as PullRequestRow. Cards sit in a lane (`lane-head` with an `overline` title and count).
