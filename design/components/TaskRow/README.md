# TaskRow

One item in any list, a task or a plugin's row: two lines, `row-height` at least, drawn by `rowView` in `static/row.js`.

A grid of `16px 1fr auto`:

- Line 1: the StateGlyph (`.glyph`, `data-category` the state's category, its hue the waiter's through `w-*`, titled with the state; a button that filters by the state), the kind pieces (`.verb`), the title (`.t`, one line, ellipsis, without the unit label line 2 shows), the changed dot (`.ch`), the pending drafts marker (`.draft-mark`, their count); on the right (`.meta`) the meta pieces (at most `show_max`, then `+n`, whose tooltip names the rest), the flags and the row's actions; pieces and actions that do not fit leave whole, as on a [PullRequestRow](../PullRequestRow/README.md).
- Line 2: the ref (`.ref`, mono) and why it waits (`.why`, one line, ellipsis); on the right (`.r2`) the workspace and link (`.aside`, hidden at 900px and below), the strip dot, whom it waits on (`.who`) and the date the time rule picks with its label (`.age`): since when it waits on you, or when someone last moved it; a pending round's countdown instead.

`:hover` takes `hover`; `.sel` takes the `me` tint and an inset `me` bar; `.mine` grounds it on `mine-bg`; `.late` turns the date late. A grouped list puts each group under a sticky `.g-head`: its label in mono, its count, "only this". A unit has no state, so a plugin's row draws no glyph.

## Consumer provides
A row model: the lens's pieces by part (`kind`, `meta`, `flag`, `ref`, `who`, `age`, `aside`), the title and its unit labels, the glyph (category, label, the state's facet), changed, the drafts, the strip dot, why, the workspace, link and actions.

## Do / don't
- Do keep each line on one line; cut the title and the reason, never whom it waits on or the date.
- Don't color any other part of the row with a waiter hue than the glyph, the dot and whom it waits on.
