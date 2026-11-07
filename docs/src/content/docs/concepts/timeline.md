---
title: The timeline
description: How a task's events become the few facts its Timeline tab shows, and how a profile words, tones and groups them.
---

A task's timeline is every event of the task and of the units it holds, kept forever (see [The timeline and the records](/concepts/tasks/#the-timeline-and-the-records)). A round of an agent alone records a dozen events: the turn sent, delivered, started, the records it wrote, the turn ended. The task page's Timeline tab shows them folded into facts, newest first: "Agent round, 7m", "dana reviewed: approved", "Moved: Next". Each fact folds its raw events under a count ("11 events") that opens to list them, and shows the prose of its newest event that carries some, such as an agent's message, as markdown.

```
events, oldest first                      facts, newest first
turn_sent        09:39  ─┐
turn_started     09:40   │  [facts.round]  ──►  09:39  Agent round, 7m    (4 events)
record           09:41   │
turn_ended       09:46  ─┘
gone_kind        11:00     no fact          ──►  11:00  gone_kind         (1 event)
review APPROVED  14:00  ─┐ [facts.review]  ──►  14:00  dana reviewed: approved
comment          14:00  ─┘
```

The daemon folds them (`GET /api/tasks/<id>/timeline?as=nodes`); without `as`, the route answers the raw events, as the kind chips and other clients read them.

## Words and tones

`timeline.toml` words each of the core's event kinds and gives it a tone; a plugin words its own kinds in its `ui.toml`, under `[events.<kind>]`:

```toml
[events.held]
label = "paused"
tone = "me"
```

The tone is what the timeline's dot and the Recent view's kind are drawn in: `loop` for the loop's and an agent's doing, `me` for yours, `people` for someone else's, none otherwise. The Recent view's "Loop only" filter keeps the `loop` events; a plugin event marked `loop = true` that sets no tone is `loop`.

## Facts

A `[facts.<name>]` folds events into one fact:

```toml
[facts.round]
start   = ["turn_sent", "turn_started"]
end     = ["turn_ended", "turn_stopped", "turn_withdrawn"]
absorb  = ["turn_delivered", "record", "late_record"]
label   = "Agent round, {{.duration}}"
running = "Agent round running"
tone    = "loop"
order   = 10
```

- `start`, required: the kinds that open the fact. With `end` set the fact is a span, open until one of its `end` kinds comes; without it, each start is a fact of its own.
- `absorb`: the kinds that join the open span, or the fact just before them when it is this one, as a comment joins the review it follows. A start kind that comes while the span is open joins it too.
- `label`: how the fact reads, over `{{.actor}}` (who did its first event), `{{.duration}}` (a closed span's, in the row words' `age_*`), `{{.count}}` (its events), `{{.event}}` (its first event's words) and `{{.sub}}` (its first event's short value: a record's kind, else its data as its kind's `data` shows it, such as a review's verdict). Its first event's words when absent.
- `running`: how a span with no end yet reads; the label when absent. A running span has no duration.
- `subs`: the words of the values `{{.sub}}` reads, such as `APPROVED = "approved"`; a value it leaves out reads as itself.
- `tone`: the fact's tone, its first event's when absent.
- `order`: where the fact is tried among the others, lowest first, name order breaking a tie. An event goes to the first open span that ends or takes it, then to the fact just before it, then opens the first fact that starts with it.

Every kind a fact names is a core kind, a kind a registered plugin declares in its manifest's `[[source.events]]`, or a task verb's event, `verb_<verb>`; any other is a load problem naming the key, and the fact keeps the lower layer's. A layer's fact replaces the one of its name whole, so a plugin's layer adds its own facts (the GitHub plugin folds review requests, reviews, pushes and the opening) and your profile can replace any.

No event is dropped: an event no fact takes is a fact of its own, labeled by its kind's words, or by the kind itself when no layer words it, as a plugin's kind once the plugin is removed.

The labels, the running texts and the `subs` words are texts a person reads, translated under `locales/<tag>/timeline.toml` (see [Translate the interface](/guides/localization/)).
