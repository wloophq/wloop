# TimelineRow

One fact of a task's timeline, as the daemon folds its events by the profile's `[facts]` of `timeline.toml` (`GET /api/tasks/<id>/timeline?as=nodes`). The task page's Timeline tab lists them newest first in an `ol.tl`, narrowed by kind with chips above it.

A `li.ev` is a three-column grid: the time in `code` and `faint`, right-aligned (the time alone on the day it is read, else the date and time, through `fmtDate`); a hairline `rule` running down with a 9px dot on it; then the body. The dot carries the fact's served tone: `loop` rings it in `loop`, `me` fills it with `me`, `people` rings it in `reviewer`, none rings it in `faint`, and a span still running (`running`) draws its ring dashed. The body is the fact's label in `body`, then the prose of its newest event that carries some, as markdown in `muted` folded after three blocks, then, when the fact folds more than one event, a `details` in `faint` whose summary counts them ("11 events") and whose `ol.raw` lists each one: its time, its kind's words with its sub, and who did it.

The kind chips are `chip`s, with "all" first and the kinds the timeline holds after it, in their served words; a pressed chip is `on`. The pull request's own events keep the unit Timeline.

## Consumer provides
The served nodes, each with its label, tone, start, end, running flag, actor and events, and the kinds seen so far.

## Do / don't
- Do take the label, the tone and the grouping from the served node; the client groups nothing.
- Do show an event no fact took as a node of its own, labeled by its kind's words or its raw kind.
- Don't build a second feed: the timeline is the task's one event log.
