# EntryRow

*Proposed.* One GTD entry in any list. Replaces today's title-and-ref entry.

Slots, left to right: waits-on strip (`strip-card`, `w-*`), StateGlyph, changed dot, title (`row-title`, one line), meta line (the ask, project, contexts, SourceChip), date, focus star. Dates: a deadline in `.when.soon` from one working day before, `.when.over` past it; a snooze "until Tue"; a waiting age. The star is `.on` when set by you, `.auto` (70% `late`) when set by staleness or deadline.

## Consumer provides
The entry (state, title, contexts, project, deadline, snooze, focus, changed), its first item's ask and asker, its first unit's card for the chip.

## Do / don't
- Do show the ask in words ("dana asks you"), not the item id.
- Don't show more than two contexts; `+n` beyond.
