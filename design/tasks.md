# Tasks and sources

wloop is a task list whose inbox fills itself. The tasks are yours; the units they point at belong to the plugins that brought them in (GitHub, Slack, and the core's sessions and workspaces). Since Slack joined GitHub as a first-class source, the interface lost track of that split: tasks look like pull request rows in some views and like plain text in others, and nothing on screen says which source a task came from or what inside that source it is about. This section sets the visual model, taking its list vocabulary from OmniFocus and Nirvana, the two references wloop's lists already follow.

## Three layers, three cues

Every task row stacks three objects. Each gets one visual channel, and no channel is shared.

| Layer | Object | Question it answers | Cue |
|---|---|---|---|
| **Commitment** | Task | what *I* decided to do about it | **shape**: the state glyph at the leading edge |
| **Ask** | Item | what someone needs, from whom | **glyph and words**: the item kind and "dana asks you" |
| **Source** | Unit, from a provider | where it lives | **label**: the neutral source chip, `PR acme/web#412`, `Thread #platform`, `DM lee` |

Color stays where it was: the waits-on hues say **whom** the task waits on, and `late` and `err` say **when**. A provider never gets a hue. GitHub and Slack are told apart by their chip, so a Slack thread waiting on you reads exactly as urgent as a pull request waiting on you.

## The state glyph

The leading glyph is the task's state, drawn like OmniFocus's status circle, and it is the target that completes the task.

| State | Glyph | Notes |
|---|---|---|
| `inbox` | dashed circle | not clarified yet |
| `next` | circle | an action you will do |
| `waiting` | half-filled circle in the waiter's hue | someone else owes it |
| `scheduled` | circle with a centre dot | snoozed until a date or activity |
| `someday` | dotted circle, faded | maybe later |
| `reference` | square | kept to read, no action |
| `done` | filled `loop` circle with a check | finished; the row goes `muted` |
| `trash` | circle with a slash | dismissed |

Four task kinds replace the glyph, because they are not commitments yet:

- **message** (an agent's question, an `inbox_post`, a proposal): a filled `me` circle with `?`. It needs a decision, so its row shows the options inline.
- **draft** (Outbox): a square with `↗`. It is something leaving, not something to do.
- **project**: a square with three bars; parallel or sequential is a word on the meta line.
- **close**: the `next` circle plus the word "close" on the meta line.

## The task row

```
| ○  • Approve the cache TTL change          @decide   PR acme/web#412   Fri   ★ |
|      dana asks you · ? question · Payments project                          |
```

1. **Strip**: whom the task waits on (`strip-card`). Inbox, next and messages wait on `me`; waiting tasks on the person; scheduled and someday on nobody.
2. **State glyph** (`state-glyph`).
3. **Changed dot**: a `me` dot before the title when someone else moved since you looked (Nirvana's and mail's unread mark).
4. **Title** in `row-title`.
5. **Contexts** as `@` chips, at most two, then `+n`.
6. **Source chip**: the first unit; `+n` when the task spans more.
7. **Date**: a deadline as a weekday or date; `late` from one working day before it, `err` past it. A snooze shows "until Tue" in `muted`. Waiting shows the age since the ask.
8. **Focus star** at the trailing edge, `late` when on, outlined on hover when off. An automatic star (stale, deadline) is outlined in `late`, like OmniFocus's inherited flag.

The second line, `meta`, is optional and holds the ask (who asks whom, item kind), the project, and the area when the view is not filtered to one.

## Items inside a task

An item is shown only in the task's detail and in the hover of the source chip, never as its own row. Each item line: kind glyph (`→` request, `?` question, `◇` decision, `□` todo), the text, who asked and who acts next, and open or resolved. A resolved item is struck through in `muted` with a `✓`. A confirmed item carries a small `confirmed` flag; an extraction that disagrees with it shows as a message with Accept and Reject.

## Sources

The task layer knows no provider. It reads each unit through the generic card every plugin projects (Plugins and profiles has the full list of fields), so a plugin added next month shows up in every list, chip and inspector with no change here.

A source chip is a `hover` tile with the provider's two-letter mark in `flag` mono, the unit kind in `meta`, and `card.label` in `code`. With today's plugins that reads `GH PR acme/web#412`, `SL Thread #platform` or `SL DM lee`; a core session reads `SE Session web-412`. A plugin that is turned off shows its chips dashed and its cards marked stale.

In the detail panel each unit renders as a **unit card**: the source chip and a link out, the title, the plugin's state word, the waits-on strip and age, the why sentence, the last human move, and the signals colored by tone. Only these generic slots appear here; the plugin's richer page (a pull request's brief and timeline, a thread's messages) opens from the card's "Open in" link into the plugin's own perspective. The cards sit right under the inspector fields, above the actions, because they are the evidence the decision rests on.

## Lists and perspectives

The lists are one state each, as in Nirvana, and keep its order and keys:

**Focus**, **Inbox**, **Next**, **Waiting**, **Scheduled**, **Someday**, **Projects**, **Reference**, **Logbook**.

- **Scheduled** becomes a view (the state exists but has no view today). Like OmniFocus's Forecast it is an agenda grouped by day: snoozes returning, deadlines, waiting tasks resurfacing, approval windows closing.
- **Reference** leaves the Logbook, which keeps only `done`.
- The **area switcher** sits at the top of the rail, as in Nirvana. It filters every view but the Inbox, and the filter shows in the view's heading ("Next · work").
- **Outbox** sits beside the Inbox under Decide: both need you to say yes.
- **Review** is a mode, not a list: a button above the lists that walks the six steps with the step chips across the top.

Saved filters are **perspectives** (OmniFocus's word). Each plugin ships its own (GitHub's Needs me, Stalled, Approved; Slack's Conversations), built on its richer model, and any filter you keep is one too. They live under their plugin in the rail, collapsed by default, and show units, not tasks. From a unit, "Add to Inbox" captures a task pointing at it; from a task, the unit card opens the plugin's perspective on that unit.

The **state glyph** also takes its click from the profile: when an action with the matching verb exists for the task (an intent declaring `verb = "done"`, for instance), completing the task runs it.

Grouping inside any list is one of: none, project, source, person, context, due. Waiting defaults to person, with the person's oldest age and a Nudge on the group head.

## The inspector

The task's detail follows OmniFocus's inspector: fields, not a wall of buttons.

1. **Head**: state glyph, title (editable), star.
2. **State**: one segmented control, Inbox, Next, Waiting, Scheduled, Someday, Reference, plus Done and Trash as their own buttons. Choosing Waiting asks for the person; Scheduled for the date or activity.
3. **Fields**, label in `muted` on the left: Project, Contexts, Area, Deadline, Snooze, Waiting on (person, since, last nudge, resurface). Empty fields show as an `add` link so the inspector keeps its shape.
4. **Items**.
5. **Sources**: one unit card per unit.
6. **Actions**, placed by slot, never by name: the daemon's recommended action as `primary`; actions whose verb is `delegate` in the Delegate block with their guidance input; `approve` in the question or draft block; the rest clustered by group. With the bundled profile that reads Draft for me, Hand to an agent and Reply; a profile that adds an intent with a `delegate` verb adds its button there.
7. **Notes**, then **History**.

Projects add Sequential or Parallel, the review interval with Mark reviewed, and their actions in order.

## Keys

The keys stay as they are and appear in each control's tooltip: `n` capture, `s` snooze, `w` waiting, `p` project, `a` approve, `d` delegate, `r` reply, `g r` review.
