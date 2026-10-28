# TimelineRow

One event of a task's timeline, read as a chat message. The task page lists them oldest first in an `ol.chat`, narrowed by kind with chips above it.

A `li.msg` on `bg` in a `radius-md` box: a `meta` line with the actor in 600 weight, the kind in `muted` (underscores read as spaces) and the time in `code`; then the event's text in `body` when it has one. Your own messages (`by-person`) sit on the right on `sel`; the agent's, a workflow's and a plugin's sit on the left.

The kind chips are `chip`s, with "all" first and the kinds the timeline holds after it; a pressed chip is `on`. The pull request's own events keep the unit Timeline.

## Consumer provides
The task's events with their kind, sub-kind, actor, time and data, and the kinds seen so far.

## Do / don't
- Do read the text from the event's data; an event with none shows only its line.
- Don't build a second feed: the timeline is the task's one event log.
