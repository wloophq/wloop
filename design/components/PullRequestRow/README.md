# PullRequestRow

A plugin's unit, such as a pull request, in a list: the TaskRow's two lines without the StateGlyph, since a unit has no task state.

Line 1: the situation as kind pieces, the title, then on the right the meta pieces (tags up to `show_max`, then `+n`), the flags (stale, intake) and the row's actions. Line 2: the ref and why it waits, then on the right the workspace, agents and the link to the service, the strip dot in the waiter's hue (`.w-me`, `.w-author`, `.w-reviewer`, `.w-loop` or `.w-nobody`), whom it waits on and its date with its label, or a pending round's countdown. `.mine` grounds the row on `mine-bg`; `.sel` tints it with `me` and an inset bar; `.late` turns the date late.

## Consumer provides
The unit (title, ref, tags), the waiter, the date, the situation word, the workspace and the actions its plugin serves.

## Do / don't
- Do keep one primary action; more go to the palette.
- Don't color any other part of the row with a waiter hue than the dot and whom it waits on.
