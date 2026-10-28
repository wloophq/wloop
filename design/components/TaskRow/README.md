# TaskRow

*Proposed.* One task in any list. Replaces today's title-and-ref row.

Slots, left to right: waits-on strip (`strip-card`, `w-*`), StateGlyph of the state's category, changed dot, title (`row-title`, one line), meta line (the ask, the parent, contexts, SourceChip), date, focus star. Dates: a deadline in `.when.soon` from one working day before, `.when.over` past it; a snooze "until Tue"; a waiting age. The star is `.on` when set by you, `.auto` (70% `late`) when set by staleness or deadline.

## Consumer provides
The task (state and its category, title, its properties such as contexts, deadline and focus, its parent, its wake, changed), its first ask's asker, its first unit's card for the chip.

## Do / don't
- Do show the ask in words ("dana asks you"), not an id.
- Don't show more than two contexts; `+n` beyond.
