# PullRequestRow

A table row for one unit: strip, title and meta, waiter, age, one action.

The first cell carries a `strip-row` border in the waiter's mark color via `.w-me`, `.w-author`, `.w-reviewer`, `.w-loop` or `.w-nobody`. `.mine` grounds the row on `mine-bg`; `.sel` tints it with `me` and an inset bar; `.late` turns the age late.

## Consumer provides
The unit (title, ref, author, tags), the waiter, the age, the situation word and the recommended action. The Why and Where columns are optional and hide when the detail panel is open.

## Do / don't
- Do keep one action button; more go to the palette.
- Don't color any other part of the row with a waiter hue.
