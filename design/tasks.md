# Tasks and sources

wloop is a task list whose inbox fills itself. The tasks are yours; the units they point at belong to the plugins that brought them in (GitHub, Slack, and the core's sessions and workspaces). Since Slack joined GitHub as a first-class source, the interface lost track of that split: tasks look like pull request rows in some views and like plain text in others, and nothing on screen says which source a task came from or what inside that source it is about. This section sets the visual model, taking its list vocabulary from OmniFocus and Nirvana, the two references wloop's lists already follow.

## Three layers, three cues

Every task row stacks three objects. Each gets one visual channel, and no channel is shared.

| Layer | Object | Question it answers | Cue |
|---|---|---|---|
| **Commitment** | Task | what *I* decided to do about it | **shape**: the state glyph at the leading edge |
| **Ask** | Ask | what someone needs, from whom | **words**: "dana asks you", with the quote it came from |
| **Source** | Unit, from a provider | where it lives | **label**: the neutral source chip, `PR acme/web#412`, `Thread #platform`, `DM lee` |

Color stays where it was: the waits-on hues say **whom** the task waits on, and `late` and `err` say **when**. A provider never gets a hue. GitHub and Slack are told apart by their chip, so a Slack thread waiting on you reads exactly as urgent as a pull request waiting on you.

## The state glyph

The leading glyph is the category of the task's state, drawn like OmniFocus's status circle, and it is the target that completes the task. The profile names the states and puts each in one of seven categories; the glyph draws the category, so a state a profile adds needs no new glyph, and the state's own label is the glyph's name and the word of the state pill.

| Category | Bundled states | Glyph | Notes |
|---|---|---|---|
| `inbox` | `inbox` | dashed circle | not clarified yet |
| `ready` | `next` | circle | an action you will do |
| `active` | | circle with a centre dot | under way |
| `waiting` | `waiting` | half-filled circle in the waiter's hue | someone else owes it |
| `deferred` | `scheduled`, `someday` | dotted circle, faded | asleep until a date, an event or a later look |
| `done` | `done`, `reference` | filled `loop` circle with a check | finished or kept to read; the row goes `muted` |
| `dropped` | `trash` | circle with a slash | dismissed |

An agent's question, a draft in the Outbox and a proposal are **gates** on a task: the task keeps its glyph, and the gate shows on its page as a GateCard and in the views that list gates. A **project** is a task that is the parent of others; its glyph is its own state's.

## The task row

```
| ○  • Approve the cache TTL change          @decide   PR acme/web#412   Fri   ★ |
|      dana asks you · ? question · Payments project                          |
```

1. **Strip**: whom the task waits on (`strip-card`). A task whose next step is yours, or with an open gate, waits on `me`; a waiting task on the person; a deferred one on nobody.
2. **State glyph** (`state-glyph`), by category.
3. **Changed dot**: a `me` dot before the title when someone else moved since you looked (Nirvana's and mail's unread mark).
4. **Title** in `row-title`.
5. **Contexts** as `@` chips, at most two, then `+n`.
6. **Source chip**: the first unit; `+n` when the task spans more.
7. **Date**: a deadline as a weekday or date; `late` from one working day before it, `err` past it. A snooze shows "until Tue" in `muted`. Waiting shows the age since the ask.
8. **Focus star** at the trailing edge, `late` when on, outlined on hover when off. An automatic star (stale, deadline) is outlined in `late`, like OmniFocus's inherited flag.

The second line, `meta`, is optional and holds the ask (who asks whom), the parent, and the area when the view is not filtered to one.

## Asks on a task

An ask is what someone needs from you, recorded by the plugin or the extraction that found it: the asker, a one-line summary, the permalink to the message, and the quotes it came from. Asks show on the task's page as an AskList, each quote set off by a `rule` bar so you can judge the ask without opening the thread, and the first ask's asker reads on the row's meta line.

## Sources

The task layer knows no provider. It reads each unit through the generic card every plugin projects (Plugins and profiles has the full list of fields), so a plugin added next month shows up in every list, chip and inspector with no change here.

A source chip is a `hover` tile with the provider's two-letter mark in `flag` mono, the unit kind in `meta`, and `card.label` in `code`. With today's plugins that reads `GH PR acme/web#412`, `SL Thread #platform` or `SL DM lee`; a core session reads `SE Session web-412`. A plugin that is turned off shows its chips dashed and its cards marked stale.

On the task's page each unit is one of its links: the unit's label opens it in the cockpit, and the service's name beside it opens it at the source. In a unit's own detail it renders as a **unit card**: the source chip and a link out, the title, the plugin's state word, the waits-on strip and age, the why sentence, the last human move, and the signals colored by tone. Only these generic slots appear there; the plugin's richer page (a pull request's brief and timeline, a thread's messages) opens from the card's "Open in" link into the plugin's own perspective.

## Lists and perspectives

The lists are the profile's **views**, each a predicate over task facts with a label, an optional grouping and the key that jumps to it, in the order of `views.toml`. The client names none of them: it lists what the catalog serves, groups by the fact a view names (a state or a category groups in the order of the states), and binds each view's key. With the bundled profile they read Focus, Inbox, Outbox, Next, Waiting, Projects, Someday and Logbook; a profile adds its own (a Needs me over open gates and your next actions, say) with no client change.

- The **area switcher** sits at the top of the rail, as in Nirvana, and filters every view; the filter shows in the view's heading ("Next · work").
- **Review** is a mode, not a list: a button above the lists that walks the profile's review steps, each a view, with the step chips across the top.

Saved filters are **perspectives** (OmniFocus's word). Each plugin ships its own (GitHub's Needs me, Stalled, Approved; Slack's Conversations), built on its richer model, and any filter you keep is one too. They live under their plugin in the rail, collapsed by default, and show units, not tasks. From a unit, "Add to Inbox" captures a task pointing at it; from a task, the unit card opens the plugin's perspective on that unit.

The **state glyph** also takes its click from the profile: when an action with the matching verb exists for the task (an intent declaring `verb = "done"`, for instance), completing the task runs it.

Grouping inside a view is the fact its `group` names: a state, a category, a property such as a context, or who owes the next step.

## The task page

The task's detail is its page, in one column (the Inspector component):

1. **Header**: title and star, the state pill with its category, the changed flag, the properties the profile declares, the links, and the task's verbs and intents.
2. **Asks**, with their quotes.
3. **Brief**.
4. **Waiting on you**: a GateCard per open gate, its intents in the gate's order, the first `primary`, `1` to `9` picking one while the card has the focus.
5. **Runs**: a RunCard per run, with Open workspace, Enter session and Transcript.
6. **Timeline** as a chat (TimelineRow), oldest first, narrowed by kind.
7. **Composer**: Send to the task's agent (`primary`), Save as draft beside it.
8. **Children**.

Actions are placed by slot, never by name: verbs in the header, a gate's intents on its card, a run's actions on its card.

## Keys

The core keys stay as they are and appear in each control's tooltip: `n` capture, `s` snooze, `w` waiting, `p` project, `a` approve, `d` delegate, `r` reply. The key that jumps to a view is the view's own, from `views.toml` (`g i` Inbox with the bundled profile), and the digits pick a gate's intents in the gate's order.
